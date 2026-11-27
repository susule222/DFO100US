package storage

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"dfolan/internal/db100"
)

// ActivityProgress persists per-character daily challenge, weekly dungeon,
// lifetime clear/kill counters (Adventure Club), and one-time/daily activity claims.
type ActivityProgress struct {
	CharacterID       int64             `json:"character_id"`
	Day               string            `json:"day"`
	DailyKills        int               `json:"daily_kills"`
	DailyClears       int               `json:"daily_clears"`
	DailyKillClaimed  bool              `json:"daily_kill_claimed"`
	DailyClearClaimed bool              `json:"daily_clear_claimed"`
	Week              string            `json:"week"`
	WeeklyClears      int               `json:"weekly_clears"`
	TotalKills        uint32            `json:"total_kills"`
	TotalClears       uint32            `json:"total_clears"`
	TotalHellClears   uint32            `json:"total_hell_clears"`
	Claims            map[string]string `json:"claims,omitempty"`
	UpdatedAt         string            `json:"updated_at"`
}

// AccountActivityStats summarizes account-wide activity totals across all characters
// for Adventure Club overview (CMD 1395) and launcher display.
type AccountActivityStats struct {
	TotalKills      uint32
	TotalClears     uint32
	TotalHellClears uint32
	CharacterCount  uint32
}

type accountMileageDailyRow struct {
	AccountID int64  `json:"account_id"`
	Day       string `json:"day"`
	Earned    int64  `json:"earned"`
	UpdatedAt string `json:"updated_at"`
}

func currencyRowTx(tx *db100.Tx, account int64) (currencyRow, error) {
	var r currencyRow
	ok, e := tx.Get("account_currency", idKey(account), &r)
	if e != nil {
		return currencyRow{}, e
	}
	if !ok {
		return currencyRow{AccountID: account}, nil
	}
	if r.Cera < 0 {
		r.Cera = 0
	}
	if r.Mileage < 0 {
		r.Mileage = 0
	}
	r.AccountID = account
	return r, nil
}

func mileageTx(tx *db100.Tx, account int64) (int64, error) {
	r, e := currencyRowTx(tx, account)
	return r.Mileage, e
}

// AccountBalances returns both CERA and Mileage balances for an account.
func (s *Store) AccountBalances(ctx context.Context, account int64) (cera uint64, mileage uint64, err error) {
	err = s.db.Mutate(func(tx *db100.Tx) error {
		r, e := currencyRowTx(tx, account)
		if e != nil {
			return e
		}
		cera, mileage = uint64(r.Cera), uint64(r.Mileage)
		return nil
	})
	return cera, mileage, err
}

// AccountMileage returns the account's Mileage balance.
func (s *Store) AccountMileage(ctx context.Context, account int64) (uint64, error) {
	_, m, err := s.AccountBalances(ctx, account)
	return m, err
}

// AddAccountMileage credits Mileage to an account, optionally respecting a
// per-day cap (dailyCap > 0 with non-empty dayKey) matching etc/mileageshop.etc.
func (s *Store) AddAccountMileage(ctx context.Context, account, delta int64, dayKey string, dailyCap int64) (added int64, total uint64, err error) {
	if account <= 0 || delta <= 0 {
		bal, e := s.AccountMileage(ctx, account)
		return 0, bal, e
	}
	err = s.db.Mutate(func(tx *db100.Tx) error {
		cur, e := currencyRowTx(tx, account)
		if e != nil {
			return e
		}
		grant := delta
		if dayKey != "" && dailyCap > 0 {
			var d accountMileageDailyRow
			ok, e := tx.Get("account_mileage_daily", idKey(account), &d)
			if e != nil {
				return e
			}
			if !ok || d.Day != dayKey {
				d = accountMileageDailyRow{AccountID: account, Day: dayKey, Earned: 0}
			}
			rem := dailyCap - d.Earned
			if rem <= 0 {
				total = uint64(cur.Mileage)
				return nil
			}
			if grant > rem {
				grant = rem
			}
			d.Earned += grant
			d.UpdatedAt = nowStamp()
			if e = tx.Put("account_mileage_daily", idKey(account), d); e != nil {
				return e
			}
		}
		next := cur.Mileage + grant
		if next > math.MaxUint32 {
			next = math.MaxUint32
		}
		cur.Mileage = next
		cur.UpdatedAt = nowStamp()
		if e = tx.Put("account_currency", idKey(account), cur); e != nil {
			return e
		}
		added = grant
		total = uint64(cur.Mileage)
		return nil
	})
	return added, total, err
}

// LoadActivityProgress reads the stored daily/weekly/lifetime progress for a character.
func (s *Store) LoadActivityProgress(ctx context.Context, account, charID int64) (ActivityProgress, error) {
	var out ActivityProgress
	if account <= 0 || charID <= 0 {
		return out, fmt.Errorf("invalid activity progress target")
	}
	err := s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, charID, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		ok, e := tx.Get("character_activity_progress", idKey(charID), &out)
		if e != nil {
			return e
		}
		if !ok {
			out = ActivityProgress{CharacterID: charID}
		}
		return nil
	})
	return out, err
}

// RecordActivityKill increments daily & lifetime monster kill counts for a character
// and reports whether the daily kill challenge threshold was just reached for today.
func (s *Store) RecordActivityKill(ctx context.Context, account, charID int64, day string, killTarget int) (ActivityProgress, bool, error) {
	var out ActivityProgress
	var justReached bool
	if account <= 0 || charID <= 0 || day == "" {
		return out, false, fmt.Errorf("invalid activity kill input")
	}
	err := s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, charID, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		var p ActivityProgress
		ok, e := tx.Get("character_activity_progress", idKey(charID), &p)
		if e != nil {
			return e
		}
		if !ok {
			p = ActivityProgress{CharacterID: charID}
		}
		if p.Day != day {
			p.Day = day
			p.DailyKills = 0
			p.DailyClears = 0
			p.DailyKillClaimed = false
			p.DailyClearClaimed = false
		}
		p.DailyKills++
		p.TotalKills++
		if killTarget > 0 && !p.DailyKillClaimed && p.DailyKills >= killTarget {
			p.DailyKillClaimed = true
			justReached = true
		}
		p.UpdatedAt = nowStamp()
		if e = tx.Put("character_activity_progress", idKey(charID), p); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, justReached, err
}

// RecordActivityClear increments daily, weekly, and lifetime clear counts for a character
// and reports whether the daily clear challenge and/or weekly bonus slot was triggered.
func (s *Store) RecordActivityClear(ctx context.Context, account, charID int64, day, week string, clearTarget, weeklyLimit int, wasHell bool) (ActivityProgress, bool, bool, error) {
	var out ActivityProgress
	var dailyClearReached, weeklyBonusGranted bool
	if account <= 0 || charID <= 0 || day == "" || week == "" {
		return out, false, false, fmt.Errorf("invalid activity clear input")
	}
	err := s.db.Mutate(func(tx *db100.Tx) error {
		if _, ok, e := charRowTx(tx, account, charID, false); e != nil {
			return e
		} else if !ok {
			return ErrNoRows
		}
		var p ActivityProgress
		ok, e := tx.Get("character_activity_progress", idKey(charID), &p)
		if e != nil {
			return e
		}
		if !ok {
			p = ActivityProgress{CharacterID: charID}
		}
		if p.Day != day {
			p.Day = day
			p.DailyKills = 0
			p.DailyClears = 0
			p.DailyKillClaimed = false
			p.DailyClearClaimed = false
		}
		if p.Week != week {
			p.Week = week
			p.WeeklyClears = 0
		}
		p.DailyClears++
		p.TotalClears++
		if wasHell {
			p.TotalHellClears++
		}
		if clearTarget > 0 && !p.DailyClearClaimed && p.DailyClears >= clearTarget {
			p.DailyClearClaimed = true
			dailyClearReached = true
		}
		if weeklyLimit > 0 && p.WeeklyClears < weeklyLimit {
			p.WeeklyClears++
			weeklyBonusGranted = true
		}
		p.UpdatedAt = nowStamp()
		if e = tx.Put("character_activity_progress", idKey(charID), p); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, dailyClearReached, weeklyBonusGranted, err
}

// AccountActivitySummary aggregates live character counts and clears/kills for CMD 1395.
func (s *Store) AccountActivitySummary(ctx context.Context, account int64) (AccountActivityStats, error) {
	var out AccountActivityStats
	err := s.db.Mutate(func(tx *db100.Tx) error {
		liveChars := map[int64]bool{}
		for _, k := range tx.Keys("characters") {
			var r characterRow
			if ok, _ := tx.Get("characters", k, &r); ok && r.AccountID == account && r.DeletedAt == nil {
				liveChars[r.ID] = true
				out.CharacterCount++
			}
		}
		for cid := range liveChars {
			var p ActivityProgress
			if ok, _ := tx.Get("character_activity_progress", idKey(cid), &p); ok {
				out.TotalKills += p.TotalKills
				out.TotalClears += p.TotalClears
				out.TotalHellClears += p.TotalHellClears
			}
		}
		return nil
	})
	return out, err
}

// AccountMileage returns the account's Mileage balance inside an AdminTx.
func (a *AdminTx) AccountMileage() (int64, error) {
	return mileageTx(a.tx, a.account)
}

// SetAccountMileage updates only the account's Mileage while preserving CERA.
func (a *AdminTx) SetAccountMileage(v int64) error {
	if v < 0 || v > math.MaxUint32 {
		return fmt.Errorf("mileage balance out of range (0..%d)", uint32(math.MaxUint32))
	}
	cur, err := currencyRowTx(a.tx, a.account)
	if err != nil {
		return err
	}
	cur.Mileage = v
	cur.UpdatedAt = nowStamp()
	return a.tx.Put("account_currency", idKey(a.account), cur)
}

// SetAccountPremiumContract extends, sets, or clears a premium contract kind (22/27/73/79/92/118/120/125)
// inside an AdminTx. days > 0 extends from max(now, current_end); days == 0 removes it.
func (a *AdminTx) SetAccountPremiumContract(kind byte, days int64, nowUnix int64) (int64, error) {
	if kind == 0 || kind >= 126 {
		return 0, fmt.Errorf("invalid contract kind %d", kind)
	}
	if days < 0 || days > 3650 {
		return 0, fmt.Errorf("contract days out of range (0..3650)")
	}
	var ends map[byte]int64
	if _, err := a.tx.Get("us224_account_premium", idKey(a.account), &ends); err != nil {
		return 0, err
	}
	if ends == nil {
		ends = map[byte]int64{}
	}
	if days == 0 {
		delete(ends, kind)
		if err := a.tx.Put("us224_account_premium", idKey(a.account), ends); err != nil {
			return 0, err
		}
		return 0, nil
	}
	base := ends[kind]
	if base < nowUnix {
		base = nowUnix
	}
	end := base + days*86400
	ends[kind] = end
	if err := a.tx.Put("us224_account_premium", idKey(a.account), ends); err != nil {
		return 0, err
	}
	return end, nil
}

// ClaimCharacterActivity records a named claim key (e.g. "checkin:2026-11-27" or "seria_welcome")
// on the target character inside an AdminTx; returns alreadyClaimed=true if already recorded.
func (a *AdminTx) ClaimCharacterActivity(claimKey string) (bool, error) {
	claimKey = strings.TrimSpace(claimKey)
	if a.charID <= 0 || claimKey == "" {
		return false, fmt.Errorf("invalid activity claim key")
	}
	var p ActivityProgress
	ok, err := a.tx.Get("character_activity_progress", idKey(a.charID), &p)
	if err != nil {
		return false, err
	}
	if !ok {
		p = ActivityProgress{CharacterID: a.charID}
	}
	if p.Claims == nil {
		p.Claims = map[string]string{}
	}
	if _, exists := p.Claims[claimKey]; exists {
		return true, nil
	}
	p.Claims[claimKey] = nowStamp()
	p.UpdatedAt = nowStamp()
	if err = a.tx.Put("character_activity_progress", idKey(a.charID), p); err != nil {
		return false, err
	}
	return false, nil
}

// ExtendPremiumContracts atomically activates or extends one or more premium
// contract kinds (22/27/73/79/92/118/120/125) on the account by the given days.
func (s *Store) ExtendPremiumContracts(ctx context.Context, account int64, daysByKind map[byte]int64) error {
	if account <= 0 || len(daysByKind) == 0 {
		return nil
	}
	nowUnix := time.Now().Unix()
	return s.db.Mutate(func(tx *db100.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var ends map[byte]int64
		if _, err := tx.Get("us224_account_premium", idKey(account), &ends); err != nil {
			return err
		}
		if ends == nil {
			ends = map[byte]int64{}
		}
		for kind, days := range daysByKind {
			if kind == 0 || kind >= 126 || days <= 0 {
				continue
			}
			base := ends[kind]
			if base < nowUnix {
				base = nowUnix
			}
			ends[kind] = base + days*86400
		}
		return tx.Put("us224_account_premium", idKey(account), ends)
	})
}
