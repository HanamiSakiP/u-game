package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	powershellBlockStart = "# >>> ugal completion >>>"
	powershellBlockEnd   = "# <<< ugal completion <<<"
)

// runCompletion 生成各 shell 的命令补全脚本。省略 shell 时自动检测当前 shell。
// 默认把脚本写入该 shell 的标准补全位置；--print 只输出到终端。
func runCompletion(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(stderr)
	printOnly := fs.Bool("print", false, "只输出补全脚本，不写入文件")
	install := fs.Bool("install", false, "写入补全脚本（默认行为）")
	dir := fs.String("dir", "", "补全脚本写入目录（默认使用该 shell 的标准位置）")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "用法: ugal completion [shell] [--print|--install] [--dir 目录]")
		fs.PrintDefaults()
	}
	if err := parseArgs(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *printOnly && *install {
		return errors.New("--print 与 --install 不能同时使用")
	}

	rest := fs.Args()
	if len(rest) > 1 {
		return errors.New("completion 最多接受一个 shell 参数")
	}

	shell := ""
	if len(rest) == 0 {
		detected, err := detectShell()
		if err != nil {
			return fmt.Errorf("%w；请显式指定 `ugal completion bash|zsh|fish|powershell`", err)
		}
		shell = detected
	} else {
		value := strings.ToLower(strings.TrimSpace(rest[0]))
		if value == "auto" {
			detected, err := detectShell()
			if err != nil {
				return fmt.Errorf("%w；请显式指定 `ugal completion bash|zsh|fish|powershell`", err)
			}
			shell = detected
		} else {
			normalized, ok := normalizeShell(value)
			if !ok {
				return fmt.Errorf("未知 shell %q，可选 bash、zsh、fish、powershell", rest[0])
			}
			shell = normalized
		}
	}

	script := completionScript(shell)
	if script == "" {
		return fmt.Errorf("不支持 shell %q", shell)
	}
	if *printOnly {
		_, err := io.WriteString(stdout, script)
		return err
	}

	path, err := completionPath(shell, *dir)
	if err != nil {
		return err
	}
	changed, err := writeCompletion(shell, script, path)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(stdout, "已写入 %s 补全: %s\n", shell, path)
	} else {
		fmt.Fprintf(stdout, "补全脚本已是最新: %s\n", path)
	}
	printCompletionHint(shell, path, stderr)
	return nil
}

func completionScript(shell string) string {
	switch shell {
	case "bash":
		return bashCompletion
	case "zsh":
		return zshCompletion
	case "fish":
		return fishCompletion
	case "powershell":
		return powershellCompletion
	default:
		return ""
	}
}

// completionPath 返回补全脚本的标准写入位置。
func completionPath(shell, dir string) (string, error) {
	if dir = strings.TrimSpace(dir); dir != "" {
		name := "ugal"
		switch shell {
		case "zsh":
			name = "_ugal"
		case "fish":
			name = "ugal.fish"
		case "powershell":
			name = "ugal.ps1"
		}
		return filepath.Join(expandUserPath(dir), name), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户主目录: %w", err)
	}
	switch shell {
	case "bash":
		dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(expandUserPath(dataHome), "bash-completion", "completions", "ugal"), nil
	case "zsh":
		zdotdir := strings.TrimSpace(os.Getenv("ZDOTDIR"))
		if zdotdir == "" {
			zdotdir = home
		}
		return filepath.Join(expandUserPath(zdotdir), ".zsh", "completions", "_ugal"), nil
	case "fish":
		configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		return filepath.Join(expandUserPath(configHome), "fish", "completions", "ugal.fish"), nil
	case "powershell":
		return powershellProfile()
	default:
		return "", fmt.Errorf("不支持 shell %q", shell)
	}
}

// writeCompletion 写入补全脚本，返回内容是否有变化。
// PowerShell 的补全需要追加到 $PROFILE，使用标记块保证重复运行不会叠加。
func writeCompletion(shell, script, path string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("创建补全目录 %s 失败: %w", filepath.Dir(path), err)
	}
	if shell != "powershell" {
		existing, err := os.ReadFile(path)
		if err == nil && string(existing) == script {
			return false, nil
		}
		if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
			return false, fmt.Errorf("写入补全脚本 %s 失败: %w", path, err)
		}
		return true, nil
	}

	block := powershellBlock(script)
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("读取 PowerShell 配置 %s 失败: %w", path, err)
	}
	merged := mergeMarkedBlock(string(existing), block)
	if merged == string(existing) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
		return false, fmt.Errorf("写入 PowerShell 配置 %s 失败: %w", path, err)
	}
	return true, nil
}

func powershellBlock(script string) string {
	return powershellBlockStart + "\n" + strings.TrimRight(script, "\n") + "\n" + powershellBlockEnd
}

// mergeMarkedBlock 将 block 写入带标记的位置；已有标记时原地替换。
func mergeMarkedBlock(existing, block string) string {
	start := strings.Index(existing, powershellBlockStart)
	if start < 0 {
		if existing != "" {
			if !strings.HasSuffix(existing, "\n") {
				existing += "\n"
			}
			existing += "\n"
		}
		return existing + block + "\n"
	}
	rest := existing[start:]
	end := strings.Index(rest, powershellBlockEnd)
	if end < 0 {
		return existing[:start] + block + "\n"
	}
	tail := rest[end+len(powershellBlockEnd):]
	return existing[:start] + block + tail
}

func powershellProfile() (string, error) {
	for _, exe := range []string{"pwsh", "powershell"} {
		out, err := exec.Command(exe, "-NoLogo", "-NoProfile", "-Command", "$PROFILE").Output()
		if err == nil {
			if path := strings.TrimSpace(string(out)); path != "" {
				return expandUserPath(path), nil
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户主目录: %w", err)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"), nil
	}
	return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1"), nil
}

func printCompletionHint(shell, path string, stderr io.Writer) {
	switch shell {
	case "bash":
		fmt.Fprintf(stderr, "提示: 新开终端后生效；当前终端可用 `source %s` 立即启用\n", path)
	case "zsh":
		fmt.Fprintln(stderr, "提示: 将以下内容加入 ~/.zshrc 后新开终端生效：")
		fmt.Fprintf(stderr, "  fpath=(\"%s\" $fpath)\n  autoload -Uz compinit && compinit\n", filepath.Dir(path))
	case "fish":
		fmt.Fprintf(stderr, "提示: 新开终端后生效；当前终端可用 `source %s` 立即启用\n", path)
	case "powershell":
		fmt.Fprintf(stderr, "提示: 重新打开 PowerShell 或运行 `. %s` 生效\n", path)
	}
}

func expandUserPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			if len(path) == 1 {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// detectShell 依次从 UGAL_SHELL、父进程、$SHELL 和 PSModulePath 推断当前 shell。
func detectShell() (string, error) {
	if value := strings.TrimSpace(os.Getenv("UGAL_SHELL")); value != "" {
		if shell, ok := normalizeShell(value); ok {
			return shell, nil
		}
		return "", fmt.Errorf("UGAL_SHELL=%q 不是支持的 shell", value)
	}
	if value := parentProcessName(); value != "" {
		if shell, ok := normalizeShell(value); ok {
			return shell, nil
		}
	}
	if value := strings.TrimSpace(os.Getenv("SHELL")); value != "" {
		if shell, ok := normalizeShell(value); ok {
			return shell, nil
		}
	}
	if strings.TrimSpace(os.Getenv("PSModulePath")) != "" {
		return "powershell", nil
	}
	return "", errors.New("无法自动检测 shell")
}

// normalizeShell 把 bash、/bin/zsh、pwsh.exe 等名称统一为补全脚本类型。
func normalizeShell(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimPrefix(name, "-")
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "bash", "rbash":
		return "bash", true
	case "zsh":
		return "zsh", true
	case "fish":
		return "fish", true
	case "pwsh", "powershell":
		return "powershell", true
	default:
		return "", false
	}
}

func parentProcessName() string {
	ppid := os.Getppid()
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(ppid), "comm"))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(ppid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

const bashCompletion = `# bash completion for ugal
_ugal_complete() {
    local cur prev cmd i w
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    cmd=""
    for ((i=1; i<COMP_CWORD; i++)); do
        w="${COMP_WORDS[i]}"
        case "$w" in
            --data-dir|-d) ((i++)) ;;
            -*) ;;
            *) cmd="$w"; break ;;
        esac
    done
    local commands="tui sync scan list missing extra libs play system cover fav patch save update config cookie completion version help"
    local globals="--data-dir -d --help -h --version -v"

    case "$prev" in
        --cookie-file|--src-dir|--install|--file|--local-dir|--game-dir|--data-dir|-d)
            COMPREPLY=($(compgen -f -- "$cur")); return 0 ;;
        --source)
            if [ "$cmd" = "cover" ]; then
                COMPREPLY=($(compgen -W "auto local cdn" -- "$cur"))
            else
                COMPREPLY=($(compgen -W "auto steamdb community api" -- "$cur"))
            fi
            return 0 ;;
        --style)
            COMPREPLY=($(compgen -W "auto portrait header logo" -- "$cur")); return 0 ;;
        --method)
            COMPREPLY=($(compgen -W "auto git archive" -- "$cur")); return 0 ;;
        --type)
            COMPREPLY=($(compgen -W "game music all" -- "$cur")); return 0 ;;
    esac

    if [ -z "$cmd" ]; then
        COMPREPLY=($(compgen -W "$commands $globals" -- "$cur")); return 0
    fi

    local opts=""
    case "$cmd" in
        sync) opts="--source --cookie --cookie-file --user-agent --browser --browser-timeout --timeout" ;;
        scan) opts="--include-partial --include-hidden" ;;
        list|missing|extra) opts="--json --owned --installed --missing --extra --type --limit --paths --include-hidden" ;;
        libs|system) opts="--json" ;;
        play) opts="--list --json --player" ;;
        cover) opts="--list --force --style --source --type --concurrency --timeout --retry --json --paths" ;;
        fav) opts="list add status remove export import import-patches clear --name --note --status --json --installed --missing --paths --offline --replace --clear --all" ;;
        patch) opts="list set remove check install hash md5 sha256 --json --all --name --hash --file --md5 --sha256 --applied --not-applied --offline --dir --game-dir --strip --backup --dry-run" ;;
        save) opts="list sync restore remove clear --json --all --no-cloud --no-local --cloud-only --local-only --local-dir --game-dir --steam-dir --steam-user --force --mirror --dry-run --paths --keep --offline" ;;
        update) opts="--src-dir --repo --branch --method --check --force --no-build --build-only --install --timeout --json" ;;
        config) opts="steamid steam-user steam-dir source user-agent update-dir repo branch" ;;
        cookie) opts="status get show path clear --print --browser-timeout" ;;
        completion) opts="bash zsh fish powershell --print --install --dir" ;;
    esac
    COMPREPLY=($(compgen -W "$opts $globals" -- "$cur"))
}
complete -F _ugal_complete ugal
`

const zshCompletion = `#compdef ugal

_ugal() {
    local -a commands options
    commands=(
        'tui:打开全屏交互界面'
        'sync:从 SteamDB 抓取已拥有游戏'
        'scan:扫描本地 Steam 库'
        'list:列出已拥有游戏及安装状态'
        'missing:只列出未安装的游戏'
        'extra:只列出本地独有的游戏'
        'libs:列出 Steam 库目录'
        'play:用本地播放器播放音乐原声带'
        'system:检测并记录系统环境'
        'cover:获取或下载游戏封面'
        'fav:本地收藏夹'
        'patch:游戏补丁记录与自动安装'
        'save:云存档 / 本地存档同步'
        'update:同步源码并重新构建'
        'config:查看或修改配置'
        'cookie:获取、查看或删除 SteamDB Cookie'
        'completion:生成命令补全脚本'
        'version:显示版本'
        'help:显示帮助'
    )
    if (( CURRENT == 2 )); then
        _describe -t commands 'ugal 命令' commands
        return
    fi

    case ${words[2]} in
        sync)
            options=(
                '--source:来源 (auto/steamdb/community/api)'
                '--cookie:SteamDB Cookie'
                '--cookie-file:Cookie 文件'
                '--user-agent:请求 User-Agent'
                '--browser:用 Firefox 完成 Cloudflare 验证'
                '--browser-timeout:浏览器验证等待时间'
                '--timeout:网络超时'
            ) ;;
        scan)
            options=(
                '--include-partial:保留未完整安装的条目'
                '--include-hidden:保留默认隐藏的 Steam 运行库 / 工具'
            ) ;;
        list|missing|extra)
            options=(
                '--json:输出 JSON'
                '--owned:只看已拥有'
                '--installed:只看本地已安装'
                '--missing:只看未安装'
                '--extra:只看本地独有'
                '--type:游戏或音乐 (game/music)'
                '--limit:限制显示行数'
                '--paths:显示完整安装路径'
                '--include-hidden:显示默认隐藏的 Steam 运行库 / 工具'
            ) ;;
        libs|system)
            options=( '--json:输出 JSON' ) ;;
        play)
            options=(
                '--list:只列出可播放的原声带'
                '--player:指定播放器命令'
                '--json:输出 JSON'
            ) ;;
        cover)
            options=(
                '--list:只查看封面状态'
                '--force:重新获取已有封面'
                '--style:封面样式 (auto/portrait/header/logo)'
                '--source:封面来源 (auto/local/cdn)'
                '--type:类型 (game/music/all)'
                '--concurrency:并发下载数'
                '--timeout:单次下载超时'
                '--retry:失败后的重试次数'
                '--json:输出 JSON'
                '--paths:显示完整封面路径'
            ) ;;
        fav)
            options=(
                'list:查看收藏夹'
                'add:添加收藏'
                'status:查看或设置收藏状态'
                'remove:删除收藏'
                'export:导出收藏夹'
                'import:导入收藏夹'
                'import-patches:从 patches.json 导入收藏'
                'clear:清空收藏夹'
                '--name:指定游戏名称'
                '--note:备注'
                '--status:收藏状态（想玩/在玩/已通关/搁置）'
                '--json:输出 JSON'
                '--installed:只看已安装'
                '--missing:只看未安装'
                '--paths:显示完整封面路径'
                '--offline:不联网获取发行商'
                '--replace:替换现有收藏'
                '--clear:清除收藏状态'
                '--all:清空收藏夹'
            ) ;;
        patch)
            options=(
                'list:查看补丁记录'
                'set:设置补丁记录'
                'remove:删除补丁记录'
                'check:检查本地补丁文件是否存在且哈希正确'
                'install:解压补丁到游戏目录并替换同名文件'
                'hash:计算文件 MD5（默认）'
                'md5:计算文件 MD5'
                'sha256:计算文件 SHA-256'
                '--json:输出 JSON'
                '--all:包含没有记录的游戏'
                '--name:补丁名称'
                '--hash:补丁哈希值'
                '--file:补丁文件（可重复指定）'
                '--md5:计算文件 MD5'
                '--sha256:计算文件 SHA-256'
                '--applied:标记该 appid 的全部补丁为已打补丁'
                '--not-applied:标记该 appid 的全部补丁为未打补丁'
                '--offline:不联网获取缺失的游戏名称'
                '--dir:补丁文件目录（默认 <数据目录>/patches）'
                '--game-dir:覆盖游戏目录'
                '--strip:去掉压缩包顶层目录层数'
                '--backup:替换前备份原文件'
                '--dry-run:只显示将写入的文件'
            ) ;;
        save)
            options=(
                'list:查看已同步的存档'
                'sync:同步云存档和本地存档到数据目录'
                'restore:把备份写回云存档 / 游戏目录'
                'remove:删除存档记录和备份'
                'clear:清空所有存档记录和备份'
                '--json:输出 JSON'
                '--all:处理所有记录'
                '--no-cloud:不同步/还原云存档'
                '--no-local:不同步/还原本地存档'
                '--cloud-only:只处理云存档'
                '--local-only:只处理本地存档'
                '--local-dir:显式指定本地存档目录'
                '--game-dir:覆盖游戏目录'
                '--steam-dir:Steam 根目录'
                '--steam-user:Steam 账号 ID'
                '--force:覆盖较新的文件'
                '--mirror:删除多余文件'
                '--dry-run:只统计不写文件'
                '--paths:显示完整存档路径'
                '--keep:只删除记录保留备份'
                '--offline:输出游戏名时不联网'
            ) ;;
        update)
            options=(
                '--src-dir:源码目录'
                '--repo:源码仓库'
                '--branch:同步分支'
                '--method:同步方式 (auto/git/archive)'
                '--check:只检查更新'
                '--force:丢弃本地改动'
                '--no-build:只同步源码'
                '--build-only:用已有源码构建'
                '--install:安装路径'
                '--timeout:网络超时'
                '--json:输出 JSON'
            ) ;;
        config)
            options=(
                'steamid:SteamID 或 AccountID'
                'steam-user:Steam 账号 ID（userdata 下的数字目录）'
                'steam-dir:Steam 根目录'
                'source:抓取来源'
                'user-agent:请求 User-Agent'
                'update-dir:自我更新的源码目录'
                'repo:源码仓库'
                'branch:同步分支'
            ) ;;
        cookie)
            options=(
                'status:查看 Cookie 状态'
                'get:用 Firefox 获取 Cookie'
                'show:显示已保存的 Cookie'
                'path:显示 Cookie 文件路径'
                'clear:删除已保存的 Cookie'
                '--print:获取后输出 Cookie'
                '--browser-timeout:浏览器验证等待时间'
            ) ;;
        completion)
            options=(
                'bash:Bash 补全'
                'zsh:Zsh 补全'
                'fish:Fish 补全'
                'powershell:PowerShell 补全'
                '--print:只输出不写入'
                '--install:写入补全脚本'
                '--dir:指定写入目录'
            ) ;;
        *)
            options=(
                '--data-dir:数据目录'
                '--help:显示帮助'
                '--version:显示版本'
            ) ;;
    esac
    _describe -t options 'ugal 选项' options
}

if (( $+CURRENT )); then
    _ugal "$@"
else
    compdef _ugal ugal 2>/dev/null || true
fi
`

const fishCompletion = `# fish completion for ugal
complete -c ugal -f

complete -c ugal -n '__fish_use_subcommand' -a tui -d '打开全屏交互界面'
complete -c ugal -n '__fish_use_subcommand' -a sync -d '从 SteamDB 抓取已拥有游戏'
complete -c ugal -n '__fish_use_subcommand' -a scan -d '扫描本地 Steam 库'
complete -c ugal -n '__fish_use_subcommand' -a list -d '列出已拥有游戏及安装状态'
complete -c ugal -n '__fish_use_subcommand' -a missing -d '只列出未安装的游戏'
complete -c ugal -n '__fish_use_subcommand' -a extra -d '只列出本地独有的游戏'
complete -c ugal -n '__fish_use_subcommand' -a libs -d '列出 Steam 库目录'
complete -c ugal -n '__fish_use_subcommand' -a play -d '用本地播放器播放音乐原声带'
complete -c ugal -n '__fish_use_subcommand' -a system -d '检测并记录系统环境'
complete -c ugal -n '__fish_use_subcommand' -a cover -d '获取或下载游戏封面'
complete -c ugal -n '__fish_use_subcommand' -a fav -d '本地收藏夹'
complete -c ugal -n '__fish_use_subcommand' -a patch -d '游戏补丁记录与自动安装'
complete -c ugal -n '__fish_use_subcommand' -a save -d '云存档 / 本地存档同步'
complete -c ugal -n '__fish_use_subcommand' -a update -d '同步源码并重新构建'
complete -c ugal -n '__fish_use_subcommand' -a config -d '查看或修改配置'
complete -c ugal -n '__fish_use_subcommand' -a cookie -d '获取、查看或删除 SteamDB Cookie'
complete -c ugal -n '__fish_use_subcommand' -a completion -d '生成命令补全脚本'
complete -c ugal -n '__fish_use_subcommand' -a version -d '显示版本'
complete -c ugal -n '__fish_use_subcommand' -a help -d '显示帮助'

complete -c ugal -l data-dir -s d -r -d '数据目录'
complete -c ugal -l help -d '显示帮助'
complete -c ugal -l version -d '显示版本'

complete -c ugal -n '__fish_seen_subcommand_from sync' -l source -xa 'auto steamdb community api' -d '抓取来源'
complete -c ugal -n '__fish_seen_subcommand_from sync' -l cookie -r -d 'SteamDB Cookie'
complete -c ugal -n '__fish_seen_subcommand_from sync' -l cookie-file -r -F -d 'Cookie 文件'
complete -c ugal -n '__fish_seen_subcommand_from sync' -l user-agent -r -d '请求 User-Agent'
complete -c ugal -n '__fish_seen_subcommand_from sync' -l browser -d '用 Firefox 完成 Cloudflare 验证'
complete -c ugal -n '__fish_seen_subcommand_from sync' -l browser-timeout -r -d '浏览器验证等待时间'
complete -c ugal -n '__fish_seen_subcommand_from sync' -l timeout -r -d '网络超时'

complete -c ugal -n '__fish_seen_subcommand_from scan' -l include-partial -d '保留未完整安装的条目'
complete -c ugal -n '__fish_seen_subcommand_from scan' -l include-hidden -d '保留默认隐藏的 Steam 运行库 / 工具'

complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l json -d '输出 JSON'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l owned -d '只看已拥有'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l installed -d '只看本地已安装'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l missing -d '只看未安装'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l extra -d '只看本地独有'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l type -xa 'game music' -d '类型'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l limit -r -d '限制显示行数'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l paths -d '显示完整安装路径'
complete -c ugal -n '__fish_seen_subcommand_from list missing extra' -l include-hidden -d '显示默认隐藏的 Steam 运行库 / 工具'

complete -c ugal -n '__fish_seen_subcommand_from libs system' -l json -d '输出 JSON'

complete -c ugal -n '__fish_seen_subcommand_from play' -l list -d '只列出可播放的原声带'
complete -c ugal -n '__fish_seen_subcommand_from play' -l player -r -d '指定播放器命令'
complete -c ugal -n '__fish_seen_subcommand_from play' -l json -d '输出 JSON'

complete -c ugal -n '__fish_seen_subcommand_from cover' -l list -d '只查看封面状态'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l force -d '重新获取已有封面'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l style -xa 'auto portrait header logo' -d '封面样式'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l source -xa 'auto local cdn' -d '封面来源'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l type -xa 'game music all' -d '类型'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l concurrency -r -d '并发下载数'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l timeout -r -d '单次下载超时'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l retry -r -d '失败后的重试次数'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l json -d '输出 JSON'
complete -c ugal -n '__fish_seen_subcommand_from cover' -l paths -d '显示完整封面路径'

complete -c ugal -n '__fish_seen_subcommand_from fav' -a 'list add status remove export import import-patches clear'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l name -r -d '指定游戏名称'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l note -r -d '备注'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l status -xa '想玩 在玩 已通关 搁置 clear' -d '收藏状态'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l clear -d '清除收藏状态'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l json -d '输出 JSON'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l installed -d '只看已安装'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l missing -d '只看未安装'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l paths -d '显示完整封面路径'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l offline -d '不联网获取发行商'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l replace -d '替换现有收藏'
complete -c ugal -n '__fish_seen_subcommand_from fav' -l all -d '清空收藏夹'

complete -c ugal -n '__fish_seen_subcommand_from patch' -a 'list set remove check install hash md5 sha256'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l json -d '输出 JSON'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l all -d '包含没有记录的游戏'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l name -r -d '补丁名称'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l hash -r -d '补丁哈希值'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l file -r -F -d '补丁文件（可重复指定）'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l md5 -d '计算文件 MD5'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l sha256 -d '计算文件 SHA-256'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l applied -d '标记该 appid 的全部补丁为已打补丁'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l not-applied -d '标记该 appid 的全部补丁为未打补丁'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l offline -d '不联网获取缺失的游戏名称'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l dir -r -F -d '补丁文件目录（默认 <数据目录>/patches）'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l game-dir -r -F -d '覆盖游戏目录'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l strip -r -d '去掉压缩包顶层目录层数'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l backup -d '替换前备份原文件'
complete -c ugal -n '__fish_seen_subcommand_from patch' -l dry-run -d '只显示将写入的文件'

complete -c ugal -n '__fish_seen_subcommand_from save' -a 'list sync restore remove clear'
complete -c ugal -n '__fish_seen_subcommand_from save' -l json -d '输出 JSON'
complete -c ugal -n '__fish_seen_subcommand_from save' -l all -d '处理所有记录'
complete -c ugal -n '__fish_seen_subcommand_from save' -l no-cloud -d '不同步/还原云存档'
complete -c ugal -n '__fish_seen_subcommand_from save' -l no-local -d '不同步/还原本地存档'
complete -c ugal -n '__fish_seen_subcommand_from save' -l cloud-only -d '只处理云存档'
complete -c ugal -n '__fish_seen_subcommand_from save' -l local-only -d '只处理本地存档'
complete -c ugal -n '__fish_seen_subcommand_from save' -l local-dir -r -F -d '本地存档目录'
complete -c ugal -n '__fish_seen_subcommand_from save' -l game-dir -r -F -d '覆盖游戏目录'
complete -c ugal -n '__fish_seen_subcommand_from save' -l steam-dir -r -F -d 'Steam 根目录'
complete -c ugal -n '__fish_seen_subcommand_from save' -l steam-user -r -d 'Steam 账号 ID'
complete -c ugal -n '__fish_seen_subcommand_from save' -l force -d '覆盖较新的文件'
complete -c ugal -n '__fish_seen_subcommand_from save' -l mirror -d '删除多余文件'
complete -c ugal -n '__fish_seen_subcommand_from save' -l dry-run -d '只统计不写文件'
complete -c ugal -n '__fish_seen_subcommand_from save' -l paths -d '显示完整存档路径'
complete -c ugal -n '__fish_seen_subcommand_from save' -l keep -d '只删除记录保留备份'
complete -c ugal -n '__fish_seen_subcommand_from save' -l offline -d '输出游戏名时不联网'

complete -c ugal -n '__fish_seen_subcommand_from update' -l src-dir -r -d '源码目录'
complete -c ugal -n '__fish_seen_subcommand_from update' -l repo -r -d '源码仓库'
complete -c ugal -n '__fish_seen_subcommand_from update' -l branch -r -d '同步分支'
complete -c ugal -n '__fish_seen_subcommand_from update' -l method -xa 'auto git archive' -d '同步方式'
complete -c ugal -n '__fish_seen_subcommand_from update' -l check -d '只检查更新'
complete -c ugal -n '__fish_seen_subcommand_from update' -l force -d '丢弃本地改动'
complete -c ugal -n '__fish_seen_subcommand_from update' -l no-build -d '只同步源码'
complete -c ugal -n '__fish_seen_subcommand_from update' -l build-only -d '用已有源码构建'
complete -c ugal -n '__fish_seen_subcommand_from update' -l install -r -d '安装路径'
complete -c ugal -n '__fish_seen_subcommand_from update' -l timeout -r -d '网络超时'
complete -c ugal -n '__fish_seen_subcommand_from update' -l json -d '输出 JSON'

complete -c ugal -n '__fish_seen_subcommand_from config' -a 'steamid steam-dir source user-agent update-dir repo branch'

complete -c ugal -n '__fish_seen_subcommand_from cookie' -a 'status get show path clear'
complete -c ugal -n '__fish_seen_subcommand_from cookie' -l print -d '获取后输出 Cookie'
complete -c ugal -n '__fish_seen_subcommand_from cookie' -l browser-timeout -r -d '浏览器验证等待时间'

complete -c ugal -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish powershell'
complete -c ugal -n '__fish_seen_subcommand_from completion' -l print -d '只输出不写入'
complete -c ugal -n '__fish_seen_subcommand_from completion' -l install -d '写入补全脚本'
complete -c ugal -n '__fish_seen_subcommand_from completion' -l dir -r -d '指定写入目录'
`

const powershellCompletion = `# PowerShell completion for ugal
Register-ArgumentCompleter -Native -CommandName ugal -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $elements = $commandAst.CommandElements
    $cmd = if ($elements.Count -gt 1) { $elements[1].Value } else { '' }
    $commands = @('tui','sync','scan','list','missing','extra','libs','play','system','cover','fav','patch','save','update','config','cookie','completion','version','help')
    $globalOptions = @('--data-dir','-d','--help','-h','--version','-v')
    $options = switch ($cmd) {
        'sync' { @('--source','--cookie','--cookie-file','--user-agent','--browser','--browser-timeout','--timeout') }
        'scan' { @('--include-partial','--include-hidden') }
        'list' { @('--json','--owned','--installed','--missing','--extra','--type','--limit','--paths','--include-hidden') }
        'missing' { @('--json','--type','--limit','--paths','--include-hidden') }
        'extra' { @('--json','--type','--limit','--paths','--include-hidden') }
        'libs' { @('--json') }
        'play' { @('--list','--json','--player') }
        'system' { @('--json') }
        'cover' { @('--list','--force','--style','--source','--type','--concurrency','--timeout','--retry','--json','--paths') }
        'fav' { @('list','add','status','remove','export','import','import-patches','clear','--name','--note','--status','--json','--installed','--missing','--paths','--offline','--replace','--clear','--all') }
        'patch' { @('list','set','remove','check','install','hash','md5','sha256','--json','--all','--name','--hash','--file','--md5','--sha256','--applied','--not-applied','--offline','--dir','--game-dir','--strip','--backup','--dry-run') }
        'save' { @('list','sync','restore','remove','clear','--json','--all','--no-cloud','--no-local','--cloud-only','--local-only','--local-dir','--game-dir','--steam-dir','--steam-user','--force','--mirror','--dry-run','--paths','--keep','--offline') }
        'update' { @('--src-dir','--repo','--branch','--method','--check','--force','--no-build','--build-only','--install','--timeout','--json') }
        'config' { @('steamid','steam-user','steam-dir','source','user-agent','update-dir','repo','branch') }
        'cookie' { @('status','get','show','path','clear','--print','--browser-timeout') }
        'completion' { @('bash','zsh','fish','powershell','--print','--install','--dir') }
        default { @() }
    }
    $candidates = if ($elements.Count -le 2) { $commands + $globalOptions } else { $options + $globalOptions }
    $candidates | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`
