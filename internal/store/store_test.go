package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func TestStoreRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "auto" {
		t.Fatalf("default source = %q", cfg.Source)
	}
	cfg.SteamID = "76561197960287930"
	cfg.SteamDir = "/tmp/Steam"
	if err := st.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg2, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SteamID != cfg.SteamID || cfg2.SteamDir != cfg.SteamDir {
		t.Fatalf("config mismatch: %#v", cfg2)
	}

	owned := &model.OwnedData{
		SteamID:   cfg.SteamID,
		Source:    "steamdb",
		FetchedAt: time.Now().UTC(),
		Games: []model.OwnedGame{
			{AppID: 440, Name: "Team Fortress 2"},
			{AppID: 220, Name: "Half-Life 2"},
		},
	}
	if err := st.SaveOwned(owned); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadOwned()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Count != 2 || loaded.Games[0].AppID != 440 {
		t.Fatalf("owned mismatch: %#v", loaded)
	}

	installed := &model.InstalledData{
		ScannedAt: time.Now().UTC(),
		SteamRoot: "/tmp/Steam",
		Games:     []model.App{{AppID: 440, Name: "Team Fortress 2", Installed: true}},
		Count:     1,
	}
	if err := st.SaveInstalled(installed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadInstalled(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreMissingFiles(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadOwned(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadOwned error = %v", err)
	}
	if _, err := st.LoadInstalled(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadInstalled error = %v", err)
	}
	if _, err := st.LoadSystem(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadSystem error = %v", err)
	}
	if _, err := st.LoadFavorites(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadFavorites error = %v", err)
	}
	if _, err := st.LoadPatches(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadPatches error = %v", err)
	}
}

func TestStoreSystemAndFavorites(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sys := &model.SystemData{OS: "linux", Arch: "amd64", Platform: "Test Linux"}
	if err := st.SaveSystem(sys); err != nil {
		t.Fatal(err)
	}
	loadedSys, err := st.LoadSystem()
	if err != nil {
		t.Fatal(err)
	}
	if loadedSys.OS != "linux" || loadedSys.DetectedAt.IsZero() {
		t.Fatalf("system = %#v", loadedSys)
	}

	fav := &model.FavoritesData{Games: []model.Favorite{
		{AppID: 440, Name: "TF2"},
		{AppID: 220, Name: "HL2"},
	}}
	if err := st.SaveFavorites(fav); err != nil {
		t.Fatal(err)
	}
	loadedFav, err := st.LoadFavorites()
	if err != nil {
		t.Fatal(err)
	}
	if loadedFav.Count != 2 || loadedFav.UpdatedAt.IsZero() {
		t.Fatalf("favorites = %#v", loadedFav)
	}
	if st.CoversDir() != filepath.Join(st.Dir, "covers") {
		t.Fatalf("covers dir = %q", st.CoversDir())
	}

	patches := &model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "HD Patch", Hash: "abc", Applied: true},
	}}
	if err := st.SavePatches(patches); err != nil {
		t.Fatal(err)
	}
	loadedPatches, err := st.LoadPatches()
	if err != nil {
		t.Fatal(err)
	}
	if loadedPatches.Count != 1 || loadedPatches.Patches[0].Name != "HD Patch" {
		t.Fatalf("patches = %#v", loadedPatches)
	}
}

func TestLoadCookie(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.LoadCookie(); got != "" {
		t.Fatalf("empty cookie = %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "cookie.txt"), []byte("a=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := st.LoadCookie(); got != "a=1" {
		t.Fatalf("cookie = %q", got)
	}
}

func TestDefaultDirIgnoresSourceOnlyUgal(t *testing.T) {
	t.Setenv("UGAL_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".ugal", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".ugal"); got != want {
		t.Fatalf("source-only .ugal should be ignored: got %q, want %q", got, want)
	}

	// 出现数据文件后，当前目录的 .ugal 才作为数据目录。
	if err := os.WriteFile(filepath.Join(project, ".ugal", "config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(project, ".ugal"); got != want {
		t.Fatalf("data .ugal should be preferred: got %q, want %q", got, want)
	}
}

func TestDefaultDirPrefersLocalDotUgal(t *testing.T) {
	t.Setenv("UGAL_DIR", "")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".ugal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != ".ugal" {
		t.Fatalf("default dir = %q", got)
	}
}
