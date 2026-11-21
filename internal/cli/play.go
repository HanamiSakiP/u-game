package cli

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-game/internal/player"
)

// cmdPlay 用本机播放器打开音乐原声带。
func (a *app) cmdPlay(args []string) error {
	fs := flag.NewFlagSet("play", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "输出 JSON")
	list := fs.Bool("list", false, "只列出可播放的原声带")
	playerCmd := fs.String("player", "", "指定播放器命令（默认自动选择，也可用 UGAL_PLAYER）")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal play [APPID|名称] [--player 命令] [--list]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
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

	music := make([]entry, 0)
	for _, e := range all {
		if e.Type != "music" || e.Status == "missing" {
			continue
		}
		music = append(music, e)
	}
	if len(music) == 0 {
		return errors.New("没有找到已安装的音乐原声带，请先运行 `ugal scan` 扫描 steamapps/music")
	}

	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *list || query == "" {
		if *asJSON {
			return writeJSONOutput(a.out, struct {
				Total int     `json:"total"`
				Music []entry `json:"music"`
			}{Total: len(music), Music: music})
		}
		headers := []string{"APPID", "名称", "大小", "目录"}
		right := []bool{true, false, true, false}
		rows := make([][]string, 0, len(music))
		for _, e := range music {
			rows = append(rows, []string{
				strconv.Itoa(e.AppID),
				truncateDisplay(e.Name, 60),
				humanSize(e.Size),
				e.Dir,
			})
		}
		fmt.Fprintf(a.out, "可播放的原声带 %d 款\n", len(music))
		renderTable(a.out, headers, right, rows)
		if query == "" {
			fmt.Fprintln(a.out, "\n用法: ugal play <APPID|名称>，例如 `ugal play 123456`")
		}
		return nil
	}

	picked, err := pickMusic(music, query)
	if err != nil {
		return err
	}
	if strings.TrimSpace(picked.Dir) == "" {
		return fmt.Errorf("%s 没有原声带目录，请重新运行 `ugal scan`", picked.Name)
	}
	spec, err := player.PlayDir(player.Options{Dir: picked.Dir, Player: *playerCmd})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSONOutput(a.out, struct {
			AppID  int      `json:"appid"`
			Name   string   `json:"name"`
			Player string   `json:"player"`
			Files  []string `json:"files"`
		}{
			AppID: picked.AppID, Name: picked.Name, Player: spec.Name, Files: spec.Files,
		})
	}
	fmt.Fprintf(a.out, "正在用 %s 播放 %s（%d 首）\n", spec.Name, picked.Name, len(spec.Files))
	return nil
}

// pickMusic 按 APPID 或名称选择一款原声带。
func pickMusic(list []entry, query string) (entry, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entry{}, errors.New("请指定 APPID 或名称")
	}
	if id, err := strconv.Atoi(q); err == nil {
		for _, e := range list {
			if e.AppID == id {
				return e, nil
			}
		}
		return entry{}, fmt.Errorf("没有找到 APPID 为 %d 的原声带", id)
	}

	var exact, matches []entry
	for _, e := range list {
		name := strings.ToLower(e.Name)
		if name == q {
			exact = append(exact, e)
			continue
		}
		if strings.Contains(name, q) {
			matches = append(matches, e)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		matches = exact
	}
	switch len(matches) {
	case 0:
		return entry{}, fmt.Errorf("没有找到名称包含 %q 的原声带", query)
	case 1:
		return matches[0], nil
	}

	names := make([]string, 0, len(matches))
	for _, e := range matches {
		if len(names) == 8 {
			names = append(names, "…")
			break
		}
		names = append(names, fmt.Sprintf("%s（%d）", e.Name, e.AppID))
	}
	return entry{}, fmt.Errorf("匹配到多款原声带，请指定 APPID：%s", strings.Join(names, "、"))
}
