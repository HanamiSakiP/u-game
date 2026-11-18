// Package saves 把 Steam 云存档与游戏目录中的本地存档同步到 ugal 数据目录，
// 并在需要时把备份写回原来的位置。
//
// 云存档读取 Steam 的 userdata/<账号ID>/<appid>/remote 目录；本地存档自动
// 查找游戏目录中的 savedata、save、saves 等常见目录。数据目录结构：
//
//	<数据目录>/saves/<appid>/cloud/...
//	<数据目录>/saves/<appid>/local/<目录名>/...
package saves

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

const (
	// KindCloud 表示 Steam 云存档（userdata 中的 remote 目录）。
	KindCloud = "cloud"
	// KindLocal 表示游戏目录中的本地存档。
	KindLocal = "local"

	// DefaultLocalDepth 是自动查找本地存档目录的最大深度。
	DefaultLocalDepth = 3
)

// Options 控制一次存档同步。
type Options struct {
	// Root 是 ugal 数据目录，存档写入 <Root>/saves。
	Root string
	// Installed 用于查找 appid 对应的游戏目录与 Steam 根目录。
	Installed *model.InstalledData
	// AppIDs 指定要同步的游戏；为空时同步所有已安装游戏。
	AppIDs []int
	// GameDirs 可以覆盖 appid 对应的游戏目录。
	GameDirs map[int]string
	// Names 可以覆盖游戏名称。
	Names map[int]string
	// SteamRoot 覆盖 Steam 根目录（默认取 Installed.SteamRoot）。
	SteamRoot string
	// SteamUser 是 Steam 账号 ID（userdata 下的数字目录）。
	SteamUser string
	// Cloud / Local 控制同步哪一类存档，默认由调用方设置为 true。
	Cloud bool
	Local bool
	// LocalDirs 显式指定本地存档目录（对所有 AppIDs 生效）；为空时自动查找。
	LocalDirs []string
	// Force 为 true 时即使备份中的文件更新也覆盖。
	Force bool
	// Mirror 为 true 时删除备份中源目录已不存在的文件。
	Mirror bool
	// DryRun 只统计，不写入文件。
	DryRun bool
	// Logf 输出进度，可为空。
	Logf func(format string, args ...interface{})
}

// SyncResult 是一次同步的汇总。
type SyncResult struct {
	SteamUser string       `json:"steam_user,omitempty"`
	CloudRoot string       `json:"cloud_root,omitempty"`
	Saves     []GameResult `json:"saves"`
	Warnings  []string     `json:"warnings,omitempty"`
}

// GameResult 是一款游戏的同步结果。
type GameResult struct {
	AppID     int              `json:"appid"`
	Name      string           `json:"name,omitempty"`
	GameDir   string           `json:"game_dir,omitempty"`
	Locations []LocationResult `json:"locations"`
	Warnings  []string         `json:"warnings,omitempty"`
}

// LocationResult 描述一个存档目录的同步结果。
type LocationResult struct {
	Kind string `json:"kind"`
	// Source 是存档的原始目录。
	Source string `json:"source"`
	// Store 是数据目录中的备份目录（相对于数据目录）。
	Store   string `json:"store"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Copied  int    `json:"copied"`
	Updated int    `json:"updated"`
	Skipped int    `json:"skipped"`
	Removed int    `json:"removed,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

// RestoreOptions 控制把备份写回原位置。
type RestoreOptions struct {
	Root  string
	IDs   []int
	Cloud bool
	Local bool
	Force bool
	// Mirror 为 true 时删除目标目录中备份里不存在的文件。
	Mirror bool
	DryRun bool
	Logf   func(format string, args ...interface{})
}

// RestoreResult 是一次还原的汇总。
type RestoreResult struct {
	Saves    []GameResult `json:"saves"`
	Warnings []string     `json:"warnings,omitempty"`
}

type copyStats struct {
	Files   int
	Bytes   int64
	Copied  int
	Updated int
	Skipped int
	Removed int
}

type copyOptions struct {
	Force  bool
	Mirror bool
	DryRun bool
}

// Sync 把云存档和本地存档复制到数据目录。
func Sync(opts Options) (*SyncResult, error) {
	root, err := expandPath(opts.Root)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("缺少数据目录")
	}
	if !opts.Cloud && !opts.Local {
		return nil, errors.New("云存档和本地存档至少选择一项")
	}

	installed := opts.Installed
	steamRoot := strings.TrimSpace(opts.SteamRoot)
	if steamRoot == "" && installed != nil {
		steamRoot = strings.TrimSpace(installed.SteamRoot)
	}
	if steamRoot != "" {
		steamRoot, err = expandPath(steamRoot)
		if err != nil {
			return nil, err
		}
	}

	result := &SyncResult{CloudRoot: steamRoot}
	if opts.Cloud {
		if steamRoot == "" {
			result.Warnings = append(result.Warnings, "未找到 Steam 根目录，跳过云存档；请先运行 `ugal scan`")
		} else {
			user, userErr := DetectUser(steamRoot, opts.SteamUser)
			if userErr != nil {
				result.Warnings = append(result.Warnings, userErr.Error())
			} else {
				result.SteamUser = user
				if pref := strings.TrimSpace(opts.SteamUser); pref != "" && pref != user {
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("指定的 Steam 账号 %s 不存在，已改用 %s", pref, user))
				}
			}
		}
	}

	ids, err := selectAppIDs(opts, installed)
	if err != nil {
		return nil, err
	}

	appByID := make(map[int]model.App)
	if installed != nil {
		for _, app := range installed.Games {
			if app.Installed {
				appByID[app.AppID] = app
			}
		}
	}

	for _, id := range ids {
		app := appByID[id]
		name := strings.TrimSpace(opts.Names[id])
		if name == "" {
			name = app.Name
		}
		gameDir := strings.TrimSpace(opts.GameDirs[id])
		if gameDir == "" {
			gameDir = strings.TrimSpace(app.Dir)
		}
		if gameDir != "" {
			if gameDir, err = expandPath(gameDir); err != nil {
				return nil, err
			}
		}

		gr := GameResult{AppID: id, Name: name, GameDir: gameDir}
		used := make(map[string]bool)
		addLocation := func(kind, source string) error {
			source, err := expandPath(source)
			if err != nil {
				return err
			}
			fi, err := os.Stat(source)
			if err != nil || !fi.IsDir() {
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					gr.Warnings = append(gr.Warnings, fmt.Sprintf("%s: %v", source, err))
				}
				return nil
			}
			store := storeRelPath(root, id, kind, source, used)
			stats, warnings := copyTree(source, filepath.Join(root, filepath.FromSlash(store)), copyOptions{
				Force:  opts.Force,
				Mirror: opts.Mirror,
				DryRun: opts.DryRun,
			})
			gr.Warnings = append(gr.Warnings, warnings...)
			gr.Locations = append(gr.Locations, LocationResult{
				Kind:    kind,
				Source:  source,
				Store:   filepath.ToSlash(store),
				Files:   stats.Files,
				Bytes:   stats.Bytes,
				Copied:  stats.Copied,
				Updated: stats.Updated,
				Skipped: stats.Skipped,
				Removed: stats.Removed,
				DryRun:  opts.DryRun,
			})
			logf(opts.Logf, "[%d] %s 存档 %s -> %s（%d 个文件）", id, kind, source, filepath.Join(root, filepath.FromSlash(store)), stats.Files)
			return nil
		}

		if opts.Cloud && result.SteamUser != "" {
			cloudDir := filepath.Join(steamRoot, "userdata", result.SteamUser, strconv.Itoa(id), "remote")
			if err := addLocation(KindCloud, cloudDir); err != nil {
				return nil, err
			}
		}

		if opts.Local {
			switch {
			case len(opts.LocalDirs) > 0:
				for _, dir := range opts.LocalDirs {
					if strings.TrimSpace(dir) == "" {
						continue
					}
					if err := addLocation(KindLocal, dir); err != nil {
						return nil, err
					}
				}
			case gameDir != "":
				if fi, err := os.Stat(gameDir); err != nil || !fi.IsDir() {
					gr.Warnings = append(gr.Warnings, fmt.Sprintf("游戏目录不存在: %s", gameDir))
				} else {
					for _, dir := range FindLocalSaveDirs(gameDir) {
						if err := addLocation(KindLocal, dir); err != nil {
							return nil, err
						}
					}
				}
			}
		}

		if len(gr.Locations) == 0 && len(opts.AppIDs) > 0 {
			if opts.Cloud && result.SteamUser == "" {
				// Steam 用户未确定时已经在结果级给出提示。
			} else if gameDir == "" && opts.Local && len(opts.LocalDirs) == 0 {
				gr.Warnings = append(gr.Warnings, "未找到游戏目录，无法自动查找本地存档；可用 --local-dir 指定")
			} else if opts.Cloud || opts.Local {
				gr.Warnings = append(gr.Warnings, "没有找到可同步的存档")
			}
		}
		if len(gr.Locations) > 0 || len(opts.AppIDs) > 0 {
			result.Saves = append(result.Saves, gr)
		}
	}
	return result, nil
}

// Restore 把数据目录中的存档备份写回原始位置。
func Restore(data *model.SavesData, opts RestoreOptions) (*RestoreResult, error) {
	root, err := expandPath(opts.Root)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("缺少数据目录")
	}
	if !opts.Cloud && !opts.Local {
		return nil, errors.New("云存档和本地存档至少选择一项")
	}
	result := &RestoreResult{}
	if data == nil {
		return result, nil
	}

	filter := make(map[int]bool, len(opts.IDs))
	for _, id := range opts.IDs {
		if id > 0 {
			filter[id] = true
		}
	}

	for _, game := range data.Saves {
		if len(filter) > 0 && !filter[game.AppID] {
			continue
		}
		gr := GameResult{AppID: game.AppID, Name: game.Name, GameDir: game.GameDir}
		for _, loc := range game.Locations {
			if loc.Kind == KindCloud && !opts.Cloud {
				continue
			}
			if loc.Kind == KindLocal && !opts.Local {
				continue
			}
			src := filepath.Join(root, filepath.FromSlash(loc.Store))
			if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
				gr.Warnings = append(gr.Warnings, fmt.Sprintf("备份目录不存在: %s", src))
				continue
			}
			target := strings.TrimSpace(loc.Path)
			if target == "" {
				gr.Warnings = append(gr.Warnings, fmt.Sprintf("%s 存档缺少原始路径", loc.Kind))
				continue
			}
			target, err = expandPath(target)
			if err != nil {
				return nil, err
			}
			if samePath(src, target) {
				gr.Warnings = append(gr.Warnings, fmt.Sprintf("备份与目标相同，跳过: %s", target))
				continue
			}
			stats, warnings := copyTree(src, target, copyOptions{
				Force:  opts.Force,
				Mirror: opts.Mirror,
				DryRun: opts.DryRun,
			})
			gr.Warnings = append(gr.Warnings, warnings...)
			gr.Locations = append(gr.Locations, LocationResult{
				Kind:    loc.Kind,
				Source:  src,
				Store:   target,
				Files:   stats.Files,
				Bytes:   stats.Bytes,
				Copied:  stats.Copied,
				Updated: stats.Updated,
				Skipped: stats.Skipped,
				Removed: stats.Removed,
				DryRun:  opts.DryRun,
			})
			logf(opts.Logf, "[%d] 还原 %s 存档 %s -> %s（写入 %d 个文件）", game.AppID, loc.Kind, src, target, stats.Copied+stats.Updated)
		}
		result.Saves = append(result.Saves, gr)
	}
	return result, nil
}

// UpdateManifest 把同步结果合并进 saves.json 数据。只会替换本次实际同步到的
// 存档类型（cloud / local），未涉及的旧记录会保留。
func UpdateManifest(data *model.SavesData, result *SyncResult) {
	if data == nil || result == nil {
		return
	}
	now := time.Now().UTC()
	if result.SteamUser != "" {
		data.SteamUser = result.SteamUser
	}
	index := make(map[int]int, len(data.Saves))
	for i, game := range data.Saves {
		index[game.AppID] = i
	}
	for _, gr := range result.Saves {
		if gr.AppID <= 0 {
			continue
		}
		i, ok := index[gr.AppID]
		if !ok {
			data.Saves = append(data.Saves, model.SaveGame{AppID: gr.AppID})
			i = len(data.Saves) - 1
			index[gr.AppID] = i
		}
		game := data.Saves[i]
		if strings.TrimSpace(gr.Name) != "" {
			game.Name = gr.Name
		}
		if strings.TrimSpace(gr.GameDir) != "" {
			game.GameDir = gr.GameDir
		}
		kinds := make(map[string]bool)
		for _, loc := range gr.Locations {
			kinds[loc.Kind] = true
		}
		if len(kinds) > 0 {
			kept := game.Locations[:0]
			for _, old := range game.Locations {
				if kinds[old.Kind] {
					continue
				}
				kept = append(kept, old)
			}
			game.Locations = kept
			for _, loc := range gr.Locations {
				game.Locations = append(game.Locations, model.SaveLocation{
					Kind:     loc.Kind,
					Path:     loc.Source,
					Store:    loc.Store,
					Files:    loc.Files,
					Bytes:    loc.Bytes,
					Copied:   loc.Copied,
					Updated:  loc.Updated,
					Skipped:  loc.Skipped,
					SyncedAt: now,
				})
			}
		}
		game.UpdatedAt = now
		data.Saves[i] = game
	}
	sort.SliceStable(data.Saves, func(i, j int) bool { return data.Saves[i].AppID < data.Saves[j].AppID })
	data.Version = model.Version
	data.UpdatedAt = now
	data.Count = len(data.Saves)
}

// Remove 从记录和磁盘中删除指定游戏的存档备份，返回删除的游戏数。
func Remove(data *model.SavesData, root string, ids []int) int {
	if data == nil {
		return 0
	}
	drop := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id > 0 {
			drop[id] = true
		}
	}
	kept := data.Saves[:0]
	removed := 0
	for _, game := range data.Saves {
		if drop[game.AppID] {
			removed++
			if strings.TrimSpace(root) != "" {
				_ = os.RemoveAll(filepath.Join(root, "saves", strconv.Itoa(game.AppID)))
			}
			continue
		}
		kept = append(kept, game)
	}
	data.Saves = kept
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	data.Count = len(data.Saves)
	return removed
}

// Find 返回指定 appid 的存档记录。
func Find(data *model.SavesData, appid int) *model.SaveGame {
	if data == nil {
		return nil
	}
	for i := range data.Saves {
		if data.Saves[i].AppID == appid {
			game := data.Saves[i]
			return &game
		}
	}
	return nil
}

// AccountID 把 SteamID64 或 AccountID 转换为 userdata 目录使用的 32 位账号 ID。
func AccountID(steamID string) string {
	value := strings.Trim(strings.TrimSpace(steamID), `"'`)
	if value == "" {
		return ""
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return ""
	}
	const steamID64Base = 76561197960265728
	if id >= steamID64Base {
		return strconv.FormatUint(id-steamID64Base, 10)
	}
	if id <= 0xFFFFFFFF {
		return strconv.FormatUint(id, 10)
	}
	return ""
}

// DetectUser 返回 userdata 下要使用的 Steam 账号 ID。
// preferred 存在时优先使用；否则选择最近修改的账号目录。
func DetectUser(steamRoot, preferred string) (string, error) {
	userdata := filepath.Join(strings.TrimSpace(steamRoot), "userdata")
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		if fi, err := os.Stat(filepath.Join(userdata, preferred)); err == nil && fi.IsDir() {
			return preferred, nil
		}
	}
	entries, err := os.ReadDir(userdata)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("未找到 Steam userdata 目录: %s", userdata)
		}
		return "", fmt.Errorf("读取 Steam userdata 目录失败: %w", err)
	}
	type candidate struct {
		user    string
		modTime time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		user := entry.Name()
		if _, err := strconv.ParseUint(user, 10, 32); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{user: user, modTime: info.ModTime()})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%s 中没有 Steam 账号目录", userdata)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].modTime.After(candidates[j].modTime)
		}
		return candidates[i].user > candidates[j].user
	})
	return candidates[0].user, nil
}

// FindLocalSaveDirs 在游戏目录中递归查找常见的存档目录。
func FindLocalSaveDirs(gameDir string) []string {
	gameDir = filepath.Clean(strings.TrimSpace(gameDir))
	if gameDir == "" {
		return nil
	}
	if fi, err := os.Stat(gameDir); err != nil || !fi.IsDir() {
		return nil
	}
	var dirs []string
	_ = filepath.WalkDir(gameDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != gameDir {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(gameDir, path)
			if relErr != nil {
				return nil
			}
			depth := len(strings.Split(filepath.ToSlash(rel), "/"))
			if depth > DefaultLocalDepth {
				return filepath.SkipDir
			}
		}
		if path != gameDir && isSaveDirName(d.Name()) {
			dirs = append(dirs, path)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}

var saveDirNames = map[string]bool{
	"savedata":   true,
	"save":       true,
	"saves":      true,
	"savegame":   true,
	"savegames":  true,
	"savedgame":  true,
	"savedgames": true,
	"save_data":  true,
	"saved":      true,
	"存档":         true,
	"存檔":         true,
}

var ignoredDirNames = map[string]bool{
	"node_modules":     true,
	"__pycache__":      true,
	".git":             true,
	".svn":             true,
	"steamapps":        true,
	"common":           true,
	"engine":           true,
	"bin":              true,
	"binaries":         true,
	"redist":           true,
	"redistributables": true,
}

func isSaveDirName(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	if saveDirNames[key] {
		return true
	}
	normalized := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(key)
	return saveDirNames[normalized]
}

func isIgnoredDir(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(key, ".") {
		return true
	}
	return ignoredDirNames[key]
}

func storeRelPath(root string, appid int, kind, source string, used map[string]bool) string {
	base := filepath.Join("saves", strconv.Itoa(appid))
	if kind == KindCloud {
		return filepath.Join(base, KindCloud)
	}
	name := sanitizeName(filepath.Base(source))
	if name == "" || name == "." {
		name = fmt.Sprintf("dir%d", len(used)+1)
	}
	candidate := name
	for i := 2; used[strings.ToLower(filepath.Join(base, KindLocal, candidate))]; i++ {
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	used[strings.ToLower(filepath.Join(base, KindLocal, candidate))] = true
	return filepath.Join(base, KindLocal, candidate)
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, name)
	return strings.Trim(name, " .")
}

func selectAppIDs(opts Options, installed *model.InstalledData) ([]int, error) {
	if len(opts.AppIDs) > 0 {
		ids := append([]int(nil), opts.AppIDs...)
		sort.Ints(ids)
		return ids, nil
	}
	if installed == nil {
		return nil, errors.New("还没有本地扫描数据，请先运行 `ugal scan`，或用 appid 指定游戏")
	}
	seen := make(map[int]bool)
	var ids []int
	for _, app := range installed.Games {
		if !app.Installed || app.AppID <= 0 || seen[app.AppID] {
			continue
		}
		seen[app.AppID] = true
		ids = append(ids, app.AppID)
	}
	sort.Ints(ids)
	if len(ids) == 0 {
		return nil, errors.New("没有已安装的游戏，请先运行 `ugal scan`")
	}
	return ids, nil
}

func copyTree(src, dst string, opts copyOptions) (copyStats, []string) {
	var stats copyStats
	var warnings []string
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)
	if samePath(src, dst) {
		warnings = append(warnings, fmt.Sprintf("源目录与目标目录相同，跳过: %s", src))
		return stats, warnings
	}
	if withinPath(dst, src) {
		warnings = append(warnings, fmt.Sprintf("目标目录位于源目录内，跳过: %s", dst))
		return stats, warnings
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("读取存档目录失败: %v", err))
		}
		return stats, warnings
	}
	if !opts.DryRun {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			warnings = append(warnings, fmt.Sprintf("创建目录失败: %v", err))
			return stats, warnings
		}
	}

	sourceRels := make(map[string]bool)
	_ = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if path == src {
			return nil
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return nil
		}
		sourceRels[rel] = true
		if d.Type()&os.ModeSymlink != 0 {
			warnings = append(warnings, fmt.Sprintf("跳过符号链接: %s", path))
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if !opts.DryRun {
				if err := os.MkdirAll(target, 0o755); err != nil {
					warnings = append(warnings, fmt.Sprintf("创建目录失败: %v", err))
				}
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, infoErr))
			return nil
		}
		if !info.Mode().IsRegular() {
			warnings = append(warnings, fmt.Sprintf("跳过特殊文件: %s", path))
			return nil
		}
		stats.Files++
		stats.Bytes += info.Size()
		existing, statErr := os.Stat(target)
		switch {
		case statErr == nil && existing.IsDir():
			warnings = append(warnings, fmt.Sprintf("目标位置是目录，跳过文件: %s", target))
			return nil
		case statErr == nil && !opts.Force && !info.ModTime().After(existing.ModTime()):
			stats.Skipped++
			return nil
		case statErr == nil:
			stats.Updated++
		default:
			stats.Copied++
		}
		if opts.DryRun {
			return nil
		}
		if err := copyFile(path, target, info); err != nil {
			warnings = append(warnings, fmt.Sprintf("复制 %s 失败: %v", path, err))
		}
		return nil
	})

	if opts.Mirror && !opts.DryRun {
		removed, removeWarnings := removeExtras(dst, sourceRels)
		stats.Removed += removed
		warnings = append(warnings, removeWarnings...)
	}
	return stats, warnings
}

func removeExtras(dst string, keep map[string]bool) (int, []string) {
	var warnings []string
	removed := 0
	var dirs []string
	_ = filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == dst {
			return nil
		}
		rel, relErr := filepath.Rel(dst, path)
		if relErr != nil || keep[rel] {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if err := os.Remove(path); err == nil {
			removed++
		} else {
			warnings = append(warnings, fmt.Sprintf("删除多余文件失败: %v", err))
		}
		return nil
	})
	sort.SliceStable(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		if err := os.Remove(dir); err == nil {
			removed++
		}
	}
	return removed, warnings
}

func copyFile(src, dst string, info fs.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ugal-save-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	_ = os.Chmod(tmpName, mode)
	_ = os.Chtimes(tmpName, info.ModTime(), info.ModTime())
	if err := os.Rename(tmpName, dst); err != nil {
		// Windows 上目标存在时 Rename 会失败，先删除再重试。
		if removeErr := os.Remove(dst); removeErr == nil {
			err = os.Rename(tmpName, dst)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	if resolved, err := filepath.EvalSymlinks(aa); err == nil {
		aa = resolved
	}
	if resolved, err := filepath.EvalSymlinks(bb); err == nil {
		bb = resolved
	}
	return aa == bb
}

// withinPath 判断 target 是否在 dir 之内。
func withinPath(dir, target string) bool {
	dirAbs, err1 := filepath.Abs(dir)
	targetAbs, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(dirAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		p = filepath.Join(home, p[2:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func logf(fn func(string, ...interface{}), format string, args ...interface{}) {
	if fn != nil {
		fn(format, args...)
	}
}
