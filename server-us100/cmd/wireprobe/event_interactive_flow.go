package main

import (
	"context"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"fmt"
	"math/bits"
	"time"
)

const (
	eventIDMiningMission uint16 = 388 // Companion mission manager required by Event 512 (Dungeon Mining)
	eventUIStateTable           = "event_ui_state"
	eventUIStateVersion         = 2 // Step-by-step progression schema (no pre-filled shortcuts)

	ev520Target5M  uint32 = 300
	ev520Target10M uint32 = 600
	ev388OnlineMax uint16 = 1800
	ev619OnlineMax uint32 = 3600
	ev619ClearSec  uint32 = 300 // PVF [proper dungeon time bonus] 300
)

// persistedEventUIState stores account-scoped interactive state for all US100
// event windows so real step-by-step progress and claims survive relog & restarts.
type persistedEventUIState struct {
	StateVersion int `json:"state_version"`

	// Event 520: Reverse Hot Time (5M=300s / 10M=600s real online timer)
	Ev520AccumSec uint32 `json:"ev520_accum_sec"`
	Ev520Claimed1 bool   `json:"ev520_claimed1"`
	Ev520Claimed2 bool   `json:"ev520_claimed2"`

	// Event 512 + 388: Dungeon Mining (weekly missions -> pickaxes -> sequential mining)
	Ev512Initialized    bool      `json:"ev512_initialized"`
	Ev512Pickaxes       uint32    `json:"ev512_pickaxes"`
	Ev512Stages         [4]uint32 `json:"ev512_stages"`
	Ev388WeekKey        string    `json:"ev388_week_key,omitempty"`
	Ev388LastLoginDay   string    `json:"ev388_last_login_day,omitempty"`
	Ev388LoginDays      uint16    `json:"ev388_login_days"`
	Ev388OnlineSec      uint16    `json:"ev388_online_sec"`
	Ev388OnlineRewarded bool      `json:"ev388_online_rewarded,omitempty"`
	Ev388DungeonClear   uint16    `json:"ev388_dungeon_clear"`
	Ev388RaidClear      uint16    `json:"ev388_raid_clear"`

	// Event 515: Everyday Connect Attendance (1 check-in per calendar day)
	Ev515LastCheckDay string `json:"ev515_last_check_day"`
	Ev515Days         byte   `json:"ev515_days"`
	Ev515DailyMask    uint32 `json:"ev515_daily_mask"`
	Ev515AccumMask    uint32 `json:"ev515_accum_mask"`

	// Event 619: Time Guide Stone Daily Check (3600s daily online or +300s/clear)
	Ev619LastDay    string `json:"ev619_last_day"`
	Ev619OnlineSec  uint32 `json:"ev619_online_sec"`
	Ev619PickedMask uint32 `json:"ev619_picked_mask"`
	Ev619ClaimedDay bool   `json:"ev619_claimed_day"`
	Ev619TotalDays  byte   `json:"ev619_total_days"`
	Ev619BonusMask  byte   `json:"ev619_bonus_mask"`

	// Event 614: Melody for Adventurers (4 weekly missions -> cumulative 4/8/12/16)
	Ev614WeekKey      string  `json:"ev614_week_key"`
	Ev614LastLoginDay string  `json:"ev614_last_login_day,omitempty"`
	Ev614Progress     [4]byte `json:"ev614_progress"`
	Ev614Claimed      [4]byte `json:"ev614_claimed"`
	Ev614TotalCleared byte    `json:"ev614_total_cleared"`
	Ev614LineMask     byte    `json:"ev614_line_mask"`

	// Event 602: Ria's Special Cube Lab II (charge 100% -> up to 3 searches/day -> save -> transfer 1/week)
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

	// Event 367: Time Attendance (1 claim per calendar day, 7-day rotating rewards)
	Ev367LastClaimDay string `json:"ev367_last_claim_day"`
	Ev367ClaimCount   uint32 `json:"ev367_claim_count,omitempty"`
}

type eventRewardItem struct {
	Template uint32
	Count    uint32
}

// Exact PVF reward & threshold tables extracted from US100 Script.pvf .evt files.
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
	ev515NewbieBonusDays = map[uint32]bool{
		1: true, 6: true, 10: true, 16: true, 19: true, 22: true, 27: true,
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
	ev619BonusThresholds = [2]byte{5, 10}
	ev619BonusRewards    = []eventRewardItem{
		{10326707, 1}, // 5 cumulative claims
		{10326709, 1}, // 10 cumulative claims
	}

	// Event 614 (WeeklyMissionEvent.evt)
	ev614MissionTargets = [4]byte{3, 1, 200, 20}
	ev614MissionRewards = []eventRewardItem{
		{50081692, 1}, {50081693, 1}, {50081694, 1}, {50081695, 1},
	}
	ev614LineTargets = [4]byte{4, 8, 12, 16}
	ev614LineRewards = []eventRewardItem{
		{50081688, 1}, {50081689, 1}, {50081690, 1}, {50081691, 1},
	}

	// Event 602 (PickAndKeepEvent.evt [pick item info] core cubes)
	ev602CubePool = []uint32{
		50081632, 50081636, 50081640, 50081644, 50081652, 50081648,
		50081633, 50081637, 50081641, 50081645,
	}

	// Event 367 (TimeAttendanceEvent.evt 7-day rotating daily pairs)
	ev367DailySchedule = [][]eventRewardItem{
		{{50061692, 3}, {50061703, 1}},
		{{50061693, 1}, {50061704, 1}},
		{{50061694, 1}, {50061705, 1}},
		{{50061695, 1}, {50061704, 1}},
		{{50061696, 2}, {50061706, 1}},
		{{50061701, 1}, {50061707, 1}},
		{{50061702, 1}, {50061708, 1}},
	}
)

func eventUIStateKey(account int64) string {
	return fmt.Sprintf("account:%d", account)
}

// advanceOnlineEventTime credits real elapsed wall-clock seconds since the last
// session tick to time-gated events (520, 388, 619).
func (w *worldSession) advanceOnlineEventTime(st *persistedEventUIState, now time.Time) (changed bool, crossedMilestone bool) {
	if w == nil || st == nil {
		return false, false
	}
	if w.eventLastTickAt.IsZero() {
		w.eventLastTickAt = now
		return false, false
	}
	delta := int64(now.Sub(w.eventLastTickAt).Seconds())
	if delta <= 0 {
		return false, false
	}
	if delta > 3600 {
		delta = 3600
	}
	w.eventLastTickAt = now
	d := uint32(delta)

	// Event 520: 5M (300s) & 10M (600s)
	if st.Ev520AccumSec < ev520Target10M {
		before := st.Ev520AccumSec
		st.Ev520AccumSec += d
		if st.Ev520AccumSec > ev520Target10M {
			st.Ev520AccumSec = ev520Target10M
		}
		changed = true
		if (before < ev520Target5M && st.Ev520AccumSec >= ev520Target5M) ||
			(before < ev520Target10M && st.Ev520AccumSec >= ev520Target10M) {
			crossedMilestone = true
		}
	}

	// Event 388: weekly 30-minute online mission -> +1 mining pickaxe once/week
	if st.Ev388OnlineSec < ev388OnlineMax {
		next := uint32(st.Ev388OnlineSec) + d
		if next >= uint32(ev388OnlineMax) {
			st.Ev388OnlineSec = ev388OnlineMax
			if !st.Ev388OnlineRewarded {
				st.Ev388OnlineRewarded = true
				st.Ev512Pickaxes++
				crossedMilestone = true
			}
		} else {
			st.Ev388OnlineSec = uint16(next)
		}
		changed = true
	}

	// Event 619: daily 60-minute online timer
	if st.Ev619OnlineSec < ev619OnlineMax {
		before := st.Ev619OnlineSec
		st.Ev619OnlineSec += d
		if st.Ev619OnlineSec > ev619OnlineMax {
			st.Ev619OnlineSec = ev619OnlineMax
		}
		changed = true
		if before < ev619OnlineMax && st.Ev619OnlineSec >= ev619OnlineMax {
			crossedMilestone = true
		}
	}
	return changed, crossedMilestone
}

func (w *worldSession) loadEventUIState(now time.Time) persistedEventUIState {
	var st persistedEventUIState
	var existed bool
	if w != nil && w.characters != nil && w.characters.Store != nil && w.account > 0 {
		existed, _ = w.characters.Store.Database().Get(eventUIStateTable, eventUIStateKey(w.account), &st)
	}
	day := w.activityDay(now)
	week := w.eventPolicy().WeeklyCycleKey(now)

	// Migrate legacy v0/v1 state (which pre-filled all timers/missions) to v2
	// genuine step-by-step progression while preserving already-claimed rewards.
	if st.StateVersion < eventUIStateVersion {
		st.StateVersion = eventUIStateVersion
		if existed {
			if !st.Ev520Claimed1 && !st.Ev520Claimed2 {
				st.Ev520AccumSec = 0
			} else if st.Ev520Claimed1 && !st.Ev520Claimed2 && st.Ev520AccumSec >= ev520Target10M {
				st.Ev520AccumSec = ev520Target5M
			}
			// Reset pre-filled unmined shortcut pickaxes & weekly counters
			if st.Ev512Stages == [4]uint32{} && st.Ev512Pickaxes == 10 {
				st.Ev512Pickaxes = 0
			}
			st.Ev388WeekKey = ""
			if !st.Ev619ClaimedDay {
				st.Ev619OnlineSec = 0
			}
			if st.Ev614Claimed == [4]byte{} {
				st.Ev614WeekKey = ""
			}
			if !st.Ev602Transferred && st.Ev602SavedItem == 0 && st.Ev602SearchesUsed == 0 {
				st.Ev602Energy = 0
			}
		} else {
			st.Ev602PendingIdx = -1
			st.Ev602SavedIdx = -1
		}
	}

	// Event 512 + 388: weekly reset of the 4 pickaxe missions (pickaxes & mined nodes persist).
	if st.Ev388WeekKey != week {
		st.Ev388WeekKey = week
		st.Ev512Initialized = true
		st.Ev388LastLoginDay = ""
		st.Ev388LoginDays = 0
		st.Ev388OnlineSec = 0
		st.Ev388OnlineRewarded = false
		st.Ev388DungeonClear = 0
		st.Ev388RaidClear = 0
	}
	if st.Ev388LastLoginDay != day {
		st.Ev388LastLoginDay = day
		if st.Ev388LoginDays < 3 {
			st.Ev388LoginDays++
			if st.Ev388LoginDays == 3 {
				st.Ev512Pickaxes++
			}
		}
	}

	// Event 619: daily online timer resets each calendar day.
	if st.Ev619LastDay != day {
		st.Ev619LastDay = day
		st.Ev619OnlineSec = 0
		st.Ev619ClaimedDay = false
	}

	// Event 614: weekly reset of the 4 weekly missions (starts from 0, +1 login day today).
	if st.Ev614WeekKey != week {
		st.Ev614WeekKey = week
		st.Ev614LastLoginDay = ""
		st.Ev614Progress = [4]byte{}
		st.Ev614Claimed = [4]byte{}
	}
	if st.Ev614LastLoginDay != day {
		st.Ev614LastLoginDay = day
		if st.Ev614Progress[0] < ev614MissionTargets[0] {
			st.Ev614Progress[0]++
		}
	}

	// Event 602: weekly reset of cube lab; daily reset of cube charge (0%) and 3 searches.
	if st.Ev602WeekKey != week {
		st.Ev602WeekKey = week
		st.Ev602LastDay = day
		st.Ev602Energy = 0
		st.Ev602SearchesUsed = 0
		st.Ev602PendingIdx = -1
		st.Ev602SavedIdx = -1
		st.Ev602SavedItem = 0
		st.Ev602PickedIndices = nil
		st.Ev602Transferred = false
		st.Ev602TransferredMask = 0
	} else {
		if st.Ev602LastDay != day {
			st.Ev602LastDay = day
			st.Ev602Energy = 0
			st.Ev602SearchesUsed = 0
			st.Ev602PendingIdx = -1
		}
		if st.Ev602SavedItem == 0 {
			st.Ev602SavedIdx = -1
		} else if st.Ev602SavedIdx < 0 || int(st.Ev602SavedIdx) >= len(ev602CubePool) || ev602CubePool[st.Ev602SavedIdx] != st.Ev602SavedItem {
			st.Ev602SavedIdx = -1
			for i, tmpl := range ev602CubePool {
				if tmpl == st.Ev602SavedItem {
					st.Ev602SavedIdx = int32(i)
					break
				}
			}
		}
	}

	// Advance real online elapsed time since the previous tick.
	w.advanceOnlineEventTime(&st, now)
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
		{Index: 2, Completed: st.Ev388OnlineSec >= ev388OnlineMax, Progress: st.Ev388OnlineSec},
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

func ev512CanMineNode(stages [4]uint32, stage, node uint32) bool {
	if stage > 3 || node > 4 {
		return false
	}
	bit := uint32(1) << node
	if (stages[stage] & bit) != 0 {
		return false // already mined/opened
	}
	if stage > 0 && (stages[stage-1]&(1<<4)) == 0 {
		return false // previous stage's cave must be opened first
	}
	if node == 4 {
		return (stages[stage] & 0x0f) == 0x0f // all 4 nodes in this stage must be mined before opening cave / final chest
	}
	if node > 0 && (stages[stage]&(1<<(node-1))) == 0 {
		return false // mine nodes sequentially 0 -> 1 -> 2 -> 3
	}
	return true
}

// interactiveEventInitPackets returns all per-event state-sync NOTI packets
// needed after NOTI 108 so every enabled event UI renders real live timers,
// real mission counts, and step-by-step unlock state on login or hot-reload.
func (w *worldSession) interactiveEventInitPackets(pol events.Policy, now time.Time) []outboundPacket {
	if w == nil || w.account == 0 {
		return nil
	}
	w.eventLastTickAt = now
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
		justChecked := false
		if st.Ev515LastCheckDay != day && st.Ev515Days < 28 {
			st.Ev515Days++
			st.Ev515LastCheckDay = day
			justChecked = true
			_ = w.saveEventUIState(st)
		}
		out = append(out, outboundPacket{
			"event_515_attendance_status", 0, protocol.NotiEv515EverydayConnect,
			protocol.Ev515EverydayConnectStatus(true, justChecked, st.Ev515Days, st.Ev515DailyMask, st.Ev515AccumMask),
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

// onInteractiveEventTick persists real elapsed online time during gameplay and
// pushes live state refreshes whenever a timed milestone is crossed.
func (w *worldSession) onInteractiveEventTick(pol events.Policy, now time.Time) []outboundPacket {
	if w == nil || w.account == 0 || w.eventLastTickAt.IsZero() {
		return nil
	}
	var st persistedEventUIState
	if w.characters == nil || w.characters.Store == nil {
		return nil
	}
	ok, _ := w.characters.Store.Database().Get(eventUIStateTable, eventUIStateKey(w.account), &st)
	if !ok || st.StateVersion < eventUIStateVersion {
		return nil
	}
	changed, crossed := w.advanceOnlineEventTime(&st, now)
	if !changed {
		return nil
	}
	_ = w.saveEventUIState(st)
	if !crossed {
		return nil
	}
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
			outboundPacket{"event_388_missions_daily", 0, protocol.NotiEv388MissionDaily, missions},
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
		)
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
	return out
}

// onInteractiveEventFatigue advances fatigue-driven weekly missions (Event 614 Mission 2).
func (w *worldSession) onInteractiveEventFatigue(now time.Time, spent uint16) []outboundPacket {
	if w == nil || w.account == 0 || spent == 0 {
		return nil
	}
	pol := w.eventPolicy()
	if !pol.WeeklyDungeon.Enabled {
		return nil
	}
	st := w.loadEventUIState(now)
	if st.Ev614Progress[2] >= ev614MissionTargets[2] {
		return nil
	}
	next := uint16(st.Ev614Progress[2]) + spent
	if next > uint16(ev614MissionTargets[2]) {
		next = uint16(ev614MissionTargets[2])
	}
	st.Ev614Progress[2] = byte(next)
	_ = w.saveEventUIState(st)
	return []outboundPacket{{
		"event_614_melody_status", 0, protocol.NotiEv614Melody,
		protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask),
	}}
}

// onInteractiveEventClear increments real dungeon-clear counters across active events.
func (w *worldSession) onInteractiveEventClear(now time.Time) []outboundPacket {
	if w == nil || w.account == 0 {
		return nil
	}
	pol := w.eventPolicy()
	st := w.loadEventUIState(now)
	var out []outboundPacket
	changed := false
	isHighTier := w.activeDungeon != nil && (w.activeDungeon.Definition.BasisLevel >= 95 || w.activeDungeon.HasHellRoom)

	if pol.Extra.SeriaWelcomeEnabled {
		ev388Changed := false
		if st.Ev388DungeonClear < 20 {
			st.Ev388DungeonClear++
			if st.Ev388DungeonClear == 20 {
				st.Ev512Pickaxes++
			}
			ev388Changed = true
		}
		if isHighTier && st.Ev388RaidClear < 1 {
			st.Ev388RaidClear = 1
			st.Ev512Pickaxes++
			ev388Changed = true
		}
		if ev388Changed {
			changed = true
			missions := protocol.Ev388Missions(ev388MissionRows(st))
			out = append(out,
				outboundPacket{"event_388_missions_daily", 0, protocol.NotiEv388MissionDaily, missions},
				outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
			)
		}
	}

	if (pol.Fatigue.BurningEnabled || pol.Fatigue.WeekendDouble || pol.Fatigue.ZeroCost || pol.Fatigue.BonusLimit > 0) && st.Ev619OnlineSec < ev619OnlineMax {
		st.Ev619OnlineSec += ev619ClearSec
		if st.Ev619OnlineSec > ev619OnlineMax {
			st.Ev619OnlineSec = ev619OnlineMax
		}
		changed = true
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
		ev614Changed := false
		if isHighTier && st.Ev614Progress[1] < ev614MissionTargets[1] {
			st.Ev614Progress[1] = ev614MissionTargets[1]
			ev614Changed = true
		}
		if st.Ev614Progress[2] < ev614MissionTargets[2] {
			next := uint16(st.Ev614Progress[2]) + 10
			if next > uint16(ev614MissionTargets[2]) {
				next = uint16(ev614MissionTargets[2])
			}
			st.Ev614Progress[2] = byte(next)
			ev614Changed = true
		}
		if st.Ev614Progress[3] < ev614MissionTargets[3] {
			st.Ev614Progress[3]++
			ev614Changed = true
		}
		if ev614Changed {
			changed = true
			out = append(out, outboundPacket{
				"event_614_melody_status", 0, protocol.NotiEv614Melody,
				protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask),
			})
		}
	}

	if (pol.DropEventEnabled || pol.CardEventEnabled) && st.Ev602Energy < 100 {
		gain := uint32(34)
		if isHighTier {
			gain = 100
		}
		st.Ev602Energy += gain
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
// (0x2a9 query/reset) across all active US100 event windows with strict
// step-by-step prerequisite enforcement.
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
	case uint32(eventIDExpHotTime): // 520: Reverse Hot Time (0=5M/300s, 1=10M/600s)
		slot := arg(0)
		reward, ok := ev520Rewards[slot]
		if !ok {
			return []outboundPacket{{"event_520_refused", 1, cmdID, protocol.EventActionRefused(2, req.EventID)}}, nil
		}
		requiredSec := ev520Target5M
		if slot == 1 {
			requiredSec = ev520Target10M
		}
		if st.Ev520AccumSec < requiredSec {
			_ = w.saveEventUIState(st)
			return []outboundPacket{
				{"event_520_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
				{"event_520_status", 0, protocol.NotiEv520ReverseHotTime, protocol.Ev520ReverseHotTimeStatus(st.Ev520AccumSec, st.Ev520Claimed1, st.Ev520Claimed2, 100)},
			}, nil
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

	case uint32(eventIDSeriaWelcome): // 512: Dungeon Mining (strict sequential nodes + pickaxe cost)
		stage := arg(0)
		node := arg(1)
		if stage > 3 || node > 4 {
			return []outboundPacket{{"event_512_refused", 1, cmdID, protocol.EventActionRefused(2, req.EventID)}}, nil
		}
		if st.Ev512Pickaxes == 0 || !ev512CanMineNode(st.Ev512Stages, stage, node) {
			_ = w.saveEventUIState(st)
			return []outboundPacket{
				{"event_512_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
				{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
			}, nil
		}
		st.Ev512Pickaxes--
		st.Ev512Stages[stage] |= uint32(1) << node
		if reward, ok := ev512Rewards[stage*10+node]; ok {
			key := fmt.Sprintf("ev512_mine:%d:%d", stage, node)
			if err := grantItem(key, "us100-ev512-mining-v1", reward); err != nil {
				return nil, err
			}
		}
		_ = w.saveEventUIState(st)
		out = append(out,
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
			outboundPacket{"event_388_mining_ping", 0, protocol.NotiEv388MiningPing, protocol.Ev388MiningPing(uint16(st.Ev512Pickaxes))},
		)
		return out, nil

	case uint32(eventIDMiningMission): // 388: Mining Weekly Mission online heartbeat (30s client poll)
		// advanceOnlineEventTime already credited real elapsed seconds in loadEventUIState.
		_ = w.saveEventUIState(st)
		missions := protocol.Ev388Missions(ev388MissionRows(st))
		out = append(out,
			outboundPacket{"event_388_missions_daily", 0, protocol.NotiEv388MissionDaily, missions},
			outboundPacket{"event_512_mining_status", 0, protocol.NotiEv512DungeonMining, protocol.Ev512DungeonMiningStatus(st.Ev512Pickaxes, st.Ev512Stages)},
		)
		return out, nil

	case uint32(eventIDDailyCheckin): // 515: Everyday Connect Attendance (1 day/calendar day)
		if st.Ev515LastCheckDay != day && st.Ev515Days < 28 {
			st.Ev515Days++
			st.Ev515LastCheckDay = day
		}
		claimDaily := func(d uint32) error {
			if d < 1 || d > 28 || d > uint32(st.Ev515Days) || (st.Ev515DailyMask&(1<<d)) != 0 {
				return nil
			}
			reward, ok := ev515DailyRewards[d]
			if !ok {
				return nil
			}
			if ev515NewbieBonusDays[d] {
				reward.Count *= 2
			}
			key := fmt.Sprintf("ev515_daily:%d", d)
			if err := grantItem(key, "us100-ev515-daily-v1", reward); err != nil {
				return err
			}
			st.Ev515DailyMask |= 1 << d
			return nil
		}
		claimAccum := func(m uint32) error {
			if m > uint32(st.Ev515Days) || (st.Ev515AccumMask&(1<<m)) != 0 {
				return nil
			}
			reward, ok := ev515AccumRewards[m]
			if !ok {
				return nil
			}
			key := fmt.Sprintf("ev515_accum:%d", m)
			if err := grantItem(key, "us100-ev515-accum-v1", reward); err != nil {
				return err
			}
			st.Ev515AccumMask |= 1 << m
			return nil
		}

		action := arg(0)
		targetDay := arg(1)
		switch action {
		case 1:
			if err := claimDaily(targetDay); err != nil {
				return nil, err
			}
		case 2:
			if err := claimAccum(targetDay); err != nil {
				return nil, err
			}
		case 3: // allRecvBtn: claim only already-unlocked days/milestones
			for d := uint32(1); d <= uint32(st.Ev515Days) && d <= 28; d++ {
				if err := claimDaily(d); err != nil {
					return nil, err
				}
			}
			for _, m := range []uint32{5, 15, 25} {
				if err := claimAccum(m); err != nil {
					return nil, err
				}
			}
		}
		_ = w.saveEventUIState(st)
		out = append(out, outboundPacket{
			"event_515_attendance_status", 0, protocol.NotiEv515EverydayConnect,
			protocol.Ev515EverydayConnectStatus(true, false, st.Ev515Days, st.Ev515DailyMask, st.Ev515AccumMask),
		})
		return out, nil

	case uint32(eventIDFatigueBurning): // 619: Time Guide Stone Daily Check (3600s daily + 5/10 milestones)
		sub := arg(0)
		var ackParams [10]uint32
		if cmdID == protocol.CmdEventQuery {
			switch sub {
			case 2:
				// CMD 681 sub=2: Reset daily pick pool once >= 3 gifts picked (does NOT bypass daily limit).
				if bits.OnesCount32(st.Ev619PickedMask) >= 3 {
					st.Ev619PickedMask = 0
					_ = w.saveEventUIState(st)
				}
			case 1:
				// Query only
			}
		} else if sub == 1 {
			// CMD 680 sub=1: Daily random gift claim requires 3600s online (or clear bonus) and unclaimed today.
			if st.Ev619OnlineSec < ev619OnlineMax || st.Ev619ClaimedDay {
				_ = w.saveEventUIState(st)
				claimedToday := uint32(0)
				if st.Ev619ClaimedDay {
					claimedToday = 1
				}
				return []outboundPacket{
					{"event_619_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
					{"event_619_dailycheck_status", 0, protocol.NotiEv619DailyCheck, protocol.Ev619DailyCheckStatus(st.Ev619OnlineSec, st.Ev619PickedMask, claimedToday, st.Ev619TotalDays, st.Ev619BonusMask)},
				}, nil
			}
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
		} else if sub == 2 {
			// CMD 680 sub=2: Bonus milestone claim strictly gated by Ev619TotalDays >= 5 / 10.
			bIdx := int(arg(1))
			if bIdx < 0 || bIdx >= len(ev619BonusRewards) || st.Ev619TotalDays < ev619BonusThresholds[bIdx] || (st.Ev619BonusMask&(1<<uint(bIdx))) != 0 {
				_ = w.saveEventUIState(st)
				claimedToday := uint32(0)
				if st.Ev619ClaimedDay {
					claimedToday = 1
				}
				return []outboundPacket{
					{"event_619_bonus_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
					{"event_619_dailycheck_status", 0, protocol.NotiEv619DailyCheck, protocol.Ev619DailyCheckStatus(st.Ev619OnlineSec, st.Ev619PickedMask, claimedToday, st.Ev619TotalDays, st.Ev619BonusMask)},
				}, nil
			}
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
		claimedToday := uint32(0)
		if st.Ev619ClaimedDay {
			claimedToday = 1
		}
		out = append(out, outboundPacket{
			"event_619_dailycheck_status", 0, protocol.NotiEv619DailyCheck,
			protocol.Ev619DailyCheckStatus(st.Ev619OnlineSec, st.Ev619PickedMask, claimedToday, st.Ev619TotalDays, st.Ev619BonusMask),
		})
		return out, nil

	case uint32(eventIDWeeklyDungeon): // 614: Melody for Adventurers (strict targets + 1 per mission)
		sub := arg(0)
		idx := int(arg(1))
		if sub == 1 {
			if idx < 0 || idx >= len(ev614MissionRewards) || st.Ev614Claimed[idx] != 0 || st.Ev614Progress[idx] < ev614MissionTargets[idx] {
				_ = w.saveEventUIState(st)
				return []outboundPacket{
					{"event_614_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
					{"event_614_melody_status", 0, protocol.NotiEv614Melody, protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask)},
				}, nil
			}
			reward := ev614MissionRewards[idx]
			key := fmt.Sprintf("ev614_mission:%s:%d", st.Ev614WeekKey, idx)
			if err := grantItem(key, "us100-ev614-mission-v1", reward); err != nil {
				return nil, err
			}
			st.Ev614Claimed[idx] = 1
			if st.Ev614TotalCleared < 16 {
				st.Ev614TotalCleared++
			}
			_ = w.saveEventUIState(st)
		} else if sub == 2 {
			if idx < 0 || idx >= len(ev614LineRewards) || st.Ev614TotalCleared < ev614LineTargets[idx] || (st.Ev614LineMask&(1<<uint(idx))) != 0 {
				_ = w.saveEventUIState(st)
				return []outboundPacket{
					{"event_614_line_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
					{"event_614_melody_status", 0, protocol.NotiEv614Melody, protocol.Ev614MelodyStatus(st.Ev614Progress, st.Ev614Claimed, st.Ev614TotalCleared, st.Ev614LineMask)},
				}, nil
			}
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

	case uint32(eventIDDropGoldBoost): // 602: Ria's Special Cube Lab II (charge 100% -> search -> save -> transfer -> milestone)
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
			case 1: // Search cube requires 100% energy, < 3 searches today, no unresolved pending item
				if st.Ev602Energy < 100 || st.Ev602SearchesUsed >= 3 || st.Ev602PendingIdx >= 0 {
					_ = w.saveEventUIState(st)
					return []outboundPacket{
						{"event_602_search_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
						{"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab, ev602Packet(st)},
					}, nil
				}
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
			case 2: // Save pending item (arg1 == 1) or discard (arg1 == 0)
				if st.Ev602PendingIdx >= 0 && int(st.Ev602PendingIdx) < len(ev602CubePool) {
					addPicked(byte(st.Ev602PendingIdx))
					if arg(1) == 1 {
						st.Ev602SavedIdx = st.Ev602PendingIdx
						st.Ev602SavedItem = ev602CubePool[st.Ev602PendingIdx]
					}
					st.Ev602PendingIdx = -1
					_ = w.saveEventUIState(st)
				}
			case 3: // Query only
			}
		} else {
			switch sub {
			case 1: // Transfer saved item requires a saved item and not yet transferred this week
				if st.Ev602Transferred || st.Ev602SavedIdx < 0 || st.Ev602SavedItem == 0 {
					_ = w.saveEventUIState(st)
					return []outboundPacket{
						{"event_602_transfer_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
						{"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab, ev602Packet(st)},
					}, nil
				}
				key := fmt.Sprintf("ev602_transfer:%s:%d", st.Ev602WeekKey, st.Ev602Contributions+1)
				if err := grantItem(key, "us100-ev602-transfer-v1", eventRewardItem{st.Ev602SavedItem, 1}); err != nil {
					return nil, err
				}
				st.Ev602Transferred = true
				st.Ev602TransferredMask |= 1
				st.Ev602SavedIdx = -1
				st.Ev602SavedItem = 0
				st.Ev602Contributions++
				_ = w.saveEventUIState(st)
			case 2: // Claim contribution milestone mIdx (0..3) requires Contributions >= mIdx+1
				mIdx := arg(1)
				if mIdx >= 4 || st.Ev602Contributions < mIdx+1 || (st.Ev602MilestoneMask&(1<<mIdx)) != 0 {
					_ = w.saveEventUIState(st)
					return []outboundPacket{
						{"event_602_milestone_not_ready", 1, cmdID, protocol.EventActionRefused(2, req.EventID)},
						{"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab, ev602Packet(st)},
					}, nil
				}
				tmpl := ev602CubePool[int(mIdx)%len(ev602CubePool)]
				key := fmt.Sprintf("ev602_milestone:%d", mIdx)
				if err := grantItem(key, "us100-ev602-milestone-v1", eventRewardItem{tmpl, 1}); err != nil {
					return nil, err
				}
				st.Ev602MilestoneMask |= 1 << mIdx
				_ = w.saveEventUIState(st)
			}
		}
		out = append(out, outboundPacket{
			"event_602_cubelab_status", 0, protocol.NotiEv602RiaCubeLab,
			ev602Packet(st),
		})
		return out, nil

	case uint32(eventIDMileageBonus): // 367: Time Attendance (1 claim per calendar day, rotating 7-day rewards)
		var ackParams [10]uint32
		if st.Ev367LastClaimDay == day {
			return []outboundPacket{{"event_367_already_claimed", 1, cmdID, protocol.EventActionRefused(2, req.EventID)}}, nil
		}
		rewards := ev367DailySchedule[int(st.Ev367ClaimCount)%len(ev367DailySchedule)]
		for i, r := range rewards {
			key := fmt.Sprintf("ev367_daily:%s:%d", day, i)
			if err := grantItem(key, "us100-ev367-daily-v1", r); err != nil {
				return nil, err
			}
		}
		st.Ev367LastClaimDay = day
		st.Ev367ClaimCount++
		_ = w.saveEventUIState(st)
		ackParams[0] = uint32(len(rewards))
		for i, r := range rewards {
			ackParams[1+i*2] = r.Template
			ackParams[2+i*2] = r.Count
		}
		ackParams[9] = 1
		out = append(out, outboundPacket{"event_367_ack", 1, protocol.CmdEventAction, protocol.EventActionSuccess(req.EventID, ackParams)})
		return out, nil

	default:
		var empty [10]uint32
		return []outboundPacket{{"event_generic_ack", 1, cmdID, protocol.EventActionSuccess(req.EventID, empty)}}, nil
	}
}
