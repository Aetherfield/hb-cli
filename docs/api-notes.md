# API notes

What is confirmed, and from where. "Unconfirmed" means not seen in source or traffic.
No secrets, cookies or user data belong in this file.

## Unsnow (hb.unsnow.org), closed source

### Official CLI API (what `hb` uses, since 2026-11-15)
The site now publishes a read-only CLI API: <https://hb.unsnow.org/openapi.json> ("❄ LDC 红包 · CLI 只读 API",
v1.1.0). `hb` talks only to it. Everything below this section is the older HAR-based reverse engineering of the
web page's own API, kept as background.
- Auth: one personal token per account, `hbr_` + 43 base64url characters, made and revoked on the web page
  (我的 → 认证令牌), shown once, scope fixed to `hongbao:read`, no expiry. Only `Authorization: Bearer`; no cookie,
  `cf_clearance` or browser User-Agent needed (network-level risk control may still refuse a request).
- Endpoints (all GET, under `/api/cli/v1`): `/me` (account, `claim_security`, `verification_url`; works while a
  check is due), `/hongbao/code/{code}` (2 letters + 8 digits, upper-cased; old 8 digits accepted),
  `/hongbao/text-code/{code}` (6-32 汉字/letters/digits, case-sensitive, URL-encoded), `/hongbao/{id}` (ids from a
  link only, never enumerated), `/hongbao/plaza`, `/hongbao/sent`, `/hongbao/received` (`limit` 1-50, `offset`
  ≤ 10000; any other query parameter is a 400 `invalid_history_query`).
- A packet carries the server's verdict: `available`, `can_claim`, `reason`; `progress.kind` is `shares` or `cells`
  (a hidden-result 刮刮乐: cells picked, not winners). Text-code answers may carry `text_code_locked(_until)`.
- Not in the API: claiming, lottery entry, picking scratch cells, the claim challenge, live events, token management.
  Claim by opening `link` in a browser (Turnstile stays).
- Limits, shared with the web page: passcode lookups 1 per 5 s per account (100 per 5 s per IP); plaza 60/60 s;
  packet-by-id and history 120/60 s; `/me` 60/60 s. 429 carries `Retry-After` and `retry_after` (seconds, ≥ 1).
  Misses (well-formed but unknown passcodes) accumulate; at the 10th and 20th, then every 5th, a Google reCAPTCHA
  is due: `403 lookup_captcha_required` (`lookup_counted`), and every lookup stops until the user completes it at
  `https://hb.unsnow.org/?cli_verify=1`. A malformed passcode is a 400 that does not count as a miss but still uses
  the lookup quota, so `hb` checks the shape before sending.
- Client rules: requests only when the user asks; no guessing, scanning, polling, loops or automatic retries
  (401/403/404/410/5xx included); a non-JSON (HTML) answer is shown as a generic error.
- The old `payout_status: "uncertain"` captures below were a test setup problem, not the API's meaning (see there).
- Checked against the live site: the user reported on 2026-11-15 that `hb`'s Unsnow commands pass with a real token.
  Which commands, and what the answers looked like, was not recorded; a captured real answer would settle the
  response shapes above, which so far come from the published OpenAPI document and a mock server.


Source: a HAR of the user's own session (2026-11-14) plus the site's public front-end JS inside it
(`/js/app.js`, `claim-security.js`, `lookup-security.js`, `community.js`).

### Auth
- Login is OAuth2 authorization-code with PKCE at `connect.linux.do/oauth2/authorize`; the redirect URI is fixed to
  `https://hb.unsnow.org/auth/callback`, so only a browser can complete it (the CLI cannot be the OAuth client).
  `GET /auth/login?redirect=` starts it, `GET /auth/logout` ends it. connect.linux.do itself sits behind a Cloudflare challenge.
- Session cookies on every API call: `hb_sess`, `hb_sess.sig` (signed session), and **`cf_clearance`** (Cloudflare).
  The browser used Edge 157 on Windows; `cf_clearance` is normally bound to the User-Agent and IP, so the CLI must send the
  same User-Agent. Whether Go's TLS fingerprint passes Cloudflare with these cookies was never confirmed, and no longer
  matters: `hb` uses the official token API (top of this section), which needs no cookie.
- No CSRF header was seen. Requests send `Content-Type: application/json`.
- `GET /api/me` returns `user{linuxdo_id,username,name,avatar_url,trust_level}`, `isOwner`, `canCreate`,
  `creditAccount{configured,client_id_masked,updated_at,verified}`, `credit_verification`, and
  `claim_security{verification_required, consecutive_misses, threshold, server_now, banned_until}`.
  The CLI should read `claim_security` before any lookup or claim and stop near `threshold`.

### Reading
| Call | Notes |
|---|---|
| `GET /api/hongbao/plaza?limit=&offset=` | `{list[], total, has_more, server_now}`. Items include `available`, `can_claim`, `type`, `total_amount`, `expires_at`, `wish`. limit max 50 (from JS). |
| `GET /api/hongbao/mine` / `received` | `{list[]}`; received items carry `received_amount`, `received_at`, `trade_no`, `status`, `creator_*`. |
| `GET /api/hongbao/:id[?details]` | Packet detail: `type`, `total_amount`, `count`, `creator`, `joined`, `participants`, `winners`, `draw_*`, `target_username`, codes. |
| `GET /api/hongbao/:id/events?updates` | `text/event-stream`, live claim updates. |
| `GET /api/hongbao/code/:code` | Numeric passcode (2 letters + 8 digits) to packet. Unknown: `404 {"code":"code_not_found"}`. |
| `GET /api/hongbao/text-code/:code` | Text passcode to packet. Same 404. |
| `GET /api/community/stats`, `/rankings` | Totals and top senders/receivers. |

### Confirmed by `hb.unsnow.org_claim.har` (one real numeric-code claim)
- Numeric passcodes (2 letters + 8 digits): `GET /api/hongbao/code/<code>` → packet JSON (`id`, `link`, `status`, `count`,
  `claimed_count`, `total_amount`, `allowed_trust_levels`, `joined`, `expires_at`, `server_now`, ...). Text passcodes use
  `GET /api/hongbao/text-code/<code>`. A numeric code sent to `text-code` is a miss (`404 {"code":"code_not_found"}`), so
  pick the endpoint by the code's shape.
- Refusals: `409 {"error":"该文字口令对应的红包暂不可领取"}` (packet exists but not claimable);
  `429 {"code":"claim_rate_limited","retry_after":4}` with a `Retry-After` header (seconds): lookups are throttled to
  roughly one per few seconds.
- `POST /api/hongbao/:id/claim-challenge` (no body) → `{challenge_id, expires_at, server_now, action:"hongbao_claim",
  site_key, verification_required:true}`. In this capture `verification_required` was **true**.
- `POST /api/hongbao/:id/grab` body `{challenge_id, verification_token}` (the token is ~850 characters, from Cloudflare
  Turnstile on the real origin) → `202 {"already":false,"pending":true,"amount":1,"payout_status":"uncertain"}`.
  Afterwards `GET /api/hongbao/:id` shows `status:"exhausted"`, `my_payout`, and `payout_notice` (here
  `{"status":"uncertain","message":"红包并未成功分发，可能是发起人限额或积分站服务降级"}`: the grab was accepted but the
  LDC payout did not go through). Cause, per the user (2026-11-15): both test accounts were set up with the **same
  merchant ID**; one is a 青春版 account below trust level 3 (cannot receive), the other cannot pay itself. So these
  captures show what a failed payout looks like, not what `uncertain` means in general.
- Plaza (`hb.unsnow.org_claim_plaza.har`): `GET /api/hongbao/plaza?limit=20&offset=0` → `{list:[{id, type, wish,
  total_amount, creator, available, can_claim, reason, participated, status, expires_at, draw_at,
  progress:{kind:"shares",used,total}, allowed_trust_levels, max_claims_24h}], total, has_more, server_now}`;
  `can_claim`/`reason` are the server's verdict for you (`"这是你发出的红包"` for your own). Then `POST
  /api/hongbao/plaza/:id/open` (packet detail) → `claim-challenge` (Turnstile required again) → `grab`: the same 202
  `{"already":false,"pending":true,"amount":1,"payout_status":"uncertain"}`, then `status:"exhausted"` with the same
  `payout_notice`. In this capture the `GET /api/hongbao/received` list taken right afterwards did not contain either
  1.00 (newest entry was an old packet), i.e. nothing was recorded as received. Same cause as above (same merchant
  ID on both test accounts): the payout failed. A normal successful payout has not been captured yet.
- Packet types seen in `/mine`: `equal`, `random`, `lottery` (`distribution_type`, `draw_at`, `drawn_at`, status
  `drawn`), `scratch` (`scratch_reveal`), `exclusive` (`target_username`). `/received` rows carry `received_amount`,
  `received_at`, `trade_no`, `creator_username`.
- Requests were same-origin with `Origin: https://hb.unsnow.org`, a browser User-Agent and the cookies `hb_sess`,
  `hb_sess.sig`, `cf_clearance` (747 chars of Cookie header in total).
- **Consequence for `hb`**: the grab cannot be sent without a Turnstile token produced by a person on the real page.
  `hb` only does the lookup (`hb unsnow check`); the claim stays in the browser.

### Claiming (flow from `app.js` and `claim-security.js`; the first half was written before the HAR above)
1. Look up the packet by passcode (above). Wrong passcodes count as "misses". Past the threshold the server answers with
   `code: "lookup_captcha_required"`; the site then calls `POST /api/lookup-captcha/challenge` and `/verify`
   (`{challenge_id, verification_token}`), which is **Google reCAPTCHA** (human only).
2. Plaza packets: `POST /api/hongbao/plaza/:id/open` returns the packet detail ("join").
3. `POST /api/hongbao/:id/claim-challenge` returns `{verification_required, challenge_id, site_key, action}`.
   If `verification_required`, a **Cloudflare Turnstile** widget (`action`, `cData = challenge_id`) must be solved on the real
   origin. Otherwise the `challenge_id` alone is enough.
4. `POST /api/hongbao/:id/grab` with body `{challenge_id, verification_token?}`. Response used by the UI:
   `amount`, `already`, `pending`, `payout_status`. Then `GET /api/hongbao/:id` for the result.
5. Errors seen in JS: `claim_banned` (with `banned_until`, `server_now`), `credit_recipient_not_found`, `code_not_found`.
- Packet `type`: `random`, `equal`, `lottery` (timed draw, `draw_seconds`/`draw_at`), `scratch` (`scratch_reveal`).
  `target_username` makes a packet exclusive. `allowed_trust_levels` and `max_claims_24h` restrict claimers.

### Creating (user's own packets)
`POST /api/hongbao` body: `total` ("1.00"), `count`, `wish`, `type`, `is_public`, `target_username`, `code_type`
(`numeric`|`text`), `random_code_count`, `text_codes[]`, `draw_seconds`, `allowed_trust_levels[]`, `max_claims_24h`.
`POST /api/hongbao/:id/codes` adds codes (`random_code_count`, `text_codes[]`).
`POST /api/credit-account` stores the user's LDC `client_id` / `client_secret` (verified by an order query).
The CLI must never log or echo these.

### Still needed
Done: grab and claim-challenge (see above). Still unknown: a plaza packet's `open` + grab (`type: random`), the
`claim_banned` / `lookup_captcha_required` bodies, and what `payout_status` takes besides `uncertain`.

## lafish (hb.lafi.sh), open source (LeUKi/hb)
- Session cookie `hb_session` (HttpOnly, 7 days); mutating routes need `Origin: https://hb.lafi.sh` and `X-CSRF-Token`
  (from `GET /api/session`).
- Claim `POST /api/activities/:id/claim`, passcode lookup `POST /api/redemptions/discover` then `/redemptions`,
  plaza `GET /api/discovery/activities`, history `GET /api/me/claims`, balance `GET /api/me/balance`.
- Human check: `403 HUMAN_CHECK_REQUIRED` with a Cap or Turnstile challenge; the server verifies Turnstile `hostname`
  (`apps/api/src/activities/human-check.ts`), so a token solved on localhost fails.
- Open API (`Bearer hbk_...`, PLUS members): `/api/open/balance`, `/api/open/balance/transfers[/batch]`.
- Third-party points issuer protocol: `POST {url}/balance|transfer|query`, `Hb-Points-Protocol: 1`
  (`docs/third-party-points-design.md`).

### lafish attached pages (附带网页) and a localhost host
Sources: `docs/judge-page-design.md`, `kit/cli/lib/page.mjs`, `kit/cli/page/host.html`, `https://hb.lafi.sh/sdk/v1.js`.
- A judge packet may declare `meta.page`, an https URL shown in an iframe beside the packet. The page talks to the packet page
  only through the SDK (`postMessage`, messages `{hb:1,id,type,...}`; replies `{hb:1,re:id,value|error}`; pushes
  `{hb:1,event:view|hongbao|announce|me|myView,value}`). Types the page can ask: `hello`, `login`, `submit`, `fill`, `identity`, `mine`.
- **The SDK pins the host origin**: `site = new URL(<script src>).origin`; it posts only to `site`, and accepts only messages from
  `event.source === window.parent && event.origin === site`. A page framed by a localhost host therefore cannot use the SDK as served
  from hb.lafi.sh; the `<script src="https://hb.lafi.sh/sdk/v1.js">` line must be rewritten to a local copy.
- The official kit simulator (MIT) does exactly that: host page on `127.0.0.1:P1` (serves `/sdk/v1.js`), the attached page on
  `localhost:P2` (a different origin, as online), `replaceAll("https://hb.lafi.sh/sdk/v1.js", "<host>/sdk/v1.js")` on HTML, and a
  reverse proxy for remote pages (`proxyPage`).
- Boundaries (design doc): the page cannot read cookies or the login, cannot transfer or create packets, cannot see others' input,
  cannot bypass cooldown or human check; `submit` needs the viewer's consent the first time.
- Unconfirmed: which real endpoints return `view`, `announce`, `me`, `myView` and the packet's `page` URL (read `apps/api/src/activities/`
  `live.ts`, `hongbao-info.ts`, `attached-page.ts`, `page-identity.ts` in M3); how `identity` tokens are minted and whether they are
  bound to the page origin.

### 矿洞夺宝 (LeUKi/kddb, MIT): the game behind a lafish attached page
Sources: `game/worker/index.mjs`, `game/worker/auth.mjs`, `page/frontend/v3/src/{api,live,types}.ts`, `judge.js`, README.
The page is a shell; the game is an HTTP + WebSocket API on its own Worker, base `<page origin>/v3/api/`, every call with `?hb=<packet id>`.
Official site: `https://hbweb-kddb.lafi.sh`. The Worker uses `Origin` only to set CORS headers; it does not reject clients without one.
- Sign-in: `GET nonce` -> lafish `POST /api/activities/:id/page-identity {nonce}` (session + CSRF; returns `{token, expiresAt}`,
  ES256, `aud` = the page origin, valid 10 min) -> `POST hello` with `Authorization: Bearer <token>` -> `{ticket, uid, game, me, ...}`.
  The ticket lasts 2 h and is sent as `Authorization: Ticket <ticket>`; a 401 means sign in again.
- Play: `GET state`, `GET summary`, `GET since?seq=`, `POST dig {cardId,index,v:2}`, `POST use {item,cardId?,index?,choice?}`,
  `POST creeper`, `POST claim` -> `{ok, voucher, points, me}`. The voucher (`kd3.<opened-at>.<uid>.<points>.<keyid>.<sig>`) is the
  `input` for lafish's judge (`POST /activities/:id/claim`, `participationMode:"code"`), which verifies it and pays.
- Push: WebSocket `live?hb=&ticket=&v=2`, text frames, an array of messages (`cell`, `card`, `drain`, `claim`; v1 sends `dig`).
  Every message for everyone carries `seq`; a skipped seq means reload (`since` or `state`).
- The game enforces the dig interval (`rules.digInterval`, seconds), chances per person and claim scope server-side.
- hb implements this as `hb kddb <packet id>` (internal/game/kddb). Every dig is a key press; there is no auto-dig.
  Not in the TUI yet: items (`use`), the creeper timer, `summary`, presence (pointers and emotes on `/presence`).
- Unconfirmed against the live site: all of the above is from source, not from a real session; the claim answer shape for the
  lafish judge (what `ClaimCode` returns) is printed raw.

## LDC (credit.linux.do), open source (linux-do/credit)
- User routes need the session cookie `linux_do_credit_session_id` and `X-Requested-With: XMLHttpRequest`:
  `/api/v1/redenvelope/{create,claim,list,:id}`, `/api/v1/payment/transfer` (needs `pay_key`), `/api/v1/order/transactions`.
- Merchant routes (client id and secret): `/api.php` order query, `/pay/distribute`.
- A plain HEAD to `credit.linux.do/` returned HTTP 403 (probably Cloudflare); unconfirmed for API calls.
