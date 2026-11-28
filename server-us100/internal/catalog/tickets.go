package catalog

import (
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"strings"
)

// UpgradeTicket describes one PVF reinforcement/amplification/refinement ticket,
// grimoire, or purifier extracted from US100 stackable/*.stk.
type UpgradeTicket struct {
	Kind         string     `json:"k"`             // "reinforce", "amplify", "separate", "grimoire", "purify"
	Level        byte       `json:"lv,omitempty"`  // target level (fixed) or delta (increase)
	Rate         byte       `json:"r,omitempty"`   // success rate 1..100
	Mode         string     `json:"m,omitempty"`   // "fixed" or "increase"
	MaxLevel     int        `json:"max,omitempty"` // -1 = unlimited, otherwise max target level
	RandomValues [][2]int   `json:"rv,omitempty"`  // [amplification random value]: [level, weight] pairs
}

// TicketCatalog holds all upgrade/amplify/refinement tickets, grimoires, purifiers,
// and amplification stat constants extracted from the US100 PVF.
type TicketCatalog struct {
	Source            pvf.ArchiveSnapshot      `json:"source"`
	Tickets           map[uint32]UpgradeTicket `json:"tickets"`
	AmplifyConsts     []int                    `json:"amplify_consts,omitempty"`
	LevelWeight       float64                  `json:"level_weight,omitempty"`
	LevelConst        float64                  `json:"level_const,omitempty"`
	RarityWeights     map[int32]float64        `json:"rarity_weights,omitempty"`
	KindCounts        map[string]int           `json:"kind_counts,omitempty"`
}

// LoadTickets reads a generated tickets JSON catalog.
func LoadTickets(filePath string) (*TicketCatalog, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var c TicketCatalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Tickets == nil {
		c.Tickets = map[uint32]UpgradeTicket{}
	}
	return &c, nil
}

// SaveTickets writes a generated tickets JSON catalog.
func SaveTickets(filePath string, c *TicketCatalog) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, b, 0600)
}

// ComputeAmplifyValue calculates the Dimensional stat bonus (amplify_value) for
// an equipment item of given minLevel, rarity (0..7), and amplify upgrade level
// using US100 PVF etc/amplifyupgrade.etc ([amplification const]) and
// etc/amplifyitem.etc ([amplification weight by level], [rarity weight], [equip level const]).
func (c *TicketCatalog) ComputeAmplifyValue(minLevel, rarity int32, upgrade byte) uint16 {
	if minLevel <= 0 {
		minLevel = 100
	}
	levelConst := 55.0
	levelWeight := 0.01
	rarityWeight := 1.0
	if c != nil {
		if c.LevelConst > 0 {
			levelConst = c.LevelConst
		}
		if c.LevelWeight > 0 {
			levelWeight = c.LevelWeight
		}
		if w, ok := c.RarityWeights[rarity]; ok && w > 0 {
			rarityWeight = w
		}
	} else {
		switch rarity {
		case 3:
			rarityWeight = 1.30
		case 4:
			rarityWeight = 1.50
		case 5:
			rarityWeight = 1.10
		case 6:
			rarityWeight = 1.40
		case 7:
			rarityWeight = 1.60
		}
	}
	// Base purification/grimoire dimensional stat at +0 is at least 1,
	// scaling by level, rarity, and amplification const at +upgrade.
	base := float64(minLevel) * levelWeight * rarityWeight
	ampConst := 0.0
	if c != nil && int(upgrade) < len(c.AmplifyConsts) {
		ampConst = float64(c.AmplifyConsts[upgrade])
	} else {
		ampConst = defaultAmplifyConst(upgrade)
	}
	// US100 amplify stat formula: round(ampConst * (minLevel + levelConst) / 100 * rarityWeight) + base
	val := math.Round(ampConst*(float64(minLevel)+levelConst)/100.0*rarityWeight + base)
	if val < 1 {
		val = 1
	}
	if val > 65535 {
		val = 65535
	}
	return uint16(val)
}

func defaultAmplifyConst(upgrade byte) float64 {
	// Extracted from US100 etc/amplifyupgrade.etc [amplification const].
	table := []float64{
		0, 1, 2, 3, 4, 6, 8, 10, 22, 28,
		35, 42, 50, 67, 108, 150, 192, 267, 342, 417,
		500, 583, 667, 750, 833, 917, 1001, 1085, 1169, 1253,
	}
	if int(upgrade) < len(table) {
		return table[upgrade]
	}
	return table[len(table)-1]
}

// ImportTickets scans list/stackable.lst, etc/amplifyupgrade.etc, and
// etc/amplifyitem.etc from a US100 PVF archive.
func ImportTickets(a *pvf.Archive) (*TicketCatalog, error) {
	cat := &TicketCatalog{
		Source:        a.Snapshot(),
		Tickets:       map[uint32]UpgradeTicket{},
		RarityWeights: map[int32]float64{},
		KindCounts:    map[string]int{},
	}

	// 1. Parse etc/amplifyupgrade.etc for [amplification const].
	if ampUp, err := ResolveScript(a, "etc/amplifyupgrade.etc"); err == nil {
		if cells := sectionCells(ampUp.Cells, "[amplification const]"); len(cells) >= 4 {
			// Stored as 4 identical ints per upgrade level (0..N).
			for i := 0; i+3 < len(cells) && len(cat.AmplifyConsts) < 35; i += 4 {
				if cells[i].Type == 0 {
					cat.AmplifyConsts = append(cat.AmplifyConsts, int(cells[i].Value))
				}
			}
		}
	}

	// 2. Parse etc/amplifyitem.etc for weights and purify materials.
	rarityNameToID := map[string]int32{
		"common": 0, "uncommon": 1, "rare": 2, "unique": 3,
		"epic": 4, "chronicle": 5, "legendary": 6, "mythology": 7,
	}
	if ampItem, err := ResolveScript(a, "etc/amplifyitem.etc"); err == nil {
		if cells := sectionCells(ampItem.Cells, "[amplification weight by level]"); len(cells) > 0 {
			cat.LevelWeight = tokenFloat(cells[0])
		}
		if cells := sectionCells(ampItem.Cells, "[equip level const]"); len(cells) > 0 {
			cat.LevelConst = tokenFloat(cells[0])
		}
		if cells := sectionCells(ampItem.Cells, "[rarity weight]"); len(cells) > 0 {
			for i := 0; i+1 < len(cells); i += 2 {
				if id, ok := rarityNameToID[strings.ToLower(strings.TrimSpace(cells[i].Text))]; ok {
					cat.RarityWeights[id] = tokenFloat(cells[i+1])
				}
			}
		}
		// [purify material] etc. repeat per entry, so scan all cells in order.
		for i := 0; i+1 < len(ampItem.Cells); i++ {
			if ampItem.Cells[i].Type != 5 {
				continue
			}
			switch ampItem.Cells[i].Text {
			case "[purify material]", "[purify only material]", "[purify only cera material]", "[gold purify material]":
				if ampItem.Cells[i+1].Type == 0 && ampItem.Cells[i+1].Value > 0 {
					id := uint32(ampItem.Cells[i+1].Value)
					if _, exists := cat.Tickets[id]; !exists {
						cat.Tickets[id] = UpgradeTicket{Kind: "purify"}
						cat.KindCounts["purify"]++
					}
				}
			}
		}
	}

	// 3. Scan every stackable in list/stackable.lst.
	stkIdx, err := ResolveScript(a, "list/stackable.lst")
	if err != nil {
		return nil, fmt.Errorf("read list/stackable.lst: %w", err)
	}
	stkRows, err := ParseIndex(stkIdx.Cells)
	if err != nil {
		return nil, fmt.Errorf("parse list/stackable.lst: %w", err)
	}
	for _, r := range stkRows {
		p := r.Path
		if !strings.HasPrefix(strings.ToLower(p), "stackable/") {
			p = path.Join("stackable", p)
		}
		scr, err := ResolveScript(a, p)
		if err != nil {
			continue
		}
		if t, ok := parseUpgradeTicketCells(scr.Cells); ok {
			cat.Tickets[r.ID] = t
			cat.KindCounts[t.Kind]++
		}
	}
	return cat, nil
}

func tokenFloat(tok pvf.Token) float64 {
	if tok.Type == 2 {
		return float64(tok.Number)
	}
	if tok.Type == 0 {
		return float64(tok.Value)
	}
	return 0
}

func parseUpgradeTicketCells(cells []pvf.Token) (UpgradeTicket, bool) {
	if c := sectionCells(cells, "[equipment amplify reinforcement ticket]"); len(c) >= 2 {
		return parseTicketCells("amplify", c), true
	}
	if c := sectionCells(cells, "[equipment reinforcement ticket]"); len(c) >= 2 {
		return parseTicketCells("reinforce", c), true
	}
	if c := sectionCells(cells, "[equipment separate reinforcement ticket]"); len(c) >= 2 {
		return parseTicketCells("separate", c), true
	}
	if c := sectionCells(cells, "[amplification random value]"); len(c) >= 2 {
		t := UpgradeTicket{Kind: "grimoire"}
		for i := 0; i+1 < len(c); i += 2 {
			if c[i].Type == 0 && c[i+1].Type == 0 && c[i+1].Value > 0 {
				t.RandomValues = append(t.RandomValues, [2]int{int(c[i].Value), int(c[i+1].Value)})
			}
		}
		return t, true
	}
	for _, tok := range cells {
		if tok.Type == 5 && tok.Text == "[equipment purify]" {
			return UpgradeTicket{Kind: "purify"}, true
		}
	}
	return UpgradeTicket{}, false
}

func parseTicketCells(kind string, cells []pvf.Token) UpgradeTicket {
	t := UpgradeTicket{Kind: kind, Mode: "fixed", MaxLevel: -1}
	if len(cells) >= 1 && cells[0].Type == 0 && cells[0].Value > 0 {
		lv := cells[0].Value
		if lv > 255 {
			lv = 255
		}
		t.Level = byte(lv)
	}
	if len(cells) >= 2 && cells[1].Type == 0 && cells[1].Value > 0 {
		r := cells[1].Value
		if r > 100 {
			r = 100
		}
		t.Rate = byte(r)
	}
	if len(cells) >= 3 && cells[2].Text != "" {
		t.Mode = strings.ToLower(strings.TrimSpace(cells[2].Text))
	}
	if len(cells) >= 4 && cells[3].Type == 0 {
		t.MaxLevel = int(cells[3].Value)
	}
	return t
}
