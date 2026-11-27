// Package db100 is the 100US server's own single-file database.
//
// It replaces the borrowed PostgreSQL/Redis stack with a dependency-free store
// that opens in milliseconds and survives crashes: one JSON document on disk,
// written atomically (temp file + fsync + rename) after every mutation, with a
// header magic, a format version and a monotonically increasing revision.
//
// Tables are string-keyed maps of JSON rows. Concurrency is a single writer
// (the server process) guarded by an in-process mutex, plus per-character
// locks that replace the old Redis advisory locks.
package db100

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

const (
	magic   = "US100DB1"
	version = 1
)

type document struct {
	Magic    string                                `json:"magic"`
	Version  int                                   `json:"version"`
	Revision uint64                                `json:"revision"`
	Tables   map[string]map[string]json.RawMessage `json:"tables"`
}

// DB is an open database file.
type DB struct {
	path string
	abs  string
	refs int // guarded by registryMu

	mu     sync.RWMutex
	doc    document
	charMu sync.Map // character lock key -> *sync.Mutex
}

// Open databases by absolute path. One process holds one live snapshot per
// file: opening the same path twice returns the same instance (refcounted),
// so every Store in the process sees the same rows, exactly like connections
// to one PostgreSQL server did. A fresh process loads from disk.
var (
	registryMu sync.Mutex
	registry   = map[string]*DB{}
)

// Open loads path, creating an empty database when it does not exist. Callers
// pair it with Close, which releases the shared instance's reference.
func Open(path string) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if db, ok := registry[abs]; ok {
		db.refs++
		return db, nil
	}
	db := &DB{path: path, abs: abs, doc: document{Magic: magic, Version: version, Tables: map[string]map[string]json.RawMessage{}}}
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := db.flushLocked(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(raw, &db.doc); err != nil {
			return nil, fmt.Errorf("db100: open %s: %w", path, err)
		}
		if db.doc.Magic != magic || db.doc.Version != version {
			return nil, fmt.Errorf("db100: %s: unsupported header %q v%d", path, db.doc.Magic, db.doc.Version)
		}
		if db.doc.Tables == nil {
			db.doc.Tables = map[string]map[string]json.RawMessage{}
		}
	}
	db.refs = 1
	registry[abs] = db
	return db, nil
}

// Close releases one reference. The instance leaves the registry when the
// last reference goes away, so a later Open re-reads the file from disk.
func (db *DB) Close() error {
	registryMu.Lock()
	defer registryMu.Unlock()
	db.refs--
	if db.refs <= 0 {
		delete(registry, db.abs)
	}
	return nil
}

// Path reports the file backing the database.
func (db *DB) Path() string { return db.path }

// Revision is the mutation counter, useful for tests and diagnostics.
func (db *DB) Revision() uint64 {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.doc.Revision
}

// Get decodes table[key] into out when present.
func (db *DB) Get(table, key string, out any) (bool, error) {
	db.mu.RLock()
	raw, ok := db.doc.Tables[table][key]
	db.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if out == nil {
		return true, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return true, fmt.Errorf("db100: %s[%s]: %w", table, key, err)
	}
	return true, nil
}

// Raw returns the stored bytes for table[key] without decoding.
func (db *DB) Raw(table, key string) (json.RawMessage, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	raw, ok := db.doc.Tables[table][key]
	return raw, ok
}

// Put marshals v and stores it at table[key], persisting the file.
func (db *DB) Put(table, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("db100: %s[%s]: %w", table, key, err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.doc.Tables[table] == nil {
		db.doc.Tables[table] = map[string]json.RawMessage{}
	}
	db.doc.Tables[table][key] = raw
	db.doc.Revision++
	return db.flushLocked()
}

// Delete removes table[key], persisting the file. Missing keys are not errors.
func (db *DB) Delete(table, key string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, ok := db.doc.Tables[table][key]; !ok {
		return nil
	}
	delete(db.doc.Tables[table], key)
	db.doc.Revision++
	return db.flushLocked()
}

// Keys returns the sorted keys of a table.
func (db *DB) Keys(table string) []string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	rows := db.doc.Tables[table]
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Each decodes every row of a table in key order until fn returns false.
func (db *DB) Each(table string, fn func(key string, raw json.RawMessage) bool) {
	db.mu.RLock()
	rows := db.doc.Tables[table]
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	snapshot := make([]json.RawMessage, len(keys))
	for i, k := range keys {
		snapshot[i] = rows[k]
	}
	db.mu.RUnlock()
	for i, k := range keys {
		if !fn(k, snapshot[i]) {
			return
		}
	}
}

// Mutate runs fn under the write lock and persists once when fn reports a
// change. It is the transactional primitive the storage layer builds on.
func (db *DB) Mutate(fn func(tx *Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	backup := copyTables(db.doc.Tables)
	tx := &Tx{db: db, changed: false}
	if err := fn(tx); err != nil {
		// Roll back: a failed transaction must not leave in-memory writes.
		db.doc.Tables = backup
		return err
	}
	if tx.changed {
		db.doc.Revision++
		if err := db.flushLocked(); err != nil {
			db.doc.Tables = backup
			db.doc.Revision--
			return err
		}
		return nil
	}
	return nil
}

// copyTables deep-copies the table map. Row values are never mutated in place
// (Put always marshals fresh bytes), so copying the maps is enough.
func copyTables(src map[string]map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	out := make(map[string]map[string]json.RawMessage, len(src))
	for table, rows := range src {
		cp := make(map[string]json.RawMessage, len(rows))
		for k, v := range rows {
			cp[k] = v
		}
		out[table] = cp
	}
	return out
}

// Tx is a write transaction over the in-memory document.
type Tx struct {
	db      *DB
	changed bool
}

// Get decodes table[key] inside the transaction.
func (tx *Tx) Get(table, key string, out any) (bool, error) {
	raw, ok := tx.db.doc.Tables[table][key]
	if !ok {
		return false, nil
	}
	if out == nil {
		return true, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return true, fmt.Errorf("db100: %s[%s]: %w", table, key, err)
	}
	return true, nil
}

// Raw returns stored bytes inside the transaction.
func (tx *Tx) Raw(table, key string) (json.RawMessage, bool) {
	raw, ok := tx.db.doc.Tables[table][key]
	return raw, ok
}

// Put stores v inside the transaction.
func (tx *Tx) Put(table, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("db100: %s[%s]: %w", table, key, err)
	}
	if tx.db.doc.Tables[table] == nil {
		tx.db.doc.Tables[table] = map[string]json.RawMessage{}
	}
	tx.db.doc.Tables[table][key] = raw
	tx.changed = true
	return nil
}

// Delete removes a row inside the transaction.
func (tx *Tx) Delete(table, key string) {
	if _, ok := tx.db.doc.Tables[table][key]; !ok {
		return
	}
	delete(tx.db.doc.Tables[table], key)
	tx.changed = true
}

// Keys lists a table's keys inside the transaction.
func (tx *Tx) Keys(table string) []string {
	rows := tx.db.doc.Tables[table]
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CharLock returns the per-character mutex, replacing the Redis advisory lock.
func (db *DB) CharLock(key string) *sync.Mutex {
	v, _ := db.charMu.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// flushLocked writes the document atomically. Caller holds the write lock.
func (db *DB) flushLocked() error {
	raw, err := json.Marshal(&db.doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(db.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := db.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, db.path)
}

// ImportSeed loads a seed document (tables of row lists) produced by
// tools/export_pg_seed.py. Existing rows win; the seed only fills gaps.
func (db *DB) ImportSeed(seedPath string) error {
	raw, err := os.ReadFile(seedPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var seed map[string][]map[string]any
	if err := json.Unmarshal(raw, &seed); err != nil {
		return fmt.Errorf("db100: seed %s: %w", seedPath, err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	added := 0
	for table, rows := range seed {
		if db.doc.Tables[table] == nil {
			db.doc.Tables[table] = map[string]json.RawMessage{}
		}
		for _, row := range rows {
			key, ok := seedKey(table, row)
			if !ok {
				continue
			}
			if _, exists := db.doc.Tables[table][key]; exists {
				continue
			}
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			db.doc.Tables[table][key] = raw
			added++
		}
	}
	if added > 0 {
		db.doc.Revision++
		return db.flushLocked()
	}
	return nil
}

// seedKey picks the primary key column per table. The keys it builds are
// byte-identical to the ones the storage layer writes at runtime, so seeded
// rows and live rows share one namespace.
func seedKey(table string, row map[string]any) (string, bool) {
	var cols []string
	switch table {
	case "accounts", "characters":
		cols = []string{"id"}
	case "account_currency":
		cols = []string{"account_id"}
	case "admin_grants":
		cols = []string{"grant_id"}
	case "character_world", "character_birth", "character_vaults", "character_fatigue":
		cols = []string{"character_id"}
	case "character_fatigue_rooms", "character_map_clears":
		cols = []string{"character_id", "run_id", "map_id"}
	case "character_quests", "character_quest_rewards":
		cols = []string{"character_id", "quest_id"}
	case "character_quest_repairs":
		cols = []string{"character_id", "quest_id", "reason"}
	case "character_tutorial_flags":
		cols = []string{"character_id", "flag"}
	case "character_events":
		cols = []string{"character_id", "event_key"}
	default:
		return "", false
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		v, ok := row[c]
		if !ok {
			return "", false
		}
		switch t := v.(type) {
		case float64:
			// JSON numbers decode as float64; keep them integral-looking so
			// the key matches strconv's int64 formatting at runtime.
			parts = append(parts, strconv.FormatFloat(t, 'f', -1, 64))
		default:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	key := parts[0]
	for _, p := range parts[1:] {
		key += "/" + p
	}
	return key, true
}
