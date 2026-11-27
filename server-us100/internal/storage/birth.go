package storage

import (
	"context"
	"fmt"

	"dfolan/internal/db100"
)

// Birth stages for a character's one-time starting route.
const (
	BirthPending  = 0 // created, has not entered its job tutorial
	BirthEntered  = 1 // inside the tutorial dungeon
	BirthReturned = 2 // tutorial finished, not yet settled in town
	BirthComplete = 3 // finished or intentionally skipped; ordinary play
)

type birthRow struct {
	CharacterID int64  `json:"character_id"`
	Stage       int16  `json:"stage"`
	Dungeon     int64  `json:"dungeon"`
	UpdatedAt   string `json:"updated_at"`
}

// MigrateBirth backfills every character that already exists as complete.
// Characters made before this build have already been played past their
// starting route, and the project's standing rule is that existing progress
// is never reset or replayed: only characters created from here on begin at
// BirthPending. An already-recorded stage is never touched, so a character
// created during this run is not re-marked complete by a later restart.
func (s *Store) MigrateBirth(ctx context.Context) error {
	return s.db.Mutate(func(tx *db100.Tx) error {
		for _, key := range tx.Keys("characters") {
			if ok, e := tx.Get("character_birth", key, nil); e != nil {
				return e
			} else if ok {
				continue
			}
			var r characterRow
			if ok, e := tx.Get("characters", key, &r); e != nil {
				return e
			} else if !ok {
				continue
			}
			row := birthRow{CharacterID: r.ID, Stage: BirthComplete, UpdatedAt: nowStamp()}
			if e := tx.Put("character_birth", key, row); e != nil {
				return e
			}
		}
		return nil
	})
}

// BirthStage reports a character's starting-route stage. A character without
// a row is treated as complete: an absent row can only mean the character
// predates this table, and such a character must never be sent back through
// a starting route it already finished.
func (s *Store) BirthStage(ctx context.Context, account, id int64) (byte, uint32, error) {
	r, ok, e := s.charRow(account, id, true)
	if e != nil {
		return BirthComplete, 0, e
	}
	if !ok {
		return BirthComplete, 0, fmt.Errorf("starting route character is not owned")
	}
	var b birthRow
	found, e := s.db.Get("character_birth", idKey(r.ID), &b)
	if e != nil {
		return BirthComplete, 0, e
	}
	if !found {
		return BirthComplete, 0, nil
	}
	return byte(b.Stage), uint32(b.Dungeon), nil
}

// StartBirth records a newly created character as owing its starting route.
func (s *Store) StartBirth(ctx context.Context, account, id int64) error {
	return s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, id, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		if ok, _ := tx.Get("character_birth", idKey(id), nil); ok {
			return fmt.Errorf("starting route already recorded")
		}
		return tx.Put("character_birth", idKey(id), birthRow{CharacterID: id, Stage: BirthPending, UpdatedAt: nowStamp()})
	})
}

// AdvanceBirth moves a character's starting route forward. The transition is
// one-way and refuses to move backwards, so a reconnect or a replayed request
// can never send a character through its starting route a second time.
func (s *Store) AdvanceBirth(ctx context.Context, account, id int64, stage byte, dungeon uint32) (bool, error) {
	if stage > BirthComplete {
		return false, fmt.Errorf("invalid starting route stage")
	}
	var applied bool
	e := s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, id, true); e != nil {
			return e
		} else if !ok {
			return nil
		}
		var b birthRow
		ok, e := tx.Get("character_birth", idKey(id), &b)
		if e != nil {
			return e
		}
		if !ok || b.Stage >= int16(stage) {
			return nil
		}
		b.Stage, b.Dungeon, b.UpdatedAt = int16(stage), int64(dungeon), nowStamp()
		applied = true
		return tx.Put("character_birth", idKey(id), b)
	})
	return applied, e
}
