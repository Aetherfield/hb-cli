package kddb

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// How the kddb web page draws a cell and a card, in terminal pixels. The
// colours and proportions are the page's own (page/frontend/v3/src/paint.ts and
// thumb.ts, MIT): a dug cell is a small tile with a bright edge on the top and
// sides and a dark 3-pixel base, an undug cell is its biome's ground texture
// under a faint tint, and a card has a coloured frame and back.

type rgba struct {
	r, g, b uint8
	a       float64
}

// parseRGBA reads "#rrggbb" or "#rrggbbaa".
func parseRGBA(s string) rgba {
	s = strings.TrimPrefix(s, "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgba{}
	}
	if len(s) == 8 {
		return rgba{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), float64(uint8(v)) / 255}
	}
	return rgba{uint8(v >> 16), uint8(v >> 8), uint8(v), 1}
}

// over lays a (possibly translucent) colour on a pixel.
func over(dst rgb, s string) rgb {
	c := parseRGBA(s)
	if !dst.on {
		return rgb{c.r, c.g, c.b, true}
	}
	f := func(d, s uint8) uint8 { return uint8(float64(d)*(1-c.a) + float64(s)*c.a) }
	return rgb{f(dst.r, c.r), f(dst.g, c.g), f(dst.b, c.b), true}
}

// The page's constants.
var (
	tileColors = map[Kind]string{
		"diamond": "#20363e", "apple": "#3a302e", "tnt": "#3b292b", "totem": "#2e382e",
		"pearl": "#2c2b3c", "shield": "#34312f", "spyglass": "#2c323a", "crystal": "#33263d",
	}
	tileBorders = map[Kind]string{
		"diamond": "#496772", "apple": "#827049", "tnt": "#75504a", "totem": "#68704b",
		"pearl": "#686080", "shield": "#6c6053", "spyglass": "#66717a", "crystal": "#7f5d96",
	}
	coverTint = map[string][2]string{
		"": {"#edc38f0f", "#0e172526"}, "end": {"#fff5c114", "#2a1f402e"}, "trial": {"#e0a06414", "#0e172526"},
		"mine": {"#edc38f0f", "#0e172540"}, "nether": {"#ff906014", "#1a050526"}, "deep": {"#2fe0e012", "#04080a40"},
	}
	cardFrames = map[string][2]string{ // frame colour, back colour
		"": {"#67533e", "#211f20"}, "end": {"#d9c27a", "#2a2637"}, "trial": {"#8c6a4a", "#2b2d2a"},
		"mine": {"#8a6b45", "#2a2118"}, "nether": {"#8a3a2c", "#2a1214"}, "deep": {"#1f6a73", "#0d1418"},
	}
	torchFlame1 = []string{"#7cf08a", "#ffd75a", "#ff5a3c"}
	torchFlame2 = []string{"#7cf08a", "#ffd75a", "#ff9a3c", "#ff4b4b"}
)

const (
	openedFill   = "#19232d"
	openedBorder = "#4c5155"
	openedBase   = "#171c22"
	mineBorder   = "#b99760"
	coverBorder  = "#8c89826b"
	coverBase    = "#303136"
	cardFrame    = 3 // 2px frame colour and 1px of padding, as on the page
	tileGap      = 1
)

// stoneKey caches a biome's ground at a size.
type stoneKey struct {
	biome string
	t     int
}

// tileSize is the side of one cell's tile for an i-pixel icon: 1px border on
// each side, and a 3px base under the icon (like the page's .map-cell).
func tileSize(i int) int { return i + 4 }

// cardPixelSize is the side of a whole card in pixels.
func cardPixelSize(size, i int) int {
	return 2*cardFrame + size*tileSize(i) + (size-1)*tileGap
}

// ground is the biome's undug-cell texture scaled to t x t with the page's
// faint tint over it (nil without the pack).
func (m *Model) ground(biome string, t int) grid {
	key := stoneKey{biome, t}
	if g, ok := m.stones[key]; ok {
		return g
	}
	var g grid
	if m.opts.Pack != nil {
		if src := fromImage(m.opts.Pack.Image(groundOf[biome]), 16); src != nil {
			g = scale(src, t)
			tint := coverTint[biome]
			for y := range g {
				for x := range g[y] {
					// the page paints a diagonal gradient from the first colour to the second
					k := float64(x+y) / float64(max(1, 2*(t-1)))
					g[y][x] = over(over(g[y][x], tint[0]), withAlpha(tint[1], k))
				}
			}
		}
	}
	if m.stones == nil {
		m.stones = map[stoneKey]grid{}
	}
	m.stones[key] = g
	return g
}

// doublePick draws the page's "双铁镐": one iron pickaxe at 77%, dimmed, in the
// top-right behind a second one in the bottom-left that casts a small shadow.
func doublePick(src grid) grid {
	n := len(src)
	small := scale(src, n*77/100)
	out := make(grid, n)
	for y := range out {
		out[y] = make([]rgb, n)
	}
	off := n - len(small)
	for y := range small { // the back one
		for x := range small[y] {
			if p := small[y][x]; p.on {
				out[y][x+off] = rgb{uint8(float64(p.r) * 0.8), uint8(float64(p.g) * 0.8), uint8(float64(p.b) * 0.8), true}
			}
		}
	}
	shadow := hexColor("#13191f")
	for y := range small { // the front one's shadow, one pixel right and up
		for x := range small[y] {
			if small[y][x].on {
				if sy, sx := y+off-1, x+1; sy >= 0 && sx < n {
					out[sy][sx] = shadow
				}
			}
		}
	}
	for y := range small { // the front one
		for x := range small[y] {
			if p := small[y][x]; p.on {
				out[y+off][x] = p
			}
		}
	}
	return out
}

// withAlpha scales a colour's alpha by k (used to fade a gradient in).
func withAlpha(s string, k float64) string {
	c := parseRGBA(s)
	return fmt.Sprintf("#%02x%02x%02x%02x", c.r, c.g, c.b, uint8(c.a*k*255))
}

// groundOf is the texture an undug cell shows in each biome (the page's GROUNDS).
var groundOf = map[string]string{
	"": "textures/block/stone.png", "mine": "textures/block/stone.png",
	"trial": "textures/block/tuff_bricks.png", "nether": "textures/block/netherrack.png",
	"deep": "textures/block/deepslate.png", "end": "textures/block/end_stone.png",
}

// noise is the ground without the client's files: grey speckle.
func noise(idx, x, y int) rgb {
	h := uint32(idx*7919+x*104729+y*1299709) * 2654435761
	v := 0x6a + int((h>>24)%0x16) - 8
	return rgb{uint8(v), uint8(v), uint8(v + 3), true}
}

func fill(g grid, x0, y0, w, h int, c rgb) {
	for y := y0; y < y0+h && y < len(g); y++ {
		for x := x0; x < x0+w && x < len(g[y]); x++ {
			if y >= 0 && x >= 0 {
				g[y][x] = c
			}
		}
	}
}

// blit draws an icon (opaque pixels only) with its top-left at (x0, y0).
func blit(dst, icon grid, x0, y0 int, tint *rgb, dim float64) {
	for y := range icon {
		for x := range icon[y] {
			p := icon[y][x]
			if !p.on || y+y0 < 0 || y+y0 >= len(dst) || x+x0 < 0 || x+x0 >= len(dst[0]) {
				continue
			}
			if dim > 0 {
				p = mix(p, dst[y+y0][x+x0], dim)
			}
			if tint != nil {
				p = mix(p, *tint, 0.30)
			}
			dst[y+y0][x+x0] = p
		}
	}
}

// tile paints one cell: (i+4) x (i+4) pixels.
func (m *Model) tile(c PublicCard, idx, i int) grid {
	w := tileSize(i)
	g := make(grid, w)
	for y := range g {
		g[y] = make([]rgb, w)
	}
	cell := cellAt(c, idx)
	fixed := c.Fixed[fmt.Sprint(idx)]
	torch, lit := c.torchAt(idx)
	iconAt := func(k Kind) grid { return m.icon(k, i) }
	// The icon sits 2px in from the left and just under the top edge, above the 3px base.
	ix, iy := 2, 1

	edges := func(border, base string) {
		b, d := over(rgb{}, border), over(rgb{}, base)
		for x := 0; x < w; x++ {
			g[0][x] = b
		}
		for y := 0; y < w; y++ {
			g[y][0], g[y][w-1] = b, b
		}
		fill(g, 0, w-3, w, 3, d)
	}
	cover := func(border, base string) { // the page's translucent edge on undug cells
		for x := 0; x < w; x++ {
			g[0][x] = over(g[0][x], border)
		}
		for y := 0; y < w; y++ {
			g[y][0], g[y][w-1] = over(g[y][0], border), over(g[y][w-1], border)
		}
		for y := w - 3; y < w; y++ {
			for x := 0; x < w; x++ {
				g[y][x] = over(g[y][x], base)
			}
		}
	}

	switch {
	case cell != nil && cell.Burn == 1: // swallowed by lava: a dim tile with the lost thing in it
		fill(g, 0, 0, w, w, hexColor("#4a1c0c"))
		edges("#8a3a1c", "#2a0f06")
		blit(g, iconAt(cell.Kind), ix, iy, nil, 0.55)
	case cell != nil:
		fillC := openedFill
		if col := tileColors[cell.Kind]; col != "" {
			fillC = col
		}
		fill(g, 0, 0, w, w, hexColor(fillC))
		border := openedBorder
		if b := tileBorders[cell.Kind]; b != "" {
			border = b
		}
		if cell.UID == m.game.MyUID() {
			border = mineBorder // the cells you dug yourself have a gold edge
		}
		edges(border, openedBase)
		for x := 1; x < w-1; x++ { // the faint shadow just under the top edge
			g[1][x] = over(g[1][x], "#060b1266")
		}
		dim := 0.0
		if lit {
			dim = 0.4 // a torch in the cell dims the icon, as on the page
		}
		blit(g, iconAt(cell.Kind), ix, iy, nil, dim)
	case fixed != "":
		kind := Kind(fixed)
		if kind == "deco" && c.Kind == "mine" {
			kind = "rail"
		}
		fill(g, 0, 0, w, w, hexColor("#202020"))
		if tex := m.icon(kind, w); tex != nil && (kind == "deco" || kind == "rail" || kind == "lava") {
			blit(g, tex, 0, 0, nil, 0) // blocks and lava fill the whole tile
		} else {
			blit(g, iconAt(kind), ix, iy, nil, 0)
		}
		edges("#3a3a3a", "#141414")
	default: // not dug
		if gr := m.ground(c.Kind, w); gr != nil {
			for y := range g {
				copy(g[y], gr[y])
			}
		} else {
			for y := 0; y < w; y++ {
				for x := 0; x < w; x++ {
					g[y][x] = noise(idx, x, y)
				}
			}
		}
		cover(coverBorder, coverBase)
		if m.hintSafe(c, idx) { // the advisor: a green frame on a cell that cannot hold a danger
			green := hexColor("#2ecc71")
			for x := 0; x < w; x++ {
				g[0][x], g[1][x], g[w-1][x] = green, green, green
			}
			for y := 0; y < w; y++ {
				g[y][0], g[y][1], g[y][w-1], g[y][w-2] = green, green, green, green
			}
		}
		if k, ok := m.peekAt(c.ID, idx); ok { // only you know this one
			p := hexColor("#7c3aed")
			for y := 1; y < w-3; y++ {
				for x := 1; x < w-1; x++ {
					g[y][x] = mix(g[y][x], p, 0.35)
				}
			}
			blit(g, iconAt(k), ix, iy, &p, 0)
		}
	}

	if lit { // a torch's light, in the page's colours
		flames := torchFlame2
		if torch.V != 2 {
			flames = torchFlame1
		}
		col := hexColor(flames[min(torch.Level, len(flames)-1)])
		n := max(2, i/4)
		fill(g, w-1-n, 1, n, n, col)
		if i >= 6 {
			g[1][w-2] = mix(col, rgb{255, 255, 255, true}, 0.5)
		}
	}
	if idx == m.cur { // the cursor: a white ring, thicker on big icons
		ring := rgb{255, 255, 255, true}
		for k := 0; k < max(1, i/8); k++ {
			for x := k; x < w-k; x++ {
				g[k][x], g[w-1-k][x] = ring, ring
			}
			for y := k; y < w-k; y++ {
				g[y][k], g[y][w-1-k] = ring, ring
			}
		}
	}
	return g
}

// cardPixels paints a whole card: frame, back, and its tiles with a 1px gap.
func (m *Model) cardPixels(c PublicCard, i int) grid {
	n := cardPixelSize(c.Size, i)
	h := n + n%2 // half-blocks need an even number of pixel rows
	fr := cardFrames[c.Kind]
	if fr[0] == "" {
		fr = cardFrames[""]
	}
	g := make(grid, h)
	for y := range g {
		g[y] = make([]rgb, n)
	}
	fill(g, 0, 0, n, h, hexColor(fr[1]))
	frame := hexColor(fr[0])
	for k := 0; k < 2; k++ {
		for x := 0; x < n; x++ {
			g[k][x], g[n-1-k][x] = frame, frame
		}
		for y := 0; y < n; y++ {
			g[y][k], g[y][n-1-k] = frame, frame
		}
	}
	inner := hexColor("#10151b")
	for x := 2; x < n-2; x++ {
		g[2][x], g[n-3][x] = inner, inner
	}
	for y := 2; y < n-2; y++ {
		g[y][2], g[y][n-3] = inner, inner
	}
	w := tileSize(i)
	for idx := 0; idx < c.Size*c.Size; idx++ {
		x0 := cardFrame + (idx%c.Size)*(w+tileGap)
		y0 := cardFrame + (idx/c.Size)*(w+tileGap)
		t := m.tile(c, idx, i)
		for y := range t {
			copy(g[y0+y][x0:x0+w], t[y])
		}
	}
	return g
}

// spriteGrid draws a whole card with i-pixel icons.
func (m *Model) spriteGrid(c PublicCard, i int) string {
	return strings.Join(halfBlocks(m.cardPixels(c, i)), "\n")
}

// halfBlocks turns a pixel grid (even height) into text rows. In a true-colour
// terminal it writes the escape sequences itself (much faster than styling
// every run); otherwise lipgloss picks the nearest colours the terminal has.
func halfBlocks(px grid) []string {
	direct := lipgloss.ColorProfile() == termenv.TrueColor
	var rows []string
	for y := 0; y+1 < len(px); y += 2 {
		var b strings.Builder
		for x := 0; x < len(px[y]); {
			top, bot := px[y][x], px[y+1][x]
			run := 1
			for x+run < len(px[y]) && px[y][x+run] == top && px[y+1][x+run] == bot {
				run++
			}
			if direct {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm%s\x1b[0m", top.r, top.g, top.b, bot.r, bot.g, bot.b, strings.Repeat("▀", run))
			} else {
				st := lipgloss.NewStyle().Foreground(lipgloss.Color(top.hex())).Background(lipgloss.Color(bot.hex()))
				b.WriteString(st.Render(strings.Repeat("▀", run)))
			}
			x += run
		}
		rows = append(rows, b.String())
	}
	return rows
}

// spriteSizes are the icon sizes in pixels, biggest first. 16 is Minecraft's
// own texture size, drawn without any resampling.
var spriteSizes = []int{16, 8, 6, 4}

// pickSprite chooses the biggest icon whose whole card fits beside the side
// panel, or 0 for the plain symbol grid. width/height are the terminal's size
// in cells (0 = unknown).
func pickSprite(width, height, cardSize int) int {
	const sidePanel = 50 // my panel plus borders
	const chrome = 18    // header, titles, info lines, a short feed, hints
	if height == 0 {
		height = 60 // not told yet: assume a roomy terminal
	}
	for _, i := range spriteSizes {
		n := cardPixelSize(cardSize, i)
		if width >= n+4+sidePanel && height >= (n+1)/2+chrome {
			return i
		}
	}
	return 0
}
