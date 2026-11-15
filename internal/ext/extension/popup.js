// Sends the current browser's session for ONE site to the local hb CLI.
// The CLI decides which site/cookies via its /spec answer; this script only
// ever talks to 127.0.0.1.
// Firefox exposes promise APIs on `browser`; Chromium MV3 does on `chrome`.
const api = globalThis.browser ?? globalThis.chrome;
const PORTS =Array.from({ length: 10 }, (_, i) => 47321 + i);
const msg = document.getElementById("msg");
const say = (t, ok) => { msg.textContent = t; msg.className = ok ? "ok" : "err"; };

async function findCli() {
  for (const p of PORTS) {
    try {
      const r = await fetch(`http://127.0.0.1:${p}/hello`, { signal: AbortSignal.timeout(600) });
      if (r.ok && (await r.json()).hb === 1) return `http://127.0.0.1:${p}`;
    } catch (_) { /* try next port */ }
  }
  return null;
}

async function send() {
  const code = document.getElementById("code").value.trim();
  if (!/^\d{6}$/.test(code)) return say("配对码是 6 位数字", false);
  say("连接 hb ...", true);
  const base = await findCli();
  if (!base) return say("没找到正在等待的 hb（先在终端运行 hb login）", false);

  const sr = await fetch(`${base}/spec?code=${code}`);
  if (!sr.ok) return say(sr.status === 403 ? "配对码不对" : `hb 返回 ${sr.status}`, false);
  const spec = await sr.json();

  let cookies = [];
  for (const url of spec.cookie_urls) {
    cookies = cookies.concat(await api.cookies.getAll({ url }));
  }
  if (spec.cookie_names && spec.cookie_names.length) {
    cookies = cookies.filter((c) => spec.cookie_names.includes(c.name));
  }
  if (!cookies.length) return say(`浏览器里没有 ${spec.provider} 的登录 cookie，请先在这个浏览器登录该站`, false);

  let csrf = "";
  if (spec.csrf_url) {
    try {
      const j = await (await fetch(spec.csrf_url, { credentials: "include" })).json();
      csrf = (j && j[spec.csrf_field]) || "";
    } catch (_) { /* optional: CLI can fetch it itself */ }
  }

  const body = {
    csrf,
    user_agent: navigator.userAgent,
    cookies: cookies.map((c) => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path,
      expires: c.expirationDate || 0, http_only: c.httpOnly, secure: c.secure,
    })),
  };
  const pr = await fetch(`${base}/session?code=${code}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  say(pr.ok ? `已发送 ${spec.provider} 会话，回终端看结果` : `发送失败：HTTP ${pr.status}`, pr.ok);
}

document.getElementById("send").addEventListener("click", () => send().catch((e) => say(String(e), false)));
document.getElementById("code").addEventListener("keydown", (e) => { if (e.key === "Enter") document.getElementById("send").click(); });
