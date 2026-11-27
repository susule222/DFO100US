package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func encodeCMD19(srcList byte, srcSlot uint16, srcItem uint32, dstList byte, dstSlot uint16, dstItem uint32) []byte {
	p := make([]byte, 29)
	p[0] = srcList
	binary.LittleEndian.PutUint16(p[1:], srcSlot)
	binary.LittleEndian.PutUint32(p[3:], srcItem)
	binary.LittleEndian.PutUint32(p[7:], 1)
	p[11] = dstList
	binary.LittleEndian.PutUint16(p[12:], dstSlot)
	binary.LittleEndian.PutUint32(p[14:], dstItem)
	binary.LittleEndian.PutUint32(p[22:], 0xffffffff)
	return p
}

func mode0VisualSlots(t *testing.T, name string, payload []byte) map[byte]uint32 {
	t.Helper()
	if len(payload) < 138+len(name)+6 || payload[0] != 0 {
		t.Fatalf("invalid NOTI 2 mode 0 payload len=%d", len(payload))
	}
	eqStart := 138 + len(name) + 5
	count := int(payload[eqStart])
	out := make(map[byte]uint32, count)
	for i := 0; i < count; i++ {
		off := eqStart + 1 + i*31
		slot := payload[off]
		itemID := binary.LittleEndian.Uint32(payload[off+1 : off+5])
		out[slot] = itemID
	}
	return out
}

func TestTownTitleEquipSwapUnequipSyncsAppearance(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "title.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "title-sync-test")
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

	const (
		titleA = uint32(26541)
		titleB = uint32(26542)
		coatID = uint32(10000)
	)
	initialState := json.RawMessage(`{
		"level":15,
		"advancement":0,
		"inventory":{
			"version":"ordinary-bag-v1",
			"equipment":[
				{"slot":10,"template":26542,"count":1},
				{"slot":11,"template":10000,"count":1},
				{"slot":18,"template":26541,"count":1}
			],
			"worn":[]
		}
	}`)
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "2",
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
	var moves equipmentSession

	// 1. Equip titleA from bag slot 18 -> empty worn slot 13
	p1 := encodeCMD19(0, 18, titleA, 3, 13, 0)
	plan1, err := moves.handle(wearSvc, w, p1, p1)
	if err != nil {
		t.Fatalf("equip titleA: %v", err)
	}
	if len(plan1) != 5 || plan1[4].Kind != 0 || plan1[4].ID != 2 || plan1[4].Name != "town_appearance_updated" {
		t.Fatalf("equip titleA plan=%+v", plan1)
	}
	// Emptied ordinary bag slot 18 in NOTI 14 space 0 must use 0xffffffff sentinel, not 0.
	if plan1[1].ID != 14 || plan1[1].Payload[0] != 0 || binary.LittleEndian.Uint32(plan1[1].Payload[5:9]) != 0xffffffff {
		t.Fatalf("after equip: space-0 emptied slot template=%#x, want 0xffffffff", binary.LittleEndian.Uint32(plan1[1].Payload[5:9]))
	}
	// NOTI 2 mode 0 must carry the active [serverID, channelID] context bytes at [3:5]
	// so 0x143807f80 matches g_curServerID / g_curChannelID and refreshes CTownUser.
	if plan1[4].Payload[3] != 1 || plan1[4].Payload[4] != 10 {
		t.Fatalf("after equip: mode-0 channelCtx=%v, want [1 10]", plan1[4].Payload[3:5])
	}
	if slots := mode0VisualSlots(t, role.Name, plan1[4].Payload); slots[13] != titleA {
		t.Fatalf("after equip: mode-0 slots=%v, want slot 13=%d", slots, titleA)
	}

	// 2. Swap titleB (bag slot 10) <-> titleA (worn slot 13)
	p2 := encodeCMD19(0, 10, titleB, 3, 13, titleA)
	plan2, err := moves.handle(wearSvc, w, p2, p2)
	if err != nil {
		t.Fatalf("swap titleB: %v", err)
	}
	if len(plan2) != 5 || plan2[4].Kind != 0 || plan2[4].ID != 2 {
		t.Fatalf("swap titleB plan=%+v", plan2)
	}
	if plan2[4].Payload[3] != 1 || plan2[4].Payload[4] != 10 {
		t.Fatalf("after swap: mode-0 channelCtx=%v, want [1 10]", plan2[4].Payload[3:5])
	}
	if slots := mode0VisualSlots(t, role.Name, plan2[4].Payload); slots[13] != titleB {
		t.Fatalf("after swap: mode-0 slots=%v, want slot 13=%d", slots, titleB)
	}

	// 3. Unequip titleB (client sends srcList=0 empty slot 18, dstList=3 slot 13)
	p3 := encodeCMD19(0, 18, 0, 3, 13, titleB)
	plan3, err := moves.handle(wearSvc, w, p3, p3)
	if err != nil {
		t.Fatalf("unequip titleB: %v", err)
	}
	if len(plan3) != 5 || plan3[4].Kind != 0 || plan3[4].ID != 2 {
		t.Fatalf("unequip titleB plan=%+v", plan3)
	}
	if plan3[4].Payload[3] != 1 || plan3[4].Payload[4] != 10 {
		t.Fatalf("after unequip: mode-0 channelCtx=%v, want [1 10]", plan3[4].Payload[3:5])
	}
	if slots := mode0VisualSlots(t, role.Name, plan3[4].Payload); slots[13] != 0 {
		t.Fatalf("after unequip: mode-0 slots=%v, want slot 13 omitted", slots)
	}

	// 3b. Re-equip titleB then drag-unequip using srcList=3 -> dstList=0:
	// ACK 19 must normalize to srcList=0, dstList=3 so the client invokes EquipSwap.
	p3ReEquip := encodeCMD19(0, 18, titleB, 3, 13, 0)
	if _, err := moves.handle(wearSvc, w, p3ReEquip, p3ReEquip); err != nil {
		t.Fatalf("re-equip titleB: %v", err)
	}
	p3Drag := encodeCMD19(3, 13, titleB, 0, 18, 0)
	plan3Drag, err := moves.handle(wearSvc, w, p3Drag, p3Drag)
	if err != nil {
		t.Fatalf("drag-unequip titleB: %v", err)
	}
	if len(plan3Drag) != 5 || plan3Drag[0].ID != 19 || plan3Drag[4].ID != 2 {
		t.Fatalf("drag-unequip plan=%+v", plan3Drag)
	}
	ack := plan3Drag[0].Payload
	if ack[1] != 0 || binary.LittleEndian.Uint16(ack[2:4]) != 18 || ack[8] != 3 || binary.LittleEndian.Uint16(ack[9:11]) != 13 {
		t.Fatalf("drag-unequip ACK 19 not normalized to bag->worn: %x", ack)
	}
	if slots := mode0VisualSlots(t, role.Name, plan3Drag[4].Payload); slots[13] != 0 {
		t.Fatalf("after drag-unequip: mode-0 slots=%v, want slot 13 omitted", slots)
	}

	// 4. Non-visual worn slot (coat slot 14) does NOT emit town_appearance_updated
	pCoat := encodeCMD19(0, 11, coatID, 3, 14, 0)
	planCoat, err := moves.handle(wearSvc, w, pCoat, pCoat)
	if err != nil {
		t.Fatalf("equip coat: %v", err)
	}
	if len(planCoat) != 4 || planCoat[3].ID != protocol.AdventurerFameNotiID {
		t.Fatalf("equip coat unexpected plan=%+v", planCoat)
	}

	// 5. Inside an active dungeon, CMD 19 does not emit town_appearance_updated
	w.activeDungeon = &dungeon.Session{}
	p4 := encodeCMD19(0, 18, titleB, 3, 13, 0)
	plan4, err := moves.handle(wearSvc, w, p4, p4)
	if err != nil {
		t.Fatalf("dungeon equip: %v", err)
	}
	for _, pkt := range plan4 {
		if pkt.ID == 2 && pkt.Kind == 0 {
			t.Fatalf("unexpected town_appearance_updated during active dungeon: %+v", plan4)
		}
	}
	if len(plan4) != 4 || plan4[3].ID != protocol.AdventurerFameNotiID {
		t.Fatalf("dungeon equip plan=%+v", plan4)
	}
}
