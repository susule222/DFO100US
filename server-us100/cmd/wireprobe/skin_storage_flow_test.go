package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"path/filepath"
	"testing"
)

func TestDamageFontUnlockEquipAndBackfill(t *testing.T) {
	ctx := context.Background()
	c, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	bc, err := catalog.LoadBoosters("../../configs/us100/boosters.generated.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	item, ok := bc.SkinItems[10333091]
	if !ok || item.Category != protocol.SkinCatDamageFont || item.Index != 53 {
		t.Fatalf("expected 10333091 in SkinItems as DamageFont #53, got ok=%v item=%+v (total=%d)", ok, item, len(bc.SkinItems))
	}

	path := filepath.Join(t.TempDir(), "skin.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 1, "profession": 0,
			"config_version": r.Source,
			"state": map[string]any{
				"inventory": inventory.Bag{
					Version: "ordinary-bag-v1",
					Items:   []inventory.BagItem{{Slot: 85, Template: 10333091, Amount: 1}},
				},
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.LiveCharacter(42, 48)
	if err != nil {
		t.Fatal(err)
	}

	w := &worldSession{
		account:    42,
		role:       role,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: c, BagRules: r, Boosters: bc},
	}

	// 1. Use Damage Font item 10333091 via CMD 507 (action=170)
	cmd507 := make([]byte, 32)
	binary.LittleEndian.PutUint16(cmd507[0:2], 85)
	cmd507[2] = 0
	binary.LittleEndian.PutUint32(cmd507[7:11], actionTypeAddSkinStorage)
	plan, err := w.useStackableAction(cmd507, []byte("raw-507-df53"))
	if err != nil {
		t.Fatalf("useStackableAction failed: %v", err)
	}
	var gotList, gotEquip bool
	for _, p := range plan {
		if p.ID == protocol.NotiSkinStorageList && len(p.Payload) == 13 && p.Payload[0] == 0 && binary.LittleEndian.Uint16(p.Payload[1:3]) == 1 && binary.LittleEndian.Uint32(p.Payload[3:7]) == 53 {
			gotList = true
		}
		if p.ID == protocol.NotiSkinStorageEquip && len(p.Payload) == 9 && p.Payload[0] == 0 && binary.LittleEndian.Uint32(p.Payload[1:5]) == 53 && binary.LittleEndian.Uint32(p.Payload[5:9]) == 53 {
			gotEquip = true
		}
	}
	if !gotList || !gotEquip {
		t.Fatalf("expected NOTI 1545 (owned #53) + NOTI 1546 (equipped #53/#53), got list=%v equip=%v plan=%+v", gotList, gotEquip, plan)
	}

	// 2. Change equipped Damage Font via CMD 1565 (normal=53, cumulative=0)
	cmd1565 := make([]byte, 44)
	binary.LittleEndian.PutUint32(cmd1565[0:4], 0)
	binary.LittleEndian.PutUint32(cmd1565[4:8], 53)
	binary.LittleEndian.PutUint32(cmd1565[8:12], 0)
	eqPlan, err := w.handleSkinStorageEquip(cmd1565)
	if err != nil {
		t.Fatalf("handleSkinStorageEquip failed: %v", err)
	}
	if len(eqPlan) != 2 || eqPlan[0].ID != protocol.CmdSkinStorageEquip || len(eqPlan[0].Payload) != 45 || eqPlan[0].Payload[0] != 1 {
		t.Fatalf("unexpected ACK 1565 plan: %+v", eqPlan)
	}
	if eqPlan[1].ID != protocol.NotiSkinStorageEquip || binary.LittleEndian.Uint32(eqPlan[1].Payload[1:5]) != 53 || binary.LittleEndian.Uint32(eqPlan[1].Payload[5:9]) != 0 {
		t.Fatalf("unexpected NOTI 1546 update: %x", eqPlan[1].Payload)
	}

	// 3. Relog entry restores owned Damage Font #53 and equipped (53, 0)
	entryPkts := skinStoragePacketsOnEntry(store, bc, 42, 48)
	if len(entryPkts) != 2 || entryPkts[0].ID != protocol.NotiSkinStorageList || entryPkts[1].ID != protocol.NotiSkinStorageEquip {
		t.Fatalf("unexpected entry restore packets: %+v", entryPkts)
	}

	// 4. Backfill test: delete skin_storage_v224 row while character_events retains consume507(10333091)
	_ = store.Database().Mutate(func(tx *db100.Tx) error {
		tx.Delete(skinStorageTable, skinStorageKey(42, 48))
		return nil
	})
	backfilledPkts := skinStoragePacketsOnEntry(store, bc, 42, 48)
	if len(backfilledPkts) != 2 || binary.LittleEndian.Uint32(backfilledPkts[0].Payload[3:7]) != 53 {
		t.Fatalf("expected auto-backfill of previously consumed 10333091 -> DamageFont #53, got %+v", backfilledPkts)
	}
}
