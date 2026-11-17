package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/store"
)

func TestRunListJSON(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOwned(&model.OwnedData{
		SteamID:   "76561197960287930",
		Source:    "steamdb",
		FetchedAt: time.Now().UTC(),
		Games: []model.OwnedGame{
			{AppID: 100, Name: "Game A"},
			{AppID: 999, Name: "Missing Game"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInstalled(&model.InstalledData{
		SteamRoot: "/steam",
		Games: []model.App{
			{AppID: 100, Name: "Game A", Type: "game", Installed: true, Size: 100, Library: "/steam"},
			{AppID: 200, Name: "Local Only", Type: "music", Installed: true, Size: 50, Library: "/steam"},
		},
		Count: 2,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		Summary summaryJSON `json:"summary"`
		Total   int         `json:"total"`
		Games   []entry     `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Summary.Owned != 2 || payload.Summary.Installed != 2 ||
		payload.Summary.Missing != 1 || payload.Summary.Extra != 1 {
		t.Fatalf("summary = %#v", payload.Summary)
	}
	if payload.Total != 2 || len(payload.Games) != 2 {
		t.Fatalf("total = %d, games = %d", payload.Total, len(payload.Games))
	}
}

func TestRunMissing(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.SaveOwned(&model.OwnedData{Games: []model.OwnedGame{
		{AppID: 1, Name: "A"},
		{AppID: 2, Name: "B"},
	}})
	_ = st.SaveInstalled(&model.InstalledData{Games: []model.App{
		{AppID: 1, Name: "A", Installed: true, Type: "game"},
	}})

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "missing"}, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "    2  ") {
		t.Fatalf("missing game B not listed:\n%s", text)
	}
	if strings.Contains(text, "    1  ") {
		t.Fatalf("installed game A should be filtered out:\n%s", text)
	}
	if !strings.Contains(text, "已拥有 2 个") {
		t.Fatalf("missing summary:\n%s", text)
	}
}

func TestRunConfig(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "config", "steamid", "76561197960287930"}, &out, &errBuf); err != nil {
		t.Fatalf("set config: %v", err)
	}
	st, _ := store.Open(dir)
	cfg, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SteamID != "76561197960287930" {
		t.Fatalf("steamid = %q", cfg.SteamID)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "config", "steamid"}, &out, &errBuf); err != nil {
		t.Fatalf("get config: %v", err)
	}
	if strings.TrimSpace(out.String()) != "76561197960287930" {
		t.Fatalf("get output = %q", out.String())
	}

	if err := Run([]string{"--data-dir", dir, "config", "source", "bogus"}, &out, &errBuf); err == nil {
		t.Fatal("expected invalid source error")
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := Run([]string{"help"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ugal sync") {
		t.Fatal("help missing usage")
	}
	out.Reset()
	if err := Run([]string{"version"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "ugal ") {
		t.Fatalf("version = %q", out.String())
	}
}

func TestRunScan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "steamapps", "appmanifest_1.acf"),
		`"AppState" { "name" "Solo" "installdir" "Solo" "StateFlags" "4" "SizeOnDisk" "10" }`)
	if err := mkdir(filepath.Join(root, "steamapps", "common", "Solo")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "scan", root, "--include-partial"}, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "已安装      : 1 个") {
		t.Fatalf("unexpected scan output:\n%s", out.String())
	}
	st, _ := store.Open(dir)
	installed, err := st.LoadInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if installed.Count != 1 || installed.SteamRoot != root {
		t.Fatalf("installed = %#v", installed)
	}
}

func TestRunVersionNumber(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := Run([]string{"version"}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "ugal 0.0.1" {
		t.Fatalf("version = %q", out.String())
	}
}

func TestRunSystem(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "system", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var info model.SystemData
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.OS == "" || info.Arch == "" || info.Platform == "" {
		t.Fatalf("system info = %#v", info)
	}
	st, _ := store.Open(dir)
	saved, err := st.LoadSystem()
	if err != nil {
		t.Fatal(err)
	}
	if saved.OS != info.OS {
		t.Fatalf("saved system = %#v", saved)
	}
}

func TestRunFavoritesImportExport(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.SaveOwned(&model.OwnedData{Games: []model.OwnedGame{
		{AppID: 100, Name: "Game A"},
		{AppID: 200, Name: "Game B"},
	}})
	_ = st.SaveInstalled(&model.InstalledData{Games: []model.App{
		{AppID: 100, Name: "Game A", Type: "game", Installed: true},
		{AppID: 300, Name: "Local Only", Type: "game", Installed: true},
	}})

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "add", "100", "300"}, &out, &errBuf); err != nil {
		t.Fatalf("fav add: %v (stderr %s)", err, errBuf.String())
	}
	fav, err := st.LoadFavorites()
	if err != nil {
		t.Fatal(err)
	}
	if fav.Count != 2 || fav.Games[0].Name != "Game A" || fav.Games[1].Name != "Local Only" {
		t.Fatalf("favorites = %#v", fav)
	}

	exportPath := filepath.Join(dir, "export.json")
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "export", exportPath}, &out, &errBuf); err != nil {
		t.Fatalf("fav export: %v", err)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatal(err)
	}

	if err := Run([]string{"--data-dir", dir, "fav", "remove", "100"}, &out, &errBuf); err != nil {
		t.Fatalf("fav remove: %v", err)
	}
	if fav, _ = st.LoadFavorites(); fav.Count != 1 {
		t.Fatalf("after remove = %#v", fav)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "import", exportPath}, &out, &errBuf); err != nil {
		t.Fatalf("fav import: %v", err)
	}
	if fav, _ = st.LoadFavorites(); fav.Count != 2 {
		t.Fatalf("after import = %#v", fav)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "clear"}, &out, &errBuf); err != nil {
		t.Fatalf("fav clear: %v", err)
	}
	if fav, _ = st.LoadFavorites(); fav.Count != 0 {
		t.Fatalf("after clear = %#v", fav)
	}

	if err := Run([]string{"--data-dir", dir, "fav", "import", exportPath, "--replace"}, &out, &errBuf); err != nil {
		t.Fatalf("fav import --replace: %v", err)
	}
	if fav, _ = st.LoadFavorites(); fav.Count != 2 {
		t.Fatalf("after replace = %#v", fav)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list: %v", err)
	}
	var payload struct {
		Total     int           `json:"total"`
		Installed int           `json:"installed"`
		Games     []favoriteRow `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Total != 2 || payload.Installed != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRunCoverFromLocalCache(t *testing.T) {
	steamRoot := t.TempDir()
	cache := filepath.Join(steamRoot, "appcache", "librarycache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "440_library_600x900.jpg"), []byte("cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, _ := store.Open(dir)
	if err := st.SaveInstalled(&model.InstalledData{
		SteamRoot: steamRoot,
		Games: []model.App{
			{AppID: 440, Name: "TF2", Type: "game", Installed: true, Library: steamRoot},
		},
		Count: 1,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "cover", "--source", "local"}, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v (stderr %s)", err, errBuf.String())
	}
	coverPath := filepath.Join(dir, "covers", "440.jpg")
	if _, err := os.Stat(coverPath); err != nil {
		t.Fatalf("cover not created: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "本地缓存    : 1 个") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if !strings.Contains(errBuf.String(), "[1/1]") {
		t.Fatalf("progress not shown:\n%s", errBuf.String())
	}

	out.Reset()
	errBuf.Reset()
	if err := Run([]string{"--data-dir", dir, "cover", "--source", "local"}, &out, &errBuf); err != nil {
		t.Fatalf("cover again: %v", err)
	}
	if !strings.Contains(out.String(), "已存在      : 1 个") {
		t.Fatalf("existing cover should be skipped:\n%s", out.String())
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "cover", "--list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("cover list: %v", err)
	}
	if !strings.Contains(out.String(), `"existing": 1`) {
		t.Fatalf("cover list output:\n%s", out.String())
	}
}

func TestRunPatchStatus(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	if err := st.SaveInstalled(&model.InstalledData{
		SteamRoot: "/steam",
		Games: []model.App{
			{AppID: 440, Name: "TF2", Type: "game", Installed: true},
		},
		Count: 1,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "list", "--all", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("patch list: %v (stderr %s)", err, errBuf.String())
	}
	var listPayload struct {
		Total   int        `json:"total"`
		NoPatch int        `json:"no_patch"`
		Patches []patchRow `json:"patches"`
	}
	if err := json.Unmarshal(out.Bytes(), &listPayload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if listPayload.Total != 1 || listPayload.NoPatch != 1 {
		t.Fatalf("default patch payload = %#v", listPayload)
	}
	if listPayload.Patches[0].Status != "无补丁" {
		t.Fatalf("default patch status = %q", listPayload.Patches[0].Status)
	}

	patchFile := filepath.Join(dir, "patch.bin")
	if err := os.WriteFile(patchFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--name", "HD Patch", "--file", patchFile, "--applied"}, &out, &errBuf); err != nil {
		t.Fatalf("patch set: %v (stderr %s)", err, errBuf.String())
	}
	loaded, err := st.LoadPatches()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Count != 1 || !loaded.Patches[0].Applied || loaded.Patches[0].Name != "HD Patch" {
		t.Fatalf("patches = %#v", loaded)
	}
	const wantHash = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if loaded.Patches[0].Hash != wantHash {
		t.Fatalf("hash = %q", loaded.Patches[0].Hash)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("list: %v", err)
	}
	var list struct {
		Games []entry `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(list.Games) != 1 || list.Games[0].Patch == nil || !list.Games[0].Patch.Applied {
		t.Fatalf("list patch = %#v", list.Games)
	}

	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--not-applied"}, &out, &errBuf); err != nil {
		t.Fatalf("patch unset applied: %v", err)
	}
	loaded, _ = st.LoadPatches()
	if loaded.Patches[0].Applied || loaded.Patches[0].Name != "HD Patch" || loaded.Patches[0].Hash != wantHash {
		t.Fatalf("patch after unset = %#v", loaded.Patches[0])
	}

	if err := Run([]string{"--data-dir", dir, "patch", "remove", "440"}, &out, &errBuf); err != nil {
		t.Fatalf("patch remove: %v", err)
	}
	if loaded, _ = st.LoadPatches(); loaded.Count != 0 {
		t.Fatalf("after remove = %#v", loaded)
	}
}

func TestRunUpdateFlags(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer

	if err := Run([]string{"--data-dir", dir, "update", "--check", "--build-only"}, &out, &errBuf); err == nil {
		t.Fatal("--check 与 --build-only 同时使用应报错")
	}
	if err := Run([]string{"--data-dir", dir, "update", "--method", "bogus"}, &out, &errBuf); err == nil {
		t.Fatal("未知同步方式应报错")
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "config", "update-dir", "~/ugal-src"}, &out, &errBuf); err != nil {
		t.Fatalf("config update-dir: %v", err)
	}
	st, _ := store.Open(dir)
	cfg, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateDir != "~/ugal-src" {
		t.Fatalf("update_dir = %q", cfg.UpdateDir)
	}
}

func TestRunUpdateDefaultSourceDir(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", filepath.Join(dir, "data"), "config", "update-dir"}, &out, &errBuf); err != nil {
		t.Fatalf("config update-dir: %v", err)
	}
	want := filepath.Join(cwd, ".ugal", "src")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("default update dir = %q, want %q", got, want)
	}
}

func TestRunUpdateBuildOnly(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(src, "cmd", "ugal", "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"ugal 9.9.9\") }\n")

	dir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "bin", "ugal")
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "update", "--build-only", "--src-dir", src, "--install", bin}, &out, &errBuf); err != nil {
		t.Fatalf("update: %v (stderr %s)", err, errBuf.String())
	}
	if !strings.Contains(out.String(), "已用本地源码构建") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not created: %v", err)
	}
	st, _ := store.Open(dir)
	state, err := st.LoadUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Built || state.Binary != bin || state.ProgramVersion != "ugal 9.9.9" {
		t.Fatalf("state = %#v", state)
	}
}

func TestDisplayWidth(t *testing.T) {
	if got := displayWidth("abc"); got != 3 {
		t.Fatalf("ascii width = %d", got)
	}
	if got := displayWidth("游戏"); got != 4 {
		t.Fatalf("cjk width = %d", got)
	}
	if got := truncateDisplay("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate = %q", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}
