package cli

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/HanamiSakiP/u-game/internal/favorites"
	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/patches"
	"github.com/HanamiSakiP/u-game/internal/saves"
	"github.com/HanamiSakiP/u-game/internal/store"
)

func newTestTUIModel(t *testing.T, games []tuiGame, fav *model.FavoritesData, pd *model.PatchesData) (tuiModel, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if fav == nil {
		fav = &model.FavoritesData{}
	}
	if pd == nil {
		pd = &model.PatchesData{}
	}
	m := newTUIModel(st, games, fav, pd, &model.SavesData{})
	m.width, m.height = 100, 30
	m.ready = true
	return m, st
}

func TestTUIFilterViews(t *testing.T) {
	fav := &model.Favorite{AppID: 1, Name: "Alpha", Status: favorites.StatusPlaying}
	games := []tuiGame{
		{AppID: 1, Name: "Alpha", Status: "installed", Favorite: fav},
		{AppID: 2, Name: "Beta", Status: "missing"},
		{AppID: 3, Name: "Gamma", Status: "extra"},
	}
	m, _ := newTestTUIModel(t, games, &model.FavoritesData{Games: []model.Favorite{*fav}}, nil)

	if got := len(m.filtered()); got != 3 {
		t.Fatalf("全部视图应有 3 款，得到 %d", got)
	}
	m.view = tuiViewInstalled
	if got := len(m.filtered()); got != 2 {
		t.Fatalf("已安装视图应有 2 款，得到 %d", got)
	}
	m.view = tuiViewMissing
	if got := len(m.filtered()); got != 1 || m.filtered()[0].AppID != 2 {
		t.Fatalf("未安装视图应只包含 Beta: %#v", m.filtered())
	}
	m.view = tuiViewFavorites
	if got := len(m.filtered()); got != 1 || m.filtered()[0].AppID != 1 {
		t.Fatalf("收藏视图应只包含 Alpha: %#v", m.filtered())
	}
}

func TestTUIFavoritesSortByNotePublisher(t *testing.T) {
	games := []tuiGame{
		{AppID: 5, Name: "NoGroup", Status: "unknown", Favorite: &model.Favorite{AppID: 5}},
		{AppID: 4, Name: "Valve Game", Status: "unknown", Favorite: &model.Favorite{AppID: 4}, Publisher: "Valve"},
		{AppID: 3, Name: "Sega Game", Status: "unknown", Favorite: &model.Favorite{AppID: 3}, Publisher: "Sega"},
		{AppID: 2, Name: "Note B", Status: "unknown", Favorite: &model.Favorite{AppID: 2, Note: "B组"}},
		{AppID: 1, Name: "Note A", Status: "unknown", Favorite: &model.Favorite{AppID: 1, Note: "A组"}},
		{AppID: 8, Name: "Alpha", Status: "unknown", Favorite: &model.Favorite{AppID: 8, Note: "A组"}},
		{AppID: 6, Name: "Not Favorite", Status: "installed"},
	}
	m, _ := newTestTUIModel(t, games, &model.FavoritesData{}, nil)
	m.view = tuiViewFavorites

	got := m.filtered()
	want := []int{8, 1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("收藏视图应有 %d 款，得到 %d: %#v", len(want), len(got), got)
	}
	for i, id := range want {
		if got[i].AppID != id {
			t.Fatalf("收藏排序 = %v, 期望 %v", gameIDs(got), want)
		}
	}
}

func gameIDs(games []tuiGame) []int {
	ids := make([]int, len(games))
	for i, g := range games {
		ids[i] = g.AppID
	}
	return ids
}

func TestTUIListEntriesGroupHeaders(t *testing.T) {
	games := []tuiGame{
		{AppID: 1, Name: "Note A", Favorite: &model.Favorite{AppID: 1, Note: "A组"}},
		{AppID: 2, Name: "Alpha", Favorite: &model.Favorite{AppID: 2, Note: "A组"}},
		{AppID: 3, Name: "Sega Game", Favorite: &model.Favorite{AppID: 3}, Publisher: "Sega"},
		{AppID: 4, Name: "NoGroup", Favorite: &model.Favorite{AppID: 4}},
	}
	m, _ := newTestTUIModel(t, games, &model.FavoritesData{}, nil)
	m.view = tuiViewFavorites

	entries := m.listEntries(m.filtered())
	var labels []string
	var order []int
	filtered := m.filtered()
	for _, e := range entries {
		if e.game < 0 {
			labels = append(labels, e.label)
			continue
		}
		order = append(order, filtered[e.game].AppID)
	}
	wantLabels := []string{"A组（2 个）", "Sega（1 个）", "无分组（1 个）"}
	if strings.Join(labels, "|") != strings.Join(wantLabels, "|") {
		t.Fatalf("分组标题 = %v, 期望 %v", labels, wantLabels)
	}
	if want := []int{2, 1, 3, 4}; fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("分组内顺序 = %v, 期望 %v", order, want)
	}
}

func TestTUIFavoritesSortFromStore(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	fav := &model.FavoritesData{Games: []model.Favorite{
		{AppID: 2, Name: "Valve Game"},
		{AppID: 1, Name: "Note A", Note: "A组"},
	}}
	if err := st.SaveFavorites(fav); err != nil {
		t.Fatalf("save favorites: %v", err)
	}
	if err := st.SavePublishers(&model.PublishersData{Publishers: map[string]string{"2": "Valve"}}); err != nil {
		t.Fatalf("save publishers: %v", err)
	}

	games, favData, pd, saves, err := loadTUIGames(st)
	if err != nil {
		t.Fatalf("load tui games: %v", err)
	}
	m := newTUIModel(st, games, favData, pd, saves)
	m.view = tuiViewFavorites

	got := m.filtered()
	if want := []int{1, 2}; len(got) != len(want) || got[0].AppID != want[0] || got[1].AppID != want[1] {
		t.Fatalf("从数据目录读取后的收藏顺序 = %v, 期望 %v", gameIDs(got), want)
	}
}

func TestTUIHidesRuntimeAppsByDefault(t *testing.T) {
	games := []tuiGame{
		{AppID: 100, Name: "Game A", Status: "installed"},
		{AppID: 228980, Name: "Steamworks Common Redistributables", Status: "extra"},
		{AppID: 1493710, Name: "Proton Experimental", Status: "extra"},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)

	if got := m.filtered(); len(got) != 1 || got[0].AppID != 100 {
		t.Fatalf("默认应隐藏运行库: %#v", got)
	}
	if got := m.viewCount(tuiViewAll); got != 1 {
		t.Fatalf("全部视图计数 = %d, want 1", got)
	}
	m.view = tuiViewInstalled
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 100 {
		t.Fatalf("已安装视图应隐藏运行库: %#v", got)
	}
	if got := m.viewCount(tuiViewInstalled); got != 1 {
		t.Fatalf("已安装视图计数 = %d, want 1", got)
	}

	m.showHidden = true
	m.view = tuiViewAll
	if got := m.filtered(); len(got) != 3 {
		t.Fatalf("显示隐藏项后应有 3 款，得到 %#v", got)
	}
	if got := m.viewCount(tuiViewAll); got != 3 {
		t.Fatalf("显示隐藏项后计数 = %d, want 3", got)
	}
}

func TestTUIMusicView(t *testing.T) {
	games := []tuiGame{
		{AppID: 1, Name: "Game Alpha", Status: "installed", Type: "game"},
		{AppID: 2, Name: "Alpha OST", Status: "installed", Type: "music", Size: 512},
		{AppID: 3, Name: "Music Only", Status: "extra", Type: "music", Size: 256},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)

	if got := m.filtered(); len(got) != 1 || got[0].AppID != 1 {
		t.Fatalf("全部视图不应包含音乐: %#v", got)
	}
	if got := m.viewCount(tuiViewAll); got != 1 {
		t.Fatalf("全部视图计数 = %d, want 1", got)
	}
	m.view = tuiViewInstalled
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 1 {
		t.Fatalf("已安装视图不应包含音乐: %#v", got)
	}
	m.view = tuiViewMusic
	if got := m.filtered(); len(got) != 2 || got[0].AppID != 2 || got[1].AppID != 3 {
		t.Fatalf("音乐视图应只包含原声带: %#v", got)
	}
	if got := m.viewCount(tuiViewMusic); got != 2 {
		t.Fatalf("音乐视图计数 = %d, want 2", got)
	}
	m.query = "ost"
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 2 {
		t.Fatalf("音乐视图搜索失败: %#v", got)
	}
}

func TestTUIMusicPlayMessage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write track: %v", err)
	}
	games := []tuiGame{
		{AppID: 1, Name: "Game Alpha", Status: "installed", Type: "game"},
		{AppID: 2, Name: "Alpha OST", Status: "installed", Type: "music", Dir: dir},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewMusic

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("音乐视图按 Enter 应返回播放命令")
	}
	if got := updated.(tuiModel).message; !strings.Contains(got, "正在打开本地播放器") {
		t.Fatalf("提示 = %q", got)
	}

	updated, _ = m.Update(tuiMusicStartMsg{appID: 2, index: 0, err: os.ErrNotExist})
	if got := updated.(tuiModel).message; !strings.Contains(got, "播放失败") {
		t.Fatalf("失败提示 = %q", got)
	}
}

func TestTUIMusicTrackSelection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}
	games := []tuiGame{
		{AppID: 2, Name: "Alpha OST", Status: "installed", Type: "music", Dir: dir},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewMusic

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 1 {
		t.Fatalf("J 应选择第二条音轨，得到 %d", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 2 {
		t.Fatalf("J 应选择第三条音轨，得到 %d", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 2 {
		t.Fatalf("J 不应越界，得到 %d", got)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 1 {
		t.Fatalf("K 应回到第二条音轨，得到 %d", got)
	}

	lines := m.musicDetailLines(&m.games[0], 60, 30)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "音轨（3）") || !strings.Contains(joined, "02.mp3") {
		t.Fatalf("详情页应显示音轨列表:\n%s", joined)
	}
	if !strings.Contains(joined, "›") {
		t.Fatalf("详情页应标记选中的音轨:\n%s", joined)
	}
}

func TestTUIMusicSwitchTrackWithoutPlayback(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}
	games := []tuiGame{
		{AppID: 2, Name: "Alpha OST", Status: "installed", Type: "music", Dir: dir},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewMusic

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatal("未播放时按 n 不应启动播放器")
	}
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 1 {
		t.Fatalf("未播放时 n 应选择下一条音轨，得到 %d", got)
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if cmd != nil {
		t.Fatal("未播放时按 N 不应启动播放器")
	}
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 0 {
		t.Fatalf("未播放时 N 应选择上一条音轨，得到 %d", got)
	}
}

func TestTUIMusicPlayingAppID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write track: %v", err)
	}
	games := []tuiGame{
		{AppID: 42, Name: "Alpha OST", Status: "installed", Type: "music", Dir: dir},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewMusic

	label := m.trackLabel(42, 0, false)
	if !strings.Contains(label, "APPID 42") || !strings.Contains(label, "01.mp3") {
		t.Fatalf("播放提示应包含 APPID 和音轨名: %q", label)
	}
	summary := nowPlayingLabel(42, 0, 3, false, true)
	if !strings.Contains(summary, "APPID 42") || !strings.Contains(summary, "1/3") {
		t.Fatalf("状态栏播放信息应包含 APPID 和进度: %q", summary)
	}
}

func TestTUIPauseWithoutPlayback(t *testing.T) {
	games := []tuiGame{{AppID: 2, Name: "Alpha OST", Status: "installed", Type: "music"}}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewMusic
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if got := updated.(tuiModel).message; !strings.Contains(got, "没有正在播放") {
		t.Fatalf("提示 = %q", got)
	}
}

func TestTUIEnterOnGameDoesNotPlay(t *testing.T) {
	games := []tuiGame{{AppID: 1, Name: "Game Alpha", Status: "installed", Type: "game"}}
	m, _ := newTestTUIModel(t, games, nil, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("游戏视图按 Enter 不应启动播放器")
	}
}

func TestTUISearch(t *testing.T) {
	games := []tuiGame{
		{AppID: 440, Name: "Team Fortress 2", Status: "installed", Dir: "/steam/steamapps/common/Team Fortress 2"},
		{AppID: 570, Name: "Dota 2", Status: "missing", Library: "/steam"},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)

	m.query = "fortress"
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 440 {
		t.Fatalf("按名称搜索失败: %#v", got)
	}
	m.query = "570"
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 570 {
		t.Fatalf("按 appid 搜索失败: %#v", got)
	}
	m.query = "steamapps"
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 440 {
		t.Fatalf("按路径搜索失败: %#v", got)
	}
	m.query = "fortress dota"
	if got := m.filtered(); len(got) != 0 {
		t.Fatalf("多个关键词应同时匹配，得到 %#v", got)
	}
}

func TestTUIToggleFavorite(t *testing.T) {
	games := []tuiGame{{AppID: 440, Name: "Team Fortress 2", Status: "installed"}}
	m, st := newTestTUIModel(t, games, nil, nil)

	m.toggleFavorite()
	data, err := st.LoadFavorites()
	if err != nil {
		t.Fatalf("load favorites: %v", err)
	}
	if len(data.Games) != 1 || data.Games[0].Status != favorites.StatusWant {
		t.Fatalf("收藏写入不正确: %#v", data.Games)
	}
	if m.games[0].Favorite == nil {
		t.Fatal("收藏后界面状态没有更新")
	}

	m.cycleFavoriteStatus()
	data, err = st.LoadFavorites()
	if err != nil {
		t.Fatalf("load favorites: %v", err)
	}
	if data.Games[0].Status != favorites.StatusPlaying {
		t.Fatalf("收藏状态应循环到在玩，得到 %q", data.Games[0].Status)
	}

	m.toggleFavorite()
	data, err = st.LoadFavorites()
	if err != nil {
		t.Fatalf("load favorites: %v", err)
	}
	if len(data.Games) != 0 {
		t.Fatalf("取消收藏后应为空: %#v", data.Games)
	}
}

func TestTUITogglePatches(t *testing.T) {
	pd := &model.PatchesData{Patches: []model.Patch{{AppID: 440, Name: "patch.xp3", Applied: false}}}
	games := []tuiGame{{AppID: 440, Name: "Team Fortress 2", Status: "installed"}}
	applyTUIExtras(games, &model.FavoritesData{}, pd, &model.SavesData{})
	m, st := newTestTUIModel(t, games, &model.FavoritesData{}, pd)

	m.togglePatches()
	data, err := st.LoadPatches()
	if err != nil {
		t.Fatalf("load patches: %v", err)
	}
	if len(data.Patches) != 1 || !data.Patches[0].Applied {
		t.Fatalf("补丁状态应变为已打: %#v", data.Patches)
	}
	if len(m.games[0].Patches) != 1 || !m.games[0].Patches[0].Applied {
		t.Fatalf("界面补丁状态没有更新: %#v", m.games[0].Patches)
	}

	m.togglePatches()
	data, err = st.LoadPatches()
	if err != nil {
		t.Fatalf("load patches: %v", err)
	}
	if data.Patches[0].Applied {
		t.Fatalf("补丁状态应变为未打: %#v", data.Patches)
	}
}

func TestTUIViewFitsTerminal(t *testing.T) {
	fav := &model.Favorite{AppID: 1, Name: "Alpha", Status: favorites.StatusPlaying}
	games := []tuiGame{
		{AppID: 1, Name: "Alpha 很长很长的游戏名称用于测试截断", Status: "installed", Favorite: fav, Dir: "/steam/steamapps/common/Alpha", Size: 1024 * 1024 * 1024},
		{AppID: 2, Name: "Beta", Status: "missing"},
	}
	pd := &model.PatchesData{Patches: []model.Patch{{AppID: 1, Name: "patch.xp3", Applied: true, Hash: "0123456789abcdef"}}}
	games[0].Patches = patches.FindAll(pd, 1)
	for _, size := range [][2]int{{100, 30}, {60, 20}} {
		m, _ := newTestTUIModel(t, games, &model.FavoritesData{Games: []model.Favorite{*fav}}, pd)
		m.width, m.height = size[0], size[1]
		out := m.View()
		lines := strings.Split(out, "\n")
		if len(lines) > size[1] {
			t.Fatalf("%dx%d: 输出 %d 行，超过终端高度", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if w := displayWidth(line); w > size[0] {
				t.Fatalf("%dx%d: 第 %d 行宽度 %d 超出: %q", size[0], size[1], i+1, w, line)
			}
		}
	}
}

func TestTUIHelpViewFitsTerminal(t *testing.T) {
	m, _ := newTestTUIModel(t, nil, nil, nil)
	m.width, m.height = 60, 20
	m.showHelp = true
	out := m.View()
	for i, line := range strings.Split(out, "\n") {
		if w := displayWidth(line); w > m.width {
			t.Fatalf("帮助第 %d 行宽度 %d 超出: %q", i+1, w, line)
		}
	}
}

func TestTUIPatchView(t *testing.T) {
	games := []tuiGame{
		{AppID: 440, Name: "Team Fortress 2", Status: "installed", Patches: []model.Patch{{AppID: 440, Name: "hd.vpk", Hash: "deadbeef", Applied: true}}},
		{AppID: 570, Name: "Dota 2", Status: "missing"},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewPatches
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 440 {
		t.Fatalf("补丁视图应只包含有补丁的游戏: %#v", got)
	}
	if got := m.viewCount(tuiViewPatches); got != 1 {
		t.Fatalf("补丁视图计数 = %d", got)
	}
	m.query = "hd.vpk"
	if got := m.filtered(); len(got) != 1 {
		t.Fatalf("按补丁名搜索失败: %#v", got)
	}
	m.query = "deadbeef"
	if got := m.filtered(); len(got) != 1 {
		t.Fatalf("按补丁哈希搜索失败: %#v", got)
	}
}

func TestTUIHandlePatchCheck(t *testing.T) {
	pd := &model.PatchesData{Patches: []model.Patch{{AppID: 440, Name: "hd.vpk"}}}
	m, _ := newTestTUIModel(t, []tuiGame{{AppID: 440, Name: "TF2", Patches: pd.Patches}}, nil, pd)
	report := &patches.CheckReport{
		Total: 1, OK: 1,
		Results: []patches.CheckResult{{AppID: 440, Name: "hd.vpk", Status: patches.CheckOK}},
	}
	updated, _ := m.Update(tuiPatchCheckMsg{appID: 440, report: report})
	m = updated.(tuiModel)
	got, ok := m.checks[tuiPatchCheckKey(440, "hd.vpk")]
	if !ok || got.Status != patches.CheckOK {
		t.Fatalf("校验结果未写入: %#v", m.checks)
	}
	if !strings.Contains(m.message, "已找到 1") {
		t.Fatalf("校验提示 = %q", m.message)
	}
	if glyph, _ := m.patchGlyph(m.games[0]); glyph != "✓" {
		t.Fatalf("校验通过后图标 = %q", glyph)
	}
}

func TestTUIHandlePatchInstall(t *testing.T) {
	pd := &model.PatchesData{Patches: []model.Patch{{AppID: 440, Name: "hd.vpk", Applied: false}}}
	m, st := newTestTUIModel(t, []tuiGame{{AppID: 440, Name: "TF2", Patches: pd.Patches}}, nil, pd)
	updated, _ := m.Update(tuiPatchInstallMsg{
		appID:   440,
		results: []tuiInstalledPatch{{name: "hd.vpk", hash: "abc123", files: 3, replaced: 2, backedUp: 2}},
	})
	m = updated.(tuiModel)
	data, err := st.LoadPatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Patches) != 1 || !data.Patches[0].Applied || data.Patches[0].Hash != "abc123" {
		t.Fatalf("补丁记录未更新: %#v", data.Patches)
	}
	if len(m.games[0].Patches) != 1 || !m.games[0].Patches[0].Applied {
		t.Fatalf("界面补丁状态未更新: %#v", m.games[0].Patches)
	}
	if !strings.Contains(m.message, "已安装 1 个补丁") {
		t.Fatalf("安装提示 = %q", m.message)
	}
}

func TestTUIInstallPatchesCmd(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.PatchesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(st.PatchesDir(), "patch.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("savedata/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	gameDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameDir, "savedata"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(gameDir, "savedata", "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := tuiGame{AppID: 440, Name: "TF2", Dir: gameDir, Patches: []model.Patch{{AppID: 440, Name: "patch.zip"}}}
	m := newTUIModel(st, []tuiGame{g}, &model.FavoritesData{}, &model.PatchesData{Patches: g.Patches}, &model.SavesData{})
	msg, ok := m.installPatchesCmd(g)().(tuiPatchInstallMsg)
	if !ok {
		t.Fatal("install command returned unexpected message")
	}
	if len(msg.errs) != 0 || len(msg.results) != 1 {
		t.Fatalf("install result = %#v", msg)
	}
	if msg.results[0].files != 1 || msg.results[0].replaced != 1 || msg.results[0].backedUp != 1 {
		t.Fatalf("install counts = %#v", msg.results[0])
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("target content = %q", got)
	}
	backups, err := filepath.Glob(filepath.Join(st.Dir, "patch-backups", "440", "*", "*", "savedata", "file.txt"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup not found: %v %v", backups, err)
	}
}

func TestTUIConfirmViewFitsTerminal(t *testing.T) {
	games := []tuiGame{{AppID: 440, Name: "Team Fortress 2", Dir: "/steam/steamapps/common/Team Fortress 2", Patches: []model.Patch{{AppID: 440, Name: "hd.vpk"}}}}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.width, m.height = 60, 20
	m.confirm = tuiConfirmPatchInstall
	out := m.View()
	for i, line := range strings.Split(out, "\n") {
		if w := displayWidth(line); w > m.width {
			t.Fatalf("确认框第 %d 行宽度 %d 超出: %q", i+1, w, line)
		}
	}
}

func TestTUISaveView(t *testing.T) {
	games := []tuiGame{
		{AppID: 440, Name: "Team Fortress 2", Status: "installed", Save: &model.SaveGame{
			AppID: 440,
			Locations: []model.SaveLocation{
				{Kind: "cloud", Path: "/steam/userdata/123/440/remote", Store: "saves/440/cloud/remote", Files: 2},
				{Kind: "local", Path: "/games/tf2/savedata", Store: "saves/440/local/savedata", Files: 1},
			},
		}},
		{AppID: 570, Name: "Dota 2", Status: "missing"},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewSaves
	if got := m.filtered(); len(got) != 1 || got[0].AppID != 440 {
		t.Fatalf("存档视图应只包含有存档的游戏: %#v", got)
	}
	if got := m.viewCount(tuiViewSaves); got != 1 {
		t.Fatalf("存档视图计数 = %d", got)
	}
	if glyph, _ := m.saveGlyph(games[0]); glyph != "B" {
		t.Fatalf("云+本地图标 = %q", glyph)
	}
	m.query = "cloud"
	if got := m.filtered(); len(got) != 1 {
		t.Fatalf("按存档类型搜索失败: %#v", got)
	}
	m.query = "savedata"
	if got := m.filtered(); len(got) != 1 {
		t.Fatalf("按存档路径搜索失败: %#v", got)
	}
	if summary := tuiSaveSummary(games[0].Save); !strings.Contains(summary, "云+本地") || !strings.Contains(summary, "3 文件") {
		t.Fatalf("存档摘要 = %q", summary)
	}
}

func TestTUISyncAndRestoreSavesCmd(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gameDir := t.TempDir()
	saveDir := filepath.Join(gameDir, "savedata")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	localFile := filepath.Join(saveDir, "slot1.sav")
	if err := os.WriteFile(localFile, []byte("local-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	steamRoot := t.TempDir()
	cloudDir := filepath.Join(steamRoot, "userdata", "12345", "440", "remote")
	if err := os.MkdirAll(cloudDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cloudFile := filepath.Join(cloudDir, "cloud.sav")
	if err := os.WriteFile(cloudFile, []byte("cloud-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := &model.InstalledData{
		SteamRoot: steamRoot,
		Games:     []model.App{{AppID: 440, Name: "Test Game", Installed: true, Dir: gameDir}},
	}
	if err := st.SaveInstalled(installed); err != nil {
		t.Fatal(err)
	}
	g := tuiGame{AppID: 440, Name: "Test Game", Status: "installed", Dir: gameDir}
	m := newTUIModel(st, []tuiGame{g}, &model.FavoritesData{}, &model.PatchesData{}, &model.SavesData{})
	m.width, m.height = 100, 30
	m.ready = true
	m.cfg = &model.Config{SteamDir: steamRoot}
	m.installed = installed

	msg, ok := m.syncSavesCmd(&g)().(tuiSaveSyncMsg)
	if !ok || msg.err != nil {
		t.Fatalf("sync result = %#v", msg)
	}
	if msg.result == nil || len(msg.result.Saves) != 1 || len(msg.result.Saves[0].Locations) != 2 {
		t.Fatalf("sync locations = %#v", msg.result)
	}
	data, err := st.LoadSaves()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Saves) != 1 || len(data.Saves[0].Locations) != 2 {
		t.Fatalf("saves.json = %#v", data)
	}

	backupLocal := filepath.Join(st.Dir, "saves", "440", "local", "savedata", "slot1.sav")
	if err := os.WriteFile(backupLocal, []byte("local-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.saveData = data
	restored, ok := m.restoreSavesCmd(g)().(tuiSaveRestoreMsg)
	if !ok || restored.err != nil {
		t.Fatalf("restore result = %#v", restored)
	}
	if restored.result == nil || len(restored.result.Saves) != 1 || len(restored.result.Saves[0].Locations) != 2 {
		t.Fatalf("restore locations = %#v", restored.result)
	}
	if content, err := os.ReadFile(localFile); err != nil || string(content) != "local-v2" {
		t.Fatalf("restored local = %q, %v", content, err)
	}

	m.removeSaves(g)
	data, err = st.LoadSaves()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Saves) != 0 {
		t.Fatalf("删除后 saves.json = %#v", data)
	}
	if _, err := os.Stat(filepath.Join(st.Dir, "saves", "440")); !os.IsNotExist(err) {
		t.Fatalf("备份目录应已删除: %v", err)
	}
}

func TestTUIHandleSaveSyncMessage(t *testing.T) {
	pd := &model.PatchesData{}
	games := []tuiGame{{AppID: 440, Name: "Test Game", Status: "installed"}}
	m, st := newTestTUIModel(t, games, nil, pd)
	data := &model.SavesData{Saves: []model.SaveGame{{AppID: 440, Name: "Test Game", Locations: []model.SaveLocation{
		{Kind: "cloud", Files: 2, Bytes: 2048},
		{Kind: "local", Files: 1, Bytes: 1024},
	}}}}
	if err := st.SaveSaves(data); err != nil {
		t.Fatal(err)
	}
	result := &saves.SyncResult{Saves: []saves.GameResult{{
		AppID: 440,
		Locations: []saves.LocationResult{
			{Kind: "cloud", Files: 2, Bytes: 2048},
			{Kind: "local", Files: 1, Bytes: 1024},
		},
	}}}
	updated, _ := m.Update(tuiSaveSyncMsg{appID: 440, result: result})
	m = updated.(tuiModel)
	if !strings.Contains(m.message, "存档已备份") || !strings.Contains(m.message, "3 个文件") {
		t.Fatalf("同步提示 = %q", m.message)
	}
	if m.games[0].Save == nil || len(m.games[0].Save.Locations) != 2 {
		t.Fatalf("界面存档状态未更新: %#v", m.games[0].Save)
	}
}

func TestTUIInstalledViewShowsSize(t *testing.T) {
	games := []tuiGame{
		{AppID: 1, Name: "Alpha", Status: "installed", Size: 1024},
		{AppID: 2, Name: "Beta", Status: "installed", Size: 512},
		{AppID: 3, Name: "Gamma", Status: "missing"},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.view = tuiViewInstalled

	status := m.statusView(m.width, m.filtered())
	if !strings.Contains(status, "已安装 2 款") || !strings.Contains(status, "占用 1.5 KiB") {
		t.Fatalf("已安装状态栏应显示占用总量: %q", status)
	}
	list := m.listPane(45, 10, m.filtered())
	if !strings.Contains(list, "1.0 KiB") || !strings.Contains(list, "512 B") {
		t.Fatalf("已安装列表应显示每款游戏的占用: %q", list)
	}
	for _, size := range [][2]int{{100, 30}, {60, 20}} {
		m.width, m.height = size[0], size[1]
		if line := m.listRow(m.games[0], 40, false); displayWidth(line) > 40 {
			t.Fatalf("列表行宽度 %d 超出 40: %q", displayWidth(line), line)
		}
	}
}

func TestTUIMouseClickTab(t *testing.T) {
	m, _ := newTestTUIModel(t, []tuiGame{{AppID: 1, Name: "Alpha", Status: "installed"}}, nil, nil)
	if m.view != tuiViewAll {
		t.Fatalf("初始视图 = %v", m.view)
	}
	_, spans := m.tabsLayout(m.width)
	x := (spans[1][0] + spans[1][1]) / 2
	updated, _ := m.Update(tea.MouseMsg{X: x, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := updated.(tuiModel).view; got != tuiViewInstalled {
		t.Fatalf("点击第 2 个标签应切换到已安装，得到 %v", got)
	}
}

func TestTUIMouseClickSelectsRow(t *testing.T) {
	games := []tuiGame{
		{AppID: 1, Name: "Alpha", Status: "installed"},
		{AppID: 2, Name: "Beta", Status: "installed"},
		{AppID: 3, Name: "Gamma", Status: "installed"},
	}
	m, _ := newTestTUIModel(t, games, nil, nil)
	updated, _ := m.Update(tea.MouseMsg{X: 2, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := updated.(tuiModel).cursor; got != 1 {
		t.Fatalf("点击第 2 行应选中下标 1，得到 %d", got)
	}
}

func TestTUIMusicMouseTrackSelectAndDrag(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}
	m, _ := newTestTUIModel(t, []tuiGame{
		{AppID: 2, Name: "Alpha OST", Status: "installed", Type: "music", Dir: dir},
	}, nil, nil)
	m.view = tuiViewMusic

	x := m.listWidth(m.width) + 3
	first := 3 + musicTrackStartRow(m.games[0])

	updated, _ := m.Update(tea.MouseMsg{X: x, Y: first + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 1 {
		t.Fatalf("点击第二条音轨应选中下标 1，得到 %d", got)
	}

	updated, _ = m.Update(tea.MouseMsg{X: x, Y: first + 2, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 2 {
		t.Fatalf("按住滑行到第三条音轨应选中下标 2，得到 %d", got)
	}

	updated, _ = m.Update(tea.MouseMsg{X: x, Y: first + 2, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone})
	m = updated.(tuiModel)
	updated, _ = m.Update(tea.MouseMsg{X: x, Y: first, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 2 {
		t.Fatalf("松开后滑行不应改变选择，得到 %d", got)
	}

	updated, _ = m.Update(tea.MouseMsg{X: 2, Y: first, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = updated.(tuiModel)
	if got := m.trackSel[2]; got != 2 {
		t.Fatalf("点击左侧列表不应选择音轨，得到 %d", got)
	}
}

func TestTUIMouseWheelScrolls(t *testing.T) {
	games := make([]tuiGame, 6)
	for i := range games {
		games[i] = tuiGame{AppID: i + 1, Name: fmt.Sprintf("Game %d", i+1), Status: "installed"}
	}
	m, _ := newTestTUIModel(t, games, nil, nil)

	updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if got := updated.(tuiModel).cursor; got != 3 {
		t.Fatalf("向下滚轮应移动 3 行，得到 %d", got)
	}
	updated, _ = updated.(tuiModel).Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if got := updated.(tuiModel).cursor; got != 0 {
		t.Fatalf("向上滚轮应回到顶部，得到 %d", got)
	}
}

func TestTUIMouseIgnoredInConfirm(t *testing.T) {
	m, _ := newTestTUIModel(t, []tuiGame{{AppID: 1, Name: "Alpha", Status: "installed"}}, nil, nil)
	m.confirm = tuiConfirmPatchInstall
	updated, _ := m.Update(tea.MouseMsg{X: 2, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := updated.(tuiModel); got.confirm != tuiConfirmPatchInstall || got.cursor != 0 {
		t.Fatalf("确认框显示时鼠标点击不应改变状态: %#v", got)
	}
}

func TestTUIConfirmSaveViewFitsTerminal(t *testing.T) {
	games := []tuiGame{{AppID: 440, Name: "Team Fortress 2", Save: &model.SaveGame{
		AppID: 440,
		Locations: []model.SaveLocation{
			{Kind: "cloud", Path: "/steam/userdata/12345/440/remote", Files: 2, Bytes: 2048},
			{Kind: "local", Path: "/games/tf2/savedata", Files: 1, Bytes: 1024},
		},
	}}}
	m, _ := newTestTUIModel(t, games, nil, nil)
	m.width, m.height = 60, 20
	m.confirm = tuiConfirmSaveRestore
	out := m.View()
	for i, line := range strings.Split(out, "\n") {
		if w := displayWidth(line); w > m.width {
			t.Fatalf("存档确认框第 %d 行宽度 %d 超出: %q", i+1, w, line)
		}
	}
}
