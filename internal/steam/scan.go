// Package steam 扫描本地 Steam 库，读取 libraryfolders.vdf 与 appmanifest_*.acf。
package steam

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/vdf"
)

// Options 控制本地扫描行为。
type Options struct {
	// SteamDir 是 Steam 根目录，也可以传入 steamapps 目录或 libraryfolders.vdf 路径。
	// 留空时自动探测常见安装位置。
	SteamDir string
	// IncludePartial 为 true 时保留 appmanifest 存在但文件目录缺失的条目。
	IncludePartial bool
}

type libraryEntry struct {
	Path         string
	Label        string
	Mounted      bool
	DownloadType int
	Apps         map[int]int64
}

// DefaultRoots 返回当前系统常见的 Steam 根目录候选。
func DefaultRoots() []string {
	home, _ := os.UserHomeDir()
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		roots = append(roots, p)
	}

	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if dir := os.Getenv(env); dir != "" {
				add(filepath.Join(dir, "Steam"))
			}
		}
		add(`C:\Program Files (x86)\Steam`)
	case "darwin":
		add(filepath.Join(home, "Library", "Application Support", "Steam"))
	default:
		add(filepath.Join(home, ".steam", "steam"))
		add(filepath.Join(home, ".steam", "root"))
		add(filepath.Join(home, ".local", "share", "Steam"))
		add(filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"))
		add(filepath.Join(home, "snap", "steam", "common", ".local", "share", "Steam"))
	}
	return roots
}

// Scan 扫描 Steam 库并返回可写入 installed.json 的数据。
func Scan(opts Options) (*model.InstalledData, error) {
	root, err := locateSteamRoot(opts.SteamDir)
	if err != nil {
		return nil, err
	}

	result := &model.InstalledData{
		Version:   model.Version,
		ScannedAt: time.Now().UTC(),
		SteamRoot: root,
	}

	lfPath := locateLibraryFolders(root)
	result.LibraryFolders = lfPath

	var entries []libraryEntry
	if lfPath != "" {
		node, err := vdf.ParseFile(lfPath)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", lfPath, err)
		}
		entries = parseLibraryFolders(node)
	}
	if len(entries) == 0 {
		// 没有 libraryfolders.vdf（或内容为空）时，至少扫描 Steam 根目录本身。
		entries = []libraryEntry{{Path: root, Mounted: true}}
	}

	seenLibs := make(map[string]bool)
	for _, entry := range entries {
		libPath := cleanPath(entry.Path)
		if libPath == "" {
			continue
		}
		key := libPath
		if resolved, err := filepath.EvalSymlinks(libPath); err == nil {
			key = resolved
		}
		if seenLibs[key] {
			continue
		}
		seenLibs[key] = true

		lib := model.Library{
			Path:         libPath,
			Label:        entry.Label,
			Mounted:      entry.Mounted,
			DownloadType: entry.DownloadType,
			Apps:         []model.App{},
		}
		if !entry.Mounted {
			result.Libraries = append(result.Libraries, lib)
			continue
		}
		if fi, err := os.Stat(libPath); err != nil || !fi.IsDir() {
			lib.Mounted = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("库目录不存在或不可访问: %s", libPath))
			result.Libraries = append(result.Libraries, lib)
			continue
		}

		appsDir := steamAppsDir(libPath)
		if appsDir == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("库目录缺少 steamapps: %s", libPath))
		}

		ids := make(map[int]int64)
		for id, size := range entry.Apps {
			ids[id] = size
		}
		for id, size := range listManifests(appsDir) {
			if _, ok := ids[id]; !ok {
				ids[id] = size
			}
		}

		libIDs := make([]int, 0, len(ids))
		for id := range ids {
			libIDs = append(libIDs, id)
		}
		sort.Ints(libIDs)

		for _, id := range libIDs {
			app, warn := readApp(libPath, appsDir, id, ids[id], entry.DownloadType)
			if warn != "" {
				result.Warnings = append(result.Warnings, warn)
			}
			if !app.Installed && !opts.IncludePartial {
				result.Skipped = append(result.Skipped, fmt.Sprintf("%d: 未完整安装（%s）", id, app.Name))
				continue
			}
			app.Library = libPath
			lib.Apps = append(lib.Apps, app)
			if app.Installed {
				lib.AppsCount++
				lib.TotalSize += app.Size
				result.Count++
				result.TotalSize += app.Size
				if app.Type == "music" {
					result.MusicCount++
				} else {
					result.GameCount++
				}
			}
			result.Games = append(result.Games, app)
		}

		sort.SliceStable(lib.Apps, func(i, j int) bool {
			return lessApp(lib.Apps[i], lib.Apps[j])
		})
		result.Libraries = append(result.Libraries, lib)
	}

	sort.SliceStable(result.Games, func(i, j int) bool {
		return lessApp(result.Games[i], result.Games[j])
	})
	sort.SliceStable(result.Libraries, func(i, j int) bool {
		return strings.ToLower(result.Libraries[i].Path) < strings.ToLower(result.Libraries[j].Path)
	})
	return result, nil
}

func lessApp(a, b model.App) bool {
	an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if an != bn {
		return an < bn
	}
	return a.AppID < b.AppID
}

func locateSteamRoot(custom string) (string, error) {
	if strings.TrimSpace(custom) != "" {
		root := normalizeRoot(custom)
		if steamAppsDir(root) == "" && locateLibraryFolders(root) == "" {
			return "", fmt.Errorf("在 %s 中找不到 steamapps 目录或 libraryfolders.vdf", root)
		}
		return root, nil
	}

	var tried []string
	for _, cand := range DefaultRoots() {
		root := normalizeRoot(cand)
		if root == "" {
			continue
		}
		tried = append(tried, root)
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		if steamAppsDir(root) != "" || locateLibraryFolders(root) != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("未找到 Steam 安装目录，请用 `ugal scan <Steam目录>` 或 `ugal config steam-dir <目录>` 指定。已尝试: %s",
		strings.Join(uniqueStrings(tried), ", "))
}

func normalizeRoot(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return ""
	}
	p = filepath.Clean(filepath.FromSlash(p))
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() && strings.EqualFold(filepath.Base(p), "libraryfolders.vdf") {
		dir := filepath.Dir(p)
		if isDirName(dir, "steamapps") || isDirName(dir, "config") {
			return filepath.Dir(dir)
		}
		return dir
	}
	if isDirName(p, "steamapps") {
		return filepath.Dir(p)
	}
	if strings.EqualFold(filepath.Base(p), "common") && isDirName(filepath.Dir(p), "steamapps") {
		return filepath.Dir(filepath.Dir(p))
	}
	return p
}

func isDirName(p, name string) bool {
	return strings.EqualFold(filepath.Base(filepath.Clean(p)), name)
}

func steamAppsDir(root string) string {
	dir := filepath.Join(root, "steamapps")
	if isDir(dir) {
		return dir
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), "steamapps") {
			return filepath.Join(root, e.Name())
		}
	}
	return ""
}

func locateLibraryFolders(root string) string {
	apps := steamAppsDir(root)
	candidates := []string{
		filepath.Join(root, "config", "libraryfolders.vdf"),
		filepath.Join(root, "libraryfolders.vdf"),
	}
	if apps != "" {
		candidates = append([]string{filepath.Join(apps, "libraryfolders.vdf")}, candidates...)
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if apps != "" {
		if p := findFileCI(apps, "libraryfolders.vdf"); p != "" {
			return p
		}
	}
	if p := findFileCI(filepath.Join(root, "config"), "libraryfolders.vdf"); p != "" {
		return p
	}
	return ""
}

func findFileCI(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func parseLibraryFolders(root *vdf.Node) []libraryEntry {
	lf := root.Child("libraryfolders")
	if lf == nil {
		lf = root
	}

	var out []libraryEntry
	for _, c := range lf.Children {
		entry := libraryEntry{Mounted: true, Apps: map[int]int64{}}
		if c.HasChildren() {
			path := c.Child("path")
			if path == nil {
				continue
			}
			entry.Path = path.String()
			if label := c.Child("label"); label != nil {
				entry.Label = label.String()
			}
			if mounted := c.Child("mounted"); mounted != nil {
				entry.Mounted = strings.TrimSpace(mounted.String()) == "1"
			}
			if dt := c.Child("downloadtype"); dt != nil {
				entry.DownloadType = dt.Int()
			}
			if apps := c.Child("apps"); apps != nil {
				for _, app := range apps.Children {
					id, err := strconv.Atoi(strings.TrimSpace(app.Key))
					if err != nil || id <= 0 {
						continue
					}
					entry.Apps[id] = app.Int64()
				}
			}
		} else if looksLikePath(c.Value) {
			// 旧版 libraryfolders.vdf: "1" "D:\\SteamLibrary"
			entry.Path = c.Value
		} else {
			continue
		}
		if entry.Path != "" {
			out = append(out, entry)
		}
	}
	return out
}

func looksLikePath(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	return filepath.IsAbs(v) || strings.ContainsAny(v, `/\`) || (len(v) >= 2 && v[1] == ':')
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return ""
	}
	p = filepath.Clean(filepath.FromSlash(p))
	// Windows 路径在非 Windows 系统上保留原样，交给 os.Stat 判断即可。
	return p
}

func listManifests(appsDir string) map[int]int64 {
	out := make(map[int]int64)
	if appsDir == "" {
		return out
	}
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "appmanifest_") || !strings.HasSuffix(lower, ".acf") {
			continue
		}
		idText := name[len("appmanifest_") : len(name)-len(".acf")]
		id, err := strconv.Atoi(idText)
		if err != nil || id <= 0 {
			continue
		}
		out[id] = 0
	}
	return out
}

func readApp(libPath, appsDir string, appid int, vdfSize int64, libDownloadType int) (model.App, string) {
	app := model.App{
		AppID:        appid,
		DownloadType: libDownloadType,
		Size:         vdfSize,
		Library:      libPath,
	}
	manifest := filepath.Join(appsDir, fmt.Sprintf("appmanifest_%d.acf", appid))
	if appsDir == "" {
		manifest = ""
	}
	if manifest == "" || !isFile(manifest) {
		if p := findFileCI(appsDir, fmt.Sprintf("appmanifest_%d.acf", appid)); p != "" {
			manifest = p
		}
	}
	if manifest == "" || !isFile(manifest) {
		app.Name = ""
		app.Type = "game"
		app.Installed = true
		app.ManifestMissing = true
		return app, fmt.Sprintf("%d: 缺少 appmanifest_%d.acf", appid, appid)
	}

	node, err := vdf.ParseFile(manifest)
	if err != nil {
		app.Name = ""
		app.Type = "game"
		app.Installed = true
		app.ManifestMissing = true
		return app, fmt.Sprintf("%d: 解析 %s 失败: %v", appid, manifest, err)
	}
	state := node.Child("AppState")
	if state == nil {
		state = node
	}
	app.Name = state.Child("name").String()
	app.InstallDir = state.Child("installdir").String()
	app.DownloadType = state.Child("DownloadType").Int()
	app.StateFlags = state.Child("StateFlags").Int()
	app.LastUpdated = state.Child("LastUpdated").Int64()
	app.BuildID = state.Child("buildid").String()
	if app.Name == "" {
		app.Name = state.Child("UserConfig").Child("name").String()
	}
	if sz := state.Child("SizeOnDisk").Int64(); sz > 0 {
		app.Size = sz
	}

	gameDir := filepath.Join(libPath, "steamapps", "common", app.InstallDir)
	musicDir := filepath.Join(libPath, "steamapps", "music", app.InstallDir)
	switch {
	case app.InstallDir != "" && isDir(gameDir):
		app.Type = "game"
		app.Dir = gameDir
		app.Installed = true
	case app.InstallDir != "" && isDir(musicDir):
		app.Type = "music"
		app.Dir = musicDir
		app.Installed = true
	default:
		// 文件夹不存在时按 readme 约定：DownloadType 1 = 音乐，3 = 游戏。
		app.Type = typeFromDownloadType(app.DownloadType)
		if app.Type == "music" {
			app.Dir = musicDir
		} else {
			app.Dir = gameDir
		}
		app.Installed = app.StateFlags&4 != 0
	}
	return app, ""
}

func typeFromDownloadType(dt int) string {
	switch dt {
	case 1:
		return "music"
	case 3:
		return "game"
	default:
		return "game"
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
