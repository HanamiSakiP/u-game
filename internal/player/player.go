// Package player 用本机已安装的播放器打开音乐原声带。
package player

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Kind 表示播放器的控制能力类别。
type Kind int

const (
	// KindGeneric 是普通播放器，只能通过系统信号暂停，换曲需要重新启动。
	KindGeneric Kind = iota
	// KindMPV 是 mpv，可以通过 IPC 精确控制暂停、切歌和查询状态。
	KindMPV
	// KindOpener 是系统默认打开方式（xdg-open / open / start），无法控制。
	KindOpener
)

// Spec 描述一次已经解析好的播放调用。
type Spec struct {
	// Name 是展示给用户的播放器名称。
	Name string
	// Path 是可执行文件路径。
	Path string
	// Args 是完整的命令行参数（包含音频文件）。
	Args []string
	// BaseArgs 是音频文件之前的参数，便于换曲时重新拼装。
	BaseArgs []string
	// Files 是本次播放的音频文件列表。
	Files []string
	// Kind 表示播放器类别，用于决定是否支持暂停和切歌。
	Kind Kind
}

// Options 控制播放器选择。
//
// Getenv、LookPath 和 GOOS 主要供测试替换，正常调用留空即可。
type Options struct {
	Dir      string
	Player   string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	GOOS     string
}

var audioExtensions = map[string]bool{
	".aac":  true,
	".ac3":  true,
	".aif":  true,
	".aiff": true,
	".alac": true,
	".ape":  true,
	".dff":  true,
	".dsf":  true,
	".flac": true,
	".m4a":  true,
	".m4b":  true,
	".mid":  true,
	".midi": true,
	".mka":  true,
	".mp2":  true,
	".mp3":  true,
	".oga":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".wma":  true,
}

// linuxPlayers 是按优先级排列的本地播放器。它们都能接受一组文件作为播放列表。
var linuxPlayers = []string{
	"mpv", "vlc", "celluloid", "audacious", "strawberry",
	"clementine", "elisa", "rhythmbox", "parole", "quodlibet", "deadbeef",
}

var darwinPlayers = []string{"mpv", "vlc", "celluloid", "iina"}

var windowsPlayers = []string{"mpv", "vlc"}

// AudioFiles 返回目录中所有可播放的音频文件（递归、按名称排序）。
// dir 也可以是单个音频文件。
func AudioFiles(dir string) ([]string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("原声带目录为空")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("无法读取原声带目录: %w", err)
	}
	if !info.IsDir() {
		if !audioExtensions[strings.ToLower(filepath.Ext(dir))] {
			return nil, fmt.Errorf("%s 不是音频文件", dir)
		}
		return []string{dir}, nil
	}

	var files []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !audioExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描原声带目录失败: %w", err)
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, b := strings.ToLower(files[i]), strings.ToLower(files[j])
		if a != b {
			return a < b
		}
		return files[i] < files[j]
	})
	return files, nil
}

// Resolve 选择一个本地播放器并生成播放参数，但不会启动它。
// 优先级：显式 Player > 环境变量 UGAL_PLAYER > 常见播放器 > 系统默认打开方式。
func Resolve(opts Options) (Spec, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	files, err := AudioFiles(opts.Dir)
	if err != nil {
		return Spec{}, err
	}
	if len(files) == 0 {
		return Spec{}, fmt.Errorf("未在 %s 中找到音频文件", opts.Dir)
	}

	custom := strings.TrimSpace(opts.Player)
	if custom == "" {
		custom = strings.TrimSpace(getenv("UGAL_PLAYER"))
	}
	if custom != "" {
		return resolveCustom(custom, files, lookPath)
	}

	for _, name := range playersFor(goos) {
		path, err := lookPath(name)
		if err != nil {
			continue
		}
		base := baseArgsFor(name)
		return Spec{
			Name:     name,
			Path:     path,
			Args:     append(append([]string{}, base...), files...),
			BaseArgs: base,
			Files:    files,
			Kind:     kindForName(name, false),
		}, nil
	}

	openerNames := openersFor(goos)
	for _, name := range openerNames {
		path, err := lookPath(name)
		if err != nil {
			continue
		}
		return Spec{
			Name:     "系统默认播放器",
			Path:     path,
			Args:     openerArgs(name, files),
			BaseArgs: nil,
			Files:    files,
			Kind:     KindOpener,
		}, nil
	}
	return Spec{}, errors.New("没有找到可用的本地播放器，可安装 mpv / VLC，或用 UGAL_PLAYER 指定播放器命令")
}

func playersFor(goos string) []string {
	switch goos {
	case "darwin":
		return darwinPlayers
	case "windows":
		return windowsPlayers
	default:
		return linuxPlayers
	}
}

func openersFor(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"open"}
	case "windows":
		return []string{"cmd"}
	default:
		return []string{"xdg-open", "gio"}
	}
}

func resolveCustom(custom string, files []string, lookPath func(string) (string, error)) (Spec, error) {
	fields, err := splitCommand(custom)
	if err != nil {
		return Spec{}, fmt.Errorf("解析播放器命令失败: %w", err)
	}
	if len(fields) == 0 {
		return Spec{}, errors.New("播放器命令为空")
	}
	name := fields[0]
	path := name
	if !looksLikePath(name) {
		resolved, err := lookPath(name)
		if err != nil {
			return Spec{}, fmt.Errorf("找不到播放器 %q: %w", name, err)
		}
		path = resolved
	}
	base := fields[1:]
	return Spec{
		Name:     filepath.Base(name),
		Path:     path,
		Args:     append(append([]string{}, base...), files...),
		BaseArgs: base,
		Files:    files,
		Kind:     kindForName(name, false),
	}, nil
}

func looksLikePath(name string) bool {
	return filepath.IsAbs(name) || strings.ContainsAny(name, `/\`)
}

func kindForName(name string, opener bool) Kind {
	if opener {
		return KindOpener
	}
	switch strings.ToLower(filepath.Base(name)) {
	case "mpv", "mpv.exe":
		return KindMPV
	default:
		return KindGeneric
	}
}

func baseArgsFor(name string) []string {
	switch name {
	case "mpv":
		return []string{"--no-video", "--no-terminal", "--really-quiet", "--"}
	case "vlc", "cvlc":
		return []string{"--started-from-file"}
	default:
		return nil
	}
}

func openerArgs(name string, files []string) []string {
	if len(files) == 0 {
		return nil
	}
	switch name {
	case "cmd", "cmd.exe":
		// start 是 cmd 内置命令；第一个空参数是窗口标题。
		return []string{"/c", "start", "", files[0]}
	case "gio":
		return []string{"open", files[0]}
	default:
		return []string{files[0]}
	}
}

// Play 启动播放器并立即返回，播放器会继续在后台运行。
func Play(spec Spec) error {
	if spec.Path == "" {
		return errors.New("没有可执行的播放器")
	}
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 失败: %w", spec.Name, err)
	}
	return cmd.Process.Release()
}

// PlayDir 解析并启动本地播放器。
func PlayDir(opts Options) (Spec, error) {
	spec, err := Resolve(opts)
	if err != nil {
		return Spec{}, err
	}
	if err := Play(spec); err != nil {
		return spec, err
	}
	return spec, nil
}

// splitCommand 按 shell 风格拆分命令，支持单引号、双引号和反斜杠转义。
func splitCommand(s string) ([]string, error) {
	var (
		fields  []string
		current strings.Builder
		quote   rune
		escaped bool
		started bool
	)
	flush := func() {
		if started {
			fields = append(fields, current.String())
			current.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			current.WriteRune(r)
			started = true
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, errors.New("命令末尾有未完成的转义")
	}
	if quote != 0 {
		return nil, errors.New("引号没有闭合")
	}
	flush()
	return fields, nil
}
