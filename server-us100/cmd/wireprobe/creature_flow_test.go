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
	registerInventoryLookups(bc, eqCat)
	defer func() {
		inventory.RegisterCreatureEggLookup(nil)
		inventory.RegisterOrdinaryEquipmentLookup(nil)
	}()

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
	if len(hatchPlan) != 3 || hatchPlan[0].ID != protocol.CmdCreatureEggHatch ||
		len(hatchPlan[0].Payload) != 4 || hatchPlan[0].Payload[0] != 1 ||
		binary.LittleEndian.Uint16(hatchPlan[0].Payload[1:3]) != 0 || hatchPlan[0].Payload[3] != protocol.CreatureSpace {
		t.Fatalf("unexpected ACK 102 payload: %+v", hatchPlan)
	}
	if hatchPlan[1].ID != protocol.NotiCreatureInfo || len(hatchPlan[1].Payload) == 0 {
		t.Fatalf("expected NOTI 105 before NOTI 14 on egg hatch, got %+v", hatchPlan)
	}
	if hatchPlan[2].ID != 14 || hatchPlan[2].Payload[0] != protocol.CreatureSpace ||
		binary.LittleEndian.Uint32(hatchPlan[2].Payload[5:9]) != 500990710 {
		t.Fatalf("expected NOTI 14 Space 7 with hatched 500990710, got %x", hatchPlan[2].Payload[:min(16, len(hatchPlan[2].Payload))])
	}
	if cid := binary.LittleEndian.Uint32(hatchPlan[2].Payload[9:13]); cid == 0 {
		t.Fatalf("expected non-zero CreatureID at row[6:10] (payload[9:13]), got 0")
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

func TestCreatureRenderNoti105AutoHatchAndBoosterTitleMigration(t *testing.T) {
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
	registerInventoryLookups(bc, eqCat)
	defer func() {
		inventory.RegisterCreatureEggLookup(nil)
		inventory.RegisterOrdinaryEquipmentLookup(nil)
	}()

	// 1. Verify NOTI 105 exact wire encoding (0x1434f2390): 1 + 16*N bytes for unnamed creatures.
	ci := protocol.CreatureInfoList([]protocol.CreatureInfoEntry{
		{InstanceID: 7, Stamina: 100, Level: 1},
	})
	if len(ci) != 17 || ci[0] != 1 ||
		binary.LittleEndian.Uint32(ci[1:5]) != 7 ||
		ci[5] != 100 || ci[6] != 0 ||
		binary.LittleEndian.Uint32(ci[7:11]) != 0 ||
		ci[11] != 1 ||
		binary.LittleEndian.Uint32(ci[12:16]) != 0 ||
		ci[16] != 0 {
		t.Fatalf("unexpected NOTI 105 wire bytes: %x", ci)
	}

	// 2. Verify entryPayloads places NOTI 105 BEFORE Space 7 and Space 3 restore.
	ep := entryPayloads{
		CreatureInfo: ci,
		Creatures:    []byte{protocol.CreatureSpace, 0, 0},
		Worn:         []byte{3, 0, 0},
	}
	var idx105, idxCre, idxWorn = -1, -1, -1
	for i, pkt := range ep.packets() {
		switch pkt.Name {
		case "creature_info_restored":
			idx105 = i
		case "creature_inventory_restored":
			idxCre = i
		case "worn_equipment_restored":
			idxWorn = i
		}
	}
	if idx105 < 0 || idxCre < 0 || idxWorn < 0 || !(idx105 < idxCre && idx105 < idxWorn) {
		t.Fatalf("expected NOTI 105 before Space 7 and Space 3: idx105=%d idxCre=%d idxWorn=%d", idx105, idxCre, idxWorn)
	}

	// 3. Seed legacy Char 48 state:
	//    - Worn slot 26 holds unhatched egg 500980049 with CreatureID=0
	//    - Space 7 slot 0 holds another unhatched egg 500980049 with CreatureID=0
	//    - Space 0 slot 78 holds misplaced Title 500330547 from older booster catalog
	//    - Space 0 slot 79 holds Title Random Box 590005789
	path := filepath.Join(t.TempDir(), "creature_render.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 1, "name": "PetHero", "profession": 0,
			"config_version": r.Source,
			"state": map[string]any{
				"level": 100,
				"inventory": inventory.Bag{
					Version: "ordinary-bag-v1",
					Worn: []inventory.BagEquipment{
						{Slot: inventory.WornCreatureSlot, Template: 500980049},
					},
					Creatures: []inventory.BagEquipment{
						{Slot: 0, Template: 500980049},
					},
					Items: []inventory.BagItem{
						{Slot: 78, Template: 500330547, Amount: 1},
						{Slot: 79, Template: 590005789, Amount: 1},
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

	// 4. ReadBag auto-hatches worn egg 500980049 -> 500990710, assigns deterministic
	//    unique CreatureIDs (1 and 2), and migrates Title 500330547 into Equipment (9..64).
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		t.Fatalf("ReadBag failed: %v", err)
	}
	if len(bag.Worn) != 1 || bag.Worn[0].Template != 500990710 || bag.Worn[0].CreatureID == 0 {
		t.Fatalf("expected auto-hatched worn creature 500990710 with CreatureID>0, got %+v", bag.Worn)
	}
	if len(bag.Creatures) != 1 || bag.Creatures[0].CreatureID == 0 || bag.Creatures[0].CreatureID == bag.Worn[0].CreatureID {
		t.Fatalf("expected unique non-zero CreatureID for bag creature, got worn=%+v bag=%+v", bag.Worn, bag.Creatures)
	}
	wornRow := inventory.EquipmentRow(bag.Worn[0])
	if gotID := binary.LittleEndian.Uint32(wornRow[6:10]); gotID != bag.Worn[0].CreatureID {
		t.Fatalf("expected EquipmentRow[6:10]=%d, got %d", bag.Worn[0].CreatureID, gotID)
	}
	if len(bag.Equipment) != 1 || bag.Equipment[0].Template != 500330547 || bag.Equipment[0].Slot < 9 || bag.Equipment[0].Slot > 64 {
		t.Fatalf("expected migrated Title 500330547 in Equipment 9..64, got %+v", bag.Equipment)
	}

	// Verify CreatureInfoPayload is deterministic and includes both CreatureIDs.
	ciPayload, err := inventory.CreatureInfoPayload(role.State)
	if err != nil || len(ciPayload) != 1+2*16 || ciPayload[0] != 2 {
		t.Fatalf("expected 2 creatures in NOTI 105 payload, err=%v len=%d", err, len(ciPayload))
	}

	w := &worldSession{
		account:    42,
		role:       role,
		characters: &character.Service{Store: store, Equipment: eqCat},
		loot:       &loot.Service{Store: store, Catalog: c, BagRules: r, Equipment: eqCat, Boosters: bc},
	}
	wearSvc := &inventory.WearService{
		Store:       store,
		Catalog:     eqCat,
		Professions: chars,
		BagRules:    r,
		Rules:       wearRules,
	}
	var eqSess equipmentSession

	// 5. Equipping Title 500330547 from migrated Equipment slot 9 -> Space 3 slot 13 succeeds.
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(0, bag.Equipment[0].Slot, 500330547, 3, 13, 0), []byte("eq-title-500330547")); err != nil {
		t.Fatalf("equip booster title 500330547 failed: %v", err)
	}

	// 6. Right-click equipping unhatched egg 500980049 (Space 7:0 -> Space 3:26) via CMD 19
	//    auto-hatches to 500990710 and emits NOTI 105 BEFORE NOTI 14 Space 3!
	plan, err := eqSess.handle(wearSvc, w, encodeCMD19(protocol.CreatureSpace, 0, 500980049, 3, inventory.WornCreatureSlot, 500990710), []byte("eq-egg-auto-hatch"))
	if err != nil {
		t.Fatalf("equip creature egg failed: %v", err)
	}
	noti105Idx, noti14WornIdx := -1, -1
	var wornTpl, wornCID uint32
	for i, pkt := range plan {
		if pkt.ID == protocol.NotiCreatureInfo {
			noti105Idx = i
		}
		if pkt.ID == 14 && len(pkt.Payload) >= 13 && pkt.Payload[0] == 3 {
			noti14WornIdx = i
			wornTpl = binary.LittleEndian.Uint32(pkt.Payload[5:9])
			wornCID = binary.LittleEndian.Uint32(pkt.Payload[9:13])
		}
	}
	if noti105Idx < 0 || noti14WornIdx < 0 || noti105Idx > noti14WornIdx {
		t.Fatalf("expected NOTI 105 before NOTI 14 Space 3, got plan=%+v", plan)
	}
	if wornTpl != 500990710 || wornCID == 0 {
		t.Fatalf("expected auto-hatched 500990710 with CreatureID>0 in Space 3 NOTI 14, got tpl=%d cid=%d", wornTpl, wornCID)
	}
	// Verify live town NOTI 2 mode-0 update carries 0x143803320 creature template + active=1.
	var mode0Payload []byte
	for _, pkt := range plan {
		if pkt.ID == 2 && len(pkt.Payload) > 0 && pkt.Payload[0] == 0 {
			mode0Payload = pkt.Payload
		}
	}
	if len(mode0Payload) == 0 {
		t.Fatalf("expected NOTI 2 mode-0 town_appearance_updated after equipping creature")
	}
	nameLen := int(binary.LittleEndian.Uint32(mode0Payload[134:138]))
	eqStart := 138 + nameLen + 5
	eqCount := int(mode0Payload[eqStart])
	creOff := eqStart + 1 + eqCount*31 + 22
	if gotTpl := binary.LittleEndian.Uint32(mode0Payload[creOff : creOff+4]); gotTpl != 500990710 {
		t.Fatalf("live NOTI 2 mode-0 creature template = %d, want 500990710", gotTpl)
	}
	if gotActive := mode0Payload[creOff+8]; gotActive != 1 {
		t.Fatalf("live NOTI 2 mode-0 creature active byte = %d, want 1", gotActive)
	}

	// 7. Opening Title Random Box 590005789 awards Title directly into Equipment (9..64), NOT Items!
	cmd160 := make([]byte, 8)
	binary.LittleEndian.PutUint16(cmd160[0:2], 79)
	binary.LittleEndian.PutUint16(cmd160[2:4], 1)
	savedAfterBox, _, _, awardTpl, _, applied, err := openBoosterBox(ctx, store, nil, w.loot, 42, w.role, []byte("open-title-box-590005789"), cmd160, func(n uint32) uint32 { return 0 })
	if err != nil || !applied {
		t.Fatalf("openBoosterBox(590005789) failed: err=%v applied=%v", err, applied)
	}
	bagAfterBox, _ := inventory.ReadBag(savedAfterBox.State)
	foundInEquip := false
	for _, eq := range bagAfterBox.Equipment {
		if eq.Template == awardTpl && eq.Slot >= 9 && eq.Slot <= 64 {
			foundInEquip = true
		}
	}
	if !foundInEquip {
		t.Fatalf("expected title %d from box 590005789 in Equipment (9..64), got equip=%+v items=%+v", awardTpl, bagAfterBox.Equipment, bagAfterBox.Items)
	}
}

func TestBoosterMisclassifiedAuraTitleAndStackableRouting(t *testing.T) {
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
	registerInventoryLookups(bc, eqCat)
	defer func() {
		inventory.RegisterCreatureEggLookup(nil)
		inventory.RegisterOrdinaryEquipmentLookup(nil)
		inventory.RegisterOrdinaryEquipmentDurabilityLookup(nil)
	}()

	path := filepath.Join(t.TempDir(), "aura_title_mig.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Seed Char 48 state reproducing the exact reported screenshot:
	// - Space 0 slot 78: misplaced Title 500330547 (先驱者的觉悟 - 风振)
	// - Space 0 slot 79: misplaced Aura Avatar 501590378 (自我觉醒气息)
	// - Space 0 slot 9:  Title 100331477 (第一皇冠, absent from equipment.generated.json)
	// - Space 0 slot 80: Aura Random Box 590005788
	// - Space 0 slot 81: Dye Box 10000079 (stackable dyes inside [equipment] tag)
	// - Space 0 slot 82: Creature Rename Card Box 50006524 (stackable inside [creature] tag)
	err = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 1, "name": "AuraHero", "profession": 0,
			"config_version": r.Source,
			"state": map[string]any{
				"level": 100,
				"inventory": inventory.Bag{
					Version: "ordinary-bag-v1",
					Equipment: []inventory.BagEquipment{
						{Slot: 9, Template: 100331477, Durability: 0},
					},
					Items: []inventory.BagItem{
						{Slot: 78, Template: 500330547, Amount: 1},
						{Slot: 79, Template: 501590378, Amount: 1},
						{Slot: 80, Template: 590005788, Amount: 1},
						{Slot: 81, Template: 10000079, Amount: 1},
						{Slot: 82, Template: 50006524, Amount: 1},
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

	// 1. ReadBag auto-migrates Aura 501590378 -> Space 1 Avatars (data=0 permanent)
	//    and Title 500330547 -> Space 0 Equipment (9..64, durability=0).
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		t.Fatalf("ReadBag failed: %v", err)
	}
	if len(bag.Avatars) != 1 || bag.Avatars[0].Template != 501590378 || bag.Avatars[0].Durability != 0 {
		t.Fatalf("expected migrated Aura 501590378 in Space 1 Avatars with Durability=0, got %+v", bag.Avatars)
	}
	auraRow := inventory.EquipmentRow(bag.Avatars[0])
	if expireData := binary.LittleEndian.Uint32(auraRow[6:10]); expireData != 0 {
		t.Fatalf("expected permanent Aura row data=0 (not expired), got %d", expireData)
	}
	var titleSlot uint16
	for _, eq := range bag.Equipment {
		if eq.Template == 500330547 {
			titleSlot = eq.Slot
			if eq.Durability != 0 {
				t.Fatalf("expected Title 500330547 durability=0, got %d", eq.Durability)
			}
		}
	}
	if titleSlot < 9 || titleSlot > 64 {
		t.Fatalf("expected migrated Title 500330547 in Equipment 9..64, got equip=%+v items=%+v", bag.Equipment, bag.Items)
	}
	for _, it := range bag.Items {
		if it.Template == 501590378 || it.Template == 500330547 {
			t.Fatalf("misclassified item %d still present in bag.Items: %+v", it.Template, bag.Items)
		}
	}

	w := &worldSession{
		account:    42,
		role:       role,
		characters: &character.Service{Store: store, Equipment: eqCat},
		loot:       &loot.Service{Store: store, Catalog: c, BagRules: r, Equipment: eqCat, Boosters: bc},
	}
	wearSvc := &inventory.WearService{
		Store:       store,
		Catalog:     eqCat,
		Professions: chars,
		BagRules:    r,
		Rules:       wearRules,
	}
	var eqSess equipmentSession

	// 2. Equip Aura 501590378 from Space 1 -> Space 3 slot 9 (Aura slot).
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(1, bag.Avatars[0].Slot, 501590378, 3, 9, 0), []byte("eq-aura-501590378")); err != nil {
		t.Fatalf("equip migrated Aura 501590378 failed: %v", err)
	}
	// 3. Equip Title 500330547 from migrated Equipment slot -> Space 3 slot 13, then swap with Title 100331477.
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(0, titleSlot, 500330547, 3, 13, 0), []byte("eq-title-500330547")); err != nil {
		t.Fatalf("equip migrated Title 500330547 failed: %v", err)
	}
	if _, err := eqSess.handle(wearSvc, w, encodeCMD19(0, 9, 100331477, 3, 13, 500330547), []byte("swap-title-100331477")); err != nil {
		t.Fatalf("equip booster-absent Title 100331477 failed: %v", err)
	}

	// 4. Open Aura Random Box 590005788 -> goes directly into Space 1 Avatars!
	openBox := func(slot uint16, tag string) (storage.Character, uint32) {
		cmd := make([]byte, 8)
		binary.LittleEndian.PutUint16(cmd[0:2], slot)
		binary.LittleEndian.PutUint16(cmd[2:4], 1)
		saved, _, _, tpl, _, applied, err := openBoosterBox(ctx, store, nil, w.loot, 42, w.role, []byte(tag), cmd, func(n uint32) uint32 { return 0 })
		if err != nil || !applied {
			t.Fatalf("openBoosterBox(slot=%d) failed: err=%v applied=%v", slot, err, applied)
		}
		w.role = saved
		return saved, tpl
	}
	saved, auraAward := openBox(80, "open-aura-box-590005788")
	bagAfter, _ := inventory.ReadBag(saved.State)
	if len(bagAfter.Avatars) != 1 || bagAfter.Avatars[0].Template != auraAward {
		t.Fatalf("expected Aura %d from box 590005788 in Space 1 Avatars, got avatars=%+v items=%+v", auraAward, bagAfter.Avatars, bagAfter.Items)
	}

	// 5. Open Dye Box 10000079 -> stackable dye stays in Space 0 Consumable Items (65..120), NOT Equipment!
	saved, dyeAward := openBox(81, "open-dye-box-10000079")
	bagAfter, _ = inventory.ReadBag(saved.State)
	foundDyeInConsumable := false
	for _, it := range bagAfter.Items {
		if it.Template == dyeAward && it.Slot >= 65 && it.Slot <= 120 {
			foundDyeInConsumable = true
		}
	}
	if !foundDyeInConsumable {
		t.Fatalf("expected Dye %d from box 10000079 in Consumable (65..120), got items=%+v equip=%+v", dyeAward, bagAfter.Items, bagAfter.Equipment)
	}

	// 6. Open Creature Rename Card Box 50006524 -> stackable 25 goes to Space 7 CreatureItems (216..271), NOT Creatures!
	saved, cardAward := openBox(82, "open-creature-card-50006524")
	bagAfter, _ = inventory.ReadBag(saved.State)
	if len(bagAfter.CreatureItems) != 1 || bagAfter.CreatureItems[0].Template != cardAward || bagAfter.CreatureItems[0].Slot < inventory.CreatureItemSlotStart {
		t.Fatalf("expected Creature Rename Card %d in Space 7 CreatureItems (216..271), got creatureItems=%+v creatures=%+v", cardAward, bagAfter.CreatureItems, bagAfter.Creatures)
	}
}
