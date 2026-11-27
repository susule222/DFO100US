package character

import (
	"bytes"
	"encoding/json"
	"testing"

	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
)

func TestKhazanEntryCommandProjection(t *testing.T) {
	c, err := catalog.LoadCharacters("../../configs/characters.next25.json")
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Catalog: c}
	for _, tc := range []struct {
		name string
		job  byte
		keys []byte
		omit bool
	}{
		{"native", 0, []byte{3, 8}, true}, {"changed", 0, []byte{1, 4}, false},
		{"other_job", 1, []byte{3, 8}, false}, {"unset", 0, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := State{Level: 7, SourceSHA256: c.Professions[tc.job].RawSHA256, InitialSkills: []int32{25, 1, 1, 175, 1, 1}}
			for i := range st.SkillCommands {
				st.SkillCommands[i] = map[uint16][]byte{25: tc.keys, 175: {1, 4}}
			}
			raw, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			role := storage.Character{ID: 906, AccountID: 113, Profession: tc.job, State: raw}
			var trees [2]protocol.SkillTree
			for i := range trees {
				rows, err := service.skillRows(role, st, i)
				if err != nil {
					t.Fatal(err)
				}
				cmds := protocol.SkillCommands{25: tc.keys, 175: {1, 4}}
				if tc.omit && i == 0 {
					delete(cmds, 25)
				}
				trees[i] = protocol.SkillTree{Skills: rows, Commands: cmds}
			}
			want, err := protocol.SkillInfoTrees(st.Level, trees)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]byte(nil), raw...)
			for i := 0; i < 2; i++ {
				got, err := service.EntrySkills(role)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, got) || !bytes.Equal(before, role.State) {
					t.Fatal("changed unrelated skill data or stored commands")
				}
			}
		})
	}
}
