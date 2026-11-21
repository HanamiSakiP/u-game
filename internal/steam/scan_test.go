package steam

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanLibraries(t *testing.T) {
	root := t.TempDir()
	lib2 := t.TempDir()

	mustMkdir(t, filepath.Join(root, "steamapps", "common", "Game A"))
	mustMkdir(t, filepath.Join(lib2, "steamapps", "music", "My OST"))

	writeFile(t, filepath.Join(root, "steamapps", "libraryfolders.vdf"), `"libraryfolders"
{
	"0"
	{
		"path"		"`+slash(root)+`"
		"label"		""
		"apps"
		{
			"100"		"111"
			"101"		"222"
			"102"		"444"
		}
	}
	"1"
	{
		"path"		"`+slash(lib2)+`"
		"mounted"	"1"
		"apps"
		{
			"200"		"333"
		}
	}
}
`)

	writeFile(t, filepath.Join(root, "steamapps", "appmanifest_100.acf"), `"AppState"
{
	"appid"		"100"
	"name"		"Game A"
	"installdir"	"Game A"
	"StateFlags"	"4"
	"DownloadType"	"3"
	"SizeOnDisk"	"111"
	"buildid"	"12345"
}
`)
	writeFile(t, filepath.Join(root, "steamapps", "appmanifest_101.acf"), `"AppState"
{
	"appid"		"101"
	"name"		"Partial Game"
	"installdir"	"Partial Game"
	"StateFlags"	"1026"
	"DownloadType"	"3"
	"SizeOnDisk"	"222"
}
`)
	writeFile(t, filepath.Join(lib2, "steamapps", "appmanifest_200.acf"), `"AppState"
{
	"appid"		"200"
	"name"		"My OST"
	"installdir"	"My OST"
	"StateFlags"	"4"
	"DownloadType"	"1"
	"SizeOnDisk"	"333"
}
`)

	res, err := Scan(Options{SteamDir: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Libraries) != 2 {
		t.Fatalf("libraries = %d, want 2", len(res.Libraries))
	}
	if res.Count != 3 || res.GameCount != 2 || res.MusicCount != 1 {
		t.Fatalf("counts = %d/%d/%d, want 3/2/1", res.Count, res.GameCount, res.MusicCount)
	}
	if res.TotalSize != 111+444+333 {
		t.Fatalf("total size = %d", res.TotalSize)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("skipped = %v, want partial game", res.Skipped)
	}

	var music, missingManifest bool
	for _, app := range res.Games {
		switch app.AppID {
		case 200:
			music = app.Type == "music" && app.Dir == filepath.Join(lib2, "steamapps", "music", "My OST")
		case 102:
			missingManifest = app.ManifestMissing && app.Installed
		}
	}
	if !music {
		t.Fatal("soundtrack was not classified as music")
	}
	if !missingManifest {
		t.Fatal("app without appmanifest was not kept")
	}

	res2, err := Scan(Options{SteamDir: root, IncludePartial: true})
	if err != nil {
		t.Fatalf("Scan include partial: %v", err)
	}
	if len(res2.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none", res2.Skipped)
	}
	found := false
	for _, app := range res2.Games {
		if app.AppID == 101 {
			found = true
			if app.Installed {
				t.Fatal("partial app should not be marked installed")
			}
		}
	}
	if !found {
		t.Fatal("partial app not included")
	}
}

func TestScanWithoutLibraryFolders(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "steamapps", "common", "Solo"))
	writeFile(t, filepath.Join(root, "steamapps", "appmanifest_1.acf"), `"AppState" { "name" "Solo" "installdir" "Solo" "StateFlags" "4" }`)
	res, err := Scan(Options{SteamDir: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Count != 1 {
		t.Fatalf("count = %d, want 1", res.Count)
	}
}

func TestScanHidesDefaultRuntimeApps(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "steamapps", "common", "Game A"))
	mustMkdir(t, filepath.Join(root, "steamapps", "common", "Proton Experimental"))
	for _, app := range []struct {
		id        int
		name, dir string
	}{
		{100, "Game A", "Game A"},
		{1493710, "Proton Experimental", "Proton Experimental"},
	} {
		writeFile(t, filepath.Join(root, "steamapps", fmt.Sprintf("appmanifest_%d.acf", app.id)),
			fmt.Sprintf(`"AppState" { "name" "%s" "installdir" "%s" "StateFlags" "4" }`, app.name, app.dir))
	}

	res, err := Scan(Options{SteamDir: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Count != 1 || res.GameCount != 1 {
		t.Fatalf("counts = %d/%d, want 1/1", res.Count, res.GameCount)
	}
	if len(res.Hidden) != 1 || !strings.Contains(res.Hidden[0], "Proton Experimental") {
		t.Fatalf("hidden = %v", res.Hidden)
	}
	for _, app := range res.Games {
		if app.AppID == 1493710 {
			t.Fatal("hidden app should not be included")
		}
	}

	res2, err := Scan(Options{SteamDir: root, IncludeHidden: true})
	if err != nil {
		t.Fatalf("Scan include hidden: %v", err)
	}
	if res2.Count != 2 || len(res2.Hidden) != 0 {
		t.Fatalf("include hidden counts = %d, hidden = %v", res2.Count, res2.Hidden)
	}
	for _, app := range res2.Games {
		if app.AppID == 1493710 {
			return
		}
	}
	t.Fatal("hidden app not included with IncludeHidden")
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func slash(p string) string {
	return filepath.ToSlash(p)
}
