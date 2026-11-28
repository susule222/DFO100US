package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"dfolan/internal/db100"
)

type QuestState struct {
	ID            uint16 `json:"id"`
	Status        string `json:"status"`
	Progress      uint32 `json:"progress"`
	ConfigVersion string `json:"config_version"`
	ProgressModel string `json:"progress_model"`
}

type questRow struct {
	CharacterID   int64   `json:"character_id"`
	QuestID       int     `json:"quest_id"`
	Status        string  `json:"status"`
	Progress      int64   `json:"progress"`
	ConfigVersion string  `json:"config_version"`
	ProgressModel string  `json:"progress_model"`
	AcceptedAt    string  `json:"accepted_at"`
	CompletedAt   *string `json:"completed_at,omitempty"`
}

func questKey(id int64, qid uint16) string { return idKey(id) + "/" + strconv.Itoa(int(qid)) }

func (s *Store) MigrateQuests(ctx context.Context) error { return nil }

type QuestLinks struct {
	Parent             uint16
	Conflicts          []uint16
	AcceptConflicts    []uint16
	PrerequisiteGroups [][]uint32
}

func (l QuestLinks) Allows(status func(uint16) string) bool {
	if l.Parent != 0 && status(l.Parent) != "accepted" {
		return false
	}
	for _, conflict := range l.Conflicts {
		if status(conflict) != "" {
			return false
		}
	}
	for _, conflict := range l.AcceptConflicts {
		if status(conflict) == "accepted" {
			return false
		}
	}
	return true
}

func checkQuestLinks(tx *db100.Tx, char int64, version string, links []QuestLinks) error {
	for _, l := range links {
		var lookupErr error
		ok := l.Allows(func(id uint16) string {
			var q questRow
			found, e := tx.Get("character_quests", questKey(char, id), &q)
			if e != nil {
				lookupErr = e
			}
			if !found {
				return ""
			}
			if q.ConfigVersion != version {
				lookupErr = fmt.Errorf("linked quest requires migration")
			}
			return q.Status
		})
		if lookupErr != nil {
			return lookupErr
		}
		if !ok {
			return fmt.Errorf("parent or mutually exclusive quest condition not met")
		}
	}
	return nil
}

func (s *Store) AcceptQuest(ctx context.Context, account, characterID int64, qid uint16, version string, minLevel, maxLevel uint32, prerequisites []uint32, initial uint32, model string, links ...QuestLinks) (QuestState, error) {
	out := QuestState{ID: qid, Status: "accepted", ConfigVersion: version, Progress: initial, ProgressModel: model}
	if qid == 0 || qid == 65535 || len(version) != 64 || model == "" || model == "legacy-zero" {
		return out, errors.New("invalid quest")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		row, ok, e := charRowTx(tx, account, characterID, false)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		if e := checkQuestLinks(tx, characterID, version, links); e != nil {
			return e
		}
		var state struct {
			Level uint32 `json:"level"`
		}
		if e = json.Unmarshal(row.State, &state); e != nil {
			return e
		}
		if state.Level < minLevel || state.Level > maxLevel {
			return errors.New("quest level condition not met")
		}
		var existing questRow
		found, e := tx.Get("character_quests", questKey(characterID, qid), &existing)
		if e != nil {
			return e
		}
		if found {
			out.Status, out.Progress, out.ConfigVersion, out.ProgressModel =
				existing.Status, uint32(existing.Progress), existing.ConfigVersion, existing.ProgressModel
			if out.Status != "accepted" || out.ConfigVersion != version || out.ProgressModel != model {
				return errors.New("quest is completed or requires configuration migration")
			}
			return nil
		}
		var prereqGroups [][]uint32
		for _, l := range links {
			if len(l.PrerequisiteGroups) > 0 {
				prereqGroups = l.PrerequisiteGroups
				break
			}
		}
		if len(prereqGroups) > 0 {
			groupSatisfied := false
			var firstMissing uint32
			for _, group := range prereqGroups {
				allDone := true
				for _, pre := range group {
					var prior questRow
					ok, e := tx.Get("character_quests", questKey(characterID, uint16(pre)), &prior)
					if e != nil {
						return e
					}
					if !ok || prior.Status != "completed" {
						allDone = false
						if firstMissing == 0 {
							firstMissing = pre
						}
						break
					}
				}
				if allDone {
					groupSatisfied = true
					break
				}
			}
			if !groupSatisfied {
				return fmt.Errorf("prerequisite %d incomplete", firstMissing)
			}
		} else {
			for _, pre := range prerequisites {
				var prior questRow
				ok, e := tx.Get("character_quests", questKey(characterID, uint16(pre)), &prior)
				if e != nil {
					return e
				}
				if !ok || prior.Status != "completed" {
					return fmt.Errorf("prerequisite %d incomplete", pre)
				}
			}
		}
		rec := questRow{CharacterID: characterID, QuestID: int(qid), Status: "accepted", Progress: int64(initial), ConfigVersion: version, ProgressModel: model, AcceptedAt: nowStamp()}
		return tx.Put("character_quests", questKey(characterID, qid), rec)
	})
	return out, e
}

func (s *Store) AbandonQuest(ctx context.Context, account, characterID int64, qid uint16) error {
	return s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, characterID, false); e != nil {
			return e
		} else if !ok {
			return errors.New("quest is not active for this character")
		}
		var r questRow
		key := questKey(characterID, qid)
		found, e := tx.Get("character_quests", key, &r)
		if e != nil {
			return e
		}
		if !found || r.Status != "accepted" {
			return errors.New("quest is not active for this character")
		}
		tx.Delete("character_quests", key)
		return nil
	})
}
