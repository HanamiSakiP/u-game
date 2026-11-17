package ugal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSourceWritesTree(t *testing.T) {
	dest := filepath.Join(t.TempDir(), ".ugal", "src")
	written, err := EnsureSource(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("first EnsureSource should write files")
	}
	for _, name := range []string{
		"go.mod",
		"source.go",
		"cmd/ugal/main.go",
		"internal/cli/cli.go",
		"internal/selfupdate/selfupdate.go",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	goMod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "module github.com/HanamiSakiP/u-game") {
		t.Fatalf("go.mod = %q", goMod)
	}

	// 已存在的完整源码不覆盖。
	if err := os.WriteFile(filepath.Join(dest, "go.mod"), []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err = EnsureSource(dest)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("existing source should not be overwritten")
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "go.mod")); string(got) != "custom\n" {
		t.Fatalf("go.mod overwritten: %q", got)
	}

	// 源码不完整时会补齐。
	if err := os.Remove(filepath.Join(dest, "cmd", "ugal", "main.go")); err != nil {
		t.Fatal(err)
	}
	written, err = EnsureSource(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("incomplete source should be rewritten")
	}
	if _, err := os.Stat(filepath.Join(dest, "cmd", "ugal", "main.go")); err != nil {
		t.Fatal(err)
	}
}

func TestIsSourceDir(t *testing.T) {
	dir := t.TempDir()
	if IsSourceDir(dir) {
		t.Fatal("empty dir should not be a source dir")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/HanamiSakiP/u-game\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsSourceDir(dir) {
		t.Fatal("module should be detected")
	}
}
