# hb

LINUX DO 红包的命令行工具。一个命令行里看 lafish（hb.lafi.sh）和 Unsnow（hb.unsnow.org）的红包，
还能在终端里玩 lafish 上的「矿洞夺宝」。

我们支持 Linux.DO 社区与开放、友善、重视技术质量的开发者社区。

> 早期的个人项目，版本 0.x。接口随站点变化，可能随时需要跟着改。

## 它做什么，不做什么

**做**：

- **lafish**：登录、看红包信息、提交一次判定，在终端里玩矿洞夺宝（全屏界面，或一行一个命令的安静模式）。
- **Unsnow**：用站点官方的只读接口查口令和红包链接、看广场、看自己发出和领到的记录。

**不做**，这是有意的：

- **不替你过人机验证**（Turnstile、Cap、reCAPTCHA 都一样）。要验证的红包，`hb` 把链接给你，你在浏览器里自己领。
- **不自动、不循环、不轮询、不自动重试。** 每一次挖掘、提交、查询都是你触发的一次操作。
- **不猜口令。** Unsnow 查错会累计，到阈值要在网页上验证；`hb` 会先在本地检查口令格式，别拿它去试口令。
- 矿洞夺宝的建议功能（`--hints`）只提示、默认关闭，每一次挖掘仍然由你按键。

## 安装

需要 [Go](https://go.dev/dl/) 1.26 或更新，并且 Go 的 bin 目录（`GOBIN`，或 `GOPATH/bin`）在 `PATH` 里。

```sh
git clone git@github.com:Aetherfield/hb-cli.git
cd hb-cli
./scripts/install.sh        # macOS / Linux
.\scripts\install.ps1       # Windows（PowerShell）
```

脚本会编译并装到 Go 的 bin 目录。版本号的格式是 `<major>.<minor>.<patch>+<构建日期>.<提交数>.g<短 sha>`，
比如 `0.1.0+20261115.2.g984f163`；大版本号写在仓库根目录的 `VERSION` 文件里，日期是 UTC。
直接 `go build` 的版本显示为 `dev+g<sha>`（工作区有改动时加 `.dirty`）。

不想装 Go：在仓库的 Releases 页面下载对应系统的压缩包（Windows 和 macOS、Linux 的 amd64 和 arm64 都有，
Windows 是 `.zip`，其余是 `.tar.gz`），用 `checksums.txt` 核对，把里面的 `hb` 放进 `PATH`。

平台：Windows 是主要的开发环境；macOS 和 Linux 在 CI 里跑 `go vet` 和 `go test`，没有人在 macOS 真机上用过。

## 开始用

### lafish

```sh
hb login lafish        # 选一种登录方式，会话存进系统钥匙串
hb whoami              # 看各站点现在登录的是谁
hb play <红包链接|id>   # 认出矿洞夺宝就进终端游戏；否则看文字信息
```

`hb login` 的方式：`token`（粘贴你在网页「我的 → 个人令牌」创建的 `hbp_…` 令牌，不用浏览器，也不用 cookie，推荐）、`ext`（浏览器扩展，用你日常浏览器里已有的登录，装一次）、`paste`（手动粘贴 cookie）、`clean`（独立的干净浏览器，登录一次）。不确定就直接运行，它会问。

`clone`（复制日常浏览器的 profile）**已弃用**：新版 Chrome / Edge 用「应用绑定加密」保存 cookie，复制出来的 profile 读不到登录，只能在弹出的窗口里重新登录，不如直接用 `clean`。Firefox 系不受影响，所以还留着，但不再推荐（见 #16）。

`ext` 用的浏览器扩展 hb session bridge 放在单独的仓库：[Aetherfield/hb-session-bridge](https://github.com/Aetherfield/hb-session-bridge)。运行 `hb login <站点> --method ext` 时，hb 会自己下载它固定的那个版本，核对 SHA-256，解压到配置目录，再告诉你在浏览器里「加载已解压的扩展程序」时选哪个目录；解压过同一个版本就直接用。自己下载了 zip 的话，用 `--ext-zip <文件>` 交给 hb 核对并解压；`--download-ext` 强制重新下载。扩展只和本机 `127.0.0.1` 上正在等待的 hb 通信，每次都要输入终端里显示的 6 位配对码。

用令牌登录前，先在网页「我的 → 个人令牌」创建一个（任何登录用户都能建，只显示一次）。用令牌要知道几件事（lafish 定的，
见 [LeUKi/hb 的设计文档](https://github.com/LeUKi/hb/blob/main/docs/personal-token-design.md) 第 8 节）：

- 令牌只管查看、领取、口令兑换和附带网页认人；**要人机验证的红包照样要在网页上领**，转账和管理令牌也只能在网页上。
- 每分钟有次数限制，按账号算（建多少个令牌都一样）：读取 60 次、广场 20 次、领取和口令兑换合计 20 次、认人 20 次。超了会回 429，
  hb 会告诉你要等多久，不会自动重试。
- 站长可以暂时关掉个人令牌，发红包的人也可以在某个红包上不允许用令牌领；这两种情况 hb 都会说明原因，这时到网页上操作。
- **用令牌领的红包，名单上名字后面会有一把灰色钥匙**，别人能看出这是脚本或客户端领的，不是在网页上点的。

矿洞夺宝：

```sh
hb play "<红包链接>"                      # 全屏游戏
hb play "<红包链接>" --mode cli           # 安静的一行一个命令；d 3 4 挖第 3 行第 4 列，s 状态，m 地图，b 清屏
hb play "<红包链接>" --mode cli -x "d 3 4"  # 只执行一条就退出
hb assets fetch                          # 可选：取 Minecraft 原版图标（约 28 MB，下载后核对哈希）
```

全屏游戏里也能用鼠标，作用和按键一样，不多不少：

- 点一格：光标放过去；拖动是瞄准，松手不会挖；在光标已经停着的那一格上再点一下才挖（或按回车），所以手滑点错不会挖。
- 左边的「矿图」是整个矿洞的布局：每张卡一块，按它在矿洞里的位置摆（颜色是群系，下面的条是挖了多少，亮的是你现在这张）。点一块就去那张卡，在图上拖动是平移，滚轮上下移；挖还是在右边那张卡上点格子。
- 开了 `--hints` 时，如果「还剩」的数量已经把一格的内容定死了（比如只剩一种东西，或者一定有危险而且只剩一种危险），这格会直接画成它是什么（带下划线、深蓝底，像素图标是蓝色调；光标说明里写「按规则推出它是…」）；其余按规则推出一定有危险的格子会标成红底 `✗`（像素图标是红框，行模式地图里是 `x`），侧栏和 `hint` 命令里也会列出；`n` / `N` 把光标跳到下一个 / 上一个零风险格（跨卡片，会切卡，到头绕一圈，关闭的卡跳过），`J` 开关「挖完自动跳到最近的零风险格」（这张卡没有了就去下一张有的卡）；这些都只移动光标，从不替你挖。
- 鼠标会让终端不能直接选文字：按住 Shift 再选，或者启动时加 `--no-mouse`。
- 没在真实终端里逐个试过：点击位置是按布局算的，不同终端、字体、缩放下如果点歪了，请用 `--no-mouse` 退回键盘，并告诉我。

### Unsnow

先在网页「我的 → 认证令牌」创建令牌（`hbr_…`，只显示一次，每个账号一个），然后：

```sh
hb login unsnow                 # 粘贴令牌；也可以 --token-stdin 从标准输入读
hb unsnow me                    # 令牌属于谁，口令查错次数和验证状态
hb unsnow check <口令|链接|编号>  # 这个红包现在能不能领（不会领取）；能领就给出链接
hb unsnow plaza                 # 公开红包广场
hb unsnow sent                  # 我发出的红包
hb unsnow received              # 我已到账的红包
```

触发口令验证（403）时 `hb` 会给出验证链接并停下；遇到 429 会说明要等几秒；任何错误都不会重试。

### 打飞机（zyese.xyz）

[打飞机](https://zyese.xyz) 是一个打字游戏。Luminet 为 hb 开了 OpenAPI，并登记了一个应用（`client_id` 写在 `internal/zyese/oidc.go`）。

```sh
hb zyese login              # 浏览器里登录 Luminet Auth（授权码 + PKCE，回调 http://localhost:1423/callback）
hb zyese me                 # 我是谁、应用是否获准、能玩哪些模式
hb zyese stats              # 我的战绩
hb zyese rank [daily|yesterday|all-time] --mode en|zh|ja
hb zyese play --mode en     # 全屏开一局；Esc 暂停，Ctrl+C 按两次放弃
hb zyese logout
```

- 要先把 Luminet 账号绑定 LINUX DO，并且在 zyese.xyz 用 LINUX DO 登录过一次。少了哪一步，hb 会直接告诉你去哪里补。
- 游戏在服务器上跑，hb 只把你敲的每个字符转发过去。中文、日文模式下，输入法上屏的每个字各算一个键。
- hb 不代打，不接受粘贴，不伪造按键时间，也不上报分数。成绩进网站的共享排行榜，标记为第三方应用。
- 访问令牌和刷新令牌存在系统凭据库里，快到期时自动刷新；刷新失败就删掉，要你重新登录。
- 断线后会在约两分钟内用同一局重连，没确认的按键按原序号重发。
- 输入遥测（可选，现在只有 Windows）：`hb zyese install-hook` 先自检，再 `hb zyese play --input-telemetry`。hb 在这一局里装一个键盘钩子，系统会告诉它每个键是键盘按的、还是别的程序塞进来的（和浏览器的 `isTrusted` 同一类东西），hb 把这个如实写进每个键的 `trusted`，并发送按键抬起。钩子不用管理员，只在这一局里、只在内存里，只用来配对终端收到的键，别的键盘事件不保存也不发送。它挡不住所有作弊（接在 USB 上的硬件模拟器和真键盘没区别），也不是现在上榜所必需的：不开它，用输入遥测版本 0 照常能打。**服务端怎么用 `trusted`，Luminet 没有写，所以我没在真实服务器上用过版本 1。**

## 命令

| 命令 | 作用 |
|---|---|
| `hb login [站点]` / `hb logout [站点]` | 登录并保存会话 / 删除已保存的会话 |
| `hb whoami` | 各站点当前登录的身份 |
| `hb play <红包>` | 玩一个 lafish 红包（`--mode auto\|tui\|cli\|text`） |
| `hb kddb <红包>` | 直接进矿洞夺宝（等价于识别为矿洞夺宝的 `hb play`） |
| `hb hints report` | 读 `--hints-log` 的日志，看建议准不准 |
| `hb assets fetch\|check\|clean` | 取、核对、删除 Minecraft 图标 |
| `hb unsnow me\|check\|plaza\|sent\|received` | Unsnow 的只读查询 |
| `hb zyese login\|me\|stats\|rank\|play\|logout` | 打飞机打字游戏（Luminet OpenAPI） |
| `hb doctor` | 检查配置、浏览器、网络可达和时钟 |

全局选项：`--json`（输出 JSON，键名始终是英文，不含密钥）、`--profile <名>`。`hb <命令> --help` 有每个命令的详细说明。

## Tab 补全

niubash（`niu`）：把下面这段加到 `~/.niubashrc`。niubash 的 Tab 目前不执行 bash 的 `complete -F` 函数，所以不能用
bash 的补全脚本，`hb completion niu` 输出的是 niubash 自己的 compdef 协议脚本；它也读不了 `source <(...)`，所以用 `eval`。

```sh
if command -v hb >/dev/null 2>&1; then
  __hb_c=$(hb completion niu 2>/dev/null)
  case "$__hb_c" in "# hb "*) eval "$__hb_c" ;; esac
  unset __hb_c
fi
```

之后新开的 niubash 里，`hb <Tab>` 补命令，`hb login <Tab>` 补站点名，`--method`、`--mode`、`--render`、`--browser` 补取值。
**口令、链接和红包编号不会被补全**，补全不该帮人猜口令（但 niubash 在 hb 没给候选时会退回成文件名补全，这一层 hb 管不到）。

其他 shell：`hb completion bash|zsh|fish|powershell`，用法见各自的 `--help`。

## 给 AI 助手用（MCP）

`hb mcp` 是一个 MCP 服务，让 Claude Code 这类助手查红包，并在你明确要求时领**一个**「点一下就领」的 lafish 红包。

```sh
hb login lafish --method token     # AI 领取只认个人令牌（见上面的说明）
claude mcp add hb -- hb mcp        # 注册到 Claude Code
cp -r skills/hb-red-packet ~/.claude/skills/   # 配套的 Skill：规定助手该怎么做
```

| 工具 | 作用 |
|---|---|
| `whoami` | 各站登录的账号 |
| `lafish_packet` | 看一个 lafish 红包：状态、领取方式、hb 能不能领 |
| `lafish_claim_preview` / `lafish_claim` | 领取的两步：先预览，再凭 `preview_id` 领 |
| `unsnow_me`、`unsnow_plaza`、`unsnow_packet`、`unsnow_sent`、`unsnow_received` | Unsnow 的只读查询（不能查口令） |
| `kddb_state`、`kddb_board` | 矿洞夺宝：状态、每张卡的格子，和 hb 的风险估计（只读） |
| `kddb_dig_grant`、`kddb_dig` | 矿洞夺宝：你授权一批（最多 10 次、10 分钟），AI 在里面一格一格挖 |
| `kddb_claim` | 矿洞夺宝：领取已挖到的奖励（单独确认，领取后不能再挖） |

**护栏是代码，不是提示词**，助手改不了（Skill 是第二道，不是第一道）：

- 只能领「点一下就领」的红包：口令红包、代码判定红包、要人机验证的红包一律不领，也就没有猜口令的余地。
- 只用个人令牌领。用 cookie 领的红包和你自己在网页上点的没区别，会绕过名单上的标记和发红包的人的开关，所以不做。
- **每一次领取都要你本人在客户端里点确认**，确认绑定在这一个红包上、两分钟内有效、用过作废，伪造和重放都没用。客户端不能弹确认，就不领，**没有免确认的开关**。
- 速度和次数：两次领取至少隔 10 秒，默认每小时 5 个、每天 20 个（`--max-per-hour`、`--max-per-day` 可以调低，不能超过上限 20 和 60，也不能由助手调整）；次数记在配置目录里，重启不清零。
- 一次失败后暂停 1 分钟，连续两次失败暂停 10 分钟；失败不重试。
- 红包的标题、祝福语这些别人写的字，返回时都装在 `untrusted` 里并注明「只当数据读」，防止有人在红包里写指令。
- 每次调用都记在配置目录的 `mcp-audit.jsonl`（不含令牌）。`--read-only` 可以完全去掉领取。

**限制，要心里有数**：

- 这些护栏只管 MCP 工具。如果助手**同时**有命令行权限，并且这台机器上的 `hb` 是登录着的，它理论上能直接运行 `hb` 命令绕开。想堵上，在 Claude Code 里禁止助手运行 `hb`（例如在设置的 `permissions.deny` 里加 `Bash(hb *)`，用 `/permissions` 确认生效）。
- 确认弹窗靠客户端支持 MCP 的 elicitation。用 SDK 的客户端测过，在 Claude Code 里实际弹出的样子我没有试过。
- 矿洞夺宝是「一次授权一批」：你在客户端里点一次确认，AI 才能在这个红包上最多挖 N 次（N ≤ 10，10 分钟内有效，一次只能有一个授权，每小时最多请求 3 次授权、每天 10 次）；挖哪一格由 AI 选，一格一个请求，挖之前都会核对这格没挖过、不是固定格、游戏的冷却到了。**挖到危险的东西（TNT、末影水晶、刷怪笼），或任何一次出错，授权立刻作废。**领取奖励是另一次确认。lafish 说「你随意」，发红包的人仍可以在红包上关掉「允许用令牌领」，用令牌玩的人名单上会有钥匙标记。
- 矿洞夺宝这部分，我只用假的游戏服务端测过，**没有在真实的游戏里试过**。建议第一次授权只给 1–2 次，在旁边看着。`--hints` 的风险估计也还没有拿真实对局校准过，所以返回里写的是「估计，不是保证」。

## 数据和安全

- 登录会话存进系统钥匙串（Windows 凭据管理器、macOS 钥匙串、Linux 的 Secret Service）。没有可用钥匙串时
  （比如没有桌面环境的 Linux）改存到 `<配置目录>/sessions.json`，只有你能读（权限 0600），**但没有加密**，
  `hb login` 会提示一次。
- 配置目录默认是系统的用户配置目录下的 `hb`，用 `HB_HOME` 改。
- `hb` 不输出请求日志，`--json` 的输出里也没有 cookie、令牌或密钥。
- 唯一会在本地写下的记录是 `--hints-log`（矿洞夺宝建议的校准日志，`hints-log.jsonl`，在配置目录里），里面只有每次挖掘的格子、建议和结果，不含登录信息。
- 如果你保存了浏览器抓包（HAR）来研究接口，里面有你的 cookie 和凭据：别提交、别分享。仓库的 `.gitignore` 已排除 `*.har`。

### 环境变量

| 变量 | 作用 |
|---|---|
| `HB_HOME` | 配置和会话的目录 |
| `HB_LANG` | 设成 `en` 切到英文提示（默认中文） |
| `HB_BROWSER` | 登录用的浏览器：`chrome`、`edge`、`brave`、`vivaldi`、`chromium`、`firefox`、`librewolf`、`zen`，或 exe 路径 |
| `HB_ASSETS` | Minecraft 图标的缓存目录 |

## 开发

```sh
go build ./...
go vet ./...
go test ./...
```

- [`docs/api-notes.md`](docs/api-notes.md)：各站接口的整理，标明哪些来自源码、哪些来自抓包、哪些还没验证。
- [`docs/THIRD_PARTY.md`](docs/THIRD_PARTY.md)：用到的别人的协议和文件，以及 Minecraft 素材为什么不随 hb 分发。

### 发版

改仓库根目录的 `VERSION`（`<major>.<minor>.<patch>`）并提交，然后打同名标签：

```sh
git tag v0.1.0 && git push origin v0.1.0
```

`release` 工作流会核对标签和 `VERSION` 一致，跑 `go vet`、`go test`，用 goreleaser 编出六个平台的压缩包，附到一个**草稿**
Release 上；看过之后手动发布。`hb --version` 显示的是 `<VERSION>+<构建日期>.<提交数>.g<短 sha>`，压缩包文件名只用标签的版本号。

## 许可

[MIT](LICENSE)。

hb 和 Minecraft、Mojang、Microsoft、LINUX DO、lafish、Unsnow 都没有隶属关系。
