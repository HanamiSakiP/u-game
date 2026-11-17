package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/system"
)

func (a *app) cmdSystem(args []string) error {
	fs := flag.NewFlagSet("system", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal system [--json]")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("system 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}

	info := system.Detect()
	info.SteamRoot = a.cfg.SteamDir
	if info.SteamRoot == "" {
		if installed, err := a.store.LoadInstalled(); err == nil {
			info.SteamRoot = installed.SteamRoot
		}
	}
	if err := a.store.SaveSystem(info); err != nil {
		return err
	}
	if *asJSON {
		return writeJSONOutput(a.out, info)
	}

	fmt.Fprintf(a.out, "系统        : %s\n", orPlaceholder(info.Platform, "-"))
	fmt.Fprintf(a.out, "发行版      : %s\n", distroLabel(info))
	fmt.Fprintf(a.out, "内核        : %s\n", orPlaceholder(info.Kernel, "-"))
	fmt.Fprintf(a.out, "架构        : %s/%s\n", orPlaceholder(info.OS, "-"), orPlaceholder(info.Arch, "-"))
	fmt.Fprintf(a.out, "桌面环境    : %s\n", orPlaceholder(info.Desktop, "-"))
	fmt.Fprintf(a.out, "会话类型    : %s\n", orPlaceholder(info.SessionType, "-"))
	fmt.Fprintf(a.out, "主机名      : %s\n", orPlaceholder(info.Hostname, "-"))
	fmt.Fprintf(a.out, "Shell       : %s\n", orPlaceholder(info.Shell, "-"))
	fmt.Fprintf(a.out, "语言        : %s\n", orPlaceholder(info.Locale, "-"))
	fmt.Fprintf(a.out, "Steam 目录  : %s\n", orPlaceholder(info.SteamRoot, "（未设置）"))
	fmt.Fprintf(a.out, "数据文件    : %s\n", a.store.SystemPath())
	return nil
}

func distroLabel(info *model.SystemData) string {
	if info == nil || strings.TrimSpace(info.Distro) == "" {
		return "-"
	}
	if strings.TrimSpace(info.DistroVersion) == "" {
		return info.Distro
	}
	return info.Distro + " " + info.DistroVersion
}
