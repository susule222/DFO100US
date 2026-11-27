package character

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Exercise the actual purchase and expiry paths for every profession in the paired US catalog.
// Each growth index is a PVF column, not necessarily a separately playable subclass.
func TestUS100TacticianAcrossProfessions(t *testing.T) {
	cat, e := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	tree, e := LoadLearningCatalog("../../configs/us100/skills.generated.json", cat.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	if len(cat.Professions) != len(tree.index) {
		t.Fatal("profession/skill catalogs differ")
	}
	for job, prof := range cat.Professions {
		rows := tree.index[job]
		if len(rows) == 0 {
			t.Fatalf("empty profession %d", job)
		}
		for growth := 0; growth <= 5; growth++ {
			init := State{Advancement: byte(growth), SourceSHA256: prof.RawSHA256, InitialSkills: prof.InitialSkills}
			known, e := knownSkills(init, 0)
			if e != nil {
				t.Fatal(e)
			}
			var ids []int
			for id, d := range rows {
				price, cap, req := d.Ints("[purchase cost]"), d.Ints("[maximum level]"), d.Ints("[required level]")
				if known[id] > 0 || !d.ForAdvancement(growth) || len(price) != 1 || price[0] <= 0 || price[0] > 1000 || len(req) != 1 || req[0] < 7 || req[0] > 110 || len(cap) != 1 || cap[0] < 1 || len(d.Fields["[special purchase cost]"])+len(d.Fields["[feature skill type]"]) != 0 {
					continue
				}
				pre := d.Ints("[pre required skill]")
				met := len(pre)%2 == 0
				for k := 0; met && k < len(pre); k += 2 {
					met = int(known[uint16(pre[k])]) >= pre[k+1]
				}
				if met {
					ids = append(ids, int(id))
				}
			}
			if len(ids) == 0 {
				t.Fatalf("no qualifying ordinary SP sample for job=%d growth=%d", job, growth)
			}
			sort.Slice(ids, func(i, j int) bool {
				a, b := rows[uint16(ids[i])], rows[uint16(ids[j])]
				if growth != 0 && a.ForAdvancement(0) != b.ForAdvancement(0) {
					return !a.ForAdvancement(0)
				}
				if a.rankNeed(1) != b.rankNeed(1) {
					return a.rankNeed(1) < b.rankNeed(1)
				}
				return ids[i] < ids[j]
			})
			id := uint16(ids[0])
			d := rows[id]
			price := d.Ints("[purchase cost]")[0]
			required := d.Ints("[required level]")[0]
			t.Run(fmt.Sprintf("job-%02d/growth-%d/skill-%d", job, growth, id), func(t *testing.T) {
				ctx := context.Background()
				store, e := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "test.db")})
				if e != nil {
					t.Fatal(e)
				}
				defer store.Close()
				svc := Service{Store: store, Catalog: cat, Learning: tree}
				write := func(level, sp int, active bool) storage.Character {
					t.Helper()
					st := init
					st.Level = byte(level)
					st.SkillPoints[0] = uint16(sp)
					raw, e := json.Marshal(st)
					if e != nil {
						t.Fatal(e)
					}
					end := time.Now().Unix() - 1
					if active {
						end += 1000
					}
					if e = store.Database().Mutate(func(tx *db100.Tx) error {
						if e := tx.Put("us224_account_premium", "1", map[byte]int64{27: end}); e != nil {
							return e
						}
						return tx.Put("characters", "1", map[string]any{"id": 1, "account_id": 1, "wire_id": 1, "profession": job, "config_version": cat.Source.Checksum, "state": json.RawMessage(raw)})
					}); e != nil {
						t.Fatal(e)
					}
					return storage.Character{ID: 1, AccountID: 1, WireID: 1, Profession: job, ConfigVersion: cat.Source.Checksum, State: raw}
				}
				req := protocol.SkillPurchase{Entries: []protocol.SkillPurchaseEntry{{ID: id, Delta: 1}}}
				for _, v := range []struct {
					level, sp int
					active    bool
					reason    string
				}{{required - 5, price, false, "character level does not allow skill rank"}, {required - 6, price, true, "character level does not allow skill rank"}, {required - 5, price - 1, true, "not enough SP"}} {
					role := write(v.level, v.sp, v.active)
					_, _, e := svc.Learn(ctx, role, "denied", req)
					if e == nil || e.Error() != v.reason {
						t.Fatalf("expected %s, got %v", v.reason, e)
					}
				}
				role := write(required-5, price, true)
				got, ap, e := svc.Learn(ctx, role, "approved", req)
				if e != nil || !ap {
					t.Fatalf("eligible purchase: %v", e)
				}
				var st State
				if e = json.Unmarshal(got.State, &st); e != nil || int(st.Level) != required-5 || st.SkillPoints[0] != 0 || st.LearnedSkills[0][id] != 1 || st.TacticianRanks[id][1] != price {
					t.Fatal("paid state", e)
				}
				if _, ap, e = svc.Learn(ctx, got, "approved", req); e != nil || ap {
					t.Fatal("duplicate charge", e)
				}
				if _, e = svc.EntrySkills(got); e != nil {
					t.Fatal("entry restore:", e)
				}
				if e = store.Database().Mutate(func(tx *db100.Tx) error { return tx.Put("us224_account_premium", "1", map[byte]int64{}) }); e != nil {
					t.Fatal(e)
				}
				got, e = svc.settleTactician(ctx, got)
				if e != nil {
					t.Fatal(e)
				}
				var after State
				if e = json.Unmarshal(got.State, &after); e != nil || after.SkillPoints[0] != uint16(price) || after.LearnedSkills[0][id] != 0 || len(after.TacticianRanks) != 0 {
					t.Fatal("expiry settlement", e)
				}
			})
		}
	}
}

// Contract level cannot replace advancement or a missing prerequisite.
func TestUS100SkillContractPreservesRestrictions(t *testing.T) {
	cat, e := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	tree, e := LoadLearningCatalog("../../configs/us100/skills.generated.json", cat.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	wrongAdv, missingPre := 0, 0
	for _, d := range tree.Rows {
		for adv := 0; adv <= 5; adv++ {
			req := d.Ints("[required level]")
			if len(req) != 1 || req[0] < 6 {
				continue
			}
			pre := d.Ints("[pre required skill]")
			if len(pre)%2 != 0 {
				continue
			}
			known := map[uint16]byte{}
			for i := 0; i < len(pre); i += 2 {
				known[uint16(pre[i])] = byte(pre[i+1])
			}
			if _, ok := known[0]; ok {
				continue
			}
			if _, err := d.Cost(req[0], adv, 1, known); err != nil {
				continue
			}
			for wrong := 0; wrong <= 5; wrong++ {
				if !d.ForAdvancement(wrong) {
					if _, err := d.Cost(req[0], wrong, 1, known); err == nil {
						t.Fatalf("job %d skill %d wrong advancement", d.Job, d.ID)
					}
					wrongAdv++
					break
				}
			}
			if len(pre) > 0 && pre[1] > 0 {
				delete(known, uint16(pre[0]))
				if _, err := d.Cost(req[0], adv, 1, known); err == nil || err.Error() != "skill prerequisite not learned" {
					t.Fatalf("job %d skill %d missing prerequisite: %v", d.Job, d.ID, err)
				}
				missingPre++
			}
		}
	}
	if wrongAdv == 0 || missingPre == 0 {
		t.Fatal("empty restriction tests")
	}
	t.Logf("ineligible growth and missing-prerequisite checks: %d, %d", wrongAdv, missingPre)
}
