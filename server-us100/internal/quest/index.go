package quest

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/storage"
	"sort"
)

// CharacterFilter represents one (job, growType, awakeningStep) 3-tuple from
// [target character] or [non target character].
type CharacterFilter struct {
	Job       string
	Grow      int32
	Awakening int32
}

// Entry holds one quest's static, source-derived properties. Everything here
// is decided by the imported catalog alone; nothing depends on a character.
type Entry struct {
	ID                  uint32
	Kind                string
	Model               string
	Initial             uint32
	Implemented         bool
	RewardUsable        bool
	Unverified          string
	Links               storage.QuestLinks
	GrowUsable          bool
	MinimumLevel        uint32
	MaximumLevel        uint32
	Jobs                []string
	Prerequisites       []uint32
	PrerequisiteGroups  [][]uint32
	GrowTypes           []int32
	TargetCharacters    []CharacterFilter
	NonTargetCharacters []CharacterFilter
	PreAnswerQuest      uint32
	PreAnswerChoice     int32
	NPC                 uint32
	Range               RangeObjective
	Seek                SeekObjective
}

// These source sections require state/effects that the quest transaction
// does not own yet. Never treat an unrelated NPC conversation as proof.
func unverifiedSection(d catalog.QuestDefinition) string {
	for _, name := range []string{"[creature kind]", "[creature level]", "[exception filter]", "[account quest]", "[synchronization account quest]", "[exception quest]", "[check skill on clear]", "[fame value]", "[send postal]", "[depend give item]", "[multi condition min clear]", "[multi condition direct check]"} {
		for _, t := range d.Script.Cells {
			if t.Type == 3 && t.Text == name {
				return name
			}
		}
	}
	return ""
}

// Index precomputes those properties once per catalog load. The hot paths —
// the available list, a run's map-clear settlement and the world proximity
// check — otherwise re-walk all 2844 quests and re-scan each quest's own
// script cells on every level-up, clear and submit.
type Index struct {
	Source            string
	Ordered           []uint32
	Entries           map[uint32]*Entry
	ByClearMap        map[uint32][]uint16
	ByDungeon         map[uint32][]uint16
	ByMonster         map[uint32][]uint16
	ByWildcardDungeon map[uint32][]uint16
	ByEnemyDungeon    map[uint32][]uint16
	ByEnemyMonster    map[uint32][]uint16
	Positional        []uint32
}

// rewardUsable reports whether Finish can settle this quest's reward today.
// Quests it rejects must also stay out of the available list: a character who
// accepts one can never submit it, which reads in game as a quest that will
// not complete no matter how often it is handed in.
func rewardUsable(d catalog.QuestDefinition) bool {
	if t := cells(d.Script.Cells, "[reward type]"); len(t) > 0 {
		if len(t) == 1 && t[0].Text == "[percent exp]" {
			return len(d.RewardCells) == 1 && d.RewardCells[0].Type == 0 && d.RewardCells[0].Value > 0 && d.RewardCells[0].Value <= 100 && len(cells(d.Script.Cells, "[reward selection int data]")) == 0
		}
		if len(t) == 1 && t[0].Text == "[awakening type]" {
			return len(d.RewardCells) == 1 && d.RewardCells[0].Type == 0 && d.RewardCells[0].Value > 0 && d.RewardCells[0].Value <= 3 && len(cells(d.Script.Cells, "[reward selection int data]")) == 0
		}
		if len(t) != 1 || t[0].Type != 6 || (t[0].Text != "[item]" && t[0].Text != "[none]") {
			return false
		}
	}
	return len(cells(d.Script.Cells, "[reward select int data]")) == 0
}

func parseCharacterFilters(tokens []pvf.Token) ([]CharacterFilter, bool) {
	if len(tokens) == 0 {
		return nil, true
	}
	if len(tokens)%3 != 0 {
		return nil, false
	}
	out := make([]CharacterFilter, 0, len(tokens)/3)
	for i := 0; i < len(tokens); i += 3 {
		if tokens[i].Type != 6 || tokens[i].Text == "" || tokens[i+1].Type != 0 || tokens[i+2].Type != 0 {
			return nil, false
		}
		out = append(out, CharacterFilter{
			Job:       tokens[i].Text,
			Grow:      tokens[i+1].Value,
			Awakening: tokens[i+2].Value,
		})
	}
	return out, true
}

func parsePrerequisiteGroups(d catalog.QuestDefinition) [][]uint32 {
	var groups [][]uint32
	var cur []uint32
	inBlock := false
	for _, t := range d.Script.Cells {
		if t.Type == 3 {
			switch t.Text {
			case "[pre required quest]":
				cur = nil
				inBlock = true
			case "[/pre required quest]":
				if inBlock && len(cur) > 0 {
					groups = append(groups, cur)
				}
				cur = nil
				inBlock = false
			default:
				if inBlock {
					if len(cur) > 0 {
						groups = append(groups, cur)
					}
					cur = nil
					inBlock = false
				}
			}
			continue
		}
		if inBlock && t.Type == 0 && t.Value > 0 {
			cur = append(cur, uint32(t.Value))
		}
	}
	if inBlock && len(cur) > 0 {
		groups = append(groups, cur)
	}
	if len(groups) == 0 && len(d.Prerequisites) > 0 {
		groups = [][]uint32{d.Prerequisites}
	}
	return groups
}

func appendUniqueU16(s []uint16, v uint16) []uint16 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func BuildIndex(c catalog.QuestCatalog) *Index {
	x := &Index{
		Source:            c.Source.Checksum,
		Entries:           map[uint32]*Entry{},
		ByClearMap:        map[uint32][]uint16{},
		ByDungeon:         make(map[uint32][]uint16),
		ByMonster:         make(map[uint32][]uint16),
		ByWildcardDungeon: make(map[uint32][]uint16),
		ByEnemyDungeon:    make(map[uint32][]uint16),
		ByEnemyMonster:    make(map[uint32][]uint16),
	}
	for id, d := range c.Quests {
		initial, model, err := InitialProgress(d)
		prereqGroups := parsePrerequisiteGroups(d)
		e := &Entry{
			ID: id, Kind: d.Kind, Model: model, Initial: initial, Implemented: err == nil,
			RewardUsable: rewardUsable(d), GrowUsable: true,
			Unverified:         unverifiedSection(d),
			MinimumLevel:       d.MinimumLevel,
			MaximumLevel:       d.MaximumLevel,
			Jobs:               d.Jobs,
			Prerequisites:      d.Prerequisites,
			PrerequisiteGroups: prereqGroups,
		}
		if len(prereqGroups) > 1 {
			e.Links.PrerequisiteGroups = prereqGroups
		}
		for _, g := range cells(d.Script.Cells, "[grow type]") {
			if g.Type != 0 {
				e.GrowUsable = false
				break
			}
			e.GrowTypes = append(e.GrowTypes, g.Value)
		}
		if tc := cells(d.Script.Cells, "[target character]"); len(tc) > 0 {
			parsed, ok := parseCharacterFilters(tc)
			if !ok {
				e.Unverified = "[target character]"
			} else {
				e.TargetCharacters = parsed
			}
		}
		if ntc := cells(d.Script.Cells, "[non target character]"); len(ntc) > 0 {
			parsed, ok := parseCharacterFilters(ntc)
			if !ok {
				e.Unverified = "[non target character]"
			} else {
				e.NonTargetCharacters = parsed
			}
		}
		if pqa := cells(d.Script.Cells, "[pre required quest answer]"); len(pqa) > 0 {
			if len(pqa) == 2 && pqa[0].Type == 0 && pqa[0].Value > 0 && pqa[1].Type == 0 && pqa[1].Value >= 0 {
				e.PreAnswerQuest = uint32(pqa[0].Value)
				e.PreAnswerChoice = pqa[1].Value
			} else {
				e.Unverified = "[pre required quest answer]"
			}
		}
		for _, v := range cells(d.Script.Cells, "[collision accept quest]") {
			if v.Type != 0 || v.Value <= 0 || v.Value >= 40000 {
				e.Unverified = "[collision accept quest]"
				break
			}
			// Missing catalog IDs (e.g. removed quest 12497 on epic 3155/3156) can
			// never be accepted and must not block the main story chain.
			if _, ok := c.Quests[uint32(v.Value)]; ok {
				e.Links.AcceptConflicts = append(e.Links.AcceptConflicts, uint16(v.Value))
			}
		}
		for _, tag := range []string{"[main quest]", "[collision quest]"} {
			data := cells(d.Script.Cells, tag)
			for _, v := range data {
				_, ok := c.Quests[uint32(v.Value)]
				if v.Type != 0 || v.Value <= 0 || v.Value >= 40000 || (tag == "[main quest]" && (!ok || len(data) != 1)) {
					e.Unverified = tag
					break
				}
				if tag == "[main quest]" {
					e.Links.Parent = uint16(v.Value)
				} else if ok {
					// Ignore removed/absent collision quest IDs (e.g. 12463/12498 on Lv.50
					// awakening epics) because they can never be accepted or completed.
					e.Links.Conflicts = append(e.Links.Conflicts, uint16(v.Value))
				}
			}
		}
		switch {
		case !e.Implemented:
		case model == HuntMonster && id > 0 && id < 40000:
			for i := 0; i < len(d.ObjectiveCells); i += 4 {
				dun := uint32(d.ObjectiveCells[i].Value)
				mob := d.ObjectiveCells[i+2].Value
				if mob > 0 {
					x.ByMonster[uint32(mob)] = appendUniqueU16(x.ByMonster[uint32(mob)], uint16(id))
				} else {
					x.ByWildcardDungeon[dun] = appendUniqueU16(x.ByWildcardDungeon[dun], uint16(id))
				}
			}
		case model == HuntEnemy && id > 0 && id < 40000:
			for i := 0; i < len(d.ObjectiveCells); i += 5 {
				dun := uint32(d.ObjectiveCells[i].Value)
				target := uint32(d.ObjectiveCells[i+2].Value)
				x.ByEnemyDungeon[dun] = appendUniqueU16(x.ByEnemyDungeon[dun], uint16(id))
				x.ByEnemyMonster[target] = appendUniqueU16(x.ByEnemyMonster[target], uint16(id))
			}
		case model == ClearDungeon && id > 0 && id < 40000:
			n := uint32(d.ObjectiveCells[0].Value)
			x.ByDungeon[n] = appendUniqueU16(x.ByDungeon[n], uint16(id))
		case model == SingleClearMap:
			m := uint32(d.ObjectiveCells[0].Value)
			if id < 65535 && id != 0 {
				x.ByClearMap[m] = appendUniqueU16(x.ByClearMap[m], uint16(id))
			}
		case model == SingleMeetNPC:
			e.NPC = uint32(d.ObjectiveCells[0].Value)
			x.Positional = append(x.Positional, id)
		case model == SingleReachRange:
			e.Range, _ = ReachRange(d)
			x.Positional = append(x.Positional, id)
		case model == SeekAndMeetNPC:
			e.Seek, _ = SeekMeet(d)
			e.NPC = e.Seek.NPC
			x.Positional = append(x.Positional, id)
		}
		x.Entries[id] = e
		x.Ordered = append(x.Ordered, id)
	}
	sort.Slice(x.Ordered, func(i, j int) bool { return x.Ordered[i] < x.Ordered[j] })
	sort.Slice(x.Positional, func(i, j int) bool { return x.Positional[i] < x.Positional[j] })
	for m := range x.ByClearMap {
		sort.Slice(x.ByClearMap[m], func(i, j int) bool { return x.ByClearMap[m][i] < x.ByClearMap[m][j] })
	}
	return x
}

// Index returns the service's lazily built quest index.
func (s *Service) Index() *Index {
	if s.index == nil || s.index.Source != s.Catalog.Source.Checksum {
		s.index = BuildIndex(s.Catalog)
	}
	return s.index
}
