package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeWiki serves one file at /images/x.png, its older versions under /old/,
// and a MediaWiki-style API that lists those versions.
type fakeWiki struct {
	srv     *httptest.Server
	current []byte // what /images/x.png serves now
	old     []byte // an earlier version, listed in the file history
	apiHits atomic.Int32
	queries []string
}

func newWiki(t *testing.T, current, old []byte) *fakeWiki {
	w := &fakeWiki{current: current, old: old}
	mux := http.NewServeMux()
	mux.HandleFunc("/images/x.png", func(rw http.ResponseWriter, r *http.Request) {
		w.queries = append(w.queries, r.URL.RawQuery)
		rw.Write(w.current)
	})
	mux.HandleFunc("/old/x.png", func(rw http.ResponseWriter, r *http.Request) { rw.Write(w.old) })
	mux.HandleFunc("/api.php", func(rw http.ResponseWriter, r *http.Request) {
		w.apiHits.Add(1)
		if r.URL.Query().Get("titles") != "File:X.png" || r.URL.Query().Get("prop") != "imageinfo" {
			http.Error(rw, "bad query", 400)
			return
		}
		type info struct {
			SHA1 string `json:"sha1"`
			URL  string `json:"url"`
		}
		infos := []info{{sha1hex(w.current), w.srv.URL + "/images/x.png"}}
		if w.old != nil {
			infos = append(infos, info{sha1hex(w.old), w.srv.URL + "/old/x.png"})
		}
		json.NewEncoder(rw).Encode(map[string]any{"query": map[string]any{"pages": map[string]any{"1": map[string]any{"imageinfo": infos}}}})
	})
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	return w
}

func wikiFile(w *fakeWiki, want []byte) File {
	return File{Path: "inventory/x.png", Bytes: int64(len(want)), SHA256: sum256(want), Title: "File:X.png",
		URL: w.srv.URL + "/images/x.png", SHA1: sha1hex(want)}
}

func TestWikiFetchTakesTheCurrentVersionWhenItMatches(t *testing.T) {
	good := pngBytes(color.NRGBA{9, 9, 9, 255})
	w := newWiki(t, good, nil)
	dir := t.TempDir()
	if err := FetchWiki(context.Background(), w.srv.Client(), w.srv.URL+"/api.php", []File{wikiFile(w, good)}, dir, nil); err != nil {
		t.Fatal(err)
	}
	if w.apiHits.Load() != 0 {
		t.Error("the history is only asked when the current version is wrong")
	}
	if len(Check(dir, []File{wikiFile(w, good)})) != 0 {
		t.Fatal("file not written correctly")
	}
	// the request must dodge the CDN's recompressed copies
	if len(w.queries) == 0 || !strings.Contains(w.queries[0], "hb=") {
		t.Errorf("the URL needs a cache-busting parameter, got %q", w.queries)
	}
	// already right: nothing is fetched again
	before := len(w.queries)
	_ = FetchWiki(context.Background(), w.srv.Client(), w.srv.URL+"/api.php", []File{wikiFile(w, good)}, dir, nil)
	if len(w.queries) != before {
		t.Error("present files must not be downloaded again")
	}
}

func TestWikiFetchFindsTheRecordedVersionInTheFileHistory(t *testing.T) {
	recorded := pngBytes(color.NRGBA{1, 2, 3, 255})
	replaced := pngBytes(color.NRGBA{200, 0, 0, 255}) // someone uploaded a new version
	w := newWiki(t, replaced, recorded)
	dir := t.TempDir()
	if err := FetchWiki(context.Background(), w.srv.Client(), w.srv.URL+"/api.php", []File{wikiFile(w, recorded)}, dir, nil); err != nil {
		t.Fatal(err)
	}
	if w.apiHits.Load() == 0 {
		t.Fatal("the history should have been consulted")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "inventory/x.png"))
	if sum256(got) != sum256(recorded) {
		t.Fatal("the recorded (older) version must be the one kept")
	}
}

func TestWikiFetchRefusesWhenTheRecordedVersionIsGone(t *testing.T) {
	recorded := pngBytes(color.NRGBA{1, 2, 3, 255})
	replaced := pngBytes(color.NRGBA{200, 0, 0, 255})
	w := newWiki(t, replaced, nil) // no history that matches
	dir := t.TempDir()
	err := FetchWiki(context.Background(), w.srv.Client(), w.srv.URL+"/api.php", []File{wikiFile(w, recorded)}, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "找不到") {
		t.Fatalf("err=%v", err)
	}
	if len(Check(dir, []File{wikiFile(w, recorded)})) == 0 {
		t.Fatal("an unverified file must not be kept")
	}
}

func TestWikiFetchChecksSHA256EvenWhenSHA1Matches(t *testing.T) {
	good := pngBytes(color.NRGBA{9, 9, 9, 255})
	w := newWiki(t, good, nil)
	f := wikiFile(w, good)
	f.SHA256 = strings.Repeat("0", 64)
	dir := t.TempDir()
	err := FetchWiki(context.Background(), w.srv.Client(), w.srv.URL+"/api.php", []File{f}, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "inventory/x.png")); statErr == nil {
		t.Fatal("a file failing SHA-256 must not be written")
	}
}

func TestGifIsReadAsItsFirstFrame(t *testing.T) {
	first := image.NewPaletted(image.Rect(0, 0, 4, 4), palette.Plan9)
	second := image.NewPaletted(image.Rect(0, 0, 4, 4), palette.Plan9)
	for i := range first.Pix {
		first.Pix[i] = 1 // palette index 1
		second.Pix[i] = 200
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{5, 5}}); err != nil {
		t.Fatal(err)
	}
	img, err := decodeImage("inventory/apple.gif", b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 4 {
		t.Fatalf("bounds %v", img.Bounds())
	}
	want := color.NRGBAModel.Convert(palette.Plan9[1])
	if got := color.NRGBAModel.Convert(img.At(0, 0)); got != want {
		t.Fatalf("pixel %v, want the first frame's %v", got, want)
	}
	if _, err := decodeImage("x.png", []byte("nope")); err == nil {
		t.Error("garbage is not a PNG")
	}
}

func TestListsAreSane(t *testing.T) {
	if len(WikiFiles) != 8 {
		t.Fatalf("%d wiki files", len(WikiFiles))
	}
	for _, f := range WikiFiles {
		if len(f.SHA1) != 40 || len(f.SHA256) != 64 || !strings.HasPrefix(f.URL, "https://minecraft.wiki/") || f.Title == "" || f.Bytes <= 0 {
			t.Errorf("bad wiki entry %+v", f)
		}
	}
	if len(AllFiles()) != len(Files)+len(WikiFiles) {
		t.Fatal("AllFiles")
	}
	for _, f := range AllFiles() {
		if strings.Contains(f.Path, "\\") {
			t.Errorf("paths use forward slashes: %s", f.Path)
		}
	}
	_ = fmt.Sprint
}
