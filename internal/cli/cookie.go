package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/HanamiSakiP/u-game/internal/browser"
	"github.com/HanamiSakiP/u-game/internal/remote"
)

// cmdCookie 管理 SteamDB Cookie：用 Firefox 获取、查看、定位和删除。
func (a *app) cmdCookie(args []string) error {
	fs := flag.NewFlagSet("cookie", flag.ContinueOnError)
	fs.SetOutput(a.err)
	printCookie := fs.Bool("print", false, "获取后输出 Cookie 内容")
	browserTimeout := fs.Duration("browser-timeout", browser.DefaultTimeout, "浏览器验证最长等待时间")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal cookie [status|get|show|path|clear] [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	sub := "status"
	if len(rest) > 0 {
		sub = strings.ToLower(rest[0])
		rest = rest[1:]
	}

	switch sub {
	case "status", "check":
		if len(rest) > 0 {
			return errors.New("cookie status 不接受额外参数")
		}
		return a.cookieStatus()
	case "get", "fetch", "browser":
		if len(rest) > 1 {
			return errors.New("cookie get 最多接受一个 SteamID")
		}
		steamID := ""
		if len(rest) == 1 {
			steamID = rest[0]
		}
		return a.cookieGet(steamID, *printCookie, *browserTimeout)
	case "show", "print":
		if len(rest) > 0 {
			return errors.New("cookie show 不接受额外参数")
		}
		return a.cookieShow()
	case "path":
		path, _, ok := a.savedCookie()
		if !ok {
			path = a.store.CookiePath()
		}
		fmt.Fprintln(a.out, path)
		return nil
	case "clear", "remove", "delete":
		return a.cookieClear()
	default:
		return fmt.Errorf("未知 cookie 子命令 %q，可选 status、get、show、path、clear", sub)
	}
}

func (a *app) cookieStatus() error {
	path, data, ok := a.savedCookie()
	fmt.Fprintf(a.out, "数据目录   : %s\n", a.store.Dir)
	fmt.Fprintf(a.out, "Cookie 文件: %s\n", path)
	if ok {
		text := strings.TrimSpace(string(data))
		fmt.Fprintf(a.out, "状态       : 已保存（%d 字节）\n", len(text))
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(a.out, "修改时间   : %s\n", info.ModTime().Format("2006-01-02 15:04:05"))
		}
	} else {
		fmt.Fprintln(a.out, "状态       : 未找到")
	}
	fmt.Fprintf(a.out, "User-Agent : %s\n", orPlaceholder(a.cfg.UserAgent, "默认"))
	fmt.Fprintln(a.out, "提示       : 运行 `ugal cookie get` 用 Firefox 完成验证并保存 Cookie")
	return nil
}

func (a *app) cookieShow() error {
	path, data, ok := a.savedCookie()
	if !ok {
		return errors.New("还没有保存 Cookie，请先运行 `ugal cookie get`")
	}
	fmt.Fprintf(a.err, "Cookie 文件: %s\n", path)
	fmt.Fprintln(a.out, strings.TrimSpace(string(data)))
	return nil
}

func (a *app) cookieClear() error {
	removed := 0
	for _, path := range a.store.CookieFiles() {
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("删除 %s 失败: %w", path, err)
		}
		fmt.Fprintf(a.out, "已删除 %s\n", path)
		removed++
	}
	if removed == 0 {
		fmt.Fprintln(a.out, "没有需要删除的 Cookie 文件")
	}
	return nil
}

func (a *app) cookieGet(steamID string, printCookie bool, browserTimeout time.Duration) error {
	steamID = strings.TrimSpace(steamID)
	if steamID == "" {
		steamID = a.cfg.SteamID
	}
	targetURL := "https://steamdb.info/"
	if id := remote.CanonicalSteamID(steamID); id != "" {
		targetURL = remote.SteamDBCalculatorURL(id)
	}

	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(a.err, format+"\n", args...)
	}
	fmt.Fprintf(a.err, "浏览器验证: 正在启动 Firefox，请在窗口中打开 %s\n", targetURL)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := browser.Verify(ctx, targetURL, browser.Options{
		Timeout:        browserTimeout,
		StopWhenLoaded: true,
		Logf:           logf,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errors.New("已取消 Cookie 获取")
		}
		return err
	}
	if strings.TrimSpace(res.Cookies) == "" {
		return errors.New("页面已加载，但没有读取到 SteamDB Cookie（可能当前网络无需验证）")
	}
	if err := a.saveBrowserCredentials(res, logf); err != nil {
		return err
	}

	fmt.Fprintf(a.out, "Cookie 文件: %s\n", a.store.CookiePath())
	fmt.Fprintf(a.out, "Cookie 长度: %d 字节\n", len(strings.TrimSpace(res.Cookies)))
	if printCookie {
		fmt.Fprintln(a.out, res.Cookies)
	}
	return nil
}

// savedCookie 返回第一个非空的 Cookie 文件路径与内容。
func (a *app) savedCookie() (string, []byte, bool) {
	for _, path := range a.store.CookieFiles() {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			return path, data, true
		}
	}
	return a.store.CookiePath(), nil, false
}
