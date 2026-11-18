package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/patches"
)

func (a *app) cmdPatch(args []string) error {
	if len(args) == 0 {
		return a.patchList(nil)
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls", "show", "status":
		return a.patchList(args[1:])
	case "set", "add", "update":
		return a.patchSet(args[1:])
	case "remove", "rm", "del", "delete":
		return a.patchRemove(args[1:])
	case "clear", "reset":
		return a.patchClear(args[1:])
	case "hash":
		return a.patchHash(args[1:])
	case "md5":
		return a.patchHash(append([]string{"--md5"}, args[1:]...))
	case "sha256":
		return a.patchHash(append([]string{"--sha256"}, args[1:]...))
	case "check", "verify", "validate":
		return a.patchCheck(args[1:])
	case "install", "apply":
		return a.patchInstall(args[1:])
	case "help", "-h", "--help":
		printPatchHelp(a.out)
		return nil
	default:
		if _, err := parseAppIDs(args); err == nil {
			return a.patchShow(args)
		}
		return fmt.Errorf("未知补丁命令 %q，运行 `ugal patch help` 查看帮助", args[0])
	}
}

func (a *app) loadPatches() (*model.PatchesData, error) {
	data, err := a.store.LoadPatches()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &model.PatchesData{Version: model.Version, Patches: []model.Patch{}}, nil
		}
		return nil, err
	}
	patches.Normalize(data)
	return data, nil
}

func patchStatus(p *model.Patch) string {
	return patches.StatusLabel(p)
}

func patchStatusAll(ps []model.Patch) string {
	return patches.StatusLabelAll(ps)
}

// patchHashCell 把补丁哈希压缩成表格单元：每个哈希只保留前 8 位，
// 多个补丁用逗号分隔，整体过长时截断。
func patchHashCell(ps []model.Patch) string {
	if len(ps) == 0 {
		return "-"
	}
	hashes := make([]string, 0, len(ps))
	for _, p := range ps {
		h := patches.NormalizeHash(p.Hash)
		if h == "" {
			continue
		}
		if len(h) > 8 {
			h = h[:8]
		}
		hashes = append(hashes, h)
	}
	if len(hashes) == 0 {
		return "-"
	}
	return truncateDisplay(strings.Join(hashes, ","), 24)
}

func attachPatches(entries []entry, data *model.PatchesData) {
	for i := range entries {
		ps := patches.FindAll(data, entries[i].AppID)
		entries[i].Patches = ps
		if len(ps) == 0 {
			entries[i].Patch = nil
			continue
		}
		p := ps[0]
		entries[i].Patch = &p
	}
}

type patchRow struct {
	AppID     int       `json:"appid"`
	Game      string    `json:"game,omitempty"`
	Status    string    `json:"status"`
	Name      string    `json:"name,omitempty"`
	Hash      string    `json:"hash,omitempty"`
	Applied   bool      `json:"applied"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (a *app) patchShow(args []string) error {
	return a.patchList(append([]string{"--all"}, args...))
}

func (a *app) patchList(args []string) error {
	fs := flag.NewFlagSet("patch list", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	all := fs.Bool("all", false, "包含没有补丁记录的已知游戏（显示「无补丁」）")
	offline := fs.Bool("offline", false, "不联网获取缺失的游戏名称")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch [list] [appid...] [--all] [--json] [--offline]")
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
	filter := make(map[int]bool, len(ids))
	for _, id := range ids {
		filter[id] = true
	}

	data, err := a.loadPatches()
	if err != nil {
		return err
	}

	var rows []patchRow
	patchCounts := make(map[int]int, len(data.Patches))
	if *all {
		owned, installed, err := a.loadData()
		if err != nil {
			return err
		}
		entries, err := buildEntries(owned, installed)
		if err != nil {
			return err
		}
		if names, err := a.resolveGameNames(appIDsFromEntries(entries), *offline); err == nil {
			applyGameNames(entries, names)
		}
		attachPatches(entries, data)
		for _, e := range entries {
			if len(filter) > 0 && !filter[e.AppID] {
				continue
			}
			patchCounts[e.AppID] = len(e.Patches)
			if len(e.Patches) == 0 {
				rows = append(rows, newPatchRow(e.AppID, e.Name, nil))
				continue
			}
			for i := range e.Patches {
				pcopy := e.Patches[i]
				rows = append(rows, newPatchRow(e.AppID, e.Name, &pcopy))
			}
		}
	} else {
		patchIDs := make([]int, 0, len(data.Patches))
		for _, p := range data.Patches {
			if len(filter) > 0 && !filter[p.AppID] {
				continue
			}
			patchIDs = append(patchIDs, p.AppID)
		}
		names, err := a.resolveGameNames(patchIDs, *offline)
		if err != nil {
			return err
		}
		for _, p := range data.Patches {
			if len(filter) > 0 && !filter[p.AppID] {
				continue
			}
			patchCounts[p.AppID]++
			pcopy := p
			rows = append(rows, newPatchRow(p.AppID, names[p.AppID], &pcopy))
		}
	}

	// 补丁数量多的游戏排在前面，多个补丁文件的记录不会被单条记录打散。
	// 数量相同时保持原有顺序（appid 升序）。
	sort.SliceStable(rows, func(i, j int) bool {
		return patchCounts[rows[i].AppID] > patchCounts[rows[j].AppID]
	})

	var applied, unpatched, partial, noPatch int
	for _, r := range rows {
		switch r.Status {
		case "已打补丁":
			applied++
		case "无补丁":
			noPatch++
		case "未打补丁":
			unpatched++
		default:
			partial++
		}
	}
	total := applied + unpatched + partial

	if *asJSON {
		payload := struct {
			Total     int        `json:"total"`
			Applied   int        `json:"applied"`
			Unpatched int        `json:"unpatched"`
			NoPatch   int        `json:"no_patch"`
			Patches   []patchRow `json:"patches"`
		}{Total: len(rows), Applied: applied, Unpatched: unpatched + partial, NoPatch: noPatch, Patches: rows}
		return writeJSONOutput(a.out, payload)
	}

	if len(rows) == 0 {
		if *all {
			fmt.Fprintln(a.out, "没有可显示的已安装游戏。")
		} else {
			fmt.Fprintf(a.out, "还没有补丁记录。可直接编辑补丁记录文件 %s，或使用 `ugal patch set <appid> --name 名称 --file 补丁文件` 添加。\n", a.store.PatchesPath())
		}
		return nil
	}

	headers := []string{"APPID", "状态", "游戏", "补丁名称", "哈希", "更新时间"}
	right := []bool{true, false, false, false, false, false}
	useColor := colorEnabled(a.out)
	tableRows := make([][]string, 0, len(rows))
	for _, r := range rows {
		hash := r.Hash
		if hash == "" {
			hash = "-"
		} else {
			hash = truncateDisplay(hash, 20)
		}
		name := r.Name
		if name == "" {
			name = "-"
		}
		updated := "-"
		if !r.UpdatedAt.IsZero() {
			updated = r.UpdatedAt.Local().Format("2006-01-02 15:04")
		}

		status := r.Status
		if status == "未打补丁" {
			status = colorize(useColor, ansiRed, status)
		}
		placeholder := fmt.Sprintf("App %d", r.AppID)
		game := truncateDisplay(orPlaceholder(r.Game, placeholder), 40)
		if strings.TrimSpace(r.Game) == "" || strings.TrimSpace(r.Game) == placeholder {
			game = colorize(useColor, ansiRed, game)
		}
		tableRows = append(tableRows, []string{
			fmt.Sprintf("%d", r.AppID),
			status,
			game,
			truncateDisplay(name, 32),
			hash,
			updated,
		})
	}
	renderTable(a.out, headers, right, tableRows)
	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "共 %d 个补丁", total)
	parts := make([]string, 0, 4)
	if applied > 0 {
		parts = append(parts, fmt.Sprintf("已打补丁 %d 个", applied))
	}
	if unpatched > 0 {
		parts = append(parts, colorize(useColor, ansiRed, fmt.Sprintf("未打补丁 %d 个", unpatched)))
	}
	if partial > 0 {
		parts = append(parts, fmt.Sprintf("部分已打 %d 个", partial))
	}
	if noPatch > 0 {
		parts = append(parts, fmt.Sprintf("无补丁 %d 个", noPatch))
	}
	if len(parts) > 0 {
		fmt.Fprintf(a.out, "（%s）", strings.Join(parts, "，"))
	}
	fmt.Fprintln(a.out)
	return nil
}

func newPatchRow(appid int, game string, p *model.Patch) patchRow {
	row := patchRow{
		AppID:  appid,
		Game:   game,
		Status: patches.StatusLabel(p),
	}
	if p != nil {
		row.Name = p.Name
		row.Hash = p.Hash
		row.Applied = p.Applied
		row.UpdatedAt = p.UpdatedAt
	}
	return row
}

// repeatedFiles 支持重复指定 --file，例如一次记录多个补丁文件。
type repeatedFiles []string

func (f *repeatedFiles) String() string {
	return strings.Join(*f, ", ")
}

func (f *repeatedFiles) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func (a *app) patchSet(args []string) error {
	fs := flag.NewFlagSet("patch set", flag.ContinueOnError)
	fs.SetOutput(a.err)
	name := fs.String("name", "", "补丁名称，默认使用补丁文件名")
	hash := fs.String("hash", "", "补丁哈希值")
	var files repeatedFiles
	fs.Var(&files, "file", "从补丁文件计算哈希（默认 MD5）并记录，可重复指定")
	md5Mode := fs.Bool("md5", false, "计算文件 MD5（默认，需与 --file 一起使用）")
	sha256Mode := fs.Bool("sha256", false, "计算文件 SHA-256（需与 --file 一起使用）")
	applied := fs.Bool("applied", false, "标记为已打补丁")
	notApplied := fs.Bool("not-applied", false, "标记为未打补丁")
	offline := fs.Bool("offline", false, "不联网获取缺失的游戏名称")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch set <appid> [--name 名称] [--hash 哈希|--file 文件 [--file 文件...] [--md5|--sha256]] [--applied|--not-applied] [--offline]")
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
	if len(ids) != 1 {
		return errors.New("patch set 需要一个 appid")
	}
	if *applied && *notApplied {
		return errors.New("--applied 和 --not-applied 不能同时使用")
	}
	nameValue := strings.TrimSpace(*name)
	hashValue := strings.TrimSpace(*hash)
	if hashValue != "" && len(files) > 0 {
		return errors.New("--hash 和 --file 不能同时使用")
	}
	if *md5Mode && *sha256Mode {
		return errors.New("--md5 和 --sha256 不能同时使用")
	}
	if *md5Mode && len(files) == 0 {
		return errors.New("--md5 需要与 --file 一起使用")
	}
	if *sha256Mode && len(files) == 0 {
		return errors.New("--sha256 需要与 --file 一起使用")
	}
	if len(files) > 1 && nameValue != "" {
		return errors.New("指定多个 --file 时不能使用 --name（每个文件按文件名分别记录）")
	}
	if nameValue == "" && hashValue == "" && len(files) == 0 && !*applied && !*notApplied {
		return errors.New("请至少指定 --name、--hash、--file、--applied 或 --not-applied")
	}

	id := ids[0]
	data, err := a.loadPatches()
	if err != nil {
		return err
	}

	algorithm := "md5"
	if *sha256Mode {
		algorithm = "sha256"
	}

	switch {
	case len(files) > 0:
		// 每个补丁文件一条记录，名称默认取文件名；同名文件再次记录时更新原记录。
		for _, file := range files {
			file = strings.TrimSpace(file)
			if file == "" {
				return errors.New("补丁文件路径不能为空")
			}
			patchName := nameValue
			if patchName == "" {
				patchName = filepath.Base(file)
			}
			patch := model.Patch{AppID: id, Name: patchName}
			if old := patches.FindByName(data, id, patchName); old != nil {
				patch = *old
				patch.Name = patchName
			}
			h, err := patches.HashFileWith(file, algorithm)
			if err != nil {
				return err
			}
			patch.Hash = h
			if *applied {
				patch.Applied = true
			} else if *notApplied {
				patch.Applied = false
			}
			patches.Set(data, patch)
		}

	case nameValue != "":
		// 按名称精确定位一条记录；不存在时新建。
		patch := model.Patch{AppID: id, Name: nameValue}
		if old := patches.FindByName(data, id, nameValue); old != nil {
			patch = *old
			patch.Name = nameValue
		}
		if hashValue != "" {
			patch.Hash = patches.NormalizeHash(hashValue)
		}
		if *applied {
			patch.Applied = true
		} else if *notApplied {
			patch.Applied = false
		}
		patches.Set(data, patch)

	case hashValue != "":
		existing := patches.FindAll(data, id)
		if len(existing) > 1 {
			return errors.New("该 appid 有多条补丁记录，请使用 --name 指定要更新的补丁")
		}
		patch := model.Patch{AppID: id}
		if len(existing) == 1 {
			patch = existing[0]
		}
		patch.Hash = patches.NormalizeHash(hashValue)
		if *applied {
			patch.Applied = true
		} else if *notApplied {
			patch.Applied = false
		}
		patches.Set(data, patch)

	default:
		// 只修改打补丁状态：未指定名称时作用于该 appid 的全部补丁记录。
		existing := patches.FindAll(data, id)
		if len(existing) == 0 {
			patch := model.Patch{AppID: id, Applied: *applied}
			patches.Set(data, patch)
			break
		}
		for _, patch := range existing {
			if *applied {
				patch.Applied = true
			} else if *notApplied {
				patch.Applied = false
			}
			patches.Set(data, patch)
		}
	}

	if err := a.store.SavePatches(data); err != nil {
		return err
	}

	names, err := a.resolveGameNames([]int{id}, *offline)
	if err != nil {
		return err
	}
	game := names[id]
	if game == "" {
		game = fmt.Sprintf("App %d", id)
	}
	recorded := patches.FindAll(data, id)
	if len(recorded) == 1 {
		p := recorded[0]
		detail := ""
		if p.Name != "" {
			detail = "，补丁 " + p.Name
		}
		if p.Hash != "" {
			detail += "，哈希 " + truncateDisplay(p.Hash, 20)
		}
		fmt.Fprintf(a.out, "已记录 %s（%d）补丁状态：%s%s\n", game, id, patchStatus(&p), detail)
		return nil
	}
	fmt.Fprintf(a.out, "已记录 %s（%d）的 %d 个补丁，整体状态：%s\n", game, id, len(recorded), patchStatusAll(recorded))
	for i := range recorded {
		p := recorded[i]
		patchName := p.Name
		if patchName == "" {
			patchName = "未命名"
		}
		detail := ""
		if p.Hash != "" {
			detail = "，哈希 " + truncateDisplay(p.Hash, 20)
		}
		fmt.Fprintf(a.out, "  - %s：%s%s\n", patchName, patchStatus(&p), detail)
	}
	return nil
}

func (a *app) patchRemove(args []string) error {
	fs := flag.NewFlagSet("patch remove", flag.ContinueOnError)
	fs.SetOutput(a.err)
	all := fs.Bool("all", false, "清空所有补丁记录")
	name := fs.String("name", "", "只删除指定名称的补丁记录")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch remove <appid...> [--name 名称] | --all")
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
	if *all && strings.TrimSpace(*name) != "" {
		return errors.New("--all 和 --name 不能同时使用")
	}
	if !*all && len(ids) == 0 {
		return errors.New("请提供要删除的 appid，或使用 --all")
	}
	nameValue := strings.TrimSpace(*name)
	if nameValue != "" && len(ids) != 1 {
		return errors.New("--name 只能与一个 appid 一起使用")
	}
	data, err := a.loadPatches()
	if err != nil {
		return err
	}
	removed := 0
	switch {
	case *all:
		removed = len(data.Patches)
		data.Patches = nil
	case nameValue != "":
		removed = patches.RemoveByName(data, ids[0], nameValue)
	default:
		removed = patches.Remove(data, ids)
	}
	if err := a.store.SavePatches(data); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已删除 %d 条补丁记录，剩余 %d 条\n", removed, data.Count)
	return nil
}

func (a *app) patchClear(args []string) error {
	fs := flag.NewFlagSet("patch clear", flag.ContinueOnError)
	fs.SetOutput(a.err)
	fs.Usage = func() { fmt.Fprintln(a.err, "用法: ugal patch clear") }
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("patch clear 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	data, err := a.loadPatches()
	if err != nil {
		return err
	}
	removed := len(data.Patches)
	data.Patches = nil
	if err := a.store.SavePatches(data); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已清空补丁记录（%d 条）\n", removed)
	return nil
}

// patchCheckRow 是 patch check 的单条输出，JSON 与表格共用。
type patchCheckRow struct {
	AppID        int    `json:"appid"`
	Game         string `json:"game,omitempty"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status"`
	ExpectedHash string `json:"expected_hash,omitempty"`
	ActualHash   string `json:"actual_hash,omitempty"`
	Path         string `json:"path,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (a *app) patchCheck(args []string) error {
	fs := flag.NewFlagSet("patch check", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	all := fs.Bool("all", false, "显示未找到的补丁明细（默认只汇总一行）")
	dirFlag := fs.String("dir", "", "补丁文件目录，默认 <数据目录>/patches")
	offline := fs.Bool("offline", false, "不联网获取缺失的游戏名称")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch check [appid...] [--dir 目录] [--all] [--json] [--offline]")
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
	filter := make(map[int]bool, len(ids))
	for _, id := range ids {
		filter[id] = true
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = a.store.PatchesDir()
	}
	dir = expandUserPath(dir)
	created := false
	if _, err := os.Stat(dir); err != nil && errors.Is(err, os.ErrNotExist) {
		created = true
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建补丁目录 %s 失败: %w", dir, err)
	}

	data, err := a.loadPatches()
	if err != nil {
		return err
	}
	if len(filter) > 0 {
		filtered := *data
		filtered.Patches = nil
		for _, p := range data.Patches {
			if filter[p.AppID] {
				filtered.Patches = append(filtered.Patches, p)
			}
		}
		filtered.Count = len(filtered.Patches)
		data = &filtered
	}

	showProgress := !*asJSON && isTerminal(a.err) &&
		!strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
	var progress patches.ProgressFunc
	if showProgress {
		progress = func(index, total int, name string) {
			fmt.Fprintf(a.err, "\r\033[K正在校验 %d/%d：%s", index, total, truncateDisplay(name, 40))
		}
	}
	report := patches.CheckDirProgress(data, dir, progress)
	if showProgress {
		fmt.Fprint(a.err, "\r\033[K")
	}

	checkIDs := make([]int, 0, len(report.Results))
	for _, r := range report.Results {
		checkIDs = append(checkIDs, r.AppID)
	}
	names, err := a.resolveGameNames(checkIDs, *offline)
	if err != nil {
		return err
	}
	rows := make([]patchCheckRow, 0, len(report.Results))
	for _, r := range report.Results {
		rows = append(rows, patchCheckRow{
			AppID:        r.AppID,
			Game:         names[r.AppID],
			Name:         r.Name,
			Status:       string(r.Status),
			ExpectedHash: r.ExpectedHash,
			ActualHash:   r.ActualHash,
			Path:         r.Path,
			Error:        r.Err,
		})
	}

	if *asJSON {
		payload := struct {
			Directory  string          `json:"directory"`
			Total      int             `json:"total"`
			OK         int             `json:"ok"`
			Missing    int             `json:"missing"`
			Mismatch   int             `json:"mismatch"`
			Unverified int             `json:"unverified"`
			Patches    []patchCheckRow `json:"patches"`
		}{
			Directory:  report.Dir,
			Total:      report.Total,
			OK:         report.OK,
			Missing:    report.Missing,
			Mismatch:   report.Mismatch,
			Unverified: report.Unverified,
			Patches:    rows,
		}
		return writeJSONOutput(a.out, payload)
	}

	fmt.Fprintf(a.out, "补丁目录: %s\n", report.Dir)
	if created {
		fmt.Fprintln(a.out, "已自动创建补丁目录，将补丁文件放入其中后再次运行即可校验。")
	}
	if report.Total == 0 {
		fmt.Fprintln(a.out, "还没有可校验的补丁记录。")
		return nil
	}

	useColor := colorEnabled(a.out)
	headers := []string{"APPID", "状态", "游戏", "补丁名称", "哈希"}
	right := []bool{true, false, false, false, false}
	tableRows := make([][]string, 0, len(rows))
	for _, r := range rows {
		missing := r.Status == string(patches.CheckMissing)
		if missing && !*all {
			continue
		}
		status := patches.CheckStatus(r.Status).Label()
		if missing {
			status = colorize(useColor, ansiRed, status)
		}
		hashStatus := "-"
		switch r.Status {
		case string(patches.CheckOK):
			hashStatus = colorize(useColor, ansiGreen, "符合")
		case string(patches.CheckMismatch):
			hashStatus = colorize(useColor, ansiRed, "不符合")
		}
		game := r.Game
		if game == "" {
			game = fmt.Sprintf("App %d", r.AppID)
		}
		name := r.Name
		if name == "" {
			name = "-"
		}
		tableRows = append(tableRows, []string{
			fmt.Sprintf("%d", r.AppID),
			status,
			truncateDisplay(game, 40),
			truncateDisplay(name, 32),
			hashStatus,
		})
	}
	if len(tableRows) > 0 {
		renderTable(a.out, headers, right, tableRows)
		fmt.Fprintln(a.out)
	}
	if !*all && report.Missing > 0 {
		fmt.Fprintf(a.out, "未找到 %d 个补丁（使用 --all 查看明细）\n", report.Missing)
	}
	fmt.Fprintf(a.out, "共 %d 条补丁：已找到 %d，未找到 %d，哈希错误 %d，无法校验 %d\n",
		report.Total, report.OK, report.Missing, report.Mismatch, report.Unverified)
	return nil
}

// patchHash 计算单个补丁文件的哈希。
func (a *app) patchHash(args []string) error {
	fs := flag.NewFlagSet("patch hash", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	md5Mode := fs.Bool("md5", false, "计算 MD5（默认）")
	sha256Mode := fs.Bool("sha256", false, "计算 SHA-256（默认 MD5）")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch hash <补丁文件> [--md5|--sha256] [--json]")
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("patch hash 需要一个补丁文件")
	}
	if *md5Mode && *sha256Mode {
		return errors.New("--md5 和 --sha256 不能同时使用")
	}
	algorithm := "md5"
	if *sha256Mode {
		algorithm = "sha256"
	}
	hash, err := patches.HashFileWith(rest[0], algorithm)
	if err != nil {
		return err
	}
	if *asJSON {
		payload := struct {
			File      string `json:"file"`
			Algorithm string `json:"algorithm"`
			SHA256    string `json:"sha256,omitempty"`
			MD5       string `json:"md5,omitempty"`
		}{File: rest[0], Algorithm: algorithm}
		if *sha256Mode {
			payload.SHA256 = hash
		} else {
			payload.MD5 = hash
		}
		return writeJSONOutput(a.out, payload)
	}
	fmt.Fprintln(a.out, hash)
	return nil
}

func printPatchHelp(w io.Writer) {
	fmt.Fprint(w, `本地游戏补丁记录（同一 appid 可对应多个补丁文件）

输入文件:
  <数据目录>/patches.json 会按内置默认记录在任意命令首次运行时自动创建，
  可直接手工编辑；运行 "ugal fav import" 可把其中出现过的游戏一键加入收藏。

补丁状态:
  没有记录                      无补丁（默认）
  全部 applied = false          未打补丁
  全部 applied = true           已打补丁
  部分 applied = true           部分已打

游戏名称来源（按优先级）:
  已安装 / 已拥有 / 收藏夹名称
  Steam 本地 appcache/appinfo.vdf
  appnames.json 缓存
  以上都没有时按需从 Steam Web API 获取并缓存；--offline 可禁止联网

用法:
  ugal patch                            列出补丁记录
  ugal patch list [appid...]            列出补丁记录，可筛选（同一 appid 可重复出现，补丁多的排前面）
  ugal patch list --all                 列出已安装游戏及补丁状态（含无补丁）
  ugal patch list --json                机器可读输出
  ugal patch list --offline             不联网，只用本地名称和缓存
  ugal patch set <appid>                新建或更新补丁记录
  ugal patch set <appid> --file 补丁文件 自动计算 MD5（默认），同名文件会更新
  ugal patch set <appid> --file a --file b
                                        一次记录多个补丁文件
  ugal patch set <appid> --file 补丁文件 --sha256
                                        自动计算 SHA-256
  ugal patch set <appid> --name 名称 --hash 哈希
                                        按名称新增或更新补丁记录
  ugal patch set <appid> --applied      标记该 appid 的全部补丁为已打补丁
  ugal patch set <appid> --not-applied  标记该 appid 的全部补丁为未打补丁
  ugal patch set <appid> --name 名称 --applied
                                        只标记指定补丁
  ugal patch set <appid> --offline      输出游戏名时不联网
  ugal patch remove <appid...>          删除该 appid 的全部补丁记录
  ugal patch remove <appid> --name 名称 只删除指定名称的补丁记录
  ugal patch remove --all               清空补丁记录
  ugal patch check                      检查 <数据目录>/patches 中的补丁文件是否存在且哈希正确（目录不存在时自动创建）
  ugal patch check --all                同时显示未找到的补丁明细
  ugal patch check --dir 补丁目录        检查指定目录
  ugal patch check --json               机器可读输出
  ugal patch check --offline            输出游戏名时不联网
  ugal patch install <appid...>         把该 appid 已记录的补丁压缩包解压到游戏目录
  ugal patch install <appid> --name 名称
                                        只安装指定补丁
  ugal patch install <appid> --file 压缩包
                                        直接安装指定压缩包并记录为补丁
  ugal patch install --all              安装所有已记录且找到文件的补丁
  ugal patch install <appid> --backup   替换同名文件前先备份
  ugal patch install <appid> --strip 1  去掉压缩包顶层目录后再解压
  ugal patch install <appid> --dry-run  只显示将写入的文件
  ugal patch install <appid> --game-dir 目录
                                        覆盖游戏目录
  ugal patch install <appid> --json     机器可读输出
  ugal patch hash <补丁文件>             只计算 MD5（默认）
  ugal patch hash <补丁文件> --sha256    只计算 SHA-256
  ugal patch md5 <补丁文件>              MD5 简写
  ugal patch sha256 <补丁文件>           SHA-256 简写
  ugal patch clear                      清空补丁记录
`)
}
