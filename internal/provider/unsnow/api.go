package unsnow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"hb/internal/auth"
)

// RefKind says how a reference names a packet, which picks the endpoint.
type RefKind int

const (
	RefID       RefKind = iota // a packet id from its link: never counts as a miss
	RefCode                    // a random passcode: 2 letters + 8 digits (or the old 8 digits)
	RefTextCode                // a text passcode: 6-32 汉字, letters or digits, case-sensitive
)

var (
	hexIDRe    = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	linkIDRe   = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	randCodeRe = regexp.MustCompile(`^([A-Za-z]{2})?[0-9]{8}$`)
	textCodeRe = regexp.MustCompile(`^[\p{Han}A-Za-z0-9]+$`)
)

// Ref is a parsed reference to a packet.
type Ref struct {
	Kind  RefKind
	Value string
}

// ParseRef reads a link (https://hb.unsnow.org/hb/<id> or /text-code/<code>),
// a 32-character packet id, or a passcode. A passcode's shape picks its
// endpoint; force says which kind it is when the shape alone cannot (8 digits
// are a random passcode unless forced to text). A passcode that cannot be
// valid is refused here: the site would still spend a lookup on it.
func ParseRef(s string, force *RefKind) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, errors.New("空的口令或链接")
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if !strings.EqualFold(u.Hostname(), "hb.unsnow.org") {
			return Ref{}, fmt.Errorf("不是 hb.unsnow.org 的链接：%s", u.Host)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 {
			switch parts[0] {
			case "hb":
				if linkIDRe.MatchString(parts[1]) {
					return Ref{RefID, parts[1]}, nil
				}
			case "text-code":
				if code, err := url.PathUnescape(parts[1]); err == nil {
					return textCode(code)
				}
			}
		}
		return Ref{}, fmt.Errorf("看不出这个链接指向哪个红包：%s", u.Path)
	}
	if force != nil {
		switch *force {
		case RefCode:
			return randomCode(s)
		case RefTextCode:
			return textCode(s)
		}
	}
	switch {
	case hexIDRe.MatchString(s):
		return Ref{RefID, strings.ToLower(s)}, nil
	case randCodeRe.MatchString(s):
		return randomCode(s)
	}
	return textCode(s)
}

func randomCode(s string) (Ref, error) {
	if !randCodeRe.MatchString(s) {
		return Ref{}, errors.New("随机口令是 2 个字母加 8 位数字（或旧的 8 位数字）")
	}
	return Ref{RefCode, strings.ToUpper(s)}, nil
}

func textCode(s string) (Ref, error) {
	if n := utf8.RuneCountInString(s); n < 6 || n > 32 || !textCodeRe.MatchString(s) {
		return Ref{}, errors.New("文字口令是 6–32 个汉字、英文字母或数字（不含空格和符号）")
	}
	return Ref{RefTextCode, s}, nil
}

func (r Ref) path() string {
	switch r.Kind {
	case RefID:
		return "/hongbao/" + url.PathEscape(r.Value)
	case RefCode:
		return "/hongbao/code/" + url.PathEscape(r.Value)
	default:
		return "/hongbao/text-code/" + url.PathEscape(r.Value)
	}
}

// Creator is who sent a packet.
type Creator struct {
	LinuxDoID int64   `json:"linuxdo_id"`
	Username  string  `json:"username"`
	Name      *string `json:"name"`
}

// Progress is how far a packet has gone. Kind "shares": shares taken (may
// include ones still being settled). Kind "cells" (a 刮刮乐 whose results are
// not public): cells picked, which says nothing about winners.
type Progress struct {
	Kind  string `json:"kind"`
	Used  int    `json:"used"`
	Total int    `json:"total"`
}

// Packet is a packet as the API shows it.
type Packet struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"` // random, equal, exclusive, lottery, scratch
	DistributionType *string  `json:"distribution_type"`
	Wish             *string  `json:"wish"`
	TotalAmount      float64  `json:"total_amount"` // LDC, not what has been paid out
	Creator          Creator  `json:"creator"`
	Available        bool     `json:"available"` // still open, whoever asks
	CanClaim         bool     `json:"can_claim"` // the account may claim it on the page, as of now
	Reason           *string  `json:"reason"`    // why not, when it may not
	Participated     bool     `json:"participated"`
	Status           string   `json:"status"` // active, ended
	CreatedAt        int64    `json:"created_at"`
	ExpiresAt        *int64   `json:"expires_at"`
	DrawAt           *int64   `json:"draw_at"`
	Progress         Progress `json:"progress"`
	ParticipantCount *int     `json:"participant_count"` // lotteries only
	Levels           []int    `json:"allowed_trust_levels"`
	MaxClaims24h     *int     `json:"max_claims_24h"`
	Link             string   `json:"link"` // open it in a browser to claim
	ServerNow        int64    `json:"server_now"`
	// A text passcode's own rule: one claim per round of packets sharing it.
	TextCodeLocked      bool   `json:"text_code_locked"`
	TextCodeLockedUntil *int64 `json:"text_code_locked_until"`
}

// Lookup asks about one packet: one request, never retried.
func (u *Unsnow) Lookup(ctx context.Context, s auth.Session, ref Ref) (Packet, error) {
	var p Packet
	err := u.get(ctx, s, ref.path(), nil, &p)
	return p, err
}

// Page is a page of a list: up to 50 items, offset at most 10000.
type Page struct{ Limit, Offset int }

func (p Page) query() (url.Values, error) {
	q := url.Values{}
	if p.Limit != 0 {
		if p.Limit < 1 || p.Limit > 50 {
			return nil, errors.New("--limit 要在 1–50")
		}
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset != 0 {
		if p.Offset < 0 || p.Offset > 10000 {
			return nil, errors.New("--offset 要在 0–10000")
		}
		q.Set("offset", strconv.Itoa(p.Offset))
	}
	return q, nil
}

// Plaza is the public plaza: open packets first, finished ones after,
// expired ones not at all.
type Plaza struct {
	List      []Packet `json:"list"`
	Total     int      `json:"total"`
	HasMore   bool     `json:"has_more"`
	ServerNow int64    `json:"server_now"`
}

func (u *Unsnow) Plaza(ctx context.Context, s auth.Session, page Page) (Plaza, error) {
	var out Plaza
	q, err := page.query()
	if err != nil {
		return out, err
	}
	err = u.get(ctx, s, "/hongbao/plaza", q, &out)
	return out, err
}

// SentRecord is one packet the account made: its total, and what reached people.
type SentRecord struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	Wish        *string `json:"wish"`
	CreatedAt   int64   `json:"created_at"`
	ExpiresAt   *int64  `json:"expires_at"`
	Link        string  `json:"link"`
	Status      string  `json:"status"`
	TotalAmount float64 `json:"total_amount"`
	Count       int     `json:"count"`
	SentAmount  float64 `json:"sent_amount"`
	SentCount   int     `json:"sent_count"`
}

// ReceivedRecord is one claim that reached the account (paid, not pending).
type ReceivedRecord struct {
	ID              string  `json:"id"`
	Type            string  `json:"type"`
	Wish            *string `json:"wish"`
	CreatedAt       int64   `json:"created_at"`
	Link            string  `json:"link"`
	Status          string  `json:"status"`
	CreatorUsername string  `json:"creator_username"`
	ReceivedAmount  float64 `json:"received_amount"`
	ReceivedAt      int64   `json:"received_at"`
}

// History is a page of the account's own records, newest first.
type History[T any] struct {
	List      []T   `json:"list"`
	Total     int   `json:"total"`
	HasMore   bool  `json:"has_more"`
	ServerNow int64 `json:"server_now"`
}

func (u *Unsnow) Sent(ctx context.Context, s auth.Session, page Page) (History[SentRecord], error) {
	var out History[SentRecord]
	q, err := page.query()
	if err != nil {
		return out, err
	}
	err = u.get(ctx, s, "/hongbao/sent", q, &out)
	return out, err
}

func (u *Unsnow) Received(ctx context.Context, s auth.Session, page Page) (History[ReceivedRecord], error) {
	var out History[ReceivedRecord]
	q, err := page.query()
	if err != nil {
		return out, err
	}
	err = u.get(ctx, s, "/hongbao/received", q, &out)
	return out, err
}
