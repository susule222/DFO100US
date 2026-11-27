package storage

import (
	"context"
	"dfolan/internal/db100"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCartContractPaidOnceAndPersists(t *testing.T) {
	ctx := context.Background()
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "test.db")}
	s, e := Open(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	base := time.Now().Unix() + 7*86400
	e = s.db.Mutate(func(tx *db100.Tx) error {
		if e := tx.Put("characters", "9", characterRow{ID: 9, AccountID: 4, State: json.RawMessage("{}")}); e != nil {
			return e
		}
		if e := tx.Put("account_currency", "4", currencyRow{AccountID: 4, Cera: 1500}); e != nil {
			return e
		}
		return tx.Put("us224_account_premium", "4", map[byte]int64{92: base, 118: 1, 22: base})
	})
	if e != nil {
		t.Fatal(e)
	}
	lines := []CartLine{{Product: 3500015, Template: 10000390, Cera: 160}, {Product: 3500032, Template: 50002918, Cera: 990}}
	del := func(c Character, _ []CartLine) (json.RawMessage, json.RawMessage, error) {
		return c.State, json.RawMessage("[]"), nil
	}
	for i := 0; i < 2; i++ {
		_, ok, e := s.CompletePremiumCart(ctx, 4, 9, "paid", lines, del)
		if e != nil || ok != (i == 0) {
			t.Fatalf("paid %d: %v %v", i, ok, e)
		}
	}
	_, _, e = s.CompletePremiumCart(ctx, 4, 9, "cannot-pay", lines, del)
	if e == nil {
		t.Fatal("overdraw")
	}
	_, _, e = s.CompletePremiumCart(ctx, 4, 9, "failed-other", lines[:1], func(Character, []CartLine) (json.RawMessage, json.RawMessage, error) {
		return nil, nil, errors.New("delivery failed")
	})
	if e == nil {
		t.Fatal("failed cart")
	}
	s.Close()
	s, e = Open(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var ends map[byte]int64
	_, e = s.db.Get("us224_account_premium", "4", &ends)
	if e != nil || ends[92] != base+15*86400 || ends[22] != base || ends[118] < base-7*86400+30*86400 || ends[118] > time.Now().Unix()+30*86400 || len(ends) != 3 {
		t.Fatal(ends, e)
	}
	var cur currencyRow
	_, e = s.db.Get("account_currency", "4", &cur)
	if e != nil || cur.Cera != 350 || len(s.db.Keys("premium_cart_receipts")) != 1 {
		t.Fatal(cur, e)
	}
	p, e := s.NeoPremiumSeconds(4)
	if e != nil || len(p) != 3 {
		t.Fatal(p, e)
	}
}
