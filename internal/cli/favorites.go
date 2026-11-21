package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/cover"
	"github.com/HanamiSakiP/u-game/internal/favorites"
	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/patches"
)

func (a *app) cmdFav(args []string) error {
	if len(args) == 0 {
		return a.favList(nil)
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls", "show":
		return a.favList(args[1:])
	case "add", "+":
		return a.favAdd(args[1:])
	case "status", "state", "set":
		return a.favStatus(args[1:])
	case "remove", "rm", "del", "delete":
		return a.favRemove(args[1:])
	case "import", "load":
		return a.favImport(args[1:])
	case "import-patches", "importpatches", "patch-import":
		return a.favImport(append([]string{"--patches"}, args[1:]...))
	case "export", "dump", "save":
		return a.favExport(args[1:])
	case "clear", "reset":
		return a.favClear(args[1:])
	case "help", "-h", "--help":
		printFavHelp(a.out)
		return nil
	default:
		if _, err := parseAppIDs(args); err == nil {
			return a.favAdd(args)
		}
		return fmt.Errorf("未知收藏夹命令 %q，运行 `ugal fav help` 查看帮助", args[0])
	}
}

func (a *app) loadFavorites() (*model.FavoritesData, error) {
	data, err := a.store.LoadFavorites()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &model.FavoritesData{Version: model.Version, Games: []model.Favorite{}}, nil
		}
		return nil, err
	}
	favorites.Normalize(data)
	return data, nil
}

func (a *app) favAdd(args []string) error {
	fs := flag.NewFlagSet("fav add", flag.ContinueOnError)
	fs.SetOutput(a.err)
	nameFlag := fs.String("name", "", "指定游戏名称（仅添加一个 appid 时可用）")
	noteFlag := fs.String("note", "", "备注")
	statusFlag := fs.String("status", "", "收藏状态（想玩/在玩/已通关/搁置，或自定义）")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal fav add <appid...> [--name 名称] [--note 备注] [--status 状态]")
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
	if len(ids) == 0 {
		return errors.New("请提供至少一个 appid，例如 `ugal fav add 440`")
	}
	if *nameFlag != "" && len(ids) != 1 {
		return errors.New("--name 只能在添加一个 appid 时使用")
	}
	names, err := a.gameNames()
	if err != nil {
		return err
	}
	status := favorites.NormalizeStatus(*statusFlag)
	items := make([]model.Favorite, 0, len(ids))
	for _, id := range ids {
		name := names[id]
		if *nameFlag != "" {
			name = *nameFlag
		}
		items = append(items, model.Favorite{AppID: id, Name: name, Note: *noteFlag, Status: status})
	}
	data, err := a.loadFavorites()
	if err != nil {
		return err
	}
	added := favorites.Add(data, items)
	if err := a.store.SaveFavorites(data); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已添加 %d 个，收藏夹共 %d 个\n", added, data.Count)
	return nil
}

// favStatus 查看或设置收藏状态。支持：
//   - ugal fav status                显示全部收藏的状态
//   - ugal fav status 440 220        显示指定收藏的状态
//   - ugal fav status 440 在玩        设置状态（可一次设置多个 appid）
//   - ugal fav status 440 clear      清除状态（clear/未标记/--clear 均可）
func (a *app) favStatus(args []string) error {
	fs := flag.NewFlagSet("fav status", flag.ContinueOnError)
	fs.SetOutput(a.err)
	clearFlag := fs.Bool("clear", false, "清除收藏状态")
	statusFlag := fs.String("status", "", "收藏状态（也可直接写在 appid 之后）")
	offline := fs.Bool("offline", false, "显示时不为缺失的名称联网")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal fav status [appid...] [状态|clear] [--clear]")
		fmt.Fprintf(a.err, "  状态可选: %s，也可以直接写自定义状态\n", strings.Join(favorites.StatusValues(), "/"))
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()

	status := ""
	hasStatus := *clearFlag
	if value := strings.TrimSpace(*statusFlag); value != "" {
		status = value
		hasStatus = true
	}
	if len(rest) > 0 {
		last := strings.TrimSpace(rest[len(rest)-1])
		if _, err := strconv.Atoi(last); err != nil {
			if hasStatus {
				return errors.New("状态只能指定一次：位置参数或 --status")
			}
			status = last
			hasStatus = true
			rest = rest[:len(rest)-1]
		}
	}
	if hasStatus {
		status = favorites.NormalizeStatus(status)
	}

	ids, err := parseAppIDs(rest)
	if err != nil {
		return err
	}
	data, err := a.loadFavorites()
	if err != nil {
		return err
	}

	if !hasStatus {
		return a.printFavoriteStatus(data, ids, *offline)
	}
	if len(ids) == 0 {
		return errors.New("请提供至少一个 appid，例如 `ugal fav status 440 在玩`")
	}
	updated := 0
	for _, id := range ids {
		idx := favoriteIndex(data, id)
		if idx < 0 {
			fmt.Fprintf(a.err, "跳过 %d：不在收藏夹中\n", id)
			continue
		}
		data.Games[idx].Status = status
		updated++
	}
	if updated == 0 {
		return errors.New("没有更新任何收藏：appid 都不在收藏夹中")
	}
	if err := a.store.SaveFavorites(data); err != nil {
		return err
	}
	if status == "" {
		fmt.Fprintf(a.out, "已清除 %d 个收藏的状态\n", updated)
	} else {
		fmt.Fprintf(a.out, "已将 %d 个收藏的状态设为 %s\n", updated, status)
	}
	return nil
}

// printFavoriteStatus 以表格显示收藏状态；ids 为空时显示全部收藏。
func (a *app) printFavoriteStatus(data *model.FavoritesData, ids []int, offline bool) error {
	if data == nil || len(data.Games) == 0 {
		fmt.Fprintln(a.out, "收藏夹为空，使用 `ugal fav add <appid>` 添加游戏。")
		return nil
	}
	wanted := make(map[int]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	selected := make([]model.Favorite, 0, len(data.Games))
	for _, g := range data.Games {
		if len(wanted) == 0 || wanted[g.AppID] {
			selected = append(selected, g)
		}
	}
	if len(selected) == 0 {
		fmt.Fprintln(a.out, "没有符合条件的收藏。")
		return nil
	}

	// 收藏里缺少名称时才回退到本地/网络名称。
	missing := make([]int, 0, len(selected))
	for _, g := range selected {
		if strings.TrimSpace(g.Name) == "" {
			missing = append(missing, g.AppID)
		}
	}
	names := make(map[int]string)
	if len(missing) > 0 {
		if resolved, err := a.resolveGameNames(missing, offline); err == nil {
			names = resolved
		}
	}

	headers := []string{"APPID", "名称", "收藏状态"}
	right := []bool{true, false, false}
	rows := make([][]string, 0, len(selected))
	for _, g := range selected {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			name = names[g.AppID]
		}
		if name == "" {
			name = fmt.Sprintf("App %d", g.AppID)
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", g.AppID),
			truncateDisplay(name, 48),
			favorites.StatusLabel(g.Status),
		})
	}
	renderTable(a.out, headers, right, rows)
	return nil
}

func favoriteIndex(data *model.FavoritesData, appid int) int {
	if data == nil {
		return -1
	}
	for i := range data.Games {
		if data.Games[i].AppID == appid {
			return i
		}
	}
	return -1
}

func (a *app) favRemove(args []string) error {
	fs := flag.NewFlagSet("fav remove", flag.ContinueOnError)
	fs.SetOutput(a.err)
	all := fs.Bool("all", false, "清空收藏夹")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal fav remove <appid...> | --all")
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
	data, err := a.loadFavorites()
	if err != nil {
		return err
	}
	removed := 0
	if *all {
		removed = len(data.Games)
		data.Games = nil
	} else {
		removed = favorites.Remove(data, ids)
	}
	if err := a.store.SaveFavorites(data); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已删除 %d 个，收藏夹共 %d 个\n", removed, data.Count)
	return nil
}

type favoriteRow struct {
	AppID          int           `json:"appid"`
	Name           string        `json:"name,omitempty"`
	Note           string        `json:"note,omitempty"`
	Publisher      string        `json:"publisher,omitempty"`
	AddedAt        time.Time     `json:"added_at,omitempty"`
	Status         string        `json:"status"`
	FavoriteStatus string        `json:"favorite_status,omitempty"`
	Type           string        `json:"type,omitempty"`
	Installed      bool          `json:"installed"`
	Cover          string        `json:"cover,omitempty"`
	CoverSize      int64         `json:"cover_size,omitempty"`
	Patch          *model.Patch  `json:"patch,omitempty"`
	Patches        []model.Patch `json:"patches,omitempty"`
}

func (a *app) favList(args []string) error {
	fs := flag.NewFlagSet("fav list", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	installedOnly := fs.Bool("installed", false, "只看本地已安装")
	missingOnly := fs.Bool("missing", false, "只看未安装")
	statusFlag := fs.String("status", "", "只看指定收藏状态（逗号分隔；none 表示未标记）")
	paths := fs.Bool("paths", false, "显示完整封面路径")
	offline := fs.Bool("offline", false, "不联网获取发行商")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal fav [list] [--json] [--installed|--missing] [--status 状态] [--paths] [--offline]")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("fav list 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	if *installedOnly && *missingOnly {
		return errors.New("--installed 和 --missing 只能选择一个")
	}
	statusFilter := parseFavoriteStatusFilter(*statusFlag)
	data, err := a.loadFavorites()
	if err != nil {
		return err
	}
	owned, installed, err := a.loadData()
	if err != nil {
		return err
	}
	patchData, err := a.loadPatches()
	if err != nil {
		return err
	}
	entryMap := make(map[int]entry)
	if owned != nil || installed != nil {
		all, err := buildEntries(owned, installed)
		if err != nil {
			return err
		}
		for _, e := range all {
			entryMap[e.AppID] = e
		}
	}

	rows := make([]favoriteRow, 0, len(data.Games))
	needPublisher := make([]int, 0, len(data.Games))
	for _, g := range data.Games {
		e, ok := entryMap[g.AppID]
		status := "unknown"
		typ := ""
		isInstalled := false
		if ok {
			status = e.Status
			typ = e.Type
			isInstalled = e.Status == "installed" || e.Status == "extra"
		}
		if *installedOnly && !isInstalled {
			continue
		}
		if *missingOnly && isInstalled {
			continue
		}
		favStatus := favorites.NormalizeStatus(g.Status)
		if statusFilter != nil && !statusFilter[favStatus] {
			continue
		}
		name := g.Name
		if name == "" {
			name = e.Name
		}
		if name == "" {
			name = fmt.Sprintf("App %d", g.AppID)
		}
		patchList := patches.FindAll(patchData, g.AppID)
		var firstPatch *model.Patch
		if len(patchList) > 0 {
			p := patchList[0]
			firstPatch = &p
		}
		row := favoriteRow{
			AppID:          g.AppID,
			Name:           name,
			Note:           g.Note,
			AddedAt:        g.AddedAt,
			Status:         status,
			FavoriteStatus: favStatus,
			Type:           typ,
			Installed:      isInstalled,
			Patch:          firstPatch,
			Patches:        patchList,
		}
		if strings.TrimSpace(g.Note) == "" {
			// 没有备注时用发行商作为默认分组。
			needPublisher = append(needPublisher, g.AppID)
		}
		if f := cover.Find(a.store.CoversDir(), g.AppID); f != "" {
			row.Cover = f
			if fi, err := os.Stat(f); err == nil {
				row.CoverSize = fi.Size()
			}
		}
		rows = append(rows, row)
	}
	if len(needPublisher) > 0 {
		publishers, err := a.resolvePublishers(needPublisher, *offline)
		if err != nil {
			return err
		}
		for i := range rows {
			rows[i].Publisher = groupPublisher(publishers[rows[i].AppID])
		}
	}

	// 按备注分组排序：有备注的按备注名称排列，没有备注的用发行商作为分组，
	// 两者都没有的统一归入「无分组」并排在最后；同一分组内按游戏名称排序。
	sort.SliceStable(rows, func(i, j int) bool {
		return favoriteSortLess(favoriteGroup(rows[i]), rows[i].Name, rows[i].AppID,
			favoriteGroup(rows[j]), rows[j].Name, rows[j].AppID)
	})

	installedCount := 0
	for _, r := range rows {
		if r.Installed {
			installedCount++
		}
	}
	if *asJSON {
		payload := struct {
			Total     int           `json:"total"`
			Installed int           `json:"installed"`
			Missing   int           `json:"missing"`
			Games     []favoriteRow `json:"games"`
		}{
			Total:     len(rows),
			Installed: installedCount,
			Missing:   len(rows) - installedCount,
			Games:     rows,
		}
		return writeJSONOutput(a.out, payload)
	}

	if len(rows) == 0 {
		if len(data.Games) == 0 {
			fmt.Fprintln(a.out, "收藏夹为空，使用 `ugal fav add <appid>` 添加游戏。")
			return nil
		}
		fmt.Fprintln(a.out, "没有符合筛选条件的收藏。")
		return nil
	}

	headers := []string{"APPID", "安装", "收藏状态", "类型", "补丁", "哈希", "名称", "封面", "备注/发行商"}
	right := []bool{true, false, false, false, false, false, false, false, false}
	groups := make([]tableGroup, 0)
	for _, r := range rows {
		coverCell := "有"
		if r.Cover == "" {
			coverCell = "-"
		} else if *paths {
			coverCell = r.Cover
		}
		group := favoriteGroup(r)
		// rows 已按分组排序，同组数据行的分组名必然相邻。
		if len(groups) == 0 || groups[len(groups)-1].Label != group {
			groups = append(groups, tableGroup{Label: group})
		}
		displayGroup := group
		if displayGroup == "" {
			displayGroup = "无分组"
		}
		groups[len(groups)-1].Rows = append(groups[len(groups)-1].Rows, []string{
			fmt.Sprintf("%d", r.AppID),
			statusLabel(r.Status),
			favorites.StatusLabel(r.FavoriteStatus),
			typeLabel(r.Type),
			patchStatusAll(r.Patches),
			patchHashCell(r.Patches),
			truncateDisplay(r.Name, 48),
			coverCell,
			truncateDisplay(displayGroup, 48),
		})
	}
	for i := range groups {
		name := groups[i].Label
		if name == "" {
			name = "无分组"
		}
		groups[i].Label = fmt.Sprintf("%s（%d 个）", truncateDisplay(name, 48), len(groups[i].Rows))
	}
	renderGroupedTable(a.out, headers, right, groups)
	fmt.Fprintf(a.out, "\n收藏 %d 个 | 已安装 %d 个 | 未安装 %d 个\n",
		len(rows), installedCount, len(rows)-installedCount)
	if line := favoriteStatusSummary(rows); line != "" {
		fmt.Fprintf(a.out, "收藏状态: %s\n", line)
	}
	return nil
}

// parseFavoriteStatusFilter 解析 --status：返回 nil 表示不过滤；
// 支持逗号分隔的多个状态，none / 未标记 表示空状态。
func parseFavoriteStatusFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "all") || raw == "全部" {
		return nil
	}
	want := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.EqualFold(part, "all") || part == "全部" {
			return nil
		}
		if strings.EqualFold(part, "none") || strings.EqualFold(part, "untagged") || part == "未标记" || part == "无状态" {
			want[""] = true
			continue
		}
		want[favorites.NormalizeStatus(part)] = true
	}
	if len(want) == 0 {
		return nil
	}
	return want
}

// favoriteStatusSummary 汇总收藏状态，全部未标记时返回空字符串。
func favoriteStatusSummary(rows []favoriteRow) string {
	counts := make(map[string]int, len(rows))
	for _, r := range rows {
		counts[favorites.NormalizeStatus(r.FavoriteStatus)]++
	}
	var parts []string
	seen := make(map[string]bool)
	appendStatus := func(status string) {
		if n := counts[status]; n > 0 && !seen[status] {
			seen[status] = true
			if status == "" {
				parts = append(parts, fmt.Sprintf("未标记 %d 个", n))
			} else {
				parts = append(parts, fmt.Sprintf("%s %d 个", status, n))
			}
		}
	}
	for _, status := range favorites.StatusValues() {
		appendStatus(status)
	}
	// 自定义状态按名称排序，保证输出稳定。
	var custom []string
	for status := range counts {
		if status == "" {
			continue
		}
		known := false
		for _, preset := range favorites.StatusValues() {
			if status == preset {
				known = true
				break
			}
		}
		if !known {
			custom = append(custom, status)
		}
	}
	sort.Strings(custom)
	for _, status := range custom {
		appendStatus(status)
	}
	appendStatus("")
	if len(parts) == 1 && counts[""] == len(rows) {
		return ""
	}
	return strings.Join(parts, " | ")
}

// favoriteGroup 返回收藏的分组名：优先使用备注，没有备注时使用发行商。
// 有备注时备注始终保留，不会被发行商分组覆盖。
func favoriteGroup(r favoriteRow) string {
	if note := strings.TrimSpace(r.Note); note != "" {
		return note
	}
	return strings.TrimSpace(r.Publisher)
}

// favoriteSortLess 比较两条收藏的排序：有备注/发行商分组的排在前面，
// 分组名不区分大小写排序，同一分组内按游戏名称排序，最后按 appid。
// fav list 和 TUI 收藏视图共用这套顺序。
func favoriteSortLess(groupA, nameA string, appIDa int, groupB, nameB string, appIDb int) bool {
	if (groupA == "") != (groupB == "") {
		return groupB == ""
	}
	if !strings.EqualFold(groupA, groupB) {
		return strings.ToLower(groupA) < strings.ToLower(groupB)
	}
	na, nb := strings.ToLower(nameA), strings.ToLower(nameB)
	if na != nb {
		return na < nb
	}
	return appIDa < appIDb
}

// publisherGroupOverrides 把多发行商组合归并到指定的主发行商。
// 键为小写后的完整组合，未列出的组合保持原样。
var publisherGroupOverrides = map[string]string{
	"hikari field / nekonyan ltd.":    "HIKARI FIELD",
	"nekonyan ltd. / hikari field":    "HIKARI FIELD",
	"tamamako gaming / sekai project": "Sekai Project",
	"sekai project / tamamako gaming": "Sekai Project",
	"kagura games / nekonyan ltd.":    "NekoNyan Ltd.",
	"nekonyan ltd. / kagura games":    "NekoNyan Ltd.",
}

// groupPublisher 返回用于分组和显示的发行商名称。
func groupPublisher(publisher string) string {
	publisher = strings.TrimSpace(publisher)
	if publisher == "" {
		return ""
	}
	if canonical, ok := publisherGroupOverrides[strings.ToLower(publisher)]; ok {
		return canonical
	}
	return publisher
}

func (a *app) favExport(args []string) error {
	fs := flag.NewFlagSet("fav export", flag.ContinueOnError)
	fs.SetOutput(a.err)
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal fav export [文件]（省略文件或使用 - 输出到标准输出）")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return errors.New("fav export 最多接受一个文件参数")
	}
	data, err := a.loadFavorites()
	if err != nil {
		return err
	}
	raw, err := favorites.Marshal(data)
	if err != nil {
		return err
	}
	if len(rest) == 0 || rest[0] == "-" {
		_, err := a.out.Write(raw)
		return err
	}
	if err := writeFileAtomic(rest[0], raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已导出 %d 个收藏到 %s\n", data.Count, rest[0])
	return nil
}

func (a *app) favImport(args []string) error {
	fs := flag.NewFlagSet("fav import", flag.ContinueOnError)
	fs.SetOutput(a.err)
	replace := fs.Bool("replace", false, "用导入内容替换现有收藏夹")
	fromPatches := fs.Bool("patches", false, "从 patches.json 导入收藏（省略文件时默认）")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal fav import [文件] [--replace] [--patches]")
		fmt.Fprintln(a.err, "  省略文件或使用 --patches 时读取 <数据目录>/patches.json；文件为 - 时读取标准输入")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return errors.New("fav import 最多接受一个文件参数")
	}
	path := ""
	if len(rest) == 1 {
		path = rest[0]
	}
	if *fromPatches {
		if path != "" {
			return errors.New("--patches 不能与文件参数同时使用")
		}
		path = a.store.PatchesPath()
	}
	return a.importFavorites(path, *replace)
}

// importFavorites 读取 path 中的收藏（path 为空时使用 patches.json）并写入收藏夹。
// patches.json 里的补丁名称不是游戏名称，会按 appid 重新解析。
func (a *app) importFavorites(path string, replace bool) error {
	if strings.TrimSpace(path) == "" {
		path = a.store.PatchesPath()
	}
	var (
		raw []byte
		err error
	)
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("没有找到导入文件 %s（运行任意命令会自动创建 patches.json）", path)
		}
		return fmt.Errorf("读取导入文件失败: %w", err)
	}
	incoming, err := favorites.Parse(raw)
	if err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	names, err := a.gameNames()
	if err != nil {
		return err
	}
	for i := range incoming.Games {
		if incoming.Games[i].Name == "" {
			incoming.Games[i].Name = names[incoming.Games[i].AppID]
		}
	}

	var data *model.FavoritesData
	added := 0
	if replace {
		data = incoming
		added = len(incoming.Games)
	} else {
		data, err = a.loadFavorites()
		if err != nil {
			return err
		}
		added = favorites.Add(data, incoming.Games)
	}
	if err := a.store.SaveFavorites(data); err != nil {
		return err
	}
	source := path
	if source == "-" {
		source = "标准输入"
	}
	fmt.Fprintf(a.out, "从 %s 导入 %d 个，新增 %d 个，收藏夹共 %d 个\n",
		source, len(incoming.Games), added, data.Count)
	return nil
}

func (a *app) favClear(args []string) error {
	fs := flag.NewFlagSet("fav clear", flag.ContinueOnError)
	fs.SetOutput(a.err)
	fs.Usage = func() { fmt.Fprintln(a.err, "用法: ugal fav clear") }
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("fav clear 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	data, err := a.loadFavorites()
	if err != nil {
		return err
	}
	removed := len(data.Games)
	data.Games = nil
	if err := a.store.SaveFavorites(data); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已清空收藏夹（%d 个）\n", removed)
	return nil
}

func printFavHelp(w io.Writer) {
	fmt.Fprint(w, `本地游戏收藏夹

用法:
  ugal fav                              查看收藏夹（等同 fav list）
  ugal fav list [--json]                列出收藏
  ugal fav list --installed             只看已安装
  ugal fav list --missing               只看未安装
  ugal fav list --status 在玩           只看指定收藏状态（可逗号分隔，none 为未标记）
  ugal fav list --offline               不联网获取发行商
  ugal fav add <appid...>               添加游戏
  ugal fav add <appid> --name 名称      自定义名称
  ugal fav add <appid> --status 在玩    添加时设置收藏状态
  ugal fav status                       查看全部收藏状态
  ugal fav status <appid...>            查看指定收藏的状态
  ugal fav status <appid...> 状态        设置收藏状态（想玩/在玩/已通关/搁置，或自定义）
  ugal fav status <appid...> clear      清除收藏状态
  ugal fav remove <appid...>            删除游戏
  ugal fav remove --all                 清空收藏夹
  ugal fav import <文件> [--replace]    导入 JSON / appid 列表
  ugal fav import [--replace]           省略文件时从 patches.json 导入（一键导入）
  ugal fav import-patches [--replace]   等同 fav import --patches
  ugal fav export [文件]                导出 JSON（省略文件输出到终端）
  ugal fav clear                        清空收藏夹

支持导入的格式:
  ugal 导出的 favorites.json
  {"games":[{"appid":440,"name":"Team Fortress 2"}]}
  patches.json（{"patches":[{"appid":440,...}]}，按 appid 导入）
  [440, 220]
  每行一个 appid，或 "appid,名称"
  列表优先按备注分组，没有备注时按发行商分组（联网获取），两者都没有则归入「无分组」
  多发行商组合归并规则:
    HIKARI FIELD / NekoNyan Ltd. 与 NekoNyan Ltd. / HIKARI FIELD 归为 HIKARI FIELD
    TamaMako gaming / Sekai Project 归为 Sekai Project（反向组合同样处理）
    Kagura Games / NekoNyan Ltd. 归为 NekoNyan Ltd.（反向组合同样处理）
  有备注的收藏仍然按备注分组，备注不会被发行商覆盖
  每组前以红色显示分组名称和该组数量，例如 "Valve（2 个）"
`)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".ugal-*")
	if err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("设置 %s 权限失败: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("保存 %s 失败: %w", path, err)
	}
	return nil
}
