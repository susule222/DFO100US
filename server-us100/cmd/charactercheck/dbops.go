package main

// Row-level fault injection against the temporary db100 file. These helpers
// replace the raw SQL the checks used under PostgreSQL: same corrupt-legacy-
// state intent, expressed as direct document edits through Store.Database().

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"dfolan/internal/db100"
	"dfolan/internal/storage"
)

func idKey(id int64) string { return strconv.FormatInt(id, 10) }

// rowGet decodes one stored row.
func rowGet(s *storage.Store, table, key string) (map[string]any, error) {
	var row map[string]any
	ok, e := s.Database().Get(table, key, &row)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, fmt.Errorf("%s[%s]: no row", table, key)
	}
	return row, nil
}

// rowUpdate edits one stored row in place under the database write lock.
func rowUpdate(s *storage.Store, table, key string, edit func(map[string]any) error) error {
	return s.Database().Mutate(func(tx *db100.Tx) error {
		var row map[string]any
		ok, e := tx.Get(table, key, &row)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("%s[%s]: no row", table, key)
		}
		if e := edit(row); e != nil {
			return e
		}
		return tx.Put(table, key, row)
	})
}

func rowDelete(s *storage.Store, table, key string) error {
	return s.Database().Mutate(func(tx *db100.Tx) error {
		tx.Delete(table, key)
		return nil
	})
}

func rowInsert(s *storage.Store, table, key string, row map[string]any) error {
	return s.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put(table, key, row)
	})
}

// rowCount counts rows whose key starts with prefix (a character id, or a
// character id plus a run id, etc.).
func rowCount(s *storage.Store, table, prefix string) int {
	n := 0
	s.Database().Each(table, func(key string, _ json.RawMessage) bool {
		if strings.HasPrefix(key, prefix) {
			n++
		}
		return true
	})
	return n
}

// setCharState is the old `UPDATE characters SET state=$2 WHERE id=$1`.
func setCharState(s *storage.Store, id int64, state json.RawMessage) error {
	return rowUpdate(s, "characters", idKey(id), func(row map[string]any) error {
		row["state"] = state
		return nil
	})
}

// setQuest is the old `UPDATE character_quests SET progress=...,
// progress_model=... WHERE character_id=$1 AND quest_id=$2`. A nil field is
// left untouched.
func setQuest(s *storage.Store, id int64, qid int, progress *int, model *string) error {
	key := idKey(id) + "/" + strconv.Itoa(qid)
	return rowUpdate(s, "character_quests", key, func(row map[string]any) error {
		if progress != nil {
			row["progress"] = *progress
		}
		if model != nil {
			row["progress_model"] = *model
		}
		return nil
	})
}
