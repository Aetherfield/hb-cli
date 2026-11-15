# Third-party material

hb reads other people's public protocols and, optionally, fetches other people's
files. Nothing below is bundled in hb's releases except where marked.

| What | Where it comes from | How hb uses it | In hb's repo / releases? |
|---|---|---|---|
| Ecoji V2 alphabet (1024 emoji) | Keith Turner, github.com/keith-turner/ecoji v2.0.2 (Apache-2.0), as frozen by lafish (LeUKi/hb `packages/contracts/src/ecoji-v2/alphabet.ts`) | Decode lafish packet links `https://hb.lafi.sh/🧧/<14 emoji>` into the packet UUID (`internal/ecoji`) | Yes: `internal/ecoji/alphabet.go`, with its SPDX header |
| 矿洞夺宝 protocol and asset manifest | github.com/LeUKi/kddb (MIT): `game/worker`, `page/frontend/v3/src`, `docs/`, and `.../minecraft/1.21.4/manifest.json` | A terminal client for the game (`internal/game/kddb`); the list of textures and their SHA-256 (`internal/assets/wanted.go`, generated from the manifest) | Our own client code, plus the hash list. No kddb code is copied |
| Minecraft textures | Mojang / Microsoft, from the official 1.21.4 client JAR (`piston-data.mojang.com`) | Pixel icons in the terminal game. **Fetched on the player's request** by `hb assets fetch`: the JAR's SHA-1 and every extracted file's SHA-256 are checked, then the JAR is deleted. Kept in the user's cache dir | **No.** Not in the repository and not in releases. hb draws its own 8x8 icons when they are absent |
| Minecraft Wiki inventory icons | minecraft.wiki: 8 files (`Invicon TNT`, shield, ender chest, vault, spawner, ancient debris, map, enchanted golden apple). Each file's Wiki page says `License Mojang`; the list, URLs and hashes come from kddb's `inventory/provenance.json` (MIT) | The 3D-looking icons the kddb page uses for what the JAR has no stand-alone texture for. **Fetched on the player's request** by `hb assets fetch`; SHA-1 and SHA-256 checked; if the current Wiki file differs, the recorded version is looked up in the file's history | **No.** Not in the repository or releases |
| lafish API (claim, plaza, page identity, open API, points issuer protocol) | github.com/LeUKi/hb (AGPL-3.0) and its public docs | Read as a specification; hb is a separate client and copies none of its code | No |
| LDC (credit.linux.do) API | github.com/linux-do/credit (Apache-2.0), `docs/swagger.json` | Read as a specification | No |
| Unsnow (hb.unsnow.org) | Closed source. Its published read-only CLI API: `https://hb.unsnow.org/openapi.json` (2026-11-15, v1.1.0) | The only API hb calls (`/api/cli/v1/...`, with the account's own personal token); `internal/provider/unsnow` | No |

Minecraft is a trademark of Mojang / Microsoft. hb has no affiliation with
Minecraft, Mojang, Microsoft, LINUX DO, lafish or Unsnow.
