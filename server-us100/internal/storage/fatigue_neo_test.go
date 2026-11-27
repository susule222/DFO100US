package storage

import (
	"context"
	"dfolan/internal/db100"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestNeoQuotaLifecycle(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, Config{DBPath: filepath.Join(t.TempDir(), "test.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	err := s.db.Mutate(func(tx *db100.Tx) error {
		for _, v := range []struct{ id, acct int64 }{{9, 4}, {10, 4}, {11, 5}} {
			if e := tx.Put("characters", idKey(v.id), characterRow{ID: v.id, AccountID: v.acct, State: json.RawMessage("{}")}); e != nil {
				return e
			}
		}
		return tx.Put("us224_account_premium", "4", map[byte]int64{120: 4000})
	})
	if err != nil {
		t.Fatal(err)
	}
	day := "2026-11-25"
	run := "0123456789abcdef0123456789abcdef"
	at := func(n int64) time.Time { return time.Unix(n, 0) }
	setEnd := func(end int64) {
		t.Helper()
		if e := s.db.Mutate(func(tx *db100.Tx) error {
			return tx.Put("us224_account_premium", "4", map[byte]int64{118: end, 120: 4000})
		}); e != nil {
			t.Fatal(e)
		}
	}
	state := func(acct, id, ts int64, used, limit uint16) {
		t.Helper()
		f, e := s.LoadFatigueAt(ctx, acct, id, day, 156, at(ts))
		if e != nil || f.Used != used || f.Limit != limit || f.UsedMax != used {
			t.Fatalf("state %+v %v", f, e)
		}
	}
	charge := func(room uint32, cost uint16, ts int64, applied bool, used uint16) {
		t.Helper()
		f, ok, e := s.ConsumeRoomFatigueAt(ctx, 4, 9, day, 156, run, room, cost, at(ts))
		if e != nil || ok != applied || f.Used != used {
			t.Fatalf("charge %+v %t %v", f, ok, e)
		}
	}
	charge(1, 43, 100, true, 43)
	state(4, 9, 100, 43, 156)
	setEnd(200)
	state(4, 9, 100, 43, 273)
	state(4, 10, 100, 0, 273)
	state(5, 11, 100, 0, 156)
	if _, e = s.LoadFatigueAt(ctx, 5, 9, day, 156, at(100)); !errors.Is(e, ErrNoRows) {
		t.Fatal("foreign character", e)
	}
	charge(2, 157, 100, true, 200)
	charge(2, 157, 100, false, 200)
	state(4, 9, 200, 156, 156)
	if _, _, e = s.ConsumeRoomFatigueAt(ctx, 4, 9, day, 156, run, 3, 1, at(200)); !errors.Is(e, ErrFatigueExhausted) {
		t.Fatalf("expired: %v", e)
	}
	charge(4, 0, 200, true, 156)
	setEnd(300)
	state(4, 9, 201, 200, 273)
	charge(3, 1, 201, true, 201)
	charge(5, 100, 201, true, 273)
	if _, _, e = s.ConsumeRoomFatigueAt(ctx, 4, 9, day, 156, run, 6, 1, at(201)); !errors.Is(e, ErrFatigueExhausted) {
		t.Fatalf("member exhausted: %v", e)
	}
	day = "2026-11-26"
	state(4, 9, 202, 0, 273)
}
