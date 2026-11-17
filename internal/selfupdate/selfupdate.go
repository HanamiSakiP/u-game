// Package selfupdate 把 ugal 源码同步到本地目录，并在本机重新构建可执行文件。
//
// 同步优先使用 git（可以增量更新并保留 .git 历史）；没有 git 或源码目录不是
// git 仓库时，改为下载 GitHub 源码包（tar.gz）并解压。
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultRepo 是 ugal 的默认源码仓库（owner/name）。
	DefaultRepo = "github.com/HanamiSakiP/u-game"
	// DefaultBranch 是默认同步分支。
	DefaultBranch = "main"

	defaultAPIBase     = "https://api.github.com"
	defaultArchiveBase = "https://codeload.github.com"
	defaultGitBase     = "https://github.com"
	userAgent          = "ugal-selfupdate"
)

// Options 控制一次更新流程。
type Options struct {
	// SourceDir 是源码保存目录，必须提供（CLI 默认使用 <当前目录>/.ugal/src）。
	SourceDir string
	// Repo 是 owner/name 或 GitHub 地址，默认 github.com/HanamiSakiP/u-game。
	Repo string
	// Branch 是同步分支，默认 main。
	Branch string
	// Method 是同步方式：auto、git 或 archive。
	Method string
	// Check 为 true 时只检查是否有更新，不下载源码、不构建。
	Check bool
	// Force 为 true 时 git 丢弃本地改动，archive 强制重新下载。
	Force bool
	// NoBuild 为 true 时只同步源码，不构建。
	NoBuild bool
	// BuildOnly 为 true 时不联网，直接用 SourceDir 中已有的源码构建。
	BuildOnly bool
	// Install 是构建后的安装路径；为空时覆盖当前可执行文件。
	Install string
	// PreviousRevision 是上次记录的源码版本，用于判断源码包是否有更新。
	PreviousRevision string
	// Timeout 是网络超时，默认 120 秒。
	Timeout time.Duration
	// APIBase / ArchiveBase / GitBase 用于测试时替换源码托管地址。
	APIBase     string
	ArchiveBase string
	GitBase     string
	// Logf 输出进度，可为空。
	Logf func(format string, args ...interface{})
}

// Result 描述一次更新的结果。
type Result struct {
	SourceDir        string `json:"source_dir"`
	Repo             string `json:"repo"`
	Branch           string `json:"branch"`
	Method           string `json:"method"`
	Revision         string `json:"revision,omitempty"`
	PreviousRevision string `json:"previous_revision,omitempty"`
	Changed          bool   `json:"changed"`
	Built            bool   `json:"built"`
	Binary           string `json:"binary,omitempty"`
	ProgramVersion   string `json:"program_version,omitempty"`
}

// Run 执行更新：同步源码，然后按需本地构建。
func Run(ctx context.Context, opts Options) (*Result, error) {
	opts, err := opts.normalized()
	if err != nil {
		return nil, err
	}
	res := &Result{
		SourceDir:        opts.SourceDir,
		Repo:             opts.Repo,
		Branch:           opts.Branch,
		PreviousRevision: opts.PreviousRevision,
	}

	if !opts.BuildOnly {
		syncCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		err := syncSource(syncCtx, opts, res)
		cancel()
		if err != nil {
			return res, err
		}
	} else {
		if !fileExists(filepath.Join(opts.SourceDir, "go.mod")) {
			return res, fmt.Errorf("源码目录 %s 中没有 go.mod，无法本地构建", opts.SourceDir)
		}
		res.Method = "local"
		res.Revision = localRevision(ctx, opts.SourceDir)
	}

	if opts.Check || opts.NoBuild {
		return res, nil
	}
	if err := buildAndInstall(ctx, opts, res); err != nil {
		return res, err
	}
	return res, nil
}

func (o Options) normalized() (Options, error) {
	o.Install = strings.TrimSpace(o.Install)
	o.Method = strings.ToLower(strings.TrimSpace(o.Method))
	switch o.Method {
	case "", "auto":
		o.Method = "auto"
	case "git", "archive":
	default:
		return o, fmt.Errorf("未知同步方式 %q，可选 auto、git、archive", o.Method)
	}
	if strings.TrimSpace(o.Repo) == "" {
		o.Repo = DefaultRepo
	}
	if strings.TrimSpace(o.Branch) == "" {
		o.Branch = DefaultBranch
	}
	if strings.TrimSpace(o.SourceDir) == "" {
		return o, errors.New("缺少源码目录")
	}
	dir, err := ExpandPath(o.SourceDir)
	if err != nil {
		return o, err
	}
	o.SourceDir = dir
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	if strings.TrimSpace(o.APIBase) == "" {
		o.APIBase = defaultAPIBase
	}
	if strings.TrimSpace(o.ArchiveBase) == "" {
		o.ArchiveBase = defaultArchiveBase
	}
	if strings.TrimSpace(o.GitBase) == "" {
		o.GitBase = defaultGitBase
	}
	return o, nil
}

func syncSource(ctx context.Context, opts Options, res *Result) error {
	method := opts.Method
	if method == "auto" {
		switch {
		case fileExists(filepath.Join(opts.SourceDir, ".git")):
			method = "git"
		case opts.Check:
			// 还没同步源码时，检查更新只需询问远端，不必克隆。
			if gitAvailable() {
				method = "git"
			} else {
				method = "archive"
			}
		case isEmptyDir(opts.SourceDir), !fileExists(opts.SourceDir):
			if gitAvailable() {
				method = "git"
			} else {
				method = "archive"
			}
		default:
			method = "archive"
		}
	}
	if method == "git" {
		err := syncGit(ctx, opts, res)
		if err == nil {
			return nil
		}
		if opts.Method != "auto" {
			return err
		}
		logf(opts, "git 同步失败（%v），改用源码包", err)
		if archiveErr := syncArchive(ctx, opts, res); archiveErr != nil {
			return fmt.Errorf("git 同步失败: %v\n源码包同步失败: %w", err, archiveErr)
		}
		return nil
	}
	return syncArchive(ctx, opts, res)
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func syncGit(ctx context.Context, opts Options, res *Result) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("未找到 git，可改用 `--method archive` 下载源码包")
	}
	owner, name, err := ParseRepo(opts.Repo)
	if err != nil {
		return err
	}
	repoURL := fmt.Sprintf("%s/%s/%s.git", strings.TrimRight(opts.GitBase, "/"), owner, name)
	res.Method = "git"

	// 尚未克隆时，--check 只用 ls-remote 查询远端分支。
	if opts.Check && !fileExists(filepath.Join(opts.SourceDir, ".git")) {
		out, err := gitOutputErr(ctx, "", git, "ls-remote", repoURL, "refs/heads/"+opts.Branch)
		if err != nil {
			return err
		}
		fields := strings.Fields(out)
		if len(fields) == 0 {
			return fmt.Errorf("远端仓库 %s 没有分支 %s", opts.Repo, opts.Branch)
		}
		res.Revision = fields[0]
		res.Changed = res.Revision != opts.PreviousRevision
		return nil
	}

	// 首次获取：克隆到源码目录。
	if !fileExists(filepath.Join(opts.SourceDir, ".git")) {
		if fileExists(opts.SourceDir) && !isEmptyDir(opts.SourceDir) {
			return fmt.Errorf("源码目录 %s 已存在且不是 git 仓库，请改用 `--method archive`，或清空目录后重试", opts.SourceDir)
		}
		if err := os.MkdirAll(filepath.Dir(opts.SourceDir), 0o755); err != nil {
			return fmt.Errorf("创建源码目录失败: %w", err)
		}
		logf(opts, "克隆 %s（分支 %s）到 %s", repoURL, opts.Branch, opts.SourceDir)
		if err := run(ctx, "", git, "clone", "--depth", "1", "--branch", opts.Branch,
			"--single-branch", repoURL, opts.SourceDir); err != nil {
			return err
		}
		res.Changed = true
		res.Revision = gitOutput(ctx, opts.SourceDir, git, "rev-parse", "HEAD")
		return nil
	}

	prev := gitOutput(ctx, opts.SourceDir, git, "rev-parse", "HEAD")
	res.PreviousRevision = prev
	res.Revision = prev

	// 让已有仓库始终跟踪命令指定的仓库，方便换源。
	if err := run(ctx, opts.SourceDir, git, "remote", "set-url", "origin", repoURL); err != nil {
		return err
	}
	logf(opts, "拉取 %s 分支 %s", repoURL, opts.Branch)
	if err := run(ctx, opts.SourceDir, git, "fetch", "--depth", "1", "origin", opts.Branch); err != nil {
		return err
	}
	latest := gitOutput(ctx, opts.SourceDir, git, "rev-parse", "FETCH_HEAD")
	if latest == "" {
		return errors.New("无法读取远端版本")
	}
	res.Revision = latest
	if latest == prev && !opts.Force {
		res.Changed = false
		return nil
	}
	res.Changed = true
	if opts.Check {
		return nil
	}

	if !opts.Force {
		dirty, err := gitDirty(ctx, opts.SourceDir, git)
		if err != nil {
			return err
		}
		if dirty {
			return errors.New("本地源码有未提交改动，已停止更新；请先提交或备份，或加 `--force` 丢弃本地改动")
		}
	}
	// 浅克隆没有可用的合并历史，直接对齐到远端提交。
	if err := run(ctx, opts.SourceDir, git, "reset", "--hard", "FETCH_HEAD"); err != nil {
		return err
	}
	return nil
}

func gitDirty(ctx context.Context, dir, git string) (bool, error) {
	out, err := gitOutputErr(ctx, dir, git, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func syncArchive(ctx context.Context, opts Options, res *Result) error {
	owner, name, err := ParseRepo(opts.Repo)
	if err != nil {
		return err
	}
	res.Method = "archive"
	client := &http.Client{Timeout: opts.Timeout}

	latest, apiErr := latestCommit(ctx, client, opts, owner, name)
	if apiErr != nil {
		if opts.Check {
			return fmt.Errorf("检查更新失败: %w", apiErr)
		}
		logf(opts, "无法获取最新提交（%v），直接下载 %s 分支源码包", apiErr, opts.Branch)
	}

	if opts.Check {
		res.Revision = latest
		res.Changed = latest == "" || latest != opts.PreviousRevision
		return nil
	}

	if latest != "" && latest == opts.PreviousRevision && !opts.Force &&
		fileExists(filepath.Join(opts.SourceDir, "go.mod")) {
		res.Revision = latest
		res.Changed = false
		return nil
	}

	ref := opts.Branch
	if latest != "" {
		ref = latest
	}
	url := archiveURL(opts.ArchiveBase, owner, name, ref)
	logf(opts, "下载源码包 %s", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载源码包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载源码包失败: %s", resp.Status)
	}

	tmp, err := os.CreateTemp("", "ugal-src-*.tar.gz")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("下载源码包失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入源码包失败: %w", err)
	}

	if err := replaceSource(tmpName, opts.SourceDir); err != nil {
		return err
	}
	if latest != "" {
		res.Revision = latest
	} else {
		res.Revision = "archive-" + time.Now().UTC().Format("20060102")
	}
	res.Changed = true
	return nil
}

// replaceSource 解压源码包，并用它整体替换 sourceDir。
func replaceSource(archive, sourceDir string) error {
	parent := filepath.Dir(sourceDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("创建源码目录失败: %w", err)
	}
	work, err := os.MkdirTemp(parent, ".ugal-src-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(work)

	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := extractTarGz(f, work); err != nil {
		return fmt.Errorf("解压源码包失败: %w", err)
	}
	root, err := sourceRoot(work)
	if err != nil {
		return err
	}
	if !fileExists(filepath.Join(root, "go.mod")) {
		return errors.New("下载的源码包中没有 go.mod，可能不是有效的 ugal 源码")
	}

	// 先备份旧目录，移动成功后再删除，避免更新失败时丢失源码。
	backup := ""
	if fileExists(sourceDir) {
		backup = sourceDir + ".old-" + time.Now().UTC().Format("20060102150405")
		if err := os.Rename(sourceDir, backup); err != nil {
			return fmt.Errorf("备份旧源码失败: %w", err)
		}
	}
	if err := os.Rename(root, sourceDir); err != nil {
		if backup != "" {
			_ = os.Rename(backup, sourceDir)
		}
		return fmt.Errorf("安装新源码失败: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func sourceRoot(work string) (string, error) {
	if fileExists(filepath.Join(work, "go.mod")) {
		return work, nil
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(work, e.Name()))
		}
	}
	if len(dirs) == 1 && fileExists(filepath.Join(dirs[0], "go.mod")) {
		return dirs[0], nil
	}
	return "", errors.New("源码包结构异常：找不到包含 go.mod 的目录")
}

func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	dest = filepath.Clean(dest)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if !within(dest, target) {
			return fmt.Errorf("源码包包含非法路径 %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("源码包包含绝对符号链接 %q", hdr.Name)
			}
			resolved := filepath.Join(filepath.Dir(target), hdr.Linkname)
			if !within(dest, resolved) {
				return fmt.Errorf("源码包包含越界符号链接 %q", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildAndInstall(ctx context.Context, opts Options, res *Result) error {
	gobin, err := exec.LookPath("go")
	if err != nil {
		return errors.New("未找到 go，无法本地构建；请安装 Go 后重试，或加 `--no-build` 只同步源码")
	}
	if !fileExists(filepath.Join(opts.SourceDir, "go.mod")) {
		return fmt.Errorf("源码目录 %s 中没有 go.mod，无法本地构建", opts.SourceDir)
	}
	target, err := installPath(opts.Install)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("创建安装目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".new-*"+exeSuffix())
	if err != nil {
		return fmt.Errorf("无法在 %s 创建构建文件（请检查目录权限，或用 `--install` 指定可写路径）: %w",
			filepath.Dir(target), err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	logf(opts, "本地构建：go build -trimpath -o %s ./cmd/ugal", target)
	cmd := exec.CommandContext(ctx, gobin, "build", "-trimpath", "-o", tmpName, "./cmd/ugal")
	cmd.Dir = opts.SourceDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("本地构建失败: %w", err)
		}
		return fmt.Errorf("本地构建失败: %w\n%s", err, msg)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("设置可执行权限失败: %w", err)
	}
	res.ProgramVersion = binaryVersion(ctx, tmpName)

	if err := replaceBinary(tmpName, target); err != nil {
		return err
	}
	res.Built = true
	res.Binary = target
	return nil
}

func replaceBinary(tmp, target string) error {
	if runtime.GOOS == "windows" {
		if err := os.Rename(tmp, target); err != nil {
			fallback := strings.TrimSuffix(target, filepath.Ext(target)) + ".new.exe"
			if moveErr := os.Rename(tmp, fallback); moveErr != nil {
				return fmt.Errorf("无法覆盖正在运行的程序，且保存新版本失败: %w", err)
			}
			return fmt.Errorf("无法覆盖正在运行的程序，新版本已保存为 %s，请关闭 ugal 后手动替换: %w", fallback, err)
		}
		return nil
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("安装新版本到 %s 失败: %w", target, err)
	}
	return nil
}

func installPath(install string) (string, error) {
	if strings.TrimSpace(install) == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("无法确定当前可执行文件位置: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		return exe, nil
	}
	p, err := ExpandPath(install)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return filepath.Join(p, "ugal"+exeSuffix()), nil
	}
	if strings.HasSuffix(install, "/") || strings.HasSuffix(install, string(filepath.Separator)) {
		return filepath.Join(p, "ugal"+exeSuffix()), nil
	}
	return p, nil
}

func binaryVersion(ctx context.Context, binary string) string {
	out, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func latestCommit(ctx context.Context, client *http.Client, opts Options, owner, name string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s", strings.TrimRight(opts.APIBase, "/"), owner, name, url.PathEscape(opts.Branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API %s", resp.Status)
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.SHA), nil
}

func archiveURL(base, owner, name, ref string) string {
	refPath := ref
	if !strings.HasPrefix(ref, "refs/") && !isCommitSHA(ref) {
		refPath = "refs/heads/" + ref
	}
	return fmt.Sprintf("%s/%s/%s/tar.gz/%s", strings.TrimRight(base, "/"), owner, name, refPath)
}

func isCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// ParseRepo 把 github.com/owner/name、https://github.com/owner/name.git
// 或 owner/name 统一解析为 owner 和 name。
func ParseRepo(repo string) (string, string, error) {
	s := strings.TrimSpace(repo)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	s = strings.Replace(s, ":", "/", 1)
	parts := strings.Split(s, "/")
	// 允许 github.com/owner/name 和 owner/name。
	if len(parts) == 3 && strings.Contains(parts[0], ".") && parts[0] != "." && parts[0] != ".." {
		parts = parts[1:]
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("无法解析仓库 %q，请使用 owner/name 格式", repo)
	}
	for _, p := range parts {
		if p == "." || p == ".." || strings.ContainsAny(p, `\ `) {
			return "", "", fmt.Errorf("无法解析仓库 %q", repo)
		}
	}
	return parts[0], parts[1], nil
}

func run(ctx context.Context, dir, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			return fmt.Errorf("%s %s 失败: %w", filepath.Base(bin), strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s 失败: %w\n%s", filepath.Base(bin), strings.Join(args, " "), err, msg)
	}
	return nil
}

func gitOutput(ctx context.Context, dir, git string, args ...string) string {
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitOutputErr(ctx context.Context, dir, git string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			return "", fmt.Errorf("%s %s 失败: %w", filepath.Base(git), strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("%s %s 失败: %w\n%s", filepath.Base(git), strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(buf.String()), nil
}

func localRevision(ctx context.Context, dir string) string {
	if git, err := exec.LookPath("git"); err == nil && fileExists(filepath.Join(dir, ".git")) {
		if rev := gitOutput(ctx, dir, git, "rev-parse", "HEAD"); rev != "" {
			return rev
		}
	}
	return ""
}

// ShortRevision 返回便于显示的短版本号。
func ShortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

func within(dest, target string) bool {
	rel, err := filepath.Rel(dest, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// ExpandPath 展开 ~ 并转换为绝对路径。
func ExpandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
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
		return filepath.Join(home, p[2:]), nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func logf(opts Options, format string, args ...interface{}) {
	if opts.Logf != nil {
		opts.Logf(format, args...)
	}
}
