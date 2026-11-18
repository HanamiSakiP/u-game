package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/store"
)

func TestMain(m *testing.M) {
	// 测试中不访问 Steam 网络接口。
	_ = os.Setenv("UGAL_OFFLINE", "1")
	os.Exit(m.Run())
}

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

func TestRunConfigSteamIDKeepsRawIdentifier(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "config", "steamid", "22202"}, &out, &errBuf); err != nil {
		t.Fatalf("set config: %v", err)
	}
	st, _ := store.Open(dir)
	cfg, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SteamID != "22202" {
		t.Fatalf("steamid = %q, want raw identifier", cfg.SteamID)
	}
	if !strings.Contains(out.String(), "22202") {
		t.Fatalf("output should show raw identifier: %q", out.String())
	}

	// 资料 URL 只提取标识，仍不做 SteamID64 转换。
	if err := Run([]string{"--data-dir", dir, "config", "steamid", "https://steamcommunity.com/id/gabelogannewell"}, &out, &errBuf); err != nil {
		t.Fatalf("set config from url: %v", err)
	}
	cfg, err = st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SteamID != "gabelogannewell" {
		t.Fatalf("steamid = %q, want vanity", cfg.SteamID)
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

func TestRunFavListGroupedByNote(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFavorites(&model.FavoritesData{Games: []model.Favorite{
		{AppID: 900003, Name: "Zeta", Note: "工具"},
		{AppID: 900001, Name: "Alpha"},
		{AppID: 900002, Name: "Beta", Note: "工具"},
		{AppID: 900004, Name: "Delta", Note: "AAA"},
		{AppID: 900005, Name: "Epsilon", Note: "工具"},
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list: %v (stderr %s)", err, errBuf.String())
	}
	text := out.String()
	if strings.Contains(text, "添加时间") {
		t.Fatalf("fav list should not show added time:\n%s", text)
	}
	if !strings.Contains(text, "备注") || !strings.Contains(text, "无分组") {
		t.Fatalf("fav list should show note column and default group:\n%s", text)
	}
	for _, want := range []string{"AAA（1 个）", "工具（3 个）", "无分组（1 个）"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fav list missing group header %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("non-terminal output should not contain ANSI colors:\n%q", text)
	}

	var order []string
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || len(fields[0]) != 6 || !strings.HasPrefix(fields[0], "9") {
			continue
		}
		order = append(order, fields[0])
	}
	want := []string{"900004", "900002", "900005", "900003", "900001"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("grouped order = %v, want %v\n%s", order, want, text)
	}
}

func TestRunFavListGroupsByPublisher(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFavorites(&model.FavoritesData{Games: []model.Favorite{
		{AppID: 910001, Name: "A"},
		{AppID: 910002, Name: "B"},
		{AppID: 910003, Name: "C", Note: "我的分组"},
		{AppID: 910004, Name: "D"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePublishers(&model.PublishersData{Publishers: map[string]string{
		"910001": "Valve",
		"910002": "Sega",
		"910004": "Valve",
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list: %v (stderr %s)", err, errBuf.String())
	}
	text := out.String()
	if !strings.Contains(text, "备注/发行商") {
		t.Fatalf("fav list should show note/publisher column:\n%s", text)
	}
	if !strings.Contains(text, "Sega") || !strings.Contains(text, "Valve") || !strings.Contains(text, "我的分组") {
		t.Fatalf("fav list should show publisher groups:\n%s", text)
	}
	for _, want := range []string{"Sega（1 个）", "Valve（2 个）", "我的分组（1 个）"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fav list missing group header %q:\n%s", want, text)
		}
	}

	var order []string
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || len(fields[0]) != 6 || !strings.HasPrefix(fields[0], "9") {
			continue
		}
		order = append(order, fields[0])
	}
	want := []string{"910002", "910001", "910004", "910003"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("publisher grouped order = %v, want %v\n%s", order, want, text)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list json: %v", err)
	}
	var payload struct {
		Games []favoriteRow `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	publishers := make(map[int]string)
	for _, row := range payload.Games {
		publishers[row.AppID] = row.Publisher
	}
	if publishers[910001] != "Valve" || publishers[910002] != "Sega" || publishers[910003] != "" || publishers[910004] != "Valve" {
		t.Fatalf("json publishers = %#v", publishers)
	}
}

func TestRunFavListPublisherGroupOverrides(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFavorites(&model.FavoritesData{Games: []model.Favorite{
		{AppID: 930001, Name: "A"},
		{AppID: 930002, Name: "B"},
		{AppID: 930003, Name: "C"},
		{AppID: 930004, Name: "D"},
		{AppID: 930005, Name: "E"},
		{AppID: 930006, Name: "F"},
		{AppID: 930007, Name: "G", Note: "我的备注"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePublishers(&model.PublishersData{Publishers: map[string]string{
		"930001": "HIKARI FIELD / NekoNyan Ltd.",
		"930002": "NekoNyan Ltd. / HIKARI FIELD",
		"930003": "TamaMako gaming / Sekai Project",
		"930004": "Kagura Games / NekoNyan Ltd.",
		"930005": "NekoNyan Ltd.",
		"930006": "TamaMako Gaming",
		"930007": "NekoNyan Ltd.",
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "list"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list: %v (stderr %s)", err, errBuf.String())
	}
	text := out.String()
	for _, want := range []string{
		"HIKARI FIELD（2 个）",
		"NekoNyan Ltd.（2 个）",
		"Sekai Project（1 个）",
		"TamaMako Gaming（1 个）",
		"我的备注（1 个）",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("fav list missing group %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		"HIKARI FIELD / NekoNyan Ltd.（",
		"NekoNyan Ltd. / HIKARI FIELD（",
		"TamaMako gaming / Sekai Project（",
		"Kagura Games / NekoNyan Ltd.（",
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("fav list should not contain combined group %q:\n%s", unwanted, text)
		}
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list json: %v", err)
	}
	var payload struct {
		Games []favoriteRow `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	wantPublishers := map[int]string{
		930001: "HIKARI FIELD",
		930002: "HIKARI FIELD",
		930003: "Sekai Project",
		930004: "NekoNyan Ltd.",
		930005: "NekoNyan Ltd.",
		930006: "TamaMako Gaming",
		930007: "NekoNyan Ltd.",
	}
	for _, row := range payload.Games {
		if row.Publisher != wantPublishers[row.AppID] {
			t.Fatalf("json publisher for %d = %q, want %q", row.AppID, row.Publisher, wantPublishers[row.AppID])
		}
	}
}

func TestGroupPublisher(t *testing.T) {
	cases := map[string]string{
		"HIKARI FIELD / NekoNyan Ltd.":    "HIKARI FIELD",
		"NekoNyan Ltd. / HIKARI FIELD":    "HIKARI FIELD",
		"TamaMako gaming / Sekai Project": "Sekai Project",
		"Sekai Project / TamaMako gaming": "Sekai Project",
		"Kagura Games / NekoNyan Ltd.":    "NekoNyan Ltd.",
		"NekoNyan Ltd. / Kagura Games":    "NekoNyan Ltd.",
		"TamaMako Gaming":                 "TamaMako Gaming",
		"HIKARI FIELD":                    "HIKARI FIELD",
		"NekoNyan Ltd.":                   "NekoNyan Ltd.",
		"":                                "",
	}
	for in, want := range cases {
		if got := groupPublisher(in); got != want {
			t.Errorf("groupPublisher(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunCreatesPatchesFile(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "config", "steamid", "76561197960287930"}, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st, _ := store.Open(dir)
	data, err := st.LoadPatches()
	if err != nil {
		t.Fatalf("patches.json not created: %v", err)
	}
	if data.Count != len(data.Patches) {
		t.Fatalf("patches = %#v", data)
	}
	if !strings.Contains(errBuf.String(), "已创建补丁记录文件") {
		t.Fatalf("missing creation notice:\n%s", errBuf.String())
	}

	// 已有文件时不应覆盖用户编辑的内容。
	if err := os.WriteFile(st.PatchesPath(), []byte(`{"version":1,"count":0,"patches":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errBuf.Reset()
	if err := Run([]string{"--data-dir", dir, "config", "steamid", "76561197960287930"}, &out, &errBuf); err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if strings.Contains(errBuf.String(), "已创建补丁记录文件") {
		t.Fatalf("existing patches.json should not be recreated:\n%s", errBuf.String())
	}
}

func TestRunFavImportFromPatchesDefault(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOwned(&model.OwnedData{Games: []model.OwnedGame{
		{AppID: 440, Name: "Team Fortress 2"},
		{AppID: 220, Name: "Half-Life 2"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "HD Patch", Applied: true},
		{AppID: 220, Name: "patch.xp3"},
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "import"}, &out, &errBuf); err != nil {
		t.Fatalf("fav import: %v (stderr %s)", err, errBuf.String())
	}
	fav, err := st.LoadFavorites()
	if err != nil {
		t.Fatal(err)
	}
	if fav.Count != 2 {
		t.Fatalf("favorites = %#v", fav)
	}
	names := make(map[int]string, len(fav.Games))
	for _, g := range fav.Games {
		names[g.AppID] = g.Name
	}
	if names[440] != "Team Fortress 2" || names[220] != "Half-Life 2" {
		t.Fatalf("names = %#v", names)
	}

	// import-patches 与 --patches 行为一致。
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "remove", "--all"}, &out, &errBuf); err != nil {
		t.Fatalf("fav remove: %v", err)
	}
	if err := Run([]string{"--data-dir", dir, "fav", "import-patches"}, &out, &errBuf); err != nil {
		t.Fatalf("fav import-patches: %v", err)
	}
	if fav, _ = st.LoadFavorites(); fav.Count != 2 {
		t.Fatalf("after import-patches = %#v", fav)
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
	// 测试从空补丁记录开始，避免内置默认记录影响计数。
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{}}); err != nil {
		t.Fatal(err)
	}
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
	// --file 默认使用 MD5。
	const wantHash = "5d41402abc4b2a76b9719d911017c592"
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

func TestRunPatchMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{}}); err != nil {
		t.Fatal(err)
	}
	patch1 := filepath.Join(dir, "patch1.bin")
	patch2 := filepath.Join(dir, "patch2.bin")
	if err := os.WriteFile(patch1, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patch2, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--file", patch1, "--file", patch2, "--applied"}, &out, &errBuf); err != nil {
		t.Fatalf("patch set multiple files: %v (stderr %s)", err, errBuf.String())
	}
	loaded, err := st.LoadPatches()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Count != 2 {
		t.Fatalf("patches = %#v", loaded)
	}
	byName := make(map[string]model.Patch)
	for _, p := range loaded.Patches {
		byName[p.Name] = p
		if !p.Applied {
			t.Fatalf("patch should be applied: %#v", p)
		}
	}
	if _, ok := byName["patch1.bin"]; !ok {
		t.Fatalf("missing patch1.bin: %#v", loaded.Patches)
	}
	if _, ok := byName["patch2.bin"]; !ok {
		t.Fatalf("missing patch2.bin: %#v", loaded.Patches)
	}

	// 不带 --name 时作用于该 appid 的全部补丁。
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--not-applied"}, &out, &errBuf); err != nil {
		t.Fatalf("patch unset all: %v", err)
	}
	loaded, _ = st.LoadPatches()
	for _, p := range loaded.Patches {
		if p.Applied {
			t.Fatalf("patch should not be applied: %#v", p)
		}
	}

	// 带 --name 时只更新指定补丁。
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--name", "patch1.bin", "--applied"}, &out, &errBuf); err != nil {
		t.Fatalf("patch set one: %v", err)
	}
	loaded, _ = st.LoadPatches()
	for _, p := range loaded.Patches {
		want := p.Name == "patch1.bin"
		if p.Applied != want {
			t.Fatalf("patch %#v applied = %v, want %v", p, p.Applied, want)
		}
	}

	// 多条记录时，只给 --hash 无法确定更新哪一条。
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--hash", "abc"}, &out, &errBuf); err == nil {
		t.Fatal("expected error for ambiguous --hash")
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "list", "440", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("patch list: %v", err)
	}
	var payload struct {
		Total   int        `json:"total"`
		Patches []patchRow `json:"patches"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Total != 2 || len(payload.Patches) != 2 {
		t.Fatalf("patch list = %#v", payload)
	}

	// 只删除指定名称的补丁。
	if err := Run([]string{"--data-dir", dir, "patch", "remove", "440", "--name", "patch1.bin"}, &out, &errBuf); err != nil {
		t.Fatalf("patch remove name: %v", err)
	}
	loaded, _ = st.LoadPatches()
	if loaded.Count != 1 || loaded.Patches[0].Name != "patch2.bin" {
		t.Fatalf("patches after remove = %#v", loaded)
	}
	if err := Run([]string{"--data-dir", dir, "patch", "remove", "440"}, &out, &errBuf); err != nil {
		t.Fatalf("patch remove appid: %v", err)
	}
	if loaded, _ = st.LoadPatches(); loaded.Count != 0 {
		t.Fatalf("patches after remove all = %#v", loaded)
	}
}

func TestRunPatchHashes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{}}); err != nil {
		t.Fatal(err)
	}
	patchFile := filepath.Join(dir, "patch.bin")
	if err := os.WriteFile(patchFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	const wantMD5 = "5d41402abc4b2a76b9719d911017c592"
	const wantSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	var out, errBuf bytes.Buffer
	// 默认算法是 MD5。
	if err := Run([]string{"--data-dir", dir, "patch", "hash", "--json", patchFile}, &out, &errBuf); err != nil {
		t.Fatalf("patch hash: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		Algorithm string `json:"algorithm"`
		SHA256    string `json:"sha256"`
		MD5       string `json:"md5"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Algorithm != "md5" || payload.MD5 != wantMD5 || payload.SHA256 != "" {
		t.Fatalf("md5 payload = %#v", payload)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "hash", "--sha256", "--json", patchFile}, &out, &errBuf); err != nil {
		t.Fatalf("patch hash --sha256: %v (stderr %s)", err, errBuf.String())
	}
	payload = struct {
		Algorithm string `json:"algorithm"`
		SHA256    string `json:"sha256"`
		MD5       string `json:"md5"`
	}{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Algorithm != "sha256" || payload.SHA256 != wantSHA256 || payload.MD5 != "" {
		t.Fatalf("sha256 payload = %#v", payload)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "md5", patchFile}, &out, &errBuf); err != nil {
		t.Fatalf("patch md5: %v (stderr %s)", err, errBuf.String())
	}
	if got := strings.TrimSpace(out.String()); got != wantMD5 {
		t.Fatalf("patch md5 output = %q", got)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--file", patchFile, "--md5"}, &out, &errBuf); err != nil {
		t.Fatalf("patch set --md5: %v (stderr %s)", err, errBuf.String())
	}
	loaded, err := st.LoadPatches()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Count != 1 || loaded.Patches[0].Hash != wantMD5 || loaded.Patches[0].Name != "patch.bin" {
		t.Fatalf("patches = %#v", loaded)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--file", patchFile, "--sha256"}, &out, &errBuf); err != nil {
		t.Fatalf("patch set --sha256: %v (stderr %s)", err, errBuf.String())
	}
	loaded, _ = st.LoadPatches()
	if loaded.Count != 1 || loaded.Patches[0].Hash != wantSHA256 {
		t.Fatalf("patches after --sha256 = %#v", loaded)
	}

	if err := Run([]string{"--data-dir", dir, "patch", "set", "440", "--md5"}, &out, &errBuf); err == nil {
		t.Fatal("expected error for --md5 without --file")
	}
	if err := Run([]string{"--data-dir", dir, "patch", "hash", "--md5", "--sha256", patchFile}, &out, &errBuf); err == nil {
		t.Fatal("expected error for --md5 with --sha256")
	}
}

func TestRunPatchCheck(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	patchDir := st.PatchesDir()
	if err := os.MkdirAll(filepath.Join(patchDir, "440"), 0o755); err != nil {
		t.Fatal(err)
	}
	aPath := filepath.Join(patchDir, "440", "a.bin")
	if err := os.WriteFile(aPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "b.bin"), []byte("wrong"), 0o644); err != nil {
		t.Fatal(err)
	}

	const md5Hello = "5d41402abc4b2a76b9719d911017c592"
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "a.bin", Hash: md5Hello},
		{AppID: 440, Name: "b.bin", Hash: md5Hello},
		{AppID: 220, Name: "c.bin", Hash: md5Hello},
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "check", "--json", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch check: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		Directory  string          `json:"directory"`
		Total      int             `json:"total"`
		OK         int             `json:"ok"`
		Missing    int             `json:"missing"`
		Mismatch   int             `json:"mismatch"`
		Unverified int             `json:"unverified"`
		Patches    []patchCheckRow `json:"patches"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Directory != patchDir {
		t.Fatalf("directory = %q, want %q", payload.Directory, patchDir)
	}
	if payload.Total != 3 || payload.OK != 1 || payload.Missing != 1 || payload.Mismatch != 1 || payload.Unverified != 0 {
		t.Fatalf("payload = %#v", payload)
	}
	byName := make(map[string]patchCheckRow)
	for _, row := range payload.Patches {
		byName[row.Name] = row
	}
	if row := byName["a.bin"]; row.Status != "ok" || row.Path != aPath || row.ActualHash != md5Hello {
		t.Fatalf("a.bin = %#v", row)
	}
	if row := byName["b.bin"]; row.Status != "mismatch" || row.ActualHash == md5Hello {
		t.Fatalf("b.bin = %#v", row)
	}
	if row := byName["c.bin"]; row.Status != "missing" || row.Path != "" {
		t.Fatalf("c.bin = %#v", row)
	}

	// --dir 可以改用其他目录。
	customDir := filepath.Join(dir, "custom")
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "check", "--dir", customDir, "--json", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch check --dir: %v", err)
	}
	payload = struct {
		Directory  string          `json:"directory"`
		Total      int             `json:"total"`
		OK         int             `json:"ok"`
		Missing    int             `json:"missing"`
		Mismatch   int             `json:"mismatch"`
		Unverified int             `json:"unverified"`
		Patches    []patchCheckRow `json:"patches"`
	}{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Directory != customDir || payload.Total != 3 || payload.Missing != 3 {
		t.Fatalf("custom dir payload = %#v", payload)
	}
	if fi, err := os.Stat(customDir); err != nil || !fi.IsDir() {
		t.Fatalf("custom dir should be created: %v", err)
	}

	// 可以按 appid 过滤。
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "check", "440", "--json", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch check appid: %v", err)
	}
	payload = struct {
		Directory  string          `json:"directory"`
		Total      int             `json:"total"`
		OK         int             `json:"ok"`
		Missing    int             `json:"missing"`
		Mismatch   int             `json:"mismatch"`
		Unverified int             `json:"unverified"`
		Patches    []patchCheckRow `json:"patches"`
	}{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if payload.Total != 2 || payload.OK != 1 || payload.Mismatch != 1 {
		t.Fatalf("filtered payload = %#v", payload)
	}

	// 文本输出包含目录和统计。
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "check", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch check text: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, patchDir) || !strings.Contains(text, "已找到 1") ||
		!strings.Contains(text, "未找到 1") || !strings.Contains(text, "哈希错误 1") {
		t.Fatalf("text output:\n%s", text)
	}
	for _, want := range []string{"APPID", "状态", "游戏", "补丁名称", "哈希", "a.bin", "符合", "不符合"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output missing %q:\n%s", want, text)
		}
	}
	// 默认不展开未找到的补丁，只汇总一行。
	if strings.Contains(text, "补丁 c.bin") {
		t.Fatalf("missing detail should be hidden:\n%s", text)
	}
	if !strings.Contains(text, "未找到 1 个补丁（使用 --all 查看明细）") {
		t.Fatalf("missing summary:\n%s", text)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "check", "--all", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch check --all: %v", err)
	}
	if !strings.Contains(out.String(), "c.bin") ||
		!strings.Contains(out.String(), "共 3 条补丁：已找到 1，未找到 1，哈希错误 1") {
		t.Fatalf("--all output:\n%s", out.String())
	}

	// 默认目录不存在时会自动创建。
	if err := os.RemoveAll(patchDir); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "patch", "check", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch check create dir: %v", err)
	}
	if fi, err := os.Stat(patchDir); err != nil || !fi.IsDir() {
		t.Fatalf("patch dir should be created: %v", err)
	}
	if !strings.Contains(out.String(), "已自动创建补丁目录") {
		t.Fatalf("text output:\n%s", out.String())
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

func TestRunCookieCommands(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer

	if err := Run([]string{"--data-dir", dir, "cookie"}, &out, &errBuf); err != nil {
		t.Fatalf("cookie status: %v", err)
	}
	if !strings.Contains(out.String(), "未找到") {
		t.Fatalf("status should report missing cookie:\n%s", out.String())
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "cookie", "path"}, &out, &errBuf); err != nil {
		t.Fatalf("cookie path: %v", err)
	}
	cookiePath := filepath.Join(dir, "cookie.txt")
	if got := strings.TrimSpace(out.String()); got != cookiePath {
		t.Fatalf("cookie path = %q, want %q", got, cookiePath)
	}

	if err := Run([]string{"--data-dir", dir, "cookie", "show"}, &out, &errBuf); err == nil {
		t.Fatal("cookie show should fail when no cookie is saved")
	}

	if err := os.WriteFile(cookiePath, []byte("cf_clearance=abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run([]string{"--data-dir", dir, "cookie", "show"}, &out, &errBuf); err != nil {
		t.Fatalf("cookie show: %v", err)
	}
	if !strings.Contains(out.String(), "cf_clearance=abc") {
		t.Fatalf("cookie show output = %q", out.String())
	}

	if err := Run([]string{"--data-dir", dir, "cookie", "clear"}, &out, &errBuf); err != nil {
		t.Fatalf("cookie clear: %v", err)
	}
	if _, err := os.Stat(cookiePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cookie file should be removed, stat err = %v", err)
	}
}

func TestRunCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		var out, errBuf bytes.Buffer
		if err := Run([]string{"completion", shell, "--print"}, &out, &errBuf); err != nil {
			t.Fatalf("completion %s: %v", shell, err)
		}
		text := out.String()
		for _, want := range []string{"ugal", "sync", "cookie", "completion"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s completion missing %q", shell, want)
			}
		}
	}
	if err := Run([]string{"completion", "bogus"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown shell should fail")
	}
}

func TestRunCompletionAutoDetect(t *testing.T) {
	t.Setenv("UGAL_SHELL", "zsh")
	var out, errBuf bytes.Buffer
	if err := Run([]string{"completion", "--print"}, &out, &errBuf); err != nil {
		t.Fatalf("auto completion: %v", err)
	}
	if !strings.Contains(out.String(), "#compdef ugal") {
		t.Fatalf("expected zsh completion, got:\n%s", out.String())
	}

	t.Setenv("UGAL_SHELL", "bash")
	out.Reset()
	if err := Run([]string{"completion", "auto", "--print"}, &out, &errBuf); err != nil {
		t.Fatalf("completion auto: %v", err)
	}
	if !strings.Contains(out.String(), "_ugal_complete") {
		t.Fatalf("expected bash completion, got:\n%s", out.String())
	}
}

func TestRunCompletionInstall(t *testing.T) {
	t.Setenv("UGAL_SHELL", "zsh")
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	if err := Run([]string{"completion", "--dir", dir}, &out, &errBuf); err != nil {
		t.Fatalf("completion install: %v", err)
	}
	path := filepath.Join(dir, "_ugal")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed completion: %v", err)
	}
	if !strings.Contains(string(data), "#compdef ugal") {
		t.Fatalf("unexpected zsh completion file:\n%s", data)
	}
	if !strings.Contains(out.String(), path) {
		t.Fatalf("install output should mention %s, got %q", path, out.String())
	}

	// 重复安装相同内容时不应报错或改变文件。
	out.Reset()
	if err := Run([]string{"completion", "zsh", "--dir", dir}, &out, &errBuf); err != nil {
		t.Fatalf("repeat completion install: %v", err)
	}
	if !strings.Contains(out.String(), "已是最新") {
		t.Fatalf("repeat install output = %q", out.String())
	}

	// --print 不写入任何文件。
	printDir := t.TempDir()
	out.Reset()
	if err := Run([]string{"completion", "bash", "--print", "--dir", printDir}, &out, &errBuf); err != nil {
		t.Fatalf("completion --print: %v", err)
	}
	if !strings.Contains(out.String(), "_ugal_complete") {
		t.Fatalf("expected bash completion on stdout, got:\n%s", out.String())
	}
	if entries, err := os.ReadDir(printDir); err != nil || len(entries) != 0 {
		t.Fatalf("--print should not write files, entries = %v, err = %v", entries, err)
	}
}

func TestRunCompletionPowerShellProfileBlock(t *testing.T) {
	t.Setenv("UGAL_SHELL", "powershell")
	dir := t.TempDir()
	profile := filepath.Join(dir, "ugal.ps1")
	if err := os.WriteFile(profile, []byte("# existing profile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Run([]string{"completion", "--dir", dir}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("powershell completion install: %v", err)
		}
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# existing profile") {
		t.Fatal("existing profile content should be preserved")
	}
	if got := strings.Count(text, powershellBlockStart); got != 1 {
		t.Fatalf("expected one completion block, got %d:\n%s", got, text)
	}
	if !strings.Contains(text, "Register-ArgumentCompleter") {
		t.Fatalf("missing completer in profile:\n%s", text)
	}
}

func TestRunCompletionUnknownOverride(t *testing.T) {
	t.Setenv("UGAL_SHELL", "tcsh")
	err := Run([]string{"completion"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "UGAL_SHELL") {
		t.Fatalf("expected UGAL_SHELL error, got %v", err)
	}
}

func TestNormalizeShell(t *testing.T) {
	cases := map[string]string{
		"bash":            "bash",
		"/bin/zsh":        "zsh",
		"-fish":           "fish",
		`C:\bin\pwsh.exe`: "powershell",
		"powershell":      "powershell",
	}
	for in, want := range cases {
		got, ok := normalizeShell(in)
		if !ok || got != want {
			t.Errorf("normalizeShell(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := normalizeShell("tcsh"); ok {
		t.Error("tcsh should not be supported")
	}
}

func TestRunSyncBrowserRequiresSteamDBSource(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	err := Run([]string{"--data-dir", dir, "sync", "76561198000000000", "--source", "community", "--browser"}, &out, &errBuf)
	if err == nil {
		t.Fatal("--browser 与非 SteamDB 来源一起使用应报错")
	}
	if !strings.Contains(err.Error(), "只适用于 SteamDB") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSyncCommunityKeepsRawID(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	// --browser 与 community 一起使用会在联网前报错，因此可以只检查转换提示。
	err := Run([]string{"--data-dir", dir, "sync", "22202", "--source", "community", "--browser"}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "只适用于 SteamDB") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(errBuf.String(), "已转换为 SteamID64") {
		t.Fatalf("community 来源不应转换 SteamID64: %s", errBuf.String())
	}
}

func TestRunUpdateDefaultSourceDir(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dataDir, "config", "update-dir"}, &out, &errBuf); err != nil {
		t.Fatalf("config update-dir: %v", err)
	}
	want := filepath.Join(dataDir, "src")
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

func TestRunPatchListUsesCachedAppNames(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 指向不存在的 Steam 目录，避免测试机器上的 appinfo.vdf 干扰结果。
	if err := st.SaveConfig(&model.Config{SteamDir: filepath.Join(dir, "no-steam")}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{
		{AppID: 575480, Name: "patch.rar"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAppNames(&model.AppNamesData{Names: map[string]string{
		"575480": "Real Game Name",
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("patch list: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		Patches []patchRow `json:"patches"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(payload.Patches) != 1 || payload.Patches[0].Game != "Real Game Name" {
		t.Fatalf("patches = %#v", payload.Patches)
	}
}

func TestRunListMergesDuplicateInstalledApps(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInstalled(&model.InstalledData{
		Games: []model.App{
			{AppID: 100, Type: "game", Installed: true, Library: "/a"},
			{AppID: 100, Name: "Named Game", Type: "game", Installed: true, Library: "/b"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "list", "--json"}, &out, &errBuf); err != nil {
		t.Fatalf("list: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		Games []entry `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(payload.Games) != 1 {
		t.Fatalf("games = %#v, want 1 merged entry", payload.Games)
	}
	if payload.Games[0].Name != "Named Game" {
		t.Fatalf("name = %q", payload.Games[0].Name)
	}
}

func TestDisplayWidth(t *testing.T) {
	if got := displayWidth("abc"); got != 3 {
		t.Fatalf("ascii width = %d", got)
	}
	if got := displayWidth("游戏"); got != 4 {
		t.Fatalf("cjk width = %d", got)
	}
	if got := displayWidth("\x1b[31m游戏\x1b[0m"); got != 4 {
		t.Fatalf("ansi width = %d", got)
	}
	if got := truncateDisplay("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate = %q", got)
	}
}

func TestRunPatchListSummary(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{
		Version: model.Version,
		Patches: []model.Patch{
			{AppID: 440, Name: "patch1.bin", Applied: false},
			{AppID: 440, Name: "patch2.bin", Applied: true},
			{AppID: 99999999, Name: "patch3.bin", Applied: false},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "patch", "list", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("patch list: %v (stderr %s)", err, errBuf.String())
	}
	text := out.String()
	for _, want := range []string{"共 3 个补丁", "已打补丁 1 个", "未打补丁 2 个", "App 99999999"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("non-terminal output should not contain ANSI colors:\n%q", text)
	}
}

func TestRunPatchListSortsMultiPatchFirst(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SavePatches(&model.PatchesData{
		Version: model.Version,
		Patches: []model.Patch{
			{AppID: 100, Name: "single.bin"},
			{AppID: 200, Name: "a.bin"},
			{AppID: 300, Name: "solo.bin"},
			{AppID: 200, Name: "b.bin"},
			{AppID: 200, Name: "c.bin"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInstalled(&model.InstalledData{
		Games: []model.App{
			{AppID: 100, Type: "game", Installed: true},
			{AppID: 200, Type: "game", Installed: true},
			{AppID: 500, Type: "game", Installed: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	checkOrder := func(args []string, want []int) {
		t.Helper()
		var out, errBuf bytes.Buffer
		if err := Run(append([]string{"--data-dir", dir}, args...), &out, &errBuf); err != nil {
			t.Fatalf("%v: %v (stderr %s)", args, err, errBuf.String())
		}
		var payload struct {
			Patches []patchRow `json:"patches"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("%v: json: %v\n%s", args, err, out.String())
		}
		if len(payload.Patches) != len(want) {
			t.Fatalf("%v: patches = %#v", args, payload.Patches)
		}
		for i, appid := range want {
			if payload.Patches[i].AppID != appid {
				t.Fatalf("%v: patches[%d] = %d, want %d (%#v)", args, i, payload.Patches[i].AppID, appid, payload.Patches)
			}
		}
	}

	// 补丁多的 appid 排前面，数量相同时保持 appid 升序。
	checkOrder([]string{"patch", "list", "--json", "--offline"}, []int{200, 200, 200, 100, 300})
	// --all 模式下无补丁的游戏排在最后。
	checkOrder([]string{"patch", "list", "--all", "--json", "--offline"}, []int{200, 200, 200, 100, 500})
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

func TestRunFavStatus(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "add", "100", "--status", "playing"}, &out, &errBuf); err != nil {
		t.Fatalf("fav add: %v (stderr %s)", err, errBuf.String())
	}
	fav, err := st.LoadFavorites()
	if err != nil {
		t.Fatal(err)
	}
	if fav.Games[0].Status != "在玩" {
		t.Fatalf("status should be normalized: %#v", fav.Games)
	}

	if err := Run([]string{"--data-dir", dir, "fav", "status", "100", "已通关"}, &out, &errBuf); err != nil {
		t.Fatalf("fav status set: %v (stderr %s)", err, errBuf.String())
	}
	if fav, _ = st.LoadFavorites(); fav.Games[0].Status != "已通关" {
		t.Fatalf("status not updated: %#v", fav.Games)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "status", "100"}, &out, &errBuf); err != nil {
		t.Fatalf("fav status show: %v", err)
	}
	if !strings.Contains(out.String(), "已通关") {
		t.Fatalf("status show output = %s", out.String())
	}

	if err := Run([]string{"--data-dir", dir, "fav", "add", "300"}, &out, &errBuf); err != nil {
		t.Fatalf("fav add 300: %v", err)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--json", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list json: %v (stderr %s)", err, errBuf.String())
	}
	var payload struct {
		Games []favoriteRow `json:"games"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	statuses := map[int]string{}
	for _, r := range payload.Games {
		statuses[r.AppID] = r.FavoriteStatus
	}
	if statuses[100] != "已通关" || statuses[300] != "" {
		t.Fatalf("favorite_status = %#v", statuses)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--status", "已通关", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list --status: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "100") || strings.Contains(text, "300") {
		t.Fatalf("status filter output =\n%s", text)
	}
	if !strings.Contains(text, "收藏状态") {
		t.Fatalf("fav list should show status column:\n%s", text)
	}

	out.Reset()
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--status", "none", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list --status none: %v", err)
	}
	if text = out.String(); !strings.Contains(text, "300") || strings.Contains(text, "100") {
		t.Fatalf("untagged filter output =\n%s", text)
	}

	if err := Run([]string{"--data-dir", dir, "fav", "status", "100", "clear"}, &out, &errBuf); err != nil {
		t.Fatalf("fav status clear: %v", err)
	}
	if fav, _ = st.LoadFavorites(); fav.Games[0].Status != "" {
		t.Fatalf("status not cleared: %#v", fav.Games)
	}
}

func TestRunFavListShowsPatchHash(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const hash = "abcdef0123456789abcdef0123456789"
	if err := st.SavePatches(&model.PatchesData{Patches: []model.Patch{
		{AppID: 100, Name: "patch.bin", Hash: hash, Applied: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFavorites(&model.FavoritesData{Games: []model.Favorite{
		{AppID: 100, Name: "Game A", Note: "组"},
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if err := Run([]string{"--data-dir", dir, "fav", "list", "--offline"}, &out, &errBuf); err != nil {
		t.Fatalf("fav list: %v (stderr %s)", err, errBuf.String())
	}
	text := out.String()
	if !strings.Contains(text, "哈希") || !strings.Contains(text, "abcdef01") {
		t.Fatalf("fav list should show shortened patch hash:\n%s", text)
	}
	if !strings.Contains(text, "已打补丁") {
		t.Fatalf("fav list should show patch status:\n%s", text)
	}
}
