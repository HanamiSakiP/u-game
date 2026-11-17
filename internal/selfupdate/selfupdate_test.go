package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testSHA = "8b7d2e0c9a4f1b6d3e5a7c0b2d4f6a8c1e3b5d7f"

func TestParseRepo(t *testing.T) {
	cases := map[string][2]string{
		"github.com/HanamiSakiP/u-game":             {"HanamiSakiP", "u-game"},
		"HanamiSakiP/u-game":                        {"HanamiSakiP", "u-game"},
		"https://github.com/HanamiSakiP/u-game":     {"HanamiSakiP", "u-game"},
		"https://github.com/HanamiSakiP/u-game.git": {"HanamiSakiP", "u-game"},
		"git@github.com:HanamiSakiP/u-game.git":     {"HanamiSakiP", "u-game"},
	}
	for input, want := range cases {
		owner, name, err := ParseRepo(input)
		if err != nil {
			t.Fatalf("ParseRepo(%q): %v", input, err)
		}
		if owner != want[0] || name != want[1] {
			t.Fatalf("ParseRepo(%q) = %q/%q", input, owner, name)
		}
	}
	for _, bad := range []string{"", "u-game", "a/b/c", "../x/y"} {
		if _, _, err := ParseRepo(bad); err == nil {
			t.Fatalf("ParseRepo(%q) should fail", bad)
		}
	}
}

func TestSyncArchiveAndSkipUnchanged(t *testing.T) {
	files := map[string]string{
		"u-game-abc/go.mod":           "module github.com/HanamiSakiP/u-game\n\ngo 1.21\n",
		"u-game-abc/cmd/ugal/main.go": "package main\n\nfunc main() {}\n",
	}
	archive := makeTarGz(t, files)
	var downloads int32

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/commits/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sha":"` + testSHA + `"}`))
	})
	mux.HandleFunc("/owner/repo/tar.gz/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&downloads, 1)
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := filepath.Join(t.TempDir(), "src")
	opts := Options{
		SourceDir:   src,
		Repo:        "owner/repo",
		Branch:      "main",
		Method:      "archive",
		NoBuild:     true,
		APIBase:     srv.URL,
		ArchiveBase: srv.URL,
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Revision != testSHA || res.Method != "archive" {
		t.Fatalf("result = %#v", res)
	}
	if got := readFile(t, filepath.Join(src, "go.mod")); !strings.Contains(got, "module github.com/HanamiSakiP/u-game") {
		t.Fatalf("go.mod = %q", got)
	}
	if got := atomic.LoadInt32(&downloads); got != 1 {
		t.Fatalf("downloads = %d", got)
	}

	// 同一版本再次更新：不再下载，也不标记为已更新。
	opts.PreviousRevision = testSHA
	res, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("second run should be unchanged: %#v", res)
	}
	if got := atomic.LoadInt32(&downloads); got != 1 {
		t.Fatalf("downloads after skip = %d", got)
	}

	// --check 只查询提交，不下载。
	opts.Check = true
	res, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("check should report up to date: %#v", res)
	}
	if got := atomic.LoadInt32(&downloads); got != 1 {
		t.Fatalf("check downloads = %d", got)
	}
}

func TestCheckReportsNewRevision(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/commits/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sha":"` + testSHA + `"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := Run(context.Background(), Options{
		SourceDir:        filepath.Join(t.TempDir(), "src"),
		Repo:             "owner/repo",
		Method:           "archive",
		Check:            true,
		PreviousRevision: "older",
		APIBase:          srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Revision != testSHA {
		t.Fatalf("result = %#v", res)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"../evil.txt": "x"})
	dest := t.TempDir()
	err := extractTarGz(bytes.NewReader(archive), dest)
	if err == nil {
		t.Fatal("expected traversal error")
	}
	if fileExists(filepath.Join(dest, "..", "evil.txt")) {
		t.Fatal("traversal file should not be written")
	}
}

func TestBuildOnlyInstallsBinary(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(src, "cmd", "ugal", "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"demo 9.9.9\") }\n")

	bin := filepath.Join(t.TempDir(), "bin", "ugal")
	res, err := Run(context.Background(), Options{
		SourceDir: src,
		BuildOnly: true,
		Install:   bin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Built || res.Binary != bin {
		t.Fatalf("result = %#v", res)
	}
	if !fileExists(bin) {
		t.Fatalf("binary %s not created", bin)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "demo 9.9.9" {
		t.Fatalf("binary output = %q", out)
	}
}

func TestSyncGitUpdates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	seed := filepath.Join(base, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "init", "-b", "main")
	writeFile(t, filepath.Join(seed, "go.mod"), "module github.com/HanamiSakiP/u-game\n\ngo 1.21\n")
	writeFile(t, filepath.Join(seed, "version.txt"), "v1")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "one")

	remotes := filepath.Join(base, "remotes")
	bare := filepath.Join(remotes, "owner", "repo.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, base, "init", "--bare", bare)
	gitRun(t, seed, "remote", "add", "origin", bare)
	gitRun(t, seed, "push", "-u", "origin", "main")

	src := filepath.Join(base, "src")
	opts := Options{
		SourceDir: src,
		Repo:      "owner/repo",
		Branch:    "main",
		Method:    "git",
		NoBuild:   true,
		GitBase:   "file://" + remotes,
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Method != "git" || res.Revision == "" {
		t.Fatalf("clone result = %#v", res)
	}
	if got := readFile(t, filepath.Join(src, "version.txt")); got != "v1" {
		t.Fatalf("version = %q", got)
	}

	// 新提交后，git 模式应增量更新。
	writeFile(t, filepath.Join(seed, "version.txt"), "v2")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "two")
	gitRun(t, seed, "push", "origin", "main")

	opts.PreviousRevision = res.Revision
	res, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatalf("second run should update: %#v", res)
	}
	if got := readFile(t, filepath.Join(src, "version.txt")); got != "v2" {
		t.Fatalf("version after update = %q", got)
	}

	// 没有新提交时保持不动。
	opts.PreviousRevision = res.Revision
	res, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("third run should be unchanged: %#v", res)
	}

	// --check 只 fetch，不修改工作区。
	writeFile(t, filepath.Join(seed, "version.txt"), "v3")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "three")
	gitRun(t, seed, "push", "origin", "main")

	opts.Check = true
	res, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatalf("check should detect update: %#v", res)
	}
	if got := readFile(t, filepath.Join(src, "version.txt")); got != "v2" {
		t.Fatalf("check should not modify worktree, version = %q", got)
	}

	// 本地有未提交改动时默认拒绝更新，--force 才会覆盖。
	writeFile(t, filepath.Join(src, "version.txt"), "local-edit")
	opts.Check = false
	opts.Force = false
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("dirty worktree should block update")
	}
	if got := readFile(t, filepath.Join(src, "version.txt")); got != "local-edit" {
		t.Fatalf("dirty update modified worktree: %q", got)
	}

	opts.Force = true
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(src, "version.txt")); got != "v3" {
		t.Fatalf("force update version = %q", got)
	}
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	all := append([]string{"-c", "user.name=ugal-test", "-c", "user.email=ugal@example.com"}, args...)
	cmd := exec.Command("git", all...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
