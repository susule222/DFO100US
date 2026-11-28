package main

import (
	"context"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"fmt"
	"time"
)

const (
	eventIDMiningMission uint16 = 388 // Companion mission manager required by Event 512 (Dungeon Mining)
	eventUIStateTable           = "event_ui_state"
)

// persistedEventUIState stores account-scoped interactive state for all US100
// event windows so progress and reward claims survive relog and server restarts.
type persistedEventUIState struct {
	// Event 520: Reverse Hot Time (5M / 10M rewards)
	Ev520AccumSec uint32 `json:"ev520_accum_sec"`
	Ev520Claimed1 bool   `json:"ev520_claimed1"`
	Ev520Claimed2 bool   `json:"ev520_claimed2"`

	// Event 512 + 388: Dungeon Mining
	Ev512Initialized  bool      `json:"ev512_initialized"`
	Ev512Pickaxes     uint32    `json:"ev512_pickaxes"`
	Ev512Stages       [4]uint32 `json:"ev512_stages"`
	Ev388LoginDays    uint16    `json:"ev388_login_days"`
	Ev388OnlineSec    uint16    `json:"ev388_online_sec"`
	Ev388DungeonClear uint16    `json:"ev388_dungeon_clear"`
	Ev388RaidClear    uint16    `json:"ev388_raid_clear"`

	// Event 515: Everyday Connect Attendance
	Ev515LastCheckDay string `json:"ev515_last_check_day"`
	Ev515Days         byte   `json:"ev515_days"`
	Ev515DailyMask    uint32 `json:"ev515_daily_mask"`
	Ev515AccumMask    uint32 `json:"ev515_accum_mask"`

	// Event 619: Time Guide Stone Daily Check
	Ev619LastDay     string `json:"ev619_last_day"`
	Ev619OnlineSec   uint32 `json:"ev619_online_sec"`
	Ev619PickedMask  uint32 `json:"ev619_picked_mask"`
	Ev619ClaimedDay  bool   `json:"ev619_claimed_day"`
	Ev619TotalDays   byte   `json:"ev619_total_days"`
	Ev619BonusMask   byte   `json:"ev619_bonus_mask"`

	// Event 614: Melody for Adventurers
	Ev614WeekKey      string  `json:"ev614_week_key"`
	Ev614Progress     [4]byte `json:"ev614_progress"`
	Ev614Claimed      [4]byte `json:"ev614_claimed"`
	Ev614TotalCleared byte    `json:"ev614_total_cleared"`
	Ev614LineMask     byte    `json:"ev614_line_mask"`

	// Event 602: Ria's Special Cube Lab II
	Ev602WeekKey         string `json:"ev602_week_key"`
	Ev602LastDay         string `json:"ev602_last_day,omitempty"`
	Ev602Energy          uint32 `json:"ev602_energy"`
	Ev602SearchesUsed    byte   `json:"ev602_searches_used"`
	Ev602PendingIdx      int32  `json:"ev602_pending_idx"`
	Ev602SavedIdx        int32  `json:"ev602_saved_idx"`
	Ev602SavedItem       uint32 `json:"ev602_saved_item"`
	Ev602PickedIndices   []byte `json:"ev602_picked_indices,omitempty"`
	Ev602Transferred     bool   `json:"ev602_transferred"`
	Ev602TransferredMask uint32 `json:"ev602_transferred_mask,omitempty"`
	Ev602Contributions   uint32 `json:"ev602_contributions"`
	Ev602MilestoneMask   uint32 `json:"ev602_milestone_mask"`

	// Event 367: Time Attendance
	Ev367LastClaimDay string `json:"ev367_last_claim_day"`
}

type eventRewardItem struct {
	Template uint32
	Count    uint32
}

// Exact PVF reward tables extracted from US100 Script.pvf .evt files.
var (
	// Event 520 (ReverseHotTime / foolsdayhottimewindow.xui): 0-indexed slots
	// verified at 0x1404d4e71..0x1404d4f12 -> 0x1404216b0 (0 = 5M, 1 = 10M).
	ev520Rewards = map[uint32]eventRewardItem{
		0: {Template: 10333092, Count: 1},
		1: {Template: 10333113, Count: 1},
	}

	// Event 512 (WelcomeMiningEvent.evt [mining reward info]: stage*10 + node)
	ev512Rewards = map[uint32]eventRewardItem{
		0:  {50007754, 1},
		1:  {50081863, 10},
		2:  {50007756, 1},
		3:  {50007759, 1},
		10: {50007759, 1},
		11: {50007757, 1},
		12: {50007755, 1},
		13: {50007758, 1},
		20: {50007754, 1},
		21: {50081863, 20},
		22: {50007759, 1},
		23: {50007760, 1},
		30: {50007757, 1},
		31: {50007759, 1},
		32: {50007755, 1},
		33: {50007761, 1},
		34: {50007770, 1},
	}

	// Event 515 (EveryDayConnectEvent.evt [daily attendance reward])
	ev515DailyRewards = map[uint32]eventRewardItem{
		1: {50007815, 1}, 2: {50007816, 1}, 3: {50007828, 1}, 4: {50007829, 2},
		5: {50007818, 1}, 6: {50007815, 1}, 7: {50007821, 1}, 8: {50007822, 2},
		9: {50007818, 1}, 10: {50007815, 1}, 11: {50007829, 2}, 12: {50007828, 1},
		13: {50007816, 1}, 14: {50007824, 1}, 15: {50007817, 1}, 16: {50007815, 1},
		17: {50007820, 1}, 18: {50007829, 2}, 19: {50007815, 1}, 20: {50007818, 1},
		21: {50007821, 1}, 22: {50007815, 1}, 23: {50007822, 2}, 24: {50007816, 1},
		25: {50007829, 2}, 26: {50007826, 2}, 27: {50007815, 1}, 28: {50007823, 1},
	}
	ev515AccumRewards = map[uint32]eventRewardItem{
		5:  {50007832, 1},
		15: {50007833, 1},
		25: {50007834, 1},
	}

	// Event 619 (TimeAttendanceRandomPickEvent.evt)
	ev619DailyPool = []eventRewardItem{
		{10326700, 1}, {10326704, 1}, {10326702, 1}, {10326706, 1},
		{10326701, 1}, {10326703, 1}, {10326705, 1},
	}
	ev619BonusRewards = []eventRewardItem{
		{10326707, 1}, // 5 cumulative claims
		{10326709, 1}, // 10 cumulative claims
	}

	// Event 614 (WeeklyMissionEvent.evt)
	ev614MissionRewards = []eventRewardItem{
		{50081692, 1}, {50081693, 1}, {50081694, 1}, {50081695, 1},
	}
	ev614LineRewards = []eventRewardItem{
		{50081688, 1}, {50081689, 1}, {50081690, 1}, {50081691, 1},
	}

	// Event 602 (PickAndKeepEvent.evt [pick item info] core cubes)
	ev602CubePool = []uint32{
		50081632, 50081636, 50081640, 50081644, 50081652, 50081648,
		50081633, 50081637, 50081641, 50081645,
	}

	// Event 367 (TimeAttendanceEvent.evt daily pair)
	ev367DailyRewards = []eventRewardItem{
		{50061692, 3},
		{50061703, 1},
	}
)

func eventUIStateKey(account int64) string {
	return fmt.Sprintf("account:%d", account)
}

func (w *worldSession) loadEventUIState(now time.Time) persistedEventUIState {
	var st persistedEventUIState
	if w != nil && w.characters != nil && w.characters.Store != nil && w.account > 0 {
		_, _ = w.characters.Store.Database().Get(eventUIStateTable, eventUIStateKey(w.account), &st)
	}
	day := w.activityDay(now)
	week := w.eventPolicy().WeeklyCycleKey(now)

	// Initialize sensible playable defaults so every open activity window is
	// immediately functional and never displays stuck 00:00 or 0/0 counters.
	if st.Ev520AccumSec < 600 {
		st.Ev520AccumSec = 600
	}
	if !st.Ev512Initialized {
		st.Ev512Initialized = true
		st.Ev512Pickaxes = 10
		st.Ev388LoginDays = 3
		st.Ev388OnlineSec = 1800
	}
	if st.Ev515Days == 0 && st.Ev515LastCheckDay == "" {
		st.Ev515Days = 0
	}
	if st.Ev619OnlineSec < 3600 {
		st.Ev619OnlineSec = 3600
	}
	if st.Ev619LastDay != day {
		st.Ev619ClaimedDay = false
	}
	if st.Ev614WeekKey != week {
		st.Ev614WeekKey = week
		st.Ev614Progress = [4]byte{3, 1, 200, 20}
		st.Ev614Claimed = [4]byte{}
	}
	if st.Ev602WeekKey != week {
		st.Ev602WeekKey = week
		st.Ev602LastDay = day
		st.Ev602Energy = 100
		st.Ev602SearchesUsed = 0
		st.Ev602PendingIdx = -1
		st.Ev602SavedIdx = -1
		st.Ev602SavedItem = 0
		st.Ev602PickedIndices = nil
		st.Ev602Transferred = false
	} else {
		if st.Ev602LastDay != day {
			st.Ev602LastDay = day
			st.Ev602SearchesUsed = 0
		}
		if st.Ev602SavedItem == 0 {
			st.Ev602SavedIdx = -1
		} else if st.Ev602SavedIdx < 0 || int(st.Ev602SavedIdx) >= len(ev602CubePool) || ev602CubePool[st.Ev602SavedIdx] != st.Ev602SavedItem {
			// Migrate legacy persisted Ev602SavedItem template to pool index.
			st.Ev602SavedIdx = -1
			for i, tmpl := range ev602CubePool {
				if tmpl == st.Ev602SavedItem {
					st.Ev602SavedIdx = int32(i)
					break
				}
			}
		}
	}
	return st
}

func (w *worldSession) saveEventUIState(st persistedEventUIState) error {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.account == 0 {
		return nil
	}
	return w.characters.Store.Database().Put(eventUIStateTable, eventUIStateKey(w.account), st)
}

func ev388MissionRows(st persistedEventUIState) []protocol.Ev388MissionRow {
	return []protocol.Ev388MissionRow{
		{Index: 0, Completed: st.Ev388LoginDays >= 3, Progress: st.Ev388LoginDays},
		{Index: 1, Completed: st.Ev388DungeonClear >= 20, Progress: st.Ev388DungeonClear},
		{Index: 2, Completed: st.Ev388OnlineSec >= 1800, Progress: st.Ev388OnlineSec},
		{Index: 3, Completed: st.Ev388RaidClear >= 1, Progress: st.Ev388RaidClear},
	}
}

func ev602Packet(st persistedEventUIState) []byte {
	savedIdx := st.Ev602SavedIdx
	if st.Ev602SavedItem == 0 {
		savedIdx = -1
	}
	mask := st.Ev602TransferredMask
	if st.Ev602Transferred {
		mask |= 1
	}
	return protocol.Ev602RiaCubeLabStatus(protocol.Ev602CubeLabState{
		DayIndex:        0,
		HasPendingItem:  st.Ev602PendingIdx >= 0 || savedIdx >= 0,
		SavedItemIdx:    savedIdx,
		PickedIndices:   st.Ev602PickedIndices,
		TransferredMask: mask,
		DailySearches:   st.Ev602SearchesUsed,
		MilestoneMask:   st.Ev602MilestoneMask,
		TimerSeconds:    900,
		EnergyPoints:    st.Ev602Energy,
		CurrentItemIdx:  st.Ev602PendingIdx,
	})
}

// interactiveEventInitPackets returns all per-event state-sync NOTI packets
// needed after NOTI 108 so every enabled event UI renders real timers, mission
// counts, and claimable buttons on login or live hot-reload.
func (w *worldSession) interactiveEventInitPackets(pol events.Policy, now time.Time) []outboundPacket {
	if w == nil || w.account == 0 {
		return nil
	}
	st := w.loadEventUIState(now)
	_ = w.saveEventUIState(st)
	day := w.activityDay(now)
	var out []outboundPacket

	if pol.ExpEventEnabled && pol.ExpMultiplier > 1.0 {
		out = append(out, outboundPacket{
			"event_520_status", 0, protocol.NotiEv520ReverseHotTime,
			protocol.Ev520ReverseHotTimeStatus(st.Ev520AccumSec, st.Ev520Claimed1, st.Ev520Claimed2, 100),
		})
	}
	if pol.Extra.SeriaWelcomeEnabled {
		missions := protocol.Ev388Missions(ev388MissionRows(st))
		out = append(out,
			outboundPacket{"event_388_missions_weekly", 0, protocol.NotiEv388MissionWeekly, missions},
			outboundPacket{"event_388_missions_daily", 0, protocol.NotiEv388MissionDaily, missions},
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
			outboundPacket{"event_388_mining_ping", 0, protocol.NotiEv388MiningPing, protocol.Ev388MiningPing(uint16(st.Ev512Pickaxes))},
		)
	}
	if pol.Extra.DailyCheckinEnabled {
		canCheck := st.Ev515LastCheckDay != day && st.Ev515Days < 28
		out = append(out, outboundPacket{
			"event_515_attendance_status", 0, protocol.NotiEv515EverydayConnect,
			protocol.Ev515EverydayConnectStatus(true, canCheck, st.Ev515Days, st.Ev515DailyMask, st.Ev515AccumMask),
		})
	}
	if pol.Fatigue.BurningEnabled || pol.Fatigue.WeekendDouble || pol.Fatigue.ZeroCost || pol.Fatigue.BonusLimit > 0 {
		claimedToday := uint32(0)
		if st.Ev619ClaimedDay {
			claimedToday = 1
		}
		out = append(out, outboundPacket{
			"event_619_dailycheck_status", 0, protocol.NotiEv619DailyCheck,
			protocol.Ev619DailyCheckStatus(st.Ev619OnlineSec, st.Ev619PickedMask, claimedToday, st.Ev619TotalDays, st.Ev619BonusMask),
		})
	}
	if pol.WeeklyDungeon.Enabled {
		out = append(out, outboundPacket{
			"event_614_melody_status", 0, protocol.NotiEv614Melody,
			protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask),
		})
	}
	if (pol.DropEventEnabled && (pol.DropRateMultiplier > 1.0 || pol.GoldMultiplier > 1.0)) ||
		(pol.CardEventEnabled && pol.CardGoldMultiplier > 1.0) {
		out = append(out, outboundPacket{
			"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab,
			ev602Packet(st),
		})
	}
	return out
}

// onInteractiveEventClear increments event mission counters when a dungeon is cleared.
func (w *worldSession) onInteractiveEventClear(now time.Time) []outboundPacket {
	if w == nil || w.account == 0 {
		return nil
	}
	pol := w.eventPolicy()
	st := w.loadEventUIState(now)
	var out []outboundPacket
	changed := false

	if pol.Extra.SeriaWelcomeEnabled {
		if st.Ev388DungeonClear < 20 {
			st.Ev388DungeonClear++
			if st.Ev388DungeonClear == 20 {
				st.Ev512Pickaxes += 2
			}
			changed = true
		}
		if st.Ev388RaidClear < 1 {
			st.Ev388RaidClear = 1
			st.Ev512Pickaxes++
			changed = true
		}
		st.Ev512Pickaxes++
		changed = true
		missions := protocol.Ev388Missions(ev388MissionRows(st))
		out = append(out,
			outboundPacket{"event_388_missions_daily", 0, protocol.NotiEv388MissionDaily, missions},
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
		)
	}
	if pol.WeeklyDungeon.Enabled && st.Ev614Progress[3] < 20 {
		st.Ev614Progress[3]++
		changed = true
		out = append(out, outboundPacket{
			"event_614_melody_status", 0, protocol.NotiEv614Melody,
			protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask),
		})
	}
	if (pol.DropEventEnabled || pol.CardEventEnabled) && st.Ev602Energy < 100 {
		st.Ev602Energy += 34
		if st.Ev602Energy > 100 {
			st.Ev602Energy = 100
		}
		changed = true
		out = append(out, outboundPacket{
			"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab,
			ev602Packet(st),
		})
	}
	if changed {
		_ = w.saveEventUIState(st)
	}
	return out
}

// handleEventCommand processes client CMD 680 (0x2a8 action/claim) and CMD 681
// (0x2a9 query/reset) across all active US100 event windows.
func (w *worldSession) handleEventCommand(ctx context.Context, cmdID uint16, plaintext []byte, now time.Time) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.loot == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("world session not ready for event command %d", cmdID)
	}
	req, err := protocol.DecodeEventCommand(plaintext)
	if err != nil {
		return nil, err
	}
	st := w.loadEventUIState(now)
	day := w.activityDay(now)
	var out []outboundPacket

	grantItem := func(eventKey, model string, item eventRewardItem) error {
		if item.Template == 0 || item.Count == 0 {
			return nil
		}
		pkts, gerr := w.grantActivityReward(ctx, eventKey, model, 0, item.Template, item.Count, 0, day, 0)
		if gerr != nil {
			return gerr
		}
		out = append(out, pkts...)
		return nil
	}

	arg := func(i int) uint32 {
		if i >= 0 && i < len(req.Args) {
			return req.Args[i]
		}
		return 0
	}

	switch req.EventID {
	case uint32(eventIDExpHotTime): // 520: Reverse Hot Time (0-indexed: 0=5M, 1=10M)
		slot := arg(0)
		reward, ok := ev520Rewards[slot]
		if !ok {
			return []outboundPacket{{"event_520_refused", 1, cmdID, protocol.EventActionRefused(2, req.EventID)}}, nil
		}
		already := (slot == 0 && st.Ev520Claimed1) || (slot == 1 && st.Ev520Claimed2)
		if !already {
			key := "ev520_claim:5m"
			if slot == 1 {
				key = "ev520_claim:10m"
			}
			if err := grantItem(key, "us100-ev520-reward-v1", reward); err != nil {
				return nil, err
			}
			if slot == 0 {
				st.Ev520Claimed1 = true
			} else {
				st.Ev520Claimed2 = true
			}
			_ = w.saveEventUIState(st)
		}
		var ackParams [10]uint32
		ackParams[0] = slot
		ackParams[1] = reward.Template
		out = append(out,
			outboundPacket{"event_520_ack", 1, protocol.CmdEventAction, protocol.EventActionSuccess(req.EventID, ackParams)},
			outboundPacket{"event_520_status", 0, protocol.NotiEv520ReverseHotTime, protocol.Ev520ReverseHotTimeStatus(st.Ev520AccumSec, st.Ev520Claimed1, st.Ev520Claimed2, 100)},
		)
		return out, nil

	case uint32(eventIDSeriaWelcome): // 512: Dungeon Mining
		stage := arg(0)
		node := arg(1)
		if stage > 3 || node > 4 {
			return []outboundPacket{{"event_512_refused", 1, cmdID, protocol.EventActionRefused(2, req.EventID)}}, nil
		}
		// Ev512Stages[stage] is a per-node bitmask (0x145214710: (mask & (1<<node)) != 0).
		bit := uint32(1) << node
		if st.Ev512Pickaxes > 0 && (st.Ev512Stages[stage]&bit) == 0 {
			st.Ev512Pickaxes--
			st.Ev512Stages[stage] |= bit
			if reward, ok := ev512Rewards[stage*10+node]; ok {
				key := fmt.Sprintf("ev512_mine:%d:%d", stage, node)
				if err := grantItem(key, "us100-ev512-mining-v1", reward); err != nil {
					return nil, err
				}
			}
			_ = w.saveEventUIState(st)
		}
		out = append(out,
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
			outboundPacket{"event_388_mining_ping", 0, protocol.NotiEv388MiningPing, protocol.Ev388MiningPing(uint16(st.Ev512Pickaxes))},
		)
		return out, nil

	case uint32(eventIDMiningMission): // 388: Mining Weekly Mission online tick
		if st.Ev388OnlineSec < 1800 {
			st.Ev388OnlineSec += 30
			if st.Ev388OnlineSec >= 1800 {
				st.Ev388OnlineSec = 1800
				st.Ev512Pickaxes++
			}
			_ = w.saveEventUIState(st)
		}
		missions := protocol.Ev388Missions(ev388MissionRows(st))
		out = append(out,
			outboundPacket{"event_388_missions_daily", 0, protocol.NotiEv388MissionDaily, missions},
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
		)
		return out, nil

	case uint32(eventIDDailyCheckin): // 515: Everyday Connect Attendance
		action := arg(0)
		targetDay := arg(1)
		switch action {
		case 3: // Daily check-in button
			if st.Ev515LastCheckDay != day && st.Ev515Days < 28 {
				st.Ev515Days++
				st.Ev515LastCheckDay = day
				_ = w.saveEventUIState(st)
			}
		case 1: // Claim daily reward for targetDay (1..28)
			if targetDay >= 1 && targetDay <= 28 && targetDay <= uint32(st.Ev515Days) && (st.Ev515DailyMask&(1<<targetDay)) == 0 {
				if reward, ok := ev515DailyRewards[targetDay]; ok {
					key := fmt.Sprintf("ev515_daily:%d", targetDay)
					if err := grantItem(key, "us100-ev515-daily-v1", reward); err != nil {
						return nil, err
					}
					st.Ev515DailyMask |= 1 << targetDay
					_ = w.saveEventUIState(st)
				}
			}
		case 2: // Claim accumulated milestone reward (5, 15, 25)
			if targetDay <= uint32(st.Ev515Days) && (st.Ev515AccumMask&(1<<targetDay)) == 0 {
				if reward, ok := ev515AccumRewards[targetDay]; ok {
					key := fmt.Sprintf("ev515_accum:%d", targetDay)
					if err := grantItem(key, "us100-ev515-accum-v1", reward); err != nil {
						return nil, err
					}
					st.Ev515AccumMask |= 1 << targetDay
					_ = w.saveEventUIState(st)
				}
			}
		}
		canCheck := st.Ev515LastCheckDay != day && st.Ev515Days < 28
		out = append(out, outboundPacket{
			"event_515_attendance_status", 0, protocol.NotiEv515EverydayConnect,
			protocol.Ev515EverydayConnectStatus(true, canCheck, st.Ev515Days, st.Ev515DailyMask, st.Ev515AccumMask),
		})
		return out, nil

	case uint32(eventIDFatigueBurning): // 619: Time Guide Stone Daily Check
		sub := arg(0)
		var ackParams [10]uint32
		if cmdID == protocol.CmdEventQuery {
			switch sub {
			case 2:
				// CMD 681 sub=2 (0x14048c510): Reset daily pick pool
				st.Ev619PickedMask = 0
				st.Ev619ClaimedDay = false
				_ = w.saveEventUIState(st)
			case 1:
				// CMD 681 sub=1 (0x14048c560): Query current Event 619 status (no mutation)
			}
		} else if sub == 1 {
			// CMD 680 sub=1 (0x14048c4a0): Daily random gift claim
			if !st.Ev619ClaimedDay {
				pickIdx := -1
				for i := 0; i < len(ev619DailyPool); i++ {
					if (st.Ev619PickedMask & (1 << uint(i))) == 0 {
						pickIdx = i
						break
					}
				}
				if pickIdx == -1 {
					st.Ev619PickedMask = 0
					pickIdx = 0
				}
				reward := ev619DailyPool[pickIdx]
				key := fmt.Sprintf("ev619_daily:%s:%d", day, st.Ev619TotalDays+1)
				if err := grantItem(key, "us100-ev619-daily-v1", reward); err != nil {
					return nil, err
				}
				st.Ev619PickedMask |= 1 << uint(pickIdx)
				st.Ev619ClaimedDay = true
				st.Ev619LastDay = day
				st.Ev619TotalDays++
				_ = w.saveEventUIState(st)
				ackParams[0] = 1
				ackParams[5] = uint32(pickIdx)
				out = append(out, outboundPacket{"event_619_ack", 1, protocol.CmdEventAction, protocol.EventActionSuccess(req.EventID, ackParams)})
			}
		} else if sub == 2 {
			// CMD 680 sub=2 (0x14048c430): Bonus milestone claim (0 = 5x, 1 = 10x)
			bIdx := int(arg(1))
			if bIdx >= 0 && bIdx < len(ev619BonusRewards) && (st.Ev619BonusMask&(1<<uint(bIdx))) == 0 {
				reward := ev619BonusRewards[bIdx]
				key := fmt.Sprintf("ev619_bonus:%d", bIdx)
				if err := grantItem(key, "us100-ev619-bonus-v1", reward); err != nil {
					return nil, err
				}
				st.Ev619BonusMask |= 1 << uint(bIdx)
				_ = w.saveEventUIState(st)
				ackParams[2] = 1
				out = append(out, outboundPacket{"event_619_ack", 1, protocol.CmdEventAction, protocol.EventActionSuccess(req.EventID, ackParams)})
			}
		}
		claimedToday := uint32(0)
		if st.Ev619ClaimedDay {
			claimedToday = 1
		}
		out = append(out, outboundPacket{
			"event_619_dailycheck_status", 0, protocol.NotiEv619DailyCheck,
			protocol.Ev619DailyCheckStatus(st.Ev619OnlineSec, st.Ev619PickedMask, claimedToday, st.Ev619TotalDays, st.Ev619BonusMask),
		})
		return out, nil

	case uint32(eventIDWeeklyDungeon): // 614: Melody for Adventurers
		sub := arg(0)
		idx := int(arg(1))
		if sub == 1 && idx >= 0 && idx < len(ev614MissionRewards) && st.Ev614Claimed[idx] == 0 {
			reward := ev614MissionRewards[idx]
			key := fmt.Sprintf("ev614_mission:%s:%d", st.Ev614WeekKey, idx)
			if err := grantItem(key, "us100-ev614-mission-v1", reward); err != nil {
				return nil, err
			}
			st.Ev614Claimed[idx] = 1
			if st.Ev614TotalCleared < 16 {
				st.Ev614TotalCleared += 4
				if st.Ev614TotalCleared > 16 {
					st.Ev614TotalCleared = 16
				}
			}
			_ = w.saveEventUIState(st)
		} else if sub == 2 && idx >= 0 && idx < len(ev614LineRewards) && (st.Ev614LineMask&(1<<uint(idx))) == 0 {
			reward := ev614LineRewards[idx]
			key := fmt.Sprintf("ev614_line:%d", idx)
			if err := grantItem(key, "us100-ev614-line-v1", reward); err != nil {
				return nil, err
			}
			st.Ev614LineMask |= 1 << uint(idx)
			_ = w.saveEventUIState(st)
		}
		out = append(out, outboundPacket{
			"event_614_melody_status", 0, protocol.NotiEv614Melody,
			protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask),
		})
		return out, nil

	case uint32(eventIDDropGoldBoost): // 602: Ria's Special Cube Lab II
		sub := arg(0)
		addPicked := func(idx byte) {
			for _, v := range st.Ev602PickedIndices {
				if v == idx {
					return
				}
			}
			if len(st.Ev602PickedIndices) < 20 {
				st.Ev602PickedIndices = append(st.Ev602PickedIndices, idx)
			}
		}
		if cmdID == protocol.CmdEventQuery {
			switch sub {
			case 1: // Search cube (CMD 681 sub=1, 0x140bdf370)
				if st.Ev602SearchesUsed < 3 {
					used := make(map[byte]bool, len(st.Ev602PickedIndices))
					for _, v := range st.Ev602PickedIndices {
						used[v] = true
					}
					pick := int32(-1)
					for i := 0; i < len(ev602CubePool); i++ {
						if !used[byte(i)] {
							pick = int32(i)
							break
						}
					}
					if pick < 0 {
						pick = int32(int(st.Ev602SearchesUsed+byte(st.Ev602Contributions)) % len(ev602CubePool))
					}
					st.Ev602PendingIdx = pick
					addPicked(byte(pick))
					st.Ev602SearchesUsed++
					_ = w.saveEventUIState(st)
				}
			case 2: // Save pending item (arg1 == 1, 0x140bdf310) or exclude (arg1 == 0, 0x140bdf260)
				if st.Ev602PendingIdx >= 0 && int(st.Ev602PendingIdx) < len(ev602CubePool) {
					addPicked(byte(st.Ev602PendingIdx))
					if arg(1) == 1 {
						st.Ev602SavedIdx = st.Ev602PendingIdx
						st.Ev602SavedItem = ev602CubePool[st.Ev602PendingIdx]
					}
					st.Ev602PendingIdx = -1
					_ = w.saveEventUIState(st)
				}
			case 3: // Query current state (CMD 681 sub=3, 0x140bdf3c0)
			}
		} else {
			switch sub {
			case 1: // Transfer saved item into inventory (CMD 680 sub=1, 0x140bdf2c0)
				tmpl := st.Ev602SavedItem
				if tmpl == 0 && st.Ev602SavedIdx >= 0 && int(st.Ev602SavedIdx) < len(ev602CubePool) {
					tmpl = ev602CubePool[st.Ev602SavedIdx]
				}
				if tmpl == 0 {
					tmpl = ev602CubePool[0]
				}
				if !st.Ev602Transferred {
					key := fmt.Sprintf("ev602_transfer:%s:%d", st.Ev602WeekKey, st.Ev602Contributions+1)
					if err := grantItem(key, "us100-ev602-transfer-v1", eventRewardItem{tmpl, 1}); err != nil {
						return nil, err
					}
					st.Ev602Transferred = true
					st.Ev602TransferredMask |= 1
					st.Ev602SavedIdx = -1
					st.Ev602SavedItem = 0
					st.Ev602Contributions++
					_ = w.saveEventUIState(st)
				}
			case 2: // Claim contribution milestone (CMD 680 sub=2, 0x140bdf1f0)
				mIdx := arg(1)
				if mIdx < 4 && (st.Ev602MilestoneMask&(1<<mIdx)) == 0 {
					tmpl := ev602CubePool[int(mIdx)%len(ev602CubePool)]
					key := fmt.Sprintf("ev602_milestone:%d", mIdx)
					if err := grantItem(key, "us100-ev602-milestone-v1", eventRewardItem{tmpl, 1}); err != nil {
						return nil, err
					}
					st.Ev602MilestoneMask |= 1 << mIdx
					_ = w.saveEventUIState(st)
				}
			}
		}
		out = append(out, outboundPacket{
			"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab,
			ev602Packet(st),
		})
		return out, nil

	case uint32(eventIDMileageBonus): // 367: Time Attendance
		var ackParams [10]uint32
		if st.Ev367LastClaimDay != day {
			for i, r := range ev367DailyRewards {
				key := fmt.Sprintf("ev367_daily:%s:%d", day, i)
				if err := grantItem(key, "us100-ev367-daily-v1", r); err != nil {
					return nil, err
				}
			}
			st.Ev367LastClaimDay = day
			_ = w.saveEventUIState(st)
		}
		// Ev367 ACK 680 (0x141915490): params[9]=1, params[0]=pairCount, params[1..]=template,count pairs
		ackParams[0] = uint32(len(ev367DailyRewards))
		for i, r := range ev367DailyRewards {
			ackParams[1+i*2] = r.Template
			ackParams[2+i*2] = r.Count
		}
		ackParams[9] = 1
		out = append(out, outboundPacket{"event_367_ack", 1, protocol.CmdEventAction, protocol.EventActionSuccess(req.EventID, ackParams)})
		return out, nil

	default:
		// Graceful ACK for any other event ID so the client never hangs waiting.
		var empty [10]uint32
		return []outboundPacket{{"event_generic_ack", 1, cmdID, protocol.EventActionSuccess(req.EventID, empty)}}, nil
	}
}
