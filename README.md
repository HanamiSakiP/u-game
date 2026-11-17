# ugal

> 当前版本：`0.0.1`

`ugal` 是一个游戏库管理命令行工具：

1. 通过 `https://steamdb.info/calculator/<steamid>` 获取账号全部已拥有游戏；
2. 通过本地 Steam 的 `steamapps/libraryfolders.vdf` 和 `appmanifest_*.acf`
   获取所有库目录、已安装游戏、音乐原声带、安装目录名与占用空间；
3. 对比两者，列出「已拥有未安装」「本地已安装但未同步」的游戏；
4. 检测并记录当前系统环境（发行版、内核、桌面、会话等）；
5. 为本地已安装游戏获取封面，优先复制 Steam 本地缓存，缺失时从 Steam CDN 下载；
6. 维护本地游戏收藏夹，支持 JSON / appid 列表导入与导出；
7. 记录游戏补丁状态（补丁名称、哈希、是否已打补丁），没有记录时显示「无补丁」；
8. 自我更新：源码内嵌在二进制中，任意命令自动写入 `./.ugal/src`；`ugal update` 同步远程更新并在本机重新构建。

数据默认保存在 `~/.ugal`；如果当前目录下的 `.ugal` 是数据目录（空目录，或含有
`config.json`、`owned.json` 等数据文件），则优先使用它（方便把数据放在项目里）。
只包含 `src/` 源码的 `.ugal` 不会影响数据目录。也可以用 `--data-dir` 或
`UGAL_DIR` 指定数据目录。

## 安装

```bash
go install github.com/HanamiSakiP/u-game/cmd/ugal@latest
```


确保 `$(go env GOPATH)/bin` 在 `PATH` 中。

也可以在本仓库内直接构建：

```bash
go build -o ugal ./cmd/ugal
```
```bash
go install ./cmd/ugal
```

安装后可以随时同步源码并重新构建：

```bash
ugal update          # 同步当前目录 .ugal/src 并覆盖当前可执行文件
ugal update --check  # 只检查是否有新版本
```

运行任意 `ugal` 命令时，如果当前目录的 `.ugal/src` 还不存在，会自动把二进制
内嵌的源码写入该目录。

## 使用

```bash
# 1. 从 SteamDB 抓取已拥有游戏
ugal sync 76561198000000000

# 2. 扫描本地 Steam 库（自动探测常见安装位置）
ugal scan

# 也可以手动指定 Steam 根目录
ugal scan "D:\SteamLibrary"
ugal scan ~/.local/share/Steam

# 3. 查看对比结果
ugal list                 # 全部已拥有游戏 + 安装状态
ugal list --missing       # 已拥有但未安装
ugal list --installed     # 本地已安装
ugal extra                # 本地已安装但 SteamDB 未记录（家庭共享、免费游戏等）
ugal libs                 # 库目录、游戏/音乐数量、占用空间

# 4. 检测并记录系统环境
ugal system               # 写入 system.json
ugal system --json        # 机器可读输出

# 5. 获取已安装游戏封面
ugal cover                # 跳过已有封面，缺失时从 CDN 下载并自动重试
ugal cover 440 220        # 只处理指定 appid
ugal cover --list         # 查看哪些游戏已有封面
ugal cover --force        # 重新获取已有封面
ugal cover --retry 3      # 下载失败时最多重试 3 次

# 6. 本地收藏夹
ugal fav                  # 查看收藏夹
ugal fav add 440 220      # 添加游戏（自动补全名称）
ugal fav add 440 --name "Team Fortress 2" --note "常玩"
ugal fav export fav.json  # 导出 JSON
ugal fav import fav.json  # 导入 JSON（合并）
ugal fav import fav.json --replace  # 用导入内容替换

# 7. 补丁记录
ugal patch                        # 查看已有补丁记录
ugal patch list --all             # 查看已知游戏及补丁状态（默认「无补丁」）
ugal patch set 440 --name "HD Patch" --file patch.xp3 --applied
ugal patch set 440 --hash <sha256> --not-applied
ugal patch remove 440             # 删除记录（恢复为「无补丁」）
ugal patch hash patch.xp3         # 只计算 SHA-256

# 8. 自我更新：同步源码到本地目录并重新构建
ugal update                       # 同步 + 本地构建，覆盖当前可执行文件
ugal update --check               # 只检查是否有新版本
ugal update --no-build            # 只同步源码
ugal update --build-only          # 不联网，用已有源码重新构建
```

常用参数：

```text
list --json                 输出 JSON
list --type game|music      按类型过滤
list --paths                显示完整安装路径
list --limit 50             限制显示行数
scan --include-partial      保留未完整安装的条目
sync --source steamdb       强制来源（auto/steamdb/community/api）
sync --cookie-file FILE     使用浏览器导出的 SteamDB Cookie
sync --timeout 60s          网络超时

cover --style auto          封面样式（auto/portrait/header/logo）
cover --source auto         封面来源（auto/local/cdn）
cover --type game|music     默认只处理游戏
cover --concurrency 4       并发下载数
cover --timeout 30s         单次下载超时
cover --retry 2             失败后的重试次数（0 表示不重试）

fav list --installed        只看已安装的收藏
fav list --missing          只看未安装的收藏
fav list --json             机器可读输出
fav remove 440              删除收藏
fav remove --all            清空收藏夹

patch list --all            包含没有补丁记录的已知游戏
patch list --json           机器可读输出
patch set --file FILE       计算并记录文件 SHA-256
patch set --applied         标记为已打补丁
patch set --not-applied     标记为未打补丁

update --check              只检查是否有更新
update --no-build           只同步源码，不构建
update --build-only         用已有源码重新构建（不联网）
update --method git|archive 指定同步方式
update --src-dir DIR        源码目录（默认 <当前目录>/.ugal/src）
update --repo OWNER/NAME    源码仓库
update --branch NAME        同步分支
update --install PATH       安装到指定路径
update --force              git 丢弃本地改动 / 强制重新下载
```

## SteamDB 与 Cloudflare

SteamDB 使用 Cloudflare 校验，服务器 / 数据中心网络可能直接收到验证页。
此时 `ugal sync` 会自动回退到：

1. Steam 社区公开游戏列表（`steamcommunity.com/.../games?tab=all&xml=1`）；
2. Steam 官方 Web API（需要环境变量 `STEAM_API_KEY`）。

如果希望坚持使用 SteamDB，可以在浏览器通过验证后导出 Cookie，放到：

```text
~/.ugal/cookie.txt
```

支持两种内容：

- 浏览器「Copy as cURL」里的 `Cookie:` 请求头内容；
- Netscape 格式的 `cookies.txt`（如 Cookie-Editor / Get cookies.txt 导出）。

也可以通过 `ugal sync --cookie-file cookies.txt` 或环境变量
`UGAL_STEAMDB_COOKIE` 临时指定。

Cloudflare 的 Cookie 通常与浏览器 User-Agent 绑定；如果仍然返回 403，
请把浏览器 UA 一起传给 ugal：

```bash
ugal sync 76561198000000000 \
  --cookie-file cookies.txt \
  --user-agent "Mozilla/5.0 ... Chrome/131.0.0.0 Safari/537.36"
```

也可以写进配置：`ugal config user-agent "Mozilla/5.0 ..."`。

> 请遵守 SteamDB 的使用条款，不要高频抓取。游戏列表也可以直接使用
> Steam 官方 API 获取。

## 配置

```bash
ugal config                                  # 查看配置与文件位置
ugal config steamid 76561198000000000        # 记住 SteamID
ugal config steam-dir "D:\SteamLibrary"      # 记住 Steam 根目录
ugal config source auto                      # auto/steamdb/community/api
ugal config update-dir ~/ugal-src            # 自我更新的源码目录
ugal config repo "HanamiSakiP/u-game"        # 自我更新的源码仓库
ugal config branch main                      # 自我更新的分支
```

配置也保存在 `~/.ugal/config.json`。

## 系统记录

`ugal system` 会检测当前系统并写入 `system.json`，`ugal scan` 也会自动记录一次。
包含：

- `os` / `arch`：如 `linux` / `amd64`；
- `platform` / `distro` / `distro_version`：读取 `/etc/os-release`；
- `kernel`：Linux 内核版本；
- `desktop` / `session_type`：桌面环境与 X11/Wayland；
- `hostname` / `shell` / `locale` / `go_version`；
- `steam_root`：最近一次扫描到的 Steam 根目录。

不读取任何账号或机器唯一标识，只记录公开的系统环境信息。

## 游戏封面

`ugal cover` 默认处理 `installed.json` 中已完整安装的游戏：

1. 如果 `.ugal/covers/<appid>.<ext>` 已存在，直接跳过；
2. 在 Steam 本地缓存中查找
   `<Steam>/appcache/librarycache/<appid>_library_600x900.jpg`
   （以及 header、capsule、logo 等），找到就复制；
3. 仍然缺失时，从 Steam CDN 下载
   `https://cdn.cloudflare.steamstatic.com/steam/apps/<appid>/library_600x900.jpg`，
   依次回退到 header、capsule 和 logo。

封面文件名固定为 `<appid>.jpg` / `.png` / `.webp`，方便脚本和收藏夹引用。
`--source local` 只使用本地缓存，`--source cdn` 只从网络下载。

默认会**跳过已有封面**，不会重复下载；需要更新时加 `--force`。
每处理完一个游戏，会向 stderr 输出一行进度，例如：

```text
[1/3] 跳过     440 Team Fortress 2
[2/3] 下载     220 Half-Life 2
[3/3] 失败     999 Unknown: 404 Not Found
```

下载失败会按 `--retry`（默认 2 次）自动重试，进度行会标注重试次数；
`--json` 输出保持干净，不混入进度日志，便于脚本处理。

## 本地收藏夹

收藏夹保存在 `favorites.json`，可按 appid 添加、删除、筛选，并支持导入导出：

```bash
ugal fav add 440 220
ugal fav list --json
ugal fav export favorites.json
ugal fav import favorites.json          # 与现有收藏合并
ugal fav import favorites.json --replace # 替换现有收藏
ugal fav remove 220
ugal fav clear
```

导入时支持：

- `ugal fav export` 生成的 JSON；
- `{"games":[{"appid":440,"name":"Team Fortress 2"}]}`；
- `[440, 220]` 或 `["440", "220"]`；
- `{"440":"Team Fortress 2","220":"Half-Life 2"}`；
- 每行一个 appid，或 `appid,名称` / `appid<TAB>名称` 的文本。

导出内容包含 `version`、`updated_at`、`count` 和 `games`，可以直接备份或迁移到
另一台机器。

## 补丁记录

补丁状态保存在 `patches.json`，每个 appid 最多一条记录：

| 情况 | 状态 |
| --- | --- |
| 没有记录 | `无补丁`（默认） |
| 有记录，`applied = false` | `未打补丁` |
| 有记录，`applied = true` | `已打补丁` |

```bash
ugal patch set 440 --name "HD Patch" --file patch.xp3 --applied
ugal patch set 440 --hash sha256:abcd... --not-applied
ugal patch list              # 只列出有记录的补丁
ugal patch list --all        # 已知游戏全部列出，无记录显示「无补丁」
ugal patch list --json       # total / applied / unpatched / no_patch
ugal patch remove 440        # 删除单条记录
ugal patch remove --all      # 清空所有记录
ugal patch hash patch.xp3    # 仅计算 SHA-256，不写记录
```

`list` 和 `fav list` 的表格中也会显示补丁状态。记录包含：

```json
{
  "appid": 440,
  "name": "HD Patch",
  "hash": "5ffe96f0...",
  "applied": true,
  "updated_at": "2026-11-17T03:19:46Z"
}
```

`--hash` 支持带 `sha256:` / `md5:` 等前缀，写入时会统一转为小写；
`--file` 会读取文件计算 SHA-256，未指定 `--name` 时用文件名作为补丁名称。
只更新 `--applied` / `--not-applied` 时，已有的名称和哈希会保留。

## 数据文件

```text
~/.ugal/
├── config.json      # SteamID、Steam 目录、抓取来源、更新设置
├── owned.json       # 已拥有游戏（appid + 名称）
├── installed.json   # 本地库目录 + appmanifest 解析结果
├── system.json      # 当前系统环境
├── favorites.json   # 本地游戏收藏夹
├── patches.json     # 补丁记录（名称、哈希、是否已打补丁）
├── update.json      # 最近一次源码同步与本地构建记录
├── covers/          # 游戏封面 <appid>.jpg / .png / .webp
└── cookie.txt       # 可选，SteamDB Cookie
```

`ugal update` 的源码默认放在当前目录的 `.ugal/src`，不一定在数据目录里；
如果两者是同一个 `.ugal`，源码也在该目录下的 `src/`。

`installed.json` 中每个库目录包含：

```json
{
  "path": "/home/user/.local/share/Steam",
  "mounted": true,
  "apps_count": 2,
  "total_size": 123456,
  "apps": [
    {
      "appid": 1985260,
      "name": "Kinkoi Golden Time",
      "installdir": "Kinkoi Golden Time",
      "type": "game",
      "download_type": 3,
      "size": 123456,
      "installed": true,
      "library": "/home/user/.local/share/Steam",
      "dir": "/home/user/.local/share/Steam/steamapps/common/Kinkoi Golden Time"
    }
  ]
}
```

说明：

- 游戏位于 `<库>/steamapps/common/<installdir>`；
- 原声带位于 `<库>/steamapps/music/<installdir>`；
- 类型优先按实际存在的目录判断，目录不存在时按 `DownloadType` 回退：
  `1 = 音乐`、`3 = 游戏`（与 readme 约定一致）；
- `StateFlags` 的第 2 位（值为 4）表示已完整安装；
- 只有 `libraryfolders.vdf` 的 `apps` 表但缺少 `appmanifest_*.acf` 的条目
  也会保留，并标记 `manifest_missing: true`。

## 自我更新

ugal 的源码在构建时通过 `go:embed` 内嵌进二进制。运行**任意** `ugal` 命令时，
如果当前目录的 `.ugal/src` 还不存在，会自动写入一份源码；已存在则跳过，不覆盖
本地修改。当前目录本身已经是 ugal 源码树时不会重复释放。

因此即使离线，也能随时查看 `./.ugal/src` 里的源码，并用 `--build-only` 构建：

```bash
ugal update                  # 同步源码 + 本地构建 + 安装
ugal update --check          # 只检查是否有新版本（不下载、不构建）
ugal update --no-build       # 只同步源码
ugal update --build-only     # 不联网，直接用已有源码重新构建
```

默认源码目录是**当前工作目录**下的 `.ugal/src`（例如在 `~/project` 运行就是
`~/project/.ugal/src`）。`ugal update` 同步远程源码时：

1. 源码目录是 git 仓库：用 `git fetch` 增量更新；
2. 源码目录不是 git 仓库（包括自动释放的内嵌源码）：下载 GitHub 源码包
   （`tar.gz`）并替换源码目录。

> 源码包方式会整体替换 `.ugal/src`，里面的本地改动不会保留；
> 需要保留改动时先备份，或直接用 `ugal update --build-only` 本地构建。

构建命令等价于：

```bash
cd <源码目录>
go build -trimpath -o <当前ugal路径> ./cmd/ugal
```

在本仓库里开发时，也可以直接用当前源码构建：

```bash
ugal update --src-dir . --build-only --install ./ugal
```

常用参数：

```text
--src-dir DIR       指定源码目录
--repo OWNER/NAME   指定仓库（默认 HanamiSakiP/u-game）
--branch NAME       指定分支（默认 main）
--method auto       同步方式：auto / git / archive
--install PATH      安装到指定路径（默认覆盖当前可执行文件）
--force             git 模式丢弃本地改动，或强制重新下载源码包
--timeout 2m        网络超时
```

也可以写进配置：

```bash
ugal config update-dir ~/ugal-src
ugal config repo HanamiSakiP/u-game
ugal config branch main
```

源码目录也可以用环境变量 `UGAL_SRC_DIR` 临时指定。

私有仓库或遇到 GitHub API 限流时，可以设置 `GITHUB_TOKEN`：

```bash
GITHUB_TOKEN=ghp_xxx ugal update
```

> `--method archive` 会整体替换源码目录，目录里的本地改动不会保留；
> 需要保留改动时请使用 git 模式，或先提交/备份。

Windows 上正在运行的 `ugal.exe` 通常无法直接覆盖。此时新版本会保存为
`ugal.exe.new`，关闭 ugal 后手动替换即可。

## 版本

默认版本为 `0.0.1`，可通过构建参数覆盖：

```bash
go build -ldflags "-X github.com/HanamiSakiP/u-game/internal/cli.version=0.0.2" ./cmd/ugal
ugal version
```

## 开发

```bash
go test ./...
go vet ./...
go build ./cmd/ugal
```
