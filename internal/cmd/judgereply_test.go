package cmd

import (
	"strings"
	"testing"
)

func TestJudgeReplyBecomesOneLine(t *testing.T) {
	// the shape of a real answer (from a real claim), with the long parts abbreviated
	const real = `{"result":"judged","message":"领取 6.28","moved":{"added":"6.28","pending":"6.28"},` +
		`"settled":{"claim":{"id":"d60326a7","activity":{"id":"a7250300","title":"矿洞夺宝","participationMode":"code","funding":"points"}}}}`
	if got := describeJudgeReply([]byte(real)); got != "红包站：领取 6.28 · 新增 6.28 · 待结算 6.28" {
		t.Fatalf("got %q", got)
	}
}

func TestJudgeReplyOtherShapes(t *testing.T) {
	cases := map[string]string{
		`{"result":"judged","message":"猜大了"}`:                        "红包站：猜大了",
		`{"result":"judged"}`:                                        "红包站：judged",
		`{"result":"rejected","message":"不在领取范围里"}`:                  "红包站（rejected）：不在领取范围里",
		`{"result":"ended"}`:                                         "红包站（ended）：ended",
		`{"result":"judged","message":"x","moved":{"added":"1.00"}}`: "红包站：x · 新增 1.00",
	}
	for in, want := range cases {
		if got := describeJudgeReply([]byte(in)); got != want {
			t.Errorf("%s\n got  %q\n want %q", in, got, want)
		}
	}
	// something unrecognised is shown as it is, shortened
	long := `{"weird":"` + strings.Repeat("字", 500) + `"}`
	got := describeJudgeReply([]byte(long))
	if !strings.HasPrefix(got, "红包站回复：") || len([]rune(got)) > 330 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %d runes: %q", len([]rune(got)), got[:40])
	}
	if got := describeJudgeReply([]byte("not json")); got != "红包站回复：not json" {
		t.Fatalf("got %q", got)
	}
}
