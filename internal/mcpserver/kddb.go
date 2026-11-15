package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hb/internal/auth"
	"hb/internal/game/kddb"
)

// 矿洞夺宝 for an assistant. lafish said it may ("你随意"), and a packet's sender
// can still turn token play off (the page identity then comes back
// TOKEN_CLAIMS_OFF). What the person decides, in their own client:
//
//   - a grant: "at most N digs on this packet, for ten minutes", asked once.
//     No grant, no dig. One grant at a time, so nothing digs in parallel.
//   - what is dug inside a grant is the assistant's pick, one request per dig,
//     paced by the game's own interval; a danger (TNT…) or any failure ends the
//     grant on the spot.
//   - claiming the earnings is a decision of its own, asked on its own.

// KddbGame is one connection to a game: the person's seat at the packet.
type KddbGame interface {
	Title() string
	State(ctx context.Context) (kddb.State, error)
	Dig(ctx context.Context, cardID, index int) (kddb.DigAnswer, error)
	Claim(ctx context.Context) (kddb.ClaimAnswer, error)
	// Submit hands a claim voucher to the packet's judge on lafish.
	Submit(ctx context.Context, voucher string) (string, error)
}

// Kddb opens the game of a packet.
type Kddb interface {
	Open(ctx context.Context, ses auth.Session, packetID string) (KddbGame, error)
}

// Caps on a grant. The assistant asks for a number; the code decides.
const (
	MaxDigsPerGrant = 10
	grantTTL        = 10 * time.Minute
	minDigGap       = 2 * time.Second
)

// DefaultGrantLimits is how many grants may be asked for: 3 an hour, 10 a day.
func DefaultGrantLimits() Limits {
	l := DefaultLimits()
	l.PerHour, l.PerDay = 3, 10
	return l
}

type grant struct {
	id      string
	packet  string
	game    KddbGame
	left    int
	expires time.Time
	nextAt  time.Time
	ended   string // why it ended, if it did
}

func (g *grant) live(now time.Time) bool { return g.ended == "" && g.left > 0 && now.Before(g.expires) }

// ---- reading ----

type kddbPacketIn struct {
	Packet string `json:"packet" jsonschema:"用户给的红包链接或编号"`
}

type cardOut struct {
	ID     int            `json:"id"`
	Size   int            `json:"size"`
	Biome  string         `json:"biome,omitempty"`
	Dug    int            `json:"dug"`
	Cells  int            `json:"cells"`
	Closed bool           `json:"closed,omitempty"`
	Left   map[string]int `json:"left,omitempty"`
}

type kddbStateOut struct {
	Note        string            `json:"note"`
	Over        bool              `json:"game_over"`
	Status      string            `json:"status"`
	Chances     int               `json:"chances"`
	Points      int               `json:"points"`
	Digs        int               `json:"digs_so_far"`
	CoolingSecs float64           `json:"cooling_seconds"`
	DigInterval float64           `json:"dig_interval_seconds"`
	Cards       []cardOut         `json:"cards"`
	Untrusted   map[string]string `json:"untrusted"`
	Grant       *grantOut         `json:"grant,omitempty"`
}

type grantOut struct {
	ID        string `json:"grant_id"`
	DigsLeft  int    `json:"digs_left"`
	ExpiresIn string `json:"expires_in"`
}

func (s *server) kddbGame(ctx context.Context, packet string) (KddbGame, string, error) {
	id, err := s.packetID(packet)
	if err != nil {
		return nil, "", err
	}
	ses, err := s.tokenSession()
	if err != nil {
		return nil, "", err
	}
	g, err := s.o.Kddb.Open(ctx, ses, id)
	if err != nil {
		s.audit("kddb_open", id, outcome(err))
		return nil, id, err
	}
	return g, id, nil
}

func cards(st kddb.State) []cardOut {
	var out []cardOut
	for _, c := range st.Game.Cards {
		left := map[string]int{}
		for k, n := range c.Left {
			if n > 0 {
				left[string(k)] = n
			}
		}
		out = append(out, cardOut{ID: c.ID, Size: c.Size, Biome: c.Kind, Dug: c.Opened(), Cells: c.Size * c.Size, Closed: c.Closed, Left: left})
	}
	return out
}

func (s *server) kddbState(ctx context.Context, _ *mcp.CallToolRequest, in kddbPacketIn) (*mcp.CallToolResult, kddbStateOut, error) {
	g, id, err := s.kddbGame(ctx, in.Packet)
	if err != nil {
		return nil, kddbStateOut{}, err
	}
	st, err := g.State(ctx)
	s.audit("kddb_state", id, outcome(err))
	if err != nil {
		return nil, kddbStateOut{}, err
	}
	out := kddbStateOut{
		Note: untrustedNote, Over: st.Game.Over, Status: st.Me.Status, Chances: st.Me.Chances, Points: st.Me.Points, Digs: st.Me.Digs,
		CoolingSecs: float64(st.Me.CoolingMs) / 1000, DigInterval: st.Game.Rules.DigInterval, Cards: cards(st),
		Untrusted: map[string]string{"title": clean(g.Title(), 200)},
	}
	s.mu.Lock()
	if cur := s.grant; cur != nil && cur.live(s.now()) && cur.packet == id {
		out.Grant = &grantOut{ID: cur.id, DigsLeft: cur.left, ExpiresIn: wait(cur.expires.Sub(s.now()))}
	}
	s.mu.Unlock()
	return nil, out, nil
}

type boardIn struct {
	Packet string `json:"packet" jsonschema:"用户给的红包链接或编号"`
	Card   int    `json:"card,omitempty" jsonschema:"卡号；不填就是第一张还没挖完的"`
}

type cellRef struct {
	Row  int     `json:"row"`
	Col  int     `json:"col"`
	Risk float64 `json:"risk,omitempty"`
}

type boardOut struct {
	Note  string         `json:"note"`
	Card  int            `json:"card"`
	Size  int            `json:"size"`
	Grid  [][]string     `json:"grid"`
	Key   string         `json:"key"`
	Left  map[string]int `json:"left"`
	Torch []cellRef      `json:"torches,omitempty"`
	// Advice is an estimate from what the game shows: not a promise.
	Advice struct {
		Exact   bool      `json:"exact"`
		Dangers int       `json:"dangers_hidden"`
		Safe    []cellRef `json:"cells_with_no_danger"`
		Lowest  []cellRef `json:"lowest_risk_cells"`
		Notes   []string  `json:"notes,omitempty"`
	} `json:"advice"`
}

func (s *server) kddbBoard(ctx context.Context, _ *mcp.CallToolRequest, in boardIn) (*mcp.CallToolResult, boardOut, error) {
	g, id, err := s.kddbGame(ctx, in.Packet)
	if err != nil {
		return nil, boardOut{}, err
	}
	st, err := g.State(ctx)
	s.audit("kddb_board", id, outcome(err))
	if err != nil {
		return nil, boardOut{}, err
	}
	card, ok := pickCard(st, in.Card)
	if !ok {
		return nil, boardOut{}, errors.New("没有这张卡，或者所有的卡都挖完了")
	}
	out := boardOut{Note: "格子的内容是游戏给的数据；advice 是 hb 按规则做的估计，不是保证：没有任何格子能保证安全，除非 cells_with_no_danger 里有它。",
		Card: card.ID, Size: card.Size, Left: map[string]int{},
		Key: ".=没挖；fixed:xxx=固定格，挖不了；peek:xxx=只有你知道的；其他是已挖出的东西（tnt、crystal、spawner 是危险）"}
	for k, n := range card.Left {
		if n > 0 {
			out.Left[string(k)] = n
		}
	}
	peek := map[int]kddb.Kind{}
	for _, p := range st.Me.Peeks {
		if p.CardID == card.ID {
			peek[p.Index] = p.Kind
		}
	}
	for r := 0; r < card.Size; r++ {
		row := make([]string, card.Size)
		for c := 0; c < card.Size; c++ {
			i := r*card.Size + c
			switch {
			case i < len(card.Cells) && card.Cells[i] != nil:
				row[c] = string(card.Cells[i].Kind)
				if card.Cells[i].Burn == 1 {
					row[c] = "lava"
				}
			case card.Fixed[fmt.Sprint(i)] != "":
				row[c] = "fixed:" + card.Fixed[fmt.Sprint(i)]
			case peek[i] != "":
				row[c] = "peek:" + string(peek[i])
			default:
				row[c] = "."
			}
		}
		out.Grid = append(out.Grid, row)
	}
	for _, t := range card.Torches {
		out.Torch = append(out.Torch, cellRef{Row: t.At/card.Size + 1, Col: t.At%card.Size + 1})
	}
	a := kddb.Advise(card, st.Me.Peeks)
	out.Advice.Exact, out.Advice.Dangers, out.Advice.Notes = a.Exact, a.Dangers, a.Notes
	for _, i := range a.Safe {
		out.Advice.Safe = append(out.Advice.Safe, cellRef{Row: i/card.Size + 1, Col: i%card.Size + 1})
	}
	type pick struct {
		i int
		r float64
	}
	var picks []pick
	for i, r := range a.Risk {
		picks = append(picks, pick{i, r})
	}
	sort.Slice(picks, func(x, y int) bool {
		if picks[x].r != picks[y].r {
			return picks[x].r < picks[y].r
		}
		return picks[x].i < picks[y].i
	})
	for _, p := range picks[:min(len(picks), 8)] {
		out.Advice.Lowest = append(out.Advice.Lowest, cellRef{Row: p.i/card.Size + 1, Col: p.i%card.Size + 1, Risk: float64(int(p.r*1000+0.5)) / 1000})
	}
	return nil, out, nil
}

func pickCard(st kddb.State, id int) (kddb.PublicCard, bool) {
	for _, c := range st.Game.Cards {
		if (id != 0 && c.ID == id) || (id == 0 && !c.Finished && !c.Closed) {
			return c, true
		}
	}
	return kddb.PublicCard{}, false
}

// ---- the grant ----

type grantIn struct {
	Packet string `json:"packet" jsonschema:"用户给的红包链接或编号"`
	Digs   int    `json:"digs" jsonschema:"想挖几次，1–10；不要多要"`
}

type grantResult struct {
	Result   string `json:"result"`
	GrantID  string `json:"grant_id,omitempty"`
	DigsLeft int    `json:"digs_left,omitempty"`
	ValidFor string `json:"valid_for,omitempty"`
	Next     string `json:"next"`
}

func (s *server) kddbGrant(ctx context.Context, req *mcp.CallToolRequest, in grantIn) (*mcp.CallToolResult, *grantResult, error) {
	if in.Digs < 1 || in.Digs > MaxDigsPerGrant {
		return nil, nil, fmt.Errorf("一次授权最多 %d 次挖掘，不能更多；先挖这一批，再让用户决定要不要下一批", MaxDigsPerGrant)
	}
	id, err := s.packetID(in.Packet)
	if err != nil {
		return nil, nil, err
	}
	if c := req.Session.InitializeParams(); c == nil || c.Capabilities == nil || c.Capabilities.Elicitation == nil {
		s.audit("kddb_dig_grant", id, "refused: the client cannot ask the user")
		return nil, nil, errors.New("挖矿需要你本人在客户端里授权，但这个客户端不能弹出确认。hb 不提供免确认的挖掘：请自己运行 `hb kddb`")
	}
	key := fmt.Sprintf("grant:%s:%d", id, in.Digs)
	answer, answered := req.Params.InputResponses["confirm"]
	if !answered {
		// everything that can be refused is refused before the person is asked
		s.mu.Lock()
		if g := s.grant; g != nil && g.live(s.now()) {
			s.mu.Unlock()
			return nil, nil, errors.New("已经有一个挖矿授权在用（同一时间只能有一个）：用完或等它作废后，再让用户重新授权")
		}
		s.mu.Unlock()
		if err := s.o.GrantGuard.Check(); err != nil {
			s.audit("kddb_dig_grant", id, "refused: "+err.Error())
			return nil, nil, err
		}
		g, _, err := s.kddbGame(ctx, in.Packet)
		if err != nil {
			return nil, nil, err
		}
		st, err := g.State(ctx)
		if err != nil {
			s.audit("kddb_dig_grant", id, outcome(err))
			return nil, nil, err
		}
		switch {
		case st.Game.Over:
			return nil, nil, errors.New("这局已经结束了")
		case st.Me.Status == "claimed":
			return nil, nil, errors.New("用户已经领取过了，不能再挖")
		case st.Me.Chances <= 0:
			return nil, nil, errors.New("用户的挖掘机会用完了")
		}
		digs := min(in.Digs, st.Me.Chances)
		key = fmt.Sprintf("grant:%s:%d", id, in.Digs)
		s.audit("kddb_dig_grant", id, "asking the user")
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{
				Message: fmt.Sprintf("AI 助手想在矿洞夺宝「%s」（%s）里替你挖，最多 %d 次，%s 内有效。\n你现在还有 %d 次机会、%d 积分。\n"+
					"挖哪一格由 AI 选；挖到危险的东西（TNT 之类）或出任何差错，授权立刻作废。名单上会标出这是用令牌玩的。\n允许吗？",
					clean(g.Title(), 100), shortID(id), digs, wait(grantTTL), st.Me.Chances, st.Me.Points),
				RequestedSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"confirm": map[string]any{"type": "boolean", "title": fmt.Sprintf("允许最多挖 %d 次", digs), "default": false},
					},
					"required": []string{"confirm"},
				},
			}},
			RequestState: s.sign(key, s.now()),
		}, nil, nil
	}
	if !s.verify(req.Params.RequestState, key) {
		s.audit("kddb_dig_grant", id, "refused: the confirmation does not belong to this request")
		return nil, nil, errors.New("这个确认无效或已过期：没有授权。要挖的话，请用户重新明确要求")
	}
	res, _ := answer.(*mcp.ElicitResult)
	if res == nil || res.Action != "accept" || res.Content["confirm"] != true {
		s.audit("kddb_dig_grant", id, "declined by the user")
		return nil, &grantResult{Result: "declined", Next: "用户没有授权，不能挖。不要再次尝试。"}, nil
	}
	if err := s.o.GrantGuard.Check(); err != nil {
		return nil, nil, err
	}
	g, _, err := s.kddbGame(ctx, in.Packet)
	if err != nil {
		return nil, nil, err
	}
	st, err := g.State(ctx)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.grant; cur != nil && cur.live(s.now()) {
		return nil, nil, errors.New("已经有一个挖矿授权在用（同一时间只能有一个）")
	}
	s.o.GrantGuard.Begin()
	s.o.GrantGuard.Done(true)
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	gr := &grant{id: hex.EncodeToString(raw[:]), packet: id, game: g, left: min(in.Digs, st.Me.Chances), expires: s.now().Add(grantTTL), nextAt: s.now()}
	s.grant = gr
	s.audit("kddb_dig_grant", id, fmt.Sprintf("granted %d digs", gr.left))
	return nil, &grantResult{Result: "granted", GrantID: gr.id, DigsLeft: gr.left, ValidFor: wait(grantTTL),
		Next: "只在这个授权里挖：先 kddb_board 看局面，再 kddb_dig 一格一格挖，每挖一格都看结果再决定下一格。挖到危险的东西授权就作废，停下来告诉用户。"}, nil
}

// ---- the dig ----

type digIn struct {
	GrantID string `json:"grant_id" jsonschema:"kddb_dig_grant 返回的 grant_id"`
	Card    int    `json:"card" jsonschema:"卡号（kddb_board 里的 card）"`
	Row     int    `json:"row" jsonschema:"第几行，从 1 开始"`
	Col     int    `json:"col" jsonschema:"第几列，从 1 开始"`
}

type digOut struct {
	Result    string `json:"result"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Amount    int    `json:"amount,omitempty"`
	Text      string `json:"text,omitempty"`
	Danger    bool   `json:"danger"`
	GrantOver bool   `json:"grant_over"`
	DigsLeft  int    `json:"digs_left"`
	Chances   int    `json:"chances_left"`
	Points    int    `json:"points"`
	ClaimNow  bool   `json:"claim_now,omitempty"`
	Next      string `json:"next"`
}

func (s *server) kddbDig(ctx context.Context, _ *mcp.CallToolRequest, in digIn) (*mcp.CallToolResult, *digOut, error) {
	s.mu.Lock()
	g := s.grant
	if g == nil || g.id != strings.TrimSpace(in.GrantID) {
		s.mu.Unlock()
		return nil, nil, errors.New("没有这个挖矿授权：先用 kddb_dig_grant 请用户授权")
	}
	if !g.live(s.now()) {
		why := firstNonEmpty(g.ended, "次数用完或已过期")
		s.mu.Unlock()
		return nil, nil, errors.New("这个授权已经作废（" + why + "）：要再挖，请用户重新授权")
	}
	if wait := g.nextAt.Sub(s.now()); wait > 0 {
		s.mu.Unlock()
		// nothing is sent and nothing is used up: it is only too soon
		return nil, nil, fmt.Errorf("太快了，还要等 %s 才能挖下一格（不是失败，没有用掉次数）", waitFine(wait))
	}
	// the dig is spent before it is sent, whatever comes of it; a dig that is
	// refused before it is sent gives both the count and the time back
	prev := g.nextAt
	g.left--
	g.nextAt = s.now().Add(minDigGap)
	s.mu.Unlock()

	end := func(why string) {
		s.mu.Lock()
		g.ended = why
		s.mu.Unlock()
	}
	st, err := g.game.State(ctx)
	if err != nil {
		end("读局面失败")
		s.audit("kddb_dig", g.packet, outcome(err))
		return nil, nil, fmt.Errorf("%w（授权已作废，不重试）", err)
	}
	card, ok := pickCard(st, in.Card)
	if !ok || card.ID != in.Card {
		s.refund(g, prev)
		return nil, nil, errors.New("没有这张卡（或它已经挖完、已关闭）：用 kddb_board 看现在有哪些卡")
	}
	if in.Row < 1 || in.Row > card.Size || in.Col < 1 || in.Col > card.Size {
		s.refund(g, prev)
		return nil, nil, fmt.Errorf("行和列都要在 1 到 %d", card.Size)
	}
	idx := (in.Row-1)*card.Size + (in.Col - 1)
	if (idx < len(card.Cells) && card.Cells[idx] != nil) || card.Fixed[fmt.Sprint(idx)] != "" {
		s.refund(g, prev)
		return nil, nil, errors.New("这一格已经挖过，或者是固定格、挖不了：用 kddb_board 看哪些格还没挖")
	}
	if cool := time.Duration(st.Me.CoolingMs) * time.Millisecond; cool > 0 {
		s.refund(g, prev)
		s.mu.Lock()
		g.nextAt = s.now().Add(cool)
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("游戏的挖掘冷却还有 %s（不是失败，没有用掉次数）", waitFine(cool))
	}
	ans, err := g.game.Dig(ctx, card.ID, idx)
	if err != nil {
		end("挖掘请求出错")
		s.audit("kddb_dig", g.packet, outcome(err))
		return nil, nil, fmt.Errorf("%w（授权已作废，不重试；告诉用户）", err)
	}
	if !ans.OK || ans.Result == nil {
		end("游戏没有接受这一下")
		s.audit("kddb_dig", g.packet, "not accepted: "+clean(ans.Message, 100))
		return nil, nil, fmt.Errorf("游戏没有接受这一下：%s（授权已作废，不重试；告诉用户）", clean(ans.Message, 100))
	}
	r := ans.Result
	out := &digOut{Result: "dug", Kind: string(r.Kind), Name: kddb.KindNames[r.Kind], Amount: r.Amount, Text: clean(r.Text, 120),
		Danger: kddb.IsDanger(r.Kind), Chances: ans.Me.Chances, Points: ans.Me.Points, ClaimNow: ans.ClaimNow}
	s.mu.Lock()
	switch {
	case out.Danger:
		g.ended = "挖到了危险的东西（" + string(r.Kind) + "）"
	case ans.Me.Chances <= 0 || ans.ClaimNow:
		g.ended = "机会用完"
	}
	out.GrantOver = !g.live(s.now())
	out.DigsLeft = g.left
	s.mu.Unlock()
	s.audit("kddb_dig", g.packet, fmt.Sprintf("card %d cell %d,%d: %s", card.ID, in.Row, in.Col, r.Kind))
	switch {
	case out.Danger:
		out.Next = "挖到了危险的东西，授权已作废。停下来，把结果告诉用户，由他决定下一步。"
	case out.GrantOver:
		out.Next = "这批授权用完了。把结果告诉用户；要继续挖或领取奖励，由他决定。"
	default:
		out.Next = "还可以挖。先看这一下的结果再决定下一格，不要一口气连着挖。"
	}
	return nil, out, nil
}

// refund gives back a dig that never reached the game: its count, and the wait it set.
func (s *server) refund(g *grant, nextAt time.Time) {
	s.mu.Lock()
	g.left++
	g.nextAt = nextAt
	s.mu.Unlock()
}

func waitFine(d time.Duration) string {
	if d < time.Second {
		return "不到 1 秒"
	}
	return wait(d)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- the earnings ----

type kddbClaimOut struct {
	Result string `json:"result"`
	Points int    `json:"points,omitempty"`
	Reply  string `json:"judge_reply,omitempty"`
	Next   string `json:"next"`
}

func (s *server) kddbClaim(ctx context.Context, req *mcp.CallToolRequest, in kddbPacketIn) (*mcp.CallToolResult, *kddbClaimOut, error) {
	id, err := s.packetID(in.Packet)
	if err != nil {
		return nil, nil, err
	}
	if c := req.Session.InitializeParams(); c == nil || c.Capabilities == nil || c.Capabilities.Elicitation == nil {
		s.audit("kddb_claim", id, "refused: the client cannot ask the user")
		return nil, nil, errors.New("领取奖励需要你本人在客户端里确认，但这个客户端不能弹出确认。请自己运行 `hb kddb`，领取后不能再挖")
	}
	key := "earnings:" + id
	answer, answered := req.Params.InputResponses["confirm"]
	g, _, err := s.kddbGame(ctx, in.Packet)
	if err != nil {
		return nil, nil, err
	}
	st, err := g.State(ctx)
	if err != nil {
		s.audit("kddb_claim", id, outcome(err))
		return nil, nil, err
	}
	if st.Me.Status == "claimed" {
		return nil, nil, errors.New("用户已经领取过了")
	}
	total := st.Me.Points + st.Me.Chest
	if !answered {
		if err := s.o.Guard.Check(); err != nil {
			s.audit("kddb_claim", id, "refused: "+err.Error())
			return nil, nil, err
		}
		s.audit("kddb_claim", id, "asking the user")
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{
				Message: fmt.Sprintf("AI 助手想替你领取矿洞夺宝「%s」里已经挖到的奖励：%d 积分。\n**领取后不能再挖。**确认领取吗？", clean(g.Title(), 100), total),
				RequestedSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"confirm": map[string]any{"type": "boolean", "title": "确认领取，之后不能再挖", "default": false},
					},
					"required": []string{"confirm"},
				},
			}},
			RequestState: s.sign(key, s.now()),
		}, nil, nil
	}
	if !s.verify(req.Params.RequestState, key) {
		s.audit("kddb_claim", id, "refused: the confirmation does not belong to this request")
		return nil, nil, errors.New("这个确认无效或已过期：没有领取。请用户重新明确要求")
	}
	res, _ := answer.(*mcp.ElicitResult)
	if res == nil || res.Action != "accept" || res.Content["confirm"] != true {
		s.audit("kddb_claim", id, "declined by the user")
		return nil, &kddbClaimOut{Result: "declined", Next: "用户没有确认，没有领取。不要再次尝试。"}, nil
	}
	if err := s.o.Guard.Check(); err != nil {
		return nil, nil, err
	}
	s.o.Guard.Begin()
	ans, err := g.Claim(ctx)
	if err != nil || !ans.OK {
		s.o.Guard.Done(false)
		msg := "领取没成功"
		if err != nil {
			msg = err.Error()
		} else if ans.Message != "" {
			msg = clean(ans.Message, 100)
		}
		s.audit("kddb_claim", id, "error: "+clean(msg, 120))
		return nil, nil, fmt.Errorf("%s（没有重试；告诉用户）", msg)
	}
	// the voucher is the proof of the payout: the person's copy is kept by Submit
	reply, err := g.Submit(ctx, ans.Voucher)
	if err != nil {
		s.o.Guard.Done(false)
		s.audit("kddb_claim", id, "claimed, judge not reached: "+clean(err.Error(), 120))
		return nil, &kddbClaimOut{Result: "claimed_not_settled", Points: ans.Points,
			Reply: clean(err.Error(), 300),
			Next:  "奖励已经领出来了，但交给红包站结算没成功（凭证 hb 已替用户存好，信息在 judge_reply 里）。如实告诉用户，让他自己处理，不要重试。"}, nil
	}
	s.o.Guard.Done(true)
	s.audit("kddb_claim", id, fmt.Sprintf("claimed %d points", ans.Points))
	return nil, &kddbClaimOut{Result: "claimed", Points: ans.Points, Reply: clean(reply, 300), Next: "把结果告诉用户。领取后不能再挖。"}, nil
}
