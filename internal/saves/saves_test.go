package saves

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func TestAccountID(t *testing.T) {
	cases := map[string]string{
		"76561197960287930": "22202",
		"22202":             "22202",
		"https://x":         "",
		"":                  "",
	}
	for in, want := range cases {
		if got := AccountID(in); got != want {
			t.Errorf("AccountID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindLocalSaveDirs(t *testing.T) {
	gameDir := t.TempDir()
	for _, rel := range []string{
		"savedata/a.sav",
		"data/save/b.sav",
		"deep/one/saves/c.sav",
		"deep/one/two/three/saves/d.sav", // 超过默认深度
		"bin/save/tool.sav",
	} {
		path := filepath.Join(gameDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := FindLocalSaveDirs(gameDir)
	want := []string{
		filepath.Join(gameDir, "data", "save"),
		filepath.Join(gameDir, "deep", "one", "saves"),
		filepath.Join(gameDir, "savedata"),
	}
	if len(got) != len(want) {
		t.Fatalf("dirs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSyncAndRestore(t *testing.T) {
	dataDir := t.TempDir()
	gameDir := t.TempDir()
	steamRoot := t.TempDir()

	localFile := filepath.Join(gameDir, "savedata", "slot1.sav")
	if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localFile, []byte("local-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cloudDir := filepath.Join(steamRoot, "userdata", "22202", "440", "remote")
	if err := os.MkdirAll(cloudDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cloudFile := filepath.Join(cloudDir, "cloud.sav")
	if err := os.WriteFile(cloudFile, []byte("cloud-v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Sync(Options{
		Root: dataDir,
		Installed: &model.InstalledData{
			SteamRoot: steamRoot,
			Games: []model.App{
				{AppID: 440, Name: "Test Game", Installed: true, Dir: gameDir},
			},
		},
		AppIDs: []int{440},
		Cloud:  true,
		Local:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SteamUser != "22202" {
		t.Fatalf("steam user = %q", result.SteamUser)
	}
	if len(result.Saves) != 1 || len(result.Saves[0].Locations) != 2 {
		t.Fatalf("result = %#v", result)
	}
	backupLocal := filepath.Join(dataDir, "saves", "440", "local", "savedata", "slot1.sav")
	if data, err := os.ReadFile(backupLocal); err != nil || string(data) != "local-v1" {
		t.Fatalf("local backup = %q, %v", data, err)
	}
	backupCloud := filepath.Join(dataDir, "saves", "440", "cloud", "cloud.sav")
	if data, err := os.ReadFile(backupCloud); err != nil || string(data) != "cloud-v1" {
		t.Fatalf("cloud backup = %q, %v", data, err)
	}

	manifest := &model.SavesData{}
	UpdateManifest(manifest, result)
	if manifest.Count != 1 || len(manifest.Saves[0].Locations) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.SteamUser != "22202" {
		t.Fatalf("manifest user = %q", manifest.SteamUser)
	}

	// 修改源文件并写回备份，默认只覆盖较旧的目标文件。
	if err := os.WriteFile(backupLocal, []byte("local-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore, err := Restore(manifest, RestoreOptions{Root: dataDir, IDs: []int{440}, Cloud: true, Local: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(restore.Saves) != 1 || len(restore.Saves[0].Locations) != 2 {
		t.Fatalf("restore = %#v", restore)
	}
	if data, err := os.ReadFile(localFile); err != nil || string(data) != "local-v2" {
		t.Fatalf("restored local = %q, %v", data, err)
	}
	if data, err := os.ReadFile(cloudFile); err != nil || string(data) != "cloud-v1" {
		t.Fatalf("restored cloud = %q, %v", data, err)
	}

	removed := Remove(manifest, dataDir, []int{440})
	if removed != 1 || manifest.Count != 0 {
		t.Fatalf("remove = %d, manifest = %#v", removed, manifest)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "saves", "440")); !os.IsNotExist(err) {
		t.Fatalf("backup dir should be removed: %v", err)
	}
}

func TestSyncMirror(t *testing.T) {
	dataDir := t.TempDir()
	gameDir := t.TempDir()
	saveDir := filepath.Join(gameDir, "savedata")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "a.sav"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Root:   dataDir,
		AppIDs: []int{1},
		GameDirs: map[int]string{
			1: gameDir,
		},
		Local:  true,
		Mirror: true,
	}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(saveDir, "a.sav")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "b.sav"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(opts); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "saves", "1", "local", "savedata")
	if _, err := os.Stat(filepath.Join(dir, "a.sav")); !os.IsNotExist(err) {
		t.Fatalf("a.sav should be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.sav")); err != nil {
		t.Fatalf("b.sav should exist: %v", err)
	}
}
