package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestPremiumDirectEveryListing(t *testing.T) {
	const sum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	rules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	apply := premiumDelivery(catalog.LootCatalog{Source: pvf.ArchiveSnapshot{Checksum: sum}}, rules)
	for id, l := range premiumListings {
		b := make([]byte, 24)
		b[2] = 1
		b[4] = 255
		b[5] = 255
		binary.LittleEndian.PutUint32(b[6:], id)
		rows, err := premiumOrder(b)
		if err != nil || len(rows) != 1 || rows[0] != l {
			t.Fatalf("listing %d: %v", id, err)
		}
		st, e := inventory.SaveBag(json.RawMessage("{}"), inventory.Bag{Version: "ordinary-bag-v1", Gold: 600000})
		if e != nil {
			t.Fatal(e)
		}
		state, delivery, e := apply(storage.Character{State: st, ConfigVersion: sum}, rows)
		if e != nil {
			t.Fatal(e)
		}
		bag, e := inventory.ReadBag(state)
		if e != nil || bag.Gold != 600000-l.Gold {
			t.Fatalf("bad gold or bag: %d", id)
		}
		if term, ok := l.Contract(); ok {
			var ds []struct {
				ContractKind byte `json:"contract_kind"`
				Days         int64
				Slot         *int
			}
			if len(bag.Items) != 0 || json.Unmarshal(delivery, &ds) != nil || len(ds) != 1 || ds[0].ContractKind != term.Kind || ds[0].Days != term.Days || ds[0].Slot != nil {
				t.Fatalf("wrong activation %d", id)
			}
		} else if len(bag.Items) != 1 || bag.Items[0].Template != l.Template || bag.Items[0].Amount != 1 {
			t.Fatalf("wrong delivery %d", id)
		}
		b[10] = 1
		if _, e = premiumOrder(b); e == nil {
			t.Fatal("accepted unsupported option")
		}
		b[10] = 0
		b[23] = 1
		if _, e = premiumOrder(b); e == nil {
			t.Fatal("accepted nonzero padding")
		}
	}
}

func TestCeraShopDanjiPotCartGoldAndAvatarPurchase(t *testing.T) {
	const sum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	rules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	apply := premiumDelivery(catalog.LootCatalog{Source: pvf.ArchiveSnapshot{Checksum: sum}}, rules)

	// Exact live-captured 2-item DANJI Pot 100 EA (3000254) cart request from events.jsonl.
	liveCartHex := "00000200ff00bec72d000000000000000000ff00bec72d000000000000000000"
	rawCart, err := hex.DecodeString(liveCartHex)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := premiumOrder(rawCart)
	if err != nil || len(lines) != 2 {
		t.Fatalf("live DANJI cart refused: lines=%+v err=%v", lines, err)
	}
	for i, l := range lines {
		if l.Product != 3000254 || l.Template != 590004385 || l.Quantity != 100 || l.Cera != 7900 || l.Gold != 0 {
			t.Fatalf("line %d mismatch: %+v", i, l)
		}
	}
	st, err := inventory.SaveBag(json.RawMessage("{}"), inventory.Bag{Version: "ordinary-bag-v1", Gold: 500000})
	if err != nil {
		t.Fatal(err)
	}
	nextState, _, err := apply(storage.Character{State: st, ConfigVersion: sum}, lines)
	if err != nil {
		t.Fatalf("DANJI pot delivery failed: %v", err)
	}
	bag, err := inventory.ReadBag(nextState)
	if err != nil || len(bag.Items) != 1 || bag.Items[0].Template != 590004385 || bag.Items[0].Amount != 200 {
		t.Fatalf("expected 200 stacked DANJI pots, got %+v err=%v", bag.Items, err)
	}

	// Gold-priced shop item (Avatar Dye Remover 3000819, 300,000 Gold) + Avatar with stat option bytes (3107733, 120 CERA).
	mixed := make([]byte, 32)
	mixed[2] = 2
	// Line 0: 3000819 (gold)
	mixed[4], mixed[5] = 255, 0
	binary.LittleEndian.PutUint32(mixed[6:], 3000819)
	// Line 1: 3107733 (avatar) with non-zero option byte at +6
	mixed[18], mixed[19] = 255, 1
	binary.LittleEndian.PutUint32(mixed[20:], 3107733)
	mixed[24] = 3
	mixedLines, err := premiumOrder(mixed)
	if err != nil || len(mixedLines) != 2 || mixedLines[0].Gold != 300000 || mixedLines[1].Kind != "avatar" || mixedLines[1].Cera != 120 {
		t.Fatalf("mixed gold+avatar order failed: %+v err=%v", mixedLines, err)
	}
	nextState2, _, err := apply(storage.Character{State: nextState, ConfigVersion: sum}, mixedLines)
	if err != nil {
		t.Fatalf("mixed delivery failed: %v", err)
	}
	bag2, err := inventory.ReadBag(nextState2)
	if err != nil || bag2.Gold != 200000 || len(bag2.Avatars) != 1 || bag2.Avatars[0].Template != 515540507 {
		t.Fatalf("unexpected mixed bag: gold=%d avatars=%+v err=%v", bag2.Gold, bag2.Avatars, err)
	}
	if avPayload, err := inventory.AvatarPayload(nextState2); err != nil || len(avPayload) == 0 {
		t.Fatalf("avatar payload failed: %v", err)
	}
}

func TestCeraShopInventoryExpansionStages(t *testing.T) {
	const sum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	rules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	apply := premiumDelivery(catalog.LootCatalog{Source: pvf.ArchiveSnapshot{Checksum: sum}}, rules)
	makeOrder := func(product uint32) []storage.CartLine {
		b := make([]byte, 24)
		b[2], b[4], b[5] = 1, 255, 255
		binary.LittleEndian.PutUint32(b[6:], product)
		lines, err := premiumOrder(b)
		if err != nil {
			t.Fatalf("order %d failed: %v", product, err)
		}
		return lines
	}
	st0, err := inventory.SaveBag(json.RawMessage("{}"), inventory.Bag{Version: "ordinary-bag-v1"})
	if err != nil {
		t.Fatal(err)
	}
	// Stage 2 before Stage 1 must be refused.
	if _, _, err := apply(storage.Character{State: st0, ConfigVersion: sum}, makeOrder(3000148)); err == nil {
		t.Fatal("expected Stage 2 purchase before Stage 1 to fail")
	}
	// Stage 1 (3000147 / 2660296, 40 CERA) expands 0 -> 8 without dropping a dummy item in bag.
	st1, del1, err := apply(storage.Character{State: st0, ConfigVersion: sum}, makeOrder(3000147))
	if err != nil {
		t.Fatalf("Stage 1 purchase failed: %v", err)
	}
	bag1, err := inventory.ReadBag(st1)
	if err != nil || bag1.ExpandedSlots != 8 || len(bag1.Items) != 0 {
		t.Fatalf("unexpected Stage 1 bag: expanded=%d items=%+v del=%s err=%v", bag1.ExpandedSlots, bag1.Items, string(del1), err)
	}
	// Re-buying Stage 1 must be refused.
	if _, _, err := apply(storage.Character{State: st1, ConfigVersion: sum}, makeOrder(3000147)); err == nil {
		t.Fatal("expected duplicate Stage 1 purchase to fail")
	}
	// Stage 2 (3000148 / 2660297, 90 CERA) expands 8 -> 16.
	st2, _, err := apply(storage.Character{State: st1, ConfigVersion: sum}, makeOrder(3000148))
	if err != nil {
		t.Fatalf("Stage 2 purchase failed: %v", err)
	}
	bag2, err := inventory.ReadBag(st2)
	if err != nil || bag2.ExpandedSlots != 16 || len(bag2.Items) != 0 {
		t.Fatalf("unexpected Stage 2 bag: expanded=%d items=%+v err=%v", bag2.ExpandedSlots, bag2.Items, err)
	}
	// Further expansion beyond max (16) must be refused.
	if _, _, err := apply(storage.Character{State: st2, ConfigVersion: sum}, makeOrder(3000148)); err == nil {
		t.Fatal("expected purchase past max stage to fail")
	}
}
