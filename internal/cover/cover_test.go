package cover

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func newTestServer(t *testing.T, files map[string][]byte) string {
	t.Helper()
	mux := http.NewServeMux()
	for path, content := range files {
		data := content
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(data)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFindPrefersJPG(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "440.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "440.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "4400.jpg"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Find(dir, 440)
	if filepath.Base(got) != "440.jpg" {
		t.Fatalf("Find = %q", got)
	}
	if Find(dir, 44) != "" {
		t.Fatal("should not match prefix of another appid")
	}
}

func TestFindAll(t *testing.T) {
	dir := t.TempDir()
	files := []string{"440.png", "440.jpg", "570.webp", "4400.jpg", "readme.txt", "730.header.jpg"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := FindAll(dir)
	if filepath.Base(got[440]) != "440.jpg" {
		t.Fatalf("440 = %q", got[440])
	}
	if filepath.Base(got[570]) != "570.webp" {
		t.Fatalf("570 = %q", got[570])
	}
	if filepath.Base(got[4400]) != "4400.jpg" {
		t.Fatalf("4400 = %q", got[4400])
	}
	if got[730] != "" {
		t.Fatalf("730 should be skipped: %#v", got)
	}
}

func TestRunCopiesLocalCache(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "appcache", "librarycache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(cache, "440_library_600x900.jpg")
	if err := os.WriteFile(src, []byte("local-cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	results, err := Run(context.Background(), Options{
		Apps:      []model.App{{AppID: 440, Name: "TF2"}},
		DestDir:   dest,
		SteamRoot: root,
		Source:    "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "local" {
		t.Fatalf("results = %#v", results)
	}
	got, err := os.ReadFile(filepath.Join(dest, "440.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "local-cover" {
		t.Fatalf("cover = %q", got)
	}
}

func TestRunDownloadsFromCDN(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 512)
	server := newTestServer(t, map[string][]byte{
		"/steam/apps/220/header.jpg": payload,
	})
	dest := t.TempDir()
	results, err := Run(context.Background(), Options{
		Apps:    []model.App{{AppID: 220, Name: "HL2"}},
		DestDir: dest,
		CDNBase: server,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "cdn" {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Size != int64(len(payload)) {
		t.Fatalf("size = %d", results[0].Size)
	}
	got, err := os.ReadFile(filepath.Join(dest, "220.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("downloaded cover mismatch")
	}
}

func TestRunRetriesAndReportsProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("y"), 512)
	var mu sync.Mutex
	attempts := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := attempts[r.URL.Path]
		attempts[r.URL.Path] = n + 1
		mu.Unlock()
		if n == 0 {
			http.Error(w, "temporary failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	dest := t.TempDir()
	var (
		progressMu sync.Mutex
		progress   []int
	)
	results, err := Run(context.Background(), Options{
		Apps:    []model.App{{AppID: 220, Name: "HL2"}},
		DestDir: dest,
		Style:   "portrait",
		Retries: 1,
		CDNBase: server.URL,
		Progress: func(done, total int, r Result) {
			progressMu.Lock()
			progress = append(progress, done)
			progressMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Source != "cdn" {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Retries != 1 {
		t.Fatalf("retries = %d", results[0].Retries)
	}
	if len(progress) != 1 || progress[0] != 1 {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestRunSkipsExistingUnlessForced(t *testing.T) {
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "100.jpg"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := Run(context.Background(), Options{
		Apps:    []model.App{{AppID: 100}},
		DestDir: dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Source != "existing" {
		t.Fatalf("source = %q", results[0].Source)
	}
}

func TestRunRejectsUnknownOptions(t *testing.T) {
	if _, err := Run(context.Background(), Options{DestDir: t.TempDir(), Style: "bogus"}); err == nil {
		t.Fatal("expected style error")
	}
	if _, err := Run(context.Background(), Options{DestDir: t.TempDir(), Source: "bogus"}); err == nil {
		t.Fatal("expected source error")
	}
}
