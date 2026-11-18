package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/HanamiSakiP/u-game/internal/browser"
	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/remote"
	"github.com/HanamiSakiP/u-game/internal/steam"
	"github.com/HanamiSakiP/u-game/internal/store"
	"github.com/HanamiSakiP/u-game/internal/system"
)

// version 可由 go install -ldflags "-X .../internal/cli.version=..." 覆盖。
var version = model.AppVersion

type app struct {
	store *store.Store
	cfg   *model.Config
	out   io.Writer
	err   io.Writer
}

type entry struct {
	AppID   int           `json:"appid"`
	Name    string        `json:"name"`
	Status  string        `json:"status"`
	Type    string        `json:"type,omitempty"`
	Library string        `json:"library,omitempty"`
	Dir     string        `json:"dir,omitempty"`
	Size    int64         `json:"size,omitempty"`
	Patch   *model.Patch  `json:"patch,omitempty"`
	Patches []model.Patch `json:"patches,omitempty"`
}

// Run 执行 ugal 命令。
func Run(args []string, stdout, stderr io.Writer) error {
	dataDir, rest, err := extractDataDir(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		printHelp(stdout)
		return nil
	}
	switch rest[0] {
	case "help", "-h", "--help":
		printHelp(stdout)
		return nil
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "ugal %s\n", version)
		return nil
	case "completion":
		return runCompletion(rest[1:], stdout, stderr)
	}

	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	cfg, err := st.LoadConfig()
	if err != nil {
		return err
	}
	a := &app{store: st, cfg: cfg, out: stdout, err: stderr}

	// patches.json 作为默认的补丁输入文件：任意命令首次运行时自动创建，
	// 用户可以直接编辑它，再用 `ugal fav import` 一键导入收藏。
	if written, err := st.EnsurePatchesFile(); err != nil {
		fmt.Fprintf(a.err, "警告: 创建补丁记录文件失败: %v\n", err)
	} else if written {
		fmt.Fprintf(a.err, "已创建补丁记录文件 %s（内置默认记录，可手工编辑，运行 `ugal fav import` 一键导入收藏）\n",
			st.PatchesPath())
	}

	switch rest[0] {
	case "sync":
		return a.cmdSync(rest[1:])
	case "scan":
		return a.cmdScan(rest[1:])
	case "list":
		return a.cmdList(rest[1:], "")
	case "missing":
		return a.cmdList(rest[1:], "missing")
	case "extra":
		return a.cmdList(rest[1:], "extra")
	case "libs":
		return a.cmdLibs(rest[1:])
	case "system", "sysinfo":
		return a.cmdSystem(rest[1:])
	case "cover", "covers":
		return a.cmdCover(rest[1:])
	case "fav", "favorite", "favorites", "collection", "collect":
		return a.cmdFav(rest[1:])
	case "patch", "patches":
		return a.cmdPatch(rest[1:])
	case "update", "upgrade", "self-update", "selfupdate":
		return a.cmdUpdate(rest[1:])
	case "config":
		return a.cmdConfig(rest[1:])
	case "cookie", "cookies":
		return a.cmdCookie(rest[1:])
	default:
		return fmt.Errorf("未知命令 %q，运行 `ugal help` 查看帮助", rest[0])
	}
}

// DataDir 从命令行参数中解析 --data-dir / -d，供启动阶段释放内嵌源码使用。
func DataDir(args []string) (string, error) {
	dir, _, err := extractDataDir(args)
	return dir, err
}

func extractDataDir(args []string) (string, []string, error) {
	var (
		dir  string
		rest []string
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--data-dir" || a == "-d":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s 需要一个目录参数", a)
			}
			dir = args[i+1]
			i++
		case strings.HasPrefix(a, "--data-dir="):
			dir = strings.TrimPrefix(a, "--data-dir=")
		default:
			rest = append(rest, a)
		}
	}
	return dir, rest, nil
}

// parseArgs 允许参数写在位置参数之后，例如 `ugal sync 765... --source steamdb`。
func parseArgs(fs *flag.FlagSet, args []string) error {
	return fs.Parse(reorderArgs(fs, args))
}

func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

func (a *app) cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(a.err)
	source := fs.String("source", "", "来源: auto、steamdb、community、api")
	cookie := fs.String("cookie", "", "SteamDB Cookie 请求头内容")
	cookieFile := fs.String("cookie-file", "", "包含 Cookie 的文件（支持 Netscape cookies.txt）")
	userAgent := fs.String("user-agent", "", "请求 User-Agent（使用 SteamDB Cookie 时建议与浏览器一致）")
	useBrowser := fs.Bool("browser", false, "用本机 Firefox 完成 SteamDB 的 Cloudflare 验证")
	browserTimeout := fs.Duration("browser-timeout", browser.DefaultTimeout, "浏览器验证最长等待时间")
	timeout := fs.Duration("timeout", 30*time.Second, "网络超时")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal sync [steamid] [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return errors.New("sync 只接受一个 SteamID 参数")
	}
	steamID := a.cfg.SteamID
	if len(rest) == 1 {
		steamID = rest[0]
	}
	if strings.TrimSpace(steamID) == "" {
		return errors.New("缺少 SteamID，例如 `ugal sync 76561198000000000`，或先运行 `ugal config steamid ...`")
	}
	if *timeout <= 0 {
		*timeout = 30 * time.Second
	}
	src := strings.TrimSpace(*source)
	if src == "" {
		src = a.cfg.Source
	}
	if src == "" {
		src = "auto"
	}
	// 社区来源按原样使用标识，不做 SteamID64 转换。
	if !strings.EqualFold(src, "community") {
		if canonical := remote.CanonicalSteamID(steamID); canonical != remote.NormalizeSteamID(steamID) {
			fmt.Fprintf(a.err, "已转换为 SteamID64: %s\n", canonical)
		}
	}

	rawCookie := strings.TrimSpace(*cookie)
	if *cookieFile != "" {
		data, err := os.ReadFile(*cookieFile)
		if err != nil {
			return fmt.Errorf("读取 Cookie 文件失败: %w", err)
		}
		rawCookie = strings.TrimSpace(string(data))
	}
	if rawCookie == "" {
		rawCookie = strings.TrimSpace(os.Getenv("UGAL_STEAMDB_COOKIE"))
	}
	if rawCookie == "" {
		rawCookie = a.store.LoadCookie()
	}

	apiKey := strings.TrimSpace(os.Getenv("STEAM_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("UGAL_STEAM_API_KEY"))
	}

	ua := strings.TrimSpace(*userAgent)
	if ua == "" {
		ua = strings.TrimSpace(a.cfg.UserAgent)
	}

	var data *model.OwnedData
	var err error
	if *useBrowser {
		switch src {
		case "", "auto", "steamdb":
		default:
			return fmt.Errorf("--browser 只适用于 SteamDB 来源，当前来源是 %s", src)
		}
		data, err = a.fetchViaBrowser(steamID, *timeout, *browserTimeout)
	} else {
		data, err = remote.Fetch(context.Background(), remote.Options{
			SteamID:   steamID,
			Source:    src,
			Cookie:    rawCookie,
			UserAgent: ua,
			Timeout:   *timeout,
			APIKey:    apiKey,
			Logf: func(format string, args ...interface{}) {
				fmt.Fprintf(a.err, format+"\n", args...)
			},
		})
	}
	if err != nil {
		return err
	}
	if err := a.store.SaveOwned(data); err != nil {
		return err
	}
	a.cfg.SteamID = data.SteamID
	if err := a.store.SaveConfig(a.cfg); err != nil {
		return err
	}

	fmt.Fprintf(a.out, "SteamID   : %s\n", data.SteamID)
	fmt.Fprintf(a.out, "来源      : %s\n", sourceLabel(data.Source))
	fmt.Fprintf(a.out, "已拥有    : %d 个\n", data.Count)
	fmt.Fprintf(a.out, "数据文件  : %s\n", a.store.OwnedPath())

	if installed, err := a.store.LoadInstalled(); err == nil {
		have := 0
		installedSet := make(map[int]bool, len(installed.Games))
		for _, ap := range installed.Games {
			if ap.Installed {
				installedSet[ap.AppID] = true
			}
		}
		for _, g := range data.Games {
			if installedSet[g.AppID] {
				have++
			}
		}
		fmt.Fprintf(a.out, "本地已安装: %d 个，未安装: %d 个\n", have, data.Count-have)
	}
	return nil
}

// fetchViaBrowser 打开 Firefox 完成 SteamDB 的 Cloudflare 验证，并把验证后的
// 页面解析为已拥有游戏。验证通过的 Cookie 会写入数据目录，供后续 sync 复用。
func (a *app) fetchViaBrowser(steamID string, netTimeout, browserTimeout time.Duration) (*model.OwnedData, error) {
	id := remote.CanonicalSteamID(steamID)
	if id == "" {
		return nil, errors.New("SteamID 不能为空")
	}
	targetURL := remote.SteamDBCalculatorURL(id)
	fmt.Fprintf(a.err, "浏览器验证: 正在启动 Firefox，请在窗口中打开 %s\n", targetURL)

	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(a.err, format+"\n", args...)
	}
	// Ctrl-C / SIGTERM 时取消验证，确保临时 Firefox 窗口被关闭。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := browser.Verify(ctx, targetURL, browser.Options{
		Timeout: browserTimeout,
		Logf:    logf,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, errors.New("已取消浏览器验证")
		}
		return nil, err
	}

	if err := a.saveBrowserCredentials(res, logf); err != nil {
		fmt.Fprintf(a.err, "警告: %v\n", err)
	}

	games, parseErr := remote.ParseSteamDB([]byte(res.HTML))
	if parseErr == nil && len(games) > 0 {
		return &model.OwnedData{
			Version:   model.Version,
			SteamID:   id,
			Source:    "browser",
			FetchedAt: time.Now().UTC(),
			Count:     len(games),
			Games:     games,
		}, nil
	}

	if strings.TrimSpace(res.Cookies) != "" {
		logf("提示: 页面解析失败（%v），改用已验证的 Cookie 重新请求 SteamDB", parseErr)
		data, err := remote.Fetch(context.Background(), remote.Options{
			SteamID:   id,
			Source:    "steamdb",
			Cookie:    res.Cookies,
			UserAgent: res.UserAgent,
			Timeout:   netTimeout,
			Logf:      logf,
		})
		if err != nil {
			return nil, fmt.Errorf("浏览器验证成功，但读取 SteamDB 失败: %w", err)
		}
		return data, nil
	}
	if parseErr != nil {
		return nil, fmt.Errorf("解析浏览器中的 SteamDB 页面失败: %w", parseErr)
	}
	return nil, errors.New("浏览器验证成功，但页面中没有游戏")
}

// saveBrowserCredentials 把浏览器验证得到的 Cookie 与 User-Agent 保存到数据目录。
// Cookie 文件权限为 0600；User-Agent 只在尚未配置时写入。
func (a *app) saveBrowserCredentials(res *browser.Result, logf func(string, ...interface{})) error {
	if res == nil {
		return nil
	}
	cookie := strings.TrimSpace(res.Cookies)
	if cookie == "" {
		return nil
	}
	path := a.store.CookiePath()
	if err := os.WriteFile(path, []byte(cookie+"\n"), 0o600); err != nil {
		return fmt.Errorf("保存浏览器 Cookie 到 %s 失败: %w", path, err)
	}
	if res.UserAgent != "" && strings.TrimSpace(a.cfg.UserAgent) == "" {
		a.cfg.UserAgent = res.UserAgent
		if err := a.store.SaveConfig(a.cfg); err != nil {
			return fmt.Errorf("保存 User-Agent 失败: %w", err)
		}
	}
	if logf != nil {
		logf("已保存浏览器 Cookie 到 %s", path)
	}
	return nil
}

func (a *app) cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(a.err)
	includePartial := fs.Bool("include-partial", false, "保留 appmanifest 存在但目录缺失的条目")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal scan [Steam目录] [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return errors.New("scan 只接受一个 Steam 目录参数")
	}
	steamDir := a.cfg.SteamDir
	if len(rest) == 1 {
		steamDir = rest[0]
	}

	result, err := steam.Scan(steam.Options{SteamDir: steamDir, IncludePartial: *includePartial})
	if err != nil {
		return err
	}
	if err := a.store.SaveInstalled(result); err != nil {
		return err
	}
	a.cfg.SteamDir = result.SteamRoot
	if err := a.store.SaveConfig(a.cfg); err != nil {
		return err
	}
	sys := system.Detect()
	sys.SteamRoot = result.SteamRoot
	if err := a.store.SaveSystem(sys); err != nil {
		return err
	}

	fmt.Fprintf(a.out, "Steam 根目录: %s\n", result.SteamRoot)
	if result.LibraryFolders != "" {
		fmt.Fprintf(a.out, "库配置文件  : %s\n", result.LibraryFolders)
	}
	fmt.Fprintf(a.out, "库目录      : %d 个\n", len(result.Libraries))
	fmt.Fprintf(a.out, "已安装      : %d 个（游戏 %d，音乐 %d），占用 %s\n",
		result.Count, result.GameCount, result.MusicCount, humanSize(result.TotalSize))
	fmt.Fprintf(a.out, "数据文件    : %s\n", a.store.InstalledPath())
	fmt.Fprintf(a.out, "系统        : %s\n", orPlaceholder(sys.Platform, sys.OS))
	fmt.Fprintf(a.out, "系统记录    : %s\n", a.store.SystemPath())

	for _, lib := range result.Libraries {
		state := ""
		if !lib.Mounted {
			state = "（未挂载）"
		}
		fmt.Fprintf(a.out, "  %s  %d 个  %s%s\n", lib.Path, lib.AppsCount, humanSize(lib.TotalSize), state)
	}

	if len(result.Warnings) > 0 {
		fmt.Fprintf(a.out, "警告        : %d 条\n", len(result.Warnings))
		for i, w := range result.Warnings {
			if i >= 5 {
				fmt.Fprintf(a.out, "  ... 其余 %d 条见 installed.json\n", len(result.Warnings)-i)
				break
			}
			fmt.Fprintf(a.out, "  - %s\n", w)
		}
	}
	if len(result.Skipped) > 0 {
		fmt.Fprintf(a.out, "跳过        : %d 个未完整安装条目（--include-partial 可保留）\n", len(result.Skipped))
	}

	if owned, err := a.store.LoadOwned(); err == nil {
		installedSet := make(map[int]bool, len(result.Games))
		for _, ap := range result.Games {
			if ap.Installed {
				installedSet[ap.AppID] = true
			}
		}
		have := 0
		for _, g := range owned.Games {
			if installedSet[g.AppID] {
				have++
			}
		}
		fmt.Fprintf(a.out, "对比        : 已拥有且已安装 %d 个，已拥有未安装 %d 个，本地未记录 %d 个\n",
			have, owned.Count-have, result.Count-have)
	}
	return nil
}

func (a *app) cmdList(args []string, forced string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	ownedFlag := fs.Bool("owned", false, "列出已拥有游戏（默认）")
	installedFlag := fs.Bool("installed", false, "列出本地已安装游戏")
	missingFlag := fs.Bool("missing", false, "列出已拥有但未安装的游戏")
	extraFlag := fs.Bool("extra", false, "列出本地已安装但 SteamDB 未记录的游戏")
	typeFlag := fs.String("type", "", "只看 game 或 music")
	limit := fs.Int("limit", 0, "最多显示多少行（0 为不限制）")
	paths := fs.Bool("paths", false, "显示完整安装路径")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal list [--owned|--installed|--missing|--extra] [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("未知参数: %s", strings.Join(fs.Args(), " "))
	}

	mode := ""
	var picked []string
	if *ownedFlag {
		picked = append(picked, "owned")
	}
	if *installedFlag {
		picked = append(picked, "installed")
	}
	if *missingFlag {
		picked = append(picked, "missing")
	}
	if *extraFlag {
		picked = append(picked, "extra")
	}
	if forced != "" {
		mode = forced
	} else if len(picked) > 1 {
		return errors.New("--owned、--installed、--missing、--extra 只能选择一个")
	} else if len(picked) == 1 {
		mode = picked[0]
	} else {
		mode = "owned"
	}

	typeFilter := strings.ToLower(strings.TrimSpace(*typeFlag))
	switch typeFilter {
	case "", "all", "全部":
		typeFilter = ""
	case "game", "games", "游戏":
		typeFilter = "game"
	case "music", "soundtrack", "音乐":
		typeFilter = "music"
	default:
		return fmt.Errorf("未知类型 %q，可选 game 或 music", *typeFlag)
	}
	if *limit < 0 {
		return errors.New("--limit 不能为负数")
	}

	owned, installed, err := a.loadData()
	if err != nil {
		return err
	}
	if owned == nil && installed == nil {
		return errors.New("还没有任何数据，请先运行 `ugal sync <steamid>` 和 `ugal scan`")
	}

	all, err := buildEntries(owned, installed)
	if err != nil {
		return err
	}
	if names, err := a.gameNames(); err == nil {
		applyGameNames(all, names)
	}
	patchData, err := a.loadPatches()
	if err != nil {
		return err
	}
	attachPatches(all, patchData)
	if mode == "owned" && owned == nil {
		mode = "installed"
	}

	var rows []entry
	for _, e := range all {
		if typeFilter != "" && e.Type != typeFilter {
			continue
		}
		switch mode {
		case "owned":
			if e.Status == "extra" {
				continue
			}
		case "missing":
			if e.Status != "missing" {
				continue
			}
		case "installed":
			if e.Status != "installed" && e.Status != "extra" {
				continue
			}
		case "extra":
			if e.Status != "extra" {
				continue
			}
		}
		rows = append(rows, e)
	}

	summary := summarize(owned, installed)
	totalRows := len(rows)
	if *asJSON {
		jsonRows := rows
		if *limit > 0 && len(jsonRows) > *limit {
			jsonRows = jsonRows[:*limit]
		}
		payload := struct {
			Summary summaryJSON `json:"summary"`
			Total   int         `json:"total"`
			Games   []entry     `json:"games"`
		}{Summary: summary, Total: totalRows, Games: jsonRows}
		return writeJSONOutput(a.out, payload)
	}

	fmt.Fprintln(a.out, summaryLine(summary))
	if owned == nil && mode == "owned" {
		fmt.Fprintln(a.out, "提示: 尚未同步已拥有游戏，以下为本地已安装列表。")
	}

	display := rows
	if *limit > 0 && len(display) > *limit {
		display = display[:*limit]
	}
	headers := []string{"APPID", "状态", "类型", "名称", "补丁", "大小", "库"}
	right := []bool{true, false, false, false, false, true, false}
	tableRows := make([][]string, 0, len(display))
	for _, e := range display {
		name := truncateDisplay(e.Name, 64)
		location := e.LibraryBase()
		if *paths {
			location = e.Dir
			if location == "" {
				location = e.Library
			}
			headers[6] = "位置"
		}
		tableRows = append(tableRows, []string{
			fmt.Sprintf("%d", e.AppID),
			statusLabel(e.Status),
			typeLabel(e.Type),
			name,
			patchStatusAll(e.Patches),
			humanSize(e.Size),
			location,
		})
	}
	renderTable(a.out, headers, right, tableRows)
	if *limit > 0 && totalRows > *limit {
		fmt.Fprintf(a.out, "\n（仅显示前 %d 条，共 %d 条）\n", *limit, totalRows)
	}
	return nil
}

func (e entry) LibraryBase() string {
	if e.Library == "" {
		return "-"
	}
	base := filepath.Base(filepath.Clean(e.Library))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return e.Library
	}
	return base
}

func (a *app) cmdLibs(args []string) error {
	fs := flag.NewFlagSet("libs", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal libs [--json]")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	installed, err := a.store.LoadInstalled()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("还没有本地扫描数据，请先运行 `ugal scan`")
		}
		return err
	}
	if *asJSON {
		return writeJSONOutput(a.out, installed.Libraries)
	}
	fmt.Fprintf(a.out, "Steam 根目录: %s\n", installed.SteamRoot)
	fmt.Fprintf(a.out, "扫描时间    : %s\n\n", installed.ScannedAt.Local().Format("2006-01-02 15:04:05"))

	headers := []string{"路径", "游戏", "音乐", "合计", "大小", "状态"}
	right := []bool{false, true, true, true, true, false}
	var rows [][]string
	var totalGames, totalMusic int
	var totalSize int64
	for _, lib := range installed.Libraries {
		games, music := 0, 0
		var size int64
		for _, ap := range lib.Apps {
			if !ap.Installed {
				continue
			}
			if ap.Type == "music" {
				music++
			} else {
				games++
			}
			size += ap.Size
		}
		totalGames += games
		totalMusic += music
		totalSize += size
		state := "正常"
		if !lib.Mounted {
			state = "未挂载"
		}
		rows = append(rows, []string{
			lib.Path,
			fmt.Sprintf("%d", games),
			fmt.Sprintf("%d", music),
			fmt.Sprintf("%d", games+music),
			humanSize(size),
			state,
		})
	}
	renderTable(a.out, headers, right, rows)
	fmt.Fprintf(a.out, "\n合计: 游戏 %d 个，音乐 %d 个，共 %d 个，占用 %s\n",
		totalGames, totalMusic, totalGames+totalMusic, humanSize(totalSize))
	return nil
}

func (a *app) cmdConfig(args []string) error {
	if len(args) == 0 {
		a.printConfig()
		return nil
	}
	key := normalizeConfigKey(args[0])
	if key == "" {
		return fmt.Errorf("未知配置项 %q，可选 steamid、steam-dir、source、user-agent、update-dir、repo、branch", args[0])
	}
	if len(args) == 1 {
		switch key {
		case "steamid":
			fmt.Fprintln(a.out, a.cfg.SteamID)
		case "steam-dir":
			fmt.Fprintln(a.out, a.cfg.SteamDir)
		case "source":
			fmt.Fprintln(a.out, a.cfg.Source)
		case "user-agent":
			fmt.Fprintln(a.out, a.cfg.UserAgent)
		case "update-dir":
			fmt.Fprintln(a.out, orPlaceholder(a.cfg.UpdateDir, a.defaultSrcDir()))
		case "repo":
			fmt.Fprintln(a.out, orPlaceholder(a.cfg.Repo, "github.com/HanamiSakiP/u-game"))
		case "branch":
			fmt.Fprintln(a.out, orPlaceholder(a.cfg.Branch, "main"))
		}
		return nil
	}
	if len(args) > 2 {
		return errors.New("config 最多接受两个参数：配置项和值")
	}
	value := strings.TrimSpace(args[1])
	switch key {
	case "steamid":
		// 只提取标识本身，不在这里转换为 SteamID64：community 来源需要保留
		// 自定义 URL / AccountID 原值，SteamDB 等来源会在抓取时再转换。
		a.cfg.SteamID = remote.NormalizeSteamID(value)
	case "steam-dir":
		a.cfg.SteamDir = strings.Trim(value, `"'`)
	case "source":
		value = strings.ToLower(value)
		if value == "" {
			value = "auto"
		}
		switch value {
		case "auto", "steamdb", "community", "api":
		default:
			return fmt.Errorf("来源 %q 无效，可选 auto、steamdb、community、api", value)
		}
		a.cfg.Source = value
	case "user-agent":
		a.cfg.UserAgent = value
	case "update-dir":
		value = strings.Trim(value, `"'`)
		a.cfg.UpdateDir = value
	case "repo":
		a.cfg.Repo = strings.Trim(value, `"'`)
	case "branch":
		a.cfg.Branch = strings.Trim(value, `"'`)
	}
	if key == "steamid" {
		value = a.cfg.SteamID
	}
	if err := a.store.SaveConfig(a.cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已设置 %s = %s\n", key, value)
	return nil
}

func (a *app) printConfig() {
	cookieState := "未找到（可选）"
	if _, err := os.Stat(a.store.CookiePath()); err == nil {
		cookieState = a.store.CookiePath()
	}
	fmt.Fprintf(a.out, "数据目录 : %s\n", a.store.Dir)
	fmt.Fprintf(a.out, "配置文件 : %s\n", a.store.ConfigPath())
	fmt.Fprintf(a.out, "SteamID  : %s\n", orPlaceholder(a.cfg.SteamID, "（未设置）"))
	fmt.Fprintf(a.out, "Steam目录: %s\n", orPlaceholder(a.cfg.SteamDir, "（自动探测）"))
	fmt.Fprintf(a.out, "来源     : %s\n", orPlaceholder(a.cfg.Source, "auto"))
	fmt.Fprintf(a.out, "Cookie   : %s\n", cookieState)
	fmt.Fprintf(a.out, "已拥有   : %s\n", a.store.OwnedPath())
	fmt.Fprintf(a.out, "已安装   : %s\n", a.store.InstalledPath())
	fmt.Fprintf(a.out, "系统记录 : %s\n", a.store.SystemPath())
	fmt.Fprintf(a.out, "收藏夹   : %s\n", a.store.FavoritesPath())
	fmt.Fprintf(a.out, "补丁记录 : %s\n", a.store.PatchesPath())
	fmt.Fprintf(a.out, "补丁目录 : %s\n", a.store.PatchesDir())
	fmt.Fprintf(a.out, "名称缓存 : %s\n", a.store.AppNamesPath())
	fmt.Fprintf(a.out, "封面目录 : %s\n", a.store.CoversDir())
	fmt.Fprintf(a.out, "源码目录 : %s\n", orPlaceholder(a.cfg.UpdateDir, a.defaultSrcDir()))
	fmt.Fprintf(a.out, "源码仓库 : %s\n", orPlaceholder(a.cfg.Repo, "github.com/HanamiSakiP/u-game"))
	fmt.Fprintf(a.out, "更新分支 : %s\n", orPlaceholder(a.cfg.Branch, "main"))
	fmt.Fprintf(a.out, "更新记录 : %s\n", a.store.UpdatePath())
}

func (a *app) loadData() (*model.OwnedData, *model.InstalledData, error) {
	owned, err := a.store.LoadOwned()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if errors.Is(err, os.ErrNotExist) {
		owned = nil
	}
	installed, err := a.store.LoadInstalled()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if errors.Is(err, os.ErrNotExist) {
		installed = nil
	}
	return owned, installed, nil
}

func buildEntries(owned *model.OwnedData, installed *model.InstalledData) ([]entry, error) {
	installedMap := make(map[int]model.App)
	if installed != nil {
		for _, ap := range installed.Games {
			old, ok := installedMap[ap.AppID]
			if !ok {
				installedMap[ap.AppID] = ap
				continue
			}
			installedMap[ap.AppID] = mergeInstalledApp(old, ap)
		}
	}

	var out []entry
	if owned != nil {
		for _, g := range owned.Games {
			e := entry{AppID: g.AppID, Name: g.Name, Status: "missing"}
			if ap, ok := installedMap[g.AppID]; ok && ap.Installed {
				e.Status = "installed"
				e.Type = ap.Type
				e.Library = ap.Library
				e.Dir = ap.Dir
				e.Size = ap.Size
				if e.Name == "" {
					e.Name = ap.Name
				}
			}
			if e.Name == "" {
				e.Name = fmt.Sprintf("App %d", e.AppID)
			}
			out = append(out, e)
		}
	}

	ownedSet := make(map[int]bool)
	if owned != nil {
		for _, g := range owned.Games {
			ownedSet[g.AppID] = true
		}
	}
	if len(installedMap) > 0 {
		ids := make([]int, 0, len(installedMap))
		for id := range installedMap {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			ap := installedMap[id]
			if !ap.Installed || ownedSet[ap.AppID] {
				continue
			}
			name := ap.Name
			if name == "" {
				name = fmt.Sprintf("App %d", ap.AppID)
			}
			out = append(out, entry{
				AppID:   ap.AppID,
				Name:    name,
				Status:  "extra",
				Type:    ap.Type,
				Library: ap.Library,
				Dir:     ap.Dir,
				Size:    ap.Size,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].AppID < out[j].AppID
	})
	return out, nil
}

// mergeInstalledApp 合并同一个 appid 在多个库目录中的记录，优先保留
// 有 appmanifest、有名称且已安装的字段，避免同一个 appid 显示成多行。
func mergeInstalledApp(a, b model.App) model.App {
	// 有 appmanifest 的记录通常包含名称和安装目录，优先作为合并基础。
	if a.ManifestMissing && !b.ManifestMissing {
		a, b = b, a
	}
	if !a.Installed && b.Installed {
		a.Installed = true
	}
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.InstallDir == "" {
		a.InstallDir = b.InstallDir
	}
	if a.Dir == "" {
		a.Dir = b.Dir
	}
	if a.Type == "" {
		a.Type = b.Type
	}
	if a.Library == "" {
		a.Library = b.Library
	}
	if a.Size == 0 {
		a.Size = b.Size
	}
	if a.DownloadType == 0 {
		a.DownloadType = b.DownloadType
	}
	if a.StateFlags == 0 {
		a.StateFlags = b.StateFlags
	}
	if a.LastUpdated == 0 {
		a.LastUpdated = b.LastUpdated
	}
	if a.BuildID == "" {
		a.BuildID = b.BuildID
	}
	if !b.ManifestMissing {
		a.ManifestMissing = false
	}
	return a
}

type summaryJSON struct {
	Owned     int `json:"owned"`
	Installed int `json:"installed"`
	Missing   int `json:"missing"`
	Extra     int `json:"extra"`
}

func summarize(owned *model.OwnedData, installed *model.InstalledData) summaryJSON {
	var s summaryJSON
	installedSet := make(map[int]bool)
	if installed != nil {
		for _, ap := range installed.Games {
			if ap.Installed {
				installedSet[ap.AppID] = true
				s.Installed++
			}
		}
	}
	if owned != nil {
		for _, g := range owned.Games {
			s.Owned++
			if !installedSet[g.AppID] {
				s.Missing++
			}
			delete(installedSet, g.AppID)
		}
	}
	s.Extra = len(installedSet)
	return s
}

func summaryLine(s summaryJSON) string {
	if s.Owned == 0 {
		if s.Installed == 0 {
			return "没有可列出的游戏"
		}
		return fmt.Sprintf("本地已安装 %d 个 | 本地未记录 %d 个", s.Installed, s.Extra)
	}
	return fmt.Sprintf("已拥有 %d 个 | 已安装 %d 个 | 未安装 %d 个 | 本地未记录 %d 个",
		s.Owned, s.Installed, s.Missing, s.Extra)
}

func statusLabel(status string) string {
	switch status {
	case "installed":
		return "已安装"
	case "missing":
		return "未安装"
	case "extra":
		return "本地独有"
	case "unknown":
		return "未记录"
	default:
		return status
	}
}

func typeLabel(t string) string {
	switch t {
	case "game":
		return "游戏"
	case "music":
		return "音乐"
	default:
		return "-"
	}
}

func sourceLabel(s string) string {
	switch s {
	case "steamdb":
		return "SteamDB"
	case "browser":
		return "SteamDB（浏览器验证）"
	case "community":
		return "Steam 社区"
	case "api":
		return "Steam Web API"
	default:
		return s
	}
}

func normalizeConfigKey(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "steamid", "steam-id", "steam_id":
		return "steamid"
	case "steam-dir", "steamdir", "steam_dir", "steam-directory":
		return "steam-dir"
	case "source":
		return "source"
	case "user-agent", "useragent", "ua":
		return "user-agent"
	case "update-dir", "updatedir", "src-dir", "srcdir", "source-dir", "sourcedir":
		return "update-dir"
	case "repo", "repository", "git":
		return "repo"
	case "branch", "ref":
		return "branch"
	default:
		return ""
	}
}

func orPlaceholder(v, placeholder string) string {
	if strings.TrimSpace(v) == "" {
		return placeholder
	}
	return v
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `ugal - Steam 游戏库管理

用法:
  ugal [--data-dir 目录] <命令> [参数]

命令:
  sync [steamid]     从 SteamDB 抓取已拥有游戏（--browser 可用浏览器通过 Cloudflare 验证）
  scan [Steam目录]   扫描本地 Steam 库，记录已安装游戏和存储目录
  list               列出已拥有游戏及本地安装状态（默认）
  missing            只列出已拥有但未安装的游戏
  extra              只列出本地已安装但 SteamDB 未记录的游戏
  libs               列出 Steam 库目录、数量与占用
  system             检测并记录当前系统环境
  cover [appid...]   获取/下载已安装游戏封面（跳过已有，失败自动重试）
  fav <子命令>       本地游戏收藏夹（添加、导入导出）
  patch <子命令>     游戏补丁记录（哈希、名称、是否已打补丁）
  update             同步源码到本地目录并重新构建（git / 源码包）
  config             查看或修改配置
  cookie <子命令>    SteamDB Cookie（浏览器获取、查看、定位、清除）
  completion [shell] 自动检测 shell 并写入补全脚本（--print 只输出）
  version            显示版本

全局参数:
  --data-dir, -d     数据目录（默认 ~/.ugal；当前目录的 .ugal 含数据时优先使用）

常用参数:
  list --installed           只看本地已安装
  list --missing             只看未安装（等同 missing）
  list --json                机器可读输出
  list --type game|music     按类型过滤
  list --paths               显示完整安装路径
  scan --include-partial     保留未完整安装的条目
  sync --source steamdb      强制指定来源
  sync --source community    使用 Steam 社区（标识按原样使用，不转换 SteamID64）
  sync --cookie-file FILE    使用浏览器导出的 SteamDB Cookie
  sync --browser             打开 Firefox 完成 Cloudflare 验证后同步
  sync --browser-timeout 3m  浏览器验证最长等待时间
  cover --list               查看封面获取状态
  cover --force              重新获取已有封面
  cover --retry 2            下载失败后的重试次数
  cover --source local|cdn   只用本地缓存或只用网络
  fav add 440                收藏游戏
  fav export fav.json        导出收藏夹
  fav import fav.json        导入收藏夹
  fav import                 从 patches.json 一键导入收藏
  patch list                 查看补丁记录
  patch list --all           查看已知游戏及补丁状态（默认无补丁）
  patch set 440 --file x.patch --applied
  patch set 440 --hash <哈希> --not-applied
  patch check                检查本地补丁文件是否存在且哈希正确
  update --check             只检查是否有新版本
  update --no-build          只同步源码，不构建
  update --build-only        用已有源码重新构建（不联网）
  update --src-dir DIR       指定源码目录（默认 <数据目录>/src）
  update --install PATH      安装到指定路径
  cookie get                 用 Firefox 获取并保存 SteamDB Cookie
  cookie show                显示已保存的 Cookie
  completion                 自动检测 shell 并写入标准补全位置
  completion bash            写入 bash 补全（zsh/fish/powershell 同理）
  completion --print         只输出补全脚本，不写入文件
  completion --dir DIR       写入指定目录

示例:
  ugal sync 76561198000000000
  ugal sync 22202            # AccountID 自动转换为 SteamID64
  ugal scan
  ugal scan "D:\SteamLibrary"
  ugal list --missing
  ugal libs
  ugal system
  ugal cover
  ugal cover 440 220
  ugal fav add 440 --name "Team Fortress 2"
  ugal fav export favorites.json
  ugal fav import favorites.json
  ugal patch list --all
  ugal patch set 440 --file patch.xp3 --applied
  ugal patch check
  ugal update
  ugal update --check
  ugal update --build-only
  ugal cookie get
  ugal completion
  ugal config steamid 76561198000000000
  ugal config steam-dir "D:\SteamLibrary"

数据目录:
  默认 ~/.ugal；当前目录的 .ugal 是数据目录（空目录或含数据文件）时优先使用。
  包含 config.json、owned.json、installed.json、system.json、favorites.json、
  patches.json、update.json、covers/、patches/、src/，以及可选的 cookie.txt
  （SteamDB Cookie）。patches.json 会在任意命令首次运行时按内置默认记录自动
  创建，可直接编辑；运行 "ugal fav import" 可从 patches.json 一键导入收藏。patches/ 用于
  存放本地补丁文件，patch check 会校验其中文件是否存在、哈希是否与
  patches.json 记录一致。
  update 命令默认把源码放在数据目录的 src/（默认 ~/.ugal/src），并在本机构建。
  运行任意命令时，如果数据目录下的 src/ 不存在，会自动写入二进制内嵌的源码
  （已有源码不覆盖）。
`)
}
