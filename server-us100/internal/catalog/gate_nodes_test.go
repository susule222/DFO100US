package catalog

import (
	"dfolan/internal/catalog/pvf"
	"testing"
)

func TestNodesAfterConditionalRows(t *testing.T) {
	tokens := []pvf.Token{{Type: 3, Text: "[dungeon]"}, {Value: 3066}, {Type: 3, Text: "[in progress]"}, {Value: 2358}, {Value: 70}, {Value: -1}, {Value: 11}, {Type: 3, Text: "[is clear quest]"}, {Value: 3167}, {Type: 3, Text: "[/dungeon]"}}
	n, e := gateNodes(tokens)
	if e != nil || len(n) != 3 || n[1].Dungeon != 70 || !n[1].Allows(nil) {
		t.Fatalf("%+v: %v", n, e)
	}
	s := map[uint16]string{2358: "accepted", 3167: "completed"}
	if n[0].Allows(nil) || n[2].Allows(nil) || !n[0].Allows(s) || !n[2].Allows(s) {
		t.Fatal("lost source quest predicate")
	}
}
