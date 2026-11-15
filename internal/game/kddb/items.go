package kddb

import "fmt"

// How an item is used (docs/V3_ITEMS.md §3, §4, and the web page's ITEM_UX).
type useKind int

const (
	useAim     useKind = iota // pick a cell, then confirm: spyglass, torch, enchanted pickaxes, key
	useConfirm                // ask y/n: map, potion, ender chest
	useChoose                 // pick one of the offered shapes: enchanting table
)

type itemDef struct {
	ID    string // the API's item id
	Name  string
	Short string // one or two characters for the toolbar
	How   useKind
	Help  string
	count func(Me) int
}

func pick(me Me, shape string) int {
	if me.Items == nil {
		return 0
	}
	return me.Items.Pick[shape]
}

func inv(me Me, f func(*Items) int) int {
	if me.Items == nil {
		return 0
	}
	return f(me.Items)
}

// itemDefs is the toolbar, in the web page's order.
var itemDefs = []itemDef{
	{"spyglass", "望远镜", "望", useAim, "看一格是什么（只有你看得见）", func(m Me) int { return m.Spyglasses }},
	{"torch", "火把", "炬", useAim, "插在一格上，大家都看得见它周围的危险", func(m Me) int { return inv(m, func(i *Items) int { return i.Torch }) }},
	{"pick-h", "附魔镐·横", "横", useAim, "一次挖开横向三格，不花机会", func(m Me) int { return pick(m, "h") }},
	{"pick-v", "附魔镐·竖", "竖", useAim, "一次挖开竖向三格，不花机会", func(m Me) int { return pick(m, "v") }},
	{"pick-x", "附魔镐·十字", "十", useAim, "一次挖开十字五格，不花机会", func(m Me) int { return pick(m, "x") }},
	{"key", "试炼钥匙", "钥", useAim, "对准宝库使用，开一次", func(m Me) int { return inv(m, func(i *Items) int { return i.Key }) }},
	{"map", "藏宝图", "图", useConfirm, "抽一格还没挖开的宝贝并标出来（只有你看得见）", func(m Me) int { return inv(m, func(i *Items) int { return i.Map }) }},
	{"potion", "急迫药水", "药", useConfirm, "10 秒里挖掘间隔变成 1 秒", func(m Me) int { return inv(m, func(i *Items) int { return i.Potion }) }},
	{"ender", "末影箱", "箱", useConfirm, "手上的分一半存进箱子、一半毁掉；一个只能用一次", func(m Me) int { return inv(m, func(i *Items) int { return i.Ender }) }},
	{"table", "附魔台", "台", useChoose, "挑一种附魔镐，得到 2 次", func(m Me) int { return inv(m, func(i *Items) int { return i.Table }) }},
}

// slots lists the items you hold, numbered 1.. in toolbar order.
func slots(me Me) []itemDef {
	var out []itemDef
	for _, d := range itemDefs {
		if d.count(me) > 0 {
			out = append(out, d)
		}
	}
	return out
}

var shapeNames = map[string]string{"h": "横三格", "v": "竖三格", "x": "十字"}

// aimProblem explains why an aimed item cannot go on this cell, or "".
func aimProblem(d itemDef, c *PublicCard, idx int, rules Rules) string {
	if c == nil || idx < 0 || idx >= len(c.Cells) {
		return "先选一格"
	}
	row, col := idx/c.Size, idx%c.Size
	switch d.ID {
	case "torch":
		// first item rules: the torch sits on a corner (needs a cell with a row and column above and left)
		if rules.Items != 2 && (row < 1 || col < 1) {
			return "火把要插在四格相交处：选一个不在最上行、最左列的格子"
		}
	case "key":
		if c.Fixed[fmt.Sprint(idx)] != "vault" {
			return "钥匙要对准宝库（▣）"
		}
	case "spyglass":
		if c.Cells[idx] != nil {
			return "这一格已经挖开了"
		}
	}
	return ""
}

func torchColor(level int) string {
	switch level {
	case 0:
		return "34" // green: no danger around
	case 1:
		return "178" // yellow
	case 2:
		return "208" // orange
	}
	return "196" // red
}

func torchWords(level int, v int) string {
	if v == 2 {
		return [...]string{"周围没有危险", "周围有 1–2 个危险", "周围有 3–4 个危险", "周围有 5 个以上危险"}[min(level, 3)]
	}
	return [...]string{"这四格里没有危险", "这四格里有 1–2 个危险", "这四格里有 3–4 个危险", "这四格里有 3–4 个危险"}[min(level, 3)]
}
