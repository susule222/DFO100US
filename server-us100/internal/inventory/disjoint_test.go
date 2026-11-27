package inventory

import (
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/game/protocol"
	"testing"
)

func testDisjointCatalog() *EquipmentCatalog {
	return &EquipmentCatalog{
		index: map[uint32]EquipmentDefinition{
			24002: {
				ID: 24002,
				Fields: map[string][]pvf.Token{
					"[minimum level]": {{Type: 0, Value: 5}},
					"[rarity]":        {{Type: 0, Value: 1}},
				},
			},
			20003: {
				ID: 20003,
				Fields: map[string][]pvf.Token{
					"[minimum level]": {{Type: 0, Value: 6}},
					"[rarity]":        {{Type: 0, Value: 0}},
				},
			},
			99001: {
				ID: 99001,
				Fields: map[string][]pvf.Token{
					"[minimum level]": {{Type: 0, Value: 100}},
					"[rarity]":        {{Type: 0, Value: 4}},
				},
			},
		},
	}
}

func TestDisjointBagRoutesCubesToWarehouseNotMaterialBag(t *testing.T) {
	cat := testDisjointCatalog()
	bag := Bag{
		Version: "ordinary-bag-v1",
		Items: []BagItem{
			{Slot: 121, Template: 10092947, Amount: 60},
		},
		Equipment: []BagEquipment{
			{Slot: 10, Template: 20003, Durability: 0},
			{Slot: 12, Template: 24002, Durability: 0},
			{Slot: 15, Template: 99001, Durability: 40},
		},
	}
	req := protocol.DisjointRequest{
		Space:          0,
		DisassemblerID: 0xffff,
		Items: []protocol.DisjointItem{
			{Slot: 10, Template: 20003},
			{Slot: 12, Template: 24002},
		},
	}
	got, rewards, deltas, err := DisjointBag(bag, cat, req)
	if err != nil {
		t.Fatalf("DisjointBag failed: %v", err)
	}
	if len(got.Equipment) != 1 || got.Equipment[0].Slot != 15 {
		t.Fatalf("expected only slot 15 remaining, got %+v", got.Equipment)
	}
	// Material bag must stay untouched (cubes go to Account Cube Warehouse).
	if len(got.Items) != 1 || got.Items[0].Template != 10092947 {
		t.Fatalf("ordinary material bag should not receive cube fragments, got %+v", got.Items)
	}
	if len(rewards) < 2 {
		t.Fatalf("expected clear + colored cube rewards, got %+v", rewards)
	}
	if rewards[0].Slot != 0 || rewards[0].Template != ClearCubeFragmentID || rewards[0].Count == 0 {
		t.Fatalf("unexpected first reward: %+v", rewards[0])
	}
	if deltas[ClearCubeFragmentID] != rewards[0].Count {
		t.Fatalf("deltas[3037]=%d, want %d", deltas[ClearCubeFragmentID], rewards[0].Count)
	}
}

func TestDisjointBagRefusesRentalAndMissingSlot(t *testing.T) {
	cat := testDisjointCatalog()
	rentalBag := Bag{
		Version: "ordinary-bag-v1",
		Equipment: []BagEquipment{
			{Slot: 10, Template: 20003, RentalUntil: 9999999999},
		},
	}
	if _, _, _, err := DisjointBag(rentalBag, cat, protocol.DisjointRequest{
		Space: 0, DisassemblerID: 0xffff, Items: []protocol.DisjointItem{{Slot: 10, Template: 20003}},
	}); err == nil {
		t.Fatal("expected rental equipment disassembly to be refused")
	}

	if _, _, _, err := DisjointBag(Bag{Version: "ordinary-bag-v1"}, cat, protocol.DisjointRequest{
		Space: 0, DisassemblerID: 0xffff, Items: []protocol.DisjointItem{{Slot: 10, Template: 20003}},
	}); err == nil {
		t.Fatal("expected missing equipment slot to be refused")
	}
}
