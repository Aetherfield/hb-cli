// Package browser finds the browsers installed on this machine and gets a
// login session out of them. Chromium-family browsers (Chrome, Edge, Brave,
// ...) are launched with a dedicated profile and driven over the DevTools
// protocol (CDP), started as a normal process (no --enable-automation) and
// attached to afterwards, so sites see an ordinary browser. Gecko-family
// browsers (Firefox, ...) have no CDP, so their cookies are read from the
// profile instead (gecko.go).
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"

	"hb/internal/auth"
)

// Browser is a running, attached browser.
type Browser struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	endpoint string          // http://127.0.0.1:<port>/
	Ctx      context.Context // chromedp tab context
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Launch starts the browser on profileDir (created if missing) and attaches.
// profileDir must not be the browser's default user-data dir: recent Chrome
// versions ignore the debugging port there.
func Launch(ctx context.Context, exe, profileDir, startURL string) (*Browser, error) {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	// Chrome refuses to start its sandbox as root (typical in containers and
	// some Linux setups); the player asked for this browser, so allow it there.
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	if startURL == "" {
		startURL = "about:blank"
	}
	// The start URL is the browser's only tab; we attach to it below instead
	// of opening a second one.
	cmd := exec.Command(exe, append(args, startURL)...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start browser: %w", err)
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if err := waitEndpoint(ctx, endpoint); err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	tabID, err := firstPageTarget(ctx, endpoint)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	// remote.NewAllocator attaches to the running browser (finding the
	// websocket URL itself from /json/version).
	allocCtx, cancelAlloc := remote.NewAllocator(ctx, endpoint)
	tabCtx, cancelTab := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(tabID)))
	b := &Browser{cmd: cmd, Ctx: tabCtx, endpoint: endpoint}
	b.cancel = func() { cancelTab(); cancelAlloc() }
	// Initialise the tab target once with no timeout; later calls derive a
	// timeout from this context so a closed browser can't hang them.
	if _, err := chromedp.Run(tabCtx, chromedp.Evaluate[int]("1")); err != nil {
		b.Close()
		return nil, fmt.Errorf("attach to browser tab: %w", err)
	}
	return b, nil
}

const callTimeout = 8 * time.Second

// firstPageTarget returns the id of the browser's first page tab.
func firstPageTarget(ctx context.Context, endpoint string) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(endpoint + "json/list")
		if err == nil {
			var targets []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			err = json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			if err == nil {
				for _, t := range targets {
					if t.Type == "page" {
						return t.ID, nil
					}
				}
			}
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", errors.New("browser opened no page tab")
}

// Alive reports whether the browser's debugging endpoint still answers.
// It is false once the user has closed the browser.
func (b *Browser) Alive() bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(b.endpoint + "json/version")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// waitEndpoint polls /json/version until the debugging endpoint answers.
func waitEndpoint(ctx context.Context, endpoint string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(endpoint + "json/version")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New("browser did not expose a debugging endpoint in 20s")
}

// Close detaches and stops the browser we started.
func (b *Browser) Close() {
	if b.cancel != nil {
		b.cancel()
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
	}
}

// EvalBool evaluates a JS expression (may be a promise) and returns whether
// it is truthy. Errors (e.g. page still loading) yield false.
func (b *Browser) EvalBool(js string) bool {
	ctx, cancel := context.WithTimeout(b.Ctx, callTimeout)
	defer cancel()
	v, err := chromedp.Run(ctx, chromedp.Evaluate[bool](js, chromedp.EvalAwaitPromise))
	return err == nil && v
}

// EvalString evaluates a JS expression (may be a promise) to a string.
func (b *Browser) EvalString(js string) (string, error) {
	ctx, cancel := context.WithTimeout(b.Ctx, callTimeout)
	defer cancel()
	return chromedp.Run(ctx, chromedp.Evaluate[string](js, chromedp.EvalAwaitPromise))
}

// Cookies returns the cookies the browser would send to the given URLs,
// including HttpOnly ones, filtered to names (all if names is empty).
func (b *Browser) Cookies(urls []string, names []string) ([]auth.Cookie, error) {
	cctx, cancel := context.WithTimeout(b.Ctx, callTimeout)
	defer cancel()
	res, err := chromedp.Run(cctx, func(ctx context.Context, t *chromedp.Target) (network.GetCookiesResult, error) {
		return cdp.Call(ctx, t, network.GetCookies, network.GetCookiesParams{URLs: urls})
	})
	if err != nil {
		return nil, err
	}
	raw := res.Cookies
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []auth.Cookie
	for _, c := range raw {
		if len(want) > 0 && !want[c.Name] {
			continue
		}
		out = append(out, auth.Cookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			Expires: c.Expires, HTTPOnly: c.HTTPOnly, Secure: c.Secure,
		})
	}
	return out, nil
}
