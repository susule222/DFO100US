package main

import (
	"bytes"
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUS100NeoMemberClaims(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "isolation.db")
	input := os.Getenv("US100_NEO_COPY")
	if input != "" {
		data, e := os.ReadFile(input)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	open := func() *storage.Store {
		s, e := storage.Open(ctx, storage.Config{DBPath: path})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	store := open()
	defer func() { store.Close() }()
	chars, e := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	gear, e := inventory.LoadEquipmentCatalog("../../configs/us100/equipment.generated.json", chars.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	bag, e := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if e != nil {
		t.Fatal(e)
	}
	ls := &loot.Service{Catalog: catalog.LootCatalog{Source: chars.Source}, BagRules: bag}
	ws := &inventory.WearService{Catalog: gear, Professions: chars, BagRules: bag}
	if input == "" {
		e = store.Database().Mutate(func(tx *db100.Tx) error {
			if e := tx.Put("us224_account_premium", "41", map[byte]int64{118: time.Now().Unix() + 3600}); e != nil {
				return e
			}
			return tx.Put("characters", "48", map[string]any{"id": 48, "account_id": 41, "profession": 0, "wire_id": 1, "config_version": chars.Source.Checksum, "state": map[string]any{"level": 6}})
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	get := func() storage.Character {
		r, e := store.LiveCharacter(41, 48)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	w := worldSession{role: get()}
	claim := func(cmd uint16, p []byte, ok bool) {
		t.Helper()
		orig := get().State
		plan, e := w.claimNeo(cmd, p, 41, 48, store, ls, ws)
		if ok {
			if e != nil || len(plan) != 2 || plan[0].ID != 14 || plan[1].Payload[0] != 1 {
				t.Fatalf("claim %d: %+v %v", cmd, plan, e)
			}
		} else if e == nil || !bytes.Equal(orig, get().State) {
			t.Fatalf("invalid claim %d changed state: %v", cmd, e)
		}
	}
	claim(1201, []byte{0}, false)
	claim(1200, []byte{1, 1, 0, 255}, false)
	// Force a full consumables partition on the isolated row; restore that row before retry.
	old := get().State
	b, e := inventory.ReadBag(old)
	if e != nil {
		t.Fatal(e)
	}
	lo := bag.Slots["[throw]"]
	used := map[uint16]bool{}
	for _, r := range b.Items {
		used[r.Slot] = true
	}
	for s := lo[0]; s <= lo[1]; s++ {
		if !used[s] {
			b.Items = append(b.Items, inventory.BagItem{Slot: s, Template: 10, Amount: 1})
		}
	}
	setState := func(state json.RawMessage) {
		t.Helper()
		if e := store.Database().Mutate(func(tx *db100.Tx) error {
			var row map[string]json.RawMessage
			_, e := tx.Get("characters", "48", &row)
			if e != nil {
				return e
			}
			row["state"] = state
			return tx.Put("characters", "48", row)
		}); e != nil {
			t.Fatal(e)
		}
	}
	full, e := inventory.SaveBag(old, b)
	if e != nil {
		t.Fatal(e)
	}
	setState(full)
	claim(1201, nil, false)
	setState(old)
	claim(1201, nil, true)
	claim(1200, []byte{1, 1, 0, 1}, true)
	store.Close()
	store = open()
	w.role = get()
	claim(1201, nil, false)
	claim(1200, []byte{1, 1, 0, 1}, false)
	b, e = inventory.ReadBag(get().State)
	if e != nil {
		t.Fatal(e)
	}
	qty := uint32(0)
	for _, it := range b.Items {
		if it.Template == 50002547 {
			qty += it.Amount
			if it.Slot < lo[0] || it.Slot > lo[1] {
				t.Fatal("wrong gift partition")
			}
		}
	}
	held := 0
	for i, it := range b.Equipment {
		if it.Template == 101010938 {
			held++
			if it.Durability != 45 || it.RentalUntil <= time.Now().Unix() || inventory.EquipmentRow(it)[10] != 7 {
				t.Fatal("wrong rental properties", it)
			}
			b.Equipment[i].RentalUntil = 1
		}
	}
	if qty != 1 || held != 1 {
		t.Fatalf("expected a gift and katana: %d %d", qty, held)
	}
	expired, _ := inventory.SaveBag(get().State, b)
	b, e = inventory.ReadBag(expired)
	if e != nil {
		t.Fatal(e)
	}
	for _, it := range b.Equipment {
		if it.Template == 101010938 {
			t.Fatal("expired rental retained")
		}
	}
	e = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("us224_account_premium", "41", map[byte]int64{118: time.Now().Unix() - 1, 120: time.Now().Unix() + 3600})
	})
	if e != nil {
		t.Fatal(e)
	}
	setState(old)
	claim(1200, []byte{1, 1, 0, 1}, false)
}
