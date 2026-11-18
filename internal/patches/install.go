package patches

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// InstallOptions 控制一次补丁安装。
type InstallOptions struct {
	// Archive 是补丁压缩包路径。
	Archive string
	// GameDir 是游戏安装目录，压缩包内容会解压到这里。
	GameDir string
	// Strip 是解压时去掉的顶层目录层数，0 表示保留压缩包中的完整路径。
	Strip int
	// BackupDir 不为空时，被替换的原有文件会先备份到该目录。
	BackupDir string
	// DryRun 只列出将要写入的文件，不修改游戏目录。
	DryRun bool
}

// InstallResult 描述一次补丁安装的结果。
type InstallResult struct {
	Archive   string   `json:"archive"`
	GameDir   string   `json:"game_dir"`
	Files     int      `json:"files"`
	Replaced  int      `json:"replaced"`
	BackedUp  int      `json:"backed_up,omitempty"`
	BackupDir string   `json:"backup_dir,omitempty"`
	Strip     int      `json:"strip,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
	Entries   []string `json:"entries,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Install 把补丁压缩包解压到游戏目录。同名文件会被替换；需要保留时可用
// BackupDir 先备份。支持 zip、tar、tar.gz/tgz、gz，以及借助系统的 7z/unar/
// unrar/bsdtar 解压 7z、rar、exe 等格式。
func Install(opts InstallOptions) (*InstallResult, error) {
	archive := strings.TrimSpace(opts.Archive)
	gameDir := strings.TrimSpace(opts.GameDir)
	if archive == "" {
		return nil, errors.New("缺少补丁文件")
	}
	if gameDir == "" {
		return nil, errors.New("缺少游戏目录")
	}
	archive, err := expandArchivePath(archive)
	if err != nil {
		return nil, err
	}
	gameDir, err = expandArchivePath(gameDir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(archive); err != nil || fi.IsDir() {
		if err != nil {
			return nil, fmt.Errorf("读取补丁文件失败: %w", err)
		}
		return nil, fmt.Errorf("补丁路径不是文件: %s", archive)
	}
	if fi, err := os.Stat(gameDir); err != nil || !fi.IsDir() {
		if err != nil {
			return nil, fmt.Errorf("游戏目录不可用: %w", err)
		}
		return nil, fmt.Errorf("游戏目录不是文件夹: %s", gameDir)
	}
	if opts.Strip < 0 {
		return nil, errors.New("--strip 不能为负数")
	}

	work, err := os.MkdirTemp("", "ugal-patch-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(work)

	kind, err := detectArchive(archive)
	if err != nil {
		return nil, err
	}
	if err := extractArchive(archive, work, kind); err != nil {
		return nil, err
	}

	result := &InstallResult{
		Archive:   archive,
		GameDir:   gameDir,
		Strip:     opts.Strip,
		DryRun:    opts.DryRun,
		BackupDir: opts.BackupDir,
	}
	failed := 0
	if opts.BackupDir != "" && !opts.DryRun {
		opts.BackupDir, err = expandArchivePath(opts.BackupDir)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(opts.BackupDir, 0o755); err != nil {
			return nil, fmt.Errorf("创建补丁备份目录失败: %w", err)
		}
		result.BackupDir = opts.BackupDir
	}

	// 解压到临时目录之后再做一次路径校验和同名替换，避免压缩包中的
	// "../" 或绝对路径跑到游戏目录之外。
	_ = filepath.WalkDir(work, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, walkErr))
			return nil
		}
		if path == work {
			return nil
		}
		rel, relErr := filepath.Rel(work, path)
		if relErr != nil {
			return nil
		}
		rel, ok := applyStrip(filepath.ToSlash(rel), opts.Strip)
		if !ok || rel == "" {
			return nil
		}
		target, joinErr := safeJoin(gameDir, rel)
		if joinErr != nil {
			result.Warnings = append(result.Warnings, joinErr.Error())
			return nil
		}
		if d.IsDir() {
			if !opts.DryRun {
				if err := os.MkdirAll(target, 0o755); err != nil {
					result.Warnings = append(result.Warnings, fmt.Sprintf("创建目录 %s 失败: %v", target, err))
					failed++
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("跳过压缩包中的符号链接: %s", rel))
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, infoErr))
			return nil
		}
		if !info.Mode().IsRegular() {
			result.Warnings = append(result.Warnings, fmt.Sprintf("跳过特殊文件: %s", rel))
			return nil
		}
		result.Files++
		if len(result.Entries) < 200 {
			result.Entries = append(result.Entries, filepath.ToSlash(rel))
		}

		existing, statErr := os.Lstat(target)
		switch {
		case statErr == nil && existing.IsDir():
			result.Warnings = append(result.Warnings, fmt.Sprintf("目标位置是目录，跳过文件: %s", target))
			failed++
			return nil
		case statErr == nil:
			result.Replaced++
			if opts.BackupDir != "" && !opts.DryRun && existing.Mode().IsRegular() {
				backup := filepath.Join(opts.BackupDir, filepath.FromSlash(rel))
				if err := copyInstallFile(target, backup, existing.Mode().Perm()); err != nil {
					result.Warnings = append(result.Warnings, fmt.Sprintf("备份 %s 失败: %v", target, err))
					failed++
				} else {
					result.BackedUp++
				}
			}
		}

		if opts.DryRun {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("创建目录失败: %v", err))
			failed++
			return nil
		}
		if err := copyInstallFile(path, target, info.Mode().Perm()); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("写入 %s 失败: %v", target, err))
			failed++
			return nil
		}
		if err := os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
			// 修改时间只是参考信息，失败不影响安装。
		}
		return nil
	})

	sort.Strings(result.Entries)
	if result.Files == 0 {
		return result, errors.New("压缩包中没有可安装的文件（检查 --strip 是否过大）")
	}
	if failed > 0 {
		return result, fmt.Errorf("补丁安装未完成：%d 个文件处理失败", failed)
	}
	return result, nil
}

func extractArchive(archive, dest, kind string) error {
	switch kind {
	case "zip":
		if err := extractZip(archive, dest); err != nil {
			return fmt.Errorf("解压 zip 失败: %w", err)
		}
	case "tar":
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := extractTar(f, dest); err != nil {
			return fmt.Errorf("解压 tar 失败: %w", err)
		}
	case "tar.gz":
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("解压 tar.gz 失败: %w", err)
		}
		defer gz.Close()
		if err := extractTar(gz, dest); err != nil {
			return fmt.Errorf("解压 tar.gz 失败: %w", err)
		}
	case "gz":
		if err := extractGz(archive, dest); err != nil {
			return fmt.Errorf("解压 gz 失败: %w", err)
		}
	default:
		if err := extractExternal(archive, dest, kind); err != nil {
			return err
		}
	}
	return nil
}

func extractZip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		rel := strings.ReplaceAll(f.Name, "\\", "/")
		if rel == "" {
			continue
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(rel, "/") {
			if target, err := safeJoin(dest, rel); err == nil {
				_ = os.MkdirAll(target, 0o755)
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		target, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel := strings.ReplaceAll(hdr.Name, "\\", "/")
		if rel == "" {
			continue
		}
		target, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := fs.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink, tar.TypeLink:
			// 补丁不需要链接，跳过以避免链接指向游戏目录之外。
		}
	}
}

func extractGz(archive, dest string) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	name := filepath.Base(archive)
	if strings.EqualFold(filepath.Ext(name), ".gz") {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if name == "" {
		name = "patch"
	}
	target := filepath.Join(dest, name)
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, gz)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func extractExternal(archive, dest, kind string) error {
	type tool struct {
		name string
		args func(dest, archive string) []string
	}
	sevenZip := func(dest, archive string) []string {
		return []string{"x", "-y", "-o" + dest, archive}
	}
	bsdtar := func(dest, archive string) []string {
		return []string{"-xf", archive, "-C", dest}
	}
	unar := func(dest, archive string) []string {
		return []string{"-q", "-o", dest, archive}
	}
	unrar := func(dest, archive string) []string {
		return []string{"x", "-o+", "-y", archive, dest + string(filepath.Separator)}
	}

	var candidates []tool
	switch kind {
	case "rar":
		candidates = []tool{
			{name: "unrar", args: unrar},
			{name: "7z", args: sevenZip},
			{name: "7zz", args: sevenZip},
			{name: "7za", args: sevenZip},
			{name: "bsdtar", args: bsdtar},
			{name: "unar", args: unar},
		}
	case "exe":
		candidates = []tool{
			{name: "7z", args: sevenZip},
			{name: "7zz", args: sevenZip},
			{name: "7za", args: sevenZip},
			{name: "bsdtar", args: bsdtar},
			{name: "unar", args: unar},
		}
	default:
		candidates = []tool{
			{name: "7z", args: sevenZip},
			{name: "7zz", args: sevenZip},
			{name: "7za", args: sevenZip},
			{name: "bsdtar", args: bsdtar},
			{name: "unar", args: unar},
			{name: "unrar", args: unrar},
		}
	}

	var (
		tried   []string
		lastErr error
	)
	for _, candidate := range candidates {
		bin, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		tried = append(tried, candidate.name)
		// 上一次尝试可能留下不完整的文件，换工具前清空临时目录。
		_ = os.RemoveAll(dest)
		_ = os.MkdirAll(dest, 0o755)
		cmd := exec.Command(bin, candidate.args(dest, archive)...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%s: %v: %s", candidate.name, err, truncateOutput(string(out), 500))
	}
	if len(tried) == 0 {
		return fmt.Errorf("无法解压 %s：请安装 7z、unar 或 unrar（7z 可解压 rar/7z/exe）", filepath.Base(archive))
	}
	if lastErr == nil {
		lastErr = errors.New("外部解压工具执行失败")
	}
	return fmt.Errorf("解压 %s 失败（已尝试 %s）: %w", filepath.Base(archive), strings.Join(tried, "、"), lastErr)
}

func detectArchive(path string) (string, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", nil
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"),
		strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"),
		strings.HasSuffix(lower, ".tar.zst"):
		return "external", nil
	case strings.HasSuffix(lower, ".zip"):
		return "zip", nil
	case strings.HasSuffix(lower, ".tar"):
		return "tar", nil
	case strings.HasSuffix(lower, ".gz"):
		return "gz", nil
	case strings.HasSuffix(lower, ".7z"):
		return "7z", nil
	case strings.HasSuffix(lower, ".rar"), strings.Contains(lower, ".part") && strings.HasSuffix(lower, ".rar"):
		return "rar", nil
	case strings.HasSuffix(lower, ".exe"):
		return "exe", nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	header := make([]byte, 512)
	n, _ := io.ReadFull(f, header)
	header = header[:n]
	switch {
	case len(header) >= 4 && header[0] == 'P' && header[1] == 'K' && header[2] == 3 && header[3] == 4:
		return "zip", nil
	case len(header) >= 2 && header[0] == 0x1f && header[1] == 0x8b:
		return "gz", nil
	case len(header) >= 6 && string(header[:6]) == "7z\xbc\xaf\x27\x1c":
		return "7z", nil
	case len(header) >= 7 && string(header[:7]) == "Rar!\x1a\x07\x00":
		return "rar", nil
	case len(header) >= 8 && string(header[:8]) == "Rar!\x1a\x07\x01\x00":
		return "rar", nil
	case len(header) >= 262 && string(header[257:262]) == "ustar":
		return "tar", nil
	}
	return "external", nil
}

func applyStrip(rel string, strip int) (string, bool) {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "./")
	if rel == "" || rel == "." {
		return "", false
	}
	if strip <= 0 {
		return rel, true
	}
	parts := strings.Split(rel, "/")
	if strip >= len(parts) {
		return "", false
	}
	return strings.Join(parts[strip:], "/"), true
}

func safeJoin(base, rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "/")
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("压缩包包含不安全路径，已跳过: %s", rel)
	}
	target := filepath.Join(base, clean)
	if !withinInstall(base, target) {
		return "", fmt.Errorf("压缩包路径超出游戏目录，已跳过: %s", rel)
	}
	return target, nil
}

func withinInstall(base, target string) bool {
	baseAbs, err1 := filepath.Abs(base)
	targetAbs, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyInstallFile(src, dst string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ugal-patch-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	_ = os.Chmod(tmpName, mode)
	if err := os.Rename(tmpName, dst); err != nil {
		// Windows 上目标存在时 Rename 会失败；先删除再重试。
		if removeErr := os.Remove(dst); removeErr == nil {
			err = os.Rename(tmpName, dst)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func expandArchivePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		p = filepath.Join(home, p[2:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func truncateOutput(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
