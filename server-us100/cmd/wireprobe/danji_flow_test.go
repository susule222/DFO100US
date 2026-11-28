package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func setupDanjiTestStore(t *testing.T, pots uint32) (*storage.Store, int64, storage.Character, catalog.LootCatalog, inventory.BagRules) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "danji.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	acc, err := store.DevelopmentAccount(ctx, "probe")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	bag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    50000,
	}
	if pots > 0 {
		bag.Items = []inventory.BagItem{{Slot: 122, Template: danjiPotTemplate, Amount: pots}}
	}
	state, err := inventory.SaveBag(json.RawMessage(`{"level":100}`), bag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acc,
		Name:          "DanjiTester",
		Profession:    0,
		ConfigVersion: checksum,
		State:         state,
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 7
	src := catalog.LootCatalog{
		Source: pvf.ArchiveSnapshot{Checksum: checksum},
		Items:  map[uint32]catalog.LootItem{},
	}
	rules := inventory.BagRules{
		Model:  "reference90-bag-v1",
		Source: checksum,
		Slots: map[string][2]uint16{
			"[throw]":               {65, 120},
			"[material]":            {121, 176},
			"[material expert job]": {233, 288},
			"[avatar emblem]":       {289, 360},
		},
		MissingStackLimit: 1000,
	}
	return store, acc, role, src, rules
}

func makeDanjiCmdPayload(slot int32, count int32, tier byte) []byte {
	p := make([]byte, 24)
	binary.LittleEndian.PutUint32(p[13:17], uint32(slot))
	binary.LittleEndian.PutUint32(p[17:21], uint32(count))
	p[21] = tier
	return p
}

func TestDanjiOpenConsumeRewardAndStack(t *testing.T) {
	store, acc, role, src, rules := setupDanjiTestStore(t, 100)
	ctx := context.Background()

	// Deterministic RNG: returns 0 (always hits Grade S first item 590704452 x1)
	zeroRand := func(uint32) uint32 { return 0 }

	// 1) Open 1x Tier 1 (costs 1 pot, stack +1; client sends slot 0 from space 0x2a)
	cmd1 := makeDanjiCmdPayload(0, 1, 1)
	saved, plan, code, err := openDanjiPot(ctx, store, acc, role, src, rules, []byte("frame-1"), cmd1, zeroRand)
	if err != nil || code != 0 {
		t.Fatalf("open 1x tier 1 failed: code=%d err=%v", code, err)
	}
	if len(plan) != 4 || plan[0].ID != 14 || plan[1].ID != 14 || plan[2].ID != 2279 || plan[3].ID != 2052 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	// Verify space 0x2a update carries slot 0, template 590004385, remaining count 99
	if plan[1].Payload[0] != 0x2a || binary.LittleEndian.Uint32(plan[1].Payload[5:9]) != danjiPotTemplate || binary.LittleEndian.Uint32(plan[1].Payload[9:13]) != 99 {
		t.Fatalf("unexpected space 0x2a update: %x", plan[1].Payload[:13])
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	var potsLeft, sRewards uint32
	for _, it := range bag.Items {
		if it.Template == danjiPotTemplate {
			potsLeft += it.Amount
		}
		if it.Template == 590704452 {
			sRewards += it.Amount
		}
	}
	if potsLeft != 99 || sRewards != 1 {
		t.Fatalf("after 1x tier 1: potsLeft=%d sRewards=%d, want 99/1", potsLeft, sRewards)
	}
	stack, err := loadDanjiStack(store, acc)
	if err != nil || stack != 1 {
		t.Fatalf("stack=%d err=%v, want 1", stack, err)
	}
	if got := binary.LittleEndian.Uint32(plan[2].Payload[0:4]); got != 1 || plan[2].Payload[5] != 1 {
		t.Fatalf("unexpected NOTI 2279 status: %x", plan[2].Payload)
	}

	// 2) Open 10x Tier 3 (costs 30 pots, stack +30 -> 31)
	cmd2 := makeDanjiCmdPayload(0, 10, 3)
	saved, plan, code, err = openDanjiPot(ctx, store, acc, saved, src, rules, []byte("frame-2"), cmd2, zeroRand)
	if err != nil || code != 0 {
		t.Fatalf("open 10x tier 3 failed: code=%d err=%v", code, err)
	}
	bag, _ = inventory.ReadBag(saved.State)
	potsLeft, sRewards = 0, 0
	for _, it := range bag.Items {
		if it.Template == danjiPotTemplate {
			potsLeft += it.Amount
		}
		if it.Template == 590704452 {
			sRewards += it.Amount
		}
	}
	if potsLeft != 69 || sRewards != 11 {
		t.Fatalf("after 10x tier 3: potsLeft=%d sRewards=%d, want 69/11", potsLeft, sRewards)
	}
	// ACK 2052 must carry 10 rewards
	if got := binary.LittleEndian.Uint32(plan[3].Payload[1:5]); got != 10 {
		t.Fatalf("ack reward count=%d, want 10", got)
	}
	stack, _ = loadDanjiStack(store, acc)
	if stack != 31 {
		t.Fatalf("stack=%d, want 31", stack)
	}

	// 3) Replay identical frame-2 must be idempotent (no extra pot deduction)
	savedReplay, _, code, err := openDanjiPot(ctx, store, acc, saved, src, rules, []byte("frame-2"), cmd2, zeroRand)
	if err != nil || code != 0 {
		t.Fatalf("replay failed: code=%d err=%v", code, err)
	}
	bag, _ = inventory.ReadBag(savedReplay.State)
	potsLeft = 0
	for _, it := range bag.Items {
		if it.Template == danjiPotTemplate {
			potsLeft += it.Amount
		}
	}
	if potsLeft != 69 {
		t.Fatalf("idempotent replay deducted pots: potsLeft=%d, want 69", potsLeft)
	}
}

func TestDanjiOpenDepletesSlotAndMilestoneWrap(t *testing.T) {
	store, acc, role, src, rules := setupDanjiTestStore(t, 30)
	ctx := context.Background()
	if err := saveDanjiStack(store, acc, 930); err != nil {
		t.Fatal(err)
	}

	// 10x tier 3 = 30 pots (depletes slot 122 to 0) and advances stack 930 + 30 -> crosses 942 -> wraps to 18!
	cmd := makeDanjiCmdPayload(122, 10, 3)
	saved, plan, code, err := openDanjiPot(ctx, store, acc, role, src, rules, []byte("frame-wrap"), cmd, func(uint32) uint32 { return 500000 })
	if err != nil || code != 0 {
		t.Fatalf("open failed: code=%d err=%v", code, err)
	}
	stack, _ := loadDanjiStack(store, acc)
	if stack != 18 {
		t.Fatalf("wrapped stack=%d, want 18", stack)
	}
	bag, _ := inventory.ReadBag(saved.State)
	var hasPot, hasMilestone bool
	for _, it := range bag.Items {
		if it.Template == danjiPotTemplate {
			hasPot = true
		}
		if it.Template == 590006329 { // 942 milestone reward
			hasMilestone = true
		}
	}
	if hasPot {
		t.Fatalf("expected DANJI pot stack at slot 122 to be removed when reaching 0")
	}
	if !hasMilestone {
		t.Fatalf("expected 942 milestone reward 590006329 in bag")
	}
	// Verify NOTI 14 includes EmptySlotItem(122)
	noti14 := plan[0].Payload
	count := int(binary.LittleEndian.Uint16(noti14[1:3]))
	foundClear122 := false
	for i := 0; i < count; i++ {
		off := 3 + i*0xb1
		slot := binary.LittleEndian.Uint16(noti14[off : off+2])
		tpl := binary.LittleEndian.Uint32(noti14[off+2 : off+6])
		if slot == 122 && tpl == 0xffffffff {
			foundClear122 = true
		}
	}
	if !foundClear122 {
		t.Fatalf("NOTI 14 did not include EmptySlotItem(122) sentinel")
	}
	// Verify space 0x2a update clears slot 0 with 0xffffffff when depleted to 0
	if plan[1].Payload[0] != 0x2a || binary.LittleEndian.Uint16(plan[1].Payload[3:5]) != 0 || binary.LittleEndian.Uint32(plan[1].Payload[5:9]) != 0xffffffff {
		t.Fatalf("expected space 0x2a slot 0 clear sentinel, got %x", plan[1].Payload[:13])
	}

	// Subsequent open with 0 pots must fail cleanly with code 0x101
	_, _, code, err = openDanjiPot(ctx, store, acc, saved, src, rules, []byte("frame-empty"), makeDanjiCmdPayload(122, 1, 1), nil)
	if err == nil || code != 0x101 {
		t.Fatalf("expected insufficient pot refusal 0x101, got code=%#x err=%v", code, err)
	}
}

func TestDanjiOpenWireGradeCountAndDirectContract(t *testing.T) {
	store, acc, role, src, rules := setupDanjiTestStore(t, 20)
	ctx := context.Background()

	// Find exact weight offset for 590006273 (Tactician's Contract 7D) and 7742 x50 (Canna's Pudding x50) in Grade B pool.
	var offsetTactician, offsetPudding50 uint32
	var accW uint32
	for _, it := range danjiGradeBPool {
		if it.Template == 590006273 && offsetTactician == 0 {
			offsetTactician = accW
		}
		if it.Template == 7742 && it.Count == 50 && offsetPudding50 == 0 {
			offsetPudding50 = accW
		}
		accW += it.Weight
	}
	if offsetTactician == 0 || offsetPudding50 == 0 {
		t.Fatalf("failed to locate expected Grade B entries: tactician=%d pudding50=%d", offsetTactician, offsetPudding50)
	}

	// Deterministic RNG sequence for 2 rolls on Tier 1:
	// Roll 1: gradeRoll=900000 (Grade B), itemRoll=offsetTactician -> 590006273 x1 (direct contract!)
	// Roll 2: gradeRoll=900000 (Grade B), itemRoll=offsetPudding50 -> 7742 x50 (bag potion!)
	seq := []uint32{900000, offsetTactician, 900000, offsetPudding50}
	idx := 0
	rng := func(limit uint32) uint32 {
		v := seq[idx%len(seq)]
		idx++
		if limit > 0 {
			return v % limit
		}
		return v
	}

	// Open 2x via 1x twice (protocol only allows 1 or 10; test 1x for contract and 1x for pudding x50)
	saved, plan1, code, err := openDanjiPot(ctx, store, acc, role, src, rules, []byte("frame-contract"), makeDanjiCmdPayload(0, 1, 1), rng)
	if err != nil || code != 0 {
		t.Fatalf("open contract roll failed: code=%d err=%v", code, err)
	}
	// ACK 2052 must carry (tpl=590006273, grade=2, count=1)
	ack1 := plan1[3].Payload
	if len(ack1) != 11 {
		t.Fatalf("ack1 len=%d, want 11 (%x)", len(ack1), ack1)
	}
	if tpl := binary.LittleEndian.Uint32(ack1[5:9]); tpl != 590006273 {
		t.Fatalf("ack1 template=%d, want 590006273", tpl)
	}
	if ack1[9] != protocol.DanjiGradeB || ack1[10] != 1 {
		t.Fatalf("ack1 grade/count=(%d,%d), want (2,1)", ack1[9], ack1[10])
	}
	// Contract item must NOT enter bag, must activate on account, and plan must include NOTI 66 for kind 27.
	bag1, _ := inventory.ReadBag(saved.State)
	for _, it := range bag1.Items {
		if it.Template == 590006273 {
			t.Fatalf("contract booster 590006273 should activate directly and not enter bag")
		}
	}
	foundNoti66 := false
	for _, pkt := range plan1 {
		if pkt.Kind == 0 && pkt.ID == 66 && len(pkt.Payload) == 11 && pkt.Payload[2] == storage.TacticianContractKind {
			secs := binary.LittleEndian.Uint64(pkt.Payload[3:11])
			if secs >= 7*86400-10 {
				foundNoti66 = true
			}
		}
	}
	if !foundNoti66 {
		t.Fatalf("expected NOTI 66 for Tactician Contract 7D in plan: %+v", plan1)
	}

	// Second roll: Canna's Pudding 7742 x50 -> ACK 2052 must encode (tpl=7742, grade=2, count=50)
	saved, plan2, code, err := openDanjiPot(ctx, store, acc, saved, src, rules, []byte("frame-pudding"), makeDanjiCmdPayload(0, 1, 1), rng)
	if err != nil || code != 0 {
		t.Fatalf("open pudding roll failed: code=%d err=%v", code, err)
	}
	ack2 := plan2[3].Payload
	if tpl := binary.LittleEndian.Uint32(ack2[5:9]); tpl != 7742 || ack2[9] != protocol.DanjiGradeB || ack2[10] != 50 {
		t.Fatalf("ack2=(tpl=%d, grade=%d, count=%d), want (7742, 2, 50)", binary.LittleEndian.Uint32(ack2[5:9]), ack2[9], ack2[10])
	}
	bag2, _ := inventory.ReadBag(saved.State)
	var puddingSlot uint16
	var puddingAmt uint32
	for _, it := range bag2.Items {
		if it.Template == 7742 {
			puddingSlot = it.Slot
			puddingAmt = it.Amount
		}
	}
	if puddingAmt != 50 || puddingSlot < 65 || puddingSlot > 120 {
		t.Fatalf("expected 7742 x50 in [throw] bag, got slot=%d amt=%d", puddingSlot, puddingAmt)
	}
}

func TestBoosterBoxesAndConsumables(t *testing.T) {
	store, acc, role, src, rules := setupDanjiTestStore(t, 0)
	ctx := context.Background()

	// Seed bag with:
	// - slot 65: 7742 x2 (Canna's Pudding, absent from loot.generated.json)
	// - slot 66: 590006229 x2 (Kaleido Box 10x booster)
	// - slot 67: 590006262 x1 (Material booster -> 10327283 x100 into [material] tab)
	// - slot 68: 590006265 x1 (Random Emblem Box)
	// - slot 69: 590704452 x1 (Selection box -> 590704453 x3)
	// - slot 70: 590006273 x1 (Bagged Tactician's Contract 7D booster)
	// - slot 71: 33 x1 (Raw Conqueror's Contract 7D stackable via CMD 44)
	bag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    50000,
		Items: []inventory.BagItem{
			{Slot: 65, Template: 7742, Amount: 2},
			{Slot: 66, Template: 590006229, Amount: 2},
			{Slot: 67, Template: 590006262, Amount: 1},
			{Slot: 68, Template: 590006265, Amount: 1},
			{Slot: 69, Template: 590704452, Amount: 1},
			{Slot: 70, Template: 590006273, Amount: 1},
			{Slot: 71, Template: 33, Amount: 1},
		},
	}
	state, err := inventory.SaveBag(role.State, bag)
	if err != nil {
		t.Fatal(err)
	}
	role, _, err = store.CommitCharacterEvent(ctx, acc, role.ID, src.Source.Checksum, "seed-booster-test", "test", func(storage.Character) (json.RawMessage, json.RawMessage, error) {
		return state, json.RawMessage(`{}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 7

	lootSvc := &loot.Service{
		Store:    store,
		Catalog:  src,
		Rules:    loot.Rules{Model: "reference90-loot-v1"},
		BagRules: rules,
	}
	ws := &worldSession{
		account: acc,
		role:    role,
		loot:    lootSvc,
	}

	// 1) CMD 44: drink Canna's Pudding (7742) twice from slot 65 with Instance=0; second drink must clear slot 65.
	makeUse44 := func(slot uint16, tpl uint32) []byte {
		p := make([]byte, 16)
		binary.LittleEndian.PutUint16(p[0:2], slot)
		p[2] = 0
		binary.LittleEndian.PutUint32(p[7:11], tpl)
		return p
	}
	plan, err := ws.useStackable(makeUse44(65, 7742), []byte("cmd44-drink-1"))
	if err != nil || len(plan) != 2 || plan[0].ID != 44 || plan[1].ID != 14 {
		t.Fatalf("first pudding drink failed: plan=%+v err=%v", plan, err)
	}
	plan, err = ws.useStackable(makeUse44(65, 7742), []byte("cmd44-drink-2"))
	if err != nil || len(plan) != 2 {
		t.Fatalf("second pudding drink failed: plan=%+v err=%v", plan, err)
	}
	b, _ := inventory.ReadBag(ws.role.State)
	for _, it := range b.Items {
		if it.Slot == 65 {
			t.Fatalf("expected slot 65 cleared after drinking last pudding, still has %+v", it)
		}
	}

	// 2) CMD 44: use raw Conqueror's Contract 7D (33) at slot 71 -> activates kind 22 + NOTI 66
	plan, err = ws.useStackable(makeUse44(71, 33), []byte("cmd44-conqueror"))
	if err != nil {
		t.Fatalf("raw contract use failed: %v", err)
	}
	foundConq := false
	for _, pkt := range plan {
		if pkt.Kind == 0 && pkt.ID == 66 && len(pkt.Payload) == 11 && pkt.Payload[2] == storage.ConquerorContractKind {
			foundConq = true
		}
	}
	if !foundConq {
		t.Fatalf("expected NOTI 66 for Conqueror Contract after CMD 44, got %+v", plan)
	}

	// 3) CMD 160: open DANJI fixed booster 590006229 x2 -> 15 x20
	makeOpen160 := func(slot, count uint16) []byte {
		p := make([]byte, 8)
		binary.LittleEndian.PutUint16(p[0:2], slot)
		binary.LittleEndian.PutUint16(p[2:4], count)
		return p
	}
	makeSelect160 := func(slot, count uint16, choice uint32) []byte {
		p := make([]byte, 16)
		binary.LittleEndian.PutUint16(p[0:2], slot)
		binary.LittleEndian.PutUint16(p[2:4], count)
		binary.LittleEndian.PutUint32(p[8:12], choice)
		return p
	}
	zeroRand := func(uint32) uint32 { return 0 }

	saved, plan, box, award, qty, applied, err := openBoosterBox(ctx, store, nil, lootSvc, acc, ws.role, []byte("open-66"), makeOpen160(66, 2), zeroRand)
	if err != nil || !applied || box != 590006229 || award != 15 || qty != 20 {
		t.Fatalf("open 590006229 x2 failed: box=%d award=%d qty=%d applied=%v err=%v", box, award, qty, applied, err)
	}
	if len(plan) < 2 || plan[0].ID != 160 || plan[1].ID != 13 {
		t.Fatalf("unexpected booster plan: %+v", plan)
	}

	// 4) CMD 160: open material booster 590006262 -> 10327283 x100 placed in [material] slot 121..176
	saved, _, _, award, qty, _, err = openBoosterBox(ctx, store, nil, lootSvc, acc, saved, []byte("open-67"), makeOpen160(67, 1), zeroRand)
	if err != nil || award != 10327283 || qty != 100 {
		t.Fatalf("open 590006262 failed: award=%d qty=%d err=%v", award, qty, err)
	}
	b, _ = inventory.ReadBag(saved.State)
	matOK := false
	for _, it := range b.Items {
		if it.Template == 10327283 && it.Amount == 100 && it.Slot >= 121 && it.Slot <= 176 {
			matOK = true
		}
	}
	if !matOK {
		t.Fatalf("expected 10327283 x100 in [material] tab, bag=%+v", b.Items)
	}

	// 5) CMD 160: open random emblem booster 590006265 -> first emblem 590004703 x1 in Emblem tab (289..360)
	saved, _, _, award, qty, _, err = openBoosterBox(ctx, store, nil, lootSvc, acc, saved, []byte("open-68"), makeOpen160(68, 1), zeroRand)
	if err != nil || award != 590004703 || qty != 1 {
		t.Fatalf("open 590006265 failed: award=%d qty=%d err=%v", award, qty, err)
	}
	b, _ = inventory.ReadBag(saved.State)
	emblemOK := false
	for _, it := range b.Items {
		if it.Template == 590004703 && it.Amount == 1 && it.Slot >= inventory.EmblemSlotStart && it.Slot <= inventory.EmblemSlotEnd {
			emblemOK = true
		}
	}
	if !emblemOK {
		t.Fatalf("expected emblem 590004703 in emblem tab (289..360), bag=%+v", b.Items)
	}

	// 6) CMD 160: open selection box 590704452 choosing 590704453 -> x3
	saved, _, _, award, qty, _, err = openBoosterBox(ctx, store, nil, lootSvc, acc, saved, []byte("open-69"), makeSelect160(69, 1, 590704453), zeroRand)
	if err != nil || award != 590704453 || qty != 3 {
		t.Fatalf("open selection 590704452 failed: award=%d qty=%d err=%v", award, qty, err)
	}

	// 7) CMD 160: open bagged contract booster 590006273 -> activates Tactician 7D + NOTI 66, nothing added to bag
	saved, plan, _, award, qty, _, err = openBoosterBox(ctx, store, nil, lootSvc, acc, saved, []byte("open-70"), makeOpen160(70, 1), zeroRand)
	if err != nil || award != 43 || qty != 7 {
		t.Fatalf("open bagged contract 590006273 failed: award=%d qty=%d err=%v", award, qty, err)
	}
	foundTact := false
	for _, pkt := range plan {
		if pkt.Kind == 0 && pkt.ID == 66 && len(pkt.Payload) == 11 && pkt.Payload[2] == storage.TacticianContractKind {
			foundTact = true
		}
	}
	if !foundTact {
		t.Fatalf("expected NOTI 66 for Tactician Contract after opening bagged 590006273")
	}
	b, _ = inventory.ReadBag(saved.State)
	for _, it := range b.Items {
		if it.Slot == 70 || it.Template == 590006273 || it.Template == 43 {
			t.Fatalf("bagged contract booster should not leave contract item in bag: %+v", it)
		}
	}
}
