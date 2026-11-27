package character

import (
	"dfolan/internal/game/protocol"
	"slices"
	"testing"
)

func TestLeapStyleIIShadow(t *testing.T) {
	type skill = protocol.LearnedSkill
	for _, tc := range []struct {
		first, second byte
		omit          bool
	}{
		{1, 0, true}, {2, 1, false}, {0, 0, false}, {0, 1, false},
	} {
		a := []skill{{ID: 175, Level: tc.first, Slot: 18}}
		b := []skill{{ID: 25, Level: 1, Slot: 15}, {ID: 175, Level: tc.second, Slot: 46}, {ID: 180, Slot: 47}}
		orig := slices.Clone(b)
		want := b
		if tc.omit {
			want = []skill{b[0], b[2]}
		}
		got := omitStyleIICommandShadows(a, b, func(id uint16) bool { return id == 175 })
		if !slices.Equal(got, want) || !slices.Equal(orig, b) {
			t.Fatalf("%+v: got %+v, original %+v", tc, got, b)
		}
	}
}
