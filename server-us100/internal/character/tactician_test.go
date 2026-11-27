package character

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestTacticianSlayerEight(t *testing.T) {
	ctx := context.Background()
	cat, e := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	learn, e := LoadLearningCatalog("../../configs/us100/skills.generated.json", cat.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	store, e := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "fixture.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	prof := cat.Professions[0]
	raw, _ := json.Marshal(State{Level: 8, SourceSHA256: prof.RawSHA256, InitialSkills: prof.InitialSkills, SkillPoints: [2]uint16{100, 210}})
	role := storage.Character{ID: 39, AccountID: 41, Profession: 0, WireID: 1, ConfigVersion: cat.Source.Checksum, State: raw}
	svc := Service{Store: store, Catalog: cat, Learning: learn}
	setEnd := func(end int64) {
		t.Helper()
		if e := store.Database().Mutate(func(tx *db100.Tx) error { return tx.Put("us224_account_premium", "41", map[byte]int64{27: end}) }); e != nil {
			t.Fatal(e)
		}
	}
	if e = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "39", map[string]any{"id": 39, "account_id": 41, "profession": 0, "wire_id": 1, "config_version": cat.Source.Checksum, "state": json.RawMessage(raw)})
	}); e != nil {
		t.Fatal(e)
	}
	req := protocol.SkillPurchase{Entries: []protocol.SkillPurchaseEntry{{ID: 58, Delta: 1}, {ID: 65, Delta: 1}}}
	if _, _, e := svc.Learn(ctx, role, "no", req); e == nil {
		t.Fatal("without contract")
	}
	setEnd(time.Now().Unix() + 600)
	got, a, e := svc.Learn(ctx, role, "yes", req)
	if e != nil || !a {
		t.Fatal(a, e)
	}
	var st State
	json.Unmarshal(got.State, &st)
	if st.Level != 8 || st.SkillPoints[0] != 70 || st.LearnedSkills[0][58] != 1 || st.LearnedSkills[0][65] != 1 {
		t.Fatal(st)
	}
	if _, a, e = svc.Learn(ctx, got, "yes", req); e != nil || a {
		t.Fatal("duplicate", a, e)
	}
	if _, e = svc.EntrySkills(got); e != nil {
		t.Fatal(e)
	}
	setEnd(time.Now().Unix() - 1)
	got, e = svc.settleTactician(ctx, got)
	if e != nil {
		t.Fatal(e)
	}
	var after State
	json.Unmarshal(got.State, &after)
	if after.SkillPoints[0] != 100 || after.LearnedSkills[0][58] != 0 || after.LearnedSkills[0][65] != 0 || len(after.TacticianRanks) != 0 {
		t.Fatal(after)
	}
	again, e := svc.settleTactician(ctx, got)
	if e != nil || string(again.State) != string(got.State) {
		t.Fatal("double refund")
	}
}
