package kddb

// Apply merges one push message into the state. It follows page/frontend/v3/
// src/live.ts for the message types a terminal client needs; anything it does
// not understand is ignored (the next State() call corrects drift).
// It returns false when a gap in seq means the caller should reload State.
func (s *State) Apply(m Live) (inSync bool) {
	g := &s.Game
	inSync = true
	switch m.Type {
	case "cell":
		// Already merged (our own dig's answer arrives before its push).
		if m.Seq > 0 && m.Seq <= g.Seq {
			return true
		}
		// The message is still merged; the caller is told to reload State
		// because pushes in between were missed (GET /since or /state).
		if m.Seq > 0 && g.Seq > 0 && m.Seq > g.Seq+1 {
			inSync = false
		}
		if m.Seq > g.Seq {
			g.Seq = m.Seq
		}
		if card := g.card(m.CardID); card != nil {
			if m.Cell != nil && m.Index >= 0 && m.Index < len(card.Cells) {
				card.Cells[m.Index] = m.Cell
				if card.Left != nil && card.Left[m.Cell.Kind] > 0 {
					card.Left[m.Cell.Kind]--
				}
			}
			// items rules: torches changed colour, lava swallowed cells, noise, warden, vaults
			if m.Torches != nil {
				card.Torches = m.Torches
			}
			for _, b := range m.Burned {
				if b.Index >= 0 && b.Index < len(card.Cells) {
					card.Cells[b.Index] = &OpenCell{Kind: b.Kind, Burn: 1}
				}
			}
			if m.Noise != nil {
				card.Noise = *m.Noise
			}
			if m.Closed != nil {
				card.Closed = *m.Closed
			}
			if m.Vaults != nil {
				card.Vaults = m.Vaults
			}
		}
		if m.NewCard != nil {
			g.put(*m.NewCard)
		}
		s.common(m)
		if m.Text != "" && m.Cell != nil {
			g.Feed = append([]FeedItem{{Seq: m.Seq, UID: m.Cell.UID, Name: m.Cell.Name, Kind: m.Cell.Kind,
				CardID: m.CardID, Index: m.Index, Text: m.Text, At: m.Cell.At}}, g.Feed...)
			if len(g.Feed) > 30 {
				g.Feed = g.Feed[:30]
			}
		}
	case "card":
		if m.Card != nil {
			g.put(*m.Card)
		}
		s.common(m)
	case "dig": // v1 servers: the whole card
		if m.Seq > g.Seq {
			g.Seq = m.Seq
		}
		if m.Card != nil {
			g.put(*m.Card)
		}
		if m.NewCard != nil {
			g.put(*m.NewCard)
		}
		s.common(m)
		if m.Feed != nil {
			g.Feed = append([]FeedItem{*m.Feed}, g.Feed...)
		}
	case "drain":
		s.common(m)
	case "claim":
		if m.Stats != nil {
			g.Stats = *m.Stats
		}
	}
	return inSync
}

func (s *State) common(m Live) {
	if m.Left != nil {
		s.Game.Left = *m.Left
	}
	if m.Over != nil && *m.Over {
		s.Game.Over = true
	}
	if m.Stats != nil {
		s.Game.Stats = *m.Stats
	}
}

func (g *PublicGame) card(id int) *PublicCard {
	for i := range g.Cards {
		if g.Cards[i].ID == id {
			return &g.Cards[i]
		}
	}
	return nil
}

// Card returns the card with that id, or nil.
func (g *PublicGame) Card(id int) *PublicCard { return g.card(id) }

// put adds a card or replaces the one with the same id, never losing opened
// cells (an older push may carry fewer).
func (g *PublicGame) put(c PublicCard) {
	old := g.card(c.ID)
	if old == nil {
		g.Cards = append(g.Cards, c)
		return
	}
	for i, cell := range old.Cells {
		if cell != nil && i < len(c.Cells) && c.Cells[i] == nil {
			c.Cells[i] = cell
		}
	}
	*old = c
}

// Opened counts the dug cells of a card.
func (c PublicCard) Opened() int {
	n := 0
	for _, cell := range c.Cells {
		if cell != nil {
			n++
		}
	}
	return n
}
