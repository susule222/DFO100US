package main

import (
	"bytes"
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/storage"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func skillReplayFixture(t *testing.T) (*character.Service, *worldSession) {
	t.Helper()
	const dir = "../../configs/us100/"
	c, e := catalog.LoadCharacters(dir + "characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	l, e := character.LoadLearningCatalog(dir+"skills.generated.json", c.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	store, e := storage.Open(context.Background(), storage.Config{DBPath: filepath.Join(t.TempDir(), "role.db")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	p := c.Professions[0]
	state := character.State{Level: 5, SourceSHA256: p.RawSHA256, InitialSkills: p.InitialSkills, SkillPoints: [2]uint16{120, 120}}
	e = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "1", map[string]any{"id": 1, "wire_id": 1, "account_id": 1, "profession": 0, "config_version": c.Source.Checksum, "state": state})
	})
	if e != nil {
		t.Fatal(e)
	}
	cs := character.Service{Store: store, Catalog: c, Learning: l}
	role, e := store.LiveCharacter(1, 1)
	if e != nil {
		t.Fatal(e)
	}
	w := worldSession{role: role}
	return &cs, &w
}

func TestUS100FiveSkillPurchaseReplay(t *testing.T) {
	cs, w := skillReplayFixture(t)
	store := cs.Store
	payload, _ := hex.DecodeString("00052e0000010500000119000001100000010b00000100c1d56e890000000000")
	var sess skillSession
	for i := 0; i < 2; i++ {
		plan, e := sess.handle(cs, w, 29, payload, payload)
		if e != nil {
			t.Fatal(e)
		}
		if len(plan) != 2 || plan[0].ID != 29 || plan[1].ID != 19 || plan[0].Payload[0] != 1 {
			t.Fatal("missing success or skill-tree refresh")
		}
		row, e := store.LiveCharacter(1, 1)
		if e != nil {
			t.Fatal(e)
		}
		var got character.State
		if e = json.Unmarshal(row.State, &got); e != nil {
			t.Fatal(e)
		}
		if got.SkillPoints != [2]uint16{40, 120} {
			t.Fatalf("points after request %d: %v", i, got.SkillPoints)
		}
		for id, rank := range map[uint16]byte{46: 2, 5: 2, 25: 1, 16: 1, 11: 1} {
			if got.LearnedSkills[0][id] != rank {
				t.Fatalf("skill %d: %d, want %d", id, got.LearnedSkills[0][id], rank)
			}
		}
	}
}

func TestUS100DragOutAndRestore(t *testing.T) {
	cs, w := skillReplayFixture(t)
	var sess skillSession
	buy, _ := hex.DecodeString("00052e0000010500000119000001100000010b00000100c1d56e890000000000")
	if _, err := sess.handle(cs, w, 29, buy, buy); err != nil {
		t.Fatal(err)
	}
	read := func() (storage.Character, character.State) {
		t.Helper()
		row, e := cs.Store.LiveCharacter(1, 1)
		if e != nil {
			t.Fatal(e)
		}
		var s character.State
		if e = json.Unmarshal(row.State, &s); e != nil {
			t.Fatal(e)
		}
		return row, s
	}
	_, before := read()
	id := uint16(169)
	if slot, ok := before.SkillSlots[0][id]; !ok || slot != 0 {
		t.Fatal("fixture does not match source shortcut")
	}
	out, _ := hex.DecodeString("000012ffffffff00992ea16100000000")
	for i := 0; i < 2; i++ {
		plan, e := sess.handle(cs, w, 28, out, out)
		if e != nil {
			t.Fatal(e)
		}
		row, s := read()
		if s.SkillSlots[0][id] != 18 || !reflect.DeepEqual(before.LearnedSkills, s.LearnedSkills) || !reflect.DeepEqual(before.InitialSkills, s.InitialSkills) || before.SkillPoints != s.SkillPoints {
			t.Fatal("skill ranks/points changed or removal not persisted")
		}
		for _, slot := range s.SkillSlots[0] {
			if slot == 0 {
				t.Fatal("quickbar slot still occupied")
			}
		}
		relogin, e := cs.EntrySkills(row)
		if e != nil || len(plan) != 2 || !bytes.Equal(plan[0].Payload, []byte{1, 0, 0, 18}) || plan[1].ID != 19 || !bytes.Equal(plan[1].Payload, relogin) {
			t.Fatal("drag/relogin projections differ")
		}
	}
	// The existing direct-writer fixture format also remains supported.
	back := []byte{0, 18, 0, 255, 255, 255, 255}
	if _, e := sess.handle(cs, w, 28, back, back); e != nil {
		t.Fatal(e)
	}
	_, restored := read()
	if !reflect.DeepEqual(restored, before) {
		t.Fatal("dragging the skill back did not restore its layout")
	}
}

func TestUS100BaseSlayerSpeedSlashAndNormalWavePurchaseReplay(t *testing.T) {
	cs, w := skillReplayFixture(t)
	// Raise fixture character to Lv15 with 70 SP (matches live role 48 capture).
	var st character.State
	if err := json.Unmarshal(w.role.State, &st); err != nil {
		t.Fatal(err)
	}
	st.Level = 15
	st.SkillPoints = [2]uint16{70, 570}
	rawState, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "1", map[string]any{
			"id": 1, "wire_id": 1, "account_id": 1, "profession": 0,
			"config_version": w.role.ConfigVersion, "state": json.RawMessage(rawState),
		})
	}); err != nil {
		t.Fatal(err)
	}
	w.role.State = rawState

	// Exact live captured CMD 29 frame: learn SpeedSlash (282, +1) + NormalWave (20, +1).
	payload, _ := hex.DecodeString("00021a01000114000001009578a86c00")
	var sess skillSession
	plan, err := sess.handle(cs, w, 29, payload, payload)
	if err != nil {
		t.Fatalf("live SpeedSlash+NormalWave purchase rejected: %v", err)
	}
	if len(plan) != 2 || plan[0].ID != 29 || plan[0].Payload[0] != 1 || plan[1].ID != 19 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	row, err := cs.Store.LiveCharacter(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got character.State
	if err := json.Unmarshal(row.State, &got); err != nil {
		t.Fatal(err)
	}
	if got.LearnedSkills[0][282] != 1 || got.LearnedSkills[0][20] != 1 || got.SkillPoints[0] != 35 {
		t.Fatalf("unexpected learned state after purchase: skills=%v sp=%v", got.LearnedSkills[0], got.SkillPoints)
	}
}
