package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicyNeutralAndMultipliers(t *testing.T) {
	n := Neutral()
	if n.EffectiveExpMultiplier(160) != 1.0 {
		t.Fatalf("neutral exp multiplier = %v, want 1.0", n.EffectiveExpMultiplier(160))
	}
	if n.EffectiveDropMultiplier(true) != 1.0 {
		t.Fatalf("neutral drop multiplier = %v, want 1.0", n.EffectiveDropMultiplier(true))
	}
	sat := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC) // Saturday
	if got := n.EffectiveFatigueLimit(156, sat); got != 156 {
		t.Fatalf("neutral weekend fatigue = %d, want 156", got)
	}

	p := DefaultActive()
	if got := p.EffectiveExpMultiplier(0); got != 2.0 {
		t.Fatalf("default exp mult at 0 fp = %v, want 2.0", got)
	}
	if stage, pct := p.BurningStage(100); stage != 3 || pct != 75 {
		t.Fatalf("burning stage at 100 fp = (%d,%d), want (3,75)", stage, pct)
	}
	if got := p.EffectiveExpMultiplier(100); got != 3.5 {
		t.Fatalf("exp mult with 2x + burning 75%% = %v, want 3.5", got)
	}
	if got := p.EffectiveDropMultiplier(true); got != 2.4 {
		t.Fatalf("drop mult 2x + growth 1.2x = %v, want 2.4", got)
	}
	if got := p.EffectiveFatigueLimit(156, sat); got != 312 {
		t.Fatalf("weekend double fatigue = %d, want 312", got)
	}
	p.Fatigue.ZeroCost = true
	if got := p.EffectiveRoomCost(1); got != 0 {
		t.Fatalf("zero-cost room cost = %d, want 0", got)
	}
}

func TestPolicyLoadAndWeeklyCycleKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.json")
	if p, err := Load(path); err != nil || p.ExpEventEnabled {
		t.Fatalf("missing events.json should return neutral policy: %+v, %v", p, err)
	}
	raw, _ := json.Marshal(DefaultActive())
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.HellParty.Enabled || loaded.Mileage.DailyCap != 50 {
		t.Fatalf("unexpected loaded policy: %+v", loaded)
	}
	// 2026-11-27 is Friday; Tuesday anchor should be 2026-11-24.
	fri := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 11, 30, 19, 0, 0, 0, time.UTC)
	tue := time.Date(2026, 12, 1, 0, 5, 0, 0, time.UTC)
	if k := loaded.WeeklyCycleKey(fri); k != "week-2026-11-24" {
		t.Fatalf("friday cycle key = %q, want week-2026-11-24", k)
	}
	if k := loaded.WeeklyCycleKey(mon); k != "week-2026-11-24" {
		t.Fatalf("monday cycle key = %q, want week-2026-11-24", k)
	}
	if k := loaded.WeeklyCycleKey(tue); k != "week-2026-12-01" {
		t.Fatalf("tuesday cycle key = %q, want week-2026-12-01", k)
	}
}
