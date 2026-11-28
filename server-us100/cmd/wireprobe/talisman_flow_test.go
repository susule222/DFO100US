package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestOpenTalismanSelectionBox10333090RoutesToSpace2B(t *testing.T) {
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{{Slot: 65, Template: 10333090, Amount: 1}})
	plain := cmd160Choice(65, 1, 0, 100401044)
	saved, plan, box, award, qty, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, role, []byte("raw-10333090-tal"), plain, nil,
	)
	if err != nil {
		t.Fatalf("openBoosterBox 10333090 failed: %v", err)
	}
	if !applied || box != 10333090 || award != 100401044 || qty != 1 {
		t.Fatalf("unexpected result applied=%v box=%d award=%d qty=%d", applied, box, award, qty)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Equipment) != 0 {
		t.Fatalf("talisman 100401044 must NOT land in ordinary Equipment tab, got %+v", bag.Equipment)
	}
	if len(bag.Talismans) != 1 || bag.Talismans[0].Template != 100401044 || bag.Talismans[0].Slot != 0 {
		t.Fatalf("expected talisman 100401044 in Space 0x2b slot 0, got %+v", bag.Talismans)
	}
	hasTalRestore := false
	for _, pkt := range plan {
		if pkt.Name == "booster_talisman_restored" && pkt.Kind == 0 && pkt.ID == 13 && len(pkt.Payload) > 0 && pkt.Payload[0] == protocol.TalismanSpace {
			hasTalRestore = true
		}
	}
	if !hasTalRestore {
		t.Fatalf("expected NOTI 13 space 0x2b restore packet in plan, got %+v", plan)
	}
}

func TestOpenRuneBox10312672RoutesToSpace2BRuneTab(t *testing.T) {
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{{Slot: 66, Template: 10312672, Amount: 1}})
	plain := cmd160Plain(66, 1, 0)
	saved, plan, _, _, _, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, role, []byte("raw-10312672-rune"), plain, func(uint32) uint32 { return 0 },
	)
	if err != nil || !applied {
		t.Fatalf("openBoosterBox 10312672 failed: err=%v applied=%v", err, applied)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Items) != 0 {
		t.Fatalf("rune must NOT land in ordinary Items bag, got %+v", bag.Items)
	}
	if len(bag.Runes) != 3 || bag.Runes[0].Slot != inventory.RuneSlotStart || bag.Runes[2].Slot != inventory.RuneSlotStart+2 {
		t.Fatalf("expected 3 runes in Space 0x2b slots %d..%d, got %+v", inventory.RuneSlotStart, inventory.RuneSlotStart+2, bag.Runes)
	}
	hasTalRestore := false
	for _, pkt := range plan {
		if pkt.Name == "booster_talisman_restored" && pkt.Kind == 0 && pkt.ID == 13 && len(pkt.Payload) > 0 && pkt.Payload[0] == protocol.TalismanSpace {
			hasTalRestore = true
		}
	}
	if !hasTalRestore {
		t.Fatalf("expected NOTI 13 space 0x2b restore packet for rune box")
	}
}

func TestTalismanEquipUnequipRuneMoveAndSort(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "talisman.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "talisman-test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join("..", "..", "configs", "us100")
	items, err := catalog.LoadLoot(filepath.Join(cfg, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := inventory.LoadBagRules(filepath.Join(cfg, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog(filepath.Join(cfg, "equipment.generated.json"), items.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	wearRules, err := inventory.LoadWearRules(filepath.Join(cfg, "rules.wear.json"), items.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	chars, err := catalog.LoadCharacters(filepath.Join(cfg, "characters.generated.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Start with legacy state where 100401044 was in Equipment slot 10 + 1 rune in slot 80.
	initialState := json.RawMessage(`{
		"level": 100,
		"advancement": 1,
		"inventory": {
			"version": "ordinary-bag-v1",
			"equipment": [{"slot": 10, "template": 100401044, "durability": 35}],
			"runes": [{"slot": 80, "Template": 10600191, "Amount": 1}]
		}
	}`)
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "TalTester",
		Profession:    0,
		ConfigVersion: items.Source.Checksum,
		State:         initialState,
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 10

	wearSvc := &inventory.WearService{
		Store:       store,
		Catalog:     gear,
		Professions: chars,
		BagRules:    rules,
		Rules:       wearRules,
	}
	w := &worldSession{role: role, channelCtx: [2]byte{1, 10}}
	var eqSess equipmentSession

	// 1. Equip migrated talisman from space 0x2b slot 0 -> space 3 slot 33.
	p1 := encodeCMD19(protocol.TalismanSpace, 0, 100401044, 3, 33, 0)
	plan1, err := eqSess.handle(wearSvc, w, p1, append([]byte("equip-tal"), p1...))
	if err != nil {
		t.Fatalf("equip talisman failed: %v", err)
	}
	bag1, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag1.Talismans) != 0 || len(bag1.Worn) != 1 || bag1.Worn[0].Slot != 33 || bag1.Worn[0].Template != 100401044 {
		t.Fatalf("expected talisman equipped in slot 33, got talismans=%+v worn=%+v", bag1.Talismans, bag1.Worn)
	}
	if fame := gear.BagAdventurerFame(bag1); fame != 185 {
		t.Fatalf("expected worn talisman adventurer fame 185, got %d", fame)
	}
	var sawSpace3, sawSpace2B bool
	for _, pkt := range plan1 {
		if pkt.Kind == 0 && pkt.ID == 14 && len(pkt.Payload) > 0 {
			if pkt.Payload[0] == 3 {
				sawSpace3 = true
			}
			if pkt.Payload[0] == protocol.TalismanSpace {
				sawSpace2B = true
			}
		}
	}
	if !sawSpace3 || !sawSpace2B {
		t.Fatalf("expected NOTI 14 updates for both space 3 and space 0x2b, got space3=%v space2b=%v", sawSpace3, sawSpace2B)
	}

	// 2. Equip rune from space 0x2b slot 80 -> equipped rune slot 320.
	pRune := encodeCMD19(protocol.TalismanSpace, 80, 10600191, protocol.TalismanSpace, 320, 0)
	if _, err := eqSess.handle(wearSvc, w, pRune, append([]byte("equip-rune"), pRune...)); err != nil {
		t.Fatalf("equip rune failed: %v", err)
	}
	bagRune, _ := inventory.ReadBag(w.role.State)
	if len(bagRune.Runes) != 1 || bagRune.Runes[0].Slot != 320 {
		t.Fatalf("expected rune moved to equipped slot 320, got %+v", bagRune.Runes)
	}

	// 3. Drag-unequip talisman 3:33 -> 0x2b:2 and verify ACK 19 normalizes src=0x2b dst=3.
	p2 := encodeCMD19(3, 33, 100401044, protocol.TalismanSpace, 2, 0)
	plan2, err := eqSess.handle(wearSvc, w, p2, append([]byte("unequip-tal"), p2...))
	if err != nil {
		t.Fatalf("unequip talisman failed: %v", err)
	}
	if len(plan2) == 0 || plan2[0].Kind != 1 || plan2[0].ID != 19 {
		t.Fatalf("expected ACK 19 first in plan, got %+v", plan2)
	}
	ack := plan2[0].Payload
	// ItemMoveSuccess layout: [0]=status(1), [1]=srcList, [2..3]=srcSlot, [4..7]=count, [8]=dstList, [9..10]=dstSlot, [11]=0
	if len(ack) != 12 || ack[1] != protocol.TalismanSpace || binary.LittleEndian.Uint16(ack[2:4]) != 2 || ack[8] != 3 || binary.LittleEndian.Uint16(ack[9:11]) != 33 {
		t.Fatalf("expected normalized ACK 19 (0x2b:2 -> 3:33), got len=%d src=%d:%d dst=%d:%d",
			len(ack), ack[1], binary.LittleEndian.Uint16(ack[2:4]), ack[8], binary.LittleEndian.Uint16(ack[9:11]))
	}

	// 4. Sort space 0x2b: move talisman from slot 2 -> slot 0 via CMD 20.
	sortPlain := make([]byte, 0, 8)
	sortPlain = append(sortPlain, protocol.TalismanSpace, 0)
	sortPlain = binary.LittleEndian.AppendUint16(sortPlain, 1)
	sortPlain = binary.LittleEndian.AppendUint16(sortPlain, 2)
	sortPlain = binary.LittleEndian.AppendUint16(sortPlain, 0)
	sortedRole, sortPlan, sortSpace, err := sortInventory(ctx, store, account, w.role, []byte("sort-2b"), sortPlain)
	if err != nil {
		t.Fatalf("sortInventory space 0x2b failed: %v", err)
	}
	if sortSpace != protocol.TalismanSpace {
		t.Fatalf("expected sortSpace 0x2b, got 0x%x", sortSpace)
	}
	bagSorted, _ := inventory.ReadBag(sortedRole.State)
	if len(bagSorted.Talismans) != 1 || bagSorted.Talismans[0].Slot != 0 {
		t.Fatalf("expected talisman sorted to slot 0, got %+v", bagSorted.Talismans)
	}
	if len(sortPlan) == 0 || sortPlan[0].ID != 13 || sortPlan[0].Payload[0] != protocol.TalismanSpace {
		t.Fatalf("expected NOTI 13 space 0x2b restore after sort, got %+v", sortPlan)
	}
}
