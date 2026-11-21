package player

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAudioFilesScansAndSorts(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Disc 2", "02.flac"))
	mustWrite(t, filepath.Join(dir, "Disc 2", "01.ogg"))
	mustWrite(t, filepath.Join(dir, "Disc 1", "01.mp3"))
	mustWrite(t, filepath.Join(dir, "cover.jpg"))
	mustWrite(t, filepath.Join(dir, "notes.txt"))

	files, err := AudioFiles(dir)
	if err != nil {
		t.Fatalf("AudioFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("应找到 3 个音频文件，得到 %d: %v", len(files), files)
	}
	want := []string{
		filepath.Join(dir, "Disc 1", "01.mp3"),
		filepath.Join(dir, "Disc 2", "01.ogg"),
		filepath.Join(dir, "Disc 2", "02.flac"),
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("排序结果 = %v, want %v", files, want)
	}
}

func TestAudioFilesEmptyDir(t *testing.T) {
	files, err := AudioFiles(t.TempDir())
	if err != nil {
		t.Fatalf("AudioFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("空目录应没有音频文件，得到 %v", files)
	}
}

func TestResolvePrefersOverride(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "01.mp3")
	mustWrite(t, file)

	looked := []string{}
	spec, err := Resolve(Options{
		Dir:    dir,
		Player: `"my player" --flag`,
		LookPath: func(name string) (string, error) {
			looked = append(looked, name)
			return "/opt/" + name, nil
		},
		GOOS: "linux",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(looked, []string{"my player"}) {
		t.Fatalf("应只查找指定播放器，得到 %v", looked)
	}
	wantArgs := []string{"--flag", file}
	if spec.Name != "my player" || spec.Path != "/opt/my player" || !reflect.DeepEqual(spec.Args, wantArgs) {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestResolveUsesEnvironment(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "track.wav")
	mustWrite(t, file)

	spec, err := Resolve(Options{
		Dir:    dir,
		Getenv: func(key string) string { return map[string]string{"UGAL_PLAYER": "vlc"}[key] },
		LookPath: func(name string) (string, error) {
			if name != "vlc" {
				return "", errors.New("not found")
			}
			return "/usr/bin/vlc", nil
		},
		GOOS: "linux",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.Path != "/usr/bin/vlc" || !reflect.DeepEqual(spec.Args, []string{file}) {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestResolvePicksFirstAvailablePlayer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "01.mp3")
	mustWrite(t, file)

	spec, err := Resolve(Options{
		Dir: dir,
		LookPath: func(name string) (string, error) {
			switch name {
			case "mpv":
				return "", errors.New("missing")
			case "vlc":
				return "/usr/bin/vlc", nil
			default:
				return "", errors.New("missing")
			}
		},
		GOOS: "linux",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.Name != "vlc" {
		t.Fatalf("应选择 vlc，得到 %#v", spec)
	}
	want := []string{"--started-from-file", file}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("参数 = %v, want %v", spec.Args, want)
	}
}

func TestResolveFallsBackToOpener(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "01.mp3")
	mustWrite(t, file)

	spec, err := Resolve(Options{
		Dir: dir,
		LookPath: func(name string) (string, error) {
			if name == "xdg-open" {
				return "/usr/bin/xdg-open", nil
			}
			return "", errors.New("missing")
		},
		GOOS: "linux",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if spec.Name != "系统默认播放器" || !reflect.DeepEqual(spec.Args, []string{file}) {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestResolveWindowsUsesStart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "01.mp3")
	mustWrite(t, file)

	spec, err := Resolve(Options{
		Dir: dir,
		LookPath: func(name string) (string, error) {
			if name == "cmd" {
				return `C:\Windows\System32\cmd.exe`, nil
			}
			return "", errors.New("missing")
		},
		GOOS: "windows",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{"/c", "start", "", file}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("参数 = %v, want %v", spec.Args, want)
	}
}

func TestResolveWithoutPlayer(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "01.mp3"))

	_, err := Resolve(Options{
		Dir:      dir,
		LookPath: func(string) (string, error) { return "", errors.New("missing") },
		GOOS:     "linux",
	})
	if err == nil || !strings.Contains(err.Error(), "没有找到可用的本地播放器") {
		t.Fatalf("错误 = %v", err)
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"mpv --no-video", []string{"mpv", "--no-video"}},
		{`"my player" --flag`, []string{"my player", "--flag"}},
		{`'a b' c\ d`, []string{"a b", "c d"}},
		{`  mpv   `, []string{"mpv"}},
	}
	for _, c := range cases {
		got, err := splitCommand(c.in)
		if err != nil {
			t.Fatalf("splitCommand(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("splitCommand(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := splitCommand(`"unterminated`); err == nil {
		t.Fatal("未闭合的引号应返回错误")
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
