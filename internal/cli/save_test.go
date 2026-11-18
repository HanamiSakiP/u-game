package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/store"
)

func writeCLIZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunSaveSyncListRestore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
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
	if err := st.SaveInstalled(&model.InstalledData{
		SteamRoot: steamRoot,
		Games: []model.App{
			{AppID: 440, Name: "Test Game", Type: "game", Installed: true, Dir: gameDir},
		},
		Count: 1,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "save", "sync", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("save sync: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		SteamUser string `json:"steam_user"`
		Saves     []struct {
			AppID     int `json:"appid"`
			Locations []struct {
				Kind  string `json:"kind"`
				Files int    `json:"files"`
			} `json:"locations"`
		} `json:"saves"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.SteamUser != "12345" || len(payload.Saves) != 1 || len(payload.Saves[0].Locations) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
	cfg, _ := st.LoadConfig()
	if cfg.SteamUser != "12345" {
		t.Fatalf("SteamUser = %q", cfg.SteamUser)
	}
	data, err := st.LoadSaves()
	if err != nil {
		t.Fatal(err)
	}
	if data.Count != 1 || len(data.Saves[0].Locations) != 2 {
		t.Fatalf("saves.json = %#v", data)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "save", "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("save list: %v", err)
	}
	var list struct {
		Total int              `json:"total"`
		Saves []model.SaveGame `json:"saves"`
	}
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if list.Total != 1 || list.Saves[0].Name != "Test Game" {
		t.Fatalf("list = %#v", list)
	}

	backupLocal := filepath.Join(dir, "saves", "440", "local", "savedata", "slot1.sav")
	if err := os.WriteFile(backupLocal, []byte("local-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "save", "restore", "--all", "--force"}, &out, &errBuf); err != nil {
		t.Fatalf("save restore: %v (stderr %s)", err, errBuf.String())
	}
	if content, err := os.ReadFile(localFile); err != nil || string(content) != "local-v2" {
		t.Fatalf("restored local = %q, %v", content, err)
	}
	if content, err := os.ReadFile(cloudFile); err != nil || string(content) != "cloud-v1" {
		t.Fatalf("restored cloud = %q, %v", content, err)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "save", "remove", "440"}, &out, &errBuf); err != nil {
		t.Fatalf("save remove: %v", err)
	}
	if data, _ := st.LoadSaves(); data.Count != 0 {
		t.Fatalf("after remove = %#v", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "saves", "440")); !os.IsNotExist(err) {
		t.Fatalf("backup should be removed: %v", err)
	}
}

func TestRunSaveSyncLocalOnlyAndDryRun(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	gameDir := t.TempDir()
	saveDir := filepath.Join(gameDir, "save")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "a.sav"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = st.SaveInstalled(&model.InstalledData{Games: []model.App{
		{AppID: 10, Name: "Game", Installed: true, Dir: gameDir},
	}})

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "save", "sync", "10", "--local-only", "--dry-run"}, &out, &errBuf); err != nil {
		t.Fatalf("dry run: %v (stderr %s)", err, errBuf.String())
	}
	if !strings.Contains(out.String(), "预演") {
		t.Fatalf("dry run output:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "saves", "10")); !os.IsNotExist(err) {
		t.Fatalf("dry run should not create backups: %v", err)
	}
	if _, err := os.Stat(st.SavesPath()); !os.IsNotExist(err) {
		t.Fatalf("dry run should not create saves.json: %v", err)
	}
}

func TestRunPatchInstallFile(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gameDir := t.TempDir()
	target := filepath.Join(gameDir, "data", "script.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInstalled(&model.InstalledData{Games: []model.App{
		{AppID: 440, Name: "Test Game", Installed: true, Dir: gameDir},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "patch.zip")
	writeCLIZip(t, archive, map[string]string{"data/script.bin": "new"})

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "install", "440", "--file", archive, "--json", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch install: %v (stderr %s)", err, errBuf.String())
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "new" {
		t.Fatalf("target = %q, %v", content, err)
	}
	records, err := st.LoadPatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Patches) != 1 {
		t.Fatalf("patches = %#v", records)
	}
	got := records.Patches[0]
	if got.Name != "patch.zip" || !got.Applied || got.Hash == "" {
		t.Fatalf("patch record = %#v", got)
	}
	var payload struct {
		Installed int `json:"installed"`
		Patches   []struct {
			Files    int  `json:"files"`
			Replaced int  `json:"replaced"`
			Applied  bool `json:"applied"`
		} `json:"patches"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Installed != 1 || len(payload.Patches) != 1 || payload.Patches[0].Files != 1 || !payload.Patches[0].Applied {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRunPatchInstallRecords(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	gameDir := t.TempDir()
	if err := st.SaveInstalled(&model.InstalledData{Games: []model.App{
		{AppID: 440, Name: "Test Game", Installed: true, Dir: gameDir},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "p.zip"},
	}}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(st.PatchesDir(), "p.zip")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLIZip(t, archive, map[string]string{"file.txt": "patched"})

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "install", "440", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch install: %v (stderr %s)", err, errBuf.String())
	}
	if content, err := os.ReadFile(filepath.Join(gameDir, "file.txt")); err != nil || string(content) != "patched" {
		t.Fatalf("installed file = %q, %v", content, err)
	}
	records, _ := st.LoadPatches()
	if len(records.Patches) != 1 || !records.Patches[0].Applied {
		t.Fatalf("records = %#v", records)
	}
}
