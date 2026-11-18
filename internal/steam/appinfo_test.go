package steam

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildAppInfo 构造一个最小的 appinfo.vdf v29 测试文件。
func buildAppInfo(t *testing.T, apps map[int]string) []byte {
	t.Helper()
	table := []string{"appinfo", "appid", "public_only", "common", "name"}

	var records []byte
	appendU32 := func(dst []byte, v uint32) []byte {
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], v)
		return append(dst, buf[:]...)
	}
	key := func(dst []byte, index uint32) []byte { return appendU32(dst, index) }

	for appid, name := range apps {
		var kv []byte
		kv = append(kv, 0x00) // object: appinfo
		kv = key(kv, 0)
		kv = append(kv, 0x02) // int32: appid
		kv = key(kv, 1)
		kv = appendU32(kv, uint32(appid))
		kv = append(kv, 0x00) // object: common
		kv = key(kv, 3)
		kv = append(kv, 0x01) // string: name
		kv = key(kv, 4)
		kv = append(kv, name...)
		kv = append(kv, 0)
		kv = append(kv, 0x08) // end common
		kv = append(kv, 0x08) // end appinfo

		data := make([]byte, appInfoKVOffset)
		data = append(data, kv...)
		records = appendU32(records, uint32(appid))
		records = appendU32(records, uint32(len(data)))
		records = append(records, data...)
	}
	records = appendU32(records, 0) // appid = 0 结束记录区

	tableOffset := 16 + len(records)
	out := make([]byte, 16)
	binary.LittleEndian.PutUint32(out[0:4], appInfoMagicV29)
	binary.LittleEndian.PutUint32(out[4:8], 1)
	binary.LittleEndian.PutUint64(out[8:16], uint64(tableOffset))
	out = append(out, records...)

	var tableData []byte
	tableData = appendU32(tableData, uint32(len(table)))
	for _, s := range table {
		tableData = append(tableData, s...)
		tableData = append(tableData, 0)
	}
	out = append(out, tableData...)
	return out
}

func TestParseAppInfoNames(t *testing.T) {
	data := buildAppInfo(t, map[int]string{
		440:     "Team Fortress 2",
		3771230: "纸上的魔法使",
	})
	names, err := ParseAppInfoNames(data)
	if err != nil {
		t.Fatalf("ParseAppInfoNames: %v", err)
	}
	if names[440] != "Team Fortress 2" {
		t.Fatalf("440 name = %q", names[440])
	}
	if names[3771230] != "纸上的魔法使" {
		t.Fatalf("3771230 name = %q", names[3771230])
	}
	if len(names) != 2 {
		t.Fatalf("names = %#v", names)
	}
}

func TestParseAppInfoNamesRejectsUnknownFormat(t *testing.T) {
	data := make([]byte, 32)
	binary.LittleEndian.PutUint32(data[0:4], 0xdeadbeef)
	if _, err := ParseAppInfoNames(data); err == nil {
		t.Fatal("expected error for unknown appinfo format")
	}
}

func TestAppInfoPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "steamapps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "appcache"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "appcache", "appinfo.vdf")
	if err := os.WriteFile(path, buildAppInfo(t, map[int]string{440: "TF2"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AppInfoPath(root); got != path {
		t.Fatalf("AppInfoPath = %q, want %q", got, path)
	}
	names, err := LoadAppInfoNames(root)
	if err != nil {
		t.Fatalf("LoadAppInfoNames: %v", err)
	}
	if names[440] != "TF2" {
		t.Fatalf("names = %#v", names)
	}
}
