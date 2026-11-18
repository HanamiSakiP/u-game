package patches

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
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

func TestInstallZipReplacesFiles(t *testing.T) {
	gameDir := t.TempDir()
	old := filepath.Join(gameDir, "data", "script.bin")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, archive, map[string]string{
		"data/script.bin": "new",
		"data/extra.dat":  "extra",
	})

	backupDir := filepath.Join(t.TempDir(), "backup")
	res, err := Install(InstallOptions{Archive: archive, GameDir: gameDir, BackupDir: backupDir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Replaced != 1 || res.BackedUp != 1 {
		t.Fatalf("result = %#v", res)
	}
	if data, err := os.ReadFile(old); err != nil || string(data) != "new" {
		t.Fatalf("replaced file = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(gameDir, "data", "extra.dat")); err != nil || string(data) != "extra" {
		t.Fatalf("extra file = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(backupDir, "data", "script.bin")); err != nil || string(data) != "old" {
		t.Fatalf("backup = %q, %v", data, err)
	}
}

func TestInstallZipStrip(t *testing.T) {
	gameDir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, archive, map[string]string{
		"wrapper/game.exe": "exe",
	})
	if _, err := Install(InstallOptions{Archive: archive, GameDir: gameDir, Strip: 1}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(gameDir, "game.exe")); err != nil || string(data) != "exe" {
		t.Fatalf("stripped file = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "wrapper")); !os.IsNotExist(err) {
		t.Fatalf("wrapper dir should not exist: %v", err)
	}
}

func TestInstallTarGz(t *testing.T) {
	gameDir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "patch.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("tar-content")
	if err := tw.WriteHeader(&tar.Header{Name: "save/patch.txt", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(InstallOptions{Archive: archive, GameDir: gameDir}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(gameDir, "save", "patch.txt")); err != nil || string(data) != "tar-content" {
		t.Fatalf("tar file = %q, %v", data, err)
	}
}

func TestInstallRejectsTraversal(t *testing.T) {
	gameDir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, archive, map[string]string{
		"../evil.txt": "evil",
	})
	if _, err := Install(InstallOptions{Archive: archive, GameDir: gameDir}); err == nil {
		t.Fatal("path traversal should be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(gameDir), "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("evil file should not exist: %v", err)
	}
}

func TestInstallDryRun(t *testing.T) {
	gameDir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, archive, map[string]string{"a.txt": "a"})
	res, err := Install(InstallOptions{Archive: archive, GameDir: gameDir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 1 {
		t.Fatalf("files = %d", res.Files)
	}
	if _, err := os.Stat(filepath.Join(gameDir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write files: %v", err)
	}
}
