package loot

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestNeoAndVIPExtraRewardCardFrozenAndGrantedAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "cards_neo.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	c, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	tables, err := Parse(c)
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	cardRules, err := LoadCardRules("../../configs/us100/rules.cards.json")
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		Store:      store,
		Catalog:    c,
		BagRules:   bagRules,
		Tables:     tables,
		CardPolicy: &cardRules,
	}

	makeCompletedRun := func(runID string) *dungeon.Session {
		boss := protocol.DungeonMonster{Entity: 4096, Level: 5, Template: 1001, Rank: 3, Team: 100}
		room := catalog.DungeonRoom{X: 0, Y: 0, Map: 9001, Boss: true}
		run := &dungeon.Session{
			RunID:      runID,
			StartedAt:  time.Unix(1795800000, 0),
			Definition: catalog.DungeonDefinition{ID: 3, BasisLevel: 5},
			Maze:       catalog.DungeonMaze{Start: [2]byte{0, 0}, Boss: [2]byte{0, 0}, Rooms: []catalog.DungeonRoom{room}},
			Room:       room,
			Monsters:   []protocol.DungeonMonster{boss},
			Loaded:     true,
			Dead:       map[uint16]bool{},
			Visited:    map[uint32][]protocol.DungeonMonster{9001: {boss}},
		}
		if err := run.BossCheck(protocol.BossCheckRequest{Actor: 1, Target: boss.Entity}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := run.ConfirmDeath(uint32(boss.Entity), 1, 1); err != nil {
			t.Fatal(err)
		}
		return run
	}

	now := time.Unix(1795800000, 0)
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		if err := tx.Put("us224_account_premium", "41", map[byte]int64{
			storage.NeoPremiumPlusKind: now.Unix() + 3600,
			storage.VIPContractKind:    now.Unix() + 3600,
		}); err != nil {
			return err
		}
		for _, pair := range []struct {
			key     string
			id, acc int64
		}{{"48", 48, 41}, {"49", 49, 42}} {
			if err := tx.Put("characters", pair.key, map[string]any{
				"id":             pair.id,
				"account_id":     pair.acc,
				"profession":     0,
				"wire_id":        1,
				"config_version": c.Source.Checksum,
				"state":          json.RawMessage(`{"level":5}`),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Member account (41): Neo Premium Plus + VIP active -> freezes extra cards and grants total gold atomically.
	roleMember, err := store.LiveCharacter(41, 48)
	if err != nil {
		t.Fatal(err)
	}
	runMember := makeCompletedRun("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	plan, err := svc.FreezeCardsAt(ctx, roleMember, runMember, cardRules, 12345, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Gold == 0 || plan.NeoBonusGold == 0 || plan.VIPBonusGold == 0 {
		t.Fatalf("expected base + neo + vip card gold, got %+v", plan)
	}
	saved, receipt, applied, err := svc.PickCard(ctx, roleMember, runMember, plan, 2)
	if err != nil || !applied || receipt.Index != 2 {
		t.Fatalf("pick card failed: applied=%t receipt=%+v err=%v", applied, receipt, err)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	wantGold := uint64(plan.Gold) + uint64(plan.NeoBonusGold) + uint64(plan.VIPBonusGold)
	if uint64(bag.Gold) != wantGold {
		t.Fatalf("member bag gold=%d, want %d (base=%d neo=%d vip=%d)", bag.Gold, wantGold, plan.Gold, plan.NeoBonusGold, plan.VIPBonusGold)
	}
	// Retry must be idempotent and never double-grant.
	savedAgain, _, appliedAgain, err := svc.PickCard(ctx, saved, runMember, plan, 2)
	if err != nil || appliedAgain {
		t.Fatalf("retry pick card: applied=%t err=%v", appliedAgain, err)
	}
	bagAgain, err := inventory.ReadBag(savedAgain.State)
	if err != nil || uint64(bagAgain.Gold) != wantGold {
		t.Fatalf("retry mutated gold: %d vs %d", bagAgain.Gold, wantGold)
	}

	// Non-member account (42): no extra card rewards.
	rolePlain, err := store.LiveCharacter(42, 49)
	if err != nil {
		t.Fatal(err)
	}
	runPlain := makeCompletedRun("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	planPlain, err := svc.FreezeCardsAt(ctx, rolePlain, runPlain, cardRules, 12345, now)
	if err != nil {
		t.Fatal(err)
	}
	if planPlain.Gold == 0 || planPlain.NeoBonusGold != 0 || planPlain.VIPBonusGold != 0 {
		t.Fatalf("non-member unexpectedly got bonus card: %+v", planPlain)
	}
}
