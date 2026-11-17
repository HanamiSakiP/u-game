package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	case "hash", "sha256":
		return a.patchHash(args[1:])
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

func attachPatches(entries []entry, data *model.PatchesData) {
	for i := range entries {
		entries[i].Patch = patches.Find(data, entries[i].AppID)
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
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch [list] [appid...] [--all] [--json]")
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
	names, err := a.gameNames()
	if err != nil {
		return err
	}

	var rows []patchRow
	if *all {
		owned, installed, err := a.loadData()
		if err != nil {
			return err
		}
		entries, err := buildEntries(owned, installed)
		if err != nil {
			return err
		}
		attachPatches(entries, data)
		for _, e := range entries {
			if len(filter) > 0 && !filter[e.AppID] {
				continue
			}
			rows = append(rows, newPatchRow(e.AppID, e.Name, e.Patch))
		}
	} else {
		for _, p := range data.Patches {
			if len(filter) > 0 && !filter[p.AppID] {
				continue
			}
			pcopy := p
			rows = append(rows, newPatchRow(p.AppID, names[p.AppID], &pcopy))
		}
	}

	if *asJSON {
		payload := struct {
			Total     int        `json:"total"`
			Applied   int        `json:"applied"`
			Unpatched int        `json:"unpatched"`
			NoPatch   int        `json:"no_patch"`
			Patches   []patchRow `json:"patches"`
		}{Total: len(rows), Patches: rows}
		for _, r := range rows {
			switch {
			case r.Status == "已打补丁":
				payload.Applied++
			case r.Status == "无补丁":
				payload.NoPatch++
			default:
				payload.Unpatched++
			}
		}
		return writeJSONOutput(a.out, payload)
	}

	if len(rows) == 0 {
		if *all {
			fmt.Fprintln(a.out, "没有可显示的已安装游戏。")
		} else {
			fmt.Fprintln(a.out, "还没有补丁记录。使用 `ugal patch set <appid> --name 名称 --file 补丁文件` 添加。")
		}
		return nil
	}

	headers := []string{"APPID", "状态", "游戏", "补丁名称", "哈希", "更新时间"}
	right := []bool{true, false, false, false, false, false}
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
		tableRows = append(tableRows, []string{
			fmt.Sprintf("%d", r.AppID),
			r.Status,
			truncateDisplay(orPlaceholder(r.Game, fmt.Sprintf("App %d", r.AppID)), 40),
			truncateDisplay(name, 32),
			hash,
			updated,
		})
	}
	renderTable(a.out, headers, right, tableRows)
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

func (a *app) patchSet(args []string) error {
	fs := flag.NewFlagSet("patch set", flag.ContinueOnError)
	fs.SetOutput(a.err)
	name := fs.String("name", "", "补丁名称")
	hash := fs.String("hash", "", "补丁哈希值")
	file := fs.String("file", "", "从补丁文件计算 SHA-256 并记录")
	applied := fs.Bool("applied", false, "标记为已打补丁")
	notApplied := fs.Bool("not-applied", false, "标记为未打补丁")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch set <appid> [--name 名称] [--hash 哈希|--file 文件] [--applied|--not-applied]")
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
	if strings.TrimSpace(*hash) != "" && strings.TrimSpace(*file) != "" {
		return errors.New("--hash 和 --file 不能同时使用")
	}
	if *name == "" && *hash == "" && *file == "" && !*applied && !*notApplied {
		return errors.New("请至少指定 --name、--hash、--file、--applied 或 --not-applied")
	}

	id := ids[0]
	data, err := a.loadPatches()
	if err != nil {
		return err
	}
	patch := model.Patch{AppID: id}
	if old := patches.Find(data, id); old != nil {
		patch = *old
	}
	if *name != "" {
		patch.Name = strings.TrimSpace(*name)
	}
	if *hash != "" {
		patch.Hash = patches.NormalizeHash(*hash)
	}
	if *file != "" {
		h, err := patches.HashFile(*file)
		if err != nil {
			return err
		}
		patch.Hash = h
		if patch.Name == "" {
			patch.Name = filepath.Base(*file)
		}
	}
	if *applied {
		patch.Applied = true
	} else if *notApplied {
		patch.Applied = false
	}
	patches.Set(data, patch)
	if err := a.store.SavePatches(data); err != nil {
		return err
	}

	names, err := a.gameNames()
	if err != nil {
		return err
	}
	game := names[id]
	if game == "" {
		game = fmt.Sprintf("App %d", id)
	}
	p := patches.Find(data, id)
	detail := ""
	if p != nil {
		if p.Name != "" {
			detail = "，补丁 " + p.Name
		}
		if p.Hash != "" {
			detail += "，哈希 " + truncateDisplay(p.Hash, 20)
		}
	}
	fmt.Fprintf(a.out, "已记录 %s（%d）补丁状态：%s%s\n", game, id, patchStatus(p), detail)
	return nil
}

func (a *app) patchRemove(args []string) error {
	fs := flag.NewFlagSet("patch remove", flag.ContinueOnError)
	fs.SetOutput(a.err)
	all := fs.Bool("all", false, "清空所有补丁记录")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch remove <appid...> | --all")
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
	data, err := a.loadPatches()
	if err != nil {
		return err
	}
	removed := 0
	if *all {
		removed = len(data.Patches)
		data.Patches = nil
	} else {
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

func (a *app) patchHash(args []string) error {
	fs := flag.NewFlagSet("patch hash", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch hash <补丁文件> [--json]")
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
	hash, err := patches.HashFile(rest[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSONOutput(a.out, struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
		}{File: rest[0], SHA256: hash})
	}
	fmt.Fprintln(a.out, hash)
	return nil
}

func printPatchHelp(w io.Writer) {
	fmt.Fprint(w, `本地游戏补丁记录

补丁状态:
  没有记录                      无补丁（默认）
  有记录且 applied = false      未打补丁
  有记录且 applied = true       已打补丁

用法:
  ugal patch                            列出补丁记录
  ugal patch list [appid...]            列出补丁记录，可筛选
  ugal patch list --all                 列出已安装游戏及补丁状态（含无补丁）
  ugal patch list --json                机器可读输出
  ugal patch set <appid>                新建或更新补丁记录
  ugal patch set <appid> --file 补丁文件 自动计算 SHA-256
  ugal patch set <appid> --applied      标记为已打补丁
  ugal patch set <appid> --not-applied  标记为未打补丁
  ugal patch remove <appid...>          删除补丁记录
  ugal patch remove --all               清空补丁记录
  ugal patch hash <补丁文件>             只计算 SHA-256
  ugal patch clear                      清空补丁记录
`)
}
