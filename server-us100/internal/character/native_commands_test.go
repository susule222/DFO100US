package character

import (
	"bytes"
	"dfolan/internal/catalog"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestNativeCommandIdentity(t *testing.T) {
	for _, tc := range []struct {
		src  string
		keys []byte
		want bool
	}{
		{"(UP),(BUFF)", []byte{0, 8}, true}, {"(RIGHT),(BUFF)", []byte{3, 8}, true},
		{"(DOWN),(RIGHT),(SKILL)", []byte{1, 3, 4}, true}, {"(SKILL)", []byte{4}, true},
		{"(UP)&(SKILL)", []byte{0, 4}, false}, {"(UP),(BUFF)", []byte{1, 4}, false},
		{"(JUMP)", []byte{6}, false}, {"", []byte{8}, false}, {"(UP),(BUFF)", nil, false},
	} {
		if got := sameNativeCommand(tc.src, tc.keys); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestNativeShadowProjectionCurrentCatalog(t *testing.T) {
	c, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	l, err := LoadLearningCatalog("../../configs/us100/skills.generated.json", c.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	if l.Source.Checksum != nativeSource.Source {
		t.Fatal("regenerate native commands for this catalog")
	}
	s := Service{Catalog: c, Learning: l}
	type cell struct{ level, slot uint64 }
	tested := 0
	for job, p := range c.Professions {
		covered := map[uint16]bool{}
		page := func(st State) [2]map[uint16]cell {
			t.Helper()
			raw, e := json.Marshal(st)
			if e != nil {
				t.Fatal(e)
			}
			prior := bytes.Clone(raw)
			b, e := s.EntrySkills(storage.Character{Profession: job, State: raw, ConfigVersion: c.Source.Checksum})
			if e != nil {
				t.Fatal(job, e)
			}
			if !bytes.Equal(raw, prior) {
				t.Fatal("modified role")
			}
			ps := pbFields(t, b[4:])[2]
			if len(ps) != 2 {
				t.Fatal("missing style")
			}
			var out [2]map[uint16]cell
			for i, pb := range ps {
				out[i] = map[uint16]cell{}
				for _, item := range pbFields(t, pb)[3] {
					f := pbFields(t, item)
					id, _ := binary.Uvarint(f[2][0])
					rank, _ := binary.Uvarint(f[3][0])
					slot, _ := binary.Uvarint(f[1][0])
					out[i][uint16(id)] = cell{rank, slot}
				}
			}
			return out
		}
		maxAdv := 0
		for _, d := range l.index[job] {
			for _, g := range d.Ints("[skill fitness growtype]") {
				if g > maxAdv {
					maxAdv = g
				}
			}
			if g := len(d.Ints("[growtype maximum level]")) - 1; g > maxAdv {
				maxAdv = g
			}
		}
		if maxAdv > 255 {
			t.Fatal("unexpected advancement")
		}
		for adv := 0; adv <= maxAdv; adv++ {
			st := State{Level: 100, Advancement: byte(adv), SourceSHA256: p.RawSHA256}
			base := page(st)
			st.LearnedSkills[0] = map[uint16]byte{}
			for id := range base[1] {
				st.LearnedSkills[0][id] = 1
			}
			got := page(st)
			for id, old := range base[1] {
				row, ok := got[1][id]
				if got[0][id].level != 1 {
					t.Fatal("lost learned skill", job, adv, id)
				}
				if s.nativeCommand(job, id) != "" {
					covered[id] = true
					if ok {
						t.Fatal("shadow retained", job, id)
					}
				} else if !ok || old != row {
					t.Fatal("unrelated placeholder moved", job, id)
				}
			}
			st.LearnedSkills[1] = st.LearnedSkills[0]
			both := page(st)
			for id := range st.LearnedSkills[1] {
				if both[1][id].level != 1 {
					t.Fatal("lost learned style II", job, id)
				}
			}
		}
		if len(covered) == 0 {
			t.Fatal("no command coverage for job", job)
		}
		tested += len(covered)
	}
	t.Logf("jobs=%d distinct eligible job/skill rows exercised over advancements=%d", len(c.Professions), tested)
}
