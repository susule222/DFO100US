package main

import (
	"bytes"
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"encoding/binary"
	"testing"
)

func TestSortLockAndUnlockPersistAcrossSortAndRestore(t *testing.T) {
	store, acc, role := setupSortTestStore(t, inventory.Bag{
		Items: []inventory.BagItem{
			{Slot: 121, Template: 1001, Amount: 50},
			{Slot: 122, Template: 1002, Amount: 10},
		},
		Equipment: []inventory.BagEquipment{
			{Slot: 9, Template: 20001, Durability: 30},
		},
		Avatars: []inventory.BagEquipment{
			{Slot: 2, Template: 50001},
		},
	})

	// Lock stackable at space 0 slot 121 (CMD 705)
	req1 := []byte{0, 121, 0, 0, 0, 0, 0, 0}
	role, plan, err := setSortLock(context.Background(), store, acc, role, []byte("f1"), req1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Kind != 1 || plan[0].ID != protocol.CmdSortLock || !bytes.Equal(plan[0].Payload, []byte{1, 0, 121, 0}) {
		t.Fatalf("unexpected sort lock plan: %+v", plan)
	}

	// Lock equipment at space 0 slot 9 and avatar at space 1 slot 2
	role, _, err = setSortLock(context.Background(), store, acc, role, []byte("f2"), []byte{0, 9, 0, 0, 0, 0, 0, 0}, true)
	if err != nil {
		t.Fatal(err)
	}
	role, _, err = setSortLock(context.Background(), store, acc, role, []byte("f3"), []byte{1, 2, 0, 0, 0, 0, 0, 0}, true)
	if err != nil {
		t.Fatal(err)
	}

	// Verify byte 0x55 (ItemSortLockOffset) is set in NOTI 13 restore rows
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		t.Fatal(err)
	}
	foundLockedItem, foundUnlockedItem, foundLockedEquip := false, false, false
	for _, r := range bag.Rows() {
		slot := binary.LittleEndian.Uint16(r[:2])
		switch slot {
		case 121:
			foundLockedItem = r[protocol.ItemSortLockOffset] == 1
		case 122:
			foundUnlockedItem = r[protocol.ItemSortLockOffset] == 0
		case 9:
			foundLockedEquip = r[protocol.ItemSortLockOffset] == 1
		}
	}
	if !foundLockedItem || !foundUnlockedItem || !foundLockedEquip {
		t.Fatalf("sort lock wire flags wrong: lockedItem=%v unlockedItem=%v lockedEquip=%v", foundLockedItem, foundUnlockedItem, foundLockedEquip)
	}
	if rows := bag.AvatarRows(); len(rows) != 1 || rows[0][protocol.ItemSortLockOffset] != 1 {
		t.Fatalf("avatar sort lock wire flag missing")
	}

	// Verify CMD 20 sort preserves sort lock on unmoved locked slot and moves unlocked item
	sortBody := []byte{
		0, 2, // space 0, tab 2
		1, 0, // count 1
		122, 0, 123, 0, // 122 -> 123
	}
	role, sortPlan, _, err := sortInventory(context.Background(), store, acc, role, []byte("f4"), sortBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(sortPlan) < 2 || sortPlan[0].ID != 13 || sortPlan[len(sortPlan)-1].ID != 20 {
		t.Fatalf("unexpected sort plan: %+v", sortPlan)
	}

	// Unlock space 0 slot 121 (CMD 706)
	role, plan, err = setSortLock(context.Background(), store, acc, role, []byte("f5"), req1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].ID != protocol.CmdSortUnlock || !bytes.Equal(plan[0].Payload, []byte{1, 0, 121, 0}) {
		t.Fatalf("unexpected sort unlock plan: %+v", plan)
	}
	bag, _ = inventory.ReadBag(role.State)
	for _, it := range bag.Items {
		if it.Slot == 121 && it.SortLocked {
			t.Fatal("slot 121 should be unlocked")
		}
	}
}

func TestItemSafetyLockLifecycleAndEntryRestore(t *testing.T) {
	store, acc, role := setupSortTestStore(t, inventory.Bag{
		Equipment: []inventory.BagEquipment{
			{Slot: 9, Template: 20001, Durability: 30},
		},
		Worn: []inventory.BagEquipment{
			{Slot: 12, Template: 20002, Durability: 35},
		},
	})

	// Lock equipment at space 0 slot 9 (CMD 267)
	role, plan, err := handleItemLockCommand(context.Background(), store, acc, role, protocol.CmdItemLock, []byte("l1"), []byte{0, 9, 0, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Kind != 1 || plan[0].ID != protocol.CmdItemLock || !bytes.Equal(plan[0].Payload, []byte{1, 0, 9, 0}) {
		t.Fatalf("unexpected item lock plan: %+v", plan)
	}

	// Verify entry restore produces NOTI 251 with (space=0, slot=9, state=1)
	noti, err := itemLocksOnEntry(role.State)
	if err != nil {
		t.Fatal(err)
	}
	wantNoti := []byte{1, 0, 0, 9, 0, 1}
	if !bytes.Equal(noti, wantNoti) {
		t.Fatalf("NOTI 251 = %x, want %x", noti, wantNoti)
	}

	// Verify CMD 20 sort re-emits NOTI 251 so safety lock survives full NOTI 13 rebuild
	sortBody := []byte{0, 0, 1, 0, 9, 0, 10, 0}
	role, sortPlan, _, err := sortInventory(context.Background(), store, acc, role, []byte("l2"), sortBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(sortPlan) != 3 || sortPlan[0].ID != 13 || sortPlan[1].ID != protocol.NotiItemLockList || sortPlan[2].ID != 20 {
		t.Fatalf("expected NOTI 13 + NOTI 251 + ACK 20, got %+v", sortPlan)
	}
	if !bytes.Equal(sortPlan[1].Payload, []byte{1, 0, 0, 10, 0, 1}) {
		t.Fatalf("post-sort NOTI 251 = %x, want slot 10 locked", sortPlan[1].Payload)
	}

	// Unlock space 0 slot 10 (CMD 268) -> ACK 268 (0s) + NOTI 252
	role, plan, err = handleItemLockCommand(context.Background(), store, acc, role, protocol.CmdItemUnlock, []byte("l3"), []byte{0, 10, 0, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].ID != protocol.CmdItemUnlock || plan[1].ID != protocol.NotiItemUnlocked {
		t.Fatalf("unexpected unlock plan: %+v", plan)
	}
	if !bytes.Equal(plan[1].Payload, []byte{0, 10, 0}) {
		t.Fatalf("NOTI 252 = %x", plan[1].Payload)
	}
	noti, err = itemLocksOnEntry(role.State)
	if err != nil || noti != nil {
		t.Fatalf("expected nil NOTI 251 after unlock, got %x err=%v", noti, err)
	}
}
