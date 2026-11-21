package player

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestControllerPauseSelectAndStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持通过信号暂停")
	}
	dir := t.TempDir()
	for _, name := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		mustWrite(t, filepath.Join(dir, name))
	}
	script := filepath.Join(dir, "fake-player.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	ctrl, err := Start(Options{Dir: dir, Player: script}, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ctrl.Stop()

	index, paused, playing := ctrl.State()
	if index != 0 || paused || !playing {
		t.Fatalf("初始状态 = %d %v %v", index, paused, playing)
	}
	if paused, err := ctrl.TogglePause(); err != nil || !paused {
		t.Fatalf("暂停失败: %v %v", paused, err)
	}
	if paused, err := ctrl.TogglePause(); err != nil || paused {
		t.Fatalf("继续失败: %v %v", paused, err)
	}
	if index, err := ctrl.Next(); err != nil || index != 1 {
		t.Fatalf("下一首 = %d %v", index, err)
	}
	if index, err := ctrl.Select(2); err != nil || index != 2 {
		t.Fatalf("选择第三首 = %d %v", index, err)
	}
	if index, err := ctrl.Next(); err != nil || index != 0 {
		t.Fatalf("最后一首的下一首应回到 0，得到 %d %v", index, err)
	}
	ctrl.Stop()
	// Stop 之后进程应结束，状态不再报播放中。
	time.Sleep(20 * time.Millisecond)
	if _, _, playing := ctrl.State(); playing {
		t.Fatal("Stop 后不应仍在播放")
	}
}

func TestArgsForIndex(t *testing.T) {
	spec := Spec{
		Path:     "/usr/bin/mpv",
		BaseArgs: []string{"--no-video", "--no-terminal", "--"},
		Files:    []string{"/music/1.mp3", "/music/2.mp3"},
		Kind:     KindMPV,
	}
	got := spec.argsForIndex(1)
	want := []string{"--no-video", "--no-terminal", "--", "/music/2.mp3"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}

	opener := Spec{
		Path:  "/usr/bin/xdg-open",
		Files: []string{"/music/1.mp3", "/music/2.mp3"},
		Kind:  KindOpener,
	}
	if got := opener.argsForIndex(1); len(got) != 1 || got[0] != "/music/2.mp3" {
		t.Fatalf("opener args = %v", got)
	}
}

func TestKindForName(t *testing.T) {
	if got := kindForName("/usr/bin/mpv", false); got != KindMPV {
		t.Fatalf("mpv kind = %v", got)
	}
	if got := kindForName("/usr/bin/vlc", false); got != KindGeneric {
		t.Fatalf("vlc kind = %v", got)
	}
	if got := kindForName("/usr/bin/xdg-open", true); got != KindOpener {
		t.Fatalf("opener kind = %v", got)
	}
}
