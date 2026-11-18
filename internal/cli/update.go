package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/selfupdate"
)

// defaultSrcDir 返回默认源码目录：数据目录下的 src（默认 ~/.ugal/src）。
func (a *app) defaultSrcDir() string {
	return a.store.SrcDir()
}

func (a *app) cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(a.err)
	srcDir := fs.String("src-dir", "", "源码目录（默认 <数据目录>/src）")
	repo := fs.String("repo", "", "源码仓库（默认 github.com/HanamiSakiP/u-game）")
	branch := fs.String("branch", "", "同步分支（默认 main）")
	method := fs.String("method", "", "同步方式: auto、git、archive")
	check := fs.Bool("check", false, "只检查是否有更新，不下载、不构建")
	force := fs.Bool("force", false, "丢弃 git 本地改动，或强制重新下载源码包")
	noBuild := fs.Bool("no-build", false, "只同步源码，不构建")
	buildOnly := fs.Bool("build-only", false, "不联网，直接用已有源码构建")
	install := fs.String("install", "", "构建后的安装路径（默认覆盖当前可执行文件）")
	timeout := fs.Duration("timeout", 120*time.Second, "网络超时")
	asJSON := fs.Bool("json", false, "输出 JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.err, "用法: ugal update [参数]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("未知参数: %s", strings.Join(fs.Args(), " "))
	}
	if *check && *buildOnly {
		return errors.New("--check 和 --build-only 不能同时使用")
	}
	if *check && *noBuild {
		return errors.New("--check 和 --no-build 不能同时使用")
	}
	if *buildOnly && *noBuild {
		return errors.New("--build-only 和 --no-build 不能同时使用")
	}

	src := firstNonEmpty(*srcDir, a.cfg.UpdateDir, os.Getenv("UGAL_SRC_DIR"), a.defaultSrcDir())
	src, err := selfupdate.ExpandPath(src)
	if err != nil {
		return err
	}
	repoName := firstNonEmpty(*repo, a.cfg.Repo, selfupdate.DefaultRepo)
	branchName := firstNonEmpty(*branch, a.cfg.Branch, selfupdate.DefaultBranch)

	prev := a.loadUpdateState(src, repoName, branchName)
	opts := selfupdate.Options{
		SourceDir:        src,
		Repo:             repoName,
		Branch:           branchName,
		Method:           *method,
		Check:            *check,
		Force:            *force,
		NoBuild:          *noBuild,
		BuildOnly:        *buildOnly,
		Install:          *install,
		Timeout:          *timeout,
		PreviousRevision: prev,
		Logf: func(format string, args ...interface{}) {
			fmt.Fprintf(a.err, format+"\n", args...)
		},
	}

	res, err := selfupdate.Run(context.Background(), opts)
	if err != nil {
		return err
	}
	if !*check {
		a.saveUpdateState(res)
	}

	owner, name, repoErr := selfupdate.ParseRepo(res.Repo)
	if *asJSON {
		var upToDate *bool
		if *check {
			v := !res.Changed
			upToDate = &v
		}
		payload := struct {
			*selfupdate.Result
			UpToDate  *bool  `json:"up_to_date,omitempty"`
			StatePath string `json:"state_path,omitempty"`
		}{
			Result:    res,
			UpToDate:  upToDate,
			StatePath: a.store.UpdatePath(),
		}
		return writeJSONOutput(a.out, payload)
	}

	fmt.Fprintf(a.out, "源码目录 : %s\n", res.SourceDir)
	repoLabel := res.Repo
	if repoErr == nil {
		repoLabel = fmt.Sprintf("github.com/%s/%s", owner, name)
	}
	fmt.Fprintf(a.out, "源码仓库 : %s (分支 %s)\n", repoLabel, res.Branch)
	if res.Method != "" {
		fmt.Fprintf(a.out, "同步方式 : %s\n", updateMethodLabel(res.Method))
	}
	if res.Revision != "" {
		if res.Changed && res.PreviousRevision != "" && res.PreviousRevision != res.Revision {
			fmt.Fprintf(a.out, "源码版本 : %s -> %s\n",
				selfupdate.ShortRevision(res.PreviousRevision), selfupdate.ShortRevision(res.Revision))
		} else {
			fmt.Fprintf(a.out, "源码版本 : %s\n", selfupdate.ShortRevision(res.Revision))
		}
	}
	fmt.Fprintf(a.out, "状态     : %s\n", updateStatusLabel(*check, *noBuild, *buildOnly, res))
	if res.Built {
		fmt.Fprintf(a.out, "可执行   : %s\n", res.Binary)
	}
	if res.ProgramVersion != "" {
		fmt.Fprintf(a.out, "程序版本 : %s\n", res.ProgramVersion)
	}
	if !*check {
		fmt.Fprintf(a.out, "状态文件 : %s\n", a.store.UpdatePath())
	}
	return nil
}

// loadUpdateState 读取上次记录的源码版本，只有目录、仓库、分支一致时才使用。
func (a *app) loadUpdateState(srcDir, repo, branch string) string {
	data, err := a.store.LoadUpdate()
	if err != nil || data == nil {
		return ""
	}
	savedDir, err := selfupdate.ExpandPath(data.SourceDir)
	if err != nil || savedDir != srcDir {
		return ""
	}
	savedOwner, savedName, err1 := selfupdate.ParseRepo(firstNonEmpty(data.Repo, selfupdate.DefaultRepo))
	curOwner, curName, err2 := selfupdate.ParseRepo(repo)
	if err1 != nil || err2 != nil || savedOwner != curOwner || savedName != curName {
		return ""
	}
	if firstNonEmpty(data.Branch, selfupdate.DefaultBranch) != branch {
		return ""
	}
	return data.Revision
}

func (a *app) saveUpdateState(res *selfupdate.Result) {
	if res == nil {
		return
	}
	state := &model.UpdateData{
		SourceDir:        res.SourceDir,
		Repo:             res.Repo,
		Branch:           res.Branch,
		Method:           res.Method,
		Revision:         res.Revision,
		PreviousRevision: res.PreviousRevision,
		Changed:          res.Changed,
		Built:            res.Built,
		Binary:           res.Binary,
		ProgramVersion:   res.ProgramVersion,
	}
	if res.Built {
		state.BuiltAt = time.Now().UTC()
	}
	if err := a.store.SaveUpdate(state); err != nil {
		fmt.Fprintf(a.err, "警告: 保存更新记录失败: %v\n", err)
	}
}

func updateMethodLabel(method string) string {
	switch method {
	case "git":
		return "git（增量同步）"
	case "archive":
		return "源码包（tar.gz）"
	case "local":
		return "本地源码"
	default:
		return method
	}
}

func updateStatusLabel(check, noBuild, buildOnly bool, res *selfupdate.Result) string {
	switch {
	case check && res.Revision == "":
		return "尚未记录源码版本"
	case check && res.Changed:
		return "有更新"
	case check:
		return "已是最新"
	case buildOnly:
		return "已用本地源码构建"
	case noBuild && res.Changed:
		return "源码已更新（未构建）"
	case noBuild:
		return "源码已是最新（未构建）"
	case res.Changed:
		return "已更新并重新构建"
	default:
		return "已是最新，已重新构建"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
