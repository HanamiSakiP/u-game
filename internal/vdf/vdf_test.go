package vdf

import "testing"

func TestParseNestedAndEscapes(t *testing.T) {
	data := []byte("\xEF\xBB\xBF\"libraryfolders\"\n{\n\t// a comment\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\"D:\\\\SteamLibrary\"\n\t\t\"apps\"\n\t\t{\n\t\t\t\"1985260\"\t\t\"5698305420\"\n\t\t}\n\t}\n}\n")
	root, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if root.Key != "libraryfolders" {
		t.Fatalf("root key = %q", root.Key)
	}
	lib := root.Child("0")
	if lib == nil {
		t.Fatal("missing library 0")
	}
	path := lib.Child("path")
	if got, want := path.String(), `D:\SteamLibrary`; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	apps := lib.Child("apps")
	if apps == nil || apps.Child("1985260").Int64() != 5698305420 {
		t.Fatalf("apps parsed incorrectly: %#v", apps)
	}
}

func TestParseLegacyFormat(t *testing.T) {
	root, err := Parse([]byte(`"LibraryFolders" { "1" "E:\\Gry\\Steam" "2" "F:\\Games" }`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := root.Child("1").String(); got != `E:\Gry\Steam` {
		t.Fatalf("legacy path = %q", got)
	}
}

func TestParseUnclosed(t *testing.T) {
	if _, err := Parse([]byte(`"root" { "a" "b"`)); err == nil {
		t.Fatal("expected error for unclosed object")
	}
}
