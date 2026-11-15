package unsnow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hb/internal/auth"
)

const testToken = "hbr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"

func session() auth.Session { return auth.Session{Provider: "unsnow", Token: testToken} }

// server answers like the CLI API, and counts the requests it gets.
func server(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*Unsnow, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Cookie") != "" {
			t.Errorf("want the token alone, got Authorization %q Cookie %q", r.Header.Get("Authorization"), r.Header.Get("Cookie"))
		}
		if !strings.HasPrefix(r.URL.Path, "/api/cli/v1/") {
			t.Errorf("outside the CLI API: %s", r.URL.Path)
		}
		handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return &Unsnow{BaseURL: srv.URL, HTTP: newClient()}, &n
}

func jsonAnswer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func TestTokenShape(t *testing.T) {
	if !ValidToken(testToken) {
		t.Fatal("a well-formed token")
	}
	for _, bad := range []string{"", "hbr_short", "hbk_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG", testToken + "x", "hbr_0123456789abcdefghijklmnopqrstuvwxyzABCD!G"} {
		if ValidToken(bad) {
			t.Errorf("%q should not pass", bad)
		}
	}
}

func TestParseRef(t *testing.T) {
	id := "cd30e0f6a98e65dadd5b06f17578252d"
	text := RefTextCode
	code := RefCode
	cases := []struct {
		in    string
		force *RefKind
		want  Ref
		bad   bool
	}{
		{in: id, want: Ref{RefID, id}},
		{in: "https://hb.unsnow.org/hb/" + id, want: Ref{RefID, id}},
		{in: "https://hb.unsnow.org/hb/mvczoefk3278bf88", want: Ref{RefID, "mvczoefk3278bf88"}}, // an older id, by link only
		{in: "co78534294", want: Ref{RefCode, "CO78534294"}},                                    // upper-cased as the site does
		{in: "12345678", want: Ref{RefCode, "12345678"}},                                        // the old 8 digits
		{in: "12345678", force: &text, want: Ref{RefTextCode, "12345678"}},
		{in: "awaqwq", want: Ref{RefTextCode, "awaqwq"}},
		{in: "你好呀呀呀呀", want: Ref{RefTextCode, "你好呀呀呀呀"}},
		{in: "https://hb.unsnow.org/text-code/%E4%BD%A0%E5%A5%BD%E5%91%80%E5%91%80%E5%91%80%E5%91%80", want: Ref{RefTextCode, "你好呀呀呀呀"}},
		{in: "abc", bad: true},                   // too short to be a passcode
		{in: "has space", bad: true},             // not 汉字/letters/digits
		{in: "a!b@c#d$", bad: true},              // symbols
		{in: strings.Repeat("字", 33), bad: true}, // too long
		{in: "awaqwq", force: &code, bad: true},  // forced, and not a random passcode
		{in: "https://evil.example/hb/" + id, bad: true},
		{in: "https://hb.unsnow.org/plaza", bad: true},
		{in: "", bad: true},
	}
	for _, c := range cases {
		got, err := ParseRef(c.in, c.force)
		if (err != nil) != c.bad || (!c.bad && got != c.want) {
			t.Errorf("%q: got %+v err %v", c.in, got, err)
		}
	}
	if p := (Ref{RefTextCode, "a b"}).path(); p != "/hongbao/text-code/a%20b" {
		t.Error(p)
	}
}

func TestMeAndWhoami(t *testing.T) {
	u, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cli/v1/me" {
			http.NotFound(w, r)
			return
		}
		jsonAnswer(w, 200, `{"user":{"linuxdo_id":42,"username":"alice","name":null,"trust_level":2},"scope":"hongbao:read",
			"claim_security":{"verification_required":true,"consecutive_misses":10,"threshold":10,"server_now":1,"banned_until":0},
			"verification_url":"https://hb.unsnow.org/?cli_verify=1"}`)
	})
	me, err := u.GetMe(context.Background(), session())
	if err != nil || me.User.Username != "alice" || !me.ClaimSecurity.VerificationRequired || me.Scope != "hongbao:read" {
		t.Fatalf("%+v %v", me, err)
	}
	id, err := u.Whoami(context.Background(), session())
	if err != nil || id.ID != "42" {
		t.Fatalf("%+v %v", id, err)
	}
	// no token, no request
	if _, err := u.GetMe(context.Background(), auth.Session{Provider: "unsnow"}); err == nil || !strings.Contains(err.Error(), "hb login unsnow") {
		t.Fatalf("%v", err)
	}
}

func TestLookupPicksTheEndpointAndReadsThePacket(t *testing.T) {
	var paths []string
	u, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		jsonAnswer(w, 200, `{"id":"x","type":"equal","total_amount":1,"creator":{"linuxdo_id":1,"username":"254"},
			"available":true,"can_claim":false,"reason":"这是你发出的红包","participated":false,"status":"active",
			"created_at":1,"expires_at":2,"draw_at":null,"progress":{"kind":"shares","used":0,"total":1},
			"allowed_trust_levels":[0,1,2,3,4],"max_claims_24h":null,"link":"https://hb.unsnow.org/hb/x","server_now":1}`)
	})
	ctx := context.Background()
	for _, ref := range []Ref{{RefCode, "CO78534294"}, {RefTextCode, "你好呀呀呀呀"}, {RefID, "cd30e0f6a98e65dadd5b06f17578252d"}} {
		pk, err := u.Lookup(ctx, session(), ref)
		if err != nil || pk.CanClaim || pk.Reason == nil || *pk.Reason != "这是你发出的红包" || pk.Progress.Total != 1 {
			t.Fatalf("%+v %v", pk, err)
		}
	}
	want := []string{
		"/api/cli/v1/hongbao/code/CO78534294",
		"/api/cli/v1/hongbao/text-code/%E4%BD%A0%E5%A5%BD%E5%91%80%E5%91%80%E5%91%80%E5%91%80",
		"/api/cli/v1/hongbao/cd30e0f6a98e65dadd5b06f17578252d",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path %d: %s want %s", i, paths[i], want[i])
		}
	}
}

func TestRefusalsAreExplainedAndNeverRetried(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		ctype   string
		body    string
		header  map[string]string
		check   func(*APIError) bool
		message string
	}{
		{"captcha", 403, "application/json", `{"error":"需要验证","code":"lookup_captcha_required","lookup_counted":true,"verification_url":"https://hb.unsnow.org/?cli_verify=1"}`, nil,
			func(e *APIError) bool { return e.Code == "lookup_captcha_required" && e.LookupCounted }, "cli_verify=1"},
		{"rate limited", 429, "application/json", `{"error":"操作太频繁","code":"claim_rate_limited","retry_after":4}`, map[string]string{"Retry-After": "4"},
			func(e *APIError) bool { return e.RetryAfter == 4*time.Second }, "4s"},
		{"token revoked", 401, "application/json", `{"error":"令牌无效","code":"invalid_token"}`, nil,
			func(e *APIError) bool { return e.Status == 401 }, "hb login unsnow"},
		{"not found", 404, "application/json", `{"error":"口令不存在","code":"code_not_found","lookup_counted":true}`, nil,
			func(e *APIError) bool { return e.Code == "code_not_found" && e.LookupCounted }, "口令不存在"},
		{"expired", 410, "application/json", `{"error":"红包已过期","code":"expired"}`, nil,
			func(e *APIError) bool { return e.Status == 410 }, "红包已过期"},
		{"server error", 503, "application/json", `{"error":"服务暂不可用"}`, nil,
			func(e *APIError) bool { return e.Status == 503 }, "服务暂不可用"},
		{"html from the network layer", 403, "text/html", `<html>blocked</html>`, nil,
			func(e *APIError) bool { return e.NotJSON }, "不是接口的 JSON"},
		{"html with 200", 200, "text/html", `<html>challenge</html>`, nil,
			func(e *APIError) bool { return e.NotJSON }, "不是接口的 JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, n := server(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.header {
					w.Header().Set(k, v)
				}
				w.Header().Set("Content-Type", c.ctype)
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})
			_, err := u.Lookup(context.Background(), session(), Ref{RefCode, "CO78534294"})
			var e *APIError
			if !errors.As(err, &e) || !c.check(e) {
				t.Fatalf("got %#v (%v)", e, err)
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Errorf("message %q lacks %q", err.Error(), c.message)
			}
			if n.Load() != 1 {
				t.Errorf("%d requests: the API forbids automatic retries", n.Load())
			}
		})
	}
}

func TestListsAndTheirPages(t *testing.T) {
	var queries []string
	u, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/api/cli/v1/hongbao/plaza":
			jsonAnswer(w, 200, `{"list":[{"id":"a","type":"scratch","total_amount":5,"creator":{"linuxdo_id":1,"username":"x"},
				"available":true,"can_claim":true,"reason":null,"participated":false,"status":"active","created_at":1,
				"progress":{"kind":"cells","used":3,"total":9},"allowed_trust_levels":[0,1,2,3,4],"link":"https://hb.unsnow.org/hb/a"}],
				"total":1,"has_more":false,"server_now":1}`)
		case "/api/cli/v1/hongbao/sent":
			jsonAnswer(w, 200, `{"list":[{"id":"s","type":"random","created_at":1,"link":"l","status":"expired","total_amount":10,"count":5,"sent_amount":6.5,"sent_count":3}],"total":30,"has_more":true,"server_now":1}`)
		case "/api/cli/v1/hongbao/received":
			jsonAnswer(w, 200, `{"list":[{"id":"r","type":"random","created_at":1,"link":"l","status":"ended","creator_linuxdo_id":7,"creator_username":"demouo","received_amount":0.32,"received_at":5}],"total":1,"has_more":false,"server_now":1}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	pl, err := u.Plaza(ctx, session(), Page{})
	if err != nil || len(pl.List) != 1 || pl.List[0].Progress.Kind != "cells" || !pl.List[0].CanClaim {
		t.Fatalf("%+v %v", pl, err)
	}
	sent, err := u.Sent(ctx, session(), Page{Limit: 5, Offset: 10})
	if err != nil || sent.List[0].SentAmount != 6.5 || !sent.HasMore || sent.Total != 30 {
		t.Fatalf("%+v %v", sent, err)
	}
	got, err := u.Received(ctx, session(), Page{Limit: 50})
	if err != nil || got.List[0].CreatorUsername != "demouo" || got.List[0].ReceivedAmount != 0.32 {
		t.Fatalf("%+v %v", got, err)
	}
	want := []string{"/api/cli/v1/hongbao/plaza?", "/api/cli/v1/hongbao/sent?limit=5&offset=10", "/api/cli/v1/hongbao/received?limit=50"}
	for i := range want {
		if queries[i] != want[i] {
			t.Errorf("request %d: %s want %s", i, queries[i], want[i])
		}
	}
	// out-of-range pages are refused here, before any request
	before := len(queries)
	for _, p := range []Page{{Limit: 51}, {Limit: -1}, {Offset: 10001}, {Offset: -1}} {
		if _, err := u.Received(ctx, session(), p); err == nil {
			t.Errorf("%+v should be refused", p)
		}
	}
	if len(queries) != before {
		t.Fatalf("a refused page must not reach the site")
	}
}
