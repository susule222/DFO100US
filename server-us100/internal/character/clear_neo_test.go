package character

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/progression"
	"dfolan/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func completedTestRun(t *testing.T, runID string, started time.Time) *dungeon.Session {
	t.Helper()
	boss := protocol.DungeonMonster{Entity: 4096, Level: 5, Template: 1001, Rank: 3, Team: 100}
	room := catalog.DungeonRoom{X: 0, Y: 0, Map: 9001, Boss: true}
	run := &dungeon.Session{
		RunID:     runID,
		StartedAt: started,
		Definition: catalog.DungeonDefinition{
			ID:         3,
			BasisLevel: 5,
		},
		Maze: catalog.DungeonMaze{
			Start: [2]byte{0, 0},
			Boss:  [2]byte{0, 0},
			Rooms: []catalog.DungeonRoom{room},
		},
		Room:     room,
		Monsters: []protocol.DungeonMonster{boss},
		Loaded:   true,
		Dead:     map[uint16]bool{},
		Visited:  map[uint32][]protocol.DungeonMonster{9001: {boss}},
	}
	if err := run.BossCheck(protocol.BossCheckRequest{Actor: 1, Target: boss.Entity}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := run.ConfirmDeath(uint32(boss.Entity), 1, 1); err != nil {
		t.Fatal(err)
	}
	if !run.Completed() {
		t.Fatal("test run did not complete")
	}
	return run
}

func TestClearAwardsNeoAndGrowthContractExpBonuses(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "clear_neo.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	chars, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	progCat, err := catalog.LoadProgression("../../configs/us100/progression.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := progression.LoadRules("../../configs/us100/rules.experience.json")
	if err != nil {
		t.Fatal(err)
	}
	svc := &ProgressionService{
		Store:       store,
		Catalog:     progCat,
		Professions: chars,
		Rules:       rules,
	}

	prof := chars.Professions[0]
	makeChar := func(id, account int64) storage.Character {
		t.Helper()
		attrs := map[string]float32{}
		for k, v := range prof.BaseGrowth {
			attrs[k] = v
		}
		state, err := json.Marshal(State{
			Level:        1,
			SourceSHA256: prof.RawSHA256,
			Attributes:   attrs,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Database().Mutate(func(tx *db100.Tx) error {
			return tx.Put("characters", idKeyForTest(id), map[string]any{
				"id":             id,
				"account_id":     account,
				"profession":     0,
				"wire_id":        1,
				"config_version": progCat.Source.Checksum,
				"state":          json.RawMessage(state),
			})
		}); err != nil {
			t.Fatal(err)
		}
		role, err := store.LiveCharacter(account, id)
		if err != nil {
			t.Fatal(err)
		}
		return role
	}

	now := time.Unix(1795800000, 0)
	started := now.Add(-30 * time.Second)

	// Case 1: No contracts -> 0 bonus.
	rolePlain := makeChar(10, 100)
	runPlain := completedTestRun(t, "00000000000000000000000000000001", started)
	savedPlain, recPlain, applied, err := svc.Clear(ctx, rolePlain, runPlain, 50, now)
	if err != nil || !applied {
		t.Fatalf("plain clear failed: %v %t", err, applied)
	}
	if recPlain.NeoExperience != 0 || recPlain.GrowthExperience != 0 {
		t.Fatalf("unexpected bonus without contracts: %+v", recPlain)
	}
	var stPlain State
	if err := json.Unmarshal(savedPlain.State, &stPlain); err != nil {
		t.Fatal(err)
	}
	baseTotal := uint64(recPlain.Base) + uint64(recPlain.Score)
	if stPlain.Experience != baseTotal {
		t.Fatalf("plain exp=%d, want %d", stPlain.Experience, baseTotal)
	}

	// Case 2: Active Neo Premium Plus (118, +10%) + Growth's Contract (79, +20%).
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("us224_account_premium", "200", map[byte]int64{
			storage.NeoPremiumPlusKind: now.Unix() + 3600,
			storage.GrowthContractKind: now.Unix() + 3600,
		})
	}); err != nil {
		t.Fatal(err)
	}
	roleBoost := makeChar(20, 200)
	runBoost := completedTestRun(t, "00000000000000000000000000000002", started)
	savedBoost, recBoost, applied, err := svc.Clear(ctx, roleBoost, runBoost, 50, now)
	if err != nil || !applied {
		t.Fatalf("boosted clear failed: %v %t", err, applied)
	}
	wantNeo := uint32(baseTotal * 10 / 100)
	wantGrowth := uint32(baseTotal * 20 / 100)
	if recBoost.NeoExperience != wantNeo || recBoost.GrowthExperience != wantGrowth {
		t.Fatalf("bonuses=(%d,%d), want (%d,%d)", recBoost.NeoExperience, recBoost.GrowthExperience, wantNeo, wantGrowth)
	}
	var stBoost State
	if err := json.Unmarshal(savedBoost.State, &stBoost); err != nil {
		t.Fatal(err)
	}
	wantExp := baseTotal + uint64(wantNeo) + uint64(wantGrowth)
	if stBoost.Experience != wantExp {
		t.Fatalf("boosted exp=%d, want %d", stBoost.Experience, wantExp)
	}

	// Case 3: Expired contracts -> 0 bonus.
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("us224_account_premium", "300", map[byte]int64{
			storage.NeoPremiumPlusKind: now.Unix() - 1,
			storage.GrowthContractKind: now.Unix() - 1,
		})
	}); err != nil {
		t.Fatal(err)
	}
	roleExp := makeChar(30, 300)
	runExp := completedTestRun(t, "00000000000000000000000000000003", started)
	_, recExp, applied, err := svc.Clear(ctx, roleExp, runExp, 50, now)
	if err != nil || !applied || recExp.NeoExperience != 0 || recExp.GrowthExperience != 0 {
		t.Fatalf("expired contracts unexpectedly granted bonus: %+v %v", recExp, err)
	}
}

func idKeyForTest(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}
