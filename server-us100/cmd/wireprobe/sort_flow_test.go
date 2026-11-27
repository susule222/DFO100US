package main

import (
	"context"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

func setupSortTestStore(t *testing.T, bag inventory.Bag) (*storage.Store, int64, storage.Character) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "sort.db")})
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
	bag.Version = "ordinary-bag-v1"
	state, err := inventory.SaveBag(json.RawMessage(`{"level":100}`), bag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acc,
		Name:          "SortTester",
		Profession:    0,
		ConfigVersion: checksum,
		State:         state,
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 9
	return store, acc, role
}

func TestSortItemsLiveMaterialMoveAndCycle(t *testing.T) {
	store, acc, role := setupSortTestStore(t, inventory.Bag{
		Items: []inventory.BagItem{
			{Slot: 121, Template: 10327281, Amount: 120},
			{Slot: 122, Template: 590004385, Amount: 210},
			{Slot: 133, Template: 10327283, Amount: 15},
		},
		Equipment: []inventory.BagEquipment{
			{Slot: 10, Template: 10001, Durability: 30},
			{Slot: 12, Template: 10002, Durability: 35},
		},
		Avatars: []inventory.BagEquipment{
			{Slot: 0, Template: 20001},
			{Slot: 5, Template: 20002},
		},
	})
	ctx := context.Background()

	// 1) Replay exact live CMD 20: swap empty slot 125 <-> occupied slot 133
	rawHex := "000302007d00850085007d0000000000"
	plain, _ := hex.DecodeString(rawHex)
	saved, plan, space, err := sortInventory(ctx, store, acc, role, []byte("sort-1"), plain)
	if err != nil || space != 0 {
		t.Fatalf("sort failed: space=%d err=%v", space, err)
	}
	if len(plan) != 2 || plan[0].ID != 14 || plan[1].ID != 20 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	found125 := false
	for _, it := range bag.Items {
		if it.Slot == 133 {
			t.Fatalf("slot 133 should now be empty")
		}
		if it.Slot == 125 && it.Template == 10327283 && it.Amount == 15 {
			found125 = true
		}
	}
	if !found125 {
		t.Fatalf("expected item 10327283 x15 moved to slot 125, got %+v", bag.Items)
	}
	// NOTI 14 must contain slot 125 (10327283) and slot 133 (0xffffffff empty)
	noti14 := plan[0].Payload
	if noti14[0] != 0 || binary.LittleEndian.Uint16(noti14[1:3]) != 2 {
		t.Fatalf("unexpected NOTI 14 header: %x", noti14[:3])
	}
	if binary.LittleEndian.Uint16(noti14[3:5]) != 125 || binary.LittleEndian.Uint32(noti14[5:9]) != 10327283 {
		t.Fatalf("unexpected first row: %x", noti14[3:13])
	}
	off2 := 3 + 0xb1
	if binary.LittleEndian.Uint16(noti14[off2:off2+2]) != 133 || binary.LittleEndian.Uint32(noti14[off2+2:off2+6]) != 0xffffffff {
		t.Fatalf("unexpected second row: %x", noti14[off2:off2+10])
	}

	// 2) Equipment swap: 10 <-> 12 in space 0
	eqReq := make([]byte, 16)
	eqReq[0] = 0
	eqReq[1] = 0
	binary.LittleEndian.PutUint16(eqReq[2:4], 2)
	binary.LittleEndian.PutUint16(eqReq[4:6], 10)
	binary.LittleEndian.PutUint16(eqReq[6:8], 12)
	binary.LittleEndian.PutUint16(eqReq[8:10], 12)
	binary.LittleEndian.PutUint16(eqReq[10:12], 10)
	saved, _, _, err = sortInventory(ctx, store, acc, saved, []byte("sort-2"), eqReq)
	if err != nil {
		t.Fatalf("equipment sort failed: %v", err)
	}
	bag, _ = inventory.ReadBag(saved.State)
	eqBySlot := map[uint16]uint32{}
	for _, eq := range bag.Equipment {
		eqBySlot[eq.Slot] = eq.Template
	}
	if eqBySlot[10] != 10002 || eqBySlot[12] != 10001 {
		t.Fatalf("equipment swap mismatch: %+v", eqBySlot)
	}

	// 3) Avatar bag sort: move slot 5 -> slot 1 in space 1
	avReq := make([]byte, 16)
	avReq[0] = 1
	avReq[1] = 0
	binary.LittleEndian.PutUint16(avReq[2:4], 2)
	binary.LittleEndian.PutUint16(avReq[4:6], 5)
	binary.LittleEndian.PutUint16(avReq[6:8], 1)
	binary.LittleEndian.PutUint16(avReq[8:10], 1)
	binary.LittleEndian.PutUint16(avReq[10:12], 5)
	saved, plan, _, err = sortInventory(ctx, store, acc, saved, []byte("sort-3"), avReq)
	if err != nil {
		t.Fatalf("avatar sort failed: %v", err)
	}
	if plan[0].Payload[0] != 1 {
		t.Fatalf("expected space 1 NOTI 14, got space=%d", plan[0].Payload[0])
	}
	bag, _ = inventory.ReadBag(saved.State)
	avBySlot := map[uint16]uint32{}
	for _, av := range bag.Avatars {
		avBySlot[av.Slot] = av.Template
	}
	if avBySlot[0] != 20001 || avBySlot[1] != 20002 {
		t.Fatalf("avatar sort mismatch: %+v", avBySlot)
	}

	// 4) Collision with unmoved item must be refused cleanly
	badReq := make([]byte, 8)
	badReq[0] = 0
	badReq[1] = 3
	binary.LittleEndian.PutUint16(badReq[2:4], 1)
	binary.LittleEndian.PutUint16(badReq[4:6], 125)
	binary.LittleEndian.PutUint16(badReq[6:8], 121) // 121 is occupied and not moving!
	if _, _, _, err = sortInventory(ctx, store, acc, saved, []byte("sort-bad"), badReq); err == nil {
		t.Fatalf("expected collision with unmoved slot 121 to be refused")
	}
}
