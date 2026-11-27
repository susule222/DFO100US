package storage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dfolan/internal/db100"
)

type eventRow struct {
	CharacterID   int64           `json:"character_id"`
	EventKey      string          `json:"event_key"`
	ConfigVersion string          `json:"config_version"`
	Model         string          `json:"model"`
	Outcome       json.RawMessage `json:"outcome"`
	CreatedAt     string          `json:"created_at"`
}

func eventKey(id int64, key string) string { return idKey(id) + "/" + key }

func (s *Store) MigrateCharacterEvents(ctx context.Context) error { return nil }

func (s *Store) CharacterEventReceipt(ctx context.Context, account, id int64, key string) (json.RawMessage, error) {
	if _, ok, e := s.charRow(account, id, false); e != nil {
		return nil, e
	} else if !ok {
		return nil, ErrNoRows
	}
	var r eventRow
	found, e := s.db.Get("character_events", eventKey(id, key), &r)
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, ErrNoRows
	}
	return r.Outcome, nil
}

// MonsterRunExperience sums the recorded per-monster gains of one run for
// this character — the clear path's receipt total, replacing the old SQL
// SUM over the event table.
func (s *Store) MonsterRunExperience(ctx context.Context, account, id int64, runID string) (uint64, error) {
	if _, ok, e := s.charRow(account, id, false); e != nil {
		return 0, e
	} else if !ok {
		return 0, ErrNoRows
	}
	prefix := idKey(id) + "/monster:" + runID + ":"
	var total uint64
	s.db.Each("character_events", func(key string, raw json.RawMessage) bool {
		if !strings.HasPrefix(key, prefix) {
			return true
		}
		var r eventRow
		if json.Unmarshal(raw, &r) != nil {
			return true
		}
		var outcome struct {
			Gain json.Number `json:"gain"`
		}
		if json.Unmarshal(r.Outcome, &outcome) == nil {
			if v, e := outcome.Gain.Int64(); e == nil && v > 0 {
				total += uint64(v)
			}
		}
		return true
	})
	return total, nil
}

// CommitCharacterEvent owns atomic state/receipt persistence. Its callback is
// pure domain code and runs only under the database write lock, which
// serialises it exactly like the owning character's row lock used to.
// Retries never recompute a reward using a later level or changed rules.
func (s *Store) CommitCharacterEvent(ctx context.Context, account, id int64, version, key, model string, apply func(Character) (json.RawMessage, json.RawMessage, error)) (Character, bool, error) {
	return s.commitEvent(ctx, account, id, version, key, model, apply, false)
}

// Contract kind IDs from US100 etc/premiumlist_new.etc (checked by HasPremium 0x143a56b00).
const (
	ConquerorContractKind byte = 22
	TacticianContractKind byte = 27
	GrowthContractKind    byte = 79
	CubeContractKind      byte = 92
	VIPContractKind       byte = 120
)

// Types and seven-day terms are from etc/premiumlist_new.etc (items 45,33,10000389,2660410).
var neoKinds = [...]byte{ConquerorContractKind, TacticianContractKind, GrowthContractKind, CubeContractKind}

type NeoPremium struct {
	Kind    byte
	Seconds uint64
}

func (s *Store) ActivePremiumKinds(account int64, now time.Time) (map[byte]bool, error) {
	if s == nil || account <= 0 {
		return nil, nil
	}
	var times map[byte]int64
	_, e := s.db.Get("us224_account_premium", idKey(account), &times)
	if e != nil {
		return nil, e
	}
	ts := now.Unix()
	active := make(map[byte]bool, len(times))
	for k, end := range times {
		if end > ts {
			active[k] = true
		}
	}
	return active, nil
}

func (s *Store) NeoPremiumSeconds(account int64) ([]NeoPremium, error) {
	var times map[byte]int64
	_, e := s.db.Get("us224_account_premium", idKey(account), &times)
	if e != nil {
		return nil, e
	}
	now := time.Now().Unix()
	var rows []NeoPremium
	for _, k := range shopPremiumKinds {
		if end := times[k]; end > now {
			rows = append(rows, NeoPremium{k, uint64(end - now)})
		}
	}
	return rows, nil
}
func (s *Store) CommitNeoContractBox(ctx context.Context, account, id int64, version, key string, apply func(Character) (json.RawMessage, json.RawMessage, error)) (Character, bool, error) {
	return s.commitEvent(ctx, account, id, version, key, "US224-neo-bonus-v1", apply, true)
}
func (s *Store) commitEvent(ctx context.Context, account, id int64, version, key, model string, apply func(Character) (json.RawMessage, json.RawMessage, error), extend bool) (Character, bool, error) {
	var role Character
	decoded, e := hex.DecodeString(version)
	if e != nil || len(decoded) != 32 || key == "" || len(key) > 200 || model == "" || len(model) > 100 || apply == nil {
		return role, false, fmt.Errorf("invalid character event")
	}
	var applied bool
	e = s.db.Mutate(func(tx *db100.Tx) error {
		row, ok, e := charRowTx(tx, account, id, true)
		if e != nil {
			return e
		}
		if !ok {
			return ErrNoRows
		}
		if row.ConfigVersion != version {
			return fmt.Errorf("character event source mismatch")
		}
		var prior eventRow
		found, e := tx.Get("character_events", eventKey(id, key), &prior)
		if e != nil {
			return e
		}
		if found {
			if prior.Model != model {
				return fmt.Errorf("character event model mismatch")
			}
			role, e = row.character()
			return e
		}
		role, e = row.character()
		if e != nil {
			return e
		}
		state, outcome, e := apply(role)
		if e != nil {
			return e
		}
		if !json.Valid(state) || !json.Valid(outcome) {
			return fmt.Errorf("invalid event JSON")
		}
		if extend {
			var times map[byte]int64
			_, err := tx.Get("us224_account_premium", idKey(account), &times)
			if err != nil {
				return err
			}
			if times == nil {
				times = map[byte]int64{}
			}
			now := time.Now().UTC().Unix()
			for _, k := range neoKinds {
				base := times[k]
				if base < now {
					base = now
				}
				if base > 1<<62 {
					return fmt.Errorf("invalid premium end time")
				}
				times[k] = base + 7*24*60*60
			}
			if err = tx.Put("us224_account_premium", idKey(account), times); err != nil {
				return err
			}
		}
		rec := eventRow{CharacterID: id, EventKey: key, ConfigVersion: version, Model: model, Outcome: outcome, CreatedAt: nowStamp()}
		if e = tx.Put("character_events", eventKey(id, key), rec); e != nil {
			return e
		}
		row.State = state
		if e = tx.Put("characters", idKey(id), row); e != nil {
			return e
		}
		role.State = state
		applied = true
		return nil
	})
	if e != nil {
		return role, false, e
	}
	return role, applied, nil
}
