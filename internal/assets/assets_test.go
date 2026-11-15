package assets

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func pngBytes(c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// fakeJar builds a tiny client "JAR" with two textures and an unrelated file.
func fakeJar(t *testing.T) (jar []byte, files []File) {
	t.Helper()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	add := func(name string, data []byte) {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	red, blue := pngBytes(color.NRGBA{255, 0, 0, 255}), pngBytes(color.NRGBA{0, 0, 255, 255})
	add("assets/minecraft/textures/item/red.png", red)
	add("assets/minecraft/textures/block/blue.png", blue)
	add("assets/minecraft/lang/en_us.json", []byte("{}"))
	zw.Close()
	files = []File{
		{Path: "textures/item/red.png", Source: "assets/minecraft/textures/item/red.png", Bytes: int64(len(red)), SHA256: sum256(red)},
		{Path: "textures/block/blue.png", Source: "assets/minecraft/textures/block/blue.png", Bytes: int64(len(blue)), SHA256: sum256(blue)},
	}
	return zb.Bytes(), files
}

func serve(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func src(srv *httptest.Server, body []byte) JarSource {
	h := sha1.Sum(body)
	return JarSource{URL: srv.URL + "/client.jar", SHA1: hex.EncodeToString(h[:]), Bytes: int64(len(body))}
}

func TestFetchExtractsAndVerifiesThenIsIdempotent(t *testing.T) {
	jar, files := fakeJar(t)
	srv, hits := serve(t, jar)
	dir := t.TempDir()

	var logs []string
	if err := FetchFrom(context.Background(), srv.Client(), src(srv, jar), files, dir, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	if bad := Check(dir, files); len(bad) != 0 {
		t.Fatalf("still bad: %v", bad)
	}
	// the JAR is not kept, and nothing but the wanted files is extracted
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jar") {
			t.Errorf("the downloaded JAR must be deleted, found %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "lang")); err == nil {
		t.Error("only the listed files may be extracted")
	}

	before := hits.Load()
	if err := FetchFrom(context.Background(), srv.Client(), src(srv, jar), files, dir, nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before {
		t.Fatal("everything present: must not download again")
	}
}

func TestFetchRefusesAJarWithTheWrongHash(t *testing.T) {
	jar, files := fakeJar(t)
	srv, _ := serve(t, jar)
	bad := src(srv, jar)
	bad.SHA1 = strings.Repeat("0", 40)
	dir := t.TempDir()
	err := FetchFrom(context.Background(), srv.Client(), bad, files, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-1") {
		t.Fatalf("err=%v", err)
	}
	if len(Check(dir, files)) != len(files) {
		t.Fatal("nothing may be extracted from an unverified JAR")
	}
}

func TestFetchRefusesAWrongSizeJar(t *testing.T) {
	jar, files := fakeJar(t)
	srv, _ := serve(t, jar)
	s := src(srv, jar)
	s.Bytes++
	if err := FetchFrom(context.Background(), srv.Client(), s, files, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "大小") {
		t.Fatalf("err=%v", err)
	}
}

func TestFetchRefusesAFileThatDoesNotMatchTheManifest(t *testing.T) {
	jar, files := fakeJar(t)
	files[0].SHA256 = strings.Repeat("a", 64) // manifest says something else
	srv, _ := serve(t, jar)
	dir := t.TempDir()
	err := FetchFrom(context.Background(), srv.Client(), src(srv, jar), files, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "textures/item/red.png")); statErr == nil {
		t.Fatal("a file that fails its hash must not be written")
	}
}

func TestFetchHandlesHTTPErrors(t *testing.T) {
	_, files := fakeJar(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	err := FetchFrom(context.Background(), srv.Client(), JarSource{URL: srv.URL, SHA1: "x", Bytes: 1}, files, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadDecodesAndSkipsAlteredFiles(t *testing.T) {
	jar, files := fakeJar(t)
	srv, _ := serve(t, jar)
	dir := t.TempDir()
	if err := FetchFrom(context.Background(), srv.Client(), src(srv, jar), files, dir, nil); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir, files)
	if err != nil || p.Len() != 2 {
		t.Fatalf("len=%d err=%v", p.Len(), err)
	}
	if c := p.Image("textures/item/red.png").NRGBAAt(0, 0); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Fatalf("pixel %v", c)
	}
	// tamper with one: it drops out of the pack instead of being drawn
	_ = os.WriteFile(filepath.Join(dir, "textures/item/red.png"), []byte("not a png"), 0o644)
	p, _ = Load(dir, files)
	if p.Len() != 1 || p.Image("textures/item/red.png") != nil {
		t.Fatalf("altered file must be ignored (len=%d)", p.Len())
	}
	// a missing dir is an empty pack, not an error
	if p, err := Load(filepath.Join(dir, "nope"), files); err != nil || p.Len() != 0 {
		t.Fatalf("missing dir: %v %v", p, err)
	}
	// a nil pack answers nil
	var nilPack *Pack
	if nilPack.Image("x") != nil || nilPack.Len() != 0 {
		t.Fatal("nil pack")
	}
}

func TestPinnedListIsSane(t *testing.T) {
	if len(Files) < 20 || len(ClientSHA1) != 40 || ClientBytes < 1e6 || !strings.HasPrefix(ClientURL, "https://piston-data.mojang.com/") {
		t.Fatalf("pinned data looks wrong: %d files, %q", len(Files), ClientURL)
	}
	seen := map[string]bool{}
	for _, f := range Files {
		if len(f.SHA256) != 64 || f.Bytes <= 0 || !strings.HasPrefix(f.Source, "assets/minecraft/") || seen[f.Path] {
			t.Errorf("bad entry %+v", f)
		}
		seen[f.Path] = true
	}
}
