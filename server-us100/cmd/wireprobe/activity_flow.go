package main

import (
	"context"
	"crypto/rand"
	"dfolan/internal/catalog"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const (
	gabrielContractKind      byte = 73
	unstableRiftContractKind byte = 125

	// US100 PVF verified Event IDs from UI/Event/InformationButtonInfo.evn
	// (strictly cc="" or cc="univ" so HUD icons resolve on US100) and list/event.lst.
	eventIDExpHotTime      uint16 = 520  // btn=269, grp=3, cc="", hot time HUD icon
	eventIDDropGoldBoost   uint16 = 602  // btn=222, grp=3, cc="", drop/bonus HUD icon
	eventIDFatigueBurning  uint16 = 619  // btn=254, grp=3, cc="", time/fatigue HUD icon
	eventIDMileageBonus    uint16 = 367  // btn=167, cc="univ", gift/mileage HUD icon
	eventIDHellParty       uint16 = 500  // btn=215, grp=3, cc="", forge/hell HUD icon
	eventIDDailyChallenge  uint16 = 392  // btn=204, grp=3, cc="", daily mission HUD icon
	eventIDWeeklyDungeon   uint16 = 614  // btn=245, grp=3, cc="", weekly mission HUD icon
	eventIDDailyCheckin    uint16 = 515  // btn=264, grp=2, cc="", everyday connect HUD icon
	eventIDSeriaWelcome    uint16 = 512  // btn=249, grp=3, cc="", welcome HUD icon
	eventIDReturneeCare    uint16 = 607  // list/event.lst ReturnUserAccessEvent.evt
	eventIDOTPSecurity     uint16 = 525  // list/event.lst SecurityEvent.evt
	eventIDPCBangBonus     uint16 = 306  // btn=110, grp=3, cc="", PC room HUD icon
)

// formatEventDesignNotice builds a DFO EventDesignWindow [text_control] markup
// string (plus trailing invisible newline spacers for auto-height layout) so
// even an unpatched client renders crisp multi-line details inside window 0x244.
// When Script.pvf includes the localized Event/EventListWindow.evt overlay,
// EventCalendar Loop 1 (0x14383d288) automatically supersedes these ASCII
// fallbacks with full UTF-16LE Chinese titles and detail panels.
func formatEventDesignNotice(lines ...string) string {
	if len(lines) == 0 {
		return ""
	}
	out := fmt.Sprintf("[text_control] %d", len(lines))
	y := 12
	for idx, line := range lines {
		color := "COLOR_WHITE"
		if idx == 0 {
			color = "COLOR_YELLOW01"
		} else if idx == len(lines)-1 {
			color = "COLOR_GREEN_LIGHT"
		}
		out += fmt.Sprintf(" ```%s ``` 12 %d %s", line, y, color)
		y += 22
	}
	spacerLines := len(lines)*2 + 1
	out += " "
	for i := 0; i < spacerLines; i++ {
		if i > 0 {
			out += "\n"
		}
		out += "."
	}
	return out
}

// buildActiveEventsPayload builds the full NOTI 108 payload for DANJI plus all
// currently enabled server activities in events.json. Each entry sets
// HasDetail=true and ShowOnCalendar=true with a current time window so the
// client renders them inside EventCalendar.xui and lights up matching HUD
// notification icons via InformationButtonInfo.evn (0x14384d77a).
//
// In EventInfo (0x143852a99), Description (+0x70) is the external webpage URL
// (leaving it empty cleanly hides the dead "Visit Event Page" link), while
// Notice (+0xc0) feeds EventDetailInfo.Notice (+0x118) for EventDesignWindow.
func buildActiveEventsPayload(pol events.Policy, now time.Time) ([]byte, error) {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startTS := int32(dayStart.AddDate(0, 0, -2).Unix())
	endTS := int32(dayStart.AddDate(0, 0, 14).Unix())

	makeEntry := func(id uint16, color byte, title, subtitle string, detailLines ...string) protocol.ActiveEventEntry {
		return protocol.ActiveEventEntry{
			EventID:        id,
			HasDetail:      true,
			EventType:      1,
			ColorType:      color,
			Title:          title,
			Subtitle:       subtitle,
			Description:    "",
			Notice:         formatEventDesignNotice(detailLines...),
			StartTime:      startTS,
			EndTime:        endTS,
			ShowOnCalendar: true,
		}
	}

	entries := []protocol.ActiveEventEntry{
		makeEntry(protocol.EventIDDanji, 4, "DANJI Secret Jar", "DANJI Event",
			"DANJI Secret Jar Event",
			"Open DANJI Pots for rare rewards.",
			"Stack milestones grant bonus items.",
			"Status: Active on server"),
	}
	if pol.ExpEventEnabled && pol.ExpMultiplier > 1.0 {
		entries = append(entries, makeEntry(
			eventIDExpHotTime, 1,
			fmt.Sprintf("EXP Boost Event (x%.1f)", pol.ExpMultiplier),
			"Combat EXP Event",
			fmt.Sprintf("Combat & Clear EXP x%.1f", pol.ExpMultiplier),
			"Applies to monster kills and clears.",
			"Adjustable live from the launcher.",
			"Status: Active on server",
		))
	}
	if (pol.DropEventEnabled && (pol.DropRateMultiplier > 1.0 || pol.GoldMultiplier > 1.0)) ||
		(pol.CardEventEnabled && pol.CardGoldMultiplier > 1.0) {
		entries = append(entries, makeEntry(
			eventIDDropGoldBoost, 4,
			fmt.Sprintf("Drop & Gold Boost (x%.1f)", math.Max(pol.DropRateMultiplier, math.Max(pol.GoldMultiplier, pol.CardGoldMultiplier))),
			"Loot & Gold Event",
			"Drop & Gold Boost Event",
			fmt.Sprintf("Drop x%.1f / Gold x%.1f", pol.DropRateMultiplier, pol.GoldMultiplier),
			fmt.Sprintf("Clear Card Gold x%.1f", pol.CardGoldMultiplier),
			"Status: Active on server",
		))
	}
	if pol.Fatigue.BurningEnabled || pol.Fatigue.WeekendDouble || pol.Fatigue.ZeroCost || pol.Fatigue.BonusLimit > 0 {
		title := "Burning Fatigue Event"
		if pol.Fatigue.ZeroCost {
			title = "Zero Fatigue Cost Event"
		} else if pol.Fatigue.WeekendDouble && !pol.Fatigue.BurningEnabled {
			title = "Weekend Double Fatigue"
		}
		entries = append(entries, makeEntry(
			eventIDFatigueBurning, 1,
			title,
			"Fatigue Bonus",
			title,
			fmt.Sprintf("Burning: %v / Weekend 2x: %v", pol.Fatigue.BurningEnabled, pol.Fatigue.WeekendDouble),
			fmt.Sprintf("Bonus FP: +%d / Zero Cost: %v", pol.Fatigue.BonusLimit, pol.Fatigue.ZeroCost),
			"Status: Active on server",
		))
	}
	if pol.Mileage.Enabled {
		entries = append(entries, makeEntry(
			eventIDMileageBonus, 3,
			fmt.Sprintf("Mileage Event (+%d/Clear)", pol.Mileage.DungeonClearPoints),
			"Mileage Shop",
			"Mileage Shop Event",
			fmt.Sprintf("Earn +%d Mileage per dungeon clear.", pol.Mileage.DungeonClearPoints),
			fmt.Sprintf("Daily grind cap: %d Mileage.", pol.Mileage.DailyCap),
			"Status: Active on server",
		))
	}
	if pol.HellParty.Enabled {
		// Leave NOTI 108 Title empty so calendar.xui preserves its native UTF-16LE
		// TitleString ("灵魂之铁砧") instead of overwriting the title bar with ASCII.
		entries = append(entries, makeEntry(
			eventIDHellParty, 4,
			"",
			"Random Hell Party",
			fmt.Sprintf("Random Hell Party (%d%%)", pol.HellParty.TriggerChance),
			"Chance to spawn Hell Party in dungeons.",
			"Defeat APCs for Epic/Mythic loot.",
			"Status: Active on server",
		))
	}
	if pol.DailyChallenge.Enabled {
		entries = append(entries, makeEntry(
			eventIDDailyChallenge, 2,
			fmt.Sprintf("Daily Challenge (%d Kills / %d Clears)", pol.DailyChallenge.KillTarget, pol.DailyChallenge.ClearTarget),
			"Daily Mission",
			"Daily Challenge Mission",
			fmt.Sprintf("Goal: %d kills / %d clears.", pol.DailyChallenge.KillTarget, pol.DailyChallenge.ClearTarget),
			fmt.Sprintf("Reward: %d Gold + %d Mileage.", pol.DailyChallenge.RewardGold, pol.DailyChallenge.RewardMileage),
			"Resets daily automatically.",
		))
	}
	if pol.WeeklyDungeon.Enabled {
		entries = append(entries, makeEntry(
			eventIDWeeklyDungeon, 2,
			fmt.Sprintf("Weekly Dungeon Bonus (%d/Week)", pol.WeeklyDungeon.WeeklyLimit),
			"Weekly Mission",
			"Weekly Dungeon Bonus",
			fmt.Sprintf("Up to %d rewarded clears per week.", pol.WeeklyDungeon.WeeklyLimit),
			fmt.Sprintf("Reward: %d Gold + %d Mileage.", pol.WeeklyDungeon.RewardGold, pol.WeeklyDungeon.RewardMileage),
			"Resets weekly automatically.",
		))
	}
	if pol.Extra.DailyCheckinEnabled {
		// Leave NOTI 108 Title empty so CUIEventPopupWindow::Open (0x14365b822)
		// preserves main.xui/calendar.xui's native UTF-16LE TitleString
		// (<20::us100_act_tooltip_515> = "每日签到活动"), while EventCalendar
		// uses event/eventlistwindow.evt (<23::us100_ev_title_515>).
		entries = append(entries, makeEntry(
			eventIDDailyCheckin, 3,
			"",
			"Attendance",
			"Daily Check-in Attendance",
			"Auto-granted on daily login:",
			"100k Gold + 20 Ryan Cores + 10 Mileage.",
			"Status: Active on server",
		))
	}
	if pol.Extra.SeriaWelcomeEnabled {
		entries = append(entries, makeEntry(
			eventIDSeriaWelcome, 3,
			"Seria's Welcome Event",
			"Adventurer Gift",
			"Seria's Welcome Gift",
			"Auto-granted welcome supply:",
			"300k Gold + 30 Remy's Touch + 20 Mileage.",
			"Status: Active on server",
		))
		// Event 512 (Dungeon Mining) reads its 4 weekly mission rows from companion
		// Event 388 (0x184) inside EventManager; register 388 with HasDetail=false so
		// it does not clutter the calendar while enabling live mission counters.
		entries = append(entries, protocol.ActiveEventEntry{
			EventID:   eventIDMiningMission,
			HasDetail: false,
		})
	}
	var params []protocol.EventInitParam
	if pol.Extra.ReturneeCareEnabled {
		entries = append(entries, makeEntry(
			eventIDReturneeCare, 2,
			"Returnee Care Event",
			"Weekly Supply",
			"Returnee Care Weekly Supply",
			"Auto-granted weekly care pack:",
			"500k Gold + 50 Crystals + 30 Mileage.",
			"Status: Active on server",
		))
		params = append(params, protocol.EventInitParam{
			EventID: eventIDReturneeCare,
			Params:  [12]uint32{1},
		})
	}
	if pol.Extra.OTPSecurityEnabled {
		entries = append(entries, makeEntry(
			eventIDOTPSecurity, 2,
			"Security Protection Bonus",
			"Daily Security",
			"Security Protection Bonus",
			"Auto-granted daily reward:",
			"50k Gold + 10 Kaleido Boxes + 5 Mileage.",
			"Status: Active on server",
		))
	}
	if pol.Extra.PCBangBonusEnabled {
		entries = append(entries, makeEntry(
			eventIDPCBangBonus, 4,
			"PC Bang Bonus (+30 FP / +20% EXP)",
			"PC Room Benefit",
			"PC Bang Special Perks",
			"+30 Daily Fatigue limit bonus.",
			"+20% bonus combat EXP in dungeons.",
			"Status: Active on server",
		))
	}
	return protocol.ActiveEventsDetailedWithParams(entries, params)
}

func fundsPayload(cera, mileage uint64) []byte {
	if cera > math.MaxInt32 {
		cera = math.MaxInt32
	}
	if mileage > math.MaxInt32 {
		mileage = math.MaxInt32
	}
	p := binary.LittleEndian.AppendUint32([]byte{1}, uint32(cera))
	return binary.LittleEndian.AppendUint32(p, uint32(mileage))
}

func (w *worldSession) eventPolicy() events.Policy {
	if w == nil {
		return events.Neutral()
	}
	return events.LoadBestEffort(w.eventsPath)
}

func (w *worldSession) activityDay(now time.Time) string {
	if w != nil && w.fatigue != nil {
		return w.fatigue.Day(now)
	}
	return now.UTC().Format("2006-01-02")
}

func (w *worldSession) grantActivityReward(ctx context.Context, eventKey, model string, gold, template, amount uint32, mileage int64, dayKey string, dailyCap int64) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.loot == nil || w.role.ID == 0 {
		return nil, nil
	}
	var out []outboundPacket
	hasBagChange := gold > 0 || (template > 0 && amount > 0)
	if !hasBagChange && mileage <= 0 {
		return nil, nil
	}
	saved, applied, err := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, eventKey, model, func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		state := current.State
		if hasBagChange {
			bag, err := inventory.ReadBag(current.State)
			if err != nil {
				return nil, nil, err
			}
			if gold > 0 {
				nextGold := uint64(bag.Gold) + uint64(gold)
				if nextGold > math.MaxUint32 {
					nextGold = math.MaxUint32
				}
				bag.Gold = uint32(nextGold)
			}
			if template > 0 && amount > 0 {
				local := catalog.LootCatalog{
					Source: w.loot.Catalog.Source,
					Items: map[uint32]catalog.LootItem{
						template: localCatalogEntry(w.loot.Catalog, template, amount),
					},
				}
				bag, _, err = bag.Add(local, w.loot.BagRules, template, amount)
				if err != nil {
					return nil, nil, err
				}
			}
			state, err = inventory.SaveBag(current.State, bag)
			if err != nil {
				return nil, nil, err
			}
		}
		receipt, err := json.Marshal(map[string]any{"gold": gold, "template": template, "amount": amount, "mileage": mileage})
		return state, receipt, err
	})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, nil
	}
	if hasBagChange {
		saved.WireID = w.role.WireID
		w.role = saved
		if bag, err := inventory.ReadBag(saved.State); err == nil {
			if upd, err := protocol.US224InventoryUpdate(bag.Rows()); err == nil {
				out = append(out, outboundPacket{"activity_reward_inventory", 0, 14, upd})
			}
		}
	}
	if mileage > 0 {
		added, total, err := w.characters.Store.AddAccountMileage(ctx, w.account, mileage, dayKey, dailyCap)
		if err == nil && added > 0 {
			if cera, err := w.characters.Store.AccountCera(ctx, w.account); err == nil {
				out = append(out, outboundPacket{"activity_mileage_updated", 0, 53, fundsPayload(cera, total)})
			}
		}
	}
	return out, nil
}

// onActivityLogin settles automatic operational activity perks (Daily Check-in,
// Seria's Welcome, Returnee Care, OTP Security) idempotently on world entry or
// live hot-reload whenever enabled in events.json.
func (w *worldSession) onActivityLogin(ctx context.Context) []outboundPacket {
	if w == nil || w.characters == nil || w.loot == nil || w.role.ID == 0 {
		return nil
	}
	ev := w.eventPolicy()
	now := time.Now()
	day := w.activityDay(now)
	week := ev.WeeklyCycleKey(now)
	var out []outboundPacket

	type autoGrant struct {
		enabled  bool
		key      string
		model    string
		gold     uint32
		template uint32
		amount   uint32
		mileage  int64
	}
	grants := []autoGrant{
		{ev.Extra.DailyCheckinEnabled, fmt.Sprintf("auto_daily_checkin:%s", day), "us100-auto-daily-checkin-v1", 100000, 10327281, 20, 10},
		{ev.Extra.SeriaWelcomeEnabled, "auto_seria_welcome", "us100-auto-seria-welcome-v1", 300000, 2660671, 30, 20},
		{ev.Extra.ReturneeCareEnabled, fmt.Sprintf("auto_returnee_care:%s", week), "us100-auto-returnee-care-v1", 500000, 10327282, 50, 30},
		{ev.Extra.OTPSecurityEnabled, fmt.Sprintf("auto_otp_security:%s", day), "us100-auto-otp-security-v1", 50000, 15, 10, 5},
	}
	for _, g := range grants {
		if !g.enabled {
			continue
		}
		if pkts, err := w.grantActivityReward(ctx, g.key, g.model, g.gold, g.template, g.amount, g.mileage, day, 0); err == nil {
			out = append(out, pkts...)
		}
	}
	return out
}

func (w *worldSession) onActivityKill(ctx context.Context) []outboundPacket {
	if w == nil || w.characters == nil || w.role.ID == 0 {
		return nil
	}
	ev := w.eventPolicy()
	if !ev.DailyChallenge.Enabled {
		return nil
	}
	now := time.Now()
	day := w.activityDay(now)
	_, reached, err := w.characters.Store.RecordActivityKill(ctx, w.account, w.role.ID, day, ev.DailyChallenge.KillTarget)
	if err != nil || !reached {
		return nil
	}
	pkts, _ := w.grantActivityReward(
		ctx,
		fmt.Sprintf("daily_kill:%s", day),
		"us100-daily-challenge-kill-v1",
		ev.DailyChallenge.RewardGold,
		ev.DailyChallenge.RewardTemplate,
		ev.DailyChallenge.RewardAmount,
		ev.DailyChallenge.RewardMileage,
		day,
		0, // challenge bonus mileage is not capped by dungeon grind cap
	)
	return pkts
}

func (w *worldSession) onActivityClear(ctx context.Context) []outboundPacket {
	if w == nil || w.characters == nil || w.role.ID == 0 || w.activeDungeon == nil {
		return nil
	}
	ev := w.eventPolicy()
	now := time.Now()
	day := w.activityDay(now)
	week := ev.WeeklyCycleKey(now)
	var out []outboundPacket

	// 1. Mileage on dungeon clear (PVF etc/mileageshop.etc)
	if ev.Mileage.Enabled && ev.Mileage.DungeonClearPoints > 0 {
		added, total, err := w.characters.Store.AddAccountMileage(ctx, w.account, ev.Mileage.DungeonClearPoints, day, ev.Mileage.DailyCap)
		if err == nil && added > 0 {
			if cera, err := w.characters.Store.AccountCera(ctx, w.account); err == nil {
				out = append(out, outboundPacket{"clear_mileage_updated", 0, 53, fundsPayload(cera, total)})
			}
		}
	}

	// 2. Daily Clear Challenge & Weekly Dungeon counter (Tuesday reset, 6/week)
	clearTarget := 0
	if ev.DailyChallenge.Enabled {
		clearTarget = ev.DailyChallenge.ClearTarget
	}
	weeklyLimit := 0
	if ev.WeeklyDungeon.Enabled {
		weeklyLimit = ev.WeeklyDungeon.WeeklyLimit
	}
	if clearTarget > 0 || weeklyLimit > 0 || w.activeDungeon.HasHellRoom {
		prog, dailyReached, weeklyGranted, err := w.characters.Store.RecordActivityClear(
			ctx, w.account, w.role.ID, day, week, clearTarget, weeklyLimit, w.activeDungeon.HasHellRoom,
		)
		if err == nil {
			if dailyReached {
				if pkts, rerr := w.grantActivityReward(
					ctx,
					fmt.Sprintf("daily_clear:%s", day),
					"us100-daily-challenge-clear-v1",
					ev.DailyChallenge.RewardGold,
					ev.DailyChallenge.RewardTemplate,
					ev.DailyChallenge.RewardAmount,
					ev.DailyChallenge.RewardMileage,
					day,
					0,
				); rerr == nil {
					out = append(out, pkts...)
				}
			}
			if weeklyGranted {
				if pkts, rerr := w.grantActivityReward(
					ctx,
					fmt.Sprintf("weekly_clear:%s:%d", week, prog.WeeklyClears),
					"us100-weekly-dungeon-v1",
					ev.WeeklyDungeon.RewardGold,
					ev.WeeklyDungeon.RewardTemplate,
					ev.WeeklyDungeon.RewardAmount,
					ev.WeeklyDungeon.RewardMileage,
					day,
					0,
				); rerr == nil {
					out = append(out, pkts...)
				}
			}
		}
	}

	// 3. Gabriel's Contract (kind 73) clear supply bonus
	if ev.Contracts.GabrielBonus {
		if active, err := w.characters.Store.ActivePremiumKinds(w.account, now); err == nil && active[gabrielContractKind] {
			var roll uint32
			if binary.Read(rand.Reader, binary.LittleEndian, &roll) == nil && roll%100 < 35 {
				if pkts, rerr := w.grantActivityReward(
					ctx,
					fmt.Sprintf("gabriel:%s", w.activeDungeon.RunID),
					"us100-gabriel-contract-v1",
					50000,
					10327281, // Ryan Core
					15,
					0,
					day,
					0,
				); rerr == nil {
					out = append(out, pkts...)
				}
			}
		}
	}

	// 3b. Event 500 (Forge of Souls): grant Soul Stones (50060799) on every dungeon clear
	// so players can actively farm reinforcement materials beyond the daily gift box.
	if ev.HellParty.Enabled {
		stoneCount := uint32(25)
		if w.activeDungeon.Definition.BasisLevel >= 95 || w.activeDungeon.HasHellRoom {
			stoneCount = 40
		}
		if pkts, rerr := w.grantActivityReward(
			ctx,
			fmt.Sprintf("ev500_clear:%s", w.activeDungeon.RunID),
			"us100-ev500-clear-v1",
			0,
			ev500MaterialItem,
			stoneCount,
			0,
			day,
			0,
		); rerr == nil {
			out = append(out, pkts...)
		}
	}

	// 4. Advance interactive event progress counters (Mining 388/512, Melody 614, Cube Lab 602).
	out = append(out, w.onInteractiveEventClear(now)...)

	return out
}
