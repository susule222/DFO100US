package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"dfolan/internal/db100"
)

// GrantItem is one item handed out by an operator grant.
type GrantItem struct {
	Template uint32 `json:"template"`
	Amount   uint32 `json:"amount"`
}

// Grant is one operator hand-out: cera at the account, gold and items at one
// character. Cera is account-scoped because the client reads it from the
// account's SELECT response, not from character state.
type Grant struct {
	ID        string      `json:"id"`
	AccountID int64       `json:"account_id"`
	Character int64       `json:"character_id,omitempty"`
	Cera      int64       `json:"cera,omitempty"`
	Gold      uint32      `json:"gold,omitempty"`
	Items     []GrantItem `json:"items,omitempty"`
	Reason    string      `json:"reason"`
	Operator  string      `json:"operator"`
}

type GrantResult struct {
	Receipt   json.RawMessage
	Character Character
	Cera      uint64
	Applied   bool
}

type currencyRow struct {
	AccountID int64  `json:"account_id"`
	Cera      int64  `json:"cera"`
	UpdatedAt string `json:"updated_at"`
}

type grantRow struct {
	GrantID     string          `json:"grant_id"`
	AccountID   int64           `json:"account_id"`
	CharacterID *int64          `json:"character_id,omitempty"`
	Request     json.RawMessage `json:"request"`
	Receipt     json.RawMessage `json:"receipt"`
	Operator    string          `json:"operator"`
	Reason      string          `json:"reason"`
	CreatedAt   string          `json:"created_at"`
}

// MigrateGrants: db100 is schemaless; the grant id remains the idempotency
// key, so a repeated run of the same grant cannot pay out twice.
func (s *Store) MigrateGrants(ctx context.Context) error { return nil }

func ceraTx(tx *db100.Tx, account int64) (int64, error) {
	var r currencyRow
	ok, e := tx.Get("account_currency", idKey(account), &r)
	if e != nil {
		return 0, e
	}
	if !ok || r.Cera < 0 {
		return 0, nil
	}
	return r.Cera, nil
}

// AccountCera reports an account's balance, creating no row for a read.
func (s *Store) AccountCera(ctx context.Context, account int64) (uint64, error) {
	var cera int64
	e := s.db.Mutate(func(tx *db100.Tx) error {
		var e error
		cera, e = ceraTx(tx, account)
		return e
	})
	return uint64(cera), e
}

// ApplyGrant pays out one grant in a single transaction and records it.
//
// The grant id is the idempotency key: replaying the same id returns the
// original receipt and pays nothing further, so a retried or double-clicked
// hand-out cannot mint a second payout. Cera cannot go negative, character
// state is only touched through the caller's mutate function (which owns the
// source catalog for gold and items), and every ownership boundary is checked
// inside the transaction.
func (s *Store) ApplyGrant(ctx context.Context, g Grant, mutate func(Character) (json.RawMessage, json.RawMessage, error)) (GrantResult, error) {
	var out GrantResult
	if g.ID == "" || g.AccountID == 0 || g.Operator == "" || g.Reason == "" {
		return out, fmt.Errorf("a grant needs an id, an account, an operator and a reason")
	}
	if g.Character == 0 && (g.Gold != 0 || len(g.Items) > 0) {
		return out, fmt.Errorf("gold and items need a character")
	}
	if g.Character != 0 && mutate == nil {
		return out, fmt.Errorf("character payout requires a source-backed mutation")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		// The audit row is the idempotency gate, claimed before anything is
		// paid. The write lock makes check-and-claim atomic, so exactly one
		// attempt ever owns the payout and a rollback releases the id again.
		request, e := json.Marshal(g)
		if e != nil {
			return e
		}
		if ok, _ := tx.Get("accounts", idKey(g.AccountID), nil); !ok {
			return fmt.Errorf("grant target is not a known account/character: %w", ErrNoRows)
		}
		if g.Character != 0 {
			if _, ok, _ := charRowTx(tx, g.AccountID, g.Character, false); !ok {
				return fmt.Errorf("grant target is not a known account/character: %w", ErrNoRows)
			}
		}
		var prior grantRow
		found, e := tx.Get("admin_grants", g.ID, &prior)
		if e != nil {
			return e
		}
		if found {
			// Already recorded: hand back the original receipt, pay nothing.
			out.Receipt = prior.Receipt
			out.Cera = uint64(ceraMust(tx, g.AccountID))
			return nil
		}
		var character *int64
		if g.Character != 0 {
			v := g.Character
			character = &v
		}
		row := grantRow{GrantID: g.ID, AccountID: g.AccountID, CharacterID: character, Request: request, Receipt: json.RawMessage("{}"), Operator: g.Operator, Reason: g.Reason, CreatedAt: nowStamp()}
		if e = tx.Put("admin_grants", g.ID, row); e != nil {
			return e
		}

		cera, e := ceraTx(tx, g.AccountID)
		if e != nil {
			return e
		}
		if g.Cera != 0 {
			// A deduction larger than the balance aborts the whole grant
			// rather than clamping, exactly as the old CHECK constraint did.
			if cera+g.Cera < 0 {
				return fmt.Errorf("cera adjustment refused (balance would go negative?)")
			}
			cera += g.Cera
			cur := currencyRow{AccountID: g.AccountID, Cera: cera, UpdatedAt: nowStamp()}
			if e = tx.Put("account_currency", idKey(g.AccountID), cur); e != nil {
				return e
			}
		}
		out.Cera = uint64(cera)

		var receipt json.RawMessage
		if g.Character != 0 {
			r, ok, e := charRowTx(tx, g.AccountID, g.Character, true)
			if e != nil {
				return e
			}
			if !ok {
				return fmt.Errorf("grant character is not owned by this account: %w", ErrNoRows)
			}
			role, e := r.character()
			if e != nil {
				return e
			}
			out.Character = role
			state, rec, e := mutate(role)
			if e != nil {
				return e
			}
			if !json.Valid(state) || !json.Valid(rec) {
				return fmt.Errorf("invalid grant payload")
			}
			r.State = state
			if e = tx.Put("characters", idKey(r.ID), r); e != nil {
				return e
			}
			out.Character.State, receipt = state, rec
		} else {
			receipt, _ = json.Marshal(map[string]any{"cera": out.Cera})
		}

		row.Receipt = receipt
		out.Receipt, out.Applied = receipt, true
		return tx.Put("admin_grants", g.ID, row)
	})
	return out, e
}

func ceraMust(tx *db100.Tx, account int64) int64 {
	v, _ := ceraTx(tx, account)
	return v
}

// GrantHistory lists recorded grants, newest first, for audit.
func (s *Store) GrantHistory(ctx context.Context, account int64, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var rows []grantRow
	s.db.Each("admin_grants", func(_ string, raw json.RawMessage) bool {
		var r grantRow
		if json.Unmarshal(raw, &r) == nil && r.AccountID == account {
			rows = append(rows, r)
		}
		return true
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt > rows[j].CreatedAt })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%v  %-24s by %-12s  %s  %s", parseStamp(r.CreatedAt), r.GrantID, r.Operator, r.Reason, r.Request))
	}
	return out, nil
}

// CashSale is a server catalog entry, never an amount or price from the client.
type CashSale struct {
	Product, Template, Quantity uint32
	Cera                        int64
}
type CashReceipt struct {
	Account, Character int64
	Order              string
	Sale               CashSale
	CeraAfter          int64
	Delivery           json.RawMessage
}
type CashDelivery func(Character, CashSale) (json.RawMessage, json.RawMessage, error)

// Settlement must store a recoverable delivery inside this transaction.
// A disconnected socket is not a delivery and cannot be used as the callback.
func (s *Store) CompleteCashSale(ctx context.Context, account, role int64, key string, sale CashSale, deliver CashDelivery) (CashReceipt, bool, error) {
	var receipt CashReceipt
	if account <= 0 || role <= 0 || key == "" || len(key) > 132 || sale.Cera <= 0 || sale.Product == 0 || sale.Template == 0 || sale.Quantity == 0 || deliver == nil {
		return receipt, false, fmt.Errorf("incomplete checkout")
	}
	applied := false
	err := s.db.Mutate(func(tx *db100.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, ok, err := charRowTx(tx, account, role, true)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoRows
		}
		k := fmt.Sprintf("%d/%s", account, key)
		if ok, e := tx.Get("cashshop_receipts", k, &receipt); e != nil {
			return e
		} else if ok {
			if receipt.Character != role || receipt.Sale != sale {
				return fmt.Errorf("order identity conflict")
			}
			return nil
		}
		bal, e := ceraTx(tx, account)
		if e != nil {
			return e
		}
		if bal < sale.Cera {
			return fmt.Errorf("insufficient CERA")
		}
		c, e := r.character()
		if e != nil {
			return e
		}
		state, del, e := deliver(c, sale)
		if e != nil {
			return e
		}
		if !json.Valid(state) || !json.Valid(del) || string(del) == "null" {
			return fmt.Errorf("delivery missing")
		}
		r.State = state
		if e = tx.Put("characters", idKey(r.ID), r); e != nil {
			return e
		}
		receipt = CashReceipt{account, role, key, sale, bal - sale.Cera, del}
		if e = tx.Put("account_currency", idKey(account), currencyRow{AccountID: account, Cera: receipt.CeraAfter, UpdatedAt: nowStamp()}); e != nil {
			return e
		}
		if e = tx.Put("cashshop_receipts", k, receipt); e != nil {
			return e
		}
		applied = true
		return nil
	})
	if err != nil {
		return CashReceipt{}, false, err
	}
	return receipt, applied, nil
}
