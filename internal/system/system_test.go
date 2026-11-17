package system

import (
	"runtime"
	"testing"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func TestParseOSRelease(t *testing.T) {
	release := parseOSRelease(`
NAME="Parrot Security"
VERSION_ID="7.1"
PRETTY_NAME="Parrot Security 7.1 (echo)"
ID=debian
`)
	if release["NAME"] != "Parrot Security" || release["VERSION_ID"] != "7.1" || release["ID"] != "debian" {
		t.Fatalf("release = %#v", release)
	}
	info := &model.SystemData{}
	applyOSRelease(info, release)
	if info.Distro != "Parrot Security" || info.DistroVersion != "7.1" {
		t.Fatalf("info = %#v", info)
	}
	if info.Platform != "Parrot Security 7.1 (echo)" {
		t.Fatalf("platform = %q", info.Platform)
	}
}

func TestDetect(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	info := Detect()
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("info = %#v", info)
	}
	if info.Platform == "" || info.DetectedAt.IsZero() || info.GoVersion == "" {
		t.Fatalf("incomplete info = %#v", info)
	}
	if info.Desktop != "KDE" || info.SessionType != "wayland" {
		t.Fatalf("desktop = %q session = %q", info.Desktop, info.SessionType)
	}
}
