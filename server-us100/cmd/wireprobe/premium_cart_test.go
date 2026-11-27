package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
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
