package protocol

import (
	"encoding/binary"
	"fmt"
)

// Verified US100 event command & state-notification opcodes recovered from DFO_unpacked.exe:
//   - CMD 680 (0x2a8) / ACK 680 (0x143830ba0): universal event action / reward claim
//   - CMD 681 (0x2a9): universal event query / reset / secondary action
//   - NOTI 1783 (0x6f7, 0x14112d110) / NOTI 1784 (0x6f8, 0x14112d5c0): Event 388 weekly/daily missions (used by Event 512)
//   - NOTI 1789 (0x6fd, 0x141105420): Event 392 Daily DFO Planner state
//   - NOTI 1949 (0x79d, 0x140bdeb80): Event 602 Ria's Special Cube Laboratory II state (52 bytes)
//   - NOTI 2134 (0x856, 0x14053b100): Event 614 Melody for Adventurers state (10 bytes)
//   - NOTI 2155 (0x86b, 0x1404e3d60): Event 512 Dungeon Mining state (20 bytes)
//   - NOTI 2156 (0x86c, 0x14112da70): Event 388 -> Event 512 pickaxe/mission sync (2 bytes)
//   - NOTI 2161 (0x871, 0x14048c000): Event 619 Time Guide Stone Daily Check state (20 bytes)
//   - NOTI 2215 (0x8a7, 0x140422af0): Event 515 Everyday Connect Attendance state (11 bytes)
//   - NOTI 2222 (0x8ae, 0x140421120): Event 520 Reverse Hot Time state (12 bytes)
const (
	CmdUseStackableAction uint16 = 507 // 0x1fb: special [action type] stackable use (0x143a01190)
	CmdRequestEventGift   uint16 = 643 // 0x283: CMDPACKET_REQUEST_EVENT_GIFT (0x142fae080 / 0x142fad4e0)
	CmdEventAction        uint16 = 680
	CmdEventQuery         uint16 = 681

	NotiEventGiftUpdate      uint16 = 559  // 0x22f: incremental event gift claim states (0x142fad6a0)
	NotiEv388MissionWeekly   uint16 = 1783
	NotiEv388MissionDaily    uint16 = 1784
	NotiEv392OneDayPlan      uint16 = 1789
	NotiEv500SoulAnvil       uint16 = 1870 // 0x74e: Event 500 Forge of Souls state (0x140d1d3e0)
	NotiEv602RiaCubeLab      uint16 = 1949
	NotiEv614Melody          uint16 = 2134
	NotiEv512DungeonMining   uint16 = 2155
	NotiEv388MiningPing      uint16 = 2156
	NotiEv619DailyCheck      uint16 = 2161
	NotiEv515EverydayConnect uint16 = 2215
	NotiEv520ReverseHotTime  uint16 = 2222
	NotiEventGiftReset       uint16 = 2272 // 0x8e0: full reset + load of event gift states (0x142fad6c0)
)

// EventInitParam is one 50-byte entry inside NOTI 108 Part 2
// (0x143851220..0x1438512f5 -> EventObject::vtable[0xe8]): u16 eventID + 12*u32 params.
type EventInitParam struct {
	EventID uint16
	Params  [12]uint32
}

// ActiveEventsDetailedWithParams encodes NOTI 108 (0x6c) including both Part 1
// (ActiveEventEntry list) and Part 2 (per-event 12*u32 initial parameters).
func ActiveEventsDetailedWithParams(entries []ActiveEventEntry, params []EventInitParam) ([]byte, error) {
	if len(params) > 255 {
		return nil, fmt.Errorf("too many event init params: %d", len(params))
	}
	base, err := ActiveEventsDetailed(entries)
	if err != nil {
		return nil, err
	}
	if len(params) == 0 {
		return base, nil
	}
	// Replace trailing u8 paramCount=0 with actual count + 50-byte records.
	p := make([]byte, 0, len(base)+len(params)*50)
	p = append(p, base[:len(base)-1]...)
	p = append(p, byte(len(params)))
	for _, item := range params {
		p = add16(p, item.EventID)
		for _, v := range item.Params {
			p = add32(p, v)
		}
	}
	return p, nil
}

// UseStackableActionRequest is a decoded CMD 507 (0x1fb) special stackable use
// request (e.g. [epic drop buff event action] items 10326700..10326704).
// Wire layout verified from 0x143a0eff3 / live capture:
//   [0..1] u16 slot, [2] u8 invenList, [3..6] u32 arg0, [7..10] u32 actionType, ...
type UseStackableActionRequest struct {
	Slot       uint16
	List       byte
	Arg0       uint32
	ActionType uint32
}

func DecodeUseStackableAction(p []byte) (UseStackableActionRequest, error) {
	var r UseStackableActionRequest
	if len(p) < 11 {
		return r, fmt.Errorf("use stackable action requires at least 11 bytes, got %d", len(p))
	}
	r.Slot = binary.LittleEndian.Uint16(p[0:2])
	r.List = p[2]
	r.Arg0 = binary.LittleEndian.Uint32(p[3:7])
	r.ActionType = binary.LittleEndian.Uint32(p[7:11])
	return r, nil
}

// UseStackableActionSuccess encodes ACK 507 (0x1fb, handler 0x143a01190):
// u8 success=1, u16 slot, u8 invenList, u32 actionType.
func UseStackableActionSuccess(r UseStackableActionRequest) []byte {
	p := make([]byte, 0, 8)
	p = append(p, 1)
	p = add16(p, r.Slot)
	p = append(p, r.List)
	p = add32(p, r.ActionType)
	return p
}

// UseStackableActionRefused encodes a failed ACK 507 (0x1fb).
func UseStackableActionRefused(code uint16) []byte {
	p := make([]byte, 0, 3)
	p = append(p, 0)
	p = add16(p, code)
	return p
}

// EventCommand is a decoded CMD 680 (0x2a8) or CMD 681 (0x2a9) request.
// All event action/query packets in US100 write u32 eventID followed by 0..4 u32 arguments.
type EventCommand struct {
	EventID uint32
	Args    []uint32
}

func DecodeEventCommand(p []byte) (EventCommand, error) {
	var c EventCommand
	if len(p) < 4 {
		return c, fmt.Errorf("event command requires at least 4 bytes, got %d", len(p))
	}
	c.EventID = binary.LittleEndian.Uint32(p[0:4])
	n := (len(p) - 4) / 4
	if n > 0 {
		c.Args = make([]uint32, n)
		for i := 0; i < n; i++ {
			c.Args[i] = binary.LittleEndian.Uint32(p[4+i*4 : 8+i*4])
		}
	}
	return c, nil
}

// EventActionSuccess encodes ACK 680 (0x2a8, handler 0x143830ba0):
// u8 success=1, u32 eventID, 10*u32 params -> dispatched to EventObject::vtable[0x20].
func EventActionSuccess(eventID uint32, params [10]uint32) []byte {
	p := make([]byte, 0, 1+4+40)
	p = append(p, 1)
	p = add32(p, eventID)
	for _, v := range params {
		p = add32(p, v)
	}
	return p
}

// EventActionRefused encodes a failed ACK 680 (0x2a8, handler 0x143830ba0).
func EventActionRefused(code uint16, eventID uint32) []byte {
	p := make([]byte, 0, 7)
	p = append(p, 0)
	p = add16(p, code)
	p = add32(p, eventID)
	return p
}

// Ev520ReverseHotTimeStatus encodes NOTI 2222 (0x8ae, 12 bytes, handler 0x140421120):
//   - [0..3]:  u32 accumulated online seconds ([obj+0x194])
//   - [4]:     u8  5M reward claimed flag (0/1) ([obj+0x198])
//   - [5]:     u8  10M reward claimed flag (0/1) ([obj+0x199])
//   - [6..7]:  u16 reserved (0)
//   - [8..11]: u32 period state (1 = in-period active) ([obj+0x19c])
func Ev520ReverseHotTimeStatus(accumSec uint32, claimed1, claimed2 bool, periodState uint32) []byte {
	p := make([]byte, 12)
	binary.LittleEndian.PutUint32(p[0:4], accumSec)
	if claimed1 {
		p[4] = 1
	}
	if claimed2 {
		p[5] = 1
	}
	binary.LittleEndian.PutUint32(p[8:12], periodState)
	return p
}

// Ev512DungeonMiningStatus encodes NOTI 2155 (0x86b, 20 bytes, handler 0x1404e3d60):
//   - [0..3]:  u32 owned pickaxes ([ui+0xce8])
//   - [4..19]: 4*u32 stage mining progress 0..5 ([ui+0xcd8..+0xce4])
func Ev512DungeonMiningStatus(pickaxes uint32, stages [4]uint32) []byte {
	p := make([]byte, 20)
	binary.LittleEndian.PutUint32(p[0:4], pickaxes)
	for i, s := range stages {
		binary.LittleEndian.PutUint32(p[4+i*4:8+i*4], s)
	}
	return p
}

// Ev388MiningPing encodes NOTI 2156 (0x86c, 2 bytes, handler 0x14112da70).
func Ev388MiningPing(val uint16) []byte {
	p := make([]byte, 2)
	binary.LittleEndian.PutUint16(p, val)
	return p
}

// Ev388MissionRow is one 4-byte mission progress row inside NOTI 1783 / NOTI 1784
// (handlers 0x14112d110 / 0x14112d5c0): u8 index, u8 completed(0/1), u16 progress.
type Ev388MissionRow struct {
	Index     byte
	Completed bool
	Progress  uint16
}

// Ev388Missions encodes NOTI 1783 (0x6f7) or NOTI 1784 (0x6f8).
func Ev388Missions(rows []Ev388MissionRow) []byte {
	p := make([]byte, 0, 1+len(rows)*4)
	p = append(p, byte(len(rows)))
	for _, r := range rows {
		done := byte(0)
		if r.Completed {
			done = 1
		}
		p = append(p, r.Index, done)
		p = add16(p, r.Progress)
	}
	return p
}

// Ev515EverydayConnectStatus encodes NOTI 2215 (0x8a7, 11 bytes, handler 0x140422af0):
//   - [0]:     u8  event active / returnee flag (1)
//   - [1]:     u8  today can check-in (1=can check today, 0=checked)
//   - [2]:     u8  cumulative attendance days (0..28)
//   - [3..6]:  u32 daily reward claimed bitmask (bit d = day d)
//   - [7..10]: u32 accumulated milestone claimed bitmask (bits 5, 15, 25)
func Ev515EverydayConnectStatus(active, todayCanCheck bool, days byte, dailyMask, accumMask uint32) []byte {
	p := make([]byte, 11)
	if active {
		p[0] = 1
	}
	if todayCanCheck {
		p[1] = 1
	}
	p[2] = days
	binary.LittleEndian.PutUint32(p[3:7], dailyMask)
	binary.LittleEndian.PutUint32(p[7:11], accumMask)
	return p
}

// Ev619DailyCheckStatus encodes NOTI 2161 (0x871, 20 bytes, handler 0x14048c000 -> [obj+0xf0..+0x103]):
//   - [0..3]:  u32 state (0) ([obj+0xf0])
//   - [4..7]:  u32 online seconds (0..3600; multiplied by 1000ms at 0x14048c0d9) ([obj+0xf4])
//   - [8]:     u8  daily claimed today (0=available, 1=claimed; checked at 0x14048c290 & 0x14048c649) ([obj+0xf8])
//   - [9..11]: padding (0)
//   - [12..15]: u32 picked gift bitmask (bits 0..6; checked at 0x14048c2ea, 0x14048c300, 0x14048c342) ([obj+0xfc])
//   - [16]:    u8  total daily claims count (unlocks 5/10 milestones at 0x14048c217) ([obj+0x100])
//   - [17]:    u8  milestone bonus claimed bitmask (bit 0 = 5x, bit 1 = 10x at 0x14048c240) ([obj+0x101])
//   - [18..19]: u16 reserved (0)
func Ev619DailyCheckStatus(onlineSec, pickedMask, claimedToday uint32, totalDays, bonusMask byte) []byte {
	p := make([]byte, 20)
	binary.LittleEndian.PutUint32(p[4:8], onlineSec)
	if claimedToday != 0 {
		p[8] = 1
	}
	binary.LittleEndian.PutUint32(p[12:16], pickedMask)
	p[16] = totalDays
	p[17] = bonusMask
	return p
}

// Ev614MelodyStatus encodes NOTI 2134 (0x856, 10 bytes, handler 0x14053b100):
//   - [0..3]: u8 progress for 4 weekly missions
//   - [4..7]: u8 claimed flags (0/1) for 4 weekly missions
//   - [8]:    u8 total completed weekly missions (0..16, milestones at 4/8/12/16)
//   - [9]:    u8 milestone reward claimed bitmask (bits 0..3)
func Ev614MelodyStatus(progress [4]byte, claimed [4]byte, totalCompleted, lineMask byte) []byte {
	p := make([]byte, 10)
	copy(p[0:4], progress[:])
	copy(p[4:8], claimed[:])
	p[8] = totalCompleted
	p[9] = lineMask
	return p
}

// Ev602CubeLabState represents the 52-byte (0x34) wire state read by NOTI 1949
// (0x79d, handler 0x140bdeb80 -> [obj+0x130..+0x163], interpreted at 0x140bdf410).
type Ev602CubeLabState struct {
	DayIndex        byte   // +0x00: day offset (weekIndex = DayIndex / 7 at 0x140bdf6fe)
	HasPendingItem  bool   // +0x01: ([obj+0x131])
	SavedItemIdx    int32  // +0x02: saved cube pool index (-1 -> 0xff if none; must appear in PickedIndices)
	PickedIndices   []byte // +0x03 count + +0x04..+0x17 up to 20 discovered/excluded cube pool indices
	TransferredMask uint32 // +0x1c: weekly transfer bitmask (bit w = week w transferred; derives contributions & transferred flag)
	DailySearches   byte   // +0x20: ([obj+0x150], used count 0..3)
	MilestoneMask   uint32 // +0x24: ([obj+0x154], claimed milestone bitmask)
	TimerSeconds    uint32 // +0x28: ([obj+0x158])
	EnergyPoints    uint32 // +0x2c: ([obj+0x15c], 0..100)
	CurrentItemIdx  int32  // +0x30: ([obj+0x160], -1 if none)
}

// Ev602RiaCubeLabStatus encodes NOTI 1949 (0x79d, 52 bytes, handler 0x140bdeb80).
func Ev602RiaCubeLabStatus(st Ev602CubeLabState) []byte {
	p := make([]byte, 0x34)
	p[0] = st.DayIndex
	if st.HasPendingItem {
		p[1] = 1
	}
	if st.SavedItemIdx >= 0 && st.SavedItemIdx < 255 {
		p[2] = byte(st.SavedItemIdx)
	} else {
		p[2] = 0xff
	}
	picked := make([]byte, 0, len(st.PickedIndices)+1)
	seen := make(map[byte]bool, len(st.PickedIndices)+1)
	for _, idx := range st.PickedIndices {
		if !seen[idx] && len(picked) < 20 {
			seen[idx] = true
			picked = append(picked, idx)
		}
	}
	// 0x140bdf63e only marks savedItemIdx ([rdi+0x40]) when savedItemIdx is in pickedIndices.
	if st.SavedItemIdx >= 0 && st.SavedItemIdx < 255 {
		sIdx := byte(st.SavedItemIdx)
		if !seen[sIdx] && len(picked) < 20 {
			picked = append(picked, sIdx)
		}
	}
	p[3] = byte(len(picked))
	copy(p[0x04:0x18], picked)
	binary.LittleEndian.PutUint32(p[0x1c:0x20], st.TransferredMask)
	p[0x20] = st.DailySearches
	binary.LittleEndian.PutUint32(p[0x24:0x28], st.MilestoneMask)
	binary.LittleEndian.PutUint32(p[0x28:0x2c], st.TimerSeconds)
	binary.LittleEndian.PutUint32(p[0x2c:0x30], st.EnergyPoints)
	binary.LittleEndian.PutUint32(p[0x30:0x34], uint32(st.CurrentItemIdx))
	return p
}

// DecodeRequestEventGift decodes CMD 643 (0x283, 0x142fae080): u16 giftID.
func DecodeRequestEventGift(p []byte) (uint16, error) {
	if len(p) < 2 {
		return 0, fmt.Errorf("request event gift requires 2 bytes, got %d", len(p))
	}
	return binary.LittleEndian.Uint16(p[0:2]), nil
}

// RequestEventGiftSuccess encodes ACK 643 (0x283, handler 0x142fad4e0):
// u8 success=1, u16 giftID, u8 claimedState (1=claimed).
func RequestEventGiftSuccess(giftID uint16, claimed byte) []byte {
	p := make([]byte, 4)
	p[0] = 1
	binary.LittleEndian.PutUint16(p[1:3], giftID)
	p[3] = claimed
	return p
}

// RequestEventGiftRefused encodes a failed ACK 643 (0x283).
func RequestEventGiftRefused(code uint16) []byte {
	p := make([]byte, 3)
	p[0] = 0
	binary.LittleEndian.PutUint16(p[1:3], code)
	return p
}

// EventGiftEntry is one (giftID, claimed) entry inside NOTI 2272 (0x8e0) or NOTI 559 (0x22f).
type EventGiftEntry struct {
	GiftID  uint16
	Claimed byte
}

// EventGiftStatus encodes NOTI 2272 (0x8e0, 0x142fad6c0) and NOTI 559 (0x22f, 0x142fad6a0):
// u16 count + count * (u16 giftID, u8 claimed).
func EventGiftStatus(entries []EventGiftEntry) []byte {
	p := make([]byte, 0, 2+len(entries)*3)
	p = add16(p, uint16(len(entries)))
	for _, e := range entries {
		p = add16(p, e.GiftID)
		p = append(p, e.Claimed)
	}
	return p
}

// Ev500SlotState is one 12-byte weapon slot record inside NOTI 1870 (0x74e, 0x140d1d3e0).
type Ev500SlotState struct {
	Upgrade   uint32 `json:"upgrade"`
	TryCount  uint32 `json:"try_count"`
	Unlocked  byte   `json:"unlocked"`
	Extracted byte   `json:"extracted"`
}

// Ev500SoulAnvilStatus encodes NOTI 1870 (0x74e, 64 bytes, handler 0x140d1d3e0):
//   - [0..43]  44-byte core block ([obj+0x10..+0x3b]):
//       [0..3]:   u32 accumCount ([obj+0x10])
//       [4..39]:  3 * 12-byte slot structs ([obj+0x14..+0x37])
//       [40..43]: u32 rewardClaimMask ([obj+0x38])
//   - [44..47] u8(0), u16(0), u8(0)
//   - [48..51] u32 wideStrByteLen (0)
//   - [52..63] 3 * u32 reserved (0)
func Ev500SoulAnvilStatus(accumCount uint32, slots [3]Ev500SlotState, rewardMask uint32) []byte {
	p := make([]byte, 64)
	binary.LittleEndian.PutUint32(p[0:4], accumCount)
	for i := 0; i < 3; i++ {
		off := 4 + i*12
		binary.LittleEndian.PutUint32(p[off:off+4], slots[i].Upgrade)
		binary.LittleEndian.PutUint32(p[off+4:off+8], slots[i].TryCount)
		p[off+8] = slots[i].Unlocked
		p[off+9] = slots[i].Extracted
	}
	binary.LittleEndian.PutUint32(p[40:44], rewardMask)
	return p
}
