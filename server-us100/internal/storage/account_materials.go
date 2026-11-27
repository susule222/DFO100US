package storage

import (
	"context"
	"dfolan/internal/db100"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

const accountMaterialsTable = "us224_account_materials"

// AccountMaterials returns the persisted account-shared cube/soul warehouse
// balances for the given account.
func (s *Store) AccountMaterials(account int64) (map[uint32]uint32, error) {
	var saved map[uint32]uint32
	_, err := s.db.Get(accountMaterialsTable, idKey(account), &saved)
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]uint32, len(saved))
	for k, v := range saved {
		if k != 0 && v > 0 {
			out[k] = v
		}
	}
	return out, nil
}

// CommitCharacterEventWithMaterials commits a character state mutation and
// account-shared material warehouse deltas atomically in one db100 transaction.
func (s *Store) CommitCharacterEventWithMaterials(
	ctx context.Context,
	account, id int64,
	version, key, model string,
	apply func(Character) (json.RawMessage, json.RawMessage, map[uint32]uint32, error),
) (Character, map[uint32]uint32, bool, error) {
	var role Character
	decoded, e := hex.DecodeString(version)
	if e != nil || len(decoded) != 32 || key == "" || len(key) > 200 || model == "" || len(model) > 100 || apply == nil {
		return role, nil, false, fmt.Errorf("invalid character material event")
	}
	var applied bool
	var finalMaterials map[uint32]uint32
	e = s.db.Mutate(func(tx *db100.Tx) error {
		row, ok, e := charRowTx(tx, account, id, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		if row.ConfigVersion != version {
			return fmt.Errorf("character event source mismatch")
		}
		var currentMat map[uint32]uint32
		if _, err := tx.Get(accountMaterialsTable, idKey(account), &currentMat); err != nil {
			return err
		}
		if currentMat == nil {
			currentMat = map[uint32]uint32{}
		}
		var prior eventRow
		found, e := tx.Get("character_events", eventKey(id, key), &prior)
		if e != nil {
			return e
		}
		if found {
			if prior.Model != model {
				return fmt.Errorf("character event model mismatch")
			}
			role, e = row.character()
			finalMaterials = currentMat
			return e
		}
		role, e = row.character()
		if e != nil {
			return e
		}
		state, outcome, deltas, e := apply(role)
		if e != nil {
			return e
		}
		if !json.Valid(state) || !json.Valid(outcome) {
			return fmt.Errorf("invalid event JSON")
		}
		for tpl, delta := range deltas {
			if tpl == 0 || delta == 0 {
				continue
			}
			if uint64(currentMat[tpl])+uint64(delta) > math.MaxUint32 {
				return fmt.Errorf("account material %d overflow", tpl)
			}
			currentMat[tpl] += delta
		}
		if len(deltas) > 0 {
			if e = tx.Put(accountMaterialsTable, idKey(account), currentMat); e != nil {
				return e
			}
		}
		rec := eventRow{CharacterID: id, EventKey: key, ConfigVersion: version, Model: model, Outcome: outcome, CreatedAt: nowStamp()}
		if e = tx.Put("character_events", eventKey(id, key), rec); e != nil {
			return e
		}
		row.State = state
		if e = tx.Put("characters", idKey(id), row); e != nil {
			return e
		}
		role.State = state
		finalMaterials = currentMat
		applied = true
		return nil
	})
	if e != nil {
		return role, nil, false, e
	}
	return role, finalMaterials, applied, nil
}

// MigrateCharacterMaterials atomically moves account-warehouse materials (such
// as cube fragments) out of a character's ordinary bag into the account
// material warehouse. When apply returns no deltas, nothing is written.
func (s *Store) MigrateCharacterMaterials(
	ctx context.Context,
	account, id int64,
	apply func(Character) (json.RawMessage, map[uint32]uint32, error),
) (Character, map[uint32]uint32, error) {
	var role Character
	var finalMaterials map[uint32]uint32
	e := s.db.Mutate(func(tx *db100.Tx) error {
		row, ok, e := charRowTx(tx, account, id, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		var currentMat map[uint32]uint32
		if _, err := tx.Get(accountMaterialsTable, idKey(account), &currentMat); err != nil {
			return err
		}
		if currentMat == nil {
			currentMat = map[uint32]uint32{}
		}
		role, e = row.character()
		if e != nil {
			return e
		}
		state, deltas, e := apply(role)
		if e != nil {
			return e
		}
		if len(deltas) == 0 {
			finalMaterials = currentMat
			return nil
		}
		if !json.Valid(state) {
			return fmt.Errorf("invalid migrated state JSON")
		}
		for tpl, delta := range deltas {
			if tpl == 0 || delta == 0 {
				continue
			}
			if uint64(currentMat[tpl])+uint64(delta) > math.MaxUint32 {
				return fmt.Errorf("account material %d overflow", tpl)
			}
			currentMat[tpl] += delta
		}
		if e = tx.Put(accountMaterialsTable, idKey(account), currentMat); e != nil {
			return e
		}
		row.State = state
		if e = tx.Put("characters", idKey(id), row); e != nil {
			return e
		}
		role.State = state
		finalMaterials = currentMat
		return nil
	})
	if e != nil {
		return role, nil, e
	}
	return role, finalMaterials, nil
}
