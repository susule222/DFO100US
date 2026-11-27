package events

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

// FatiguePolicy controls live server fatigue events (weekend double FP, bonus FP,
// zero-cost / infinite FP, and burning fatigue staged EXP bonus).
type FatiguePolicy struct {
	WeekendDouble  bool   `json:"weekend_double"`
	BonusLimit     uint16 `json:"bonus_limit"`
	ZeroCost       bool   `json:"zero_cost"`
	BurningEnabled bool   `json:"burning_enabled"`
}

// MileagePolicy controls account Mileage accumulation (NOTI 53) and daily caps,
// aligned with US100 PVF etc/mileageshop.etc.
type MileagePolicy struct {
	Enabled            bool  `json:"enabled"`
	DungeonClearPoints int64 `json:"dungeon_clear_points"`
	DailyCap           int64 `json:"daily_cap"`
	ShopRebatePercent  int   `json:"shop_rebate_percent"`
}

// HellPartyPolicy controls native NOTI 28 Random Hell room assignment and
// Hell Party epic/mythic drops when clearing the marked room.
type HellPartyPolicy struct {
	Enabled         bool `json:"enabled"`
	TriggerChance   int  `json:"trigger_chance"`    // 0..100
	MinDungeonLevel int  `json:"min_dungeon_level"` // 1..100
	DropCount       int  `json:"drop_count"`        // 1..5
	EpicWeight      int  `json:"epic_weight"`       // 0..100
	MythicWeight    int  `json:"mythic_weight"`     // 0..100
}

// DailyChallengePolicy controls automatic daily kill/clear challenge rewards
// (PVF etc/dailychallengetable.etc).
type DailyChallengePolicy struct {
	Enabled        bool   `json:"enabled"`
	KillTarget     int    `json:"kill_target"`
	ClearTarget    int    `json:"clear_target"`
	RewardGold     uint32 `json:"reward_gold"`
	RewardMileage  int64  `json:"reward_mileage"`
	RewardTemplate uint32 `json:"reward_template"`
	RewardAmount   uint32 `json:"reward_amount"`
}

// WeeklyDungeonPolicy controls Tuesday-reset weekly dungeon bonus rewards
// (PVF etc/weeklydungeon.etc: reset day index 2, default 6 entries).
type WeeklyDungeonPolicy struct {
	Enabled        bool   `json:"enabled"`
	WeeklyLimit    int    `json:"weekly_limit"`
	ResetWeekday   int    `json:"reset_weekday"` // 0=Sun .. 2=Tue .. 6=Sat
	RewardGold     uint32 `json:"reward_gold"`
	RewardMileage  int64  `json:"reward_mileage"`
	RewardTemplate uint32 `json:"reward_template"`
	RewardAmount   uint32 `json:"reward_amount"`
}

// ContractEffectsPolicy toggles extended gameplay effects for purchased contracts
// (Growth kind 79 drop bonus, Gabriel kind 73 supply bonus, Unstable Rift kind 125).
type ContractEffectsPolicy struct {
	GrowthDropBonus   bool `json:"growth_drop_bonus"`
	GabrielBonus      bool `json:"gabriel_bonus"`
	UnstableRiftBonus bool `json:"unstable_rift_bonus"`
}

// ExtraActivitiesPolicy holds launcher-managed operational activity toggles.
type ExtraActivitiesPolicy struct {
	DailyCheckinEnabled bool `json:"daily_checkin_enabled"`
	SeriaWelcomeEnabled bool `json:"seria_welcome_enabled"`
	ReturneeCareEnabled bool `json:"returnee_care_enabled"`
	OTPSecurityEnabled  bool `json:"otp_security_enabled"`
	PCBangBonusEnabled  bool `json:"pcbang_bonus_enabled"`
}

// Policy is the unified hot-reloaded server activity policy saved by the
// launcher to runtime/launcher-gm/events.json.
type Policy struct {
	Version            int                   `json:"version"`
	ExpEventEnabled    bool                  `json:"exp_event_enabled"`
	ExpMultiplier      float64               `json:"exp_multiplier"`
	DropEventEnabled   bool                  `json:"drop_event_enabled"`
	DropRateMultiplier float64               `json:"drop_rate_multiplier"`
	GoldMultiplier     float64               `json:"gold_multiplier"`
	CardEventEnabled   bool                  `json:"card_event_enabled"`
	CardGoldMultiplier float64               `json:"card_gold_multiplier"`
	Fatigue            FatiguePolicy         `json:"fatigue"`
	Mileage            MileagePolicy         `json:"mileage"`
	HellParty          HellPartyPolicy       `json:"hell_party"`
	DailyChallenge     DailyChallengePolicy  `json:"daily_challenge"`
	WeeklyDungeon      WeeklyDungeonPolicy   `json:"weekly_dungeon"`
	Contracts          ContractEffectsPolicy `json:"contracts"`
	Extra              ExtraActivitiesPolicy `json:"extra"`
}

// Neutral returns an inactive policy so absent config preserves exact baseline behavior.
func Neutral() Policy {
	return Policy{
		Version:            1,
		ExpMultiplier:      1.0,
		DropRateMultiplier: 1.0,
		GoldMultiplier:     1.0,
		CardGoldMultiplier: 1.0,
	}
}

// DefaultActive returns a recommended playable default activity preset for the launcher.
func DefaultActive() Policy {
	return Policy{
		Version:            1,
		ExpEventEnabled:    true,
		ExpMultiplier:      2.0,
		DropEventEnabled:   true,
		DropRateMultiplier: 2.0,
		GoldMultiplier:     2.0,
		CardEventEnabled:   true,
		CardGoldMultiplier: 2.0,
		Fatigue: FatiguePolicy{
			WeekendDouble:  true,
			BonusLimit:     0,
			ZeroCost:       false,
			BurningEnabled: true,
		},
		Mileage: MileagePolicy{
			Enabled:            true,
			DungeonClearPoints: 2,
			DailyCap:           50,
			ShopRebatePercent:  5,
		},
		HellParty: HellPartyPolicy{
			Enabled:         true,
			TriggerChance:   35,
			MinDungeonLevel: 1,
			DropCount:       2,
			EpicWeight:      85,
			MythicWeight:    15,
		},
		DailyChallenge: DailyChallengePolicy{
			Enabled:        true,
			KillTarget:     15,
			ClearTarget:    3,
			RewardGold:     200000,
			RewardMileage:  10,
			RewardTemplate: 10327281, // Ryan Core (炉岩核)
			RewardAmount:   30,
		},
		WeeklyDungeon: WeeklyDungeonPolicy{
			Enabled:        true,
			WeeklyLimit:    6,
			ResetWeekday:   2, // Tuesday (PVF etc/weeklydungeon.etc)
			RewardGold:     500000,
			RewardMileage:  15,
			RewardTemplate: 10327282, // Harmonious Crystal (协调结晶体)
			RewardAmount:   20,
		},
		Contracts: ContractEffectsPolicy{
			GrowthDropBonus:   true,
			GabrielBonus:      true,
			UnstableRiftBonus: true,
		},
		Extra: ExtraActivitiesPolicy{
			DailyCheckinEnabled: true,
			SeriaWelcomeEnabled: true,
			ReturneeCareEnabled: true,
			OTPSecurityEnabled:  true,
			PCBangBonusEnabled:  false,
		},
	}
}

func clampFloat(v, minV, maxV, fallback float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fallback
	}
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func clampInt(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

// Sanitize clamps every policy field into safe wire-compatible bounds.
func (p Policy) Sanitize() Policy {
	if p.Version <= 0 {
		p.Version = 1
	}
	p.ExpMultiplier = clampFloat(p.ExpMultiplier, 1.0, 10.0, 1.0)
	p.DropRateMultiplier = clampFloat(p.DropRateMultiplier, 1.0, 10.0, 1.0)
	p.GoldMultiplier = clampFloat(p.GoldMultiplier, 1.0, 10.0, 1.0)
	p.CardGoldMultiplier = clampFloat(p.CardGoldMultiplier, 1.0, 5.0, 1.0)
	if p.Fatigue.BonusLimit > 500 {
		p.Fatigue.BonusLimit = 500
	}
	if p.Mileage.DungeonClearPoints < 0 {
		p.Mileage.DungeonClearPoints = 0
	}
	if p.Mileage.DungeonClearPoints > 100 {
		p.Mileage.DungeonClearPoints = 100
	}
	if p.Mileage.DailyCap < 0 {
		p.Mileage.DailyCap = 0
	}
	p.Mileage.ShopRebatePercent = clampInt(p.Mileage.ShopRebatePercent, 0, 50)
	p.HellParty.TriggerChance = clampInt(p.HellParty.TriggerChance, 0, 100)
	p.HellParty.MinDungeonLevel = clampInt(p.HellParty.MinDungeonLevel, 1, 100)
	p.HellParty.DropCount = clampInt(p.HellParty.DropCount, 1, 5)
	p.HellParty.EpicWeight = clampInt(p.HellParty.EpicWeight, 0, 100)
	p.HellParty.MythicWeight = clampInt(p.HellParty.MythicWeight, 0, 100)
	if p.DailyChallenge.KillTarget <= 0 {
		p.DailyChallenge.KillTarget = 15
	}
	if p.DailyChallenge.ClearTarget <= 0 {
		p.DailyChallenge.ClearTarget = 3
	}
	if p.WeeklyDungeon.WeeklyLimit <= 0 {
		p.WeeklyDungeon.WeeklyLimit = 6
	}
	p.WeeklyDungeon.ResetWeekday = clampInt(p.WeeklyDungeon.ResetWeekday, 0, 6)
	return p
}

// Load reads events.json from path. Missing files return Neutral() with nil error.
func Load(path string) (Policy, error) {
	if path == "" {
		return Neutral(), nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Neutral(), nil
	}
	if err != nil {
		return Neutral(), err
	}
	p := Neutral()
	if err = json.Unmarshal(b, &p); err != nil {
		return Neutral(), err
	}
	return p.Sanitize(), nil
}

// LoadBestEffort reads events.json, returning Neutral() on any error or absence.
func LoadBestEffort(path string) Policy {
	p, err := Load(path)
	if err != nil {
		return Neutral()
	}
	return p
}

// BurningStage returns the burning fatigue tier (0..4) and EXP bonus percent
// based on today's consumed fatigue points.
func (p Policy) BurningStage(usedFatigue uint16) (stage int, bonusPct int) {
	if !p.Fatigue.BurningEnabled {
		return 0, 0
	}
	switch {
	case usedFatigue >= 156:
		return 4, 100
	case usedFatigue >= 100:
		return 3, 75
	case usedFatigue >= 60:
		return 2, 50
	case usedFatigue >= 30:
		return 1, 20
	default:
		return 0, 0
	}
}

// EffectiveExpMultiplier computes the combined EXP multiplier from the server
// EXP event, Burning Fatigue stage, and PC Bang event toggle.
func (p Policy) EffectiveExpMultiplier(usedFatigue uint16) float64 {
	mult := 1.0
	if p.ExpEventEnabled && p.ExpMultiplier > 1.0 {
		mult *= p.ExpMultiplier
	}
	if _, pct := p.BurningStage(usedFatigue); pct > 0 {
		mult *= 1.0 + float64(pct)/100.0
	}
	if p.Extra.PCBangBonusEnabled {
		mult *= 1.2
	}
	if mult > 20.0 {
		mult = 20.0
	}
	return mult
}

// EffectiveDropMultiplier computes the combined base drop probability multiplier
// from the server drop event and Growth Contract (kind 79, +20% PVF rate).
func (p Policy) EffectiveDropMultiplier(growthContractActive bool) float64 {
	mult := 1.0
	if p.DropEventEnabled && p.DropRateMultiplier > 1.0 {
		mult *= p.DropRateMultiplier
	}
	if growthContractActive && p.Contracts.GrowthDropBonus {
		mult *= 1.2
	}
	if mult > 15.0 {
		mult = 15.0
	}
	return mult
}

// EffectiveGoldMultiplier returns the monster gold drop multiplier.
func (p Policy) EffectiveGoldMultiplier() float64 {
	if p.DropEventEnabled && p.GoldMultiplier > 1.0 {
		return p.GoldMultiplier
	}
	return 1.0
}

// EffectiveCardGoldMultiplier returns the post-dungeon card gold multiplier.
func (p Policy) EffectiveCardGoldMultiplier() float64 {
	if p.CardEventEnabled && p.CardGoldMultiplier > 1.0 {
		return p.CardGoldMultiplier
	}
	return 1.0
}

// EffectiveFatigueLimit computes the daily base fatigue cap after weekend double,
// configured bonus limit, and PC Bang bonus.
func (p Policy) EffectiveFatigueLimit(base uint16, now time.Time) uint16 {
	total := uint32(base)
	if p.Fatigue.WeekendDouble {
		wd := now.Weekday()
		if wd == time.Saturday || wd == time.Sunday {
			total += uint32(base)
		}
	}
	total += uint32(p.Fatigue.BonusLimit)
	if p.Extra.PCBangBonusEnabled {
		total += 30
	}
	if total > 999 {
		total = 999
	}
	return uint16(total)
}

// EffectiveRoomCost returns 0 when the zero-fatigue event is active.
func (p Policy) EffectiveRoomCost(base uint16) uint16 {
	if p.Fatigue.ZeroCost {
		return 0
	}
	return base
}

// WeeklyCycleKey returns a stable reset-day-anchored week identifier
// (e.g. "2026-11-24" for Tuesday reset, matching etc/weeklydungeon.etc).
func (p Policy) WeeklyCycleKey(now time.Time) string {
	resetDay := time.Weekday(clampInt(p.WeeklyDungeon.ResetWeekday, 0, 6))
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	diff := (int(d.Weekday()) - int(resetDay) + 7) % 7
	anchor := d.AddDate(0, 0, -diff)
	return fmt.Sprintf("week-%s", anchor.Format("2006-01-02"))
}
