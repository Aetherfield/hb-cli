// Package i18n holds the tiny message catalog used for CLI output.
// Chinese is the default; HB_LANG=en switches to English.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

type Lang string

const (
	ZH Lang = "zh"
	EN Lang = "en"
)

// Current returns the active language from HB_LANG (default zh).
func Current() Lang {
	if strings.HasPrefix(strings.ToLower(os.Getenv("HB_LANG")), "en") {
		return EN
	}
	return ZH
}

var catalog = map[string]map[Lang]string{
	"root.short": {
		ZH: "hb — LINUX DO 红包命令行工具",
		EN: "hb — red-packet CLI for LINUX DO",
	},
	"root.long": {
		ZH: "查红包、玩矿洞夺宝。领取和人机验证永远由你自己来，hb 不会替你循环、轮询或重试。",
		EN: "Look up red packets, play 矿洞夺宝. Claiming and human checks are always yours; hb never loops, polls or retries for you.",
	},
	"login.short": {
		ZH: "登录某个站点并保存会话",
		EN: "Log in to a site and save the session",
	},
	"login.opening": {
		ZH: "正在打开浏览器登录 %s ...",
		EN: "opening a browser to log in to %s ...",
	},
	"login.waiting": {
		ZH: "请在弹出的浏览器里完成登录，我会自动检测",
		EN: "log in in the browser window; I'll detect it automatically",
	},
	"login.saved": {
		ZH: "已保存 %s 的会话 (profile: %s)",
		EN: "saved %s session (profile: %s)",
	},
	"login.pick.provider": {ZH: "登录哪个站点？", EN: "Which site?"},
	"login.pick.method":   {ZH: "怎么登录？", EN: "How do you want to log in?"},
	"login.m.ext": {
		ZH: "浏览器扩展 — 用你日常浏览器里已有的登录（推荐，装一次）",
		EN: "Browser extension — reuse your everyday browser's login (recommended, install once)",
	},
	"login.m.clone": {
		ZH: "[已弃用] 复制日常浏览器的 profile — 新版 Chrome/Edge 的应用绑定加密让副本读不到登录；Firefox 系仍可用",
		EN: "[deprecated] Copy the everyday browser's profile — app-bound encryption in recent Chrome/Edge keeps the copy from reading the login; Firefox still works",
	},
	"login.clone.deprecated": {
		ZH: "clone 已弃用：新版 Chrome / Edge 用「应用绑定加密」保存 cookie，复制出来的 profile 读不到登录，只能在弹出的窗口里重新登录一次。\n  建议改用 --method token（个人令牌）或 --method ext（浏览器扩展）；要一个能留下来的独立浏览器就用 --method clean。Firefox 系不受影响。",
		EN: "clone is deprecated: recent Chrome / Edge store cookies under app-bound encryption, so a copied profile cannot read the login and you have to log in again in the window that opens.\n  Use --method token (personal token) or --method ext (browser extension); for a browser of its own that stays, --method clean. Firefox is not affected.",
	},
	"login.m.paste": {
		ZH: "手动粘贴 cookie",
		EN: "Paste a cookie by hand",
	},
	"login.m.clean": {
		ZH: "独立干净浏览器 — 登录一次，之后各站共用（仅 Chromium 系：Chrome/Edge/Brave…）",
		EN: "Clean dedicated browser — log in once, shared by all sites (Chromium family only)",
	},
	"login.m.token": {
		ZH: "个人令牌 — 粘贴你在网页「我的 → 个人令牌」创建的令牌（不用浏览器，也不用 cookie）",
		EN: "Personal token — paste one made on the site's 我的 page (no browser, no cookie)",
	},
	"login.ext.manual": {
		ZH: "也可以自己从 %s 下载 v%s 的 zip，再运行 hb login --method ext --ext-zip <下载的 zip>，hb 核对后帮你解压。",
		EN: "Or download the v%[2]s zip from %[1]s yourself and run hb login --method ext --ext-zip <the zip>; hb checks and unpacks it.",
	},
	"login.ext.unpacked": {
		ZH: "已核对并解压 %s",
		EN: "checked and unpacked %s",
	},
	"login.ext.downloading": {
		ZH: "下载扩展 hb session bridge v%s：%s",
		EN: "downloading the extension hb session bridge v%s: %s",
	},
	"login.ext.install": {
		ZH: "Chrome/Edge/Brave 等:\n  1) 打开 chrome://extensions（Edge: edge://extensions），开启「开发者模式」\n  2) 「加载已解压的扩展程序」选择: %s（只需装一次）\nFirefox 系:\n  1) 打开 about:debugging#/runtime/this-firefox → 「临时载入附加组件」，选上面目录里的 manifest.json\n  2) 在 about:addons → 该扩展 → 权限，允许访问站点（重启 Firefox 后需重新载入）",
		EN: "Chrome/Edge/Brave etc.:\n  1) open chrome://extensions (Edge: edge://extensions), enable Developer mode\n  2) \"Load unpacked\" and pick: %s (one-time)\nFirefox family:\n  1) open about:debugging#/runtime/this-firefox -> \"Load Temporary Add-on\", pick manifest.json in that folder\n  2) in about:addons -> the extension -> Permissions, allow site access (re-load after a Firefox restart)",
	},
	"login.ext.code": {
		ZH: "3) 在已登录 %s 的浏览器里点扩展图标，输入配对码: %s\n等待扩展发送中 ...",
		EN: "3) in the browser logged in to %s, click the extension icon and enter pairing code: %s\nwaiting for the extension ...",
	},
	"login.clone.warn": {
		ZH: "会把日常浏览器的登录文件（含所有站点 cookie）复制到 %s，用完默认删除。请先彻底关闭该浏览器。",
		EN: "This copies your everyday browser's login files (cookies of ALL sites) to %s and deletes them afterwards. Close that browser completely first.",
	},
	"login.paste.title": {ZH: "粘贴 cookie「%s」的值", EN: "Paste the value of cookie %q"},
	"login.paste.desc": {
		ZH: "在 %s 登录后，F12 → 应用 → Cookie 里复制 %s 的值（输入已隐藏）",
		EN: "Log in at %s, then F12 → Application → Cookies and copy %s (input is hidden)",
	},
	"login.pick.browser": {ZH: "用哪个浏览器？", EN: "Which browser?"},
	"login.default.tag":  {ZH: "（系统默认）", EN: " (system default)"},
	"login.needchromium": {
		ZH: "%s 没有调试协议(CDP)，这个方式用不了。请换成 Chrome/Edge/Brave 等，或改用 ext / clone / paste 方式",
		EN: "%s has no DevTools protocol (CDP), so this method can't drive it. Use Chrome/Edge/Brave, or the ext / clone / paste methods",
	},
	"login.nochromium": {
		ZH: "没找到 Chrome/Edge/Brave 等 Chromium 系浏览器；该方式需要它们。可改用 ext / clone / paste",
		EN: "no Chromium-family browser found; this method needs one. Use ext / clone / paste instead",
	},
	"login.gecko.info": {
		ZH: "从 %s 的 profile 直接读取 cookie（无需关闭浏览器）: %s",
		EN: "reading cookies straight from %s's profile (the browser may stay open): %s",
	},
	"login.gecko.none": {
		ZH: "%s 里没有找到登录 cookie，请先在该浏览器登录 %s 再试",
		EN: "no login cookie found in %s; log in to %s in that browser first",
	},
	"login.file.warn": {
		ZH: "注意：这台机器没有可用的系统钥匙串（比如没有桌面环境的 Linux），会话改存在 %s。文件权限只有你能读，但没有加密。",
		EN: "note: no usable system keychain on this machine (e.g. Linux without a desktop session); the session is stored in %s. Only you can read the file, but it is not encrypted.",
	},
	"login.ua.title": {ZH: "浏览器的 User-Agent", EN: "Your browser's User-Agent"},
	"login.ua.desc": {
		ZH: "该站在 Cloudflare 后面，cf_clearance 绑定 UA。在已登录 %s 的浏览器按 F12 → 控制台，输入 navigator.userAgent 回车，复制结果粘贴到这里",
		EN: "This site is behind Cloudflare and cf_clearance is tied to the UA. In the browser logged in to %s press F12 -> Console, run navigator.userAgent and paste the result here",
	},
	"login.closed": {
		ZH: "浏览器已关闭，登录未完成",
		EN: "browser was closed before login completed",
	},
	"logout.short": {
		ZH: "删除某个站点已保存的会话",
		EN: "Delete a saved site session",
	},
	"logout.done": {
		ZH: "已删除 %s 的会话 (profile: %s)",
		EN: "deleted %s session (profile: %s)",
	},
	"whoami.short": {
		ZH: "显示各站点当前登录的身份",
		EN: "Show who you are logged in as on each site",
	},
	"doctor.short": {
		ZH: "检查配置、网络和时钟，排查问题",
		EN: "Check config, connectivity and clock to diagnose problems",
	},
	"doctor.h.config":   {ZH: "配置目录", EN: "Config directory"},
	"doctor.h.browsers": {ZH: "浏览器", EN: "Browsers"},
	"doctor.h.sites":    {ZH: "站点", EN: "Sites"},
	"doctor.down":       {ZH: "不可达", EN: "unreachable"},
	"doctor.nobrowser":  {ZH: "没找到浏览器（登录的 ext、clone、clean 方式要用）", EN: "none found (the ext, clone and clean login methods need one)"},
	"doctor.config": {
		ZH: "配置目录: %s",
		EN: "config dir: %s",
	},
	"doctor.reach.ok": {
		ZH: "%-22s 可达 (%d ms, HTTP %d)",
		EN: "%-22s reachable (%d ms, HTTP %d)",
	},
	"doctor.reach.fail": {
		ZH: "%-22s 不可达: %v",
		EN: "%-22s unreachable: %v",
	},
	"doctor.skew": {
		ZH: "与 %s 的时钟偏差: %s",
		EN: "clock skew vs %s: %s",
	},
	"doctor.skew.warn": {
		ZH: "时钟偏差超过 60 秒，签名/会话可能异常",
		EN: "clock skew over 60s; signatures/sessions may misbehave",
	},
}

// T returns the message for key in the active language, formatted with args.
func T(key string, args ...any) string {
	m, ok := catalog[key]
	if !ok {
		return key
	}
	s, ok := m[Current()]
	if !ok {
		s = m[ZH]
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}
