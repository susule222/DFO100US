package storage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"dfolan/internal/db100"
)

type VaultState struct {
	Slots         uint16
	Slots2        uint16
	Items         json.RawMessage
	Items2        json.RawMessage
	ConfigVersion string
}

type AccountVaultState struct {
	Slots uint16
	Gold  uint32
	Items json.RawMessage
}

type vaultRow struct {
	CharacterID   int64           `json:"character_id"`
	Slots         int             `json:"slots"`
	Slots2        int             `json:"slots2,omitempty"`
	Items         json.RawMessage `json:"items"`
	Items2        json.RawMessage `json:"items2,omitempty"`
	ConfigVersion string          `json:"config_version"`
	UpdatedAt     string          `json:"updated_at"`
}

type accountVaultRow struct {
	AccountID int64           `json:"account_id"`
	Slots     int             `json:"slots"`
	Gold      uint32          `json:"gold,omitempty"`
	Items     json.RawMessage `json:"items,omitempty"`
	UpdatedAt string          `json:"updated_at"`
}

func defaultItemsJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("[]")
	}
	return raw
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
		v.Slots = uint16(r.Slots)
		v.Slots2 = uint16(r.Slots2)
		v.Items = defaultItemsJSON(r.Items)
		v.Items2 = defaultItemsJSON(r.Items2)
		v.ConfigVersion = r.ConfigVersion
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
			r = vaultRow{CharacterID: id, Slots: int(initial), Items: json.RawMessage("[]"), Items2: json.RawMessage("[]"), ConfigVersion: version, UpdatedAt: nowStamp()}
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
		r.Items = defaultItemsJSON(r.Items)
		r.Items2 = defaultItemsJSON(r.Items2)
		r.UpdatedAt = nowStamp()
		if e = tx.Put("character_vaults", idKey(id), r); e != nil {
			return e
		}
		v.Slots = uint16(r.Slots)
		v.Slots2 = uint16(r.Slots2)
		v.Items = defaultItemsJSON(r.Items)
		v.Items2 = defaultItemsJSON(r.Items2)
		v.ConfigVersion = r.ConfigVersion
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
		out.Items = defaultItemsJSON(r.Items)
	} else {
		out.Items = json.RawMessage("[]")
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
			r = accountVaultRow{AccountID: account, Items: json.RawMessage("[]")}
		}
		if int(newSlots) > r.Slots {
			r.Slots = int(newSlots)
		}
		r.Items = defaultItemsJSON(r.Items)
		r.UpdatedAt = nowStamp()
		if err = tx.Put("account_vaults", idKey(account), r); err != nil {
			return err
		}
		out.Slots = uint16(r.Slots)
		out.Gold = r.Gold
		out.Items = defaultItemsJSON(r.Items)
		return nil
	})
	return out, e
}

// CommitVaultOp atomically updates a character's bag state together with its
// Personal Safe I/II (character_vaults) and shared Account Vault (account_vaults)
// inside a single database write transaction, guarded by an idempotent event key.
func (s *Store) CommitVaultOp(
	ctx context.Context,
	account, id int64,
	initialSlots uint16,
	vaultVersion, charVersion, key, model string,
	apply func(role Character, pv VaultState, av AccountVaultState) (nextCharState json.RawMessage, nextPV VaultState, nextAV AccountVaultState, receipt json.RawMessage, err error),
) (Character, VaultState, AccountVaultState, bool, error) {
	var outRole Character
	var outPV VaultState
	var outAV AccountVaultState
	decoded, err := hex.DecodeString(charVersion)
	if err != nil || len(decoded) != 32 || initialSlots == 0 || len(vaultVersion) != 64 || key == "" || len(key) > 200 || model == "" || len(model) > 100 || apply == nil {
		return outRole, outPV, outAV, false, fmt.Errorf("invalid vault operation parameters")
	}
	var applied bool
	err = s.db.Mutate(func(tx *db100.Tx) error {
		cr, ok, e := charRowTx(tx, account, id, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		if cr.ConfigVersion != charVersion {
			return fmt.Errorf("vault op character source mismatch")
		}
		var vr vaultRow
		vFound, e := tx.Get("character_vaults", idKey(id), &vr)
		if e != nil {
			return e
		}
		if !vFound {
			vr = vaultRow{CharacterID: id, Slots: int(initialSlots), Items: json.RawMessage("[]"), Items2: json.RawMessage("[]"), ConfigVersion: vaultVersion, UpdatedAt: nowStamp()}
		}
		var ar accountVaultRow
		aFound, e := tx.Get("account_vaults", idKey(account), &ar)
		if e != nil {
			return e
		}
		if !aFound {
			ar = accountVaultRow{AccountID: account, Items: json.RawMessage("[]")}
		}
		curRole, e := cr.character()
		if e != nil {
			return e
		}
		curPV := VaultState{
			Slots:         uint16(vr.Slots),
			Slots2:        uint16(vr.Slots2),
			Items:         defaultItemsJSON(vr.Items),
			Items2:        defaultItemsJSON(vr.Items2),
			ConfigVersion: vr.ConfigVersion,
		}
		curAV := AccountVaultState{
			Slots: uint16(ar.Slots),
			Gold:  ar.Gold,
			Items: defaultItemsJSON(ar.Items),
		}

		// Idempotency replay check
		var existing eventRow
		seen, e := tx.Get("character_events", eventKey(id, key), &existing)
		if e != nil {
			return e
		}
		if seen {
			outRole, outPV, outAV = curRole, curPV, curAV
			return nil
		}

		nextState, nextPV, nextAV, receipt, e := apply(curRole, curPV, curAV)
		if e != nil {
			return e
		}
		if len(nextState) > 0 && !json.Valid(nextState) {
			return fmt.Errorf("vault op produced invalid character state")
		}
		if len(receipt) == 0 {
			receipt = json.RawMessage("{}")
		}
		stamp := nowStamp()
		if len(nextState) > 0 {
			cr.State = nextState
			if e = tx.Put("characters", idKey(id), cr); e != nil {
				return e
			}
		}
		vr.Slots = int(nextPV.Slots)
		vr.Slots2 = int(nextPV.Slots2)
		vr.Items = defaultItemsJSON(nextPV.Items)
		vr.Items2 = defaultItemsJSON(nextPV.Items2)
		vr.ConfigVersion = nextPV.ConfigVersion
		if vr.ConfigVersion == "" {
			vr.ConfigVersion = vaultVersion
		}
		vr.UpdatedAt = stamp
		if e = tx.Put("character_vaults", idKey(id), vr); e != nil {
			return e
		}
		if nextAV.Slots > 0 || aFound {
			ar.Slots = int(nextAV.Slots)
			ar.Gold = nextAV.Gold
			ar.Items = defaultItemsJSON(nextAV.Items)
			ar.UpdatedAt = stamp
			if e = tx.Put("account_vaults", idKey(account), ar); e != nil {
				return e
			}
		}
		ev := eventRow{
			CharacterID:   id,
			EventKey:      key,
			ConfigVersion: charVersion,
			Model:         model,
			Outcome:       receipt,
			CreatedAt:     stamp,
		}
		if e = tx.Put("character_events", eventKey(id, key), ev); e != nil {
			return e
		}
		outRole, e = cr.character()
		if e != nil {
			return e
		}
		outPV = VaultState{
			Slots:         uint16(vr.Slots),
			Slots2:        uint16(vr.Slots2),
			Items:         defaultItemsJSON(vr.Items),
			Items2:        defaultItemsJSON(vr.Items2),
			ConfigVersion: vr.ConfigVersion,
		}
		outAV = AccountVaultState{
			Slots: uint16(ar.Slots),
			Gold:  ar.Gold,
			Items: defaultItemsJSON(ar.Items),
		}
		applied = true
		return nil
	})
	return outRole, outPV, outAV, applied, err
}
