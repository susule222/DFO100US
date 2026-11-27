package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/inventory"
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
		Model:             "reference90-bag-v1",
		Source:            checksum,
		Slots:             map[string][2]uint16{"[throw]": {65, 120}, "[material]": {121, 176}},
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
