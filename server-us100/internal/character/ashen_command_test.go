package character

import (
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"slices"
	"testing"
)

func TestAshenProjectionPreservesSlotsAndRanks(t *testing.T) {
	type row = protocol.LearnedSkill
	for _, rank := range []byte{0, 1, 3} {
		input := []row{{ID: 46, Slot: 23, Level: 1}, {ID: 16, Slot: 32, Level: rank}, {ID: 11, Slot: 21, Level: 1}, {ID: 17, Slot: 33, Level: 0}}
		original := slices.Clone(input)
		want := slices.Clone(input)
		if rank == 0 {
			want = slices.Delete(want, 1, 2)
		}
		got := omitUnlearnedAshen(input)
		if !slices.Equal(got, want) || !slices.Equal(input, original) {
			t.Fatal(rank, got, input)
		}
	}
}
func TestAshenAckScope(t *testing.T) {
	lc := &LearningCatalog{index: map[byte]map[uint16]LearningDefinition{0: {16: {Path: "skill/swordman/ashenfork.skl", SHA256: "031f5a7abdf406eb28f9d6782c748d80fd80ee32e7a9eaf1d91deec4cbd1c8be"}}}}
	cs := Service{Learning: lc}
	req := protocol.SkillPurchase{Entries: []protocol.SkillPurchaseEntry{{ID: 16, Delta: 1}}}
	for _, tc := range []struct {
		job, rank    byte
		custom, want bool
	}{{0, 1, false, true}, {0, 1, true, false}, {0, 2, false, false}, {1, 1, false, false}} {
		st := State{}
		st.LearnedSkills[0] = map[uint16]byte{16: tc.rank}
		if tc.custom {
			st.SkillCommands[0] = map[uint16][]byte{16: {3, 4}}
		}
		b, e := json.Marshal(st)
		if e != nil {
			t.Fatal(e)
		}
		r := storage.Character{Profession: tc.job, State: b}
		if cs.AshenFirstRankAckOnly(r, req) != tc.want {
			t.Fatal(tc)
		}
	}
	if cs.ashenCommandProfile(1) || (&Service{}).ashenCommandProfile(0) {
		t.Fatal("applied to unrelated source")
	}
}
