package db100

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type row struct {
	HP  int    `json:"hp"`
	Tag string `json:"tag"`
}

func TestPutGetDeleteReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "us100.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	if db.Revision() != 0 {
		t.Fatalf("fresh revision = %d", db.Revision())
	}
	if err := db.Put("characters", "11", row{HP: 31600, Tag: "live"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if db.Revision() != 1 {
		t.Fatalf("revision after put = %d", db.Revision())
	}

	var got row
	ok, err := db.Get("characters", "11", &got)
	if err != nil || !ok || got.HP != 31600 || got.Tag != "live" {
		t.Fatalf("get = %v,%v,%+v", ok, err, got)
	}

	// Close and reopen: the mutation must reload from disk, not from the
	// in-process shared instance.
	db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	ok, err = db2.Get("characters", "11", &got)
	if err != nil || !ok || got.HP != 31600 {
		t.Fatalf("get after reopen = %v,%v,%+v", ok, err, got)
	}
	if db2.Revision() != 1 {
		t.Fatalf("revision after reopen = %d", db2.Revision())
	}

	if err := db2.Delete("characters", "11"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, _ := db2.Get("characters", "11", nil); ok {
		t.Fatal("row still present after delete")
	}
	// Deleting a missing key is a no-op, not an error, and bumps nothing.
	if err := db2.Delete("characters", "11"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if db2.Revision() != 2 {
		t.Fatalf("revision after delete = %d", db2.Revision())
	}
}

func TestHeaderGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(path, []byte(`{"magic":"NOPE","version":1,"revision":0,"tables":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("wrong magic accepted")
	}
	if err := os.WriteFile(path, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestMutateTransaction(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "tx.db"))
	if err != nil {
		t.Fatal(err)
	}
	err = db.Mutate(func(tx *Tx) error {
		if err := tx.Put("character_world", "1/2", row{HP: 1}); err != nil {
			return err
		}
		return tx.Put("character_world", "1/3", row{HP: 2})
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if keys := db.Keys("character_world"); len(keys) != 2 || keys[0] != "1/2" {
		t.Fatalf("keys = %v", keys)
	}
	if db.Revision() != 1 {
		t.Fatalf("two-row tx revision = %d, want single bump", db.Revision())
	}

	// A failing transaction must not persist partial writes.
	failErr := os.ErrInvalid
	err = db.Mutate(func(tx *Tx) error {
		if err := tx.Put("character_world", "1/9", row{HP: 9}); err != nil {
			return err
		}
		return failErr
	})
	if err != failErr {
		t.Fatalf("mutate error = %v", err)
	}
	if ok, _ := db.Get("character_world", "1/9", nil); ok {
		t.Fatal("failed tx left a row")
	}
}

func TestEachOrder(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "each.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"c", "a", "b"} {
		if err := db.Put("t", k, row{Tag: k}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	db.Each("t", func(key string, raw json.RawMessage) bool {
		seen = append(seen, key)
		return true
	})
	if len(seen) != 3 || seen[0] != "a" || seen[2] != "c" {
		t.Fatalf("Each order = %v", seen)
	}
	// Early stop.
	count := 0
	db.Each("t", func(string, json.RawMessage) bool { count++; return false })
	if count != 1 {
		t.Fatalf("Each early stop visited %d", count)
	}
}

func TestCrashConsistency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put("characters", "1", row{HP: 100}); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash that left a half-written temp file behind and a valid
	// main file: rename-based flush means the main file is either the old or
	// the new document, never a mix.
	if err := os.WriteFile(path+".tmp", []byte(`{"magic":"US100DB`), 0o644); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("open after crash: %v", err)
	}
	defer db2.Close()
	var got row
	ok, err := db2.Get("characters", "1", &got)
	if err != nil || !ok || got.HP != 100 {
		t.Fatalf("post-crash row = %v,%v,%+v", ok, err, got)
	}
	// The next write must replace the stale tmp, not fail on it.
	if err := db2.Put("characters", "1", row{HP: 200}); err != nil {
		t.Fatalf("put over stale tmp: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file left behind: %v", err)
	}
}

func TestRegistrySharedInstance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("same path opened twice returned different instances")
	}
	if err := a.Put("t", "1", row{HP: 7}); err != nil {
		t.Fatal(err)
	}
	var got row
	ok, err := b.Get("t", "1", &got)
	if err != nil || !ok || got.HP != 7 {
		t.Fatalf("second handle did not see the write: %v %v %+v", ok, err, got)
	}
	// Two refs: closing one keeps the instance registered.
	a.Close()
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c != b {
		t.Fatal("instance replaced while a reference was still open")
	}
	b.Close()
	c.Close()
	// All refs released: the next open must reload from disk.
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d == b {
		t.Fatal("closed instance was handed out again")
	}
	ok, err = d.Get("t", "1", &got)
	if err != nil || !ok || got.HP != 7 {
		t.Fatalf("reload lost the row: %v %v %+v", ok, err, got)
	}
}

func TestCharLockSerializes(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "lock.db"))
	if err != nil {
		t.Fatal(err)
	}
	l1 := db.CharLock("acct-1")
	l2 := db.CharLock("acct-1")
	if l1 != l2 {
		t.Fatal("CharLock returned different mutexes for one key")
	}
	l1.Lock()
	done := make(chan struct{})
	go func() { l2.Lock(); l2.Unlock(); close(done) }()
	select {
	case <-done:
		t.Fatal("second lock acquired while first held")
	default:
	}
	l1.Unlock()
	<-done
}

func TestImportSeed(t *testing.T) {
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.json")
	seed := map[string][]map[string]any{
		"characters": {
			{"id": float64(11), "account_id": float64(1), "hp": float64(31600)},
		},
		"character_world": {
			{"character_id": float64(11), "revision": float64(1)},
		},
		"character_quests": {
			{"character_id": float64(11), "quest_id": float64(3145), "status": "accepted"},
		},
	}
	raw, _ := json.Marshal(seed)
	if err := os.WriteFile(seedPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := Open(filepath.Join(dir, "seeded.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ImportSeed(seedPath); err != nil {
		t.Fatalf("import: %v", err)
	}
	if keys := db.Keys("characters"); len(keys) != 1 || keys[0] != "11" {
		t.Fatalf("seeded characters keys = %v", keys)
	}
	if keys := db.Keys("character_world"); len(keys) != 1 || keys[0] != "11" {
		t.Fatalf("seeded world keys = %v", keys)
	}
	if keys := db.Keys("character_quests"); len(keys) != 1 || keys[0] != "11/3145" {
		t.Fatalf("seeded quest keys = %v", keys)
	}
	// Existing rows win over seed rows.
	if err := db.Put("characters", "11", row{HP: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.ImportSeed(seedPath); err != nil {
		t.Fatal(err)
	}
	var got row
	if _, err := db.Get("characters", "11", &got); err != nil || got.HP != 1 {
		t.Fatalf("seed overwrote live row: %v %+v", err, got)
	}
	// Missing seed file is a no-op.
	if err := db.ImportSeed(filepath.Join(dir, "missing.json")); err != nil {
		t.Fatalf("missing seed: %v", err)
	}
}
