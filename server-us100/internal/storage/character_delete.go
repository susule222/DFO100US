package storage

import (
	"context"
	"errors"
	"sort"

	"dfolan/internal/db100"
)

// DeleteCharacter retains all role state and dependent receipts. A name stays
// reserved while archived. Matching slot AND exact confirmation name prevents
// a retried old slot from deleting its newly shifted neighbour.
func (s *Store) DeleteCharacter(ctx context.Context, account int64, slot uint16, name string) (int64, error) {
	var id int64
	e := s.db.Mutate(func(tx *db100.Tx) error {
		if ok, _ := tx.Get("accounts", idKey(account), nil); !ok {
			return ErrNoRows
		}
		var roster []characterRow
		for _, key := range tx.Keys("characters") {
			var r characterRow
			if ok, e := tx.Get("characters", key, &r); e != nil {
				return e
			} else if ok && r.AccountID == account && r.DeletedAt == nil {
				roster = append(roster, r)
			}
		}
		sort.Slice(roster, func(i, j int) bool { return roster[i].WireID < roster[j].WireID })
		if int(slot) >= len(roster) {
			return ErrNoRows
		}
		target := roster[slot]
		if name == "" || target.Name != name {
			return errors.New("delete confirmation does not match current roster")
		}
		at := nowStamp()
		target.DeletedAt = &at
		id = target.ID
		return tx.Put("characters", idKey(target.ID), target)
	})
	return id, e
}
