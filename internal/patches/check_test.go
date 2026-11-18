package patches

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func TestCheckDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "patches")
	mustWrite := func(rel, content string) string {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	aPath := mustWrite("a.bin", "hello")
	bPath := mustWrite("b.bin", "wrong")
	cPath := mustWrite(filepath.Join("220", "c.bin"), "hello")
	// 同名文件位于不同子目录：任意一个哈希正确即视为通过。
	badPath := mustWrite(filepath.Join("old", "dup.bin"), "bad")
	goodPath := mustWrite(filepath.Join("new", "dup.bin"), "hello")
	// 同一个文件名按 appid 分目录存放时，只与本 appid 目录下的文件比较。
	onePath := mustWrite(filepath.Join("880", "same.bin"), "one")
	twoPath := mustWrite(filepath.Join("990", "same.bin"), "two")
	oneHash, err := HashFileMD5(onePath)
	if err != nil {
		t.Fatal(err)
	}
	twoHash, err := HashFileMD5(twoPath)
	if err != nil {
		t.Fatal(err)
	}
	unreadablePath := filepath.Join(dir, "unreadable.bin")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), unreadablePath); err != nil {
		t.Fatal(err)
	}

	const md5Hello = "5d41402abc4b2a76b9719d911017c592"
	const sha256Hello = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	data := &model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "a.bin", Hash: "md5:" + md5Hello},
		{AppID: 440, Name: "b.bin", Hash: md5Hello},
		{AppID: 220, Name: "c.bin", Hash: "sha256:" + sha256Hello},
		{AppID: 330, Name: "missing.bin", Hash: md5Hello},
		{AppID: 440, Name: "dup.bin", Hash: md5Hello},
		{AppID: 880, Name: "same.bin", Hash: oneHash},
		{AppID: 990, Name: "same.bin", Hash: twoHash},
		{AppID: 550, Name: "", Hash: md5Hello},
		{AppID: 660, Name: "a.bin", Hash: "xyz"},
		{AppID: 770, Name: "unreadable.bin", Hash: md5Hello},
	}}
	report := CheckDir(data, dir)

	if report.Total != len(data.Patches) {
		t.Fatalf("total = %d", report.Total)
	}
	if report.OK != 5 || report.Missing != 1 || report.Mismatch != 1 || report.Unverified != 3 {
		t.Fatalf("report = %#v", report)
	}

	byKey := make(map[string]CheckResult)
	for _, r := range report.Results {
		byKey[fmt.Sprintf("%d/%s", r.AppID, r.Name)] = r
	}
	if got := byKey["440/a.bin"]; got.Status != CheckOK || got.Path != aPath || got.ActualHash != md5Hello {
		t.Fatalf("a.bin = %#v", got)
	}
	if got := byKey["440/b.bin"]; got.Status != CheckMismatch || got.Path != bPath || got.ActualHash == md5Hello {
		t.Fatalf("b.bin = %#v", got)
	}
	if got := byKey["220/c.bin"]; got.Status != CheckOK || got.Path != cPath || got.ActualHash != sha256Hello {
		t.Fatalf("c.bin = %#v", got)
	}
	if got := byKey["330/missing.bin"]; got.Status != CheckMissing {
		t.Fatalf("missing.bin = %#v", got)
	}
	if got := byKey["440/dup.bin"]; got.Status != CheckOK || got.Path != goodPath || got.Path == badPath {
		t.Fatalf("dup.bin = %#v", got)
	}
	if got := byKey["880/same.bin"]; got.Status != CheckOK || got.Path != onePath {
		t.Fatalf("880 same.bin = %#v", got)
	}
	if got := byKey["990/same.bin"]; got.Status != CheckOK || got.Path != twoPath {
		t.Fatalf("990 same.bin = %#v", got)
	}
	if got := byKey["550/"]; got.Status != CheckUnverified || !strings.Contains(got.Err, "没有名称") {
		t.Fatalf("empty name = %#v", got)
	}
	if got := byKey["660/a.bin"]; got.Status != CheckUnverified || !strings.Contains(got.Err, "哈希算法") {
		t.Fatalf("unsupported hash = %#v", got)
	}
	if got := byKey["770/unreadable.bin"]; got.Status != CheckUnverified || got.Path != unreadablePath || got.Err == "" {
		t.Fatalf("unreadable = %#v", got)
	}

	// 目录不存在时全部标记为缺失，不返回错误。
	missingDir := filepath.Join(root, "nope")
	missing := CheckDir(data, missingDir)
	if missing.Total != len(data.Patches) || missing.Missing != len(data.Patches)-1 || missing.Unverified != 1 || missing.Dir != missingDir {
		t.Fatalf("missing dir = %#v", missing)
	}
}

func TestCheckDirProgress(t *testing.T) {
	data := &model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "a.bin", Hash: "5d41402abc4b2a76b9719d911017c592"},
		{AppID: 220, Name: "b.bin", Hash: "5d41402abc4b2a76b9719d911017c592"},
	}}
	type call struct {
		index int
		total int
		name  string
	}
	var calls []call
	report := CheckDirProgress(data, t.TempDir(), func(index, total int, name string) {
		calls = append(calls, call{index, total, name})
	})
	if report.Total != 2 || len(calls) != 2 {
		t.Fatalf("report = %#v, calls = %#v", report, calls)
	}
	if calls[0] != (call{1, 2, "a.bin"}) || calls[1] != (call{2, 2, "b.bin"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestCheckStatusLabel(t *testing.T) {
	cases := map[CheckStatus]string{
		CheckOK:         "已找到",
		CheckMissing:    "未找到",
		CheckMismatch:   "哈希错误",
		CheckUnverified: "无法校验",
	}
	for status, want := range cases {
		if got := status.Label(); got != want {
			t.Fatalf("%s.Label() = %q, want %q", status, got, want)
		}
	}
}
