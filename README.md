# hb

LINUX DO 红包的命令行工具。一个命令行里看 lafish（hb.lafi.sh）和 Unsnow（hb.unsnow.org）的红包，
还能在终端里玩 lafish 上的「矿洞夺宝」。

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

平台：Windows 是主要的开发环境；macOS 和 Linux 能交叉编译、`go vet` 通过，但 macOS 没有在真机上跑过。

## 开始用

### lafish

```sh
hb login lafish        # 选一种登录方式，会话存进系统钥匙串
hb whoami              # 看各站点现在登录的是谁
hb play <红包链接|id>   # 认出矿洞夺宝就进终端游戏；否则看文字信息
```

`hb login` 的五种方式：`ext`（浏览器扩展，用你日常浏览器里已有的登录，装一次）、`clone`（复制日常浏览器的
profile 到调试窗口；Chromium 系要先关浏览器，Firefox 系直接读 cookie）、`paste`（手动粘贴 cookie）、
`clean`（独立的干净浏览器，登录一次）、`token`（粘贴你在网页「我的 → 个人令牌」创建的 `hbp_…` 令牌，不用浏览器，
也不用 cookie）。不确定就直接运行，它会问。

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

## 许可

[MIT](LICENSE)。

hb 和 Minecraft、Mojang、Microsoft、LINUX DO、lafish、Unsnow 都没有隶属关系。
