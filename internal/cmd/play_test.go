package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hb/internal/auth"
	"hb/internal/httpx"
	"hb/internal/provider/lafish"
)

func act(page string) lafish.Activity {
	a := lafish.Activity{ID: "a1", Title: "t"}
	if page != "" {
		a.Page = json.RawMessage(`{"url":"` + page + `"}`)
	}
	return a
}

func TestDetectGame(t *testing.T) {
	cases := []struct {
		page, site, want string
	}{
		{"", "", kindNone},
		{"https://hbweb-kddb.lafi.sh/v3/", "", kindKddb},
		{"https://HBWEB-KDDB.lafi.sh/v3/", "", kindKddb},
		{"https://game.example/play/", "", kindWeb},
		{"https://my-kddb.example/v3/", "https://my-kddb.example", kindKddb}, // explicit --site
		{"", "https://my-kddb.example", kindNone},                            // no page: nothing to play
	}
	for _, c := range cases {
		if got := detectGame(act(c.page), c.site); got != c.want {
			t.Errorf("page=%q site=%q: got %s want %s", c.page, c.site, got, c.want)
		}
	}
}

func TestResolvePacketRefAcceptsTheLinkPeopleGet(t *testing.T) {
	const link = "https://hb.lafi.sh/%F0%9F%A7%A7/%F0%9F%A7%B4%F0%9F%A5%BB%F0%9F%8C%80%F0%9F%A4%B4%F0%9F%94%A8%F0%9F%8D%B1%F0%9F%92%BD%F0%9F%A7%93%F0%9F%92%83%F0%9F%A5%AD%F0%9F%92%B3%F0%9F%91%86%F0%9F%8C%9A%E2%98%95"
	id, err := resolvePacketRef(link)
	if err != nil || len(id) != 36 {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if again, err := resolvePacketRef("🧴🥻🌀🤴🔨🍱💽🧓💃🥭💳👆🌚☕"); err != nil || again != id {
		t.Fatalf("bare code: %q %v", again, err)
	}
	if _, err := resolvePacketRef("not a packet"); err == nil || !strings.Contains(err.Error(), "红包链接") {
		t.Fatalf("a bad ref should say what is accepted: %v", err)
	}
}

func TestGameSiteRefusesOtherPages(t *testing.T) {
	if _, err := gameSite(act("https://game.example/play/"), ""); err == nil || !strings.Contains(err.Error(), "--site") {
		t.Fatalf("should point at --site, got %v", err)
	}
	if o, err := gameSite(act("https://hbweb-kddb.lafi.sh/v3/"), ""); err != nil || o != "https://hbweb-kddb.lafi.sh" {
		t.Fatalf("o=%q err=%v", o, err)
	}
	if o, err := gameSite(act(""), "https://x.example/anything"); err != nil || o != "https://x.example" {
		t.Fatalf("o=%q err=%v", o, err)
	}
}

func fakeLafish(t *testing.T, claimStatus int, claimBody string, claims *[]map[string]any) lafishSession {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/claim") {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			*claims = append(*claims, b)
			w.WriteHeader(claimStatus)
			w.Write([]byte(claimBody))
		}
	}))
	t.Cleanup(srv.Close)
	return lafishSession{
		lf: &lafish.Lafish{API: srv.URL + "/api", HTTP: httpx.New()},
		s:  auth.Session{CSRF: "c", Cookies: []auth.Cookie{{Name: "hb_session", Value: "v"}}},
	}
}

func TestPlayTextShowsPublicInfoWithoutSubmitting(t *testing.T) {
	var claims []map[string]any
	ls := fakeLafish(t, 200, `{}`, &claims)
	a := act("")
	a.Status = "active"
	a.HumanCheck = true
	a.HumanCheckMethod = "cap"
	a.Judge = map[string]any{"prompt": "猜一个数", "judged": float64(3), "unclaimed": "100", "fingerprint": "abc"}

	cmd := newPlayCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := playText(cmd, context.Background(), ls, a, "a1", ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"t  [active]", "猜一个数", "已判定 3 次", "奖池还剩 100", "代码指纹：abc", "人机验证"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if len(claims) != 0 {
		t.Fatal("no --input: nothing may be submitted")
	}
}

func TestPlayTextSubmitsExactlyOnce(t *testing.T) {
	var claims []map[string]any
	ls := fakeLafish(t, 200, `{"result":"judged","message":"大了"}`, &claims)
	cmd := newPlayCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := playText(cmd, context.Background(), ls, act(""), "a1", "50"); err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0]["input"] != "50" || claims[0]["participationMode"] != "code" {
		t.Fatalf("claims=%v", claims)
	}
	if !strings.Contains(buf.String(), "大了") {
		t.Fatalf("answer not shown:\n%s", buf.String())
	}
}

func TestPlayTextHumanCheckIsNotBypassed(t *testing.T) {
	var claims []map[string]any
	ls := fakeLafish(t, 403, `{"error":{"code":"HUMAN_CHECK_REQUIRED","message":"x"}}`, &claims)
	cmd := newPlayCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := playText(cmd, context.Background(), ls, act(""), "a1", "50")
	if err == nil || !strings.Contains(err.Error(), "不替你过") {
		t.Fatalf("err=%v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("must not retry: %d claims", len(claims))
	}
}
