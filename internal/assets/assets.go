// Package assets fetches Minecraft's own textures for the terminal game, the
// way kddb does (docs/MINECRAFT_ASSETS.md there): they are Mojang's, so they are
// not in this repository and not in hb's releases. On the player's request
// `hb assets fetch` downloads the pinned official client JAR, checks its SHA-1,
// pulls out only the listed files, checks each one's SHA-256, and deletes the
// JAR. Pure Go: no ffmpeg or ImageMagick, so it is the same on Windows, macOS
// and Linux.
package assets

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// File is one texture: where it is kept under the cache dir, where it is in the
// JAR, and what it must hash to.
type File struct {
	Path, Source string
	Bytes        int64
	SHA256       string
	// Only for files from the Minecraft Wiki (WikiFiles): the file page title,
	// the download URL, and the SHA-1 the Wiki's API reports for that version.
	Title, URL, SHA1 string
}

// AllFiles is every file hb can use: the JAR's textures and the Wiki's icons.
func AllFiles() []File {
	return append(append([]File(nil), Files...), WikiFiles...)
}

// JarSource pins one client JAR.
type JarSource struct {
	URL, SHA1 string
	Bytes     int64
}

// DefaultJar is Mojang's official 1.21.4 client.
var DefaultJar = JarSource{ClientURL, ClientSHA1, ClientBytes}

// Dir is where the textures live: the user's cache dir (AppData\Local on
// Windows, ~/Library/Caches on macOS, ~/.cache on Linux). $HB_ASSETS overrides.
func Dir() (string, error) {
	if d := os.Getenv("HB_ASSETS"); d != "" {
		return d, nil
	}
	c, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "hb", "minecraft", Version), nil
}

func sum256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Check returns the files that are missing or whose bytes are not the pinned ones.
func Check(dir string, files []File) []File {
	var bad []File
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || int64(len(b)) != f.Bytes || sum256(b) != f.SHA256 {
			bad = append(bad, f)
		}
	}
	return bad
}

// Fetch downloads Mojang's pinned client and extracts Files into dir.
func Fetch(ctx context.Context, dir string, log func(string)) error {
	if err := FetchFrom(ctx, http.DefaultClient, DefaultJar, Files, dir, log); err != nil {
		return err
	}
	return FetchWiki(ctx, http.DefaultClient, WikiAPI, WikiFiles, dir, log)
}

// FetchFrom is Fetch with the JAR and file list given (tests use a small JAR).
// Already-correct files are left alone; if everything is present it downloads nothing.
func FetchFrom(ctx context.Context, hc *http.Client, jar JarSource, files []File, dir string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	missing := Check(dir, files)
	if len(missing) == 0 {
		log("素材已齐，不用下载。")
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "client-*.jar")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // the JAR is only needed for this one extraction
	defer tmp.Close()

	log(fmt.Sprintf("下载 Mojang 官方客户端 JAR（%.1f MB，只为取出 %d 个文件，取完就删）…", float64(jar.Bytes)/1e6, len(missing)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jar.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hb-assets/1.0")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: %s 返回 HTTP %d", jar.URL, resp.StatusCode)
	}
	h := sha1.New()
	var got int64
	last := time.Now()
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			got += int64(n)
			h.Write(buf[:n])
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				return werr
			}
			if time.Since(last) > 2*time.Second {
				log(fmt.Sprintf("  已下载 %.1f / %.1f MB", float64(got)/1e6, float64(jar.Bytes)/1e6))
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("下载中断: %w", rerr)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if got != jar.Bytes {
		return fmt.Errorf("JAR 大小对不上：拿到 %d 字节，清单记的是 %d", got, jar.Bytes)
	}
	if s := hex.EncodeToString(h.Sum(nil)); s != jar.SHA1 {
		return fmt.Errorf("JAR 的 SHA-1 对不上：拿到 %s，清单记的是 %s（不会使用）", s, jar.SHA1)
	}

	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return fmt.Errorf("读 JAR 失败: %w", err)
	}
	defer zr.Close()
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}
	for _, f := range missing {
		e, ok := entries[f.Source]
		if !ok {
			return fmt.Errorf("JAR 里没有 %s", f.Source)
		}
		rc, err := e.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
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
	log(fmt.Sprintf("完成：取出并核对了 %d 个文件，已删除 JAR。", len(missing)))
	return nil
}

// Remove deletes the cached textures.
func Remove(dir string) error { return os.RemoveAll(dir) }

// Pack is the decoded textures, keyed by Path.
type Pack struct{ img map[string]*image.NRGBA }

// Load decodes every listed file that is present and still matches its hash.
// A missing or altered file is simply absent from the pack (callers fall back
// to built-in art); the error is only for an unreadable dir.
func Load(dir string, files []File) (*Pack, error) {
	p := &Pack{img: map[string]*image.NRGBA{}}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
		return nil, err
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || int64(len(b)) != f.Bytes || sum256(b) != f.SHA256 {
			continue
		}
		src, err := decodeImage(f.Path, b)
		if err != nil {
			continue
		}
		dst := image.NewNRGBA(src.Bounds())
		draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
		p.img[f.Path] = dst
	}
	return p, nil
}

// NewPack builds a pack from decoded textures (tests, and callers with their own).
func NewPack(img map[string]*image.NRGBA) *Pack { return &Pack{img: img} }

// Image returns a texture by Path, or nil.
func (p *Pack) Image(path string) *image.NRGBA {
	if p == nil {
		return nil
	}
	return p.img[path]
}

// Len is how many textures are loaded.
func (p *Pack) Len() int {
	if p == nil {
		return 0
	}
	return len(p.img)
}
