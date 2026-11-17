// Package cover 获取本地已安装游戏的封面，优先使用 Steam 本地缓存，其次从 Steam CDN 下载。
package cover

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

const (
	defaultUA     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	maxCoverBytes = 16 << 20
)

// 默认从这些 Steam CDN 主机依次尝试。
var defaultCDNBases = []string{
	"https://cdn.cloudflare.steamstatic.com",
	"https://cdn.akamai.steamstatic.com",
	"https://steamcdn-a.akamaihd.net",
}

var imageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
}

// Options 控制封面获取行为。
type Options struct {
	// Apps 是需要处理的应用列表，通常来自 installed.json。
	Apps []model.App
	// DestDir 是封面保存目录，一般由 Store.CoversDir() 提供。
	DestDir string
	// SteamRoot 用于查找 Steam 本地封面缓存 appcache/librarycache。
	SteamRoot string
	// Force 为 true 时即使本地已有封面也重新获取。
	Force bool
	// Style 可选 auto、portrait、header、logo。
	Style string
	// Source 可选 auto、local、cdn。
	Source string
	// Concurrency 是并发下载数，默认 4。
	Concurrency int
	// Timeout 是单个 HTTP 请求的超时时间，默认 30 秒。
	Timeout time.Duration
	// Retries 是失败后的重试次数；0 表示不重试，CLI 默认 2。
	Retries int
	// UserAgent 是下载封面时使用的 User-Agent。
	UserAgent string
	// CDNBase 仅用于测试或自定义镜像；为空时使用默认 Steam CDN。
	CDNBase string
	// Logf 接收进度日志，可为 nil。
	Logf func(format string, args ...interface{})
	// Progress 在每个应用处理完成后调用，done 从 1 开始。
	Progress func(done, total int, result Result)
}

// Result 描述单个应用封面的处理结果。
type Result struct {
	AppID   int    `json:"appid"`
	Name    string `json:"name,omitempty"`
	Source  string `json:"source"` // existing、local、cdn、missing、error
	File    string `json:"file,omitempty"`
	URL     string `json:"url,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Retries int    `json:"retries,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Run 为 opts.Apps 获取封面。已有封面在非 Force 模式下会被跳过。
func Run(ctx context.Context, opts Options) ([]Result, error) {
	if strings.TrimSpace(opts.DestDir) == "" {
		return nil, fmt.Errorf("封面保存目录不能为空")
	}
	style, err := normalizeStyle(opts.Style)
	if err != nil {
		return nil, err
	}
	source, err := normalizeSource(opts.Source)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建封面目录 %s 失败: %w", opts.DestDir, err)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Concurrency > 16 {
		opts.Concurrency = 16
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultUA
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	apps := dedupeApps(opts.Apps)
	results := make([]Result, len(apps))
	client := &http.Client{Timeout: opts.Timeout}

	type job struct {
		index int
		app   model.App
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	done := 0
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				result := fetchOne(ctx, client, j.app, opts, style, source, ua, logf)
				results[j.index] = result
				if opts.Progress != nil {
					progressMu.Lock()
					done++
					opts.Progress(done, len(apps), result)
					progressMu.Unlock()
				}
			}
		}()
	}
	for i, app := range apps {
		jobs <- job{index: i, app: app}
	}
	close(jobs)
	wg.Wait()
	return results, nil
}

func fetchOne(ctx context.Context, client *http.Client, app model.App, opts Options, style, source, ua string, logf func(string, ...interface{})) Result {
	res := Result{AppID: app.AppID, Name: app.Name}
	if app.AppID <= 0 {
		res.Source = "error"
		res.Error = "无效的 appid"
		return res
	}

	if existing := Find(opts.DestDir, app.AppID); existing != "" && !opts.Force {
		res.Source = "existing"
		res.File = existing
		if fi, err := os.Stat(existing); err == nil {
			res.Size = fi.Size()
		}
		return res
	}

	if source == "auto" || source == "local" {
		if local := findLocalCover(opts.SteamRoot, app.AppID, style); local != "" {
			dest, err := copyCover(opts.DestDir, app.AppID, local)
			if err != nil {
				res.Source = "error"
				res.Error = fmt.Sprintf("复制本地封面失败: %v", err)
				return res
			}
			res.Source = "local"
			res.File = dest
			res.URL = local
			if fi, err := os.Stat(dest); err == nil {
				res.Size = fi.Size()
			}
			logf("本地缓存: %d %s", app.AppID, app.Name)
			return res
		}
		if source == "local" {
			res.Source = "error"
			res.Error = "Steam 本地缓存中没有找到封面"
			return res
		}
	}

	bases := defaultCDNBases
	if strings.TrimSpace(opts.CDNBase) != "" {
		bases = []string{strings.TrimRight(strings.TrimSpace(opts.CDNBase), "/")}
	}
	attempts := opts.Retries + 1
	var (
		dest   string
		rawURL string
		size   int64
		err    error
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		dest, rawURL, size, err = download(ctx, client, bases, app.AppID, style, opts.DestDir, ua)
		if err == nil {
			res.Retries = attempt - 1
			break
		}
		res.Retries = attempt
		if attempt < attempts {
			logf("重试 %d/%d: %d %s: %v", attempt, opts.Retries, app.AppID, app.Name, err)
		}
	}
	if err != nil {
		res.Retries = attempts - 1
		res.Source = "error"
		res.Error = err.Error()
		return res
	}
	res.Source = "cdn"
	res.File = dest
	res.URL = rawURL
	res.Size = size
	logf("已下载  : %d %s", app.AppID, app.Name)
	return res
}

func download(ctx context.Context, client *http.Client, bases []string, appid int, style, destDir, ua string) (string, string, int64, error) {
	var lastErr error
	for _, base := range bases {
		for _, name := range candidateFiles(style) {
			rawURL := strings.TrimRight(base, "/") + "/steam/apps/" + strconv.Itoa(appid) + "/" + name
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
			if err != nil {
				lastErr = err
				continue
			}
			req.Header.Set("User-Agent", ua)
			req.Header.Set("Accept", "image/avif,image/webp,image/jpeg,image/png,*/*;q=0.8")
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
				continue
			}
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("%s 返回 HTTP %d", rawURL, resp.StatusCode)
				continue
			}
			if len(data) == 0 || len(data) > maxCoverBytes {
				lastErr = fmt.Errorf("%s 返回的内容大小异常", rawURL)
				continue
			}
			ct := strings.ToLower(resp.Header.Get("Content-Type"))
			if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/json") {
				lastErr = fmt.Errorf("%s 返回的不是图片", rawURL)
				continue
			}
			if len(data) < 256 {
				lastErr = fmt.Errorf("%s 返回的内容过小", rawURL)
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext == "" {
				ext = ".jpg"
			}
			dest, err := writeCover(destDir, appid, ext, data)
			if err != nil {
				lastErr = err
				break
			}
			return dest, rawURL, int64(len(data)), nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的封面地址")
	}
	return "", "", 0, fmt.Errorf("下载封面失败: %w", lastErr)
}

func writeCover(destDir string, appid int, ext string, data []byte) (string, error) {
	if !imageExts[strings.ToLower(ext)] {
		ext = ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	dest := filepath.Join(destDir, strconv.Itoa(appid)+strings.ToLower(ext))
	tmp, err := os.CreateTemp(destDir, ".cover-*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("写入封面失败: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", fmt.Errorf("设置封面权限失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("写入封面失败: %w", err)
	}
	_ = os.Remove(dest)
	if err := os.Rename(tmpName, dest); err != nil {
		return "", fmt.Errorf("保存封面失败: %w", err)
	}
	removeOtherCovers(destDir, appid, dest)
	return dest, nil
}

func copyCover(destDir string, appid int, src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		ext = ".jpg"
	}
	return writeCover(destDir, appid, ext, data)
}

// Find 返回封面目录中某个 appid 的封面文件，没有时返回空串。
func Find(dir string, appid int) string {
	if appid <= 0 {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	prefix := strconv.Itoa(appid) + "."
	best := ""
	bestRank := 99
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		ext := "." + strings.TrimPrefix(name, prefix)
		if !imageExts[ext] {
			continue
		}
		rank := extRank(ext)
		if best == "" || rank < bestRank {
			best = filepath.Join(dir, e.Name())
			bestRank = rank
		}
	}
	return best
}

func extRank(ext string) int {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return 0
	case ".png":
		return 1
	case ".webp":
		return 2
	default:
		return 9
	}
}

func removeOtherCovers(dir string, appid int, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := strconv.Itoa(appid) + "."
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(strings.ToLower(e.Name()), prefix) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if filepath.Clean(p) != filepath.Clean(keep) {
			_ = os.Remove(p)
		}
	}
}

func findLocalCover(root string, appid int, style string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	dirs := []string{
		filepath.Join(root, "appcache", "librarycache"),
		filepath.Join(root, "steamapps", "librarycache"),
		filepath.Join(root, "librarycache"),
	}
	wanted := localCandidates(style, appid)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		files := make(map[string]string, len(entries))
		for _, e := range entries {
			if !e.IsDir() {
				files[strings.ToLower(e.Name())] = filepath.Join(dir, e.Name())
			}
		}
		for _, name := range wanted {
			if p, ok := files[strings.ToLower(name)]; ok {
				return p
			}
		}
	}
	return ""
}

func localCandidates(style string, appid int) []string {
	id := strconv.Itoa(appid)
	var stems []string
	switch style {
	case "portrait":
		stems = []string{"_library_600x900", "_library_600x900_2x"}
	case "header":
		stems = []string{"_header", "_capsule_616x353"}
	case "logo":
		stems = []string{"_logo"}
	default:
		stems = []string{
			"_library_600x900", "_library_600x900_2x",
			"_header", "_capsule_616x353",
			"_library_hero", "_logo",
		}
	}
	var out []string
	for _, stem := range stems {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			out = append(out, id+stem+ext)
		}
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		out = append(out, id+ext)
	}
	return out
}

func candidateFiles(style string) []string {
	switch style {
	case "portrait":
		return []string{"library_600x900.jpg", "library_600x900_2x.jpg", "library_600x900.png"}
	case "header":
		return []string{"header.jpg", "capsule_616x353.jpg"}
	case "logo":
		return []string{"logo.png"}
	default:
		return []string{
			"library_600x900.jpg",
			"library_600x900_2x.jpg",
			"header.jpg",
			"capsule_616x353.jpg",
			"logo.png",
		}
	}
}

func dedupeApps(apps []model.App) []model.App {
	seen := make(map[int]bool, len(apps))
	out := make([]model.App, 0, len(apps))
	for _, app := range apps {
		if app.AppID <= 0 || seen[app.AppID] {
			continue
		}
		seen[app.AppID] = true
		out = append(out, app)
	}
	return out
}

func normalizeStyle(style string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "", "auto", "all":
		return "auto", nil
	case "portrait", "cover", "capsule", "竖版":
		return "portrait", nil
	case "header", "banner", "横版":
		return "header", nil
	case "logo", "icon":
		return "logo", nil
	default:
		return "", fmt.Errorf("未知封面样式 %q，可选 auto、portrait、header、logo", style)
	}
}

func normalizeSource(source string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "auto":
		return "auto", nil
	case "local", "cache", "本地":
		return "local", nil
	case "cdn", "download", "network", "下载":
		return "cdn", nil
	default:
		return "", fmt.Errorf("未知封面来源 %q，可选 auto、local、cdn", source)
	}
}
