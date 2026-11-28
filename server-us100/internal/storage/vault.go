package storage

import (
	"context"
	"encoding/json"
	"errors"

	"dfolan/internal/db100"
)

type VaultState struct {
	Slots         uint16
	Slots2        uint16
	Items         json.RawMessage
	ConfigVersion string
}

type AccountVaultState struct {
	Slots uint16
	Gold  uint32
}

type vaultRow struct {
	CharacterID   int64           `json:"character_id"`
	Slots         int             `json:"slots"`
	Slots2        int             `json:"slots2,omitempty"`
	Items         json.RawMessage `json:"items"`
	ConfigVersion string          `json:"config_version"`
	UpdatedAt     string          `json:"updated_at"`
}

type accountVaultRow struct {
	AccountID int64  `json:"account_id"`
	Slots     int    `json:"slots"`
	Gold      uint32 `json:"gold,omitempty"`
	UpdatedAt string `json:"updated_at"`
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
		v.Slots, v.Slots2, v.Items, v.ConfigVersion = uint16(r.Slots), uint16(r.Slots2), r.Items, r.ConfigVersion
		return nil
	})
	return v, e
}

// SavePersonalVaultSlots persists an upgraded Personal Safe I (tab=0) or
// Personal Safe II (tab=1) slot capacity atomically, never shrinking capacity.
func (s *Store) SavePersonalVaultSlots(ctx context.Context, account, id int64, initial, newSlots uint16, tab byte, version string) (VaultState, error) {
	var v VaultState
	if initial == 0 || newSlots == 0 || len(version) != 64 {
		return v, errors.New("invalid vault upgrade parameters")
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
		}
		if tab == 0 {
			if int(newSlots) > r.Slots {
				r.Slots = int(newSlots)
			}
		} else {
			if int(newSlots) > r.Slots2 {
				r.Slots2 = int(newSlots)
			}
		}
		r.UpdatedAt = nowStamp()
		if e = tx.Put("character_vaults", idKey(id), r); e != nil {
			return e
		}
		v.Slots, v.Slots2, v.Items, v.ConfigVersion = uint16(r.Slots), uint16(r.Slots2), r.Items, r.ConfigVersion
		return nil
	})
	return v, e
}

// LoadAccountVault returns the persisted Account Vault state (0 slots if not yet opened).
func (s *Store) LoadAccountVault(ctx context.Context, account int64) (AccountVaultState, error) {
	var out AccountVaultState
	if account <= 0 {
		return out, errors.New("invalid account")
	}
	var r accountVaultRow
	found, err := s.db.Get("account_vaults", idKey(account), &r)
	if err != nil {
		return out, err
	}
	if found && r.Slots > 0 {
		out.Slots = uint16(r.Slots)
		out.Gold = r.Gold
	}
	return out, nil
}

// SaveAccountVaultSlots persists an upgraded Account Vault capacity atomically.
func (s *Store) SaveAccountVaultSlots(ctx context.Context, account int64, newSlots uint16) (AccountVaultState, error) {
	var out AccountVaultState
	if account <= 0 || newSlots == 0 {
		return out, errors.New("invalid account vault upgrade parameters")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		var r accountVaultRow
		found, err := tx.Get("account_vaults", idKey(account), &r)
		if err != nil {
			return err
		}
		if !found {
			r = accountVaultRow{AccountID: account}
		}
		if int(newSlots) > r.Slots {
			r.Slots = int(newSlots)
		}
		r.UpdatedAt = nowStamp()
		if err = tx.Put("account_vaults", idKey(account), r); err != nil {
			return err
		}
		out.Slots = uint16(r.Slots)
		out.Gold = r.Gold
		return nil
	})
	return out, e
}
