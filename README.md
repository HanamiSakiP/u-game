# ugal

> 当前版本：`0.0.1`

`ugal` 是一个游戏库管理命令行工具：

1. 通过 `https://steamdb.info/calculator/<steamid>` 获取账号全部已拥有游戏；
2. 通过本地 Steam 的 `steamapps/libraryfolders.vdf` 和 `appmanifest_*.acf`
   获取所有库目录、已安装游戏、音乐原声带、安装目录名与占用空间；
3. 对比两者，列出「已拥有未安装」「本地已安装但未同步」的游戏；
4. 检测并记录当前系统环境（发行版、内核、桌面、会话等）；
5. 为本地已安装游戏获取封面，优先复制 Steam 本地缓存，缺失时从 Steam CDN 下载；
6. 维护本地游戏收藏夹，记录收藏状态（想玩/在玩/已通关/搁置），支持 JSON / appid 列表导入与导出，并可从 `patches.json` 一键导入；
7. 记录游戏补丁状态（补丁名称、SHA-256/MD5 哈希、是否已打补丁），同一个 appid 可以记录多个补丁文件，没有记录时显示「无补丁」；`patches.json` 会在任意命令首次运行时按内置默认记录自动创建并可直接编辑；支持从本地 `patches/` 目录校验补丁文件是否存在、哈希是否正确；列表会按 appid 自动解析游戏名称；
8. 存档同步：把 Steam 云存档（`userdata/<账号ID>/<appid>/remote`）和游戏目录中的本地存档（通常为 `savedata`、`save` 等）备份到 `~/.ugal/saves`，并支持写回原位置；
9. 补丁自动安装：把 `patches/` 中的补丁压缩包解压到对应游戏目录，同名文件自动替换，可选用 `--backup` 先备份原文件；
10. 自我更新：源码内嵌在二进制中，任意命令自动写入数据目录下的 `src/`（默认 `~/.ugal/src`）；`ugal update` 同步远程更新并在本机重新构建。
11. Cookie 管理：`ugal cookie get` 用 Firefox 获取 SteamDB Cookie，支持查看、定位和清除。
12. 命令补全：`ugal completion` 自动检测当前 shell 并写入标准补全位置，也可显式指定 bash/zsh/fish/powershell。

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
ugal update          # 同步数据目录下的 src（默认 ~/.ugal/src）并覆盖当前可执行文件
ugal update --check  # 只检查是否有新版本
```

运行任意 `ugal` 命令时，如果数据目录下的 `src/` 还不存在，会自动把二进制
内嵌的源码写入该目录。

## 使用

```bash
# 1. 从 SteamDB 抓取已拥有游戏
ugal sync 76561198000000000

# 如果 SteamDB 要求 Cloudflare 验证，可以用 Firefox 手动通过验证
ugal sync 76561198000000000 --browser

# SteamDB / 官方 API 来源会自动把 AccountID（32 位账号 ID）、SteamID2/3
# 或资料 URL 转换为 SteamID64
ugal sync 22202
ugal sync "STEAM_1:0:11101"

# 指定 Steam 社区来源时按原样使用标识，不再转换为 SteamID64
ugal sync "https://steamcommunity.com/id/自定义昵称" --source community
ugal sync 22202 --source community   # 按自定义昵称 /id/22202 访问

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
ugal fav add 440 --status 在玩   # 添加时记录收藏状态
ugal fav status 440 已通关       # 修改收藏状态
ugal fav status 440 clear       # 清除收藏状态
ugal fav list --status 想玩      # 按收藏状态筛选
ugal fav export fav.json  # 导出 JSON
ugal fav import fav.json  # 导入 JSON（合并）
ugal fav import fav.json --replace  # 用导入内容替换
ugal fav import           # 从 patches.json 一键导入（合并）

# 7. 补丁记录
ugal patch                        # 查看已有补丁记录
ugal patch list --all             # 查看已知游戏及补丁状态（默认「无补丁」）
ugal patch set 440 --name "HD Patch" --file patch.xp3 --applied
ugal patch set 440 --file patch2.xp3 --applied  # 同一 appid 记录第二个补丁文件
ugal patch set 440 --file a.xp3 --file b.xp3    # 一次记录多个补丁文件
ugal patch set 440 --hash <sha256> --not-applied
ugal patch remove 440             # 删除该 appid 的全部记录（恢复为「无补丁」）
ugal patch remove 440 --name patch.xp3  # 只删除一个补丁文件
ugal patch hash patch.xp3         # 只计算 MD5（默认）
ugal patch hash patch.xp3 --sha256 # 只计算 SHA-256
ugal patch md5 patch.xp3          # MD5 简写
ugal patch sha256 patch.xp3       # SHA-256 简写
ugal patch check                  # 校验 ~/.ugal/patches 中文件是否存在、哈希是否正确
ugal patch check --dir ~/补丁     # 校验指定目录
ugal patch install 440            # 解压该游戏记录的补丁到游戏目录，替换同名文件
ugal patch install 440 --backup   # 替换前先备份原文件
ugal patch install 440 --dry-run  # 只预览将写入的文件
ugal patch install --all          # 安装所有已记录且找到文件的补丁

# 8. 存档同步：云存档 + 游戏目录中的本地存档
ugal save                         # 查看已同步的存档
ugal save sync                    # 备份云存档和本地存档到 ~/.ugal/saves
ugal save sync --no-cloud         # 只同步本地存档（savedata 等）
ugal save sync --no-local         # 只同步 Steam 云存档
ugal save sync 440 --local-dir "D:\Game\savedata"
ugal save restore 440             # 把备份写回云存档 / 游戏目录
ugal save restore --all --force   # 还原全部并覆盖较新的目标文件
ugal save list --paths            # 查看存档来源路径

# 9. 自我更新：同步源码到本地目录并重新构建
ugal update                       # 同步 + 本地构建，覆盖当前可执行文件
ugal update --check               # 只检查是否有新版本
ugal update --no-build            # 只同步源码
ugal update --build-only          # 不联网，用已有源码重新构建

# 10. SteamDB Cookie
ugal cookie                       # 查看 Cookie 状态和文件位置
ugal cookie get                   # 用 Firefox 完成验证并保存 Cookie
ugal cookie get --print           # 获取后输出 Cookie 内容
ugal cookie show                  # 显示已保存的 Cookie
ugal cookie clear                 # 删除已保存的 Cookie

# 11. 命令补全
ugal completion                   # 自动检测当前 shell 并写入补全脚本
ugal completion bash              # 显式指定 bash（zsh/fish/powershell 同理）
ugal completion --print           # 只输出补全脚本，不写入文件
```

常用参数：

```text
list --json                 输出 JSON
list --type game|music      按类型过滤
list --paths                显示完整安装路径
list --limit 50             限制显示行数
scan --include-partial      保留未完整安装的条目
sync --source steamdb       强制来源（auto/steamdb/community/api）
sync --source community     使用 Steam 社区，标识按原样使用（不转换 SteamID64）
sync --cookie-file FILE     使用浏览器导出的 SteamDB Cookie
sync --browser              打开 Firefox 完成 Cloudflare 验证后同步
sync --browser-timeout 3m   浏览器验证最长等待时间
sync --timeout 60s          网络超时
cookie get                  用 Firefox 获取并保存 Cookie
cookie show                 显示已保存的 Cookie
completion                  自动检测 shell 并写入补全脚本
completion bash             显式写入 bash 补全脚本
completion --print          只输出补全脚本

cover --style auto          封面样式（auto/portrait/header/logo）
cover --source auto         封面来源（auto/local/cdn）
cover --type game|music     默认只处理游戏
cover --concurrency 4       并发下载数
cover --timeout 30s         单次下载超时
cover --retry 2             失败后的重试次数（0 表示不重试）

fav list --installed        只看已安装的收藏
fav list --missing          只看未安装的收藏
fav list --status 在玩      只看指定收藏状态（逗号分隔，none 为未标记）
fav list --offline          不联网获取发行商
fav list --json             机器可读输出
fav add 440 --status 想玩   添加时设置收藏状态
fav status 440 已通关       设置收藏状态
fav status 440 clear        清除收藏状态
fav remove 440              删除收藏
fav remove --all            清空收藏夹

patch list --all            包含没有补丁记录的已知游戏
patch list --json           机器可读输出
patch list --offline        不联网获取缺失的游戏名称
patch set --file FILE       计算并记录文件 MD5（默认），可重复指定
patch set --file FILE --sha256 计算并记录文件 SHA-256
patch set --name NAME       指定补丁名称（默认使用文件名）
patch hash FILE             只计算文件 MD5
patch hash FILE --sha256    只计算文件 SHA-256
patch check                 检查 <数据目录>/patches 中补丁文件是否存在、哈希是否正确
patch check --dir DIR       检查指定目录
patch check --json          机器可读输出
patch install 440           自动解压该游戏记录的补丁到游戏目录
patch install 440 --file FILE  直接安装指定压缩包并记录为补丁
patch install 440 --name NAME  只安装指定名称的补丁
patch install 440 --backup  替换同名文件前备份到 <数据目录>/patch-backups
patch install 440 --strip 1 去掉压缩包顶层目录后再解压
patch install 440 --dry-run 只显示将写入的文件
patch install --all         安装所有已记录且找到文件的补丁
patch set --applied         标记该 appid 的全部补丁为已打补丁
patch set --not-applied     标记该 appid 的全部补丁为未打补丁
patch remove --name NAME    只删除指定名称的补丁记录

save sync                   备份云存档和本地存档到 <数据目录>/saves
save sync --no-cloud        只同步本地存档
save sync --no-local        只同步云存档
save sync --local-dir DIR   显式指定本地存档目录（需要 appid）
save sync --mirror          删除备份中源目录已不存在的文件
save sync --dry-run         只统计不写文件
save restore 440            把备份写回云存档 / 游戏目录
save restore --all          还原所有记录
save restore --force        目标文件更新时也覆盖
save remove 440             删除记录和备份
save list --paths           显示完整存档来源路径

update --check              只检查是否有更新
update --no-build           只同步源码，不构建
update --build-only         用已有源码重新构建（不联网）
update --method git|archive 指定同步方式
update --src-dir DIR        源码目录（默认 <数据目录>/src）
update --repo OWNER/NAME    源码仓库
update --branch NAME        同步分支
update --install PATH       安装到指定路径
update --force              git 丢弃本地改动 / 强制重新下载
```

## SteamDB 与 Cloudflare

SteamDB 使用 Cloudflare 校验，服务器 / 数据中心网络可能直接收到验证页。
此时可以先让 ugal 打开 Firefox 完成验证：

```bash
ugal sync 76561198000000000 --browser
```

ugal 会使用一个独立的临时 Firefox 配置打开计算器页面。你在窗口中完成
Cloudflare 验证后，它会自动读取验证后的页面和 Cookie 继续同步，并把 Cookie
保存到 `~/.ugal/cookie.txt`，之后普通的 `ugal sync` 也会优先复用。默认最长
等待 3 分钟，可以用 `--browser-timeout` 调整。需要本机安装 Firefox；也可以用
环境变量 `UGAL_FIREFOX` 指定可执行文件。临时配置会注入一个只用于隐藏
`navigator.webdriver` 的辅助脚本，避免 Cloudflare 验证反复提示「重试」；
如果仍然循环，可以改用下面的 Cookie 方式。

只想单独准备 Cookie 时，可以运行：

```bash
ugal cookie get          # 用 Firefox 验证并保存到 ~/.ugal/cookie.txt
ugal cookie get --print  # 同时输出 Cookie 内容
ugal cookie              # 查看状态和文件位置
ugal cookie clear        # 删除已保存的 Cookie
```

如果不使用 `--browser`，`ugal sync` 在 SteamDB 失败后会自动回退到：

1. Steam 社区公开游戏列表（`steamcommunity.com/.../games?tab=all&xml=1`）；
2. Steam 官方 Web API（需要环境变量 `STEAM_API_KEY`）。

如果希望坚持使用 SteamDB，也可以在浏览器通过验证后导出 Cookie，放到：

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
ugal config steamid 76561198000000000        # 记住 SteamID（原样保存，抓取时按来源转换）
ugal config steam-user 12345678              # 记住 Steam 账号 ID（userdata 下的数字目录）
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

收藏夹保存在 `favorites.json`，可按 appid 添加、删除、筛选，并支持导入导出。
`fav list` 优先按备注分组排序；没有备注的收藏会联网获取发行商并按发行商分组，
两者都没有时归入「无分组」。表格中显示备注/发行商而不是添加时间。有备注的收藏
仍然按备注分组，备注不会被发行商覆盖。多发行商组合按以下规则归并：

| 原始组合 | 分组 |
| --- | --- |
| `HIKARI FIELD / NekoNyan Ltd.` | `HIKARI FIELD` |
| `NekoNyan Ltd. / HIKARI FIELD` | `HIKARI FIELD` |
| `TamaMako gaming / Sekai Project` | `Sekai Project` |
| `Sekai Project / TamaMako gaming` | `Sekai Project` |
| `Kagura Games / NekoNyan Ltd.` | `NekoNyan Ltd.` |
| `NekoNyan Ltd. / Kagura Games` | `NekoNyan Ltd.` |

未列出的组合保持原样。每组前会用红色显示组名和该组数量（例如
`Valve（2 个）`）。

### 收藏状态

每条收藏可以记录一个状态：`想玩`、`在玩`、`已通关`、`搁置`，也可以直接写自定义状态。
`fav list` 的表格包含「安装」和「收藏状态」两列，底部会汇总各状态数量；
`fav list --status` 可按状态筛选，`none` / `未标记` 表示还没有设置状态。

```bash
ugal fav add 440 --status 在玩     # 添加时设置
ugal fav status 440 已通关         # 修改
ugal fav status 440 220 搁置       # 一次修改多个
ugal fav status                    # 查看全部收藏状态
ugal fav status 440                # 查看单个收藏
ugal fav status 440 clear          # 清除（clear / 未标记 / --clear）
ugal fav list --status 想玩        # 只看想玩
ugal fav list --status 想玩,在玩   # 多个状态
ugal fav list --status none        # 只看未标记
```

状态保存在 `favorites.json` 的 `status` 字段，导入导出时随收藏一起保留。常见英文
写法会自动归一，例如 `playing` → `在玩`、`finished` / `completed` → `已通关`、
`on-hold` → `搁置`。

```bash
ugal fav add 440 220
ugal fav list --json
ugal fav export favorites.json
ugal fav import favorites.json          # 与现有收藏合并
ugal fav import favorites.json --replace # 替换现有收藏
ugal fav import                         # 从 patches.json 一键导入（合并）
ugal fav import --replace               # 用 patches.json 内容替换收藏
ugal fav import-patches                 # 等同 fav import --patches
ugal fav remove 220
ugal fav clear
```

导入时支持：

- `ugal fav export` 生成的 JSON；
- `{"games":[{"appid":440,"name":"Team Fortress 2"}]}`；
- `patches.json`（`{"patches":[{"appid":440,...}]}`）：按 appid 导入，
  补丁名称不会被当成游戏名称，游戏名称会按已有数据自动解析；
- `[440, 220]` 或 `["440", "220"]`；
- `{"440":"Team Fortress 2","220":"Half-Life 2"}`；
- 每行一个 appid，或 `appid,名称` / `appid<TAB>名称` 的文本。

`patches.json` 会在任意命令首次运行时按内置默认内容自动创建。你可以手工往里
补充 appid 和补丁记录，然后运行 `ugal fav import`，把其中出现过的游戏一次性
加入收藏夹。

导出内容包含 `version`、`updated_at`、`count` 和 `games`，可以直接备份或迁移到
另一台机器。

## 补丁记录

补丁状态保存在 `patches.json`。同一个 appid 可以对应多条补丁记录，
用补丁名称（`name`）区分；名称相同则视为同一个补丁文件的更新。
没有记录时显示「无补丁」。

`patches.json` 是默认的补丁输入文件：任意命令首次运行时，如果它不存在会
按内置默认模板自动创建（默认模板为 `internal/store/default_patches.json`），
可以直接手工编辑；`patches.json` 已存在时不会被覆盖。编辑后运行
`ugal fav import`，即可把其中出现过的游戏一键加入收藏夹。

| 情况 | 状态 |
| --- | --- |
| 没有记录 | `无补丁`（默认） |
| 全部记录 `applied = false` | `未打补丁` |
| 全部记录 `applied = true` | `已打补丁` |
| 部分记录已打补丁 | `部分已打` |

```bash
ugal patch set 440 --name "HD Patch" --file patch.xp3 --applied
ugal patch set 440 --file patch2.xp3 --applied   # 再记录一个补丁文件
ugal patch set 440 --file a.xp3 --file b.xp3     # 一次记录多个补丁文件
ugal patch set 440 --hash sha256:abcd... --not-applied
ugal patch list              # 只列出有记录的补丁，同一 appid 可出现多次（补丁多的排前面）
ugal patch list --all        # 已知游戏全部列出，无记录显示「无补丁」
ugal patch list --json       # total / applied / unpatched / no_patch
ugal patch list --offline    # 不联网，只用本地名称和 appnames.json 缓存
ugal patch remove 440        # 删除该 appid 的全部补丁记录
ugal patch remove 440 --name patch.xp3  # 只删除一个补丁文件
ugal patch remove --all      # 清空所有记录
ugal patch hash patch.xp3    # 仅计算 MD5（默认），不写记录
ugal patch hash patch.xp3 --sha256  # 仅计算 SHA-256，不写记录
ugal patch md5 patch.xp3     # MD5 简写
ugal patch sha256 patch.xp3  # SHA-256 简写
ugal patch set 440 --file patch.xp3 --sha256  # 用 SHA-256 记录补丁
```

### 校验本地补丁文件

把补丁文件放进 `~/.ugal/patches/`（也可以按 appid 或任意子目录分类），
`patch check` 会递归查找并校验。目录不存在时会自动创建：

```bash
ugal patch check              # 校验默认目录 <数据目录>/patches
ugal patch check --dir ~/补丁 # 校验指定目录
ugal patch check 440          # 只校验指定 appid
ugal patch check --all        # 同时显示未找到的补丁明细
ugal patch check --json       # 机器可读输出
ugal patch check --offline    # 输出游戏名时不联网
```

默认文本输出只展开「已找到」的补丁，表格列为 `APPID`、`状态`、`游戏`、
`补丁名称`、`哈希`；哈希列只显示「符合」或「不符合」，未找到的补丁汇总成
一行。校验期间会在终端显示进度。需要查看未找到的明细时加 `--all`。
`--json` 始终包含全部记录。

匹配和校验规则：

- 按补丁记录中的 `name` 与文件名匹配，不区分大小写，支持子目录；
- 同一文件名存在多个副本时，只要任意一个哈希与记录一致就算通过；
- 哈希长度 32 位按 MD5 校验，64 位按 SHA-256 校验；
- 记录没有名称、哈希长度不支持或文件无法读取时显示「无法校验」。

| 状态 | 含义 |
| --- | --- |
| `已找到`（`ok`） | 文件存在且哈希正确 |
| `未找到`（`missing`） | 目录中找不到对应文件 |
| `哈希错误`（`mismatch`） | 文件存在，但哈希与记录不一致 |
| `无法校验`（`unverified`） | 缺少名称/哈希，或文件无法读取 |

`--json` 输出包含 `directory`、`total`、`ok`、`missing`、`mismatch`、
`unverified` 和逐条 `patches`（含 `path`、`expected_hash`、`actual_hash`）。

`list` 和 `fav list` 的表格中也会显示补丁状态；`fav list` 还会在「哈希」列
显示每个补丁哈希的前 8 位（多个补丁用逗号分隔）。这两个命令的 JSON 输出中，
每款游戏同时包含 `patch`（第一条记录，兼容旧格式）和 `patches`（全部记录）。
`patch list --json` 则按补丁记录逐条输出，同一 appid 可能出现多次。
表格按 appid 下的补丁数量从多到少排序，包含多个补丁文件的游戏优先显示，
数量相同时保持 appid 升序；`--all` 模式下「无补丁」的游戏排在最后。

`ugal patch list` 会在表格末尾显示补丁总数和状态统计。在终端中，「未打补丁」
以及未找到名称的游戏（显示为 `App <appid>`）会以红色显示；输出被重定向、
设置 `NO_COLOR` 或 `TERM=dumb` 时自动使用纯文本。

补丁列表的「游戏」列会按以下优先级解析 appid 对应的名称：

1. `installed.json` / `owned.json` / `favorites.json` 中已有的名称；
2. Steam 本地 `appcache/appinfo.vdf`（离线读取，通常已包含账号接触过的游戏）；
3. `appnames.json` 缓存；
4. 仍然缺失时，按需请求 Steam 官方 `ISteamApps/GetAppList`，成功后写入
   `appnames.json`，后续离线也能显示。

不想联网时加 `--offline`（或设置环境变量 `UGAL_OFFLINE=1`）。联网失败只会
保留 `App <appid>` 占位名，不会影响命令本身；也可以直接编辑
`appnames.json` 手动补充名称，例如：

```json
{
  "version": 1,
  "count": 1,
  "names": { "575480": "ChronoClock" }
}
```

`patches.json` 中的 `patches` 是记录数组，同一 appid 可以有多项：

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
`--file` 会读取文件计算 MD5（默认），加上 `--sha256` 则改为计算 SHA-256，
未指定 `--name` 时用文件名作为补丁名称。重复记录同名文件会更新原记录，
不同名称会新增记录；`--file` 可以重复指定多个文件。
当同一 appid 已有多条记录时，只给 `--hash` 无法确定更新哪一条，需要用
`--name` 指定补丁名称。只更新 `--applied` / `--not-applied` 且未指定
`--name` 时，会作用于该 appid 的全部补丁记录。

### 自动安装补丁

`patch install` 会从 `<数据目录>/patches` 中递归查找补丁记录对应的压缩包，
然后解压到 `installed.json` 记录的该 appid 游戏目录。压缩包中与游戏目录
同名的文件会被直接替换：

```bash
ugal patch install 440                  # 安装该 appid 所有已找到的补丁记录
ugal patch install 440 --name patch.7z  # 只安装一条记录
ugal patch install 440 --file ~/patch.zip
ugal patch install --all                # 所有已安装且找到文件的游戏
ugal patch install 440 --backup         # 替换前备份原文件
ugal patch install 440 --strip 1        # 补丁内有一层包装目录时去掉
ugal patch install 440 --dry-run        # 只预览，不写入
ugal patch install 440 --game-dir ~/Game  # 手动指定游戏目录
```

- 内置支持 `zip`、`tar`、`tar.gz`/`tgz`、`gz`；
- `7z`、`rar`、`exe` 等格式需要系统中有 `7z`、`unar`、`unrar` 或 `bsdtar`，
  ugal 会依次尝试；`exe` 只会被当作压缩包解压，不会执行安装程序；
- 默认保留压缩包内的完整路径。如果补丁把所有文件放在一个顶层目录中，用
  `--strip 1` 去掉这一层；
- `--backup` 会把被替换的原文件保存到
  `<数据目录>/patch-backups/<appid>/<时间>/`；
- 安装成功后会把这些补丁记录标记为「已打补丁」；`--file` 还会按文件名
  新建记录并计算 MD5；
- 解压时会校验路径，拒绝 `../`、绝对路径等跳出游戏目录的条目。

## 存档同步

`ugal save sync` 会把存档备份到数据目录，`ugal save restore` 再写回原位置。

云存档读取 Steam 的：

```text
<Steam根目录>/userdata/<账号ID>/<appid>/remote/
```

本地存档会扫描游戏安装目录，自动识别名为 `savedata`、`save`、`saves`、
`savegame`、`savegames` 等目录（最多向下 3 层），找到多个时分别备份。
标准结构为：

```text
~/.ugal/
├── saves.json                  # 每个存档的来源路径、文件数、大小、同步时间
└── saves/
    └── <appid>/
        ├── cloud/              # Steam 云存档
        └── local/
            ├── savedata/       # 游戏目录中的本地存档
            └── save/           # 其他被识别到的存档目录
```

```bash
ugal save                       # 查看已同步的存档
ugal save sync                  # 同步所有已安装游戏的云存档 + 本地存档
ugal save sync 440 220          # 只同步指定游戏
ugal save sync --no-cloud       # 只同步本地存档
ugal save sync --no-local       # 只同步云存档
ugal save sync 440 --local-dir "D:\Game\savedata"  # 手动指定存档目录
ugal save sync --steam-user 12345678                 # 指定 Steam 账号
ugal save sync --dry-run        # 只统计将同步的文件
ugal save sync --force          # 备份较新时也覆盖
ugal save sync --mirror         # 删除备份中源目录已不存在的文件
ugal save restore 440           # 把备份写回云存档 / 游戏目录
ugal save restore --all         # 还原所有记录
ugal save restore --force       # 覆盖较新的目标文件
ugal save restore --dry-run     # 只统计将写入的文件
ugal save remove 440            # 删除记录和备份
ugal save remove 440 --keep     # 只删除记录，保留备份
ugal save clear                 # 清空所有存档记录和备份
```

说明：

- 默认只复制比备份更新（或备份中不存在）的文件；删除源文件不会自动删除
  备份，需要完全镜像时使用 `--mirror`；
- 有多个 Steam 账号时，默认使用最近修改的 `userdata` 目录；建议用
  `ugal config steam-user <账号ID>` 固定账号；
- `usernames`/自定义 URL 不是账号 ID，云存档需要使用数字账号 ID；
- `--local-dir` 会应用于指定的 appid，因此需要同时给出 appid；
- 存档同步只读写本地文件和 Steam 的 userdata 目录，不会调用 Steam Cloud
  Web API。

## 数据文件

```text
~/.ugal/
├── config.json      # SteamID、Steam 目录、抓取来源、更新设置
├── owned.json       # 已拥有游戏（appid + 名称）
├── installed.json   # 本地库目录 + appmanifest 解析结果
├── system.json      # 当前系统环境
├── favorites.json   # 本地游戏收藏夹
├── patches.json     # 补丁记录（默认输入文件，任意命令首次运行时自动创建）
├── patches/         # 本地补丁文件（patch check 校验、patch install 解压）
├── saves.json       # 云存档 / 本地存档同步记录
├── saves/           # 存档备份 <appid>/cloud、<appid>/local/<目录名>
├── patch-backups/   # patch install --backup 保存的被替换文件
├── appnames.json    # appid → 游戏名称缓存
├── publishers.json  # appid → 发行商缓存
├── update.json      # 最近一次源码同步与本地构建记录
├── src/             # 自我更新源码（默认源码目录）
├── covers/          # 游戏封面 <appid>.jpg / .png / .webp
└── cookie.txt       # 可选，SteamDB Cookie（--browser 验证后会写入）
```

`ugal update` 的源码默认放在数据目录的 `src/`（默认 `~/.ugal/src`）。

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
如果数据目录下的 `src/` 还不存在，会自动写入一份源码；已存在则跳过，不覆盖
本地修改。当前目录本身已经是 ugal 源码树时不会重复释放。

因此即使离线，也能随时查看 `~/.ugal/src` 里的源码，并用 `--build-only` 构建：

```bash
ugal update                  # 同步源码 + 本地构建 + 安装
ugal update --check          # 只检查是否有新版本（不下载、不构建）
ugal update --no-build       # 只同步源码
ugal update --build-only     # 不联网，直接用已有源码重新构建
```

默认源码目录是**数据目录**下的 `src/`（默认 `~/.ugal/src`；使用 `--data-dir`
时就是该数据目录下的 `src/`）。`ugal update` 同步远程源码时：

1. 源码目录是 git 仓库：用 `git fetch` 增量更新；
2. 源码目录不是 git 仓库（包括自动释放的内嵌源码）：下载 GitHub 源码包
   （`tar.gz`）并替换源码目录。

> 源码包方式会整体替换数据目录下的 `src/`，里面的本地改动不会保留；
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

## 命令补全

`ugal completion [shell]` 会自动检测当前 shell（依次参考 `UGAL_SHELL`、父进程名、
`$SHELL`、`PSModulePath`），并把补全脚本写入该 shell 的标准位置；支持 `bash`、
`zsh`、`fish` 和 `powershell`。检测不到时会提示显式指定。

```bash
ugal completion                  # 自动检测并写入
ugal completion zsh              # 显式指定并写入
UGAL_SHELL=fish ugal completion  # 临时覆盖检测结果
ugal completion --print          # 只输出到终端，不写文件
ugal completion --dir DIR        # 写入指定目录（便于测试或自定义布局）
```

默认写入位置：

| Shell | 路径 |
| --- | --- |
| Bash | `$XDG_DATA_HOME/bash-completion/completions/ugal`（默认 `~/.local/share/...`） |
| Zsh | `${ZDOTDIR:-~}/.zsh/completions/_ugal` |
| Fish | `$XDG_CONFIG_HOME/fish/completions/ugal.fish`（默认 `~/.config/...`） |
| PowerShell | `$PROFILE`（用 `# >>> ugal completion >>>` 标记块追加，重复运行不会叠加） |

Bash / Fish 写入后新开终端即可生效；当前终端也可以立即启用：

```bash
source ~/.local/share/bash-completion/completions/ugal
source ~/.config/fish/completions/ugal.fish
```

Zsh 还需要让 fpath 包含补全目录，在 `~/.zshrc` 中加入：

```zsh
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

PowerShell 重新打开窗口，或运行 `. $PROFILE` 生效。也可以随时用
`ugal completion --print` 把脚本输出到终端自行处理。

补全会提示子命令、常用参数，以及 `--source`、`--type`、`--method` 等选项的取值。

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
