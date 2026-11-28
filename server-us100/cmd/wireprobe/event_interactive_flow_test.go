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
	"path/filepath"
	"testing"
	"time"
)

func makeEventCMD(eventID uint32, args ...uint32) []byte {
	b := make([]byte, 4+len(args)*4)
	binary.LittleEndian.PutUint32(b[0:4], eventID)
	for i, v := range args {
		binary.LittleEndian.PutUint32(b[4+i*4:8+i*4], v)
	}
	return b
}

func TestInteractiveEventInitAndRewardClaims(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "ev_ui.db")})
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
	acctID, err := store.DevelopmentAccount(ctx, "event-tester")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"experience":0}`), inventory.Bag{Version: "ordinary-bag-v1"})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "EventClaimer",
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

	w := &worldSession{
		account:    acctID,
		role:       role,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
	}

	now := time.Date(2026, 11, 27, 22, 30, 0, 0, time.Local)
	pol := events.DefaultActive()

	// 1. Initial sync emits state NOTIs for 520, 512+388, 515, 619, 614, 602
	//    and auto-increments Event 515 daily attendance to day 1 on login.
	initPkts := w.interactiveEventInitPackets(pol, now)
	seenIDs := map[uint16]bool{}
	var init515Days byte
	for _, p := range initPkts {
		seenIDs[p.ID] = true
		if p.ID == protocol.NotiEv515EverydayConnect && len(p.Payload) >= 11 {
			init515Days = p.Payload[2]
		}
	}
	if init515Days != 1 {
		t.Fatalf("expected Ev515 auto-checkin to set days=1 on login, got %d", init515Days)
	}
	for _, want := range []uint16{
		protocol.NotiEv520ReverseHotTime,
		protocol.NotiEv388MissionWeekly,
		protocol.NotiEv388MissionDaily,
		protocol.NotiEv512DungeonMining,
		protocol.NotiEv515EverydayConnect,
		protocol.NotiEv619DailyCheck,
		protocol.NotiEv614Melody,
		protocol.NotiEv602RiaCubeLab,
	} {
		if !seenIDs[want] {
			t.Fatalf("missing initial event state NOTI %d (got %v)", want, seenIDs)
		}
	}

	// 2. Claim Event 520 reward #0 (5M -> 10333092) and reward #1 (10M -> 10333113) via 0-indexed CMD 680
	pkts520a, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(520, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	var gotInvA, got2222A bool
	for _, p := range pkts520a {
		if p.ID == 14 {
			gotInvA = true
		}
		if p.ID == protocol.NotiEv520ReverseHotTime && p.Payload[4] == 1 && p.Payload[5] == 0 && binary.LittleEndian.Uint32(p.Payload[8:12]) == 100 {
			got2222A = true
		}
	}
	if !gotInvA || !got2222A {
		t.Fatalf("Ev520 5M claim expected NOTI 14 + NOTI 2222(claimed1=1,claimed2=0,maxLv=100), got inv=%v 2222=%v", gotInvA, got2222A)
	}
	pkts520b, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(520, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var gotInvB, got2222B bool
	for _, p := range pkts520b {
		if p.ID == 14 {
			gotInvB = true
		}
		if p.ID == protocol.NotiEv520ReverseHotTime && p.Payload[4] == 1 && p.Payload[5] == 1 {
			got2222B = true
		}
	}
	if !gotInvB || !got2222B {
		t.Fatalf("Ev520 10M claim expected NOTI 14 + NOTI 2222(claimed1=1,claimed2=1), got inv=%v 2222=%v", gotInvB, got2222B)
	}

	// 3. Mine Event 512 stage 0 node 0, then node 3, then node 1 (bitmask 1<<node)
	pkts512, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(512, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	var gotMiningInv, got2155 bool
	for _, p := range pkts512 {
		if p.ID == 14 {
			gotMiningInv = true
		}
		if p.ID == protocol.NotiEv512DungeonMining {
			if binary.LittleEndian.Uint32(p.Payload[0:4]) == 9 && binary.LittleEndian.Uint32(p.Payload[4:8]) == (1<<0) {
				got2155 = true
			}
		}
	}
	if !gotMiningInv || !got2155 {
		t.Fatalf("Ev512 mine expected NOTI 14 + NOTI 2155(pickaxes=9,stage0=1), got inv=%v 2155=%v", gotMiningInv, got2155)
	}
	if _, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(512, 0, 3), now); err != nil {
		t.Fatal(err)
	}
	pkts512Node1, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(512, 0, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var gotBitmask bool
	for _, p := range pkts512Node1 {
		if p.ID == protocol.NotiEv512DungeonMining && binary.LittleEndian.Uint32(p.Payload[4:8]) == ((1<<0)|(1<<3)|(1<<1)) {
			gotBitmask = true
		}
	}
	if !gotBitmask {
		t.Fatalf("Ev512 out-of-order mining did not preserve bitmask (1<<0|1<<3|1<<1)")
	}

	// 4. Event 515: claim day 1 (action=1, day=1) + next day login + allRecvBtn (action=3, day=0)
	pkts515, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(515, 1, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var got515Claim bool
	for _, p := range pkts515 {
		if p.ID == protocol.NotiEv515EverydayConnect && p.Payload[2] == 1 && (binary.LittleEndian.Uint32(p.Payload[3:7])&(1<<1)) != 0 {
			got515Claim = true
		}
	}
	if !got515Claim {
		t.Fatalf("Ev515 day 1 claim did not set dailyClaimedMask bit 1")
	}
	// Advance to next calendar day -> allRecvBtn (action=3) should auto-count day 2 and claim day 2.
	nextDay := now.Add(24 * time.Hour)
	pkts515All, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(515, 3, 0), nextDay)
	if err != nil {
		t.Fatal(err)
	}
	var got515All bool
	for _, p := range pkts515All {
		if p.ID == protocol.NotiEv515EverydayConnect && p.Payload[2] == 2 && (binary.LittleEndian.Uint32(p.Payload[3:7])&((1<<1)|(1<<2))) == ((1<<1)|(1<<2)) {
			got515All = true
		}
	}
	if !got515All {
		t.Fatalf("Ev515 action=3 (allRecvBtn) did not claim day 2 on next calendar day")
	}

	// 5. Event 602 query (CMD 681 sub=3), search (sub=1), save (sub=2,1), transfer (CMD 680 sub=1)
	if pktsQ, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(602, 3, 0, 0), now); err != nil || len(pktsQ) != 1 || pktsQ[0].ID != protocol.NotiEv602RiaCubeLab {
		t.Fatalf("Ev602 query failed: pkts=%d err=%v", len(pktsQ), err)
	}
	if _, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(602, 1), now); err != nil {
		t.Fatal(err)
	}
	if pkts602S, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(602, 2, 1), now); err != nil {
		t.Fatal(err)
	} else if len(pkts602S) != 1 || pkts602S[0].ID != protocol.NotiEv602RiaCubeLab || pkts602S[0].Payload[2] != 0 || pkts602S[0].Payload[3] < 1 {
		t.Fatalf("Ev602 save did not encode savedItemIdx=0 and pickedIndices: %x", pkts602S[0].Payload)
	}
	pkts602T, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(602, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var got602Inv, got602Mask bool
	for _, p := range pkts602T {
		if p.ID == 14 {
			got602Inv = true
		}
		if p.ID == protocol.NotiEv602RiaCubeLab && binary.LittleEndian.Uint32(p.Payload[0x1c:0x20]) == 1 && p.Payload[2] == 0xff {
			got602Mask = true
		}
	}
	if !got602Inv || !got602Mask {
		t.Fatalf("Ev602 transfer expected NOTI 14 + transferredMask=1, got inv=%v mask=%v", got602Inv, got602Mask)
	}

	// 6. Event 614 claim weekly mission 0 + Event 619 query/claim/reset + Event 367 daily claim
	if _, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(614, 1, 0), now); err != nil {
		t.Fatal(err)
	}
	// CMD 681 sub=1 on Event 619 is a pure status query and must NOT claim a reward.
	pkts619Q, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(619, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkts619Q) != 1 || pkts619Q[0].ID != protocol.NotiEv619DailyCheck || pkts619Q[0].Payload[8] != 0 || binary.LittleEndian.Uint32(pkts619Q[0].Payload[12:16]) != 0 {
		t.Fatalf("Ev619 CMD 681 sub=1 query mutated state or encoded wrong offsets: %+v", pkts619Q)
	}
	// CMD 680 sub=1 claims daily gift #0 -> p[8]=1 (claimedToday), p[12:16]=1 (pickedMask), p[16]=1 (totalDays).
	pkts619Claim, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(619, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var got619Noti bool
	for _, p := range pkts619Claim {
		if p.ID == protocol.NotiEv619DailyCheck && p.Payload[8] == 1 && binary.LittleEndian.Uint32(p.Payload[12:16]) == 1 && p.Payload[16] == 1 {
			got619Noti = true
		}
	}
	if !got619Noti {
		t.Fatalf("Ev619 claim did not emit expected NOTI 2161 (claimedToday=1 at +8, pickedMask=1 at +12): %+v", pkts619Claim)
	}
	// CMD 681 sub=2 resets gift pool -> p[8]=0, p[12:16]=0, totalDays preserved (=1).
	pkts619Reset, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(619, 2), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkts619Reset) != 1 || pkts619Reset[0].ID != protocol.NotiEv619DailyCheck || pkts619Reset[0].Payload[8] != 0 || binary.LittleEndian.Uint32(pkts619Reset[0].Payload[12:16]) != 0 || pkts619Reset[0].Payload[16] != 1 {
		t.Fatalf("Ev619 reset did not clear claimedToday & pickedMask: %+v", pkts619Reset)
	}
	pkts367, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(367, 1), now)
	if err != nil {
		t.Fatal(err)
	}
	var got367Ack bool
	for _, p := range pkts367 {
		if p.Kind == 1 && p.ID == protocol.CmdEventAction && len(p.Payload) == 45 && binary.LittleEndian.Uint32(p.Payload[41:45]) == 1 {
			got367Ack = true
		}
	}
	if !got367Ack {
		t.Fatalf("Ev367 claim expected 45-byte ACK 680 with popup flag=1")
	}

	// 7. Verify all claimed items actually landed in character bag!
	bag, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	wantTemplates := map[uint32]bool{10333092: false, 10333113: false, 50007754: false, 50007815: false, 50081632: false, 50081692: false, 10326700: false, 50061692: false}
	for _, it := range bag.Items {
		if _, ok := wantTemplates[it.Template]; ok {
			wantTemplates[it.Template] = true
		}
	}
	for tmpl, ok := range wantTemplates {
		if !ok {
			t.Fatalf("expected claimed reward template %d in character bag", tmpl)
		}
	}

	// 8. Use claimed Event 619 item (10326700) via CMD 507 (0x1fb) and verify ACK 507 + bag decrement
	var elixirSlot uint16
	for _, it := range bag.Items {
		if it.Template == 10326700 {
			elixirSlot = it.Slot
			break
		}
	}
	cmd507 := make([]byte, 32)
	binary.LittleEndian.PutUint16(cmd507[0:2], elixirSlot)
	cmd507[2] = 0
	binary.LittleEndian.PutUint32(cmd507[7:11], 0x10c) // [epic drop buff event action]
	plan507, err := w.useStackableAction(cmd507, []byte("cmd507-elixir-1"))
	if err != nil {
		t.Fatalf("useStackableAction CMD 507 failed: %v", err)
	}
	if len(plan507) != 2 || plan507[0].ID != protocol.CmdUseStackableAction || plan507[1].ID != 14 {
		t.Fatalf("unexpected CMD 507 plan: %+v", plan507)
	}
	if plan507[0].Payload[0] != 1 || binary.LittleEndian.Uint16(plan507[0].Payload[1:3]) != elixirSlot || binary.LittleEndian.Uint32(plan507[0].Payload[4:8]) != 0x10c {
		t.Fatalf("unexpected ACK 507 payload: %x", plan507[0].Payload)
	}
	bagAfter507, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range bagAfter507.Items {
		if it.Slot == elixirSlot {
			t.Fatalf("expected slot %d to be consumed after CMD 507, still present: %+v", elixirSlot, it)
		}
	}
}
