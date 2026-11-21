package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/cover"
	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/steam"
)

func (a *app) cmdCover(args []string) error {
	fs := flag.NewFlagSet("cover", flag.ContinueOnError)
	fs.SetOutput(a.err)
	force := fs.Bool("force", false, "重新获取已有封面")
	style := fs.String("style", "auto", "封面样式: auto、portrait、header、logo")
	source := fs.String("source", "auto", "封面来源: auto、local、cdn")
	concurrency := fs.Int("concurrency", 4, "并发下载数")
	timeout := fs.Duration("timeout", 30*time.Second, "单次下载超时")
	retries := fs.Int("retry", 2, "失败后的重试次数（0 表示不重试）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	listOnly := fs.Bool("list", false, "只查看封面状态，不下载")
	typeFlag := fs.String("type", "game", "类型: game、music、all")
	paths := fs.Bool("paths", false, "显示完整封面路径")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal cover [appid...] [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	typeFilter, err := normalizeTypeFilter(*typeFlag)
	if err != nil {
		return err
	}
	if *retries < 0 {
		return errors.New("--retry 不能为负数")
	}
	ids, err := parseAppIDs(fs.Args())
	if err != nil {
		return err
	}

	owned, installed, err := a.loadData()
	if err != nil {
		return err
	}
	if installed == nil && len(ids) == 0 {
		return errors.New("还没有本地扫描数据，请先运行 `ugal scan`")
	}
	apps := buildCoverTargets(installed, owned, ids, typeFilter)
	if len(apps) == 0 {
		fmt.Fprintln(a.out, "没有需要处理的游戏。")
		return nil
	}

	coverDir := a.store.CoversDir()
	steamRoot := ""
	if installed != nil {
		steamRoot = installed.SteamRoot
	}
	if steamRoot == "" {
		steamRoot = a.cfg.SteamDir
	}

	if *listOnly {
		results := make([]cover.Result, 0, len(apps))
		for _, ap := range apps {
			r := cover.Result{AppID: ap.AppID, Name: ap.Name, Source: "missing"}
			if f := cover.Find(coverDir, ap.AppID); f != "" {
				r.Source = "existing"
				r.File = f
				if fi, err := os.Stat(f); err == nil {
					r.Size = fi.Size()
				}
			}
			results = append(results, r)
		}
		if *asJSON {
			return writeJSONOutput(a.out, newCoverPayload(coverDir, results))
		}
		headers := []string{"APPID", "封面", "大小", "名称", "文件"}
		right := []bool{true, false, true, false, false}
		rows := make([][]string, 0, len(results))
		for _, r := range results {
			file := r.File
			if file == "" {
				file = "-"
			} else if !*paths {
				file = filepath.Base(file)
			}
			state := "有"
			if r.Source == "missing" {
				state = "无"
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", r.AppID),
				state,
				humanSize(r.Size),
				truncateDisplay(orPlaceholder(r.Name, fmt.Sprintf("App %d", r.AppID)), 48),
				file,
			})
		}
		renderTable(a.out, headers, right, rows)
		counts := countCovers(results)
		fmt.Fprintf(a.out, "\n封面目录: %s\n", coverDir)
		fmt.Fprintf(a.out, "已有 %d 个，缺少 %d 个，共 %d 个\n", counts.Existing, counts.Missing, len(results))
		return nil
	}

	results, err := cover.Run(context.Background(), cover.Options{
		Apps:        apps,
		DestDir:     coverDir,
		SteamRoot:   steamRoot,
		Force:       *force,
		Style:       *style,
		Source:      *source,
		Concurrency: *concurrency,
		Timeout:     *timeout,
		Retries:     *retries,
		UserAgent:   a.cfg.UserAgent,
		Progress: func(done, total int, r cover.Result) {
			state := coverSourceLabel(r.Source)
			name := truncateDisplay(orPlaceholder(r.Name, fmt.Sprintf("App %d", r.AppID)), 48)
			line := fmt.Sprintf("[%d/%d] %-6s %d %s", done, total, state, r.AppID, name)
			if r.Retries > 0 {
				line += fmt.Sprintf("（重试 %d 次）", r.Retries)
			}
			if r.Error != "" {
				line += ": " + r.Error
			}
			fmt.Fprintln(a.err, line)
		},
	})
	if err != nil {
		return err
	}
	counts := countCovers(results)
	if *asJSON {
		return writeJSONOutput(a.out, newCoverPayload(coverDir, results))
	}

	fmt.Fprintf(a.out, "封面目录    : %s\n", coverDir)
	fmt.Fprintf(a.out, "目标        : %d 个\n", len(results))
	fmt.Fprintf(a.out, "已存在      : %d 个（已跳过，--force 可重新下载）\n", counts.Existing)
	fmt.Fprintf(a.out, "本地缓存    : %d 个\n", counts.Local)
	fmt.Fprintf(a.out, "CDN 下载    : %d 个\n", counts.CDN)
	if counts.Retries > 0 {
		fmt.Fprintf(a.out, "重试        : %d 次\n", counts.Retries)
	}
	if counts.Failed > 0 {
		fmt.Fprintf(a.out, "失败        : %d 个\n", counts.Failed)
		printed := 0
		for _, r := range results {
			if r.Source != "error" && r.Error == "" {
				continue
			}
			if printed >= 10 {
				fmt.Fprintf(a.out, "  ... 其余 %d 个失败见 --json 输出\n", counts.Failed-printed)
				break
			}
			fmt.Fprintf(a.out, "  - %d %s: %s\n", r.AppID, r.Name, r.Error)
			printed++
		}
	}
	if *paths {
		for _, r := range results {
			if r.File != "" {
				fmt.Fprintf(a.out, "  %d  %s\n", r.AppID, r.File)
			}
		}
	}
	return nil
}

func coverSourceLabel(source string) string {
	switch source {
	case "existing":
		return "跳过"
	case "local":
		return "本地缓存"
	case "cdn":
		return "下载"
	case "missing":
		return "缺少"
	case "error":
		return "失败"
	default:
		return source
	}
}

type coverPayload struct {
	Dir      string         `json:"dir"`
	Total    int            `json:"total"`
	Existing int            `json:"existing"`
	Local    int            `json:"local"`
	CDN      int            `json:"cdn"`
	Missing  int            `json:"missing"`
	Retries  int            `json:"retries"`
	Failed   int            `json:"failed"`
	Covers   []cover.Result `json:"covers"`
}

type coverCounts struct {
	Existing int
	Local    int
	CDN      int
	Missing  int
	Retries  int
	Failed   int
}

func countCovers(results []cover.Result) coverCounts {
	var c coverCounts
	for _, r := range results {
		c.Retries += r.Retries
		switch r.Source {
		case "existing":
			c.Existing++
		case "local":
			c.Local++
		case "cdn":
			c.CDN++
		case "missing":
			c.Missing++
		default:
			if r.Error != "" {
				c.Failed++
			}
		}
	}
	return c
}

func newCoverPayload(dir string, results []cover.Result) coverPayload {
	c := countCovers(results)
	return coverPayload{
		Dir:      dir,
		Total:    len(results),
		Existing: c.Existing,
		Local:    c.Local,
		CDN:      c.CDN,
		Missing:  c.Missing,
		Retries:  c.Retries,
		Failed:   c.Failed,
		Covers:   results,
	}
}

func buildCoverTargets(installed *model.InstalledData, owned *model.OwnedData, ids []int, typeFilter string) []model.App {
	installedMap := make(map[int]model.App)
	names := make(map[int]string)
	if installed != nil {
		for _, ap := range installed.Games {
			old, ok := installedMap[ap.AppID]
			if !ok || (!old.Installed && ap.Installed) || (ap.Name != "" && old.Name == "") {
				installedMap[ap.AppID] = ap
			}
		}
	}
	if owned != nil {
		for _, g := range owned.Games {
			names[g.AppID] = g.Name
		}
	}

	var out []model.App
	if len(ids) > 0 {
		for _, id := range ids {
			ap, ok := installedMap[id]
			if !ok {
				ap = model.App{AppID: id}
			}
			if ap.Name == "" {
				ap.Name = names[id]
			}
			if ap.Name == "" {
				ap.Name = fmt.Sprintf("App %d", id)
			}
			out = append(out, ap)
		}
		return out
	}
	if installed == nil {
		return nil
	}
	for _, ap := range installed.Games {
		if !ap.Installed || steam.IsDefaultHiddenApp(ap.AppID) {
			continue
		}
		appType := ap.Type
		if appType == "" {
			appType = "game"
		}
		if typeFilter != "" && typeFilter != "all" && appType != typeFilter {
			continue
		}
		if ap.Name == "" {
			ap.Name = names[ap.AppID]
		}
		out = append(out, ap)
	}
	return out
}

func parseAppIDs(args []string) ([]int, error) {
	var out []int
	seen := make(map[int]bool)
	for _, arg := range args {
		for _, part := range strings.Split(arg, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.Atoi(part)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("无效的 appid %q", part)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func normalizeTypeFilter(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all", "全部":
		return "all", nil
	case "game", "games", "游戏":
		return "game", nil
	case "music", "soundtrack", "音乐":
		return "music", nil
	default:
		return "", fmt.Errorf("未知类型 %q，可选 game、music、all", raw)
	}
}
