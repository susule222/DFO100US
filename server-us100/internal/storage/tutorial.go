package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"dfolan/internal/db100"
)

type tutorialRow struct {
	CharacterID int64  `json:"character_id"`
	Flag        int    `json:"flag"`
	Completed   bool   `json:"completed"`
	UpdatedAt   string `json:"updated_at"`
}

func (s *Store) MigrateTutorial(ctx context.Context) error { return nil }

func (s *Store) SaveTutorialFlag(ctx context.Context, account, id int64, index byte, completed bool) error {
	if index >= 101 {
		return fmt.Errorf("tutorial index out of range")
	}
	return s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, id, false); e != nil {
			return e
		} else if !ok {
			return fmt.Errorf("tutorial character is not owned")
		}
		rec := tutorialRow{CharacterID: id, Flag: int(index), Completed: completed, UpdatedAt: nowStamp()}
		return tx.Put("character_tutorial_flags", idKey(id)+"/"+strconv.Itoa(int(index)), rec)
	})
}

func (s *Store) TutorialFlags(ctx context.Context, account, id int64) ([]byte, error) {
	if _, ok, e := s.charRow(account, id, false); e != nil {
		return nil, e
	} else if !ok {
		return nil, fmt.Errorf("tutorial character is not owned")
	}
	prefix := idKey(id) + "/"
	var flags []int
	s.db.Each("character_tutorial_flags", func(key string, raw json.RawMessage) bool {
		if !strings.HasPrefix(key, prefix) {
			return true
		}
		var r tutorialRow
		if json.Unmarshal(raw, &r) == nil && r.Completed {
			flags = append(flags, r.Flag)
		}
		return true
	})
	// Keys sort lexicographically ("10" < "2"); restore numeric order.
	for i := 1; i < len(flags); i++ {
		for j := i; j > 0 && flags[j] < flags[j-1]; j-- {
			flags[j], flags[j-1] = flags[j-1], flags[j]
		}
	}
	out := make([]byte, 0, len(flags))
	for _, f := range flags {
		out = append(out, byte(f))
	}
	return out, nil
}
