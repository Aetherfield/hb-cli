package kddb

import (
	"fmt"
	"image"
	"strconv"
	"strings"
)

// Pixel art for the terminal. A cell is drawn with half-block characters: "▀"
// with the upper pixel as foreground and the lower as background, so a
// character holds two pixels and a t-pixel icon takes t columns by t/2 rows.
// Smaller terminal fonts mean more columns and rows, which lets hb pick a
// bigger icon.
//
// Icons come from Minecraft's own textures when the player has fetched them
// (`hb assets fetch`, see internal/assets): Mojang's files are not in this
// repository. For things that have no stand-alone texture in the client
// (shield, vault, spawner, ender chest, map), and when nothing was fetched,
// hb draws its own 8x8 art below.

type rgb struct {
	r, g, b uint8
	on      bool // false = transparent
}

type grid [][]rgb

func hexColor(s string) rgb {
	s = strings.TrimPrefix(s, "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return rgb{}
	}
	return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v), true}
}

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

func mix(a, b rgb, t float64) rgb {
	if !a.on {
		return b
	}
	if !b.on {
		return a
	}
	f := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return rgb{f(a.r, b.r), f(a.g, b.g), f(a.b, b.b), true}
}

type sprite struct {
	art [8]string
	pal map[byte]string
}

// spriteArt is hb's own 8x8 art: the fallback.
var spriteArt = map[Kind]sprite{
	"emerald": {[8]string{"........", "..gGGg..", ".gGwGGg.", "gGwGGGGg", "gGGGGGdd", ".gGGGdd.", "..gddd..", "........"},
		map[byte]string{'g': "#2ecc71", 'G': "#1faa59", 'w': "#d5ffe6", 'd': "#12633a"}},
	"diamond": {[8]string{"........", "..cCCc..", ".cCwCCc.", "cCwCCCCc", "cCCCCCbd", ".cCCCbd.", "..cbbd..", "........"},
		map[byte]string{'c': "#7fe9f5", 'C': "#3ccfe0", 'w': "#e8fdff", 'b': "#1b9fb0", 'd': "#0c6a78"}},
	"apple": {[8]string{"...ll...", "..llyy..", ".yyYYyy.", "yYYYYYYy", "yYwYYYYy", "yYYYYYYy", ".yYYYYy.", "..yyyy.."},
		map[byte]string{'l': "#3c9b3c", 'y': "#e8b923", 'Y': "#ffd43b", 'w': "#fff6c0"}},
	"totem": {[8]string{".ggggg..", "g.hh..g.", "..hh....", "...hh...", "..hhhh..", ".hh..hh.", "hh....hh", "h......h"},
		map[byte]string{'g': "#c9ced6", 'h': "#8b5a2b"}},
	"tnt": {[8]string{"rrrrrrrr", "rRRRRRRr", "wwwwwwww", "wTTwNNww", "wwTwNwNw", "wwTwNNww", "rRRRRRRr", "rrrrrrrr"},
		map[byte]string{'r': "#a93226", 'R': "#e74c3c", 'w': "#f1f1f1", 'T': "#222222", 'N': "#222222"}},
	"pearl": {[8]string{"..dddd..", ".dtttdd.", "dtwtttdd", "dttttttd", "dttttttd", "dtttttdd", ".dtttdd.", "..dddd.."},
		map[byte]string{'d': "#0b4d40", 't': "#1d8f7a", 'w': "#9ff2dc"}},
	"eye": {[8]string{"..dddd..", ".dGGGGd.", "dGwwwwGd", "dGwPPwGd", "dGwPPwGd", "dGwwwwGd", ".dGGGGd.", "..dddd.."},
		map[byte]string{'d': "#14532d", 'G': "#2f9c63", 'w': "#fde68a", 'P': "#7c3aed"}},
	"shield": {[8]string{"bbbbbbbb", "bsSSSSsb", "bsBBBBsb", "bsBssBsb", "bsBBBBsb", ".bsSSsb.", "..bssb..", "...bb..."},
		map[byte]string{'b': "#5a3b1c", 's': "#8a8a8a", 'S': "#b5b5b5", 'B': "#3f6fb5"}},
	"spyglass": {[8]string{".....yy.", "....yYy.", "...bYy..", "..bbb...", ".bbB....", "bbB.....", "bB......", "B......."},
		map[byte]string{'y': "#d4a017", 'Y': "#f5d76e", 'b': "#7c4a1e", 'B': "#a8652a"}},
	"crystal": {[8]string{"...pp...", "..pPPp..", ".pPwwPp.", "pPwRRwPp", "pPwRRwPp", ".pPwwPp.", "..pPPp..", "...pp..."},
		map[byte]string{'p': "#a21caf", 'P': "#d946ef", 'w': "#fbcfe8", 'R': "#ef4444"}},
	"coal": {[8]string{"........", "..kkk...", ".kKKkk..", ".kKwkkk.", "..kkkKk.", ".kkKkk..", "..kkk...", "........"},
		map[byte]string{'k': "#1f2937", 'K': "#374151", 'w': "#6b7280"}},
	"stick": {[8]string{".......b", "......bB", ".....bB.", "....bB..", "...bB...", "..bB....", ".bB.....", "bB......"},
		map[byte]string{'b': "#7c4a1e", 'B': "#a8652a"}},
	"map": {[8]string{"pppppppp", "pwwwwwwp", "pwGGwBBp", "pwGgwBBp", "pwwwwRwp", "pwBBwwwp", "pwwwwwwp", "pppppppp"},
		map[byte]string{'p': "#7a5b32", 'w': "#e8d9a8", 'G': "#4caf50", 'g': "#2e7d32", 'B': "#4a90d9", 'R': "#d9534f"}},
	"potion": {[8]string{"...cc...", "...cc...", "..cccc..", ".cPPPPc.", "cPPpPPPc", "cPPPPPPc", ".cPPPPc.", "..cccc.."},
		map[byte]string{'c': "#cfd8dc", 'P': "#d946ef", 'p': "#f5d0fe"}},
	"key": {[8]string{"..yyy...", ".yY.Yy..", ".yY.Yy..", "..yyy...", "...y....", "...yy...", "...y....", "...yy..."},
		map[byte]string{'y': "#d4a017", 'Y': "#f5d76e"}},
	"vault": {[8]string{"dddddddd", "dCCCCCCd", "dCbbbbCd", "dCbKKbCd", "dCbKKbCd", "dCbbbbCd", "dCCCCCCd", "dddddddd"},
		map[byte]string{'d': "#2b2b2b", 'C': "#6b7280", 'b': "#9ca3af", 'K': "#fbbf24"}},
	"deco": {[8]string{"gggggggg", "gGGgGGGg", "gggggggg", "GGgGGGgG", "gggggggg", "gGGgGGGg", "gggggggg", "GGgGGGgG"},
		map[byte]string{'g': "#4b5563", 'G': "#374151"}},
	"lava": {[8]string{"oOoOOoOo", "OyOoOyOO", "oOOyOOoO", "OoOOoyOo", "oOyOOoOO", "OOoOyOOo", "oyOOoOOy", "OoOyOoOo"},
		map[byte]string{'o': "#ea580c", 'O': "#f97316", 'y': "#fde047"}},
	"debris": {[8]string{"pppppppp", "pPPpPPPp", "pPdPPdPp", "pppPPppp", "pPPdPPPp", "pPppPPdp", "pPPPpPPp", "pppppppp"},
		map[byte]string{'p': "#5b3a4a", 'P': "#7a5163", 'd': "#3a2430"}},
	"cobweb": {[8]string{"w..w..w.", ".w.w.w..", "..www...", "wwwwwwww", "..www...", ".w.w.w..", "w..w..w.", "........"},
		map[byte]string{'w': "#e5e7eb"}},
	"spawner": {[8]string{"gggggggg", "gdd..ddg", "gd.rr.dg", "g.rRRr.g", "g.rRRr.g", "gd.rr.dg", "gdd..ddg", "gggggggg"},
		map[byte]string{'g': "#4b5563", 'd': "#111827", 'r': "#7f1d1d", 'R': "#ef4444"}},
	"minecart": {[8]string{"........", ".i....i.", ".iiiiii.", ".ibbbbi.", ".ibbbbi.", ".iiiiii.", "..oo.oo.", "........"},
		map[byte]string{'i': "#9ca3af", 'b': "#7c4a1e", 'o': "#374151"}},
	"ender": {[8]string{"dddddddd", "dpppppPd", "dpPPPPpd", "dddddddd", "dpPPPPpd", "dpPyyPpd", "dpPPPPpd", "dddddddd"},
		map[byte]string{'d': "#1b1034", 'p': "#3b2a6b", 'P': "#5b43a3", 'y': "#7dd3fc"}},
}

// builtin returns hb's own 8x8 art for a kind, or nil.
func builtin(k Kind) grid {
	s, ok := spriteArt[k]
	if !ok {
		return nil
	}
	pal := map[byte]rgb{}
	for ch, h := range s.pal {
		pal[ch] = hexColor(h)
	}
	g := make(grid, 8)
	for y := range g {
		g[y] = make([]rgb, 8)
		for x := 0; x < 8 && x < len(s.art[y]); x++ {
			g[y][x] = pal[s.art[y][x]] // '.' and unknown letters stay transparent
		}
	}
	return g
}

// inventoryOf maps what is dug to the Minecraft Wiki's inventory render of it
// (the page's ICONS): fetched by `hb assets fetch`, 32x32 (the map is 16x16).
var inventoryOf = map[Kind]string{
	"tnt": "inventory/tnt.png", "apple": "inventory/enchanted_golden_apple.gif",
	"shield": "inventory/shield.png", "ender": "inventory/ender_chest.png",
	"vault": "inventory/vault.png", "spawner": "inventory/spawner.png",
	"debris": "inventory/ancient_debris.png", "map": "inventory/map.png",
}

// textureOf maps what is dug to the client's own texture.
var textureOf = map[Kind]string{
	"emerald": "textures/item/emerald.png", "diamond": "textures/item/diamond.png",
	"apple": "textures/item/golden_apple.png", "totem": "textures/item/iron_pickaxe.png",
	"tnt": "textures/block/tnt_side.png", "pearl": "textures/item/ender_pearl.png",
	"eye": "textures/item/ender_eye.png", "crystal": "textures/item/end_crystal.png",
	"spyglass": "textures/item/spyglass.png", "coal": "textures/item/coal.png",
	"stick": "textures/item/stick.png", "key": "textures/item/trial_key.png",
	"minecart": "textures/item/chest_minecart.png", "cobweb": "textures/block/cobweb.png",
	"lava": "textures/block/lava_still.png", "deco": "textures/block/tuff_bricks.png",
	"debris": "textures/block/ancient_debris_side.png",
	"rail":   "textures/block/rail.png", // decoration of the mine biome
}

// stoneOf is what an unopened cell looks like in each biome.
var stoneOf = map[string]string{
	"": "textures/block/stone.png", "mine": "textures/block/cobblestone.png",
	"trial": "textures/block/tuff_bricks.png", "nether": "textures/block/netherrack.png",
	"deep": "textures/block/deepslate.png", "end": "textures/block/end_stone.png",
}

const (
	potionBottle  = "textures/item/potion.png"
	potionOverlay = "textures/item/potion_overlay.png"
	hasteColor    = "#d9c043" // the haste potion's liquid, as the web page tints it
)

func fromImage(img *image.NRGBA, size int) grid {
	if img == nil {
		return nil
	}
	b := img.Bounds()
	if b.Dx() < size || b.Dy() < size {
		return nil
	}
	g := make(grid, size)
	for y := 0; y < size; y++ {
		g[y] = make([]rgb, size)
		for x := 0; x < size; x++ { // the first frame of an animation strip is the top size x size
			c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			if c.A >= 128 {
				g[y][x] = rgb{c.R, c.G, c.B, true}
			}
		}
	}
	return g
}

// masterOf returns the icon source for a kind: the client's texture if it was
// fetched, else hb's own art, else nil.
func (m *Model) masterOf(k Kind) grid {
	if g, ok := m.masters[k]; ok {
		return g
	}
	var g grid
	if m.opts.Pack != nil {
		// The page draws these from the Wiki's inventory renders (the TNT block,
		// the shield, chests, ...), which look much more like the game than a
		// flat texture; prefer them when they were fetched.
		if p := inventoryOf[k]; p != "" {
			if img := m.opts.Pack.Image(p); img != nil {
				g = fromImage(img, img.Bounds().Dx())
			}
		}
		switch {
		case g != nil:
		case k == "potion":
			bottle, liquid := fromImage(m.opts.Pack.Image(potionBottle), 16), fromImage(m.opts.Pack.Image(potionOverlay), 16)
			if bottle != nil && liquid != nil {
				tint := hexColor(hasteColor)
				g = make(grid, 16)
				for y := range g {
					g[y] = make([]rgb, 16)
					for x := range g[y] {
						if l := liquid[y][x]; l.on { // the liquid layer times the effect colour, under the bottle
							g[y][x] = rgb{uint8(int(l.r) * int(tint.r) / 255), uint8(int(l.g) * int(tint.g) / 255), uint8(int(l.b) * int(tint.b) / 255), true}
						}
						if b := bottle[y][x]; b.on {
							g[y][x] = b
						}
					}
				}
			}
		case textureOf[k] != "":
			g = fromImage(m.opts.Pack.Image(textureOf[k]), 16)
			if k == "totem" && g != nil {
				g = doublePick(g) // the extra chances are shown as two pickaxes
			}
		}
	}
	if g == nil {
		g = builtin(k)
	}
	if m.masters == nil {
		m.masters = map[Kind]grid{}
	}
	m.masters[k] = g
	return g
}

// scale resamples a square grid to t x t: box averaging when shrinking, the
// nearest pixel when enlarging (hb's 8x8 art at 16), unchanged when equal.
func scale(src grid, t int) grid {
	n := len(src)
	out := make(grid, t)
	for y := 0; y < t; y++ {
		out[y] = make([]rgb, t)
		for x := 0; x < t; x++ {
			y0, y1 := y*n/t, max((y+1)*n/t, y*n/t+1)
			x0, x1 := x*n/t, max((x+1)*n/t, x*n/t+1)
			var r, g, b, on, total float64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					total++
					if p := src[yy][xx]; p.on {
						r, g, b, on = r+float64(p.r), g+float64(p.g), b+float64(p.b), on+1
					}
				}
			}
			if on > 0 && on*2 >= total { // mostly covered: opaque, with the average colour
				out[y][x] = rgb{uint8(r / on), uint8(g / on), uint8(b / on), true}
			}
		}
	}
	return out
}

// icon is a kind scaled to t pixels, cached.
func (m *Model) icon(k Kind, t int) grid {
	key := iconKey{k, t}
	if g, ok := m.icons[key]; ok {
		return g
	}
	var g grid
	if src := m.masterOf(k); src != nil {
		g = scale(src, t)
	}
	if m.icons == nil {
		m.icons = map[iconKey]grid{}
	}
	m.icons[key] = g
	return g
}

type iconKey struct {
	k Kind
	t int
}
