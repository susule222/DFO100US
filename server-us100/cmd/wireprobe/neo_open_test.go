package main

import (
	"bytes"
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestNeoDailyOpen(t *testing.T) {
	lootPath := os.Getenv("US100_NEO_LOOT_COPY")
	if lootPath == "" {
		lootPath = "../../configs/us100/loot.generated.json"
	}
	c, e := catalog.LoadLoot(lootPath)
	if e != nil {
		t.Fatal(e)
	}
	r, e := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if e != nil {
		t.Fatal(e)
	}
	svc := &loot.Service{Catalog: c, BagRules: r}
	if _, ok := dailyGiftQuantity(50002547); ok {
		t.Fatal("unknown reward allowed")
	}
	for _, tc := range []struct {
		name     string
		award, n uint32
		slot     uint16
		full     bool
	}{{"core", 10327281, 60, 65, false}, {"crystals", 10327282, 30, 65, false}, {"wrong-slot", 10327281, 60, 66, false}, {"full", 10327281, 60, 65, true}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "role-copy.db")
			if src := os.Getenv("US100_NEO_OPEN_COPY"); src != "" {
				b, e := os.ReadFile(src)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			ctx := context.Background()
			s, e := storage.Open(ctx, storage.Config{DBPath: path})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { s.Close() }()
			if os.Getenv("US100_NEO_OPEN_COPY") == "" {
				e = s.Database().Mutate(func(tx *db100.Tx) error {
					return tx.Put("characters", "48", map[string]any{"id": 48, "account_id": 41, "wire_id": 1, "profession": 0, "config_version": r.Source, "state": map[string]any{"inventory": inventory.Bag{Version: "ordinary-bag-v1", Items: []inventory.BagItem{{Slot: 65, Template: 50002547, Amount: 1}}}}})
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			if tc.full {
				e = s.Database().Mutate(func(tx *db100.Tx) error {
					var row map[string]any
					_, e := tx.Get("characters", "48", &row)
					if e != nil {
						return e
					}
					b := inventory.Bag{Version: "ordinary-bag-v1", Items: []inventory.BagItem{{Slot: 65, Template: 50002547, Amount: 1}}}
					for i := uint16(121); i <= 176; i++ {
						b.Items = append(b.Items, inventory.BagItem{Slot: i, Template: 10327282, Amount: 1000})
					}
					row["state"].(map[string]any)["inventory"] = b
					return tx.Put("characters", "48", row)
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			role, e := s.LiveCharacter(41, 48)
			if e != nil {
				t.Fatal(e)
			}
			q, ok := dailyGiftQuantity(tc.award)
			if !ok || q != tc.n {
				t.Fatal(q, ok)
			}
			grant := neoBoxGrant(svc, tc.slot, 50002547, tc.award, q)
			commit := func(key string) (bool, error) {
				_, ap, e := s.CommitCharacterEvent(ctx, 41, 48, r.Source, key, "US224-neo-daily-open-v1", grant)
				return ap, e
			}
			ap, e := commit("test-daily-1")
			if tc.full || tc.slot != 65 {
				if ap || e == nil {
					t.Fatal("bad selection committed")
				}
				after, e := s.LiveCharacter(41, 48)
				if e != nil || !bytes.Equal(role.State, after.State) {
					t.Fatal("failure changed state", e)
				}
				return
			}
			if e != nil || !ap {
				t.Fatal(ap, e)
			}
			if ap, e = commit("test-daily-1"); e != nil || ap {
				t.Fatal("retry", ap, e)
			}
			if ap, e = commit("test-daily-2"); e == nil || ap {
				t.Fatal("empty box spent again")
			}
			s.Close()
			s, e = storage.Open(ctx, storage.Config{DBPath: path})
			if e != nil {
				t.Fatal(e)
			}
			after, e := s.LiveCharacter(41, 48)
			if e != nil {
				t.Fatal(e)
			}
			b, e := inventory.ReadBag(after.State)
			if e != nil {
				t.Fatal(e)
			}
			before, e := inventory.ReadBag(role.State)
			if e != nil {
				t.Fatal(e)
			}
			count := func(b inventory.Bag, id uint32) uint32 {
				var n uint32
				for _, v := range b.Items {
					if v.Template == id {
						if id == tc.award && (v.Slot < 121 || v.Slot > 176) {
							t.Fatal("not material slot", v)
						}
						n += v.Amount
					}
				}
				return n
			}
			if count(before, 50002547) != count(b, 50002547)+1 || count(b, tc.award) != count(before, tc.award)+q {
				t.Fatal("wrong persisted inventory", b.Items)
			}
		})
	}
}
