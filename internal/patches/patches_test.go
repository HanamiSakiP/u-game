package patches

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func TestSetFindRemove(t *testing.T) {
	data := &model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "Old", Hash: "SHA256:ABCDEF", Applied: true},
	}}
	Set(data, model.Patch{AppID: 220, Name: "Patch B", Hash: "abc", Applied: false})
	p := Find(data, 440)
	if p == nil || p.Name != "Old" || p.Hash != "abcdef" || !p.Applied {
		t.Fatalf("patch = %#v", p)
	}
	if StatusLabel(Find(data, 440)) != "已打补丁" {
		t.Fatal("applied status")
	}
	if StatusLabel(Find(data, 220)) != "未打补丁" {
		t.Fatal("unpatched status")
	}
	if StatusLabel(nil) != "无补丁" {
		t.Fatal("no patch status")
	}
	if data.Count != 2 || data.Patches[0].AppID != 220 {
		t.Fatalf("data = %#v", data)
	}
	if removed := Remove(data, []int{220, 999}); removed != 1 || data.Count != 1 {
		t.Fatalf("removed = %d data = %#v", removed, data)
	}
}

func TestNormalizeHash(t *testing.T) {
	cases := map[string]string{
		"  SHA256:ABCDEF  ": "abcdef",
		"sha-256:abc":       "abc",
		"md5:ABC":           "abc",
		"ABCDEF":            "abcdef",
	}
	for in, want := range cases {
		if got := NormalizeHash(in); got != want {
			t.Fatalf("NormalizeHash(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHashFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("hash = %q", got)
	}
}

func TestHashFileMD5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "patch.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := HashFileMD5(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "5d41402abc4b2a76b9719d911017c592"
	if got != want {
		t.Fatalf("md5 = %q", got)
	}
	got, err = HashFileWith(path, "MD5")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HashFileWith md5 = %q", got)
	}
	if _, err := HashFileWith(path, "crc32"); err == nil {
		t.Fatal("expected error for unsupported algorithm")
	}
}

func TestMultiplePatchesPerApp(t *testing.T) {
	data := &model.PatchesData{Patches: []model.Patch{
		{AppID: 440, Name: "a.xp3", Hash: "aaa", Applied: true},
		{AppID: 440, Name: "b.xp3", Hash: "bbb", Applied: false},
		{AppID: 440, Name: "a.xp3", Hash: "SHA256:CCC", Applied: true},
	}}
	Normalize(data)
	if data.Count != 2 {
		t.Fatalf("count = %d, patches = %#v", data.Count, data.Patches)
	}
	a := FindByName(data, 440, "a.xp3")
	if a == nil || a.Hash != "ccc" || !a.Applied {
		t.Fatalf("a = %#v", a)
	}
	if StatusLabelAll(FindAll(data, 440)) != "部分已打" {
		t.Fatalf("status = %q", StatusLabelAll(FindAll(data, 440)))
	}

	// 同名记录更新，不会新增记录。
	Set(data, model.Patch{AppID: 440, Name: "b.xp3", Hash: "BBB2", Applied: false})
	if data.Count != 2 {
		t.Fatalf("count after set = %d", data.Count)
	}
	if StatusLabelAll(FindAll(data, 440)) != "部分已打" {
		t.Fatalf("status after set = %q", StatusLabelAll(FindAll(data, 440)))
	}
	Set(data, model.Patch{AppID: 440, Name: "a.xp3", Hash: "ccc", Applied: false})
	if got := StatusLabelAll(FindAll(data, 440)); got != "未打补丁" {
		t.Fatalf("status = %q", got)
	}
	Set(data, model.Patch{AppID: 440, Name: "a.xp3", Hash: "ccc", Applied: true})
	if got := StatusLabelAll(FindAll(data, 440)); got != "部分已打" {
		t.Fatalf("status = %q", got)
	}
	Set(data, model.Patch{AppID: 440, Name: "b.xp3", Hash: "BBB2", Applied: true})
	if got := StatusLabelAll(FindAll(data, 440)); got != "已打补丁" {
		t.Fatalf("status = %q", got)
	}

	// 按名称只删除一条。
	if removed := RemoveByName(data, 440, "a.xp3"); removed != 1 || data.Count != 1 {
		t.Fatalf("removed = %d, data = %#v", removed, data)
	}
	// 按 appid 删除剩余全部记录。
	if removed := Remove(data, []int{440}); removed != 1 || data.Count != 0 {
		t.Fatalf("removed = %d, data = %#v", removed, data)
	}
}

func TestSetPreservesExistingFields(t *testing.T) {
	data := &model.PatchesData{}
	Set(data, model.Patch{AppID: 1, Name: "Patch", Hash: "abc", Applied: true})
	// 模拟 CLI 只更新 applied 时读取旧记录再写回。
	p := Find(data, 1)
	if p == nil {
		t.Fatal("missing patch")
	}
	updated := *p
	updated.Applied = false
	Set(data, updated)
	p = Find(data, 1)
	if p.Name != "Patch" || p.Hash != "abc" || p.Applied {
		t.Fatalf("patch = %#v", p)
	}
}
