package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/dungeon"
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

func makeEventCMD(eventID uint32, args ...uint32) []byte {
	b := make([]byte, 4+len(args)*4)
	binary.LittleEndian.PutUint32(b[0:4], eventID)
	for i, v := range args {
		binary.LittleEndian.PutUint32(b[4+i*4:8+i*4], v)
	}
	return b
}

func TestInteractiveEventStepByStepProgressionAndClaims(t *testing.T) {
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
		Name:          "EventStepper",
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

	t0 := time.Date(2026, 11, 28, 18, 0, 0, 0, time.Local)

	// 1. Fresh login starts all timers/energy/pickaxes at 0 (NOT pre-completed!)
	initPkts := w.interactiveEventInitPackets(pol, t0)
	var saw520Zero, saw512Zero, saw619Zero, saw602Zero, saw614Day1 bool
	for _, p := range initPkts {
		switch p.ID {
		case protocol.NotiEv520ReverseHotTime:
			if binary.LittleEndian.Uint32(p.Payload[0:4]) == 0 {
				saw520Zero = true
			}
		case protocol.NotiEv512DungeonMining:
			if binary.LittleEndian.Uint32(p.Payload[0:4]) == 0 {
				saw512Zero = true
			}
		case protocol.NotiEv619DailyCheck:
			if binary.LittleEndian.Uint32(p.Payload[4:8]) == 0 {
				saw619Zero = true
			}
		case protocol.NotiEv602RiaCubeLab:
			if binary.LittleEndian.Uint32(p.Payload[0x2c:0x30]) == 0 {
				saw602Zero = true
			}
		case protocol.NotiEv614Melody:
			if p.Payload[0] == 1 && p.Payload[1] == 0 && p.Payload[2] == 0 && p.Payload[3] == 0 {
				saw614Day1 = true
			}
		}
	}
	if !saw520Zero || !saw512Zero || !saw619Zero || !saw602Zero || !saw614Day1 {
		t.Fatalf("fresh login must start step-by-step from 0: 520=%v 512=%v 619=%v 602=%v 614=%v",
			saw520Zero, saw512Zero, saw619Zero, saw602Zero, saw614Day1)
	}

	// 2. Premature claims MUST be refused before prerequisites are met!
	pktsEarly520, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(520, 0), t0.Add(1*time.Minute))
	if err != nil || pktsEarly520[0].Name != "event_520_not_ready" {
		t.Fatalf("Ev520 5M at 1 min must be refused, got %+v err=%v", pktsEarly520, err)
	}
	pktsEarly512, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(512, 0, 0), t0.Add(1*time.Minute))
	if err != nil || pktsEarly512[0].Name != "event_512_not_ready" {
		t.Fatalf("Ev512 mine with 0 pickaxes must be refused, got %+v err=%v", pktsEarly512, err)
	}
	pktsEarly602, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(602, 1), t0.Add(1*time.Minute))
	if err != nil || pktsEarly602[0].Name != "event_602_search_not_ready" {
		t.Fatalf("Ev602 search at 0%% energy must be refused, got %+v err=%v", pktsEarly602, err)
	}
	pktsEarly619, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(619, 1), t0.Add(1*time.Minute))
	if err != nil || pktsEarly619[0].Name != "event_619_not_ready" {
		t.Fatalf("Ev619 daily claim at 1 min must be refused, got %+v err=%v", pktsEarly619, err)
	}
	pktsEarly614, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(614, 1, 3), t0.Add(1*time.Minute))
	if err != nil || pktsEarly614[0].Name != "event_614_not_ready" {
		t.Fatalf("Ev614 mission 3 at 0 clears must be refused, got %+v err=%v", pktsEarly614, err)
	}

	// 3. Ev520 step-by-step: at 5 min (300s) -> 5M unlocks, 10M STILL refused; at 10 min (600s) -> 10M unlocks!
	t5m := t0.Add(5 * time.Minute)
	pkts520a, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(520, 0), t5m)
	if err != nil {
		t.Fatal(err)
	}
	var got520aInv bool
	for _, p := range pkts520a {
		if p.ID == 14 {
			got520aInv = true
		}
	}
	if !got520aInv {
		t.Fatalf("Ev520 5M at 5m should succeed and grant item")
	}
	pkts520bEarly, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(520, 1), t5m)
	if err != nil || pkts520bEarly[0].Name != "event_520_not_ready" {
		t.Fatalf("Ev520 10M at 5m must still be refused, got %+v", pkts520bEarly)
	}
	t10m := t0.Add(10 * time.Minute)
	pkts520b, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(520, 1), t10m)
	if err != nil {
		t.Fatal(err)
	}
	var got520bInv bool
	for _, p := range pkts520b {
		if p.ID == 14 {
			got520bInv = true
		}
	}
	if !got520bInv {
		t.Fatalf("Ev520 10M at 10m should succeed and grant item")
	}

	// 4. Ev512/388 step-by-step: 30 min online grants 1 pickaxe; skipping node 0 -> node 1 refused; node 0 succeeds!
	t30m := t0.Add(30 * time.Minute)
	w.onInteractiveEventTick(pol, t30m)
	pktsSkip512, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(512, 0, 1), t30m)
	if err != nil || pktsSkip512[0].Name != "event_512_not_ready" {
		t.Fatalf("Ev512 skipping node 0 to node 1 must be refused, got %+v", pktsSkip512)
	}
	pktsMine0, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(512, 0, 0), t30m)
	if err != nil {
		t.Fatal(err)
	}
	var got512Inv, got512Mask bool
	for _, p := range pktsMine0 {
		if p.ID == 14 {
			got512Inv = true
		}
		if p.ID == protocol.NotiEv512DungeonMining && binary.LittleEndian.Uint32(p.Payload[0:4]) == 0 && binary.LittleEndian.Uint32(p.Payload[4:8]) == 1 {
			got512Mask = true
		}
	}
	if !got512Inv || !got512Mask {
		t.Fatalf("Ev512 node 0 should consume 1 pickaxe and set bit 0, got inv=%v mask=%v", got512Inv, got512Mask)
	}

	// 5. Ev602 step-by-step: 3 ordinary dungeon clears charge 0 -> 34 -> 68 -> 100%, then search -> save -> transfer!
	w.activeDungeon = &dungeon.Session{Definition: catalog.DungeonDefinition{ID: 3, BasisLevel: 50}}
	w.onInteractiveEventClear(t30m)
	w.onInteractiveEventClear(t30m)
	w.onInteractiveEventClear(t30m)
	if _, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(602, 1), t30m); err != nil {
		t.Fatal(err)
	}
	if _, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(602, 2, 1), t30m); err != nil {
		t.Fatal(err)
	}
	pkts602T, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(602, 1), t30m)
	if err != nil {
		t.Fatal(err)
	}
	var got602Inv bool
	for _, p := range pkts602T {
		if p.ID == 14 {
			got602Inv = true
		}
	}
	if !got602Inv {
		t.Fatalf("Ev602 transfer after charge+search+save should grant item")
	}
	// Milestone 1 (requires 2 transfers) must still be refused after 1 transfer!
	pkts602M1, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(602, 2, 1), t30m)
	if err != nil || pkts602M1[0].Name != "event_602_milestone_not_ready" {
		t.Fatalf("Ev602 milestone 1 after 1 contribution must be refused, got %+v", pkts602M1)
	}

	// 6. Ev619 step-by-step: 30m online + 3 clears (+15m) = 45m -> still locked; +15m (60m total) -> unlocks!
	// Milestone 0 (5 days) must STILL be refused after 1 day!
	t45m := t30m.Add(15 * time.Minute)
	pkts619Ok, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(619, 1), t45m)
	if err != nil {
		t.Fatal(err)
	}
	var got619Inv bool
	for _, p := range pkts619Ok {
		if p.ID == 14 {
			got619Inv = true
		}
	}
	if !got619Inv {
		t.Fatalf("Ev619 daily claim at 60m effective time (45m online + 3*5m clear bonus) should succeed")
	}
	pkts619BonusEarly, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(619, 2, 0), t45m)
	if err != nil || pkts619BonusEarly[0].Name != "event_619_bonus_not_ready" {
		t.Fatalf("Ev619 5-day bonus on day 1 must be refused, got %+v", pkts619BonusEarly)
	}

	// 7. Ev614 step-by-step: Lv95+ clear unlocks Mission 1; claiming it sets TotalCleared=1 (not 4), so Line 0 (4) stays locked!
	w.activeDungeon = &dungeon.Session{Definition: catalog.DungeonDefinition{ID: 100, BasisLevel: 100}}
	w.onInteractiveEventClear(t45m)
	pkts614M1, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(614, 1, 1), t45m)
	if err != nil {
		t.Fatal(err)
	}
	var got614Total1 bool
	for _, p := range pkts614M1 {
		if p.ID == protocol.NotiEv614Melody && p.Payload[8] == 1 {
			got614Total1 = true
		}
	}
	if !got614Total1 {
		t.Fatalf("Ev614 claiming 1 mission must increment TotalCleared to 1 (not 4)")
	}
	pkts614LineEarly, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(614, 2, 0), t45m)
	if err != nil || pkts614LineEarly[0].Name != "event_614_line_not_ready" {
		t.Fatalf("Ev614 line 0 (4 missions) must be refused at TotalCleared=1, got %+v", pkts614LineEarly)
	}

	// 8. Ev515 day 1 check-in: day 1 succeeds, day 2 stays locked; Ev367 succeeds once/day and refuses duplicate same day
	pkts515, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(515, 1, 1), t45m)
	if err != nil {
		t.Fatal(err)
	}
	var got515Day1 bool
	for _, p := range pkts515 {
		if p.ID == protocol.NotiEv515EverydayConnect && (binary.LittleEndian.Uint32(p.Payload[3:7])&(1<<1)) != 0 {
			got515Day1 = true
		}
	}
	if !got515Day1 {
		t.Fatalf("Ev515 day 1 claim should set bit 1")
	}
	pkts367, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(367, 1), t45m)
	if err != nil || len(pkts367) < 2 {
		t.Fatalf("Ev367 first daily claim should succeed, got %d pkts err=%v", len(pkts367), err)
	}
	pkts367Dup, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(367, 1), t45m)
	if err != nil || pkts367Dup[0].Name != "event_367_already_claimed" {
		t.Fatalf("Ev367 second claim same day must be refused, got %+v", pkts367Dup)
	}

	// 9. Verify all legitimately unlocked items landed in bag
	bag, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	wantTemplates := map[uint32]bool{10333092: false, 10333113: false, 50007754: false, 50007815: false, 50081632: false, 50081693: false, 10326700: false, 50061692: false}
	for _, it := range bag.Items {
		if _, ok := wantTemplates[it.Template]; ok {
			wantTemplates[it.Template] = true
		}
	}
	for tmpl, ok := range wantTemplates {
		if !ok {
			t.Fatalf("expected earned reward template %d in character bag", tmpl)
		}
	}

	// 10. Event 500 (Forge of Souls): CMD 643 daily gift 78 grants 50060816,
	//     and CMD 681 reinforces slot 0 (+0 -> +1 at 100% rate) consuming 35x 50060799,
	//     and CMD 680 claims milestone reward 0 (50060817).
	giftReq := make([]byte, 2)
	binary.LittleEndian.PutUint16(giftReq, ev500GiftDailyID)
	giftPkts, err := w.handleEventGiftCommand(ctx, giftReq, t45m)
	if err != nil {
		t.Fatalf("Ev500 CMD 643 daily gift failed: %v", err)
	}
	var gotGiftAck, gotGiftNoti bool
	for _, p := range giftPkts {
		if p.ID == protocol.CmdRequestEventGift && len(p.Payload) == 4 && p.Payload[0] == 1 && binary.LittleEndian.Uint16(p.Payload[1:3]) == ev500GiftDailyID && p.Payload[3] == 1 {
			gotGiftAck = true
		}
		if p.ID == protocol.NotiEventGiftUpdate {
			gotGiftNoti = true
		}
	}
	if !gotGiftAck || !gotGiftNoti {
		t.Fatalf("Ev500 CMD 643 expected ACK 643 + NOTI 559, got ack=%v noti=%v", gotGiftAck, gotGiftNoti)
	}
	// Duplicate claim same day must be refused
	dupGift, err := w.handleEventGiftCommand(ctx, giftReq, t45m)
	if err != nil || len(dupGift) == 0 || dupGift[0].Payload[0] != 0 {
		t.Fatalf("Ev500 duplicate daily gift must be refused, got %+v err=%v", dupGift, err)
	}
	// Grant 100x Soul Stone (50060799) and reinforce slot 0 via CMD 681 (Event 500, args=[0,0,0])
	if _, err := w.grantActivityReward(ctx, "ev500_test_stones", "us100-test-v1", 0, ev500MaterialItem, 100, 0, w.activityDay(t45m), 0); err != nil {
		t.Fatalf("seed Soul Stones failed: %v", err)
	}
	forgePkts, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(500, 0, 0, 0), t45m)
	if err != nil {
		t.Fatalf("Ev500 CMD 681 forge failed: %v", err)
	}
	var gotForgeAck, gotNoti1870 bool
	for _, p := range forgePkts {
		if p.ID == protocol.CmdEventQuery && len(p.Payload) == 45 && p.Payload[0] == 1 && binary.LittleEndian.Uint32(p.Payload[9:13]) == 1 {
			gotForgeAck = true
		}
		if p.ID == protocol.NotiEv500SoulAnvil && len(p.Payload) == 64 && binary.LittleEndian.Uint32(p.Payload[0:4]) == 1 && binary.LittleEndian.Uint32(p.Payload[4:8]) == 1 {
			gotNoti1870 = true
		}
	}
	if !gotForgeAck || !gotNoti1870 {
		t.Fatalf("Ev500 forge expected ACK 681(success=1) + NOTI 1870(accum=1,lv=1), got ack=%v 1870=%v", gotForgeAck, gotNoti1870)
	}
	// Claim cumulative reward 0 via CMD 680
	rewPkts, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(500, 0), t45m)
	if err != nil {
		t.Fatalf("Ev500 CMD 680 reward claim failed: %v", err)
	}
	var gotRewMask bool
	for _, p := range rewPkts {
		if p.ID == protocol.NotiEv500SoulAnvil && (binary.LittleEndian.Uint32(p.Payload[40:44])&1) != 0 {
			gotRewMask = true
		}
	}
	if !gotRewMask {
		t.Fatalf("Ev500 reward 0 claim should set rewardMask bit 0")
	}

	// Resonance (CMD 681 args=[1, 0, 0]): first resonance is free (cost=0) and sets CharName in NOTI 1870.
	syncPkts, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(500, 1, 0, 0), t45m)
	if err != nil {
		t.Fatalf("Ev500 CMD 681 resonance failed: %v", err)
	}
	var gotSyncNoti bool
	for _, p := range syncPkts {
		if p.ID == protocol.NotiEv500SoulAnvil && len(p.Payload) == 64+len(w.role.Name) && p.Payload[44] == 1 && binary.LittleEndian.Uint16(p.Payload[45:47]) == 1 {
			nameLen := binary.LittleEndian.Uint32(p.Payload[48:52])
			if string(p.Payload[52:52+nameLen]) == w.role.Name {
				gotSyncNoti = true
			}
		}
	}
	if !gotSyncNoti {
		t.Fatalf("Ev500 resonance expected NOTI 1870 with active=1, count=1, charName=%q", w.role.Name)
	}

	// Extract Soul Stone (Window 0x2ac CMD 681 args=[2, 1, 0]): grants +1 weapon reinforce ticket 50060834,
	// marks slot 0 Extracted=1, and blocks both duplicate extract and further forging on slot 0.
	extPkts, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(500, 2, 1, 0), t45m)
	if err != nil {
		t.Fatalf("Ev500 CMD 681 extract failed: %v", err)
	}
	var gotExtAck, gotExtNoti bool
	for _, p := range extPkts {
		if p.ID == protocol.CmdEventQuery && len(p.Payload) == 45 && p.Payload[0] == 1 && binary.LittleEndian.Uint32(p.Payload[5:9]) == 2 {
			gotExtAck = true
		}
		if p.ID == protocol.NotiEv500SoulAnvil && len(p.Payload) >= 64 && p.Payload[4+9] == 1 {
			gotExtNoti = true
		}
	}
	if !gotExtAck || !gotExtNoti {
		t.Fatalf("Ev500 extract expected ACK 681 + NOTI 1870(extracted=1), got ack=%v noti=%v", gotExtAck, gotExtNoti)
	}
	bagAfterExt, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	var hasTicket50060834 bool
	for _, it := range bagAfterExt.Items {
		if it.Template == 50060834 && it.Amount >= 1 {
			hasTicket50060834 = true
		}
	}
	if !hasTicket50060834 {
		t.Fatalf("Ev500 extract (+1 stone) should grant weapon reinforce ticket 50060834")
	}
	if dupExt, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(500, 2, 1, 0), t45m); err != nil || len(dupExt) == 0 || dupExt[0].Payload[0] != 0 {
		t.Fatalf("Ev500 duplicate extract must be refused, got %+v err=%v", dupExt, err)
	}
	if postExtForge, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(500, 0, 0, 0), t45m); err != nil || len(postExtForge) == 0 || postExtForge[0].Payload[0] != 0 {
		t.Fatalf("Ev500 forging an extracted slot must be refused, got %+v err=%v", postExtForge, err)
	}

	// Dungeon clear Soul Stone drop (50060799) when HellParty.Enabled
	var stonesBefore uint32
	for _, it := range bagAfterExt.Items {
		if it.Template == ev500MaterialItem {
			stonesBefore += it.Amount
		}
	}
	w.activeDungeon = &dungeon.Session{
		RunID:      "ev500-clear-drop-test-run",
		Definition: catalog.DungeonDefinition{ID: 3, BasisLevel: 100},
	}
	clearPkts := w.onActivityClear(ctx)
	if len(clearPkts) == 0 {
		t.Fatalf("expected dungeon clear activity packets")
	}
	bagAfterClear, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	var stonesAfter uint32
	for _, it := range bagAfterClear.Items {
		if it.Template == ev500MaterialItem {
			stonesAfter += it.Amount
		}
	}
	if stonesAfter-stonesBefore != 40 {
		t.Fatalf("Ev500 high-tier dungeon clear Soul Stone drop = +%d, want +40", stonesAfter-stonesBefore)
	}

	// 11. Event 392 (Daily DFO Planner / Window 0x29f):
	//     - Init emits NOTI 1789 (unlocking Ev392UI::OnOpen readiness flags)
	//     - Claiming before selecting or clearing is refused
	//     - CMD 681 [1, 2] selects Mission 2 (proper dungeon clear, target 4)
	//     - Clearing 3 dungeons is not enough (refused); 4th clear unlocks daily reward 50031683
	//     - Step bonus 1 (req 4 completions) remains refused after 1 completion, and succeeds once 4 completions are reached
	var gotNoti1789Init bool
	for _, p := range initPkts {
		if p.ID == protocol.NotiEv392OneDayPlan && len(p.Payload) == 14 && p.Payload[1] == 0 && p.Payload[5] == 4 {
			gotNoti1789Init = true
		}
	}
	if !gotNoti1789Init {
		t.Fatalf("interactiveEventInitPackets must emit NOTI 1789 (len=14) when DailyChallenge.Enabled")
	}
	if early392, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(392, 1, 2), t45m); err != nil || early392[0].Name != "event_392_daily_not_ready" {
		t.Fatalf("Ev392 daily claim before selecting/clearing must be refused, got %+v err=%v", early392, err)
	}
	selPkts, err := w.handleEventCommand(ctx, protocol.CmdEventQuery, makeEventCMD(392, 1, 2), t45m)
	if err != nil {
		t.Fatalf("Ev392 CMD 681 select mission 2 failed: %v", err)
	}
	var got392Selected bool
	for _, p := range selPkts {
		if p.ID == protocol.NotiEv392OneDayPlan && len(p.Payload) == 14 && p.Payload[1] == 2 && p.Payload[2] == 0 {
			got392Selected = true
		}
	}
	if !got392Selected {
		t.Fatalf("Ev392 select mission 2 should emit NOTI 1789 with selected=2, progress=0")
	}
	for i := 0; i < 3; i++ {
		w.onInteractiveEventClear(t45m)
	}
	if early392_3, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(392, 1, 2), t45m); err != nil || early392_3[0].Name != "event_392_daily_not_ready" {
		t.Fatalf("Ev392 mission 2 at 3/4 clears must still be refused, got %+v err=%v", early392_3, err)
	}
	w.onInteractiveEventClear(t45m)
	claim392, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(392, 1, 2), t45m)
	if err != nil {
		t.Fatalf("Ev392 mission 2 daily claim at 4/4 clears failed: %v", err)
	}
	var got392DailyNoti bool
	for _, p := range claim392 {
		if p.ID == protocol.NotiEv392OneDayPlan && len(p.Payload) == 14 && p.Payload[3] == 1 && p.Payload[8] == 1 {
			got392DailyNoti = true
		}
	}
	if !got392DailyNoti {
		t.Fatalf("Ev392 daily claim should set dailyClaimed=1 and step[1].completed=1 in NOTI 1789")
	}
	if stepEarly, err := w.handleEventCommand(ctx, protocol.CmdEventAction, makeEventCMD(392, 4, 1), t45m); err != nil || stepEarly[0].Name != "event_392_step_not_ready" {
		t.Fatalf("Ev392 step bonus 1 at 1/4 completions must be refused, got %+v err=%v", stepEarly, err)
	}
	bagAfter392, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	var has392Reward bool
	for _, it := range bagAfter392.Items {
		if it.Template == 50031683 && it.Amount >= 1 {
			has392Reward = true
		}
	}
	if !has392Reward {
		t.Fatalf("Ev392 daily claim should grant reward 50031683")
	}
}
