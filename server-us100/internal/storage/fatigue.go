package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"dfolan/internal/db100"
)

type FatigueState struct {
	Day     string `json:"day"`
	Used    uint16 `json:"used"`
	Limit   uint16 `json:"limit"`
	UsedMax uint16 `json:"used_max"`
}

type fatigueRow struct {
	CharacterID int64  `json:"character_id"`
	Day         string `json:"day"`
	Used        int    `json:"used"`
	DailyLimit  int    `json:"daily_limit"`
	UsedMax     int    `json:"used_max"`
	UpdatedAt   string `json:"updated_at"`
}

type fatigueRoomRow struct {
	CharacterID int64  `json:"character_id"`
	RunID       string `json:"run_id"`
	MapID       int64  `json:"map_id"`
	Day         string `json:"day"`
	Cost        int    `json:"cost"`
	CreatedAt   string `json:"created_at"`
}

func roomKey(id int64, run string, room uint32) string {
	return idKey(id) + "/" + run + "/" + strconv.FormatUint(uint64(room), 10)
}

func (s *Store) MigrateFatigue(ctx context.Context) error { return nil }

var ErrFatigueExhausted = errors.New("fatigue exhausted")

// rolloverFatigueTx upserts the day row exactly like the old atomic SQL: a
// newer day resets consumption and adopts the new limit, a same-or-older day
// keeps what was consumed (a backwards clock never grants a fresh quota).
func rolloverFatigueTx(tx *db100.Tx, account, id int64, day string, limit uint16, now time.Time) (FatigueState, error) {
	var out FatigueState
	if _, ok, e := charRowTx(tx, account, id, false); e != nil {
		return out, e
	} else if !ok {
		return out, ErrNoRows
	}
	var r fatigueRow
	found, e := tx.Get("character_fatigue", idKey(id), &r)
	if e != nil {
		return out, e
	}
	if !found {
		r = fatigueRow{CharacterID: id, Day: day, DailyLimit: int(limit)}
	} else if day > r.Day {
		r.Day, r.Used, r.UsedMax, r.DailyLimit = day, 0, 0, int(limit)
	} else if int(limit) != r.DailyLimit && limit > 0 {
		// Allow live fatigue events (weekend double / bonus FP / PC Bang) to
		// update today's cap immediately while preserving today's usage.
		r.DailyLimit = int(limit)
	}
	r.UpdatedAt = nowStamp()
	if e = tx.Put("character_fatigue", idKey(id), r); e != nil {
		return out, e
	}
	out.Day, out.Used, out.Limit, out.UsedMax = r.Day, uint16(r.Used), uint16(r.DailyLimit), uint16(r.UsedMax)
	// The stored daily limit is the base for this reset day. Derive the
	// entitlement inside the same account/room transaction, never as a refill.
	var ends map[byte]int64
	if _, err := tx.Get("us224_account_premium", idKey(account), &ends); err != nil {
		return FatigueState{}, err
	}
	if end := ends[NeoPremiumPlusKind]; end > 0 && end > now.Unix() && out.Limit < 273 {
		out.Limit = 273
	}
	return out, nil
}

// Expiry can leave actual consumption above the base cap. Keep that history
// in storage for a same-day renewal; never emit a negative remaining balance.
func (s FatigueState) displayable() FatigueState {
	if s.Used > s.Limit {
		s.Used = s.Limit
	}
	if s.UsedMax > s.Limit {
		s.UsedMax = s.Limit
	}
	return s
}

// The fatigue row serializes concurrent charges and rollover. The room ledger
// makes retries idempotent even after an uncertain commit or response failure.
func (s *Store) ConsumeRoomFatigue(ctx context.Context, account, id int64, day string, limit uint16, run string, room uint32, cost uint16) (FatigueState, bool, error) {
	return s.ConsumeRoomFatigueAt(ctx, account, id, day, limit, run, room, cost, time.Now())
}
func (s *Store) ConsumeRoomFatigueAt(ctx context.Context, account, id int64, day string, limit uint16, run string, room uint32, cost uint16, now time.Time) (FatigueState, bool, error) {
	var out FatigueState
	var charged bool
	if limit == 0 || len(run) != 32 || room == 0 {
		return out, false, fmt.Errorf("invalid fatigue charge")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		state, e := rolloverFatigueTx(tx, account, id, day, limit, now)
		if e != nil {
			return e
		}
		out = state
		if ok, _ := tx.Get("character_fatigue_rooms", roomKey(id, run, room), nil); ok {
			return nil // retry: already charged for this room
		}
		if cost > 0 && out.Used >= out.Limit {
			return ErrFatigueExhausted // rolls the rollover back, as the old tx did
		}
		// A room is affordable whenever some fatigue remains. Clamp at zero;
		// configurable costs never wrap the unsigned wire counters.
		if remaining := int(out.Limit) - int(out.Used); int(cost) > remaining && cost > 0 {
			cost = uint16(remaining)
		}
		out.Used += cost
		if out.Used > out.UsedMax {
			out.UsedMax = out.Used
		}
		rec := fatigueRoomRow{CharacterID: id, RunID: run, MapID: int64(room), Day: out.Day, Cost: int(cost), CreatedAt: nowStamp()}
		if e = tx.Put("character_fatigue_rooms", roomKey(id, run, room), rec); e != nil {
			return e
		}
		var r fatigueRow
		if _, e = tx.Get("character_fatigue", idKey(id), &r); e != nil {
			return e
		}
		r.Used, r.UsedMax, r.UpdatedAt = int(out.Used), int(out.UsedMax), nowStamp()
		if e = tx.Put("character_fatigue", idKey(id), r); e != nil {
			return e
		}
		charged = true
		return nil
	})
	if e != nil {
		return out, false, e
	}
	return out.displayable(), charged, nil
}

// Ownership and rollover are one atomic statement. Reconnecting on the same
// day preserves consumption. A backwards clock never grants a fresh quota.
func (s *Store) LoadFatigue(ctx context.Context, account, id int64, day string, limit uint16) (FatigueState, error) {
	return s.LoadFatigueAt(ctx, account, id, day, limit, time.Now())
}
func (s *Store) LoadFatigueAt(ctx context.Context, account, id int64, day string, limit uint16, now time.Time) (FatigueState, error) {
	var out FatigueState
	if limit == 0 {
		return out, errors.New("zero fatigue limit")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		state, e := rolloverFatigueTx(tx, account, id, day, limit, now)
		out = state
		return e
	})
	return out.displayable(), e
}
