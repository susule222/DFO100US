package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCapsuleDrawEvent4189Flow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "ev4189.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	acctID, err := store.DevelopmentAccount(ctx, "capsule-tester")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"experience":0}`), inventory.Bag{
		Version: "ordinary-bag-v1",
		Items: []inventory.BagItem{
			{Slot: 121, Template: ev4189CoinItem, Amount: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "CapsuleTester",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	bagRules := inventory.BagRules{
		Source:            checksum,
		EquipmentSlots:    [2]uint16{9, 64},
		Slots:             map[string][2]uint16{"[throw]": {65, 120}, "[material]": {121, 176}, "[waste]": {65, 120}},
		MissingStackLimit: 1000,
	}
	lootCat := catalog.LootCatalog{Items: map[uint32]catalog.LootItem{}}
	lootCat.Source.Checksum = checksum

	evPath := filepath.Join(dir, "events.json")
	pol := events.DefaultActive()
	rawPol, _ := json.Marshal(pol)
	if err := os.WriteFile(evPath, rawPol, 0600); err != nil {
		t.Fatal(err)
	}

	w := &worldSession{
		account:    acctID,
		role:       role,
		eventsPath: evPath,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
	}
	now := time.Date(2026, 11, 28, 20, 0, 0, 0, time.Local)

	// 1. NOTI 108 must include Event 4189 so Dawn Seria (NPC 500000209) appears in Seria's Room.
	evPayload, err := buildActiveEventsPayload(pol, now)
	if err != nil {
		t.Fatal(err)
	}
	found4189 := false
	for i := 0; i+2 <= len(evPayload); i++ {
		if binary.LittleEndian.Uint16(evPayload[i:i+2]) == protocol.EventIDCapsuleDraw {
			found4189 = true
			break
		}
	}
	if !found4189 {
		t.Fatalf("buildActiveEventsPayload missing EventIDCapsuleDraw (%d)", protocol.EventIDCapsuleDraw)
	}

	// 1b. Entry Space 0x2a restore (NOTI 13) must mirror 2x 590005793 in slot 1 so
	//     DFO_unpacked.exe (0x140242445 / 0x140242e7a -> GetItemCount(0x149788e68, 590005793))
	//     displays 2 coins and enables Insert Coin!.
	djRestore, err := danjiSpaceRestorePayload(w.role.State)
	if err != nil {
		t.Fatalf("danjiSpaceRestorePayload failed: %v", err)
	}
	const wireRowSize = 0xb1
	if len(djRestore) != 3+wireRowSize || djRestore[0] != protocol.DanjiInventorySpace ||
		binary.LittleEndian.Uint16(djRestore[1:3]) != 1 ||
		binary.LittleEndian.Uint16(djRestore[3:5]) != 1 ||
		binary.LittleEndian.Uint32(djRestore[5:9]) != ev4189CoinItem ||
		binary.LittleEndian.Uint32(djRestore[9:13]) != 2 {
		t.Fatalf("unexpected entry Space 0x2a restore for capsule coins: len=%d hdr=%x row=%x", len(djRestore), djRestore[:3], djRestore[3:13])
	}

	// 2. Login init must emit 72-byte NOTI 2338 with drawnIndex=-1, drawnCount=0, 60x '0'.
	initPkts := w.interactiveEventInitPackets(pol, now)
	var init2338 []byte
	for _, p := range initPkts {
		if p.ID == protocol.NotiEv4189CapsuleDraw {
			init2338 = p.Payload
			break
		}
	}
	if len(init2338) != 72 {
		t.Fatalf("expected 72-byte NOTI 2338 on init, got %d bytes", len(init2338))
	}
	if int16(binary.LittleEndian.Uint16(init2338[2:4])) != -1 {
		t.Fatalf("expected init drawnIndex=-1, got %d", int16(binary.LittleEndian.Uint16(init2338[2:4])))
	}
	if binary.LittleEndian.Uint32(init2338[8:12]) != 0 {
		t.Fatalf("expected init drawnCount=0, got %d", binary.LittleEndian.Uint32(init2338[8:12]))
	}
	for i := 0; i < 60; i++ {
		if init2338[12+i] != '0' {
			t.Fatalf("expected slot %d = '0', got %q", i, init2338[12+i])
		}
	}

	// 3. Draw #1 (CMD 681 action=1): consumes 1 coin, picks slot in 0..49, grants exact pool reward.
	draw1, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(uint32(protocol.EventIDCapsuleDraw), 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var noti1, space2a1 []byte
	for _, p := range draw1 {
		if p.ID == protocol.NotiEv4189CapsuleDraw {
			noti1 = p.Payload
		}
		if p.ID == 14 && len(p.Payload) >= 3 && p.Payload[0] == protocol.DanjiInventorySpace {
			space2a1 = p.Payload
		}
	}
	if len(noti1) != 72 {
		t.Fatalf("draw #1 missing 72-byte NOTI 2338")
	}
	if len(space2a1) != 3+2*wireRowSize {
		t.Fatalf("draw #1 missing Space 0x2a NOTI 14 coin sync, got len=%d", len(space2a1))
	}
	slot1Row := space2a1[3+wireRowSize : 3+2*wireRowSize]
	if binary.LittleEndian.Uint16(slot1Row[0:2]) != 1 ||
		binary.LittleEndian.Uint32(slot1Row[2:6]) != ev4189CoinItem ||
		binary.LittleEndian.Uint32(slot1Row[6:10]) != 1 {
		t.Fatalf("draw #1 Space 0x2a slot 1 expected 590005793 x1, got %x", slot1Row[:10])
	}
	idx1 := int(int16(binary.LittleEndian.Uint16(noti1[2:4])))
	if idx1 < 0 || idx1 >= len(ev4189Pool) {
		t.Fatalf("draw #1 invalid drawnIndex=%d", idx1)
	}
	if binary.LittleEndian.Uint32(noti1[8:12]) != 1 {
		t.Fatalf("draw #1 expected drawnCount=1, got %d", binary.LittleEndian.Uint32(noti1[8:12]))
	}
	if noti1[12+idx1] != '1' {
		t.Fatalf("draw #1 expected slot %d marked '1', got %q", idx1, noti1[12+idx1])
	}

	// Verify 1 coin remaining and exact reward item added to bag.
	bag1, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	var coins, wonCount uint32
	expected := ev4189Pool[idx1]
	for _, it := range bag1.Items {
		if it.Template == ev4189CoinItem {
			coins += it.Amount
		}
		if it.Template == expected.Template {
			wonCount += it.Amount
		}
	}
	if coins != 1 {
		t.Fatalf("expected 1 coin remaining after draw #1, got %d", coins)
	}
	if wonCount < expected.Count {
		t.Fatalf("expected reward template %d x%d in bag, got %d", expected.Template, expected.Count, wonCount)
	}

	// 4. Draw #2 must pick a distinct slot and consume the last coin.
	draw2, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(uint32(protocol.EventIDCapsuleDraw), 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var noti2, space2a2 []byte
	for _, p := range draw2 {
		if p.ID == protocol.NotiEv4189CapsuleDraw {
			noti2 = p.Payload
		}
		if p.ID == 14 && len(p.Payload) >= 3 && p.Payload[0] == protocol.DanjiInventorySpace {
			space2a2 = p.Payload
		}
	}
	if len(space2a2) != 3+2*wireRowSize {
		t.Fatalf("draw #2 missing Space 0x2a NOTI 14 coin sync, got len=%d", len(space2a2))
	}
	slot1Row2 := space2a2[3+wireRowSize : 3+2*wireRowSize]
	if binary.LittleEndian.Uint16(slot1Row2[0:2]) != 1 || binary.LittleEndian.Uint32(slot1Row2[2:6]) != 0xffffffff {
		t.Fatalf("draw #2 Space 0x2a slot 1 expected EmptySlotItem(1), got %x", slot1Row2[:10])
	}
	idx2 := int(int16(binary.LittleEndian.Uint16(noti2[2:4])))
	if idx2 < 0 || idx2 >= len(ev4189Pool) || idx2 == idx1 {
		t.Fatalf("draw #2 expected distinct slot from %d, got %d", idx1, idx2)
	}
	if binary.LittleEndian.Uint32(noti2[8:12]) != 2 {
		t.Fatalf("draw #2 expected drawnCount=2, got %d", binary.LittleEndian.Uint32(noti2[8:12]))
	}

	// 5. Draw #3 with 0 coins must NOT draw and return drawnIndex=-1 with drawnCount=2.
	draw3, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(uint32(protocol.EventIDCapsuleDraw), 1), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(draw3) != 1 || draw3[0].ID != protocol.NotiEv4189CapsuleDraw {
		t.Fatalf("expected single NOTI 2338 sync when out of coins, got %d pkts", len(draw3))
	}
	if int16(binary.LittleEndian.Uint16(draw3[0].Payload[2:4])) != -1 || binary.LittleEndian.Uint32(draw3[0].Payload[8:12]) != 2 {
		t.Fatalf("out-of-coins draw must not mutate state: idx=%d count=%d",
			int16(binary.LittleEndian.Uint16(draw3[0].Payload[2:4])),
			binary.LittleEndian.Uint32(draw3[0].Payload[8:12]))
	}

	// 6. Reset (CMD 681 action=2) clears all slots back to '0' and drawnCount=0.
	resetPkts, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(uint32(protocol.EventIDCapsuleDraw), 2), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(resetPkts) != 1 || resetPkts[0].ID != protocol.NotiEv4189CapsuleDraw {
		t.Fatalf("reset expected 1 NOTI 2338 packet, got %d", len(resetPkts))
	}
	rp := resetPkts[0].Payload
	if int16(binary.LittleEndian.Uint16(rp[2:4])) != -1 || binary.LittleEndian.Uint32(rp[8:12]) != 0 {
		t.Fatalf("reset expected drawnIndex=-1 count=0, got idx=%d count=%d",
			int16(binary.LittleEndian.Uint16(rp[2:4])),
			binary.LittleEndian.Uint32(rp[8:12]))
	}
	for i := 0; i < 60; i++ {
		if rp[12+i] != '0' {
			t.Fatalf("after reset expected slot %d = '0', got %q", i, rp[12+i])
		}
	}
}
