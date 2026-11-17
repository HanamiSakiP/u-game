// Package system 检测并记录 ugal 运行所在的系统环境。
package system

import (
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

// Detect 读取当前系统信息。读取失败时对应字段留空，不会返回错误。
func Detect() *model.SystemData {
	info := &model.SystemData{
		Version:     model.Version,
		DetectedAt:  time.Now().UTC(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Kernel:      kernelRelease(),
		Desktop:     desktopName(),
		SessionType: sessionType(),
		Shell:       strings.TrimSpace(os.Getenv("SHELL")),
		Locale:      strings.TrimSpace(os.Getenv("LANG")),
		GoVersion:   runtime.Version(),
	}
	if host, err := os.Hostname(); err == nil {
		info.Hostname = strings.TrimSpace(host)
	}
	applyOSRelease(info, readOSRelease())
	if info.Platform == "" {
		info.Platform = strings.TrimSpace(info.OS + "/" + info.Arch)
	}
	return info
}

func readOSRelease() map[string]string {
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return parseOSRelease(string(data))
	}
	return nil
}

func parseOSRelease(text string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		value = strings.ReplaceAll(value, `\"`, `"`)
		if key != "" {
			out[strings.ToUpper(key)] = value
		}
	}
	return out
}

func applyOSRelease(info *model.SystemData, release map[string]string) {
	if len(release) == 0 {
		return
	}
	info.Distro = firstNonEmpty(release["NAME"], release["ID"])
	info.DistroVersion = firstNonEmpty(release["VERSION_ID"], release["VERSION"])
	platform := strings.TrimSpace(release["PRETTY_NAME"])
	if platform == "" {
		platform = strings.TrimSpace(strings.Join(nonEmpty(info.Distro, info.DistroVersion), " "))
	}
	info.Platform = platform
}

func kernelRelease() string {
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v
			}
		}
	}
	return ""
}

func desktopName() string {
	for _, key := range []string{"XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "DESKTOP_SESSION"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func sessionType() string {
	if v := strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")); v != "" {
		return strings.ToLower(v)
	}
	if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" {
		return "wayland"
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) != "" {
		return "x11"
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
