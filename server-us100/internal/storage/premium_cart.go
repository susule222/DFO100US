package storage

import (
	"context"
	"dfolan/internal/db100"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type CartLine struct {
	Product, Template, Gold uint32
	Cera                    int64
}

// Exact server listings joined to premiumlist_new.etc; single-reward wrappers
// resolve to the underlying contract. Other products remain physical items.
type ContractTerm struct {
	Kind byte
	Days int64
}

var shopTerms = map[CartLine]ContractTerm{
	{Product: 3400021, Template: 590004900, Gold: 0, Cera: 380}:   {73, 7},
	{Product: 3400022, Template: 590004901, Gold: 0, Cera: 710}:   {73, 15},
	{Product: 3400023, Template: 590004902, Gold: 0, Cera: 990}:   {73, 30},
	{Product: 3400004, Template: 590004687, Gold: 0, Cera: 380}:   {125, 7},
	{Product: 3400005, Template: 590004689, Gold: 0, Cera: 710}:   {125, 15},
	{Product: 3400006, Template: 590004691, Gold: 0, Cera: 990}:   {125, 30},
	{Product: 3500033, Template: 50042664, Gold: 0, Cera: 400}:    {120, 7},
	{Product: 3500034, Template: 50042665, Gold: 0, Cera: 770}:    {120, 14},
	{Product: 3500026, Template: 50002533, Gold: 0, Cera: 200}:    {118, 3},
	{Product: 3500027, Template: 50002534, Gold: 0, Cera: 380}:    {118, 7},
	{Product: 3500028, Template: 50002535, Gold: 0, Cera: 710}:    {118, 15},
	{Product: 3500032, Template: 50002918, Gold: 0, Cera: 990}:    {118, 30},
	{Product: 3500000, Template: 44, Gold: 0, Cera: 150}:          {27, 3},
	{Product: 3500001, Template: 45, Gold: 0, Cera: 280}:          {27, 7},
	{Product: 3500002, Template: 46, Gold: 0, Cera: 525}:          {27, 15},
	{Product: 3500007, Template: 31, Gold: 0, Cera: 120}:          {22, 3},
	{Product: 3500008, Template: 33, Gold: 0, Cera: 225}:          {22, 7},
	{Product: 3500009, Template: 34, Gold: 0, Cera: 420}:          {22, 15},
	{Product: 3500011, Template: 2660409, Gold: 0, Cera: 180}:     {79, 3},
	{Product: 3500012, Template: 2660410, Gold: 0, Cera: 335}:     {79, 7},
	{Product: 3500013, Template: 2660411, Gold: 0, Cera: 630}:     {79, 15},
	{Product: 3500017, Template: 10000388, Gold: 0, Cera: 45}:     {92, 3},
	{Product: 3500016, Template: 10000389, Gold: 0, Cera: 85}:     {92, 7},
	{Product: 3000678, Template: 10000389, Gold: 500000, Cera: 0}: {92, 7},
	{Product: 3500015, Template: 10000390, Gold: 0, Cera: 160}:    {92, 15},
}

func (l CartLine) Contract() (ContractTerm, bool) { v, ok := shopTerms[l]; return v, ok }

var shopPremiumKinds = []byte{22, 27, 73, 79, 92, 118, 120, 125}

func extendCartContracts(tx *db100.Tx, account int64, lines []CartLine) error {
	var ends map[byte]int64
	now := time.Now().Unix()
	for _, l := range lines {
		v, ok := l.Contract()
		if !ok {
			continue
		}
		if ends == nil {
			if _, err := tx.Get("us224_account_premium", idKey(account), &ends); err != nil {
				return err
			}
			if ends == nil {
				ends = map[byte]int64{}
			}
		}
		base := ends[v.Kind]
		if base < 0 || base > 1<<62 {
			return fmt.Errorf("invalid contract end time")
		}
		if base < now {
			base = now
		}
		ends[v.Kind] = base + v.Days*86400
	}
	if ends == nil {
		return nil
	}
	return tx.Put("us224_account_premium", idKey(account), ends)
}

type CartReceipt struct {
	Character int64
	Lines     []CartLine
	Delivery  json.RawMessage
}
type CartDeliver func(Character, []CartLine) (json.RawMessage, json.RawMessage, error)

// One purchase request is one commit. The role callback must deduct any gold
// from the same bag it fills. Never call the single-item cashier in a loop.
func (s *Store) CompletePremiumCart(ctx context.Context, account, role int64, key string, lines []CartLine, deliver CartDeliver) (out CartReceipt, applied bool, err error) {
	if account <= 0 || role <= 0 || len(lines) == 0 || len(lines) > 10 || len(key) == 0 || len(key) > 132 || deliver == nil {
		return out, false, fmt.Errorf("invalid cart")
	}
	var cost int64
	for _, l := range lines {
		if l.Product == 0 || l.Template == 0 || l.Cera < 0 || l.Cera > 2147483647 || (l.Cera > 0) == (l.Gold > 0) {
			return out, false, fmt.Errorf("invalid listing")
		}
		cost += l.Cera
	}
	err = s.db.Mutate(func(tx *db100.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		r, ok, e := charRowTx(tx, account, role, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		k := fmt.Sprintf("%d/%s", account, key)
		if exists, e := tx.Get("premium_cart_receipts", k, &out); e != nil {
			return e
		} else if exists {
			if out.Character != role || !slices.Equal(out.Lines, lines) {
				return fmt.Errorf("order identity conflict")
			}
			return nil
		}
		bal, e := ceraTx(tx, account)
		if e != nil {
			return e
		}
		if bal < cost {
			return fmt.Errorf("insufficient CERA")
		}
		c, e := r.character()
		if e != nil {
			return e
		}
		state, del, e := deliver(c, lines)
		if e != nil {
			return e
		}
		if !json.Valid(state) || !json.Valid(del) || string(del) == "null" {
			return fmt.Errorf("missing cart delivery")
		}
		if e = extendCartContracts(tx, account, lines); e != nil {
			return e
		}
		r.State = state
		if e = tx.Put("characters", idKey(role), r); e != nil {
			return e
		}
		if cost > 0 {
			if e = tx.Put("account_currency", idKey(account), currencyRow{AccountID: account, Cera: bal - cost, UpdatedAt: nowStamp()}); e != nil {
				return e
			}
		}
		out = CartReceipt{role, lines, del}
		if e = tx.Put("premium_cart_receipts", k, out); e != nil {
			return e
		}
		applied = true
		return nil
	})
	if err != nil {
		return CartReceipt{}, false, err
	}
	return
}
