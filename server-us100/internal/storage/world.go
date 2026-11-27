package storage

import (
	"context"
	"encoding/json"
	"errors"

	"dfolan/internal/db100"
)

type WorldPosition struct {
	Town   uint32       `json:"town"`
	Area   uint32       `json:"area"`
	X      uint16       `json:"x"`
	Y      uint16       `json:"y"`
	Return *WorldReturn `json:"return,omitempty"`
}
type WorldReturn struct {
	Town uint32 `json:"town"`
	Area uint32 `json:"area"`
	X    uint16 `json:"x"`
	Y    uint16 `json:"y"`
}
type WorldState struct {
	Position      WorldPosition `json:"position"`
	Revision      int64         `json:"revision"`
	ConfigVersion string        `json:"config_version"`
}

var ErrWorldConflict = errors.New("world position changed concurrently")

type worldRow struct {
	CharacterID   int64           `json:"character_id"`
	Position      json.RawMessage `json:"position"`
	ConfigVersion string          `json:"config_version"`
	Revision      int64           `json:"revision"`
	UpdatedAt     string          `json:"updated_at"`
}

func (s *Store) MigrateWorld(ctx context.Context) error { return nil }

func (s *Store) LoadWorld(ctx context.Context, account, characterID int64, initial WorldPosition, version string) (WorldState, error) {
	var out WorldState
	if len(version) != 64 {
		return out, errors.New("invalid world configuration version")
	}
	b, e := json.Marshal(initial)
	if e != nil {
		return out, e
	}
	e = s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, characterID, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		var r worldRow
		found, e := tx.Get("character_world", idKey(characterID), &r)
		if e != nil {
			return e
		}
		if !found {
			r = worldRow{CharacterID: characterID, Position: b, ConfigVersion: version, Revision: 1, UpdatedAt: nowStamp()}
			if e = tx.Put("character_world", idKey(characterID), r); e != nil {
				return e
			}
		}
		out.Revision, out.ConfigVersion = r.Revision, r.ConfigVersion
		return json.Unmarshal(r.Position, &out.Position)
	})
	return out, e
}

// SaveWorldWithVersion persists a position under an explicit source version.
// It exists for the entry path: a character whose row was created against an
// older archive re-enters through the new source's spawn policy, and that
// validated spawn is saved as the new bound position instead of leaving the
// row stamped with a version the current catalog cannot load.
func (s *Store) SaveWorldWithVersion(ctx context.Context, account, characterID int64, next WorldPosition, version string) error {
	b, e := json.Marshal(next)
	if e != nil {
		return e
	}
	return s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, characterID, false); e != nil {
			return e
		} else if !ok {
			return ErrWorldConflict
		}
		var r worldRow
		ok, e := tx.Get("character_world", idKey(characterID), &r)
		if e != nil {
			return e
		}
		if !ok {
			return ErrWorldConflict
		}
		r.Position, r.ConfigVersion, r.Revision, r.UpdatedAt = b, version, r.Revision+1, nowStamp()
		return tx.Put("character_world", idKey(characterID), r)
	})
}

func (s *Store) SaveWorld(ctx context.Context, account, characterID int64, old WorldState, next WorldPosition) (WorldState, error) {
	out := old
	b, e := json.Marshal(next)
	if e != nil {
		return out, e
	}
	e = s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, characterID, false); e != nil {
			return e
		} else if !ok {
			return ErrWorldConflict
		}
		var r worldRow
		ok, e := tx.Get("character_world", idKey(characterID), &r)
		if e != nil {
			return e
		}
		if !ok || r.Revision != old.Revision {
			return ErrWorldConflict
		}
		r.Position, r.Revision, r.UpdatedAt = b, r.Revision+1, nowStamp()
		if e := tx.Put("character_world", idKey(characterID), r); e != nil {
			return e
		}
		out.Position, out.Revision = next, r.Revision
		return nil
	})
	if e != nil {
		return old, e
	}
	return out, nil
}
