package storage

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"dfolan/internal/db100"
)

type mapClearRow struct {
	CharacterID   int64  `json:"character_id"`
	RunID         string `json:"run_id"`
	MapID         int64  `json:"map_id"`
	SourceVersion string `json:"source_version"`
	ClearedAt     string `json:"cleared_at"`
}

func clearKey(id int64, run string, mapID uint32) string {
	return idKey(id) + "/" + run + "/" + strconv.FormatUint(uint64(mapID), 10)
}

func (s *Store) CreditQuestKill(ctx context.Context, account, characterID int64, run string, room uint32, entity uint16, source, model string, ids []uint16) (bool, error) {
	seed, err := hex.DecodeString(run)
	if err != nil || len(seed) != 16 || room == 0 || len(source) != 64 {
		return false, fmt.Errorf("invalid quest kill evidence")
	}
	key := fmt.Sprintf("%s/%d", clearKey(characterID, run, room), entity)
	changed := false
	err = s.db.Mutate(func(tx *db100.Tx) error {
		role, ok, e := charRowTx(tx, account, characterID, false)
		if e != nil {
			return e
		}
		if !ok || role.ConfigVersion != source {
			return ErrNoRows
		}
		if _, ok := tx.Raw("character_quest_kills", key); ok {
			return nil
		}
		for _, id := range ids {
			var q questRow
			k := questKey(characterID, id)
			found, e := tx.Get("character_quests", k, &q)
			if e != nil {
				return e
			}
			if found && q.ConfigVersion == source && q.ProgressModel == model && q.Status == "accepted" && q.Progress > 0 {
				q.Progress--
				if e = tx.Put("character_quests", k, q); e != nil {
					return e
				}
				changed = true
			}
		}
		return tx.Put("character_quest_kills", key, true)
	})
	return changed, err
}

func (s *Store) MigrateQuestObjectives(ctx context.Context) error { return nil }

// CompleteQuestObjective marks one accepted objective satisfied. The caller
// must have verified the source condition itself; this only owns the durable
// transition and refuses a quest that is not this character's accepted quest
// under the same source and progress model.
func (s *Store) CompleteQuestObjective(ctx context.Context, account, characterID int64, qid uint16, version, model string) (bool, error) {
	return s.settleObjective(ctx, account, characterID, qid, version, model, true)
}

// SettleQuestObjective is CompleteQuestObjective without the pending-progress
// requirement: an idempotent objective (meeting a source NPC) settles to
// satisfied even when a retry finds it already settled, while a kill credit
// must observe the 1->0 transition to pay out exactly once.
func (s *Store) SettleQuestObjective(ctx context.Context, account, characterID int64, qid uint16, version, model string) (bool, error) {
	return s.settleObjective(ctx, account, characterID, qid, version, model, false)
}

func (s *Store) settleObjective(ctx context.Context, account, characterID int64, qid uint16, version, model string, requirePending bool) (bool, error) {
	if qid == 0 || qid == 65535 || len(version) != 64 || model == "" {
		return false, fmt.Errorf("invalid quest objective completion")
	}
	var applied bool
	e := s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, characterID, true); e != nil {
			return e
		} else if !ok {
			return nil
		}
		var r questRow
		found, e := tx.Get("character_quests", questKey(characterID, qid), &r)
		if e != nil {
			return e
		}
		if !found || r.Status != "accepted" || r.ConfigVersion != version || r.ProgressModel != model {
			return nil
		}
		if requirePending && r.Progress == 0 {
			return nil
		}
		if r.Progress != 0 {
			r.Progress = 0
			if e := tx.Put("character_quests", questKey(characterID, qid), r); e != nil {
				return e
			}
		}
		applied = true
		return nil
	})
	return applied, e
}

// A server-owned run supplies the map and matching source objective IDs.
// A duplicate clear must not complete a newly accepted/reaccepted quest.
func (s *Store) RecordQuestMapClear(ctx context.Context, account, characterID int64, run string, mapID uint32, version, model string, matching []uint16) (bool, error) {
	return s.recordClear(account, characterID, run, mapID, version, model, matching, "character_map_clears")
}

func (s *Store) RecordQuestDungeonClear(ctx context.Context, account, characterID int64, run string, dungeonID uint32, version, model string, matching []uint16) (bool, error) {
	return s.recordClear(account, characterID, run, dungeonID, version, model, matching, "character_dungeon_clears")
}

// A distinct ledger: a scene object's removal is not a monster kill or a
// clear of the whole room. One instance of this object per authored run.
func (s *Store) RecordMirkwoodScene(ctx context.Context, role Character, run, model string) (bool, error) {
	return s.recordClear(role.AccountID, role.ID, run, 13099, role.ConfigVersion, model, []uint16{3157}, "character_scene_objects")
}

func (s *Store) recordClear(account, characterID int64, run string, mapID uint32, version, model string, matching []uint16, table string) (bool, error) {
	seed, err := hex.DecodeString(run)
	if err != nil || len(seed) != 16 || mapID == 0 || len(version) != 64 || model == "" {
		return false, fmt.Errorf("invalid map clear evidence")
	}
	var recorded bool
	err = s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, characterID, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		key := clearKey(characterID, run, mapID)
		if ok, err := tx.Get(table, key, nil); err != nil {
			return err
		} else if ok {
			return nil // duplicate evidence: idempotent no-op
		}
		rec := mapClearRow{CharacterID: characterID, RunID: run, MapID: int64(mapID), SourceVersion: version, ClearedAt: nowStamp()}
		if e := tx.Put(table, key, rec); e != nil {
			return e
		}
		for _, qid := range matching {
			if qid == 0 || qid == 65535 {
				return fmt.Errorf("invalid quest identity")
			}
			var q questRow
			found, e := tx.Get("character_quests", questKey(characterID, qid), &q)
			if e != nil {
				return e
			}
			if !found || q.Status != "accepted" || q.Progress <= 0 || (table == "character_map_clears" && q.Progress != 1) || q.ConfigVersion != version || q.ProgressModel != model {
				continue
			}
			q.Progress--
			if e = tx.Put("character_quests", questKey(characterID, qid), q); e != nil {
				return e
			}
		}
		recorded = true
		return nil
	})
	return recorded, err
}
