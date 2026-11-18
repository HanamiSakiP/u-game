// Package store 管理 .ugal 数据目录中的配置和缓存文件。
package store

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

// defaultPatchesJSON 是新建数据目录时写入 patches.json 的默认内容，
// 可以直接编辑 internal/store/default_patches.json 后重新构建。
//
//go:embed default_patches.json
var defaultPatchesJSON []byte

const (
	configFile     = "config.json"
	ownedFile      = "owned.json"
	installedFile  = "installed.json"
	systemFile     = "system.json"
	favoritesFile  = "favorites.json"
	patchesFile    = "patches.json"
	patchesDir     = "patches"
	appNamesFile   = "appnames.json"
	publishersFile = "publishers.json"
	updateFile     = "update.json"
	coversDir      = "covers"
)

// Store 表示一个 ugal 数据目录。
type Store struct {
	Dir string
}

// DefaultDir 返回默认数据目录：UGAL_DIR > 当前目录下的 .ugal > ~/.ugal。
//
// 当前目录的 .ugal 只存放了旧版源码（src/）时不算数据目录，避免创建源码目录后
// 意外切换数据位置。
func DefaultDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("UGAL_DIR")); dir != "" {
		return expandHome(dir)
	}
	if cwd, err := os.Getwd(); err == nil {
		local := filepath.Join(cwd, ".ugal")
		if fi, err := os.Stat(local); err == nil && fi.IsDir() && looksLikeDataDir(local) {
			return local, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户主目录: %w", err)
	}
	return filepath.Join(home, ".ugal"), nil
}

// looksLikeDataDir 判断目录是否像 ugal 数据目录：空目录或包含已知的数据文件。
func looksLikeDataDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	if len(entries) == 0 {
		return true
	}
	for _, e := range entries {
		switch e.Name() {
		case configFile, ownedFile, installedFile, systemFile, favoritesFile,
			patchesFile, appNamesFile, publishersFile, updateFile, coversDir, patchesDir, "cookie.txt", "cookies.txt", "steamdb_cookie.txt":
			return true
		}
	}
	return false
}

// Open 打开（必要时创建）数据目录。dir 为空时使用 DefaultDir。
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	dir, err := expandHome(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s 失败: %w", dir, err)
	}
	return &Store{Dir: dir}, nil
}

// Path 返回数据目录下某个文件的路径。
func (s *Store) Path(name string) string { return filepath.Join(s.Dir, name) }

// ConfigPath 返回 config.json 的路径。
func (s *Store) ConfigPath() string { return s.Path(configFile) }

// OwnedPath 返回 owned.json 的路径。
func (s *Store) OwnedPath() string { return s.Path(ownedFile) }

// InstalledPath 返回 installed.json 的路径。
func (s *Store) InstalledPath() string { return s.Path(installedFile) }

// SystemPath 返回 system.json 的路径。
func (s *Store) SystemPath() string { return s.Path(systemFile) }

// FavoritesPath 返回 favorites.json 的路径。
func (s *Store) FavoritesPath() string { return s.Path(favoritesFile) }

// PatchesPath 返回 patches.json 的路径。
func (s *Store) PatchesPath() string { return s.Path(patchesFile) }

// PatchesDir 返回存放本地补丁文件的目录（默认 <数据目录>/patches）。
func (s *Store) PatchesDir() string { return s.Path(patchesDir) }

// AppNamesPath 返回 appnames.json 的路径。
func (s *Store) AppNamesPath() string { return s.Path(appNamesFile) }

// PublishersPath 返回 publishers.json 的路径。
func (s *Store) PublishersPath() string { return s.Path(publishersFile) }

// UpdatePath 返回 update.json 的路径。
func (s *Store) UpdatePath() string { return s.Path(updateFile) }

// SrcDir 返回默认的源码目录（数据目录下的 src）。
func (s *Store) SrcDir() string { return s.Path("src") }

// CoversDir 返回本地封面缓存目录。
func (s *Store) CoversDir() string { return s.Path(coversDir) }

// LoadConfig 读取配置；文件不存在时返回默认配置。
func (s *Store) LoadConfig() (*model.Config, error) {
	cfg := &model.Config{Version: model.Version, Source: "auto"}
	data, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", s.ConfigPath(), err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", s.ConfigPath(), err)
		}
	}
	if cfg.Version == 0 {
		cfg.Version = model.Version
	}
	if cfg.Source == "" {
		cfg.Source = "auto"
	}
	return cfg, nil
}

// SaveConfig 原子写入配置。
func (s *Store) SaveConfig(cfg *model.Config) error {
	cfg.Version = model.Version
	cfg.UpdatedAt = time.Now().UTC()
	return writeJSON(s.ConfigPath(), cfg, 0o644)
}

// LoadOwned 读取 owned.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadOwned() (*model.OwnedData, error) {
	var data model.OwnedData
	if err := readJSON(s.OwnedPath(), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SaveOwned 原子写入 owned.json。
func (s *Store) SaveOwned(data *model.OwnedData) error {
	if data.Version == 0 {
		data.Version = model.Version
	}
	data.Count = len(data.Games)
	return writeJSON(s.OwnedPath(), data, 0o644)
}

// LoadInstalled 读取 installed.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadInstalled() (*model.InstalledData, error) {
	var data model.InstalledData
	if err := readJSON(s.InstalledPath(), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SaveInstalled 原子写入 installed.json。
func (s *Store) SaveInstalled(data *model.InstalledData) error {
	if data.Version == 0 {
		data.Version = model.Version
	}
	return writeJSON(s.InstalledPath(), data, 0o644)
}

// LoadSystem 读取 system.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadSystem() (*model.SystemData, error) {
	var data model.SystemData
	if err := readJSON(s.SystemPath(), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SaveSystem 原子写入 system.json。
func (s *Store) SaveSystem(data *model.SystemData) error {
	if data.Version == 0 {
		data.Version = model.Version
	}
	if data.DetectedAt.IsZero() {
		data.DetectedAt = time.Now().UTC()
	}
	return writeJSON(s.SystemPath(), data, 0o644)
}

// LoadFavorites 读取 favorites.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadFavorites() (*model.FavoritesData, error) {
	var data model.FavoritesData
	if err := readJSON(s.FavoritesPath(), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SaveFavorites 原子写入 favorites.json。
func (s *Store) SaveFavorites(data *model.FavoritesData) error {
	if data == nil {
		data = &model.FavoritesData{}
	}
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	data.Count = len(data.Games)
	return writeJSON(s.FavoritesPath(), data, 0o644)
}

// LoadPatches 读取 patches.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadPatches() (*model.PatchesData, error) {
	var data model.PatchesData
	if err := readJSON(s.PatchesPath(), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SavePatches 原子写入 patches.json。
func (s *Store) SavePatches(data *model.PatchesData) error {
	if data == nil {
		data = &model.PatchesData{}
	}
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	data.Count = len(data.Patches)
	return writeJSON(s.PatchesPath(), data, 0o644)
}

// EnsurePatchesFile 在 patches.json 不存在时按内置默认模板创建补丁记录文件，
// 让用户可以直接手工编辑该文件。返回 true 表示本次新建了文件。
func (s *Store) EnsurePatchesFile() (bool, error) {
	path := s.PatchesPath()
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("检查 %s 失败: %w", path, err)
	}
	data := &model.PatchesData{Version: model.Version, Patches: []model.Patch{}}
	if len(defaultPatchesJSON) > 0 {
		if err := json.Unmarshal(defaultPatchesJSON, data); err != nil {
			return false, fmt.Errorf("解析内置默认补丁记录失败: %w", err)
		}
	}
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	data.Count = len(data.Patches)
	if err := writeJSON(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// LoadAppNames 读取 appnames.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadAppNames() (*model.AppNamesData, error) {
	var data model.AppNamesData
	if err := readJSON(s.AppNamesPath(), &data); err != nil {
		return nil, err
	}
	if data.Names == nil {
		data.Names = map[string]string{}
	}
	return &data, nil
}

// SaveAppNames 原子写入 appnames.json。
func (s *Store) SaveAppNames(data *model.AppNamesData) error {
	if data == nil {
		data = &model.AppNamesData{}
	}
	if data.Names == nil {
		data.Names = map[string]string{}
	}
	for id, name := range data.Names {
		name = strings.TrimSpace(name)
		if name == "" {
			delete(data.Names, id)
			continue
		}
		data.Names[id] = name
	}
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	data.Count = len(data.Names)
	return writeJSON(s.AppNamesPath(), data, 0o644)
}

// LoadPublishers 读取 publishers.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadPublishers() (*model.PublishersData, error) {
	var data model.PublishersData
	if err := readJSON(s.PublishersPath(), &data); err != nil {
		return nil, err
	}
	if data.Publishers == nil {
		data.Publishers = map[string]string{}
	}
	return &data, nil
}

// SavePublishers 原子写入 publishers.json。
func (s *Store) SavePublishers(data *model.PublishersData) error {
	if data == nil {
		data = &model.PublishersData{}
	}
	if data.Publishers == nil {
		data.Publishers = map[string]string{}
	}
	for id, publisher := range data.Publishers {
		publisher = strings.TrimSpace(publisher)
		if publisher == "" {
			delete(data.Publishers, id)
			continue
		}
		data.Publishers[id] = publisher
	}
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	data.Count = len(data.Publishers)
	return writeJSON(s.PublishersPath(), data, 0o644)
}

// LoadUpdate 读取 update.json。文件不存在时返回 os.ErrNotExist。
func (s *Store) LoadUpdate() (*model.UpdateData, error) {
	var data model.UpdateData
	if err := readJSON(s.UpdatePath(), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SaveUpdate 原子写入 update.json。
func (s *Store) SaveUpdate(data *model.UpdateData) error {
	if data == nil {
		data = &model.UpdateData{}
	}
	data.Version = model.Version
	data.UpdatedAt = time.Now().UTC()
	return writeJSON(s.UpdatePath(), data, 0o644)
}

// CookieFiles 按优先级返回可能包含 SteamDB Cookie 的文件路径。
func (s *Store) CookieFiles() []string {
	return []string{
		s.Path("cookie.txt"),
		s.Path("cookies.txt"),
		s.Path("steamdb_cookie.txt"),
	}
}

// LoadCookie 依次查找 .ugal 目录下的 Cookie 文件并返回原始内容。
// 支持 cookie.txt、cookies.txt、steamdb_cookie.txt。
func (s *Store) LoadCookie() string {
	for _, path := range s.CookieFiles() {
		data, err := os.ReadFile(path)
		if err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

// CookiePath 返回建议放置 Cookie 的文件路径。
func (s *Store) CookiePath() string { return s.Path("cookie.txt") }

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return err
		}
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v interface{}, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 %s 失败: %w", path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
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
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("保存 %s 失败: %w", path, err)
	}
	return nil
}

func expandHome(p string) (string, error) {
	p = strings.TrimSpace(p)
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
		return filepath.Join(home, p[2:]), nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}
