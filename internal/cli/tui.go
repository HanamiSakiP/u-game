package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/HanamiSakiP/u-game/internal/cover"
	"github.com/HanamiSakiP/u-game/internal/favorites"
	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/patches"
	"github.com/HanamiSakiP/u-game/internal/player"
	"github.com/HanamiSakiP/u-game/internal/remote"
	"github.com/HanamiSakiP/u-game/internal/saves"
	"github.com/HanamiSakiP/u-game/internal/steam"
	"github.com/HanamiSakiP/u-game/internal/store"
)

// tuiView 是 TUI 左侧列表的筛选视图。
type tuiView int

const (
	tuiViewAll tuiView = iota
	tuiViewInstalled
	tuiViewMissing
	tuiViewFavorites
	tuiViewPatches
	tuiViewSaves
	tuiViewMusic
	tuiViewCount
)

var tuiViewNames = []string{"全部", "已安装", "未安装", "收藏", "补丁", "存档", "音乐"}

// tuiGame 是 TUI 中展示的一款游戏，合并了已拥有、本地安装、收藏和补丁信息。
type tuiGame struct {
	AppID     int
	Name      string
	Status    string
	Type      string
	Library   string
	Dir       string
	Size      int64
	Cover     string
	Publisher string
	Favorite  *model.Favorite
	Patches   []model.Patch
	Save      *model.SaveGame
}

func (g tuiGame) installed() bool {
	return g.Status == "installed" || g.Status == "extra"
}

func (g tuiGame) isMusic() bool {
	return g.Type == "music"
}

var (
	tuiAccent = lipgloss.AdaptiveColor{Light: "#0B6E99", Dark: "#66C0F4"}
	tuiPink   = lipgloss.AdaptiveColor{Light: "#B4006B", Dark: "#F06BB0"}
	tuiGreen  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#7BD88F"}
	tuiYellow = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"}
	tuiRed    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF7B72"}
	tuiMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#7D8590"}
	tuiBorder = lipgloss.AdaptiveColor{Light: "#B6C2CF", Dark: "#30363D"}
	tuiText   = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#E6EDF3"}
	tuiSelBg  = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#264F78"}
)

var (
	tuiTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(tuiAccent)
	tuiMutedStyle    = lipgloss.NewStyle().Foreground(tuiMuted)
	tuiTextStyle     = lipgloss.NewStyle().Foreground(tuiText)
	tuiAccentStyle   = lipgloss.NewStyle().Foreground(tuiAccent)
	tuiPinkStyle     = lipgloss.NewStyle().Foreground(tuiPink)
	tuiGreenStyle    = lipgloss.NewStyle().Foreground(tuiGreen)
	tuiYellowStyle   = lipgloss.NewStyle().Foreground(tuiYellow)
	tuiRedStyle      = lipgloss.NewStyle().Foreground(tuiRed)
	tuiSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(tuiSelBg).Bold(true)
	tuiTabActive     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0B1117")).Background(tuiAccent).Padding(0, 1)
	tuiTabInactive   = lipgloss.NewStyle().Foreground(tuiMuted).Padding(0, 1)
	tuiPaneStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tuiBorder).Padding(0, 1)
	tuiHelpPaneStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tuiAccent).Padding(0, 1)
)

// tuiConfirm 是当前等待确认的操作。
type tuiConfirm int

const (
	tuiConfirmNone tuiConfirm = iota
	tuiConfirmPatchInstall
	tuiConfirmSaveRestore
	tuiConfirmSaveRemove
)

// tuiModel 是 bubbletea 的界面状态。
type tuiModel struct {
	st        *store.Store
	games     []tuiGame
	favData   *model.FavoritesData
	patchData *model.PatchesData
	saveData  *model.SavesData
	cfg       *model.Config
	installed *model.InstalledData

	view       tuiView
	cursor     int
	width      int
	height     int
	ready      bool
	query      string
	searchMode bool
	showHelp   bool
	showHidden bool
	message    string

	checks     map[string]patches.CheckResult
	confirm    tuiConfirm
	installing bool
	checking   bool
	syncing    bool

	publishersLoaded bool

	tracks    map[string][]string
	trackErrs map[string]string
	trackSel  map[int]int
	trackDrag bool

	play    *player.Controller
	playApp int
	playSeq int
}

func newTUIModel(st *store.Store, games []tuiGame, fav *model.FavoritesData, pd *model.PatchesData, saves *model.SavesData) tuiModel {
	return tuiModel{
		st:        st,
		games:     games,
		favData:   fav,
		patchData: pd,
		saveData:  saves,
		tracks:    make(map[string][]string),
		trackErrs: make(map[string]string),
		trackSel:  make(map[int]int),
	}
}

// runTUI 启动全屏交互界面。
func runTUI(dataDir string, stdout, stderr io.Writer) error {
	if !isTerminal(stdout) || !isTerminal(os.Stdin) {
		return errors.New("tui 需要在交互式终端中运行")
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	if _, err := st.EnsurePatchesFile(); err != nil {
		fmt.Fprintf(stderr, "警告: 创建补丁记录文件失败: %v\n", err)
	}
	games, fav, pd, saves, err := loadTUIGames(st)
	if err != nil {
		return err
	}
	cfg, err := st.LoadConfig()
	if err != nil {
		return err
	}
	installed, err := st.LoadInstalled()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		installed = nil
	}
	m := newTUIModel(st, games, fav, pd, saves)
	m.cfg = cfg
	m.installed = installed
	program := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithOutput(stdout),
	)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("TUI 运行失败: %w", err)
	}
	return nil
}

// loadTUIGames 从数据目录读取游戏、收藏、补丁和存档信息。
func loadTUIGames(st *store.Store) ([]tuiGame, *model.FavoritesData, *model.PatchesData, *model.SavesData, error) {
	var owned *model.OwnedData
	var installed *model.InstalledData
	var err error
	if owned, err = st.LoadOwned(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil, err
		}
		owned = nil
	}
	if installed, err = st.LoadInstalled(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil, err
		}
		installed = nil
	}
	entries, err := buildEntries(owned, installed)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	fav := &model.FavoritesData{}
	if data, err := st.LoadFavorites(); err == nil {
		fav = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil, err
	}
	pd := &model.PatchesData{}
	if data, err := st.LoadPatches(); err == nil {
		pd = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil, err
	}
	saves := &model.SavesData{}
	if data, err := st.LoadSaves(); err == nil {
		saves = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil, err
	}

	favorites.Normalize(fav)
	patches.Normalize(pd)

	nameCache := map[string]string{}
	if data, err := st.LoadAppNames(); err == nil && data.Names != nil {
		nameCache = data.Names
	}
	covers := cover.FindAll(st.CoversDir())

	games := make([]tuiGame, 0, len(entries)+len(fav.Games))
	seen := make(map[int]bool, len(entries))
	for _, e := range entries {
		games = append(games, tuiGame{
			AppID:   e.AppID,
			Name:    e.Name,
			Status:  e.Status,
			Type:    e.Type,
			Library: e.Library,
			Dir:     e.Dir,
			Size:    e.Size,
			Cover:   covers[e.AppID],
		})
		seen[e.AppID] = true
	}
	// 收藏夹里可能有 owned.json / installed.json 中没有的游戏，也一并显示。
	for _, f := range fav.Games {
		if seen[f.AppID] {
			continue
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = strings.TrimSpace(nameCache[strconv.Itoa(f.AppID)])
		}
		if name == "" {
			name = fmt.Sprintf("App %d", f.AppID)
		}
		games = append(games, tuiGame{AppID: f.AppID, Name: name, Status: "unknown", Cover: covers[f.AppID]})
		seen[f.AppID] = true
	}
	publishers := tuiPublisherCache(st)
	for i := range games {
		games[i].Publisher = publishers[games[i].AppID]
	}
	sort.SliceStable(games, func(i, j int) bool {
		a, b := strings.ToLower(games[i].Name), strings.ToLower(games[j].Name)
		if a != b {
			return a < b
		}
		return games[i].AppID < games[j].AppID
	})
	applyTUIExtras(games, fav, pd, saves)
	return games, fav, pd, saves, nil
}

// tuiPublisherCache 读取 publishers.json 缓存里的发行商，并套用分组归并规则。
func tuiPublisherCache(st *store.Store) map[int]string {
	publishers := make(map[int]string)
	data, err := st.LoadPublishers()
	if err != nil {
		return publishers
	}
	for key, publisher := range data.Publishers {
		id, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || id <= 0 {
			continue
		}
		if publisher = groupPublisher(publisher); publisher != "" {
			publishers[id] = publisher
		}
	}
	return publishers
}

// saveTuiPublishers 把后台获取到的发行商写回 publishers.json 缓存。
func saveTuiPublishers(st *store.Store, fetched map[int]string) error {
	if len(fetched) == 0 {
		return nil
	}
	cache, err := st.LoadPublishers()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		cache = &model.PublishersData{Publishers: make(map[string]string)}
	}
	if cache.Publishers == nil {
		cache.Publishers = make(map[string]string)
	}
	for id, publisher := range fetched {
		if publisher = strings.TrimSpace(publisher); publisher != "" {
			cache.Publishers[strconv.Itoa(id)] = publisher
		}
	}
	return st.SavePublishers(cache)
}

// applyTUIExtras 把收藏、补丁和存档记录挂到游戏列表上。
func applyTUIExtras(games []tuiGame, fav *model.FavoritesData, pd *model.PatchesData, saves *model.SavesData) {
	favMap := make(map[int]model.Favorite)
	if fav != nil {
		for _, f := range fav.Games {
			favMap[f.AppID] = f
		}
	}
	saveMap := make(map[int]model.SaveGame)
	if saves != nil {
		for _, s := range saves.Saves {
			saveMap[s.AppID] = s
		}
	}
	for i := range games {
		g := &games[i]
		if f, ok := favMap[g.AppID]; ok {
			copied := f
			g.Favorite = &copied
		} else {
			g.Favorite = nil
		}
		g.Patches = patches.FindAll(pd, g.AppID)
		if s, ok := saveMap[g.AppID]; ok {
			copied := s
			g.Save = &copied
		} else {
			g.Save = nil
		}
	}
}

type tuiPatchCheckMsg struct {
	appID  int
	report *patches.CheckReport
}

type tuiMusicStartMsg struct {
	appID int
	index int
	ctrl  *player.Controller
	err   error
}

type tuiMusicTickMsg struct {
	seq int
}

type tuiPublishersMsg struct {
	publishers map[int]string
	err        error
}

type tuiInstalledPatch struct {
	name     string
	hash     string
	files    int
	replaced int
	backedUp int
}

type tuiPatchInstallMsg struct {
	appID   int
	game    string
	results []tuiInstalledPatch
	errs    []string
}

func (m tuiModel) Init() tea.Cmd { return nil }

// fetchFavoritesPublishersCmd 在后台获取没有备注的收藏的发行商，
// 用于按发行商排序；离线时直接跳过。
func (m tuiModel) fetchFavoritesPublishersCmd() tea.Cmd {
	if offlineRequested(false) {
		return nil
	}
	seen := make(map[int]bool)
	ids := make([]int, 0)
	for _, g := range m.games {
		if g.Favorite == nil || seen[g.AppID] {
			continue
		}
		if strings.TrimSpace(g.Favorite.Note) != "" || strings.TrimSpace(g.Publisher) != "" {
			continue
		}
		seen[g.AppID] = true
		ids = append(ids, g.AppID)
	}
	if len(ids) == 0 {
		return nil
	}
	userAgent := ""
	if m.cfg != nil {
		userAgent = m.cfg.UserAgent
	}
	return func() tea.Msg {
		fetched, err := remote.FetchPublishers(context.Background(), remote.PublisherOptions{
			AppIDs:    ids,
			UserAgent: userAgent,
			Timeout:   12 * time.Second,
		})
		return tuiPublishersMsg{publishers: fetched, err: err}
	}
}

// checkPatchesCmd 在后台校验补丁文件，appID 为 0 时校验全部记录。
func (m tuiModel) checkPatchesCmd(appID int) tea.Cmd {
	var list []model.Patch
	if m.patchData != nil {
		if appID == 0 {
			list = append(list, m.patchData.Patches...)
		} else {
			list = patches.FindAll(m.patchData, appID)
		}
	}
	dir := m.st.PatchesDir()
	return func() tea.Msg {
		return tuiPatchCheckMsg{
			appID:  appID,
			report: patches.CheckDir(&model.PatchesData{Patches: list}, dir),
		}
	}
}

// startMusicCmd 在后台启动可控制的播放器，从指定音轨开始播放。
func (m tuiModel) startMusicCmd(g tuiGame, index int) tea.Cmd {
	return func() tea.Msg {
		ctrl, err := player.Start(player.Options{Dir: g.Dir}, index)
		return tuiMusicStartMsg{appID: g.AppID, index: index, ctrl: ctrl, err: err}
	}
}

// musicTickCmd 定时刷新 mpv 的播放进度，让 TUI 能跟随自动切歌。
func (m tuiModel) musicTickCmd(seq int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return tuiMusicTickMsg{seq: seq}
	})
}

// installPatchesCmd 在后台把选中的游戏补丁解压到游戏目录，并先备份原文件。
func (m tuiModel) installPatchesCmd(g tuiGame) tea.Cmd {
	st := m.st
	return func() tea.Msg {
		msg := tuiPatchInstallMsg{appID: g.AppID, game: g.Name}
		if strings.TrimSpace(g.Dir) == "" {
			msg.errs = append(msg.errs, "未找到游戏目录，请先运行 ugal scan")
			return msg
		}
		index := patches.IndexDir(st.PatchesDir())
		backupRoot := filepath.Join(st.Dir, "patch-backups", strconv.Itoa(g.AppID), time.Now().Format("20060102-150405.000000000"))
		for i, p := range g.Patches {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				msg.errs = append(msg.errs, "补丁记录缺少文件名")
				continue
			}
			archive := index.Find(name, g.AppID)
			if archive == "" {
				msg.errs = append(msg.errs, fmt.Sprintf("%s: 未找到补丁文件", name))
				continue
			}
			res, err := patches.Install(patches.InstallOptions{
				Archive:   archive,
				GameDir:   g.Dir,
				BackupDir: filepath.Join(backupRoot, strconv.Itoa(i)),
			})
			if err != nil {
				msg.errs = append(msg.errs, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			hash, _ := patches.HashFileWith(archive, "md5")
			msg.results = append(msg.results, tuiInstalledPatch{
				name:     name,
				hash:     hash,
				files:    res.Files,
				replaced: res.Replaced,
				backedUp: res.BackedUp,
			})
		}
		return msg
	}
}

type tuiSaveSyncMsg struct {
	appID     int
	all       bool
	steamUser string
	result    *saves.SyncResult
	err       error
}

type tuiSaveRestoreMsg struct {
	appID  int
	result *saves.RestoreResult
	err    error
}

// syncSavesCmd 在后台把云存档和本地存档复制到数据目录。g 为 nil 时同步全部已安装游戏。
func (m tuiModel) syncSavesCmd(g *tuiGame) tea.Cmd {
	st := m.st
	cfg := m.cfg
	installed := m.installed
	appID := 0
	var appIDs []int
	gameDirs := make(map[int]string)
	names := make(map[int]string)
	if g != nil {
		appID = g.AppID
		appIDs = []int{g.AppID}
		if strings.TrimSpace(g.Dir) != "" {
			gameDirs[g.AppID] = g.Dir
		}
		if strings.TrimSpace(g.Name) != "" {
			names[g.AppID] = g.Name
		}
	}
	return func() tea.Msg {
		steamRoot := ""
		if installed != nil {
			steamRoot = installed.SteamRoot
		}
		steamUser := ""
		configDir := ""
		if cfg != nil {
			configDir = cfg.SteamDir
			if steamRoot == "" {
				steamRoot = cfg.SteamDir
			}
			steamUser = firstNonEmpty(cfg.SteamUser, saves.AccountID(cfg.SteamID))
			if converted := saves.AccountID(steamUser); converted != "" {
				steamUser = converted
			}
		}
		steamRoot = resolveCloudRoot(steamRoot, configDir)
		result, err := saves.Sync(saves.Options{
			Root:      st.Dir,
			Installed: installed,
			AppIDs:    appIDs,
			GameDirs:  gameDirs,
			Names:     names,
			SteamRoot: steamRoot,
			SteamUser: steamUser,
			Cloud:     true,
			Local:     true,
		})
		if err != nil {
			return tuiSaveSyncMsg{appID: appID, all: g == nil, err: err}
		}
		data := &model.SavesData{}
		if d, loadErr := st.LoadSaves(); loadErr == nil {
			data = d
		}
		saves.UpdateManifest(data, result)
		if err := st.SaveSaves(data); err != nil {
			return tuiSaveSyncMsg{appID: appID, all: g == nil, err: err}
		}
		if result.SteamUser != "" {
			if saved, loadErr := st.LoadConfig(); loadErr == nil && saved.SteamUser != result.SteamUser {
				saved.SteamUser = result.SteamUser
				_ = st.SaveConfig(saved)
			}
		}
		return tuiSaveSyncMsg{appID: appID, all: g == nil, steamUser: result.SteamUser, result: result}
	}
}

// restoreSavesCmd 在后台把数据目录中的存档备份写回原始位置。
func (m tuiModel) restoreSavesCmd(g tuiGame) tea.Cmd {
	st := m.st
	data := m.saveData
	return func() tea.Msg {
		result, err := saves.Restore(data, saves.RestoreOptions{
			Root:  st.Dir,
			IDs:   []int{g.AppID},
			Cloud: true,
			Local: true,
		})
		return tuiSaveRestoreMsg{appID: g.AppID, result: result, err: err}
	}
}

// removeSaves 删除一款游戏的存档记录和备份文件。
func (m *tuiModel) removeSaves(g tuiGame) {
	if m.saveData == nil {
		m.message = "没有存档记录"
		return
	}
	saves.Remove(m.saveData, m.st.Dir, []int{g.AppID})
	if err := m.st.SaveSaves(m.saveData); err != nil {
		m.message = fmt.Sprintf("保存存档记录失败: %v", err)
		return
	}
	m.reloadSaveData()
	m.message = fmt.Sprintf("已删除 %s 的存档记录和备份", g.Name)
}

// reloadSaveData 重新读取 saves.json 并把存档信息挂回游戏列表。
func (m *tuiModel) reloadSaveData() {
	data := &model.SavesData{}
	if d, err := m.st.LoadSaves(); err == nil {
		data = d
	}
	m.saveData = data
	m.applyExtras()
	m.clampCursor()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.clampCursor()
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tuiPatchCheckMsg:
		m.checking = false
		if msg.report == nil {
			m.message = "补丁校验失败"
			return m, nil
		}
		if m.checks == nil {
			m.checks = make(map[string]patches.CheckResult)
		}
		for _, r := range msg.report.Results {
			m.checks[tuiPatchCheckKey(r.AppID, r.Name)] = r
		}
		m.message = fmt.Sprintf("补丁校验：已找到 %d · 未找到 %d · 哈希错误 %d · 无法校验 %d",
			msg.report.OK, msg.report.Missing, msg.report.Mismatch, msg.report.Unverified)
		return m, nil
	case tuiPatchInstallMsg:
		m.installing = false
		m.handlePatchInstall(msg)
		return m, nil
	case tuiMusicStartMsg:
		if m.play != nil {
			m.play.Stop()
			m.play = nil
		}
		if msg.err != nil {
			m.message = fmt.Sprintf("播放失败: %v", msg.err)
			return m, nil
		}
		m.play = msg.ctrl
		m.playApp = msg.appID
		m.playSeq++
		index, paused, playing := msg.ctrl.State()
		if playing {
			m.message = fmt.Sprintf("正在播放：%s", m.trackLabel(msg.appID, index, paused))
		} else {
			m.message = fmt.Sprintf("已切换到 %s", m.trackLabel(msg.appID, index, paused))
		}
		return m, m.musicTickCmd(m.playSeq)
	case tuiMusicTickMsg:
		if m.play == nil || msg.seq != m.playSeq {
			return m, nil
		}
		m.play.Refresh()
		if _, _, playing := m.play.State(); !playing {
			return m, nil
		}
		return m, m.musicTickCmd(m.playSeq)
	case tuiPublishersMsg:
		if len(msg.publishers) == 0 {
			if msg.err != nil {
				m.message = "未能获取发行商，收藏按备注排序"
			}
			return m, nil
		}
		selectedID := 0
		if g := m.selected(m.filtered()); g != nil {
			selectedID = g.AppID
		}
		for i := range m.games {
			if publisher := groupPublisher(msg.publishers[m.games[i].AppID]); publisher != "" {
				m.games[i].Publisher = publisher
			}
		}
		if err := saveTuiPublishers(m.st, msg.publishers); err != nil {
			m.message = fmt.Sprintf("保存发行商缓存失败: %v", err)
		}
		if m.view == tuiViewFavorites && selectedID != 0 {
			for i, g := range m.filtered() {
				if g.AppID == selectedID {
					m.cursor = i
					break
				}
			}
			m.clampCursor()
		}
		return m, nil
	case tuiSaveSyncMsg:
		m.syncing = false
		m.handleSaveSync(msg)
		return m, nil
	case tuiSaveRestoreMsg:
		m.syncing = false
		m.handleSaveRestore(msg)
		return m, nil
	case tea.KeyMsg:
		if m.confirm != tuiConfirmNone {
			switch msg.String() {
			case "ctrl+c", "q":
				return m.quit()
			case "enter", "y", "Y":
				kind := m.confirm
				m.confirm = tuiConfirmNone
				g := m.selected(m.filtered())
				if g == nil {
					return m, nil
				}
				switch kind {
				case tuiConfirmPatchInstall:
					m.installing = true
					m.message = "正在安装补丁…"
					return m, m.installPatchesCmd(*g)
				case tuiConfirmSaveRestore:
					m.syncing = true
					m.message = "正在还原存档…"
					return m, m.restoreSavesCmd(*g)
				case tuiConfirmSaveRemove:
					m.removeSaves(*g)
				}
			case "esc", "n", "N":
				m.confirm = tuiConfirmNone
				m.message = "已取消操作"
			}
			return m, nil
		}
		if m.showHelp {
			switch msg.String() {
			case "q", "ctrl+c":
				return m.quit()
			default:
				m.showHelp = false
				return m, nil
			}
		}
		if m.searchMode {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m.quit()
		case "?":
			m.showHelp = true
		case "tab":
			return m, m.setView((m.view + 1) % tuiViewCount)
		case "shift+tab":
			return m, m.setView((m.view + tuiViewCount - 1) % tuiViewCount)
		case "1", "2", "3", "4", "5", "6", "7":
			n, _ := strconv.Atoi(msg.String())
			return m, m.setView(tuiView(n - 1))
		case "/":
			m.searchMode = true
			m.message = ""
		case "esc":
			if m.query != "" {
				m.query = ""
				m.cursor = 0
				m.message = "已清除筛选"
			}
		case "enter", "o":
			if m.view != tuiViewMusic {
				break
			}
			g := m.selected(m.filtered())
			if g == nil || !g.isMusic() {
				break
			}
			tracks, perr := m.tracksFor(*g)
			if perr != "" {
				m.message = perr
				break
			}
			if len(tracks) == 0 {
				m.message = "未找到音频文件"
				break
			}
			index := m.trackCursor(*g, tracks)
			m.message = "正在打开本地播放器…"
			return m, m.startMusicCmd(*g, index)
		case " ":
			m.togglePause()
		case "J":
			m.moveTrack(1)
		case "K":
			m.moveTrack(-1)
		case "n":
			m.switchTrack(1)
		case "N":
			m.switchTrack(-1)
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup", "ctrl+b":
			m.move(-m.pageSize())
		case "pgdown", "ctrl+f":
			m.move(m.pageSize())
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = m.maxCursor()
		case "h":
			m.showHidden = !m.showHidden
			m.cursor = 0
			if m.showHidden {
				m.message = "已显示 Steam 运行库 / 工具"
			} else {
				m.message = "已隐藏 Steam 运行库 / 工具"
			}
		case "f":
			m.toggleFavorite()
		case "s":
			m.cycleFavoriteStatus()
		case "p":
			m.togglePatches()
		case "c":
			if m.installing || m.syncing {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			if m.checking {
				m.message = "正在校验补丁，请稍候"
				break
			}
			g := m.selected(m.filtered())
			if g == nil {
				break
			}
			if len(g.Patches) == 0 {
				m.message = "该游戏没有补丁记录"
				break
			}
			m.checking = true
			m.message = "正在校验补丁文件…"
			return m, m.checkPatchesCmd(g.AppID)
		case "C":
			if m.installing || m.syncing {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			if m.checking {
				m.message = "正在校验补丁，请稍候"
				break
			}
			if m.patchData == nil || len(m.patchData.Patches) == 0 {
				m.message = "patches.json 中还没有补丁记录"
				break
			}
			m.checking = true
			m.message = "正在校验全部补丁文件…"
			return m, m.checkPatchesCmd(0)
		case "i":
			if m.installing || m.syncing {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			g := m.selected(m.filtered())
			if g == nil {
				break
			}
			if len(g.Patches) == 0 {
				m.message = "该游戏没有补丁记录"
				break
			}
			if strings.TrimSpace(g.Dir) == "" {
				m.message = "未找到游戏目录，请先运行 ugal scan"
				break
			}
			m.confirm = tuiConfirmPatchInstall
		case "b":
			if m.busy() {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			g := m.selected(m.filtered())
			if g == nil {
				break
			}
			m.syncing = true
			m.message = "正在备份存档…"
			return m, m.syncSavesCmd(g)
		case "B":
			if m.busy() {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			m.syncing = true
			m.message = "正在备份全部存档…"
			return m, m.syncSavesCmd(nil)
		case "R":
			if m.busy() {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			g := m.selected(m.filtered())
			if g == nil {
				break
			}
			if g.Save == nil || len(g.Save.Locations) == 0 {
				m.message = "该游戏没有存档记录，先按 b 备份"
				break
			}
			m.confirm = tuiConfirmSaveRestore
		case "x":
			if m.busy() {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			g := m.selected(m.filtered())
			if g == nil {
				break
			}
			if g.Save == nil || len(g.Save.Locations) == 0 {
				m.message = "该游戏没有存档记录"
				break
			}
			m.confirm = tuiConfirmSaveRemove
		case "r":
			if m.busy() {
				m.message = "正在处理其他任务，请稍候"
				break
			}
			return m, m.reload()
		}
		return m, nil
	}
	return m, nil
}

// handleMouse 处理鼠标滚轮和左键点击：点击标签切换视图，点击列表行选中游戏。
func (m tuiModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.confirm != tuiConfirmNone {
		return m, nil
	}
	if m.showHelp {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.showHelp = false
		}
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.move(-3)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.move(3)
		return m, nil
	}
	if msg.Action == tea.MouseActionRelease {
		m.trackDrag = false
	}
	if msg.Action != tea.MouseActionPress && msg.Action != tea.MouseActionMotion {
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if msg.Y == 1 {
		if msg.Action == tea.MouseActionPress {
			if v, ok := m.tabAt(msg.X, m.width); ok {
				return m, m.setView(v)
			}
		}
		return m, nil
	}
	w := m.width
	if w < 20 {
		w = 20
	}
	// 音乐视图的右侧详情面板显示音轨列表：点击或按住左键滑行即可选歌。
	if m.view == tuiViewMusic && w >= 78 && msg.Y >= 3 && msg.X > m.listWidth(w) {
		return m.handleMusicTrackMouse(msg)
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Y < 3 || msg.X < 0 || msg.X >= m.listWidth(w) {
		return m, nil
	}
	games := m.filtered()
	entries := m.listEntries(games)
	rows := m.pageSize()
	offset := listWindow(cursorEntry(entries, m.cursor), len(entries), rows)
	row := msg.Y - 3
	index := offset + row
	if row < 0 || row >= rows || index < 0 || index >= len(entries) || entries[index].game < 0 {
		return m, nil
	}
	m.cursor = entries[index].game
	m.message = ""
	return m, nil
}

// handleMusicTrackMouse 处理音乐视图右侧音轨列表的点击与按住滑行选歌。
func (m tuiModel) handleMusicTrackMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if m.selectTrackAt(msg.Y) {
			m.trackDrag = true
		}
	case tea.MouseActionMotion:
		if m.trackDrag && (msg.Button == tea.MouseButtonLeft || msg.Button == tea.MouseButtonNone) {
			m.selectTrackAt(msg.Y)
		}
	}
	return m, nil
}

// selectTrackAt 根据鼠标纵坐标选中右侧音轨列表中的一首歌，返回是否命中音轨行。
func (m *tuiModel) selectTrackAt(y int) bool {
	g := m.selected(m.filtered())
	if g == nil || !g.isMusic() {
		return false
	}
	tracks, perr := m.tracksFor(*g)
	if perr != "" || len(tracks) == 0 {
		return false
	}
	firstRow := musicTrackStartRow(*g)
	start, end := musicTrackWindow(tracks, m.trackCursor(*g, tracks), m.pageSize(), firstRow)
	row := y - 3 - firstRow
	index := start + row
	if row < 0 || index < start || index >= end {
		return false
	}
	if m.trackSel == nil {
		m.trackSel = make(map[int]int)
	}
	if m.trackSel[g.AppID] == index {
		return true
	}
	m.trackSel[g.AppID] = index
	m.message = fmt.Sprintf("已选择音轨 %d/%d：%s", index+1, len(tracks), trackName(g.Dir, tracks[index]))
	return true
}

func (m tuiModel) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.searchMode = false
		m.query = ""
		m.cursor = 0
		m.message = "已清除筛选"
		return m, nil
	case "enter":
		m.searchMode = false
		return m, nil
	case "backspace", "ctrl+h":
		runes := []rune(m.query)
		if len(runes) > 0 {
			m.query = string(runes[:len(runes)-1])
		}
		m.cursor = 0
	case "ctrl+u":
		m.query = ""
		m.cursor = 0
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+b":
		m.move(-m.pageSize())
	case "pgdown", "ctrl+f":
		m.move(m.pageSize())
	case " ":
		m.query += " "
		m.cursor = 0
	default:
		if msg.Type == tea.KeyRunes {
			m.query += string(msg.Runes)
			m.cursor = 0
		}
	}
	return m, nil
}

// setView 切换视图；首次进入收藏视图时在后台获取缺少的发行商，
// 返回的命令会在获取完成后重新排序收藏列表。
func (m *tuiModel) setView(v tuiView) tea.Cmd {
	if v < 0 || v >= tuiViewCount {
		return nil
	}
	m.view = v
	m.cursor = 0
	m.message = ""
	m.trackDrag = false
	if v == tuiViewFavorites && !m.publishersLoaded {
		m.publishersLoaded = true
		return m.fetchFavoritesPublishersCmd()
	}
	return nil
}

func (m *tuiModel) move(delta int) {
	if !m.installing && !m.checking {
		m.message = ""
	}
	max := m.maxCursor()
	if max < 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > max {
		m.cursor = max
	}
}

func (m tuiModel) pageSize() int {
	n := m.height - 6
	if n < 1 {
		n = 1
	}
	return n
}

func (m tuiModel) maxCursor() int {
	return len(m.filtered()) - 1
}

func (m *tuiModel) clampCursor() {
	max := m.maxCursor()
	if max < 0 {
		m.cursor = 0
		return
	}
	if m.cursor > max {
		m.cursor = max
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// hidden 报告当前是否应当隐藏该游戏。Steam 运行库 / 工具默认隐藏，
// 按 h 可以临时显示。
func (m tuiModel) hidden(g tuiGame) bool {
	return !m.showHidden && steam.IsDefaultHiddenApp(g.AppID)
}

// filtered 返回当前视图和搜索条件筛选后的游戏。
func (m tuiModel) filtered() []tuiGame {
	out := make([]tuiGame, 0, len(m.games))
	for _, g := range m.games {
		// 只因为出现在收藏夹里才加载的游戏，取消收藏后不再出现在列表中。
		if g.Status == "unknown" && g.Favorite == nil {
			continue
		}
		if m.hidden(g) {
			continue
		}
		// 音乐单独一个页面：游戏视图不混入原声带，音乐视图只显示原声带。
		if m.view == tuiViewMusic {
			if !g.isMusic() {
				continue
			}
		} else if g.isMusic() {
			continue
		}
		switch m.view {
		case tuiViewInstalled:
			if !g.installed() {
				continue
			}
		case tuiViewMissing:
			if g.Status != "missing" {
				continue
			}
		case tuiViewFavorites:
			if g.Favorite == nil {
				continue
			}
		case tuiViewPatches:
			if len(g.Patches) == 0 {
				continue
			}
		case tuiViewSaves:
			if g.Save == nil || len(g.Save.Locations) == 0 {
				continue
			}
		}
		if !tuiMatch(g, m.query) {
			continue
		}
		out = append(out, g)
	}
	// 收藏视图与 fav list 保持一致：优先按备注分组，没有备注时按发行商分组，
	// 两者都没有的排在最后；同一分组内按游戏名称排序。
	if m.view == tuiViewFavorites {
		sort.SliceStable(out, func(i, j int) bool {
			return favoriteSortLess(tuiFavoriteGroup(out[i]), out[i].Name, out[i].AppID,
				tuiFavoriteGroup(out[j]), out[j].Name, out[j].AppID)
		})
	}
	return out
}

// tuiFavoriteGroup 返回收藏视图的排序分组：优先备注，没有备注时使用发行商。
func tuiFavoriteGroup(g tuiGame) string {
	if g.Favorite == nil {
		return ""
	}
	if note := strings.TrimSpace(g.Favorite.Note); note != "" {
		return note
	}
	return strings.TrimSpace(g.Publisher)
}

func tuiMatch(g tuiGame, query string) bool {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(tokens) == 0 {
		return true
	}
	parts := []string{g.Name, strconv.Itoa(g.AppID), g.Library, g.Dir, g.Publisher}
	if g.Favorite != nil {
		parts = append(parts, g.Favorite.Status, g.Favorite.Note)
	}
	for _, p := range g.Patches {
		parts = append(parts, p.Name, p.Hash)
	}
	if g.Save != nil {
		parts = append(parts, g.Save.Name, g.Save.GameDir)
		for _, loc := range g.Save.Locations {
			parts = append(parts, loc.Kind, saveKindLabel(loc.Kind), loc.Path, loc.Store)
		}
	}
	haystack := strings.ToLower(strings.Join(parts, " "))
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func (m tuiModel) selected(games []tuiGame) *tuiGame {
	if len(games) == 0 {
		return nil
	}
	i := m.cursor
	if i < 0 {
		i = 0
	}
	if i >= len(games) {
		i = len(games) - 1
	}
	return &games[i]
}

// tracksFor 返回原声带的音轨列表，并按目录缓存扫描结果。
func (m tuiModel) tracksFor(g tuiGame) ([]string, string) {
	dir := strings.TrimSpace(g.Dir)
	if dir == "" {
		return nil, "未找到原声带目录，请先运行 ugal scan"
	}
	if tracks, ok := m.tracks[dir]; ok {
		return tracks, m.trackErrs[dir]
	}
	tracks, err := player.AudioFiles(dir)
	if err != nil {
		if m.trackErrs != nil {
			m.trackErrs[dir] = err.Error()
		}
		if m.tracks != nil {
			m.tracks[dir] = nil
		}
		return nil, err.Error()
	}
	if m.tracks != nil {
		m.tracks[dir] = tracks
	}
	return tracks, ""
}

// trackName 优先显示相对原声带目录的路径（如 Disc 2/01.flac）。
func trackName(dir, file string) string {
	if rel, err := filepath.Rel(dir, file); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(file)
}

// trackCursor 返回用户在该原声带中选中的音轨下标。
func (m tuiModel) trackCursor(g tuiGame, tracks []string) int {
	if len(tracks) == 0 {
		return 0
	}
	index := m.trackSel[g.AppID]
	if index < 0 || index >= len(tracks) {
		return 0
	}
	return index
}

// moveTrack 在当前原声带的音轨列表中上下移动选择。
func (m *tuiModel) moveTrack(delta int) {
	if m.view != tuiViewMusic {
		return
	}
	g := m.selected(m.filtered())
	if g == nil || !g.isMusic() {
		return
	}
	tracks, perr := m.tracksFor(*g)
	if perr != "" {
		m.message = perr
		return
	}
	if len(tracks) == 0 {
		m.message = "未找到音频文件"
		return
	}
	index := m.trackCursor(*g, tracks) + delta
	if index < 0 {
		index = 0
	}
	if index >= len(tracks) {
		index = len(tracks) - 1
	}
	if m.trackSel == nil {
		m.trackSel = make(map[int]int)
	}
	m.trackSel[g.AppID] = index
	m.message = fmt.Sprintf("已选择音轨 %d/%d：%s", index+1, len(tracks), trackName(g.Dir, tracks[index]))
}

// switchTrack 让当前播放器切到上/下一首；没有在播放时只移动音轨选择，
// 这样不用先播放原声带也能浏览并“切歌”，按 Enter 再开始播放。
func (m *tuiModel) switchTrack(delta int) {
	if m.view != tuiViewMusic {
		return
	}
	g := m.selected(m.filtered())
	if g == nil || !g.isMusic() {
		return
	}
	playing := false
	if m.play != nil && m.playApp == g.AppID {
		_, _, playing = m.play.State()
	}
	if !playing {
		m.moveTrack(delta)
		return
	}
	var (
		index int
		err   error
	)
	if delta < 0 {
		index, err = m.play.Prev()
	} else {
		index, err = m.play.Next()
	}
	if err != nil {
		m.message = fmt.Sprintf("切换音轨失败: %v", err)
		return
	}
	if m.trackSel == nil {
		m.trackSel = make(map[int]int)
	}
	m.trackSel[g.AppID] = index
	_, paused, _ := m.play.State()
	m.message = "正在播放：" + m.trackLabel(g.AppID, index, paused)
}

// togglePause 暂停或继续当前播放器。
func (m *tuiModel) togglePause() {
	if m.play == nil {
		m.message = "没有正在播放的音乐"
		return
	}
	paused, err := m.play.TogglePause()
	if err != nil {
		m.message = fmt.Sprintf("暂停 / 继续失败: %v", err)
		return
	}
	if paused {
		m.message = "已暂停（Space 继续）"
	} else {
		m.message = "已继续播放"
	}
}

// trackLabel 生成“播放中 · APPID 42 · 歌名（3/12）”形式的提示。
func (m tuiModel) trackLabel(appID, index int, paused bool) string {
	state := "播放中"
	if paused {
		state = "已暂停"
	}
	for _, g := range m.games {
		if g.AppID != appID || !g.isMusic() {
			continue
		}
		tracks, _ := m.tracksFor(g)
		if index >= 0 && index < len(tracks) {
			return fmt.Sprintf("%s · APPID %d · %s（%d/%d）", state, appID, trackName(g.Dir, tracks[index]), index+1, len(tracks))
		}
		break
	}
	return fmt.Sprintf("%s · APPID %d", state, appID)
}

// nowPlayingLabel 生成底部状态栏的播放信息，始终带上正在播放的 APPID。
func nowPlayingLabel(appID, index, total int, paused, playing bool) string {
	state := "播放中"
	switch {
	case paused:
		state = "已暂停"
	case !playing:
		state = "已停止"
	}
	if total > 0 && index >= 0 && index < total {
		return fmt.Sprintf("%s · APPID %d · %d/%d", state, appID, index+1, total)
	}
	return fmt.Sprintf("%s · APPID %d", state, appID)
}

// stopPlayback 结束当前播放并让定时刷新失效。
func (m *tuiModel) stopPlayback() {
	if m.play != nil {
		m.play.Stop()
		m.play = nil
	}
	m.playSeq++
}

// quit 退出 TUI 前先停止由 ugal 启动的播放器。
func (m tuiModel) quit() (tea.Model, tea.Cmd) {
	m.stopPlayback()
	return m, tea.Quit
}

func (m *tuiModel) toggleFavorite() {
	games := m.filtered()
	g := m.selected(games)
	if g == nil {
		return
	}
	if m.favData == nil {
		m.favData = &model.FavoritesData{}
	}
	if favoriteIndex(m.favData, g.AppID) >= 0 {
		favorites.Remove(m.favData, []int{g.AppID})
		m.message = fmt.Sprintf("已取消收藏 %s", g.Name)
	} else {
		favorites.Add(m.favData, []model.Favorite{{
			AppID:  g.AppID,
			Name:   g.Name,
			Status: favorites.StatusWant,
		}})
		m.publishersLoaded = false
		m.message = fmt.Sprintf("已收藏 %s（%s）", g.Name, favorites.StatusWant)
	}
	if err := m.st.SaveFavorites(m.favData); err != nil {
		m.message = fmt.Sprintf("保存收藏失败: %v", err)
		return
	}
	m.applyExtras()
	m.clampCursor()
}

func (m *tuiModel) cycleFavoriteStatus() {
	games := m.filtered()
	g := m.selected(games)
	if g == nil {
		return
	}
	if m.favData == nil {
		m.favData = &model.FavoritesData{}
	}
	idx := favoriteIndex(m.favData, g.AppID)
	if idx < 0 {
		favorites.Add(m.favData, []model.Favorite{{
			AppID:  g.AppID,
			Name:   g.Name,
			Status: favorites.StatusWant,
		}})
		m.publishersLoaded = false
		m.message = fmt.Sprintf("已收藏 %s（%s）", g.Name, favorites.StatusWant)
	} else {
		sequence := append(favorites.StatusValues(), "")
		current := favorites.NormalizeStatus(m.favData.Games[idx].Status)
		next := sequence[0]
		for i, s := range sequence {
			if s == current {
				next = sequence[(i+1)%len(sequence)]
				break
			}
		}
		m.favData.Games[idx].Status = next
		favorites.Normalize(m.favData)
		if next == "" {
			m.message = fmt.Sprintf("已清除 %s 的收藏状态", g.Name)
		} else {
			m.message = fmt.Sprintf("%s 的收藏状态: %s", g.Name, next)
		}
	}
	if err := m.st.SaveFavorites(m.favData); err != nil {
		m.message = fmt.Sprintf("保存收藏失败: %v", err)
		return
	}
	m.applyExtras()
	m.clampCursor()
}

func (m *tuiModel) togglePatches() {
	if m.busy() {
		m.message = "正在处理其他任务，请稍候"
		return
	}
	games := m.filtered()
	g := m.selected(games)
	if g == nil {
		return
	}
	if m.patchData == nil {
		m.patchData = &model.PatchesData{}
	}
	list := patches.FindAll(m.patchData, g.AppID)
	if len(list) == 0 {
		m.message = fmt.Sprintf("%s 没有补丁记录", g.Name)
		return
	}
	applied := true
	for _, p := range list {
		if !p.Applied {
			applied = false
			break
		}
	}
	for _, p := range list {
		p.Applied = !applied
		patches.Set(m.patchData, p)
	}
	if err := m.st.SavePatches(m.patchData); err != nil {
		m.message = fmt.Sprintf("保存补丁记录失败: %v", err)
		return
	}
	if applied {
		m.message = fmt.Sprintf("已把 %s 的补丁标记为未打", g.Name)
	} else {
		m.message = fmt.Sprintf("已把 %s 的补丁标记为已打", g.Name)
	}
	m.applyExtras()
}

func (m *tuiModel) applyExtras() {
	if m.favData != nil {
		favorites.Normalize(m.favData)
	}
	if m.patchData != nil {
		patches.Normalize(m.patchData)
	}
	applyTUIExtras(m.games, m.favData, m.patchData, m.saveData)
}

func (m tuiModel) patchGlyph(g tuiGame) (string, lipgloss.Style) {
	checked := false
	for _, p := range g.Patches {
		r, ok := m.checks[tuiPatchCheckKey(p.AppID, p.Name)]
		if !ok {
			continue
		}
		checked = true
		switch r.Status {
		case patches.CheckMismatch:
			return "✗", tuiRedStyle
		case patches.CheckMissing:
			return "?", tuiYellowStyle
		case patches.CheckUnverified:
			return "!", tuiYellowStyle
		}
	}
	if checked {
		return "✓", tuiGreenStyle
	}
	applied := 0
	for _, p := range g.Patches {
		if p.Applied {
			applied++
		}
	}
	switch {
	case len(g.Patches) > 0 && applied == len(g.Patches):
		return "✓", tuiGreenStyle
	case applied > 0:
		return "◐", tuiAccentStyle
	default:
		return "!", tuiYellowStyle
	}
}

func (m tuiModel) saveGlyph(g tuiGame) (string, lipgloss.Style) {
	if g.Save == nil || len(g.Save.Locations) == 0 {
		return "·", tuiMutedStyle
	}
	cloud, local := false, false
	for _, loc := range g.Save.Locations {
		switch loc.Kind {
		case saves.KindCloud:
			cloud = true
		case saves.KindLocal:
			local = true
		}
	}
	switch {
	case cloud && local:
		return "B", tuiGreenStyle
	case cloud:
		return "C", tuiAccentStyle
	default:
		return "L", tuiYellowStyle
	}
}

func (m *tuiModel) handlePatchInstall(msg tuiPatchInstallMsg) {
	files, replaced, backed := 0, 0, 0
	for _, r := range msg.results {
		files += r.files
		replaced += r.replaced
		backed += r.backedUp
	}
	if len(msg.results) > 0 {
		if m.patchData == nil {
			m.patchData = &model.PatchesData{}
		}
		for _, r := range msg.results {
			delete(m.checks, tuiPatchCheckKey(msg.appID, r.name))
			record := model.Patch{AppID: msg.appID, Name: r.name, Applied: true}
			if old := patches.FindByName(m.patchData, msg.appID, r.name); old != nil {
				record = *old
				record.Applied = true
			}
			if record.Hash == "" && r.hash != "" {
				record.Hash = r.hash
			}
			patches.Set(m.patchData, record)
		}
		if err := m.st.SavePatches(m.patchData); err != nil {
			m.message = fmt.Sprintf("补丁已安装，但保存记录失败: %v", err)
			return
		}
		m.applyExtras()
	}
	if len(msg.results) == 0 {
		if len(msg.errs) > 0 {
			m.message = "补丁安装失败：" + strings.Join(msg.errs, "；")
		} else {
			m.message = "没有可安装的补丁"
		}
		return
	}
	m.message = fmt.Sprintf("已安装 %d 个补丁：写入 %d 个文件，替换 %d 个，备份 %d 个", len(msg.results), files, replaced, backed)
	if len(msg.errs) > 0 {
		m.message += fmt.Sprintf("；失败 %d 个", len(msg.errs))
	}
}

func tuiPatchCheckKey(appID int, name string) string {
	return strconv.Itoa(appID) + "\x00" + strings.TrimSpace(name)
}

// busy 表示当前有后台任务在运行，避免同时执行会互相覆盖的操作。
func (m tuiModel) busy() bool {
	return m.installing || m.checking || m.syncing
}

func (m *tuiModel) handleSaveSync(msg tuiSaveSyncMsg) {
	if msg.err != nil {
		m.message = fmt.Sprintf("存档备份失败: %v", msg.err)
		return
	}
	if m.cfg != nil && msg.steamUser != "" {
		m.cfg.SteamUser = msg.steamUser
	}
	m.reloadSaveData()
	if msg.result == nil {
		m.message = "存档备份完成"
		return
	}
	games, locations, files := 0, 0, 0
	var bytes int64
	warnings := len(msg.result.Warnings)
	for _, gr := range msg.result.Saves {
		warnings += len(gr.Warnings)
		if len(gr.Locations) == 0 {
			continue
		}
		games++
		for _, loc := range gr.Locations {
			locations++
			files += loc.Files
			bytes += loc.Bytes
		}
	}
	scope := "当前游戏"
	if msg.all {
		scope = "全部游戏"
	}
	if locations == 0 {
		m.message = scope + "没有找到可备份的存档"
		if warnings > 0 {
			m.message += fmt.Sprintf("（%d 条提示）", warnings)
		}
		return
	}
	m.message = fmt.Sprintf("%s存档已备份：%d 款 · %d 个位置 · %d 个文件 · %s",
		scope, games, locations, files, humanSize(bytes))
	if warnings > 0 {
		m.message += fmt.Sprintf(" · %d 条提示", warnings)
	}
}

func (m *tuiModel) handleSaveRestore(msg tuiSaveRestoreMsg) {
	if msg.err != nil {
		m.message = fmt.Sprintf("存档还原失败: %v", msg.err)
		return
	}
	if msg.result == nil {
		m.message = "存档还原完成"
		return
	}
	locations, files := 0, 0
	warnings := len(msg.result.Warnings)
	for _, gr := range msg.result.Saves {
		warnings += len(gr.Warnings)
		for _, loc := range gr.Locations {
			locations++
			files += loc.Copied + loc.Updated
		}
	}
	if locations == 0 {
		m.message = "没有可还原的存档"
		if warnings > 0 {
			m.message += fmt.Sprintf("（%d 条提示）", warnings)
		}
		return
	}
	m.message = fmt.Sprintf("已还原 %d 个存档位置，写入 %d 个文件", locations, files)
	if warnings > 0 {
		m.message += fmt.Sprintf(" · %d 条提示", warnings)
	}
}

func (m *tuiModel) reload() tea.Cmd {
	games, fav, pd, saves, err := loadTUIGames(m.st)
	if err != nil {
		m.message = fmt.Sprintf("读取数据失败: %v", err)
		return nil
	}
	m.games, m.favData, m.patchData, m.saveData = games, fav, pd, saves
	if cfg, err := m.st.LoadConfig(); err == nil {
		m.cfg = cfg
	}
	if installed, err := m.st.LoadInstalled(); err == nil {
		m.installed = installed
	} else if errors.Is(err, os.ErrNotExist) {
		m.installed = nil
	}
	m.checks = nil
	m.publishersLoaded = false
	m.tracks = make(map[string][]string)
	m.trackErrs = make(map[string]string)
	m.clampCursor()
	m.message = fmt.Sprintf("已重新读取 %d 款游戏、%d 款音乐", m.viewCount(tuiViewAll), m.viewCount(tuiViewMusic))
	if m.view == tuiViewFavorites {
		m.publishersLoaded = true
		return m.fetchFavoritesPublishersCmd()
	}
	return nil
}

func (m tuiModel) View() string {
	if !m.ready {
		return "ugal 正在加载…"
	}
	w, h := m.width, m.height
	if w < 20 {
		w = 20
	}
	if h < 6 {
		h = 6
	}
	games := m.filtered()
	bodyH := h - 4
	if bodyH < 1 {
		bodyH = 1
	}
	header := m.headerView(w)
	tabs := m.tabsView(w)
	body := m.bodyView(w, bodyH, games)
	status := m.statusView(w, games)
	hints := m.hintsView(w)
	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, status, hints)
}

func (m tuiModel) headerView(w int) string {
	gameCount, installed, music := 0, 0, 0
	for _, g := range m.games {
		if g.isMusic() {
			music++
			continue
		}
		if m.hidden(g) {
			continue
		}
		gameCount++
		if g.installed() {
			installed++
		}
	}
	favCount := 0
	for _, g := range m.games {
		if g.Favorite != nil && !m.hidden(g) {
			favCount++
		}
	}
	patchCount, saveCount := 0, 0
	for _, g := range m.games {
		if g.isMusic() || m.hidden(g) {
			continue
		}
		if len(g.Patches) > 0 {
			patchCount++
		}
		if g.Save != nil && len(g.Save.Locations) > 0 {
			saveCount++
		}
	}
	left := tuiTitleStyle.Render("ugal") + " " + tuiMutedStyle.Render("Steam 游戏库")
	right := fmt.Sprintf("%d 款 · %d 音乐 · %d 已安装 · %d 收藏 · %d 补丁 · %d 存档",
		gameCount, music, installed, favCount, patchCount, saveCount)
	line := left
	if pad := w - displayWidth(left) - displayWidth(right); pad > 0 {
		line += strings.Repeat(" ", pad) + tuiMutedStyle.Render(right)
	}
	return fitLine(line, w)
}

func (m tuiModel) tabsView(w int) string {
	line, _ := m.tabsLayout(w)
	return line
}

// tabsLayout 返回渲染后的标签栏，以及每个标签在行内的起止列（左闭右开），
// 供鼠标点击时判断命中的视图。
func (m tuiModel) tabsLayout(w int) (string, [][2]int) {
	build := func(withCount bool) ([]string, [][2]int) {
		parts := make([]string, 0, len(tuiViewNames))
		spans := make([][2]int, 0, len(tuiViewNames))
		x := 0
		for i, name := range tuiViewNames {
			label := fmt.Sprintf("%d %s", i+1, name)
			if withCount {
				label += fmt.Sprintf(" %d", m.viewCount(tuiView(i)))
			}
			var rendered string
			if tuiView(i) == m.view {
				rendered = tuiTabActive.Render(label)
			} else {
				rendered = tuiTabInactive.Render(label)
			}
			parts = append(parts, rendered)
			width := displayWidth(rendered)
			spans = append(spans, [2]int{x, x + width})
			x += width + 1 // 标签之间有一个空格
		}
		return parts, spans
	}
	parts, spans := build(true)
	if displayWidth(strings.Join(parts, " ")) > w {
		parts, spans = build(false)
	}
	line := strings.Join(parts, " ")
	if m.query != "" {
		line += "  " + tuiMutedStyle.Render("筛选: "+m.query)
	}
	return fitLine(line, w), spans
}

// tabAt 返回横坐标 x 处的标签视图。
func (m tuiModel) tabAt(x, w int) (tuiView, bool) {
	_, spans := m.tabsLayout(w)
	for i, span := range spans {
		if x >= span[0] && x < span[1] {
			return tuiView(i), true
		}
	}
	return 0, false
}

func (m tuiModel) viewCount(v tuiView) int {
	if v == tuiViewMusic {
		n := 0
		for _, g := range m.games {
			if g.isMusic() {
				n++
			}
		}
		return n
	}
	n := 0
	for _, g := range m.games {
		if g.isMusic() || m.hidden(g) {
			continue
		}
		switch v {
		case tuiViewInstalled:
			if g.installed() {
				n++
			}
		case tuiViewMissing:
			if g.Status == "missing" {
				n++
			}
		case tuiViewFavorites:
			if g.Favorite != nil {
				n++
			}
		case tuiViewPatches:
			if len(g.Patches) > 0 {
				n++
			}
		case tuiViewSaves:
			if g.Save != nil && len(g.Save.Locations) > 0 {
				n++
			}
		default:
			n++
		}
	}
	return n
}

func (m tuiModel) bodyView(w, h int, games []tuiGame) string {
	if m.confirm != tuiConfirmNone {
		return m.confirmView(w, h)
	}
	if m.showHelp {
		return m.helpView(w, h)
	}
	if w < 78 {
		return m.listPane(w, h, games)
	}
	listW := m.listWidth(w)
	detailW := w - listW - 1
	list := m.listPane(listW, h, games)
	detail := m.detailPane(detailW, h, games)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", detail)
}

// listWidth 返回左侧列表的宽度，与 bodyView 的布局保持一致。
func (m tuiModel) listWidth(w int) int {
	if w < 78 {
		return w
	}
	listW := w * 45 / 100
	if listW < 34 {
		listW = 34
	}
	if listW > 64 {
		listW = 64
	}
	detailW := w - listW - 1
	if detailW < 28 {
		detailW = 28
		listW = w - detailW - 1
	}
	return listW
}

// tuiListEntry 是列表中的一行：label 非空时是分组标题，否则显示 game 指向的游戏。
type tuiListEntry struct {
	label string
	game  int
}

// listEntries 返回列表的显示行。收藏视图按备注/发行商插入分组标题，
// 其他视图一行一款游戏。
func (m tuiModel) listEntries(games []tuiGame) []tuiListEntry {
	if m.view != tuiViewFavorites {
		entries := make([]tuiListEntry, len(games))
		for i := range games {
			entries[i] = tuiListEntry{game: i}
		}
		return entries
	}
	counts := make(map[string]int, len(games))
	for _, g := range games {
		counts[tuiFavoriteGroup(g)]++
	}
	entries := make([]tuiListEntry, 0, len(games)+len(counts))
	last := ""
	first := true
	for i, g := range games {
		group := tuiFavoriteGroup(g)
		if first || group != last {
			label := group
			if label == "" {
				label = "无分组"
			}
			entries = append(entries, tuiListEntry{
				label: fmt.Sprintf("%s（%d 个）", truncateDisplay(label, 48), counts[group]),
				game:  -1,
			})
			last = group
			first = false
		}
		entries = append(entries, tuiListEntry{game: i})
	}
	return entries
}

// cursorEntry 返回光标所在游戏在列表显示行中的下标。
func cursorEntry(entries []tuiListEntry, cursor int) int {
	for i, e := range entries {
		if e.game == cursor {
			return i
		}
	}
	return 0
}

// listWindow 返回高度为 rows 的列表可见区域的第一行下标。
func listWindow(cursor, total, rows int) int {
	if rows < 1 {
		rows = 1
	}
	if total < 1 {
		return 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	offset := cursor - rows + 1
	if offset < 0 {
		return 0
	}
	return offset
}

// groupRow 渲染收藏分组标题。
func (m tuiModel) groupRow(label string, innerW int) string {
	return tuiRedStyle.Bold(true).Render(truncateDisplay("  "+label, innerW))
}

func (m tuiModel) listPane(w, h int, games []tuiGame) string {
	rows := h - 2
	if rows < 1 {
		rows = 1
	}
	innerW := w - 4
	if innerW < 4 {
		innerW = 4
	}
	lines := make([]string, 0, rows)

	if len(games) == 0 {
		if m.view == tuiViewMusic && m.query == "" {
			lines = append(lines, tuiMutedStyle.Render("  还没有音乐原声带"))
			lines = append(lines, "")
			lines = append(lines, tuiMutedStyle.Render("  运行 ugal scan 扫描 steamapps/music 下的原声带"))
		} else if m.view == tuiViewPatches && m.query == "" {
			lines = append(lines, tuiMutedStyle.Render("  还没有补丁记录"))
			lines = append(lines, "")
			lines = append(lines, tuiMutedStyle.Render("  用 ugal patch set 添加记录后再回来"))
		} else if m.view == tuiViewSaves && m.query == "" {
			lines = append(lines, tuiMutedStyle.Render("  还没有存档记录"))
			lines = append(lines, "")
			lines = append(lines, tuiMutedStyle.Render("  按 b 备份当前游戏，或按 B 备份全部已安装游戏"))
		} else {
			lines = append(lines, tuiMutedStyle.Render("  没有匹配的游戏"))
			if len(m.games) == 0 {
				lines = append(lines, "")
				lines = append(lines, tuiMutedStyle.Render("  先运行 ugal sync / ugal scan 获取数据"))
			} else if m.query != "" {
				lines = append(lines, "")
				lines = append(lines, tuiMutedStyle.Render("  按 Esc 清除筛选"))
			}
		}
	} else {
		entries := m.listEntries(games)
		offset := listWindow(cursorEntry(entries, m.cursor), len(entries), rows)
		end := offset + rows
		if end > len(entries) {
			end = len(entries)
		}
		for i := offset; i < end; i++ {
			entry := entries[i]
			if entry.game < 0 {
				lines = append(lines, m.groupRow(entry.label, innerW))
				continue
			}
			lines = append(lines, m.listRow(games[entry.game], innerW, entry.game == m.cursor))
		}
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return tuiPaneStyle.Width(w - 2).Height(h - 2).Render(strings.Join(lines, "\n"))
}

func (m tuiModel) listRow(g tuiGame, innerW int, selected bool) string {
	star := " "
	if g.Favorite != nil {
		star = "★"
	}
	icon, iconStyle := "○", tuiMutedStyle
	switch g.Status {
	case "installed":
		icon, iconStyle = "●", tuiGreenStyle
	case "extra":
		icon, iconStyle = "◆", tuiAccentStyle
	}
	if m.view == tuiViewPatches {
		icon, iconStyle = m.patchGlyph(g)
	}
	if m.view == tuiViewSaves {
		icon, iconStyle = m.saveGlyph(g)
	}
	if m.view == tuiViewMusic {
		icon, iconStyle = "♪", tuiPinkStyle
		if m.play != nil && m.playApp == g.AppID {
			if _, paused, playing := m.play.State(); playing {
				icon, iconStyle = "▶", tuiGreenStyle
				if paused {
					icon, iconStyle = "▮", tuiYellowStyle
				}
			}
		}
	}
	id := strconv.Itoa(g.AppID)
	idW := 7
	if innerW < 16 {
		idW = 0
	}
	// 已安装视图在 APPID 前显示占用空间。
	size, sizeW := "", 0
	if m.view == tuiViewInstalled && g.Size > 0 {
		size = humanSize(g.Size)
		sizeW = displayWidth(size)
		if innerW-4-idW-sizeW-6 < 4 {
			size, sizeW = "", 0
		}
	}
	prefixW := 4
	nameW := innerW - prefixW - idW - 1
	if sizeW > 0 {
		nameW -= sizeW + 1
	}
	if nameW < 4 {
		nameW = 4
	}
	name := truncateDisplay(g.Name, nameW)

	if selected {
		line := star + " " + icon + " " + name
		if sizeW > 0 {
			line = padRight(line, innerW-idW-sizeW-1) + size + " "
		}
		if idW > 0 {
			line = padRight(line, innerW-idW) + id
		}
		return tuiSelectedStyle.Render(fitLine(line, innerW))
	}

	var b strings.Builder
	if star == "★" {
		b.WriteString(tuiPinkStyle.Render(star))
	} else {
		b.WriteString(" ")
	}
	b.WriteString(" ")
	b.WriteString(iconStyle.Render(icon))
	b.WriteString(" ")
	b.WriteString(name)
	if sizeW > 0 {
		pad := innerW - idW - sizeW - 1 - displayWidth(b.String())
		if pad < 1 {
			pad = 1
		}
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(tuiMutedStyle.Render(size))
		b.WriteString(" ")
	}
	if idW > 0 {
		pad := innerW - idW - displayWidth(b.String())
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(tuiMutedStyle.Render(id))
	}
	return b.String()
}

func (m tuiModel) detailPane(w, h int, games []tuiGame) string {
	rows := h - 2
	if rows < 1 {
		rows = 1
	}
	innerW := w - 4
	if innerW < 4 {
		innerW = 4
	}
	g := m.selected(games)
	lines := m.detailLines(g, innerW, rows)
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return tuiPaneStyle.Width(w - 2).Height(h - 2).Render(strings.Join(lines, "\n"))
}

func (m tuiModel) detailLines(g *tuiGame, innerW int, rows int) []string {
	if g == nil {
		return []string{"", tuiMutedStyle.Render("  未选择游戏")}
	}
	if g.isMusic() {
		return m.musicDetailLines(g, innerW, rows)
	}
	lines := make([]string, 0, 24)
	lines = append(lines, tuiTitleStyle.Render(truncateDisplay(g.Name, innerW)))
	lines = append(lines, m.detailTags(g, innerW))
	lines = append(lines, "")
	lines = append(lines, m.detailField("APPID", strconv.Itoa(g.AppID), innerW))
	lines = append(lines, m.detailField("状态", statusLabel(g.Status), innerW))
	if g.Type != "" {
		lines = append(lines, m.detailField("类型", typeLabel(g.Type), innerW))
	}
	lines = append(lines, m.detailField("大小", humanSize(g.Size), innerW))
	if g.Library != "" {
		lines = append(lines, m.detailField("库目录", g.Library, innerW))
	}
	if g.Dir != "" {
		lines = append(lines, m.detailField("安装目录", g.Dir, innerW))
	}
	if g.Favorite != nil {
		lines = append(lines, m.detailField("收藏", tuiFavoriteLabel(g.Favorite), innerW))
		if strings.TrimSpace(g.Favorite.Note) != "" {
			lines = append(lines, m.detailField("备注", g.Favorite.Note, innerW))
		}
		if strings.TrimSpace(g.Publisher) != "" {
			lines = append(lines, m.detailField("发行商", g.Publisher, innerW))
		}
	} else {
		lines = append(lines, m.detailField("收藏", "未收藏", innerW))
	}
	if g.Save != nil && len(g.Save.Locations) > 0 {
		lines = append(lines, m.detailField("存档", tuiSaveSummary(g.Save), innerW))
	}
	if g.Cover != "" {
		lines = append(lines, m.detailField("封面", g.Cover, innerW))
	}

	lines = append(lines, "")
	if len(g.Patches) == 0 {
		lines = append(lines, tuiMutedStyle.Render(padRight("补丁", 10))+"无补丁")
	} else {
		lines = append(lines, tuiAccentStyle.Bold(true).Render(fmt.Sprintf("补丁（%d）", len(g.Patches))))
	}
	for _, p := range g.Patches {
		name := p.Name
		if strings.TrimSpace(name) == "" {
			name = "未命名"
		}
		state := "未打"
		style := tuiYellowStyle
		if p.Applied {
			state = "已打"
			style = tuiGreenStyle
		}
		hash := shortHash(p.Hash)
		value := fmt.Sprintf("%s  %s", name, state)
		if hash != "" {
			value += "  " + hash
		}
		if r, ok := m.checks[tuiPatchCheckKey(p.AppID, p.Name)]; ok {
			value += "  " + r.Status.Label()
			switch r.Status {
			case patches.CheckOK:
				style = tuiGreenStyle
			case patches.CheckMismatch:
				style = tuiRedStyle
			case patches.CheckMissing, patches.CheckUnverified:
				style = tuiYellowStyle
			}
		}
		lines = append(lines, "  "+tuiMutedStyle.Render("• ")+style.Render(truncateDisplay(value, innerW-4)))
	}

	lines = append(lines, "")
	if g.Save == nil || len(g.Save.Locations) == 0 {
		lines = append(lines, tuiMutedStyle.Render(padRight("存档", 10))+"未备份")
		return lines
	}
	lines = append(lines, tuiAccentStyle.Bold(true).Render(fmt.Sprintf("存档（%d）", len(g.Save.Locations))))
	for _, loc := range g.Save.Locations {
		info := fmt.Sprintf("%s  %d 文件  %s", saveKindLabel(loc.Kind), loc.Files, humanSize(loc.Bytes))
		if !loc.SyncedAt.IsZero() {
			info += "  " + loc.SyncedAt.Local().Format("2006-01-02 15:04")
		}
		style := tuiAccentStyle
		if loc.Kind == saves.KindLocal {
			style = tuiYellowStyle
		}
		lines = append(lines, "  "+tuiMutedStyle.Render("• ")+style.Render(truncateDisplay(info, innerW-4)))
		if strings.TrimSpace(loc.Path) != "" {
			lines = append(lines, "    "+tuiMutedStyle.Render(truncateDisplay(loc.Path, innerW-6)))
		}
	}
	return lines
}

func (m tuiModel) musicDetailLines(g *tuiGame, innerW int, rows int) []string {
	tracks, perr := m.tracksFor(*g)
	lines := make([]string, 0, rows)
	lines = append(lines, tuiTitleStyle.Render(truncateDisplay(g.Name, innerW)))
	lines = append(lines, m.detailTags(g, innerW))
	lines = append(lines, "")
	lines = append(lines, m.detailField("APPID", strconv.Itoa(g.AppID), innerW))
	lines = append(lines, m.detailField("状态", statusLabel(g.Status), innerW))
	lines = append(lines, m.detailField("大小", humanSize(g.Size), innerW))
	if g.Dir != "" {
		lines = append(lines, m.detailField("目录", g.Dir, innerW))
	}

	playLabel := "未播放"
	playIndex := -1
	playPaused := false
	if m.play != nil && m.playApp == g.AppID {
		index, paused, playing := m.play.State()
		playIndex, playPaused = index, paused
		switch {
		case paused:
			playLabel = "已暂停"
		case playing:
			playLabel = "播放中"
		default:
			playLabel = "已停止"
		}
		if index >= 0 && index < len(tracks) {
			playLabel += fmt.Sprintf(" · %d/%d", index+1, len(tracks))
		}
	}
	lines = append(lines, m.detailField("播放", playLabel, innerW))
	lines = append(lines, "")

	if perr != "" {
		lines = append(lines, tuiYellowStyle.Render(truncateDisplay(perr, innerW)))
		return lines
	}
	if len(tracks) == 0 {
		lines = append(lines, tuiMutedStyle.Render("没有找到音频文件"))
		return lines
	}

	lines = append(lines, tuiAccentStyle.Bold(true).Render(fmt.Sprintf("音轨（%d）  Space 暂停 · J/K 选择 · Enter 播放 · n/N 切歌", len(tracks))))
	cursor := m.trackCursor(*g, tracks)
	start, end := musicTrackWindow(tracks, cursor, rows, len(lines))
	if end == 0 {
		if len(lines) > rows {
			lines = lines[:rows]
		}
		return lines
	}

	for i := start; i < end; i++ {
		name := fmt.Sprintf("%02d  %s", i+1, trackName(g.Dir, tracks[i]))
		prefix := "   "
		style := tuiTextStyle
		playing := i == playIndex && m.play != nil && m.playApp == g.AppID
		if playing {
			prefix = "▶  "
			if playPaused {
				prefix = "❚❚ "
			}
			style = tuiGreenStyle
			if playPaused {
				style = tuiYellowStyle
			}
		}
		if i == cursor {
			if playing {
				prefix = "▶› "
				if playPaused {
					prefix = "❚› "
				}
			} else {
				prefix = "›  "
			}
			if !playing {
				style = tuiAccentStyle.Bold(true)
			}
		}
		lines = append(lines, prefix+style.Render(truncateDisplay(name, innerW-4)))
	}
	return lines
}

// musicTrackStartRow 返回原声带详情中音轨区域的起始行号（从 0 开始），
// 与 musicDetailLines 渲染的固定头部保持一致。
func musicTrackStartRow(g tuiGame) int {
	row := 9 // 标题、标签、空行、APPID、状态、大小、播放、空行、音轨标题
	if g.Dir != "" {
		row++
	}
	return row
}

// musicTrackWindow 返回音轨列表的可见窗口 [start, end)。
// firstRow 是音轨区域在详情内容中的起始行号。
func musicTrackWindow(tracks []string, cursor, rows, firstRow int) (start, end int) {
	available := rows - firstRow
	if available < 1 || len(tracks) == 0 {
		return 0, 0
	}
	if cursor >= available {
		start = cursor - available + 1
	}
	if maxStart := len(tracks) - available; start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	end = start + available
	if end > len(tracks) {
		end = len(tracks)
	}
	return start, end
}

func (m tuiModel) detailTags(g *tuiGame, innerW int) string {
	statusStyle := tuiMutedStyle
	switch g.Status {
	case "installed":
		statusStyle = tuiGreenStyle
	case "missing":
		statusStyle = tuiYellowStyle
	case "extra":
		statusStyle = tuiAccentStyle
	}
	parts := []string{statusStyle.Render(statusLabel(g.Status))}
	if g.Type != "" {
		parts = append(parts, tuiMutedStyle.Render(typeLabel(g.Type)))
	}
	if g.Favorite != nil {
		parts = append(parts, tuiPinkStyle.Render(tuiFavoriteLabel(g.Favorite)))
	}
	if len(g.Patches) > 0 {
		parts = append(parts, tuiAccentStyle.Render(patches.StatusLabelAll(g.Patches)))
	}
	if g.Save != nil && len(g.Save.Locations) > 0 {
		parts = append(parts, tuiAccentStyle.Render(fmt.Sprintf("存档 %d", len(g.Save.Locations))))
	}
	return fitLine(strings.Join(parts, tuiMutedStyle.Render(" · ")), innerW)
}

func (m tuiModel) detailField(label, value string, innerW int) string {
	labelW := 10
	if labelW > innerW-6 {
		labelW = innerW - 6
	}
	if labelW < 2 {
		labelW = 2
	}
	valueW := innerW - labelW
	if valueW < 2 {
		valueW = 2
	}
	return tuiMutedStyle.Render(padRight(label, labelW)) + tuiTextStyle.Render(truncateDisplay(value, valueW))
}

func (m tuiModel) statusView(w int, games []tuiGame) string {
	if m.searchMode {
		prompt := tuiAccentStyle.Bold(true).Render("/") + m.query + tuiAccentStyle.Render("▏")
		right := tuiMutedStyle.Render("Enter 确认 · Esc 清除")
		return fitLine(prompt+"  "+right, w)
	}
	if m.message != "" {
		return fitLine(tuiAccentStyle.Render(m.message), w)
	}
	if m.view == tuiViewFavorites {
		summary := fmt.Sprintf("收藏 %d 款 · 按备注/发行商排序", len(games))
		if m.query != "" {
			summary += fmt.Sprintf(" · 筛选 %q", m.query)
		}
		return fitLine(tuiMutedStyle.Render(summary), w)
	}
	if m.view == tuiViewPatches {
		records, applied := 0, 0
		for _, g := range games {
			records += len(g.Patches)
			for _, p := range g.Patches {
				if p.Applied {
					applied++
				}
			}
		}
		summary := fmt.Sprintf("补丁 %d 款 · 记录 %d 条 · 已打 %d · 未打 %d", len(games), records, applied, records-applied)
		if m.query != "" {
			summary += fmt.Sprintf(" · 筛选 %q", m.query)
		}
		return fitLine(tuiMutedStyle.Render(summary), w)
	}
	if m.view == tuiViewSaves {
		locations, files := 0, 0
		var bytes int64
		for _, g := range games {
			if g.Save == nil {
				continue
			}
			for _, loc := range g.Save.Locations {
				locations++
				files += loc.Files
				bytes += loc.Bytes
			}
		}
		summary := fmt.Sprintf("存档 %d 款 · %d 个位置 · %d 个文件 · %s · C 云 / L 本地 / B 两者",
			len(games), locations, files, humanSize(bytes))
		if m.query != "" {
			summary += fmt.Sprintf(" · 筛选 %q", m.query)
		}
		return fitLine(tuiMutedStyle.Render(summary), w)
	}
	if m.view == tuiViewMusic {
		var bytes int64
		for _, g := range games {
			bytes += g.Size
		}
		summary := fmt.Sprintf("音乐 %d 款 · 占用 %s · ♪ 原声带", len(games), humanSize(bytes))
		if m.play != nil {
			index, paused, playing := m.play.State()
			summary += " · " + nowPlayingLabel(m.playApp, index, len(m.play.Files()), paused, playing)
		}
		if m.query != "" {
			summary += fmt.Sprintf(" · 筛选 %q", m.query)
		}
		return fitLine(tuiMutedStyle.Render(summary), w)
	}
	if m.view == tuiViewInstalled {
		var bytes int64
		for _, g := range games {
			bytes += g.Size
		}
		summary := fmt.Sprintf("已安装 %d 款 · 占用 %s", len(games), humanSize(bytes))
		if m.query != "" {
			summary += fmt.Sprintf(" · 筛选 %q", m.query)
		}
		return fitLine(tuiMutedStyle.Render(summary), w)
	}
	installed, missing := 0, 0
	for _, g := range games {
		if g.installed() {
			installed++
		} else if g.Status == "missing" {
			missing++
		}
	}
	summary := fmt.Sprintf("当前 %d 款 · 已安装 %d · 未安装 %d", len(games), installed, missing)
	if m.query != "" {
		summary += fmt.Sprintf(" · 筛选 %q", m.query)
	}
	return fitLine(tuiMutedStyle.Render(summary), w)
}

func (m tuiModel) hintsView(w int) string {
	hints := "↑↓/jk 或点击 移动 · Tab 切换 · / 搜索 · h 隐藏项 · f 收藏 · s 状态 · p 补丁 · c 校验 · i 安装 · r 刷新 · ? 帮助 · q 退出"
	switch {
	case m.searchMode:
		hints = "输入关键词筛选名称 / APPID / 路径 / 备注 / 补丁 / 存档 · Enter 确认 · Esc 清除"
	case m.showHelp:
		hints = "按任意键关闭帮助"
	case m.view == tuiViewPatches:
		hints = "↑↓/jk 移动 · p 状态 · c 校验 · C 全部校验 · i 安装 · Tab 切换 · / 搜索 · ? 帮助 · q 退出"
	case m.view == tuiViewSaves:
		hints = "↑↓/jk 移动 · b 备份 · B 全部备份 · R 还原 · x 删除 · Tab 切换 · / 搜索 · ? 帮助 · q 退出"
	case m.view == tuiViewMusic:
		hints = "↑↓/jk 选原声带 · J/K 或点击/滑行 选音轨 · Enter 播放 · Space 暂停 · n/N 切歌 · / 搜索 · ? 帮助 · q 退出"
	}
	return fitLine(tuiMutedStyle.Render(truncateDisplay(hints, w)), w)
}

func (m tuiModel) confirmView(w, h int) string {
	rows := h - 2
	if rows < 1 {
		rows = 1
	}
	innerW := w - 4
	if innerW < 10 {
		innerW = 10
	}
	g := m.selected(m.filtered())
	locationCount := 0
	if g != nil && g.Save != nil {
		locationCount = len(g.Save.Locations)
	}
	var lines []string
	switch m.confirm {
	case tuiConfirmSaveRestore:
		lines = []string{tuiTitleStyle.Render("还原存档"), ""}
		if g == nil {
			lines = append(lines, tuiMutedStyle.Render("未选择游戏"))
		} else {
			lines = append(lines,
				m.detailField("游戏", g.Name, innerW),
				m.detailField("位置", fmt.Sprintf("%d 个", locationCount), innerW),
				"",
				tuiYellowStyle.Render(truncateDisplay("备份中的文件会写回原始位置，并覆盖较旧的存档。", innerW)),
				"",
				tuiAccentStyle.Bold(true).Render("Enter 确认还原 · Esc 取消"),
			)
		}
	case tuiConfirmSaveRemove:
		lines = []string{tuiTitleStyle.Render("删除存档记录"), ""}
		if g == nil {
			lines = append(lines, tuiMutedStyle.Render("未选择游戏"))
		} else {
			lines = append(lines,
				m.detailField("游戏", g.Name, innerW),
				m.detailField("位置", fmt.Sprintf("%d 个", locationCount), innerW),
				"",
				tuiRedStyle.Render(truncateDisplay("会删除 saves.json 中的记录和 <数据目录>/saves 下的备份，无法恢复。", innerW)),
				"",
				tuiAccentStyle.Bold(true).Render("Enter 确认删除 · Esc 取消"),
			)
		}
	default:
		lines = []string{tuiTitleStyle.Render("安装补丁"), ""}
		if g == nil {
			lines = append(lines, tuiMutedStyle.Render("未选择游戏"))
		} else {
			lines = append(lines,
				m.detailField("游戏", g.Name, innerW),
				m.detailField("目录", g.Dir, innerW),
				m.detailField("补丁", fmt.Sprintf("%d 个", len(g.Patches)), innerW),
				"",
				tuiYellowStyle.Render(truncateDisplay("同名文件会被替换，原文件会先备份到数据目录的 patch-backups。", innerW)),
				"",
				tuiAccentStyle.Bold(true).Render("Enter 确认安装 · Esc 取消"),
			)
		}
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return tuiHelpPaneStyle.Width(w - 2).Height(h - 2).Render(strings.Join(lines, "\n"))
}

func (m tuiModel) helpView(w, h int) string {
	rows := h - 2
	if rows < 1 {
		rows = 1
	}
	innerW := w - 4
	if innerW < 10 {
		innerW = 10
	}
	lines := []string{
		tuiTitleStyle.Render("键盘快捷键"),
		"",
		m.detailField("↑/↓ · j/k", "移动光标", innerW),
		m.detailField("鼠标", "点击标签切换视图、点击列表选择游戏、滚轮滚动", innerW),
		m.detailField("PgUp/PgDn", "翻页", innerW),
		m.detailField("g / G", "跳到顶部 / 底部", innerW),
		m.detailField("Tab", "切换视图（全部/已安装/未安装/收藏/补丁/存档/音乐）", innerW),
		m.detailField("1 - 7", "直接切换视图，7 为音乐", innerW),
		m.detailField("Enter / o", "播放选中的音乐原声带或音轨", innerW),
		m.detailField("Space", "暂停 / 继续当前音乐", innerW),
		m.detailField("J / K", "在音乐视图选择上一条 / 下一条音轨", innerW),
		m.detailField("n / N", "播放下一首 / 上一首音轨；未播放时只移动音轨选择", innerW),
		m.detailField("/", "搜索名称、APPID、路径、备注、补丁或存档", innerW),
		m.detailField("h", "显示 / 隐藏 Steam 运行库和工具", innerW),
		m.detailField("f", "收藏 / 取消收藏", innerW),
		m.detailField("s", "循环收藏状态（想玩/在玩/已通关/搁置/清除）", innerW),
		m.detailField("p", "切换补丁的已打/未打状态", innerW),
		m.detailField("c", "校验当前游戏的补丁文件与哈希", innerW),
		m.detailField("C", "校验 patches.json 中的全部补丁", innerW),
		m.detailField("i", "安装当前游戏的补丁（确认后执行，自动备份）", innerW),
		m.detailField("b", "备份当前游戏的云存档和本地存档", innerW),
		m.detailField("B", "备份全部已安装游戏的存档", innerW),
		m.detailField("R", "还原当前游戏的存档（确认后执行）", innerW),
		m.detailField("x", "删除当前游戏的存档记录和备份（确认后执行）", innerW),
		m.detailField("r", "重新读取数据目录", innerW),
		m.detailField("?", "打开 / 关闭帮助", innerW),
		m.detailField("q", "退出", innerW),
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return tuiHelpPaneStyle.Width(w - 2).Height(h - 2).Render(strings.Join(lines, "\n"))
}

// fitLine 把一行限制在终端宽度内，避免自动换行破坏全屏布局。
func fitLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func padRight(s string, width int) string {
	n := width - displayWidth(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

func tuiFavoriteLabel(f *model.Favorite) string {
	if f == nil {
		return "未收藏"
	}
	if status := favorites.NormalizeStatus(f.Status); status != "" {
		return "★ " + status
	}
	return "★ 已收藏"
}

func tuiSaveSummary(s *model.SaveGame) string {
	if s == nil || len(s.Locations) == 0 {
		return "未备份"
	}
	cloud, local, files := 0, 0, 0
	var bytes int64
	for _, loc := range s.Locations {
		switch loc.Kind {
		case saves.KindCloud:
			cloud++
		case saves.KindLocal:
			local++
		}
		files += loc.Files
		bytes += loc.Bytes
	}
	parts := []string{fmt.Sprintf("%d 个位置", len(s.Locations))}
	switch {
	case cloud > 0 && local > 0:
		parts = append(parts, "云+本地")
	case cloud > 0:
		parts = append(parts, "云")
	case local > 0:
		parts = append(parts, "本地")
	}
	parts = append(parts, fmt.Sprintf("%d 文件", files), humanSize(bytes))
	return strings.Join(parts, " · ")
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 10 {
		return hash
	}
	return hash[:10] + "…"
}
