package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/saves"
	"github.com/HanamiSakiP/u-game/internal/steam"
)

func (a *app) cmdSave(args []string) error {
	if len(args) == 0 {
		return a.saveList(nil)
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls", "show", "status":
		return a.saveList(args[1:])
	case "sync", "pull", "fetch", "collect", "backup":
		return a.saveSync(args[1:])
	case "restore", "push", "apply", "load":
		return a.saveRestore(args[1:])
	case "remove", "rm", "del", "delete":
		return a.saveRemove(args[1:])
	case "clear", "reset":
		return a.saveClear(args[1:])
	case "help", "-h", "--help":
		printSaveHelp(a.out)
		return nil
	default:
		if _, err := parseAppIDs(args); err == nil {
			return a.saveList(args)
		}
		return fmt.Errorf("未知存档命令 %q，运行 `ugal save help` 查看帮助", args[0])
	}
}

func (a *app) loadSaves() (*model.SavesData, error) {
	data, err := a.store.LoadSaves()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &model.SavesData{Version: model.Version, Saves: []model.SaveGame{}}, nil
		}
		return nil, err
	}
	if data.Saves == nil {
		data.Saves = []model.SaveGame{}
	}
	return data, nil
}

func (a *app) saveSync(args []string) error {
	fs := flag.NewFlagSet("save sync", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	offline := fs.Bool("offline", false, "输出游戏名时不联网")
	noCloud := fs.Bool("no-cloud", false, "不同步云存档")
	noLocal := fs.Bool("no-local", false, "不同步本地存档")
	cloudOnly := fs.Bool("cloud-only", false, "只同步云存档")
	localOnly := fs.Bool("local-only", false, "只同步本地存档")
	steamDir := fs.String("steam-dir", "", "Steam 根目录（默认取 installed.json）")
	steamUser := fs.String("steam-user", "", "Steam 账号 ID（userdata 下的数字目录）")
	gameDir := fs.String("game-dir", "", "覆盖游戏目录（只能与一个 appid 一起使用）")
	force := fs.Bool("force", false, "即使备份中的文件更新也覆盖")
	mirror := fs.Bool("mirror", false, "删除备份中源目录已不存在的文件")
	dryRun := fs.Bool("dry-run", false, "只统计，不写入文件")
	var localDirs []string
	fs.Func("local-dir", "显式指定本地存档目录（可重复）", func(value string) error {
		if strings.TrimSpace(value) != "" {
			localDirs = append(localDirs, value)
		}
		return nil
	})
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal save sync [appid...] [--no-cloud|--no-local] [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ids, err := parseAppIDs(fs.Args())
	if err != nil {
		return err
	}
	if *cloudOnly && *localOnly {
		return errors.New("--cloud-only 和 --local-only 不能同时使用")
	}
	if *cloudOnly && *noCloud {
		return errors.New("--cloud-only 和 --no-cloud 不能同时使用")
	}
	if *localOnly && *noLocal {
		return errors.New("--local-only 和 --no-local 不能同时使用")
	}
	cloud := !*noCloud && !*localOnly
	local := !*noLocal && !*cloudOnly
	if !cloud && !local {
		return errors.New("云存档和本地存档至少选择一项")
	}
	if strings.TrimSpace(*gameDir) != "" && len(ids) != 1 {
		return errors.New("--game-dir 只能与一个 appid 一起使用")
	}
	if len(localDirs) > 0 && len(ids) == 0 {
		return errors.New("--local-dir 需要与 appid 一起使用")
	}

	_, installed, err := a.loadData()
	if err != nil {
		return err
	}
	if len(ids) == 0 && installed == nil {
		return errors.New("还没有本地扫描数据，请先运行 `ugal scan`，或用 appid 指定游戏")
	}

	names := map[int]string{}
	if len(ids) > 0 {
		if resolved, err := a.resolveGameNames(ids, *offline); err == nil {
			names = resolved
		}
	}
	gameDirs := map[int]string{}
	if strings.TrimSpace(*gameDir) != "" {
		gameDirs[ids[0]] = expandUserPath(strings.TrimSpace(*gameDir))
	}

	root := strings.TrimSpace(*steamDir)
	if root == "" && installed != nil {
		root = installed.SteamRoot
	}
	if root == "" {
		root = a.cfg.SteamDir
	}
	root = a.resolveCloudRoot(root, a.cfg.SteamDir)
	user := firstNonEmpty(*steamUser, a.cfg.SteamUser, saves.AccountID(a.cfg.SteamID))
	if converted := saves.AccountID(user); converted != "" {
		user = converted
	}

	result, err := saves.Sync(saves.Options{
		Root:      a.store.Dir,
		Installed: installed,
		AppIDs:    ids,
		GameDirs:  gameDirs,
		Names:     names,
		SteamRoot: root,
		SteamUser: user,
		Cloud:     cloud,
		Local:     local,
		LocalDirs: localDirs,
		Force:     *force,
		Mirror:    *mirror,
		DryRun:    *dryRun,
		Logf: func(format string, args ...interface{}) {
			fmt.Fprintf(a.err, format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}

	if !*dryRun {
		data, err := a.loadSaves()
		if err != nil {
			return err
		}
		saves.UpdateManifest(data, result)
		if err := a.store.SaveSaves(data); err != nil {
			return err
		}
		if result.SteamUser != "" && result.SteamUser != a.cfg.SteamUser {
			a.cfg.SteamUser = result.SteamUser
			if err := a.store.SaveConfig(a.cfg); err != nil {
				fmt.Fprintf(a.err, "警告: 保存 Steam 账号记录失败: %v\n", err)
			}
		}
	}

	if *asJSON {
		payload := struct {
			*saves.SyncResult
			DataDir   string `json:"data_dir"`
			SavesJSON string `json:"saves_json"`
			DryRun    bool   `json:"dry_run,omitempty"`
		}{
			SyncResult: result,
			DataDir:    a.store.Dir,
			SavesJSON:  a.store.SavesPath(),
			DryRun:     *dryRun,
		}
		return writeJSONOutput(a.out, payload)
	}
	return a.printSaveSyncResult(result, *dryRun)
}

func (a *app) printSaveSyncResult(result *saves.SyncResult, dryRun bool) error {
	if result.SteamUser != "" {
		fmt.Fprintf(a.out, "Steam 账号 : %s\n", result.SteamUser)
	}
	if result.CloudRoot != "" {
		fmt.Fprintf(a.out, "Steam 目录 : %s\n", result.CloudRoot)
	}
	fmt.Fprintf(a.out, "存档目录   : %s\n", a.store.SavesDir())
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.out, "提示       : %s\n", warning)
	}

	var (
		games     int
		locations int
		files     int
		bytes     int64
		warnings  int
	)
	for _, game := range result.Saves {
		if len(game.Locations) == 0 && len(game.Warnings) == 0 {
			continue
		}
		games++
		name := game.Name
		if name == "" {
			name = fmt.Sprintf("App %d", game.AppID)
		}
		fmt.Fprintf(a.out, "\n%s（%d）\n", name, game.AppID)
		for _, loc := range game.Locations {
			locations++
			files += loc.Files
			bytes += loc.Bytes
			fmt.Fprintf(a.out, "  %s: %d 个文件，%s\n", saveKindLabel(loc.Kind), loc.Files, humanSize(loc.Bytes))
			fmt.Fprintf(a.out, "    来源: %s\n", loc.Source)
			fmt.Fprintf(a.out, "    备份: %s\n", filepathJoinData(a.store.Dir, loc.Store))
			if loc.Copied+loc.Updated+loc.Skipped > 0 {
				fmt.Fprintf(a.out, "    变更: 新增 %d，更新 %d，跳过 %d\n", loc.Copied, loc.Updated, loc.Skipped)
			}
			if loc.Removed > 0 {
				fmt.Fprintf(a.out, "    删除多余文件: %d\n", loc.Removed)
			}
		}
		for _, warning := range game.Warnings {
			warnings++
			fmt.Fprintf(a.out, "  提示: %s\n", warning)
		}
	}
	if games == 0 {
		fmt.Fprintln(a.out, "没有找到可同步的存档。")
		return nil
	}
	prefix := "已同步"
	if dryRun {
		prefix = "预演"
	}
	fmt.Fprintf(a.out, "\n%s %d 个游戏、%d 个存档位置、%d 个文件（%s）\n",
		prefix, games, locations, files, humanSize(bytes))
	if warnings > 0 {
		fmt.Fprintf(a.out, "提示: %d 条\n", warnings)
	}
	if !dryRun {
		fmt.Fprintf(a.out, "记录: %s\n", a.store.SavesPath())
	}
	return nil
}

func (a *app) saveList(args []string) error {
	fs := flag.NewFlagSet("save list", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	paths := fs.Bool("paths", false, "显示完整存档路径")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal save list [appid...] [--paths] [--json]")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ids, err := parseAppIDs(fs.Args())
	if err != nil {
		return err
	}
	data, err := a.loadSaves()
	if err != nil {
		return err
	}
	filter := make(map[int]bool, len(ids))
	for _, id := range ids {
		filter[id] = true
	}
	games := make([]model.SaveGame, 0, len(data.Saves))
	for _, game := range data.Saves {
		if len(filter) > 0 && !filter[game.AppID] {
			continue
		}
		games = append(games, game)
	}
	sort.SliceStable(games, func(i, j int) bool { return games[i].AppID < games[j].AppID })

	if *asJSON {
		payload := struct {
			DataDir   string           `json:"data_dir"`
			SavesDir  string           `json:"saves_dir"`
			SteamUser string           `json:"steam_user,omitempty"`
			Total     int              `json:"total"`
			Saves     []model.SaveGame `json:"saves"`
		}{
			DataDir:   a.store.Dir,
			SavesDir:  a.store.SavesDir(),
			SteamUser: data.SteamUser,
			Total:     len(games),
			Saves:     games,
		}
		return writeJSONOutput(a.out, payload)
	}

	if len(games) == 0 {
		fmt.Fprintln(a.out, "还没有同步过存档，运行 `ugal save sync` 收集云存档和本地存档。")
		return nil
	}

	headers := []string{"APPID", "游戏", "云", "本地", "文件", "大小", "同步时间", "位置"}
	right := []bool{true, false, true, true, true, true, false, false}
	rows := make([][]string, 0, len(games))
	for _, game := range games {
		var cloud, local bool
		var files int
		var bytes int64
		var latest time.Time
		var locations []string
		for _, loc := range game.Locations {
			if loc.Kind == saves.KindCloud {
				cloud = true
			}
			if loc.Kind == saves.KindLocal {
				local = true
			}
			files += loc.Files
			bytes += loc.Bytes
			if loc.SyncedAt.After(latest) {
				latest = loc.SyncedAt
			}
			source := loc.Path
			if !*paths {
				source = filepathBaseLabel(loc.Path)
			}
			if source != "" {
				locations = append(locations, source)
			}
		}
		name := game.Name
		if name == "" {
			name = fmt.Sprintf("App %d", game.AppID)
		}
		when := "-"
		if !latest.IsZero() {
			when = latest.Local().Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", game.AppID),
			truncateDisplay(name, 40),
			boolMark(cloud),
			boolMark(local),
			fmt.Sprintf("%d", files),
			humanSize(bytes),
			when,
			truncateDisplay(strings.Join(locations, ", "), 48),
		})
	}
	renderTable(a.out, headers, right, rows)
	fmt.Fprintf(a.out, "\n共 %d 个游戏，备份目录 %s\n", len(games), a.store.SavesDir())
	return nil
}

func (a *app) saveRestore(args []string) error {
	fs := flag.NewFlagSet("save restore", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	noCloud := fs.Bool("no-cloud", false, "不还原云存档")
	noLocal := fs.Bool("no-local", false, "不还原本地存档")
	cloudOnly := fs.Bool("cloud-only", false, "只还原云存档")
	localOnly := fs.Bool("local-only", false, "只还原本地存档")
	all := fs.Bool("all", false, "还原所有已记录的游戏")
	force := fs.Bool("force", false, "即使目标文件更新也覆盖")
	mirror := fs.Bool("mirror", false, "删除目标目录中备份里不存在的文件")
	dryRun := fs.Bool("dry-run", false, "只统计，不写入文件")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal save restore [appid...] [--all] [--no-cloud|--no-local] [参数]")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ids, err := parseAppIDs(fs.Args())
	if err != nil {
		return err
	}
	if *cloudOnly && *localOnly {
		return errors.New("--cloud-only 和 --local-only 不能同时使用")
	}
	if *cloudOnly && *noCloud {
		return errors.New("--cloud-only 和 --no-cloud 不能同时使用")
	}
	if *localOnly && *noLocal {
		return errors.New("--local-only 和 --no-local 不能同时使用")
	}
	cloud := !*noCloud && !*localOnly
	local := !*noLocal && !*cloudOnly
	if !cloud && !local {
		return errors.New("云存档和本地存档至少选择一项")
	}
	if !*all && len(ids) == 0 {
		return errors.New("请提供要还原的 appid，或使用 --all")
	}

	data, err := a.loadSaves()
	if err != nil {
		return err
	}
	if len(data.Saves) == 0 {
		return errors.New("还没有同步过存档，运行 `ugal save sync` 后再还原")
	}
	filter := make(map[int]bool, len(ids))
	for _, id := range ids {
		filter[id] = true
	}
	found := 0
	for _, game := range data.Saves {
		if len(filter) > 0 && filter[game.AppID] {
			found++
		}
	}
	if len(filter) > 0 && found == 0 {
		return fmt.Errorf("saves.json 中没有这些 appid 的存档记录: %s", joinInts(ids))
	}

	result, err := saves.Restore(data, saves.RestoreOptions{
		Root:   a.store.Dir,
		IDs:    ids,
		Cloud:  cloud,
		Local:  local,
		Force:  *force,
		Mirror: *mirror,
		DryRun: *dryRun,
		Logf: func(format string, args ...interface{}) {
			fmt.Fprintf(a.err, format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}
	if *asJSON {
		payload := struct {
			*saves.RestoreResult
			DataDir string `json:"data_dir"`
			DryRun  bool   `json:"dry_run,omitempty"`
		}{RestoreResult: result, DataDir: a.store.Dir, DryRun: *dryRun}
		return writeJSONOutput(a.out, payload)
	}

	var (
		games     int
		files     int
		locations int
		warnings  int
	)
	for _, game := range result.Saves {
		if len(game.Locations) == 0 && len(game.Warnings) == 0 {
			continue
		}
		games++
		name := game.Name
		if name == "" {
			name = fmt.Sprintf("App %d", game.AppID)
		}
		fmt.Fprintf(a.out, "\n%s（%d）\n", name, game.AppID)
		for _, loc := range game.Locations {
			locations++
			files += loc.Copied + loc.Updated
			fmt.Fprintf(a.out, "  %s: 写入 %d 个文件，跳过 %d 个\n", saveKindLabel(loc.Kind), loc.Copied+loc.Updated, loc.Skipped)
			fmt.Fprintf(a.out, "    备份: %s\n", loc.Source)
			fmt.Fprintf(a.out, "    目标: %s\n", loc.Store)
			if loc.Removed > 0 {
				fmt.Fprintf(a.out, "    删除多余文件: %d\n", loc.Removed)
			}
			if loc.Skipped > 0 && !*force {
				fmt.Fprintln(a.out, "    提示: 目标文件较新，已跳过；使用 --force 覆盖")
			}
		}
		for _, warning := range game.Warnings {
			warnings++
			fmt.Fprintf(a.out, "  提示: %s\n", warning)
		}
	}
	if games == 0 {
		fmt.Fprintln(a.out, "没有可还原的存档。")
		return nil
	}
	prefix := "已还原"
	if *dryRun {
		prefix = "预演"
	}
	fmt.Fprintf(a.out, "\n%s %d 个游戏、%d 个存档位置、%d 个文件\n", prefix, games, locations, files)
	if warnings > 0 {
		fmt.Fprintf(a.out, "提示: %d 条\n", warnings)
	}
	return nil
}

func (a *app) saveRemove(args []string) error {
	fs := flag.NewFlagSet("save remove", flag.ContinueOnError)
	fs.SetOutput(a.err)
	all := fs.Bool("all", false, "删除所有存档记录和备份")
	keep := fs.Bool("keep", false, "只删除记录，保留备份文件")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal save remove <appid...> [--keep] | --all")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ids, err := parseAppIDs(fs.Args())
	if err != nil {
		return err
	}
	if !*all && len(ids) == 0 {
		return errors.New("请提供要删除的 appid，或使用 --all")
	}
	data, err := a.loadSaves()
	if err != nil {
		return err
	}
	if *all {
		ids = ids[:0]
		for _, game := range data.Saves {
			ids = append(ids, game.AppID)
		}
	}
	root := a.store.Dir
	if *keep {
		root = ""
	}
	removed := saves.Remove(data, root, ids)
	if err := a.store.SaveSaves(data); err != nil {
		return err
	}
	if *keep {
		fmt.Fprintf(a.out, "已删除 %d 个游戏的存档记录，备份文件保留在 %s\n", removed, a.store.SavesDir())
	} else {
		fmt.Fprintf(a.out, "已删除 %d 个游戏的存档记录和备份，剩余 %d 个\n", removed, data.Count)
	}
	return nil
}

func (a *app) saveClear(args []string) error {
	args = append(args, "--all")
	return a.saveRemove(args)
}

// resolveCloudRoot 优先使用含有 userdata 的 Steam 根目录。scan 时如果传入的是
// 库目录（例如 D:\SteamLibrary），installed.SteamRoot 也会是库目录；此时再尝试
// 配置目录和常见 Steam 安装位置，避免云存档同步失败。
func (a *app) resolveCloudRoot(candidates ...string) string {
	seen := make(map[string]bool)
	var first string
	try := func(path string) string {
		path = strings.TrimSpace(expandUserPath(path))
		if path == "" || seen[path] {
			return ""
		}
		seen[path] = true
		if first == "" {
			first = path
		}
		if hasNumericDir(filepathJoinData(path, "userdata")) {
			return path
		}
		return ""
	}
	for _, candidate := range candidates {
		if root := try(candidate); root != "" {
			return root
		}
	}
	for _, candidate := range steam.DefaultRoots() {
		if root := try(candidate); root != "" {
			return root
		}
	}
	return first
}

func hasNumericDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.ParseUint(entry.Name(), 10, 32); err == nil {
			return true
		}
	}
	return false
}

func saveKindLabel(kind string) string {
	switch kind {
	case saves.KindCloud:
		return "云存档"
	case saves.KindLocal:
		return "本地存档"
	default:
		return kind
	}
}

func boolMark(v bool) string {
	if v {
		return "是"
	}
	return "-"
}

func filepathJoinData(root, rel string) string {
	rel = strings.ReplaceAll(rel, "/", string(os.PathSeparator))
	if rel == "" {
		return root
	}
	return strings.TrimRight(root, string(os.PathSeparator)) + string(os.PathSeparator) + rel
}

func filepathBaseLabel(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	base := parts[len(parts)-1]
	parent := ""
	if len(parts) > 1 {
		parent = parts[len(parts)-2]
	}
	if parent == "" {
		return base
	}
	return parent + "/" + base
}

func joinInts(ids []int) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return strings.Join(parts, ", ")
}

func printSaveHelp(w io.Writer) {
	fmt.Fprint(w, `云存档 / 本地存档同步

自动识别:
  云存档   <Steam根目录>/userdata/<账号ID>/<appid>/remote
  本地存档 游戏目录中的 savedata、save、saves、savegame 等常见目录
           （最多向下查找 3 层，可用 --local-dir 显式指定）

备份目录:
  <数据目录>/saves/<appid>/cloud/...
  <数据目录>/saves/<appid>/local/<目录名>/...
  <数据目录>/saves.json 记录每个存档的来源路径、文件数、大小与同步时间

用法:
  ugal save                             列出已同步的存档
  ugal save list [appid...]             列出存档记录
  ugal save list --paths                显示完整来源路径
  ugal save list --json                 机器可读输出
  ugal save sync [appid...]             同步云存档和本地存档到 <数据目录>/saves
  ugal save sync --no-cloud             只同步本地存档
  ugal save sync --no-local             只同步云存档
  ugal save sync --local-dir 目录       显式指定本地存档目录（可重复）
  ugal save sync --game-dir 目录        指定某个 appid 的游戏目录
  ugal save sync --steam-user 账号ID    指定 userdata 下的账号目录
  ugal save sync --steam-dir 目录       指定 Steam 根目录
  ugal save sync --force                备份已较新时也覆盖
  ugal save sync --mirror               删除备份中源目录已不存在的文件
  ugal save sync --dry-run              只统计，不写文件
  ugal save restore <appid...>          把备份写回云存档 / 游戏目录
  ugal save restore --all               还原所有记录
  ugal save restore --no-local          只还原云存档
  ugal save restore --force             目标文件更新时也覆盖
  ugal save restore --dry-run           只统计，不写文件
  ugal save remove <appid...>           删除存档记录和备份
  ugal save remove <appid...> --keep    只删除记录，保留备份
  ugal save clear                       清空所有存档记录和备份

说明:
  没有指定 --local-dir 时，会在游戏目录中自动查找名为 savedata、save、
  saves、savegame、savegames 等目录；找到多个时分别备份。
  同步默认只复制源目录中比备份新的文件，删除源文件不会自动删除备份，
  需要完全镜像时使用 --mirror。
  云端存档对应当前 Steam 账号；账号可在 config 中用 steam-user 设置。
`)
}
