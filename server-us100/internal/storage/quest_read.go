package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dfolan/internal/db100"
)

type questRepairRow struct {
	CharacterID int64           `json:"character_id"`
	QuestID     int             `json:"quest_id"`
	Reason      string          `json:"reason"`
	BeforeState json.RawMessage `json:"before_state"`
	AfterState  json.RawMessage `json:"after_state"`
	RepairedAt  string          `json:"repaired_at"`
}

func (s *Store) Quests(ctx context.Context, account, id int64) ([]QuestState, error) {
	if _, ok, e := s.charRow(account, id, false); e != nil {
		return nil, e
	} else if !ok {
		return nil, errors.New("character is not owned")
	}
	prefix := idKey(id) + "/"
	var rows []questRow
	s.db.Each("character_quests", func(key string, raw json.RawMessage) bool {
		if !strings.HasPrefix(key, prefix) {
			return true
		}
		var r questRow
		if json.Unmarshal(raw, &r) == nil {
			rows = append(rows, r)
		}
		return true
	})
	out := make([]QuestState, 0, len(rows))
	for _, r := range rows { // Each visits keys in sorted order: "<id>/<qid>" pads qid sorting adequately for uint16
		out = append(out, QuestState{ID: uint16(r.QuestID), Status: r.Status, Progress: uint32(r.Progress), ConfigVersion: r.ConfigVersion, ProgressModel: r.ProgressModel})
	}
	// Lexicographic key order is not numeric qid order; re-sort explicitly.
	sortQuests(out)
	return out, nil
}

func sortQuests(q []QuestState) {
	for i := 1; i < len(q); i++ {
		for j := i; j > 0 && q[j].ID < q[j-1].ID; j-- {
			q[j], q[j-1] = q[j-1], q[j]
		}
	}
}

// Repair only the accepted, zero-valued records produced by the initial
// incomplete codec. A persisted before/after audit and compare-and-swap keep
// this operation idempotent and prevent overwriting newer objective progress.
func (s *Store) RepairLegacyQuest(ctx context.Context, account, id int64, before QuestState, initial uint32, model string) error {
	if before.Status != "accepted" || before.Progress != 0 || before.ProgressModel != "legacy-zero" || initial == 0 || model == "legacy-zero" || model == "" {
		return errors.New("not an eligible legacy quest")
	}
	return s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, id, false); e != nil {
			return e
		} else if !ok {
			return fmt.Errorf("legacy quest changed; repair refused")
		}
		after := before
		after.Progress = initial
		after.ProgressModel = model
		oldJSON, _ := json.Marshal(before)
		newJSON, _ := json.Marshal(after)

		var r questRow
		found, e := tx.Get("character_quests", questKey(id, before.ID), &r)
		if e != nil {
			return e
		}
		// Compare-and-swap: only the exact legacy shape is repaired.
		if !found || r.Status != "accepted" || r.Progress != 0 || r.ProgressModel != "legacy-zero" || r.ConfigVersion != before.ConfigVersion {
			return fmt.Errorf("legacy quest changed; repair refused")
		}
		r.Progress, r.ProgressModel = int64(initial), model
		if e = tx.Put("character_quests", questKey(id, before.ID), r); e != nil {
			return e
		}
		reason := "initial-zero-false-completion"
		rec := questRepairRow{CharacterID: id, QuestID: int(before.ID), Reason: reason, BeforeState: oldJSON, AfterState: newJSON, RepairedAt: nowStamp()}
		return tx.Put("character_quest_repairs", questKey(id, before.ID)+"/"+reason, rec)
	})
}
