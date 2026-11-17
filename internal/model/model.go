// Package model 定义 ugal 写入 .ugal 数据目录的公共数据结构。
package model

import "time"

// Version 是数据文件的格式版本。
const Version = 1

// AppVersion 是 ugal 的默认程序版本，可由构建参数覆盖。
const AppVersion = "0.0.1"

// Config 对应 .ugal/config.json。
type Config struct {
	Version   int       `json:"version"`
	SteamID   string    `json:"steamid,omitempty"`
	SteamDir  string    `json:"steam_dir,omitempty"`
	Source    string    `json:"source,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	UpdateDir string    `json:"update_dir,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OwnedGame 是 Steam 账号拥有的一件商品（游戏、软件或原声带）。
type OwnedGame struct {
	AppID int    `json:"appid"`
	Name  string `json:"name"`
}

// OwnedData 对应 .ugal/owned.json。
type OwnedData struct {
	Version   int         `json:"version"`
	SteamID   string      `json:"steamid"`
	Source    string      `json:"source"`
	FetchedAt time.Time   `json:"fetched_at"`
	Count     int         `json:"count"`
	Games     []OwnedGame `json:"games"`
}

// App 是一份 appmanifest 所描述的本地安装项。
type App struct {
	AppID           int    `json:"appid"`
	Name            string `json:"name"`
	InstallDir      string `json:"installdir,omitempty"`
	Type            string `json:"type"` // game 或 music
	DownloadType    int    `json:"download_type,omitempty"`
	Size            int64  `json:"size,omitempty"`
	Installed       bool   `json:"installed"`
	ManifestMissing bool   `json:"manifest_missing,omitempty"`
	StateFlags      int    `json:"state_flags,omitempty"`
	LastUpdated     int64  `json:"last_updated,omitempty"`
	BuildID         string `json:"buildid,omitempty"`
	Dir             string `json:"dir,omitempty"`
	Library         string `json:"library,omitempty"`
}

// Library 是一个 Steam 库目录。
type Library struct {
	Path         string `json:"path"`
	Label        string `json:"label,omitempty"`
	Mounted      bool   `json:"mounted"`
	DownloadType int    `json:"download_type,omitempty"`
	AppsCount    int    `json:"apps_count"`
	TotalSize    int64  `json:"total_size,omitempty"`
	Apps         []App  `json:"apps"`
}

// InstalledData 对应 .ugal/installed.json。
type InstalledData struct {
	Version        int       `json:"version"`
	ScannedAt      time.Time `json:"scanned_at"`
	SteamRoot      string    `json:"steam_root"`
	LibraryFolders string    `json:"library_folders,omitempty"`
	Libraries      []Library `json:"libraries"`
	Games          []App     `json:"games"`
	Count          int       `json:"count"`
	GameCount      int       `json:"game_count"`
	MusicCount     int       `json:"music_count"`
	TotalSize      int64     `json:"total_size,omitempty"`
	Warnings       []string  `json:"warnings,omitempty"`
	Skipped        []string  `json:"skipped,omitempty"`
}

// SystemData 对应 .ugal/system.json，记录 ugal 运行所在系统。
type SystemData struct {
	Version       int       `json:"version"`
	DetectedAt    time.Time `json:"detected_at"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Platform      string    `json:"platform,omitempty"`
	Distro        string    `json:"distro,omitempty"`
	DistroVersion string    `json:"distro_version,omitempty"`
	Kernel        string    `json:"kernel,omitempty"`
	Desktop       string    `json:"desktop,omitempty"`
	SessionType   string    `json:"session_type,omitempty"`
	Hostname      string    `json:"hostname,omitempty"`
	Shell         string    `json:"shell,omitempty"`
	Locale        string    `json:"locale,omitempty"`
	GoVersion     string    `json:"go_version,omitempty"`
	SteamRoot     string    `json:"steam_root,omitempty"`
}

// Favorite 是本地收藏夹中的一款游戏。
type Favorite struct {
	AppID   int       `json:"appid"`
	Name    string    `json:"name,omitempty"`
	Note    string    `json:"note,omitempty"`
	AddedAt time.Time `json:"added_at,omitempty"`
}

// FavoritesData 对应 .ugal/favorites.json。
type FavoritesData struct {
	Version   int        `json:"version"`
	UpdatedAt time.Time  `json:"updated_at"`
	Count     int        `json:"count"`
	Games     []Favorite `json:"games"`
}

// Patch 是某款游戏的补丁记录。没有记录时视为「无补丁」。
type Patch struct {
	AppID     int       `json:"appid"`
	Name      string    `json:"name,omitempty"`
	Hash      string    `json:"hash,omitempty"`
	Applied   bool      `json:"applied"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// PatchesData 对应 .ugal/patches.json。
type PatchesData struct {
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Count     int       `json:"count"`
	Patches   []Patch   `json:"patches"`
}

// UpdateData 对应 .ugal/update.json，记录最近一次源码同步与本地构建。
type UpdateData struct {
	Version          int       `json:"version"`
	UpdatedAt        time.Time `json:"updated_at"`
	SourceDir        string    `json:"source_dir"`
	Repo             string    `json:"repo"`
	Branch           string    `json:"branch"`
	Method           string    `json:"method,omitempty"` // git 或 archive
	Revision         string    `json:"revision,omitempty"`
	PreviousRevision string    `json:"previous_revision,omitempty"`
	Changed          bool      `json:"changed"`
	Built            bool      `json:"built"`
	Binary           string    `json:"binary,omitempty"`
	ProgramVersion   string    `json:"program_version,omitempty"`
	BuiltAt          time.Time `json:"built_at,omitempty"`
}
