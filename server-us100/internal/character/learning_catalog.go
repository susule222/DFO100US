package character

import (
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"fmt"
	"os"
)

// LearningCatalog retains current PVF fields and provenance. No foreign-job
// skill IDs or reference-server SP table is merged into this projection.
type LearningDefinition struct {
	Job          byte
	ID           uint16
	Path, SHA256 string
	Fields       map[string][]pvf.Token
}
type LearningCatalog struct {
	Source pvf.ArchiveSnapshot  `json:"source"`
	Rows   []LearningDefinition `json:"rows"`
	index  map[byte]map[uint16]LearningDefinition
}

func LoadLearningCatalog(path string, source string) (*LearningCatalog, error) {
	p, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var c LearningCatalog
	if e = json.Unmarshal(p, &c); e != nil {
		return nil, e
	}
	if c.Source.Checksum != source || len(c.Rows) == 0 {
		return nil, fmt.Errorf("learning source mismatch")
	}
	c.index = map[byte]map[uint16]LearningDefinition{}
	for _, d := range c.Rows {
		if d.ID == 0 || len(d.SHA256) != 64 || d.Path == "" {
			return nil, fmt.Errorf("invalid source skill")
		}
		if c.index[d.Job] == nil {
			c.index[d.Job] = map[uint16]LearningDefinition{}
		}
		if _, ok := c.index[d.Job][d.ID]; ok {
			return nil, fmt.Errorf("duplicate job skill")
		}
		c.index[d.Job][d.ID] = d
	}
	return &c, nil
}
func (d LearningDefinition) Ints(tag string) []int {
	var out []int
	for _, t := range d.Fields[tag] {
		if t.Type != 0 {
			return nil
		}
		out = append(out, int(t.Value))
	}
	return out
}
func (d LearningDefinition) Active() bool {
	ts := d.Fields["[type]"]
	return len(ts) == 1 && ts[0].Text == "[active]"
}

// Explicit source profession eligibility for unlearned skill definitions.
// Existing .chr free grants do not depend on manual learning eligibility.
// In DNF PVF .skl files, [growtype maximum level] is the authoritative per-
// advancement rank cap (0 means ineligible, >0 means learnable), whereas
// [skill fitness growtype] only indicates subclass affinity/tab grouping (for
// example, Male Slayer Lv15 SpeedSlash ID 282 has cap=[60,0,0,0,0,60] with
// fitness=[5] so base Slayer adv=0 and Ghostblade adv=5 can both learn it).
func (d LearningDefinition) ForAdvancement(adv int) bool {
	if adv < 0 || adv > 5 {
		return false
	}
	t := d.Fields["[type]"]
	if len(t) != 1 || (t[0].Text != "[active]" && t[0].Text != "[passive]") {
		return false
	}
	if cap := d.Ints("[growtype maximum level]"); len(cap) > 0 {
		return adv < len(cap) && cap[adv] > 0
	}
	if grow := d.Ints("[skill fitness growtype]"); len(grow) > 0 {
		for _, g := range grow {
			if g == adv {
				return true
			}
		}
		return false
	}
	return adv >= 1
}
func (d LearningDefinition) rankNeed(rank int) int {
	req := d.Ints("[required level]")
	if len(req) != 1 {
		return 1 << 30
	}
	need := req[0]
	step := d.Ints("[required level range]")
	if rank > 1 && len(step) == 1 && step[0] > 0 {
		need += (rank - 1) * step[0]
	}
	return need
}
func (d LearningDefinition) Cost(level, advancement, target int, known map[uint16]byte) (int, error) {
	if advancement < 0 || advancement > 5 || target < 1 || target > 255 || !d.ForAdvancement(advancement) {
		return 0, fmt.Errorf("unsupported skill advancement/level")
	}
	required, max, cost := d.Ints("[required level]"), d.Ints("[maximum level]"), d.Ints("[purchase cost]")
	if len(required) != 1 || len(max) != 1 || len(cost) == 0 {
		return 0, fmt.Errorf("skill learning fields unavailable")
	}
	priceIdx := target - 1
	if priceIdx >= len(cost) {
		priceIdx = len(cost) - 1
	}
	price := cost[priceIdx]
	if price < 0 {
		return 0, fmt.Errorf("skill learning fields unavailable")
	}
	if len(d.Fields["[special purchase cost]"]) > 0 || len(d.Fields["[feature skill type]"]) > 0 {
		return 0, fmt.Errorf("technique skill learning pending")
	}
	limit := max[0]
	if caps := d.Ints("[growtype maximum level]"); len(caps) > advancement {
		if caps[advancement] < limit {
			limit = caps[advancement]
		}
	}
	need := d.rankNeed(target)
	if target > limit || level < need {
		return 0, fmt.Errorf("character level does not allow skill rank")
	}
	pre := d.Ints("[pre required skill]")
	if len(pre)%2 != 0 {
		return 0, fmt.Errorf("invalid skill prerequisite")
	}
	for i := 0; i < len(pre); i += 2 {
		if int(known[uint16(pre[i])]) < pre[i+1] {
			return 0, fmt.Errorf("skill prerequisite not learned")
		}
	}
	return price, nil
}
