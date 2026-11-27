package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"dfolan/internal/db100"
)

type questRewardRow struct {
	CharacterID   int64           `json:"character_id"`
	QuestID       int             `json:"quest_id"`
	SourceVersion string          `json:"source_version"`
	Model         string          `json:"model"`
	Receipt       json.RawMessage `json:"receipt"`
	CreatedAt     string          `json:"created_at"`
}

func (s *Store) MigrateQuestRewards(ctx context.Context) error { return nil }

type QuestRewardCommit struct {
	Character Character
	Receipt   json.RawMessage
	Applied   bool
}

func (s *Store) CommitQuestReward(ctx context.Context, account, id int64, qid uint16, version, progressModel, rewardModel string, apply func(Character) (json.RawMessage, json.RawMessage, error), links ...QuestLinks) (QuestRewardCommit, error) {
	var out QuestRewardCommit
	if qid == 0 || qid == 65535 || len(version) != 64 || progressModel == "" || rewardModel == "" || apply == nil {
		return out, fmt.Errorf("invalid quest reward request")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		row, ok, e := charRowTx(tx, account, id, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		if row.ConfigVersion != version {
			return fmt.Errorf("quest character source mismatch")
		}
		var q questRow
		found, e := tx.Get("character_quests", questKey(id, qid), &q)
		if e != nil {
			return e
		}
		if !found {
			return ErrNoRows
		}
		if q.ConfigVersion != version || q.ProgressModel != progressModel || q.Progress != 0 {
			return fmt.Errorf("quest objective not ready under current source")
		}
		if q.Status == "completed" {
			var rec questRewardRow
			ok, e := tx.Get("character_quest_rewards", questKey(id, qid), &rec)
			if e != nil {
				return e
			}
			if !ok || rec.SourceVersion != version {
				return ErrNoRows
			}
			out.Receipt = rec.Receipt
			out.Character, e = row.character()
			return e
		}
		if q.Status != "accepted" {
			return fmt.Errorf("quest is not accepted")
		}
		if e := checkQuestLinks(tx, id, version, links); e != nil {
			return e
		}
		role, e := row.character()
		if e != nil {
			return e
		}
		out.Character = role
		state, receipt, e := apply(role)
		if e != nil {
			return e
		}
		if !json.Valid(state) || !json.Valid(receipt) {
			return fmt.Errorf("invalid quest reward JSON")
		}
		rec := questRewardRow{CharacterID: id, QuestID: int(qid), SourceVersion: version, Model: rewardModel, Receipt: receipt, CreatedAt: nowStamp()}
		if e = tx.Put("character_quest_rewards", questKey(id, qid), rec); e != nil {
			return e
		}
		row.State = state
		if e = tx.Put("characters", idKey(id), row); e != nil {
			return e
		}
		completed := nowStamp()
		q.Status, q.CompletedAt = "completed", &completed
		if e = tx.Put("character_quests", questKey(id, qid), q); e != nil {
			return e
		}
		out.Character.State = state
		out.Receipt, out.Applied = receipt, true
		return nil
	})
	if e != nil {
		return out, e
	}
	return out, nil
}
