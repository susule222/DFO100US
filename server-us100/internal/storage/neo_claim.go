package storage

import (
	"dfolan/internal/db100"
	"encoding/json"
	"fmt"
	"time"
)

const NeoPremiumPlusKind byte = 118

// Claim and receipt share the character/entitlement write transaction.
// Only the server clock and the purchased account contract determine eligibility.
func (s *Store) ClaimNeoBenefit(account, id int64, version string, daily bool, deliver func(Character, int64) (json.RawMessage, error)) (Character, error) {
	var role Character
	e := s.db.Mutate(func(tx *db100.Tx) error {
		row, ok, e := charRowTx(tx, account, id, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		if version != row.ConfigVersion || version != "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69" {
			return fmt.Errorf("unexpected membership source")
		}
		var ends map[byte]int64
		if _, e = tx.Get("us224_account_premium", idKey(account), &ends); e != nil {
			return e
		}
		now := time.Now().UTC()
		end := ends[NeoPremiumPlusKind]
		if end <= now.Unix() {
			return fmt.Errorf("Neo Premium Plus expired or absent")
		}
		key := fmt.Sprintf("%d/%s", account, now.Format("2006-01-02"))
		if daily {
			var prior json.RawMessage
			ok, e = tx.Get("neo_daily_claims", key, &prior)
			if e != nil {
				return e
			}
			if ok {
				return fmt.Errorf("daily benefit already received")
			}
		}
		role, e = row.character()
		if e != nil {
			return e
		}
		next, e := deliver(role, end)
		if e != nil {
			return e
		}
		if !json.Valid(next) {
			return fmt.Errorf("invalid claim state")
		}
		row.State = next
		if e = tx.Put("characters", idKey(id), row); e != nil {
			return e
		}
		if daily {
			if e = tx.Put("neo_daily_claims", key, map[string]any{"character_id": id, "template": 50002547, "quantity": 1, "claimed_at": now}); e != nil {
				return e
			}
		}
		role.State = next
		return nil
	})
	return role, e
}
