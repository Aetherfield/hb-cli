package kddb

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"hb/internal/assets"
)

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func spriteModel(t *testing.T, pack *assets.Pack, opts Options) *Model {
	t.Helper()
	opts.Pack = pack
	return newModel(t, &fakeGame{}, nil, opts, baseState())
}

func TestEveryBuiltinSpriteIsWellFormed(t *testing.T) {
	for k, s := range spriteArt {
		for y, row := range s.art {
			if len(row) != 8 {
				t.Errorf("%s row %d has %d columns, want 8", k, y, len(row))
			}
			for i := 0; i < len(row); i++ {
				if row[i] == '.' {
					continue
				}
				if _, ok := s.pal[row[i]]; !ok {
					t.Errorf("%s row %d uses %q with no colour", k, y, row[i])
				}
			}
		}
		if g := builtin(k); len(g) != 8 {
			t.Errorf("%s: builtin grid is %d tall", k, len(g))
		}
	}
	if builtin("nope") != nil {
		t.Error("unknown kind has no art")
	}
}

func TestScaleKeepsSizeAndTransparency(t *testing.T) {
	src := fromImage(solid(16, 16, color.NRGBA{200, 10, 10, 255}), 16)
	for _, size := range []int{16, 8, 6, 4} {
		g := scale(src, size)
		if len(g) != size || len(g[0]) != size {
			t.Fatalf("scaled to %d got %dx%d", size, len(g), len(g[0]))
		}
		for y := range g {
			for x := range g[y] {
				if !g[y][x].on || g[y][x].r != 200 {
					t.Fatalf("size %d (%d,%d): %+v", size, x, y, g[y][x])
				}
			}
		}
	}
	// a half-transparent image: the transparent half stays transparent
	img := solid(16, 16, color.NRGBA{0, 200, 0, 255})
	for y := 8; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{})
		}
	}
	g := scale(fromImage(img, 16), 4)
	if !g[0][0].on || g[3][0].on {
		t.Fatalf("top should be opaque and bottom transparent: %+v / %+v", g[0][0], g[3][0])
	}
}

func TestTexturesComeFromThePackAndFallBackToBuiltinArt(t *testing.T) {
	pack := assets.NewPack(map[string]*image.NRGBA{
		"textures/item/emerald.png": solid(16, 16, color.NRGBA{1, 2, 3, 255}),
	})
	m := spriteModel(t, pack, Options{})
	if g := m.masterOf("emerald"); len(g) != 16 || g[0][0] != (rgb{1, 2, 3, true}) {
		t.Fatalf("emerald should be the client's texture: %v", g[0][0])
	}
	// diamond is not in this pack: hb's own 8x8 art
	if g := m.masterOf("diamond"); len(g) != 8 {
		t.Fatalf("diamond should fall back to builtin art, got %d", len(g))
	}
	// the shield has no stand-alone texture at all: builtin even with a full pack
	if g := m.masterOf("shield"); len(g) != 8 {
		t.Fatalf("shield is always builtin, got %d", len(g))
	}
	// no pack: everything builtin, and unknown kinds draw nothing
	m2 := spriteModel(t, nil, Options{})
	if g := m2.masterOf("emerald"); len(g) != 8 {
		t.Fatalf("no pack: builtin, got %d", len(g))
	}
	if m2.masterOf("mystery") != nil {
		t.Fatal("unknown kind")
	}
}

func TestLavaUsesTheFirstFrameOfTheStrip(t *testing.T) {
	strip := solid(16, 320, color.NRGBA{255, 0, 0, 255})
	for y := 16; y < 320; y++ { // frames after the first are blue
		for x := 0; x < 16; x++ {
			strip.SetNRGBA(x, y, color.NRGBA{0, 0, 255, 255})
		}
	}
	m := spriteModel(t, assets.NewPack(map[string]*image.NRGBA{"textures/block/lava_still.png": strip}), Options{})
	if g := m.masterOf("lava"); len(g) != 16 || g[15][15].r != 255 || g[15][15].b != 0 {
		t.Fatalf("lava must be the first frame only: %+v", g[15][15])
	}
}

func TestPotionIsTheTintedLiquidUnderTheBottle(t *testing.T) {
	bottle := solid(16, 16, color.NRGBA{})
	bottle.SetNRGBA(0, 0, color.NRGBA{9, 9, 9, 255})
	liquid := solid(16, 16, color.NRGBA{255, 255, 255, 255})
	m := spriteModel(t, assets.NewPack(map[string]*image.NRGBA{potionBottle: bottle, potionOverlay: liquid}), Options{})
	g := m.masterOf("potion")
	if g[0][0] != (rgb{9, 9, 9, true}) {
		t.Fatalf("the bottle is drawn over the liquid: %+v", g[0][0])
	}
	want := hexColor(hasteColor) // white liquid times the haste colour is the haste colour
	if g[5][5] != want {
		t.Fatalf("liquid should be tinted %v, got %v", want, g[5][5])
	}
}

func TestRenderModeAndZoomKey(t *testing.T) {
	// "auto" without true colour stays on symbols
	m := spriteModel(t, nil, Options{Render: "auto", TrueColor: false})
	m.width, m.height = 200, 80
	if got := m.spriteSize(m.current()); got != 0 {
		t.Fatalf("no true colour: symbols, got %d", got)
	}
	// "auto" with true colour and room uses icons
	m = spriteModel(t, nil, Options{Render: "auto", TrueColor: true})
	m.width, m.height = 200, 80
	if got := m.spriteSize(m.current()); got != 16 {
		t.Fatalf("true colour and plenty of room: 16px, got %d", got)
	}
	// "glyph" never uses icons; "sprite" always does (at least 4px)
	m = spriteModel(t, nil, Options{Render: "glyph", TrueColor: true})
	m.width, m.height = 200, 80
	if m.spriteSize(m.current()) != 0 {
		t.Fatal("glyph")
	}
	m = spriteModel(t, nil, Options{Render: "sprite"})
	m.width, m.height = 60, 20
	if m.spriteSize(m.current()) != 4 {
		t.Fatal("forced sprite on a tiny terminal still draws 4px icons")
	}

	// z cycles auto -> symbols -> 4 -> 6 -> 8 -> 16 -> auto
	m = spriteModel(t, nil, Options{Render: "auto", TrueColor: true})
	m.width, m.height = 200, 80
	var seen []int
	for i := 0; i < 6; i++ {
		run(t, m, key(m, "z"))
		seen = append(seen, m.zoom)
	}
	want := []int{-1, 4, 6, 8, 16, 0}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("zoom sequence %v, want %v", seen, want)
		}
	}
	m.zoom = 6
	if m.spriteSize(m.current()) != 6 {
		t.Fatal("a fixed zoom wins")
	}
}

func TestViewWithIconsStaysComplete(t *testing.T) {
	st := baseState()
	st.Game.Cards = []PublicCard{card(1, 0, 0, 5), card(2, 5, 0, 5)}
	st.Me.Items = &Items{Torch: 1}
	m := newModel(t, &fakeGame{}, nil, Options{Render: "sprite", TrueColor: true}, st)
	m.width, m.height = 200, 80 // room for 16px icons and the world map beside them
	v := m.View()
	for _, want := range []string{"▀", "卡 #1", "待领取", "道具栏", "动态", "矿图"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in the icon view", want)
		}
	}
	for _, ln := range strings.Split(v, "\n") {
		if w := lipgloss.Width(ln); w > m.width {
			t.Fatalf("a line is %d columns wide on a %d column terminal:\n%s", w, m.width, ln)
		}
	}
	m.width = 70 // narrow: still renders, stacked
	if !strings.Contains(m.View(), "待领取") {
		t.Fatal("narrow icon view lost the player panel")
	}
}
