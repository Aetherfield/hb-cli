package ext

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The extension lives in its own repository now. hb pins one release of it:
// --download-ext fetches exactly that zip and refuses it unless its SHA-256
// is the one below. A new extension release means changing both here.
const (
	BridgeRepo    = "https://github.com/Aetherfield/hb-session-bridge"
	BridgeVersion = "0.2.0"
	BridgeSHA256  = "6a4729a5f856f444b3a409ce13468c027594eb5f67f369b4c2dca7b439a84e7d"
)

// BridgeReleases is where to download it by hand.
const BridgeReleases = BridgeRepo + "/releases"

// BridgeZipURL is the pinned release's zip.
func BridgeZipURL() string {
	return fmt.Sprintf("%s/releases/download/v%s/hb-session-bridge-%s.zip", BridgeRepo, BridgeVersion, BridgeVersion)
}

const maxZip = 2 << 20 // the extension is about 10 KB; anything this big is not it

// Download fetches the pinned extension, checks its SHA-256, and unpacks it to
// <dir>/hb-session-bridge-<version>, which it returns. An existing copy of
// that version is replaced.
func Download(ctx context.Context, hc *http.Client, url, sum, dir string) (string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载扩展失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载扩展失败：HTTP %d（%s）", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxZip+1))
	if err != nil {
		return "", fmt.Errorf("下载扩展失败：%w", err)
	}
	if len(b) > maxZip {
		return "", errors.New("下载的扩展大得不正常，已放弃")
	}
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != sum {
		return "", fmt.Errorf("下载的扩展 SHA-256 对不上（期望 %s，得到 %s），已丢弃，没有解压", sum, hex.EncodeToString(got[:]))
	}
	dst := filepath.Join(dir, "hb-session-bridge-"+BridgeVersion)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := unzip(b, dst); err != nil {
		_ = os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}

// unzip writes regular files only, and only under dst.
func unzip(b []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("扩展的 zip 读不了：%w", err)
	}
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." || strings.HasSuffix(f.Name, "/") {
			continue // a directory entry
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, ":") {
			return fmt.Errorf("扩展的 zip 里有不安全的路径 %q，已放弃", f.Name)
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("扩展的 zip 里有不是普通文件的条目 %q，已放弃", f.Name)
		}
		out := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxZip+1))
		rc.Close()
		if err != nil {
			return err
		}
		if len(data) > maxZip {
			return errors.New("扩展里的文件大得不正常，已放弃")
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "manifest.json")); err != nil {
		return errors.New("扩展的 zip 里没有 manifest.json")
	}
	return nil
}
