package main

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"encoding/hex"
	"testing"
)

func TestDeleteItemThenOpenAbyssFragmentBox50081632(t *testing.T) {
	// Reproduces the live Character 48 sequence:
	//   1. CMD 18 deletes slot 80 (template 50, Character Safe Upgrade Kit) -> ACK 18 must
	//      precede NOTI 13 so the client pops its InvenMgr transaction lock.
	//   2. CMD 160 opens slot 81 (50081632, 深渊碎片箱 (500)) -> grants 500x 10307438 in Materials.
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{
		{Slot: 80, Template: 50, Amount: 1},
		{Slot: 81, Template: 50081632, Amount: 1},
	})
	liveCmd18, err := hex.DecodeString("0e00000010001a0808011050183220012000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	saved, plan, space, code, err := deleteInventoryItems(
		context.Background(), s, 42, role, []byte("raw-delete-80"), liveCmd18,
	)
	if err != nil {
		t.Fatalf("deleteInventoryItems failed: err=%v code=%d", err, code)
	}
	if space != 0 || code != 0 {
		t.Fatalf("unexpected space=%d code=%d", space, code)
	}
	if len(plan) < 2 || plan[0].ID != protocol.CmdDeleteItem || plan[0].Kind != 1 || plan[1].ID != 13 || plan[1].Kind != 0 {
		t.Fatalf("expected ACK 18 before NOTI 13, got %+v", plan)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Items) != 1 || bag.Items[0].Slot != 81 || bag.Items[0].Template != 50081632 {
		t.Fatalf("expected slot 80 deleted and slot 81 preserved, got %+v", bag.Items)
	}

	// Now open 50081632 (深渊碎片箱 (500)) in slot 81.
	saved2, plan2, box, award, qty, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, saved, []byte("raw-open-50081632"), cmd160Plain(81, 1, 0), nil,
	)
	if err != nil {
		t.Fatalf("openBoosterBox 50081632 failed: %v", err)
	}
	if !applied || box != 50081632 || award != 10307438 || qty != 500 {
		t.Fatalf("unexpected open result applied=%v box=%d award=%d qty=%d", applied, box, award, qty)
	}
	if len(plan2) < 2 || plan2[0].ID != 160 || plan2[1].ID != 13 {
		t.Fatalf("expected ACK 160 + NOTI 13, got %+v", plan2)
	}
	bag2, err := inventory.ReadBag(saved2.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag2.Items) != 1 || bag2.Items[0].Template != 10307438 || bag2.Items[0].Amount != 500 {
		t.Fatalf("expected 500x 10307438 Abyss Fragments, got %+v", bag2.Items)
	}
	if bag2.Items[0].Slot < 121 || bag2.Items[0].Slot > 176 {
		t.Fatalf("expected Abyss Fragments in materials tab (121..176), got slot %d", bag2.Items[0].Slot)
	}
}

func TestDeleteItemPartialStackAndRefusal(t *testing.T) {
	s, _, role := setupBoosterTestStore(t, []inventory.BagItem{
		{Slot: 65, Template: 1113, Amount: 5},
	})
	// Build CMD 18 deleting 2 of 5 from slot 65 (0x41), template 1113 (0x0459 -> varint d9 08).
	// PB: f2=0 (10 00), f3={f1=1(08 01), f2=65(10 41), f3=1113(18 d9 08), f4=2(20 02)} (1a 09 08 01 10 41 18 d9 08 20 02), f4=0 (20 00) -> 15 bytes
	raw, err := hex.DecodeString("0f00000010001a090801104118d908200220000000000000")
	if err != nil {
		t.Fatal(err)
	}
	saved, _, _, _, err := deleteInventoryItems(
		context.Background(), s, 42, role, []byte("raw-partial-65"), raw,
	)
	if err != nil {
		t.Fatalf("partial delete failed: %v", err)
	}
	bag, _ := inventory.ReadBag(saved.State)
	if len(bag.Items) != 1 || bag.Items[0].Amount != 3 {
		t.Fatalf("expected 3 remaining after deleting 2 of 5, got %+v", bag.Items)
	}

	// Deleting more than available must refuse cleanly.
	rawOver, _ := hex.DecodeString("0f00000010001a090801104118d908200420000000000000")
	_, _, _, code, err := deleteInventoryItems(
		context.Background(), s, 42, saved, []byte("raw-over-65"), rawOver,
	)
	if err == nil || code == 0 {
		t.Fatalf("expected refusal when deleting more than held, got err=%v code=%d", err, code)
	}
}
