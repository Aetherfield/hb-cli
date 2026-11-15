package assets

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WikiAPI is the Minecraft Wiki's MediaWiki API.
const WikiAPI = "https://minecraft.wiki/api.php"

// decodeImage reads a PNG, or the first frame of a GIF (the enchanted golden
// apple's inventory icon is an animation; hb draws it still).
func decodeImage(path string, b []byte) (image.Image, error) {
	if strings.HasSuffix(strings.ToLower(path), ".gif") {
		g, err := gif.DecodeAll(bytes.NewReader(b))
		if err != nil || len(g.Image) == 0 {
			return nil, fmt.Errorf("bad gif: %v", err)
		}
		// the first frame, composited on a transparent canvas the size of the GIF
		canvas := image.NewNRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
		first := g.Image[0]
		for y := first.Bounds().Min.Y; y < first.Bounds().Max.Y; y++ {
			for x := first.Bounds().Min.X; x < first.Bounds().Max.X; x++ {
				canvas.Set(x, y, first.At(x, y))
			}
		}
		return canvas, nil
	}
	return png.Decode(bytes.NewReader(b))
}

// uncached makes a URL the Wiki's CDN has not seen: its cache holds recompressed
// copies (same pixels, different bytes), and only the original matches the
// recorded hashes. kddb does the same.
func uncached(u string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%shb=%d%d", u, sep, time.Now().UnixNano(), rand.Uint32())
}

func get(ctx context.Context, hc *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hb-assets/1.0 (terminal client for a Minecraft-style game; fetches a few inventory icons)")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func sha1hex(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:])
}

// fetchOne gets the version of a Wiki file recorded in the list: the current
// one if its SHA-1 matches, else the older version with that SHA-1 from the
// file's history (the Wiki's images get replaced).
func fetchOne(ctx context.Context, hc *http.Client, api string, f File) ([]byte, error) {
	b, err := get(ctx, hc, uncached(f.URL))
	if err == nil && sha1hex(b) == f.SHA1 {
		return b, nil
	}
	q := url.Values{"action": {"query"}, "format": {"json"}, "prop": {"imageinfo"}, "iiprop": {"sha1|url"}, "iilimit": {"max"}, "titles": {f.Title}}
	body, aerr := get(ctx, hc, api+"?"+q.Encode())
	if aerr != nil {
		return nil, fmt.Errorf("%s: 当前版本对不上清单，查文件历史也失败了: %w", f.Path, aerr)
	}
	var res struct {
		Query struct {
			Pages map[string]struct {
				ImageInfo []struct {
					SHA1 string `json:"sha1"`
					URL  string `json:"url"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("%s: Wiki API 的回复读不懂: %w", f.Path, err)
	}
	for _, p := range res.Query.Pages {
		for _, info := range p.ImageInfo {
			if info.SHA1 != f.SHA1 {
				continue
			}
			old, err := get(ctx, hc, uncached(info.URL))
			if err != nil {
				return nil, err
			}
			if sha1hex(old) != f.SHA1 {
				return nil, fmt.Errorf("%s: 历史版本的 SHA-1 也对不上", f.Path)
			}
			return old, nil
		}
	}
	return nil, fmt.Errorf("%s: Wiki 上找不到清单记的那一版（SHA-1 %s）", f.Path, f.SHA1)
}

// FetchWiki downloads the listed Minecraft Wiki icons into dir, checking each
// one's SHA-1 and SHA-256. Files that are already right are left alone.
func FetchWiki(ctx context.Context, hc *http.Client, api string, files []File, dir string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	missing := Check(dir, files)
	if len(missing) == 0 {
		return nil
	}
	log(fmt.Sprintf("从 Minecraft Wiki 取 %d 张背包图标（共 %.0f KB）…", len(missing), float64(totalBytes(missing))/1024))
	for _, f := range missing {
		b, err := fetchOne(ctx, hc, api, f)
		if err != nil {
			return err
		}
		if int64(len(b)) != f.Bytes || sum256(b) != f.SHA256 {
			return fmt.Errorf("%s 的内容对不上清单（SHA-256 不一致），不使用", f.Path)
		}
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		part := dst + ".part"
		if err := os.WriteFile(part, b, 0o644); err != nil {
			return err
		}
		if err := os.Rename(part, dst); err != nil {
			return err
		}
	}
	log(fmt.Sprintf("完成：取了 %d 张并核对。", len(missing)))
	return nil
}

func totalBytes(fs []File) int64 {
	var n int64
	for _, f := range fs {
		n += f.Bytes
	}
	return n
}
