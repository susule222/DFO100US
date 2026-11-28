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

func TestCreatureBoosterHatchEquipSortAndAllCategoryMigrations(t *testing.T) {
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
	chars, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	eqCat, err := inventory.LoadEquipmentCatalog("../../configs/us100/equipment.generated.json", r.Source)
	if err != nil {
		t.Fatal(err)
	}
	wearRules, err := inventory.LoadWearRules("../../configs/us100/rules.wear.json", r.Source)
	if err != nil {
		t.Fatal(err)
	}

	// Verify CreatureEgg mapping for SD Azalea [Holy] (500980049 -> 500990710).
	if hatched := bc.CreatureEggs[500980049]; hatched != 500990710 {
		t.Fatalf("expected CreatureEggs[500980049]=500990710, got %d (total=%d)", hatched, len(bc.CreatureEggs))
	}

	path := filepath.Join(t.TempDir(), "creature.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Seed character with misplaced items across Space 0 (exactly mirroring live bug + all other categories).
	err = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 1, "name": "PetHero", "profession": 0,
			"config_version": r.Source,
			"state": map[string]any{
				"level": 100,
				"inventory": inventory.Bag{
					Version: "ordinary-bag-v1",
					Items: []inventory.BagItem{
						{Slot: 86, Template: 500980049, Amount: 1}, // Creature Egg misplaced in Consumables
						{Slot: 87, Template: 25, Amount: 3},        // Creature Rename Card misplaced in Consumables
						{Slot: 88, Template: 90290, Amount: 2},     // Flag Gem misplaced in Consumables
						{Slot: 89, Template: 10600191, Amount: 1},  // Rune misplaced in Consumables
						{Slot: 90, Template: 590004744, Amount: 1}, // Emblem misplaced in Consumables
						{Slot: 91, Template: 590005790, Amount: 1}, // Creature Random Box
					},
					Equipment: []inventory.BagEquipment{
						{Slot: 9, Template: 500950002, Durability: 0},  // Red Creature Artifact misplaced in Equipment
						{Slot: 10, Template: 100380044, Durability: 0}, // Guild Insignia [flag] misplaced in Equipment
					},
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

	// 1. ReadBag auto-migrates every misplaced item into its authentic US100 space & slot range!
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		t.Fatalf("ReadBag migration failed: %v", err)
	}
	if len(bag.Creatures) != 1 || bag.Creatures[0].Template != 500980049 || bag.Creatures[0].Slot != 0 {
		t.Fatalf("expected migrated creature egg 500980049 at space 7 slot 0, got %+v", bag.Creatures)
	}
	if len(bag.CreatureArtifacts) != 1 || bag.CreatureArtifacts[0].Template != 500950002 || bag.CreatureArtifacts[0].Slot != inventory.ArtifactSlotStart {
		t.Fatalf("expected migrated artifact 500950002 at space 7 slot 160, got %+v", bag.CreatureArtifacts)
	}
	if len(bag.CreatureItems) != 1 || bag.CreatureItems[0].Template != 25 || bag.CreatureItems[0].Slot != inventory.CreatureItemSlotStart || bag.CreatureItems[0].Amount != 3 {
		t.Fatalf("expected migrated creature item 25 x3 at space 7 slot 216, got %+v", bag.CreatureItems)
	}
	if len(bag.Insignias) != 1 || bag.Insignias[0].Template != 100380044 || bag.Insignias[0].Slot != 0 {
		t.Fatalf("expected migrated insignia 100380044 at space 0x25 slot 0, got %+v", bag.Insignias)
	}
	if len(bag.Gems) != 1 || bag.Gems[0].Template != 90290 || bag.Gems[0].Slot != inventory.GemSlotStart || bag.Gems[0].Amount != 2 {
		t.Fatalf("expected migrated gem 90290 x2 at space 0x25 slot 56, got %+v", bag.Gems)
	}
	if len(bag.Runes) != 1 || bag.Runes[0].Template != 10600191 || bag.Runes[0].Slot != inventory.RuneSlotStart {
		t.Fatalf("expected migrated rune 10600191 at space 0x2b slot 80, got %+v", bag.Runes)
	}
	if len(bag.Equipment) != 0 {
		t.Fatalf("expected 0 ordinary equipment remaining, got %+v", bag.Equipment)
	}
	var emblemSlot uint16
	for _, it := range bag.Items {
		if it.Template == 590004744 {
			emblemSlot = it.Slot
		}
	}
	if emblemSlot < inventory.EmblemSlotStart || emblemSlot > inventory.EmblemSlotEnd {
		t.Fatalf("expected migrated emblem in 289..360, got slot %d", emblemSlot)
	}

	// Verify entry restore payloads for Space 7 and Space 0x25.
	creRestore, err := inventory.CreaturePayload(role.State)
	if err != nil || creRestore[0] != protocol.CreatureSpace || binary.LittleEndian.Uint16(creRestore[1:3]) != 3 {
		t.Fatalf("unexpected Space 7 restore: err=%v head=%x", err, creRestore[:min(8, len(creRestore))])
	}
	insRestore, err := inventory.InsigniaPayload(role.State)
	if err != nil || insRestore[0] != protocol.InsigniaSpace || binary.LittleEndian.Uint16(insRestore[1:3]) != 2 {
		t.Fatalf("unexpected Space 0x25 restore: err=%v head=%x", err, insRestore[:min(8, len(insRestore))])
	}

	w := &worldSession{
		account:    42,
		role:       role,
		characters: &character.Service{Store: store, Equipment: eqCat},
		loot:       &loot.Service{Store: store, Catalog: c, BagRules: r, Equipment: eqCat, Boosters: bc},
	}

	// 2. Open Creature Random Box 590005790 (slot 91) via CMD 160 -> awards Creature into Space 7 + NOTI 13 Space 7!
	cmd160 := make([]byte, 8)
	binary.LittleEndian.PutUint16(cmd160[0:2], 91)
	binary.LittleEndian.PutUint16(cmd160[2:4], 1)
	savedAfterBox, boxPlan, _, awardTpl, _, applied, err := openBoosterBox(ctx, store, nil, w.loot, 42, w.role, []byte("open-590005790"), cmd160, func(n uint32) uint32 { return 0 })
	if err != nil || !applied {
		t.Fatalf("openBoosterBox(590005790) failed: err=%v applied=%v", err, applied)
	}
	if !inventory.IsCreatureTemplate(awardTpl) {
		t.Fatalf("expected 590005790 to award a creature template, got %d", awardTpl)
	}
	var gotCreatureRestore bool
	for _, p := range boxPlan {
		if p.ID == 13 && len(p.Payload) > 0 && p.Payload[0] == protocol.CreatureSpace {
			gotCreatureRestore = true
		}
	}
	if !gotCreatureRestore {
		t.Fatalf("expected NOTI 13 Space 7 restore after opening creature box, got %+v", boxPlan)
	}
	w.role = savedAfterBox
	bagAfterBox, _ := inventory.ReadBag(w.role.State)
	if len(bagAfterBox.Creatures) != 2 {
		t.Fatalf("expected 2 creatures in Space 7 after opening box, got %+v", bagAfterBox.Creatures)
	}

	// 3. Right-click hatch SD Azalea egg 500980049 at Space 7 slot 0 via CMD 102 -> hatched 500990710!
	cmd102 := make([]byte, 8)
	cmd102[0] = protocol.CreatureSpace
	binary.LittleEndian.PutUint16(cmd102[1:3], 0)
	hatchPlan, err := w.handleCreatureEggHatch(cmd102, []byte("hatch-102-slot0"))
	if err != nil {
		t.Fatalf("handleCreatureEggHatch failed: %v", err)
	}
	if len(hatchPlan) != 2 || hatchPlan[0].ID != protocol.CmdCreatureEggHatch ||
		len(hatchPlan[0].Payload) != 4 || hatchPlan[0].Payload[0] != 1 ||
		binary.LittleEndian.Uint16(hatchPlan[0].Payload[1:3]) != 0 || hatchPlan[0].Payload[3] != protocol.CreatureSpace {
		t.Fatalf("unexpected ACK 102 payload: %+v", hatchPlan)
	}
	if hatchPlan[1].ID != 14 || hatchPlan[1].Payload[0] != protocol.CreatureSpace ||
		binary.LittleEndian.Uint32(hatchPlan[1].Payload[5:9]) != 500990710 {
		t.Fatalf("expected NOTI 14 Space 7 with hatched 500990710, got %x", hatchPlan[1].Payload[:min(16, len(hatchPlan[1].Payload))])
	}

	// 4. Equip hatched creature (Space 7:0 -> Space 3:26), artifact (Space 7:160 -> Space 3:27), insignia (Space 0x25:0 -> Space 3:31).
	wearSvc := &inventory.WearService{
		Store:       store,
		Catalog:     eqCat,
		Professions: chars,
		BagRules:    r,
		Rules:       wearRules,
	}
	var eqSess equipmentSession
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(protocol.CreatureSpace, 0, 500990710, 3, inventory.WornCreatureSlot, 0), []byte("eq-cre")); err != nil {
		t.Fatalf("equip creature failed: %v", err)
	}
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(protocol.CreatureSpace, inventory.ArtifactSlotStart, 500950002, 3, inventory.WornArtifactSlotMin, 0), []byte("eq-art")); err != nil {
		t.Fatalf("equip creature artifact failed: %v", err)
	}
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(protocol.InsigniaSpace, 0, 100380044, 3, inventory.WornInsigniaSlot, 0), []byte("eq-ins")); err != nil {
		t.Fatalf("equip insignia failed: %v", err)
	}
	bagWorn, _ := inventory.ReadBag(w.role.State)
	if len(bagWorn.Worn) != 3 {
		t.Fatalf("expected 3 worn items (creature+artifact+insignia), got %+v", bagWorn.Worn)
	}
	// Drag-unequip creature 3:26 -> Space 7:5 and verify normalized ACK 19 (src=7:5 dst=3:26).
	unPlan, err := eqSess.handle(wearSvc, w, encodeCMD19(3, inventory.WornCreatureSlot, 500990710, protocol.CreatureSpace, 5, 0), []byte("uneq-cre"))
	if err != nil {
		t.Fatalf("unequip creature failed: %v", err)
	}
	if len(unPlan) < 3 || unPlan[0].ID != 19 || unPlan[0].Payload[1] != protocol.CreatureSpace || binary.LittleEndian.Uint16(unPlan[0].Payload[2:4]) != 5 || unPlan[0].Payload[8] != 3 || binary.LittleEndian.Uint16(unPlan[0].Payload[9:11]) != inventory.WornCreatureSlot {
		t.Fatalf("expected normalized creature drag-unequip ACK 19, got %+v", unPlan)
	}

	// 5. Sort Space 7 (move creature from slot 1 -> slot 0) and Space 0x25 (move gem 56 -> 57).
	sortCre := make([]byte, 8)
	sortCre[0] = protocol.CreatureSpace
	binary.LittleEndian.PutUint16(sortCre[2:4], 1)
	binary.LittleEndian.PutUint16(sortCre[4:6], 1)
	binary.LittleEndian.PutUint16(sortCre[6:8], 0)
	if _, spPlan, sp, err := sortInventory(ctx, store, 42, w.role, []byte("sort-cre"), sortCre); err != nil || sp != protocol.CreatureSpace || spPlan[0].Payload[0] != protocol.CreatureSpace {
		t.Fatalf("sort Space 7 failed: err=%v sp=%d plan=%+v", err, sp, spPlan)
	}

	// 6. Awarder.Grant routes every special item & previously unmapped stackable alias cleanly!
	awarder := &inventory.Awarder{Catalog: c, Rules: r, Equipment: eqCat}
	st, _, err := awarder.Grant(w.role.State, 500980049, 1)
	if err != nil {
		t.Fatalf("Awarder.Grant(creature) failed: %v", err)
	}
	st, _, err = awarder.Grant(st, 500950002, 1)
	if err != nil {
		t.Fatalf("Awarder.Grant(artifact) failed: %v", err)
	}
	st, _, err = awarder.Grant(st, 100380044, 1)
	if err != nil {
		t.Fatalf("Awarder.Grant(insignia) failed: %v", err)
	}
	st, _, err = awarder.Grant(st, 90290, 5)
	if err != nil {
		t.Fatalf("Awarder.Grant(gem) failed: %v", err)
	}
	st, _, err = awarder.Grant(st, 10600191, 1)
	if err != nil {
		t.Fatalf("Awarder.Grant(rune) failed: %v", err)
	}
	// Verify previously unmapped PVF stackable types map cleanly via rules.bag.json slot_aliases.
	for stType, wantTarget := range map[string]string{
		"[etc]":                  "[throw]",
		"[recipe]":               "[throw]",
		"[quest receive]":        "[quest]",
		"[expert town potion]":   "[material expert job]",
		"[material instant item]": "[material]",
	} {
		if got := r.SlotAliases[stType]; got != wantTarget {
			t.Fatalf("rules.bag.json slot_aliases[%q]=%q, want %q", stType, got, wantTarget)
		}
	}
	_ = st
}
