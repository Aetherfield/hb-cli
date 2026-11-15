// Package kddb is a client for 矿洞夺宝 (github.com/LeUKi/kddb, MIT): the
// Minecraft-style multiplayer digging packet on lafish. The game runs on its
// own Cloudflare Worker; its page is only a shell around that API, so a
// terminal client can play it. Types mirror page/frontend/v3/src/types.ts.
package kddb

import "encoding/json"

type Kind string

// KindNames are the Chinese names the game uses (types.ts KIND_NAMES).
var KindNames = map[Kind]string{
	"emerald": "绿宝石", "diamond": "钻石", "apple": "附魔金苹果", "totem": "双铁镐",
	"tnt": "TNT", "pearl": "末影珍珠", "shield": "盾牌", "spyglass": "望远镜", "eye": "末影之眼", "crystal": "末影水晶",
	"coal": "煤炭", "stick": "木棍", "map": "藏宝图", "potion": "急迫药水", "key": "试炼钥匙", "vault": "宝库", "deco": "装饰",
	"lava": "岩浆", "debris": "远古残骸", "cobweb": "蜘蛛网", "spawner": "刷怪笼", "minecart": "箱子矿车", "ender": "末影箱",
}

var BiomeNames = map[string]string{
	"end": "末地宝藏", "trial": "试炼密室", "mine": "废弃矿井", "nether": "下界", "deep": "深暗之域",
}

type OpenCell struct {
	Kind Kind   `json:"kind"`
	UID  int64  `json:"uid"`
	Name string `json:"name"`
	At   int64  `json:"at"`
	Burn int    `json:"burn,omitempty"` // swallowed by lava
}

// Torch: level 0 green, 1 yellow, 2 orange (v2) / red (v1), 3 red (v2).
type Torch struct {
	At    int    `json:"at"`
	Level int    `json:"level"`
	Name  string `json:"name"`
	V     int    `json:"v,omitempty"`
}

type WokeBy struct {
	UID    int64  `json:"uid"`
	Name   string `json:"name"`
	Amount int    `json:"amount"`
}

type PublicCard struct {
	ID       int               `json:"id"`
	X        int               `json:"x"`
	Y        int               `json:"y"`
	Size     int               `json:"size"`
	Kind     string            `json:"kind,omitempty"` // biome; empty on a plain card
	Fixed    map[string]string `json:"fixed,omitempty"`
	Vaults   map[string]int    `json:"vaults,omitempty"` // cell -> openings left
	Torches  []Torch           `json:"torches,omitempty"`
	Noise    float64           `json:"noise,omitempty"`
	Closed   bool              `json:"closed,omitempty"`
	WokeBy   *WokeBy           `json:"wokeBy,omitempty"`
	FromEye  bool              `json:"fromEye,omitempty"`
	Cells    []*OpenCell       `json:"cells"` // null = not dug yet
	Left     map[Kind]int      `json:"left"`
	Finished bool              `json:"finished"`
}

type FeedItem struct {
	Seq       int    `json:"seq"`
	UID       int64  `json:"uid"`
	Name      string `json:"name"`
	Kind      Kind   `json:"kind"`
	CardID    int    `json:"cardId"`
	Index     int    `json:"index"`
	Text      string `json:"text"`
	At        int64  `json:"at"`
	NewCardID *int   `json:"newCardId"`
}

type Stats struct {
	Digs          int `json:"digs"`
	Players       int `json:"players"`
	Claimed       int `json:"claimed"`
	ClaimedPoints int `json:"claimedPoints"`
}

type Rules struct {
	Difficulty  string  `json:"difficulty"`
	DigInterval float64 `json:"digInterval"` // seconds between two digs of one person; absent on old games = 1
	Creeper     int     `json:"creeper"`
	Items       int     `json:"items"` // 1: item bar; 2: second item rules (torch lights a 3x3 on the cell itself)
	Step        string  `json:"step"`  // smallest unit of a third-party-points packet
}

type PublicGame struct {
	Seq        int          `json:"seq"`
	Rules      Rules        `json:"rules"`
	Pool       int          `json:"pool"`
	Left       int          `json:"left"`
	DeadlineAt *int64       `json:"deadlineAt"`
	WrapUpAt   *int64       `json:"wrapUpAt"`
	Over       bool         `json:"over"`
	Stats      Stats        `json:"stats"`
	Cards      []PublicCard `json:"cards"`
	Feed       []FeedItem   `json:"feed"`
}

type Items struct {
	Torch  int            `json:"torch"`
	Map    int            `json:"map"`
	Potion int            `json:"potion"`
	Ender  int            `json:"ender"`
	Table  int            `json:"table"`
	Key    int            `json:"key"`
	Pick   map[string]int `json:"pick"` // h, v, x
}

type DigResult struct {
	Kind      Kind   `json:"kind"`
	CardID    int    `json:"cardId"`
	Index     int    `json:"index"`
	Amount    int    `json:"amount"`
	Blocked   bool   `json:"blocked"`
	Text      string `json:"text"`
	NewCardID *int   `json:"newCardId"`
	Loot      string `json:"loot,omitempty"`
}

// Peek is a cell only you know (spyglass or treasure map).
type Peek struct {
	CardID int  `json:"cardId"`
	Index  int  `json:"index"`
	Kind   Kind `json:"kind"`
}

type Me struct {
	Chances      int        `json:"chances"`
	Shield       bool       `json:"shield"`
	Peeks        []Peek     `json:"peeks"`
	Points       int        `json:"points"`
	Digs         int        `json:"digs"`
	Status       string     `json:"status"` // playing | claimed
	LastResult   *DigResult `json:"lastResult"`
	CoolingMs    int        `json:"coolingMs"`
	Shields      int        `json:"shields"`
	Spyglasses   int        `json:"spyglasses"`
	Items        *Items     `json:"items"`
	Coal         int        `json:"coal"`
	Stick        int        `json:"stick"`
	Poison       int        `json:"poison"`
	Chest        int        `json:"chest"`
	HasteMs      int        `json:"hasteMs"`
	SlowMs       int        `json:"slowMs"`
	TableChoices []string   `json:"tableChoices"`
	CreeperMs    *int       `json:"creeperMs"`
	VaultsOpened []string   `json:"vaultsOpened"`
}

type State struct {
	Game    PublicGame `json:"game"`
	Me      Me         `json:"me"`
	Blocked *string    `json:"blocked"`
	Drained int        `json:"drained"`
}

// DigAnswer is POST /dig. On a miss OK is false and Message says why.
type DigAnswer struct {
	OK       bool            `json:"ok"`
	Message  string          `json:"message"`
	Result   *DigResult      `json:"result"`
	Me       Me              `json:"me"`
	ClaimNow bool            `json:"claimNow"`
	Update   json.RawMessage `json:"update"`
	Blocked  *string         `json:"blocked"`
	Card     *PublicCard     `json:"card"`
}

// UseAnswer is POST /use (docs/V3_ITEMS.md §4).
type UseAnswer struct {
	OK       bool              `json:"ok"`
	Message  string            `json:"message"`
	Me       Me                `json:"me"`
	Text     string            `json:"text"`
	ClaimNow bool              `json:"claimNow"`
	Left     *int              `json:"left"`
	Updates  []json.RawMessage `json:"updates"`
	Results  []DigResult       `json:"results"`
	Peek     *Peek             `json:"peek"`
	Card     *PublicCard       `json:"card"`
	Blocked  *string           `json:"blocked"`
	Reward   *struct {
		Kind   string `json:"kind"`
		Amount int    `json:"amount"`
	} `json:"reward"`
}

// ClaimAnswer is POST /claim; Voucher is what lafish's judge takes as input.
type ClaimAnswer struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Voucher string `json:"voucher"`
	Points  int    `json:"points"`
	Me      Me     `json:"me"`
}

// Step is one entry of your dig log; TopEntry a leaderboard line (GET /summary).
type Step struct {
	K string `json:"k"`
	A int    `json:"a"`
	P int    `json:"p"`
	B int    `json:"b,omitempty"`
}
type TopEntry struct {
	UID    int64  `json:"uid"`
	Name   string `json:"name"`
	Points int    `json:"points"`
}
type Summary struct {
	Me struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Points  int    `json:"points"`
		Digs    int    `json:"digs"`
		Trail   []Step `json:"trail"`
		Best    int    `json:"best"`
		TNT     int    `json:"tnt"`
		Blocked int    `json:"blocked"`
		Lost    int    `json:"lost"`
	} `json:"me"`
	Top   []TopEntry `json:"top"`
	Stats Stats      `json:"stats"`
	Pool  int        `json:"pool"`
	Left  int        `json:"left"`
	Over  bool       `json:"over"`
}

// Live is one push message. Only the fields hb uses are typed.
type Live struct {
	Type    string      `json:"type"`
	Seq     int         `json:"seq"`
	CardID  int         `json:"cardId"`
	Index   int         `json:"index"`
	Cell    *OpenCell   `json:"cell"`
	NewCard *PublicCard `json:"newCard"`
	Card    *PublicCard `json:"card"`
	Text    string      `json:"text"`
	Left    *int        `json:"left"`
	Over    *bool       `json:"over"`
	Stats   *Stats      `json:"stats"`
	Name    string      `json:"name"`
	Points  int         `json:"points"`
	Feed    *FeedItem   `json:"feed"`
	Torches []Torch     `json:"torches"`
	Burned  []struct {
		Index int  `json:"index"`
		Kind  Kind `json:"kind"`
	} `json:"burned"`
	Noise  *float64        `json:"noise"`
	Closed *bool           `json:"closed"`
	Vaults map[string]int  `json:"vaults"`
	Raw    json.RawMessage `json:"-"`
}
