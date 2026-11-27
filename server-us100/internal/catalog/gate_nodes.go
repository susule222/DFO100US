package catalog

import (
	"dfolan/internal/catalog/pvf"
	"fmt"
)

type GateNode struct {
	Dungeon   uint32
	Quest     int32
	Condition string
}

// The primary page is a sequence of dungeon, [optional predicate], argument.
// A conditional row does not terminate the following unconditional rows.
// Extra map pages have their own mode selector: do not accidentally merge them.
func gateNodes(tokens []pvf.Token) ([]GateNode, error) {
	var nodes []GateNode
	for i := 0; i < len(tokens); i++ {
		if tokens[i].Type != 3 || tokens[i].Text != "[dungeon]" {
			continue
		}
		// Dedicated mode pages such as Noblesse Code list only large dungeon
		// IDs, with no ordinary admission predicate. Keep every ID for audit;
		// these pages must be entered via their mode controller, never solo.
		end := i + 1
		for end < len(tokens) && tokens[end].Type == 0 && tokens[end].Value >= 40000 {
			end++
		}
		if end > i+1 && end < len(tokens) && tokens[end].Type == 3 && tokens[end].Text == "[/dungeon]" {
			for _, v := range tokens[i+1 : end] {
				nodes = append(nodes, GateNode{Dungeon: uint32(v.Value), Condition: "mode-specific"})
			}
			return nodes, nil
		}
		for i++; i < len(tokens); {
			if tokens[i].Type == 3 && tokens[i].Text == "[/dungeon]" {
				return nodes, nil
			}
			v := tokens[i]
			if v.Type != 0 || v.Value <= 0 {
				return nil, fmt.Errorf("invalid dungeon row at %d", i)
			}
			i++
			if i == len(tokens) {
				break
			}
			kind := ""
			if tokens[i].Type == 3 {
				kind = tokens[i].Text
				i++
			}
			if i == len(tokens) || tokens[i].Type != 0 {
				return nil, fmt.Errorf("invalid gate predicate at %d", i)
			}
			nodes = append(nodes, GateNode{uint32(v.Value), tokens[i].Value, kind})
			i++
		}
		return nil, fmt.Errorf("unterminated dungeon block")
	}
	return nil, nil
}

func SourceGateNodes(a *pvf.Archive) (map[uint32][]GateNode, map[uint32]string, error) {
	s, e := ReadScript(a, "list/worldmap.lst")
	if e != nil {
		return nil, nil, e
	}
	rows, e := ParseIndex(s.Cells)
	if e != nil {
		return nil, nil, e
	}
	gates := map[uint32][]GateNode{}
	pending := map[uint32]string{}
	for _, r := range rows {
		s, e := ResolveScript(a, r.Path)
		if e != nil {
			pending[r.ID] = e.Error()
			continue
		}
		nodes, e := gateNodes(s.Cells)
		if e != nil {
			pending[r.ID] = e.Error()
			continue
		}
		if len(nodes) > 0 {
			gates[r.ID] = nodes
		}
	}
	return gates, pending, nil
}

func (n GateNode) Allows(status map[uint16]string) bool {
	if n.Condition == "" && n.Quest == -1 {
		return true
	}
	if n.Quest <= 0 || n.Quest >= 40000 {
		return false
	}
	switch n.Condition {
	case "[in progress]":
		return status[uint16(n.Quest)] == "accepted"
	case "[is clear quest]", "":
		return status[uint16(n.Quest)] == "completed"
	}
	return false
}
