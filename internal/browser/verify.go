// Package browser 通过本机 Firefox 的 Marionette 协议完成需要人工交互的
// 网页验证（例如 SteamDB 的 Cloudflare 验证），并返回验证后的页面内容。
//
// 验证窗口使用独立的临时 Firefox 配置目录，不会读取或修改用户日常浏览器的
// 资料；浏览器关闭后临时目录会被删除。
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout 是等待人工完成验证的默认时长。
	DefaultTimeout = 3 * time.Minute
	startupTimeout = 30 * time.Second
	stopTimeout    = 5 * time.Second
)

// Options 控制一次浏览器验证。
type Options struct {
	// Timeout 是等待人工完成验证的最长时间，默认 3 分钟。
	Timeout time.Duration
	// Headless 为 true 时使用无头模式（仅用于测试；无头模式通常无法通过验证）。
	Headless bool
	// StopWhenLoaded 为 true 时，只要页面加载完成且没有验证页就立即返回，
	// 不要求页面中出现游戏表格（用于只获取 Cookie）。
	StopWhenLoaded bool
	// Firefox 指定 Firefox 可执行文件；为空时自动查找。
	Firefox string
	// Logf 输出进度，可为空。
	Logf func(format string, args ...interface{})
}

// Result 是浏览器验证后的页面信息。
type Result struct {
	URL       string
	HTML      string
	Cookies   string
	UserAgent string
}

// Verify 用 Firefox 打开 targetURL，等待 Cloudflare 等验证通过后返回页面内容。
func Verify(ctx context.Context, targetURL string, opts Options) (*Result, error) {
	opts = opts.normalized()
	if strings.TrimSpace(targetURL) == "" {
		return nil, errors.New("缺少要验证的页面地址")
	}

	ff, err := findFirefox(opts.Firefox)
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "ugal-firefox-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时 Firefox 配置目录失败: %w", err)
	}
	defer os.RemoveAll(profile)
	if err := writeProfilePrefs(profile); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("获取本地调试端口失败: %w", err)
	}
	if err := appendProfilePort(profile, port); err != nil {
		return nil, err
	}

	proc, err := startFirefox(ff, profile, opts.Headless)
	if err != nil {
		return nil, err
	}
	defer proc.stop()

	client, err := waitMarionette(ctx, profile, port, proc)
	if err != nil {
		return nil, err
	}
	defer func() {
		quitMarionette(client)
		client.close()
	}()

	userAgent, err := newSession(ctx, client, opts.Timeout)
	if err != nil {
		return nil, err
	}
	setTimeouts(ctx, client, opts.Timeout)
	if err := installStealthAddon(ctx, client, profile); err != nil {
		logf(opts, "警告: 无法加载浏览器辅助脚本（%v），Cloudflare 验证页可能反复要求重试", err)
	}

	logf(opts, "已打开 Firefox，请在浏览器窗口中完成验证（最长等待 %s）…", opts.Timeout)
	if err := navigate(ctx, client, targetURL, opts.Timeout); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 页面可能仍在加载验证脚本，继续轮询，避免过早失败。
		logf(opts, "警告: 页面加载未完成（%v），继续等待验证结果…", err)
	}

	deadline := time.Now().Add(opts.Timeout)
	challengeLogged := false
	// 给验证脚本一点启动时间，避免刚导航完就执行脚本造成干扰。
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	for {
		state, err := readPageState(ctx, client)
		if err != nil {
			return nil, fmt.Errorf("浏览器会话中断（窗口可能已关闭）: %w", err)
		}
		switch {
		case isPrivateProfile(state.Title, state.Text):
			return nil, errors.New("Steam 资料是私密的，SteamDB 无法读取游戏列表")
		case isChallengePage(state.Title, state.Text):
			if !challengeLogged {
				logf(opts, "检测到 Cloudflare 验证页，请在 Firefox 窗口中点击验证…")
				challengeLogged = true
			}
		case state.Rows > 0:
			// 表格已经出现，稍等片刻让名称等异步内容渲染完成。
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			return collectResult(ctx, client, targetURL, userAgent)
		case opts.StopWhenLoaded && state.Ready == "complete":
			// 只需要 Cookie 时不等待游戏表格，页面加载完成即可返回。
			return collectResult(ctx, client, targetURL, userAgent)
		}

		if time.Now().After(deadline) {
			if isChallengePage(state.Title, state.Text) {
				return nil, fmt.Errorf("等待浏览器验证超时（%s）：Cloudflare 验证页仍未通过；可以重新运行，或用 --cookie-file 提供浏览器导出的 Cookie", opts.Timeout)
			}
			logf(opts, "警告: 未检测到游戏表格，尝试直接解析当前页面")
			return collectResult(ctx, client, targetURL, userAgent)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

type pageState struct {
	Title string `json:"title"`
	Ready string `json:"ready"`
	URL   string `json:"url"`
	Rows  int    `json:"rows"`
	Text  string `json:"text"`
}

const pageStateScript = `var title = document.title || "";
var body = document.body;
var text = body ? (body.textContent || "") : "";
return JSON.stringify({
  title: title.slice(0, 200),
  ready: document.readyState || "",
  url: location.href || "",
  rows: document.querySelectorAll("tr[data-appid], [data-appid]").length,
  text: text.slice(0, 300)
});`

func readPageState(ctx context.Context, client *marionetteClient) (*pageState, error) {
	raw, err := client.executeString(ctx, pageStateScript)
	if err != nil {
		return nil, err
	}
	var state pageState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, fmt.Errorf("解析页面状态失败: %w", err)
	}
	return &state, nil
}

func collectResult(ctx context.Context, client *marionetteClient, targetURL, userAgent string) (*Result, error) {
	html, err := client.executeString(ctx, "return document.documentElement ? document.documentElement.outerHTML : '';")
	if err != nil {
		return nil, fmt.Errorf("读取页面内容失败: %w", err)
	}
	if strings.TrimSpace(html) == "" {
		return nil, errors.New("浏览器中的页面内容为空")
	}
	if ua, err := client.executeString(ctx, "return navigator.userAgent;"); err == nil && strings.TrimSpace(ua) != "" {
		userAgent = ua
	}
	res := &Result{URL: targetURL, HTML: html, UserAgent: userAgent}
	if cookies, err := client.cookies(ctx); err == nil {
		res.Cookies = cookieHeader(cookies, "steamdb.info")
	}
	return res, nil
}

func isChallengePage(title, text string) bool {
	joined := strings.ToLower(title + "\n" + text)
	for _, marker := range []string{
		"just a moment",
		"checking your browser",
		"verify you are human",
		"cf-challenge",
		"cf_chl_opt",
		"challenge-platform",
		"请稍候",
		"正在验证",
		"验证您",
	} {
		if strings.Contains(joined, marker) {
			return true
		}
	}
	return false
}

func isPrivateProfile(title, text string) bool {
	joined := strings.ToLower(title + "\n" + text)
	return strings.Contains(joined, "profile is private") || strings.Contains(joined, "this profile is private")
}

func cookieHeader(cookies []webdriverCookie, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	var pairs []string
	for _, c := range cookies {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.Domain)), ".")
		if domain != "" && domain != host && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		pairs = append(pairs, name+"="+c.Value)
	}
	return strings.Join(pairs, "; ")
}

func (o Options) normalized() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	return o
}

func logf(opts Options, format string, args ...interface{}) {
	if opts.Logf != nil {
		opts.Logf(format, args...)
	}
}

func findFirefox(explicit string) (string, error) {
	if p := strings.TrimSpace(explicit); p != "" {
		return lookFirefox(p)
	}
	if p := strings.TrimSpace(os.Getenv("UGAL_FIREFOX")); p != "" {
		return lookFirefox(p)
	}
	for _, name := range []string{"firefox", "firefox-esr", "firefox-bin"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Firefox.app/Contents/MacOS/firefox"}
	case "windows":
		for _, base := range []string{
			os.Getenv("ProgramFiles"),
			os.Getenv("ProgramFiles(x86)"),
			os.Getenv("LOCALAPPDATA"),
		} {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, "Mozilla Firefox", "firefox.exe"))
			}
		}
	case "linux":
		candidates = []string{
			"/usr/lib/firefox/firefox",
			"/usr/lib64/firefox/firefox",
			"/opt/firefox/firefox",
			"/snap/bin/firefox",
		}
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("未找到 Firefox；请安装 Firefox，或用 UGAL_FIREFOX 指定可执行文件路径")
}

func lookFirefox(s string) (string, error) {
	if p, err := exec.LookPath(s); err == nil {
		return p, nil
	}
	if fi, err := os.Stat(s); err == nil && !fi.IsDir() {
		return s, nil
	}
	return "", fmt.Errorf("无法执行 Firefox %q: 文件不存在", s)
}

func writeProfilePrefs(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建 Firefox 配置目录失败: %w", err)
	}
	prefs := []string{
		`user_pref("browser.shell.checkDefaultBrowser", false);`,
		`user_pref("browser.startup.page", 0);`,
		`user_pref("browser.startup.homepage", "about:blank");`,
		`user_pref("browser.aboutwelcome.enabled", false);`,
		`user_pref("browser.tabs.warnOnClose", false);`,
		`user_pref("datareporting.policy.dataSubmissionEnabled", false);`,
		`user_pref("toolkit.telemetry.enabled", false);`,
		`user_pref("app.update.auto", false);`,
		`user_pref("app.update.enabled", false);`,
	}
	content := strings.Join(prefs, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("写入 Firefox 配置失败: %w", err)
	}
	return nil
}

func appendProfilePort(dir string, port int) error {
	f, err := os.OpenFile(filepath.Join(dir, "user.js"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入 Firefox 端口配置失败: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "user_pref(\"marionette.port\", %d);\n", port); err != nil {
		return fmt.Errorf("写入 Firefox 端口配置失败: %w", err)
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type firefoxProcess struct {
	cmd    *exec.Cmd
	stderr *tailBuffer
	done   chan struct{}
	err    error
}

func startFirefox(bin, profile string, headless bool) (*firefoxProcess, error) {
	args := []string{"--profile", profile, "--no-remote", "--new-instance"}
	if headless {
		args = append(args, "--headless")
	}
	args = append(args, "--marionette", "about:blank")

	cmd := exec.Command(bin, args...)
	cmd.Stdout = io.Discard
	stderr := &tailBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 Firefox 失败: %w", err)
	}
	p := &firefoxProcess{cmd: cmd, stderr: stderr, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *firefoxProcess) exited() bool {
	if p == nil {
		return true
	}
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *firefoxProcess) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if !p.exited() {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(stopTimeout):
	}
}

func waitMarionette(ctx context.Context, profile string, fallbackPort int, proc *firefoxProcess) (*marionetteClient, error) {
	deadline := time.Now().Add(startupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if proc.exited() {
			detail := strings.TrimSpace(proc.stderr.String())
			if detail == "" {
				detail = "Firefox 进程已退出"
			}
			return nil, fmt.Errorf("Firefox 启动失败: %s", truncateText(detail, 1200))
		}
		port := readActivePort(profile)
		if port == 0 {
			port = fallbackPort
		}
		dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		client, err := dialMarionette(dialCtx, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		cancel()
		if err == nil {
			return client, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("等待 Firefox 调试端口超时: %v", lastErr)
}

func readActivePort(profile string) int {
	data, err := os.ReadFile(filepath.Join(profile, "MarionetteActivePort"))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

func newSession(ctx context.Context, client *marionetteClient, timeout time.Duration) (string, error) {
	raw, err := client.call(ctx, "WebDriver:NewSession", map[string]interface{}{
		"capabilities": map[string]interface{}{},
	}, timeout)
	if err != nil {
		return "", err
	}
	var payload struct {
		SessionID    string `json:"sessionId"`
		Capabilities struct {
			UserAgent string `json:"userAgent"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("解析 Firefox 会话失败: %w", err)
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return "", errors.New("Firefox 未返回有效会话")
	}
	return strings.TrimSpace(payload.Capabilities.UserAgent), nil
}

func setTimeouts(ctx context.Context, client *marionetteClient, timeout time.Duration) {
	pageLoad := int(timeout.Milliseconds())
	if pageLoad < 60000 {
		pageLoad = 60000
	}
	_, _ = client.call(ctx, "WebDriver:SetTimeouts", map[string]interface{}{
		"implicit": 0,
		"pageLoad": pageLoad,
		"script":   60000,
	}, 10*time.Second)
}

func navigate(ctx context.Context, client *marionetteClient, targetURL string, timeout time.Duration) error {
	_, err := client.call(ctx, "WebDriver:Navigate", map[string]interface{}{
		"url": targetURL,
	}, timeout+10*time.Second)
	return err
}

func quitMarionette(client *marionetteClient) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_, _ = client.call(ctx, "Marionette:Quit", map[string]interface{}{
		"flags": []string{"eForceQuit"},
	}, stopTimeout)
}

// tailBuffer 只保留最后 max 字节，用于报告 Firefox 启动错误。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if t.max > 0 && len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func truncateText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
