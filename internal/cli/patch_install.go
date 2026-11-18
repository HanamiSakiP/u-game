package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/patches"
)

type patchTask struct {
	appid   int
	name    string
	archive string
	record  bool
}

type patchInstallRow struct {
	AppID    int      `json:"appid"`
	Game     string   `json:"game,omitempty"`
	Archive  string   `json:"archive"`
	GameDir  string   `json:"game_dir"`
	Files    int      `json:"files"`
	Replaced int      `json:"replaced"`
	BackedUp int      `json:"backed_up,omitempty"`
	Backup   string   `json:"backup_dir,omitempty"`
	DryRun   bool     `json:"dry_run,omitempty"`
	Entries  []string `json:"entries,omitempty"`
	Applied  bool     `json:"applied"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func (a *app) patchInstall(args []string) error {
	fs := flag.NewFlagSet("patch install", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	all := fs.Bool("all", false, "安装所有已记录且找到文件的补丁")
	offline := fs.Bool("offline", false, "输出游戏名时不联网")
	name := fs.String("name", "", "只安装指定名称的补丁记录")
	dirFlag := fs.String("dir", "", "补丁文件目录，默认 <数据目录>/patches")
	gameDirFlag := fs.String("game-dir", "", "覆盖游戏目录（只能与一个 appid 一起使用）")
	strip := fs.Int("strip", 0, "解压时去掉的顶层目录层数")
	backup := fs.Bool("backup", false, "替换同名文件前先备份到 <数据目录>/patch-backups")
	dryRun := fs.Bool("dry-run", false, "只显示将要写入的文件，不修改游戏目录")
	var files []string
	fs.Func("file", "直接安装指定压缩包（可重复），并记录为补丁", func(value string) error {
		if strings.TrimSpace(value) != "" {
			files = append(files, value)
		}
		return nil
	})
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal patch install <appid...> [--name 名称] [--file 压缩包] [参数]")
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
	allMode := *all && len(ids) == 0 && len(files) == 0
	if *strip < 0 {
		return errors.New("--strip 不能为负数")
	}
	if strings.TrimSpace(*gameDirFlag) != "" && len(ids) != 1 {
		return errors.New("--game-dir 只能与一个 appid 一起使用")
	}
	if strings.TrimSpace(*name) != "" && len(files) > 1 {
		return errors.New("指定多个 --file 时不能使用 --name（每个文件按文件名分别记录）")
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = a.store.PatchesDir()
	}
	dir = expandUserPath(dir)

	_, installed, err := a.loadData()
	if err != nil {
		return err
	}
	gameDirs := installedDirs(installed)

	data, err := a.loadPatches()
	if err != nil {
		return err
	}

	// 构建安装任务：每个任务对应一个压缩包。
	var tasks []patchTask
	var failures []string
	skipped := 0

	if len(files) > 0 {
		if len(ids) != 1 {
			return errors.New("使用 --file 时需要且只能指定一个 appid")
		}
		id := ids[0]
		for _, file := range files {
			file = expandUserPath(strings.TrimSpace(file))
			if file == "" {
				continue
			}
			patchName := strings.TrimSpace(*name)
			if patchName == "" {
				patchName = filepath.Base(file)
			}
			tasks = append(tasks, patchTask{appid: id, name: patchName, archive: file, record: true})
		}
	} else {
		if len(ids) == 0 {
			if !*all {
				return errors.New("请提供要安装补丁的 appid，或使用 --all")
			}
			for _, p := range data.Patches {
				if p.AppID > 0 {
					ids = append(ids, p.AppID)
				}
			}
			ids = uniqueInts(ids)
		}
		if len(ids) == 0 {
			return errors.New("patches.json 中还没有补丁记录")
		}
		nameFilter := strings.TrimSpace(*name)
		index := patches.IndexDir(dir)
		for _, id := range ids {
			records := patches.FindAll(data, id)
			if len(records) == 0 {
				if allMode {
					skipped++
					continue
				}
				failures = append(failures, fmt.Sprintf("%d: 没有补丁记录", id))
				continue
			}
			if nameFilter != "" {
				p := patches.FindByName(data, id, nameFilter)
				if p == nil {
					if allMode {
						skipped++
						continue
					}
					failures = append(failures, fmt.Sprintf("%d: 没有名为 %s 的补丁记录", id, nameFilter))
					continue
				}
				records = []model.Patch{*p}
			}
			added := 0
			for _, record := range records {
				archive := index.Find(record.Name, id)
				if archive == "" {
					if nameFilter != "" && !allMode {
						failures = append(failures, fmt.Sprintf("%d: 补丁文件 %s 未在 %s 中找到", id, record.Name, dir))
					}
					continue
				}
				tasks = append(tasks, patchTask{appid: id, name: record.Name, archive: archive})
				added++
			}
			if added == 0 && nameFilter == "" && !allMode {
				failures = append(failures, fmt.Sprintf("%d: 没有找到可安装的补丁文件（目录 %s）", id, dir))
			}
			if added == 0 && allMode {
				skipped++
			}
		}
	}

	if len(tasks) == 0 {
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "\n"))
		}
		if *asJSON {
			payload := struct {
				Total     int               `json:"total"`
				Installed int               `json:"installed"`
				Failed    int               `json:"failed"`
				Skipped   int               `json:"skipped,omitempty"`
				Patches   []patchInstallRow `json:"patches"`
			}{Total: 0, Installed: 0, Failed: 0, Skipped: skipped, Patches: []patchInstallRow{}}
			if err := writeJSONOutput(a.out, payload); err != nil {
				return err
			}
			return nil
		}
		if skipped > 0 {
			fmt.Fprintf(a.out, "没有找到可安装的补丁（跳过 %d 个未安装或缺少文件的游戏）\n", skipped)
			return nil
		}
		return errors.New("没有找到可安装的补丁")
	}

	names, _ := a.resolveGameNames(taskIDs(tasks), *offline)
	rows := make([]patchInstallRow, 0, len(tasks))
	applyIDs := make(map[int]bool)
	type appliedPatch struct {
		appid int
		name  string
	}
	var applied []appliedPatch
	success := 0

	for _, t := range tasks {
		gameName := names[t.appid]
		row := patchInstallRow{
			AppID:   t.appid,
			Game:    gameName,
			Archive: t.archive,
			DryRun:  *dryRun,
		}
		gameDir := strings.TrimSpace(*gameDirFlag)
		if gameDir == "" {
			gameDir = gameDirs[t.appid]
		}
		gameDir = expandUserPath(gameDir)
		row.GameDir = gameDir
		if gameDir == "" {
			if allMode {
				skipped++
				continue
			}
			row.Error = "未找到游戏目录，请先运行 `ugal scan` 或用 --game-dir 指定"
			failures = append(failures, fmt.Sprintf("%d: %s", t.appid, row.Error))
			rows = append(rows, row)
			continue
		}
		if fi, err := os.Stat(t.archive); err != nil || fi.IsDir() {
			if err != nil {
				row.Error = fmt.Sprintf("补丁文件不可用: %v", err)
			} else {
				row.Error = "补丁路径不是文件: " + t.archive
			}
			failures = append(failures, fmt.Sprintf("%d: %s", t.appid, row.Error))
			rows = append(rows, row)
			continue
		}

		backupDir := ""
		if *backup && !*dryRun {
			backupDir = filepath.Join(a.store.Dir, "patch-backups", fmt.Sprintf("%d", t.appid), time.Now().Format("20060102-150405.000000000"))
		}
		res, err := patches.Install(patches.InstallOptions{
			Archive:   t.archive,
			GameDir:   gameDir,
			Strip:     *strip,
			BackupDir: backupDir,
			DryRun:    *dryRun,
		})
		if err != nil {
			row.Error = err.Error()
			failures = append(failures, fmt.Sprintf("%d: %s", t.appid, err))
			rows = append(rows, row)
			continue
		}
		success++
		row.Files = res.Files
		row.Replaced = res.Replaced
		row.BackedUp = res.BackedUp
		row.Backup = res.BackupDir
		row.Entries = res.Entries
		row.Warnings = res.Warnings
		row.Applied = !*dryRun
		rows = append(rows, row)
		if !*dryRun {
			applyIDs[t.appid] = true
			applied = append(applied, appliedPatch{appid: t.appid, name: t.name})
		}
	}

	if !*dryRun && success > 0 {
		for _, item := range applied {
			record := model.Patch{AppID: item.appid, Name: item.name, Applied: true}
			t := findTask(tasks, item.appid, item.name)
			if old := patches.FindByName(data, item.appid, item.name); old != nil {
				record = *old
				record.Applied = true
			}
			if t != nil && (t.record || record.Hash == "") {
				if hash, err := patches.HashFileWith(t.archive, "md5"); err == nil {
					record.Hash = hash
				}
			}
			patches.Set(data, record)
		}
		if err := a.store.SavePatches(data); err != nil {
			return err
		}
	}

	if *asJSON {
		payload := struct {
			Total     int               `json:"total"`
			Installed int               `json:"installed"`
			Failed    int               `json:"failed"`
			DryRun    bool              `json:"dry_run,omitempty"`
			Patches   []patchInstallRow `json:"patches"`
			Failures  []string          `json:"failures,omitempty"`
		}{
			Total:     len(rows),
			Installed: success,
			Failed:    len(failures),
			DryRun:    *dryRun,
			Patches:   rows,
			Failures:  failures,
		}
		if err := writeJSONOutput(a.out, payload); err != nil {
			return err
		}
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "\n"))
		}
		return nil
	}

	for _, row := range rows {
		game := row.Game
		if game == "" {
			game = fmt.Sprintf("App %d", row.AppID)
		}
		fmt.Fprintf(a.out, "\n%s（%d）\n", game, row.AppID)
		fmt.Fprintf(a.out, "  补丁: %s\n", row.Archive)
		fmt.Fprintf(a.out, "  目录: %s\n", row.GameDir)
		if row.Error != "" {
			fmt.Fprintf(a.out, "  失败: %s\n", row.Error)
			continue
		}
		action := "安装"
		if row.DryRun {
			action = "预演"
		}
		fmt.Fprintf(a.out, "   %s: %d 个文件，替换 %d 个", action, row.Files, row.Replaced)
		if row.BackedUp > 0 {
			fmt.Fprintf(a.out, "，备份 %d 个", row.BackedUp)
		}
		fmt.Fprintln(a.out)
		if row.Backup != "" {
			fmt.Fprintf(a.out, "  备份: %s\n", row.Backup)
		}
		if row.DryRun && len(row.Entries) > 0 {
			fmt.Fprintln(a.out, "  将写入:")
			for i, entry := range row.Entries {
				if i >= 30 {
					fmt.Fprintf(a.out, "    ... 其余 %d 个\n", len(row.Entries)-i)
					break
				}
				fmt.Fprintf(a.out, "    %s\n", entry)
			}
		}
		if row.Applied {
			fmt.Fprintln(a.out, "  已更新补丁记录为「已打补丁」")
		}
		for _, warning := range row.Warnings {
			fmt.Fprintf(a.out, "  提示: %s\n", warning)
		}
	}
	fmt.Fprintf(a.out, "\n共 %d 个补丁：成功 %d，失败 %d", len(rows), success, len(failures))
	if skipped > 0 {
		fmt.Fprintf(a.out, "，跳过 %d", skipped)
	}
	fmt.Fprintln(a.out)
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func installedDirs(installed *model.InstalledData) map[int]string {
	out := make(map[int]string)
	if installed == nil {
		return out
	}
	for _, app := range installed.Games {
		if !app.Installed || app.AppID <= 0 {
			continue
		}
		if strings.TrimSpace(app.Dir) != "" {
			out[app.AppID] = app.Dir
		}
	}
	return out
}

func taskIDs(tasks []patchTask) []int {
	ids := make([]int, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.appid)
	}
	return uniqueInts(ids)
}

func findTask(tasks []patchTask, appid int, name string) *patchTask {
	for i := range tasks {
		if tasks[i].appid == appid && tasks[i].name == name {
			return &tasks[i]
		}
	}
	return nil
}

func uniqueInts(ids []int) []int {
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
