package kddb

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"hb/internal/assets"
)

func TestCardPixelSizeFollowsThePagesProportions(t *testing.T) {
	// frame 3 each side + tiles (icon+4 wide) + 1px gaps between them
	cases := []struct{ size, icon, want int }{
		{3, 16, 6 + 3*20 + 2}, {6, 16, 6 + 6*20 + 5}, {7, 8, 6 + 7*12 + 6}, {5, 4, 6 + 5*8 + 4},
	}
	for _, c := range cases {
		if got := cardPixelSize(c.size, c.icon); got != c.want {
			t.Errorf("cardPixelSize(%d,%d) = %d, want %d", c.size, c.icon, got, c.want)
		}
	}
}

func TestSpriteGridHasTheRightSize(t *testing.T) {
	m := spriteModel(t, nil, Options{})
	c := m.st.Game.Cards[0] // 3x3
	for _, icon := range []int{16, 8, 6, 4} {
		n := cardPixelSize(c.Size, icon)
		lines := strings.Split(m.spriteGrid(c, icon), "\n")
		if len(lines) != (n+1)/2 {
			t.Fatalf("icon %d: %d rows, want %d", icon, len(lines), (n+1)/2)
		}
		for i, ln := range lines {
			if w := lipgloss.Width(ln); w != n {
				t.Fatalf("icon %d row %d is %d columns wide, want %d", icon, i, w, n)
			}
		}
	}
}

func TestPickSpriteUsesTheBiggestIconWhoseWholeCardFits(t *testing.T) {
	// width needs cardPixelSize+54, height cardPixelSize/2+18
	// 7x7: 16px=152, 8px=96, 6px=82, 4px=68. 5x5: 110, 70, 60, 50.
	cases := []struct{ w, h, size, want int }{
		{260, 100, 7, 16},
		{200, 100, 7, 8}, // 16px needs 206 across
		{260, 80, 7, 8},  // ...and 94 rows
		{140, 70, 7, 6},  // 8px needs 150 across
		{125, 60, 7, 4},
		{120, 60, 7, 0}, // not even 4px (122 across)
		{300, 40, 7, 0}, // plenty wide but too short
		{300, 0, 5, 8},  // height unknown is assumed roomy (60 rows): 16px would need 73
		{115, 50, 5, 6},
		{105, 44, 5, 4},
		{170, 80, 5, 16}, // a smaller card gets 16px sooner
	}
	for _, c := range cases {
		if got := pickSprite(c.w, c.h, c.size); got != c.want {
			t.Errorf("pickSprite(%d,%d,%d) = %d, want %d", c.w, c.h, c.size, got, c.want)
		}
	}
}

func TestUnopenedCellsUseTheBiomeGroundWithTheFaintTint(t *testing.T) {
	pack := assets.NewPack(map[string]*image.NRGBA{
		"textures/block/stone.png":      solid(16, 16, color.NRGBA{100, 100, 100, 255}),
		"textures/block/netherrack.png": solid(16, 16, color.NRGBA{200, 50, 50, 255}),
	})
	m := spriteModel(t, pack, Options{})
	plain, nether := m.ground("", 20), m.ground("nether", 20)
	if plain == nil || nether == nil || plain[5][5] == nether[5][5] {
		t.Fatalf("each biome has its own ground: %v vs %v", plain[5][5], nether[5][5])
	}
	// the page's tint is faint: the ground stays close to the texture, not blacked out
	if d := int(plain[5][5].r) - 100; d < -30 || d > 30 {
		t.Fatalf("the tint should be faint, got %+v", plain[5][5])
	}
	if m.ground("deep", 20) != nil { // deepslate is not in this pack: speckle fallback
		t.Fatal("a texture that is not in the pack gives nil")
	}
}

func TestDugTileLooksLikeThePagesTile(t *testing.T) {
	st := baseState()
	c := card(1, 0, 0, 3)
	c.Cells[0] = &OpenCell{Kind: "tnt", UID: 9, Name: "bob"}   // someone else's
	c.Cells[1] = &OpenCell{Kind: "tnt", UID: 7, Name: "me"}    // mine (the fake game says I am 7)
	c.Cells[2] = &OpenCell{Kind: "emerald", UID: 9, Name: "x"} // a kind with no special colours
	st.Game.Cards = []PublicCard{c}
	m := newModel(t, &fakeGame{}, nil, Options{}, st)
	m.cur = 8 // keep the cursor off these tiles
	const icon = 8
	w := tileSize(icon)

	other := m.tile(c, 0, icon)
	if other[0][4] != hexColor(tileBorders["tnt"]) || other[3][0] != hexColor(tileBorders["tnt"]) {
		t.Errorf("top and left edges use the kind's border colour: %+v", other[0][4])
	}
	for y := w - 3; y < w; y++ { // the 3px dark base
		if other[y][4] != hexColor(openedBase) {
			t.Errorf("row %d should be the dark base, got %+v", y, other[y][4])
		}
	}
	if other[w/2][w/2-1] == (rgb{}) || other[4][1] != hexColor(tileColors["tnt"]) {
		t.Errorf("the tile is filled with the kind's colour: %+v", other[4][1])
	}

	mine := m.tile(c, 1, icon)
	if mine[0][4] != hexColor(mineBorder) {
		t.Errorf("a cell you dug has a gold edge, got %+v", mine[0][4])
	}

	plain := m.tile(c, 2, icon)
	if plain[0][4] != hexColor(openedBorder) || plain[4][1] != hexColor(openedFill) {
		t.Errorf("default colours: border %+v fill %+v", plain[0][4], plain[4][1])
	}
}

func TestCursorAndTorchAreDrawnOnTheTile(t *testing.T) {
	st := baseState()
	c := card(1, 0, 0, 3)
	c.Torches = []Torch{{At: 0, Level: 3, V: 2}}
	st.Game.Cards = []PublicCard{c}
	m := newModel(t, &fakeGame{}, nil, Options{}, st)
	m.cur = 0
	g := m.tile(c, 0, 16)
	w := tileSize(16)
	white := rgb{255, 255, 255, true}
	if g[0][0] != white || g[w-1][w-1] != white || g[1][1] != white {
		t.Errorf("the cursor ring is 2px on 16px icons: %+v %+v %+v", g[0][0], g[w-1][w-1], g[1][1])
	}
	m.cur = 4
	g = m.tile(c, 0, 16)
	if g[0][0] == white {
		t.Error("no ring on a tile the cursor is not on")
	}
	if want := hexColor(torchFlame2[3]); g[2][w-3] != want {
		t.Errorf("a level-3 torch is red in the top-right corner: want %+v got %+v", want, g[2][w-3])
	}
}

func TestCardFrameAndGapsUseTheBiomeColours(t *testing.T) {
	m := spriteModel(t, nil, Options{})
	for _, biome := range []string{"", "nether", "deep", "end"} {
		c := card(1, 0, 0, 3)
		c.Kind = biome
		g := m.cardPixels(c, 8)
		fr := cardFrames[biome]
		if g[0][0] != hexColor(fr[0]) || g[1][g.width()/2] != hexColor(fr[0]) {
			t.Errorf("%q: the frame is %s, got %+v", biome, fr[0], g[0][0])
		}
		// the gap between the first two tiles shows the card's back colour
		gapX := cardFrame + tileSize(8)
		if g[cardFrame+4][gapX] != hexColor(fr[1]) {
			t.Errorf("%q: the 1px gap should be the back colour %s, got %+v", biome, fr[1], g[cardFrame+4][gapX])
		}
	}
}

func (g grid) width() int { return len(g[0]) }

func TestCardHasAnEvenNumberOfPixelRows(t *testing.T) {
	m := spriteModel(t, nil, Options{})
	for _, size := range []int{3, 5, 6, 7} {
		for _, icon := range []int{16, 8, 6, 4} {
			c := card(1, 0, 0, size)
			g := m.cardPixels(c, icon)
			if len(g)%2 != 0 {
				t.Errorf("size %d icon %d: %d rows is odd; half-blocks need pairs", size, icon, len(g))
			}
		}
	}
}

func TestInventoryIconsArePreferredWhenFetched(t *testing.T) {
	green := solid(32, 32, color.NRGBA{0, 200, 0, 255}) // the Wiki's 32x32 render of the TNT block
	red := solid(16, 16, color.NRGBA{200, 0, 0, 255})   // the client's flat side texture
	both := assets.NewPack(map[string]*image.NRGBA{
		"inventory/tnt.png": green, "textures/block/tnt_side.png": red,
	})
	m := spriteModel(t, both, Options{})
	if g := m.masterOf("tnt"); len(g) != 32 || g[0][0].g != 200 {
		t.Fatalf("with both, the inventory render wins: %dx, %+v", len(g), g[0][0])
	}
	flat := spriteModel(t, assets.NewPack(map[string]*image.NRGBA{"textures/block/tnt_side.png": red}), Options{})
	if g := flat.masterOf("tnt"); len(g) != 16 || g[0][0].r != 200 {
		t.Fatalf("without it, the flat texture: %dx, %+v", len(g), g[0][0])
	}
	// and it scales down to the icon size like any other
	if icon := m.icon("tnt", 16); len(icon) != 16 || icon[0][0].g != 200 {
		t.Fatalf("scaled icon: %d, %+v", len(icon), icon[0][0])
	}
	// the map's inventory icon is 16x16, not 32x32
	m16 := spriteModel(t, assets.NewPack(map[string]*image.NRGBA{"inventory/map.png": solid(16, 16, color.NRGBA{9, 9, 9, 255})}), Options{})
	if g := m16.masterOf("map"); len(g) != 16 {
		t.Fatalf("map icon is %dx", len(g))
	}
}

func TestDoublePickaxeLooksLikeThePages(t *testing.T) {
	src := fromImage(solid(16, 16, color.NRGBA{200, 200, 200, 255}), 16)
	g := doublePick(src)
	if len(g) != 16 {
		t.Fatalf("size %d", len(g))
	}
	// 12px pickaxes in opposite corners; the middle of the top-left and bottom-right is empty only
	// where neither covers it
	if !g[15][0].on || !g[0][15].on {
		t.Fatalf("front (bottom-left) and back (top-right) pickaxes: %+v %+v", g[15][0], g[0][15])
	}
	front, back := g[10][4], g[1][14]
	if back.r >= front.r {
		t.Fatalf("the back pickaxe is dimmer: back %+v front %+v", back, front)
	}
}

func TestOutputIsPlainWidthBothWays(t *testing.T) {
	m := spriteModel(t, nil, Options{})
	c := m.st.Game.Cards[0]
	n := cardPixelSize(c.Size, 8)

	// quantised colours (lipgloss) and direct 24-bit escapes must draw the same cells
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	for _, p := range []termenv.Profile{termenv.ANSI256, termenv.TrueColor} {
		lipgloss.SetColorProfile(p)
		lines := strings.Split(m.spriteGrid(c, 8), "\n")
		for i, ln := range lines {
			if w := lipgloss.Width(ln); w != n {
				t.Fatalf("profile %v row %d: %d columns, want %d", p, i, w, n)
			}
		}
		out := strings.Join(lines, "")
		hasTrue := strings.Contains(out, "\x1b[38;2;")
		if (p == termenv.TrueColor) != hasTrue {
			t.Fatalf("profile %v: 24-bit escapes present = %v", p, hasTrue)
		}
	}
}
