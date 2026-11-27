package storage

import (
	"context"
	"encoding/json"
	"errors"

	"dfolan/internal/db100"
)

type VaultState struct {
	Slots         uint16
	Items         json.RawMessage
	ConfigVersion string
}

type vaultRow struct {
	CharacterID   int64           `json:"character_id"`
	Slots         int             `json:"slots"`
	Items         json.RawMessage `json:"items"`
	ConfigVersion string          `json:"config_version"`
	UpdatedAt     string          `json:"updated_at"`
}

func (s *Store) MigrateVault(ctx context.Context) error { return nil }

// Initialization inserts only absent state; reconnect never resets stored items.
func (s *Store) LoadVault(ctx context.Context, account, id int64, initial uint16, version string) (VaultState, error) {
	var v VaultState
	if initial == 0 || len(version) != 64 {
		return v, errors.New("invalid vault source")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, id, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		var r vaultRow
		found, e := tx.Get("character_vaults", idKey(id), &r)
		if e != nil {
			return e
		}
		if !found {
			r = vaultRow{CharacterID: id, Slots: int(initial), Items: json.RawMessage("[]"), ConfigVersion: version, UpdatedAt: nowStamp()}
			if e = tx.Put("character_vaults", idKey(id), r); e != nil {
				return e
			}
		}
		v.Slots, v.Items, v.ConfigVersion = uint16(r.Slots), r.Items, r.ConfigVersion
		return nil
	})
	return v, e
}
