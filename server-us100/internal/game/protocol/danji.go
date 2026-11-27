package protocol

import (
	"encoding/binary"
	"fmt"
)

// EventIDDanji is the active event ID checked by the US100 client's DANJI HUD
// button visibility gate (0x140399390 -> IsEventActive(0x1057)) and jar-open
// state machine (0x14039c75a -> IsEventActive(0x1057)).
const EventIDDanji uint16 = 0x1057 // 4183

// DanjiInventorySpace is the dedicated 64-slot inventory space (0x149788e68,
// initialized at 0x143a88754) queried by the DANJI UI (0x14039930c, 0x1403997e8,
// 0x140399d04, 0x14039be8c, 0x14039c774) for DANJI pots (template 590004385).
const DanjiInventorySpace byte = 0x2a

// US224DanjiInventory encodes NOTI 13 or NOTI 14 for inventory space 0x2a (42).
// Verified from DFO_unpacked.exe NOTI 13 (0x1434fc3c3..0x1434fc9e7) and NOTI 14
// (0x14351074b..0x143510775): header is u8(0x2a) + u16(count), followed by
// count * 0xb1-byte ordinary item records (with no avatar extra trailers).
func US224DanjiInventory(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return us224OrdinaryList([]byte{DanjiInventorySpace}, rows)
}

// ActiveEvents encodes NOTI 108 (0x6c, handler 0x14384d5d0 -> parser 0x143851af0).
// Wire layout: u16 count, then count * (u16 eventID, u8 hasDetail=0).
// When hasDetail is 0, the client skips the optional detail strings and inserts
// eventID directly into EventManager's active event map (0x143851bf2 -> 0x143851e62).
func ActiveEvents(eventIDs []uint16) ([]byte, error) {
	if len(eventIDs) > 65535 {
		return nil, fmt.Errorf("too many active events: %d", len(eventIDs))
	}
	p := make([]byte, 0, 2+len(eventIDs)*3)
	p = add16(p, uint16(len(eventIDs)))
	for _, id := range eventIDs {
		p = add16(p, id)
		p = append(p, 0) // hasDetail = 0
	}
	return p, nil
}

// ActiveEventEntry describes one active event entry inside NOTI 108 (0x6c).
// Verified against DFO_unpacked.exe parser 0x143851af0 and EventCalendar.xui
// populator 0x14383d230 / row renderer 0x1438c6e80:
//   - ColorType: 1=orange, 2=light blue, 3=green, 4=gold
//   - Title -> EventInfo+0x38 (rendered in Event Calendar rows)
//   - StartTime/EndTime -> EventInfo+0x60/+0x68 (Unix seconds)
//   - ShowOnCalendar -> EventInfo+0xe8 (must be true to appear in Event Calendar)
type ActiveEventEntry struct {
	EventID        uint16
	HasDetail      bool
	EventType      byte
	ColorType      byte
	Title          string
	Subtitle       string
	Description    string
	StartTime      int32
	EndTime        int32
	Link           string
	Notice         string
	ShowOnCalendar bool
}

func appendNoti108String(dst []byte, s string) []byte {
	const maxLen = 0xffe
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	dst = add32(dst, uint32(len(s)))
	return append(dst, s...)
}

// ActiveEventsDetailed encodes full NOTI 108 (0x6c) including optional
// EventCalendar metadata (0x143851bf8..0x143851dbc) and the Part-2 parameter
// count terminator (u8 0 at 0x1438511bf).
func ActiveEventsDetailed(entries []ActiveEventEntry) ([]byte, error) {
	if len(entries) > 65535 {
		return nil, fmt.Errorf("too many active events: %d", len(entries))
	}
	p := make([]byte, 0, 3+len(entries)*64)
	p = add16(p, uint16(len(entries)))
	for _, ev := range entries {
		p = add16(p, ev.EventID)
		if !ev.HasDetail {
			p = append(p, 0)
			continue
		}
		p = append(p, 1) // hasDetail = 1
		evType := ev.EventType
		if evType == 0 {
			evType = 1
		}
		color := ev.ColorType
		if color == 0 || color > 4 {
			color = 1
		}
		p = append(p, evType, color)
		p = appendNoti108String(p, ev.Title)
		p = appendNoti108String(p, ev.Subtitle)
		p = appendNoti108String(p, ev.Description)
		p = add32(p, uint32(ev.StartTime))
		p = add32(p, uint32(ev.EndTime))
		p = appendNoti108String(p, ev.Link)
		p = appendNoti108String(p, ev.Notice)
		if ev.ShowOnCalendar {
			p = append(p, 1)
		} else {
			p = append(p, 0)
		}
	}
	// Part 2 (0x1438510e0 -> 0x1438511bf): u8 paramCount = 0.
	p = append(p, 0)
	return p, nil
}

// DanjiStatus encodes NOTI 2279 (0x8e7, handler 0x140399480), which reads 6 raw
// bytes into global 0x1495b4a20:
//   - [0..3]: u32 current DANJI Stack gauge (0..942 in NewLostTreasure.evt)
//   - [4]:    u8  state flag (0x1495b4a24)
//   - [5]:    u8  lobby idle animation level 1..5 (0x1495b4a25 -> [rbx+0x4d8])
func DanjiStatus(stack uint32, flag byte, animLevel byte) []byte {
	if animLevel < 1 {
		animLevel = 1
	}
	if animLevel > 5 {
		animLevel = 5
	}
	p := make([]byte, 6)
	binary.LittleEndian.PutUint32(p[0:4], stack)
	p[4] = flag
	p[5] = animLevel
	return p
}

// DanjiOpenRequest is the decoded CMD 2052 (0x804) payload sent by 0x14039c812..0x14039c856.
// The client sends 22 bytes (0x16, padded to 24 on the encrypted wire):
//   - [0..12]:  13-byte native packet-struct header prefix (uninitialized stack bytes)
//   - [13..16]: i32 bag slot holding DANJI pot (template 590004385)
//   - [17..20]: i32 open count (1 or 10 per NewLostTreasure.evt [able count])
//   - [21]:     u8  jar tier (1, 2, or 3 per NewLostTreasure.evt [level])
//   - [22..23]: 2-byte zero cipher padding
type DanjiOpenRequest struct {
	Slot  int32
	Count int32
	Tier  byte
}

func DecodeDanjiOpen(p []byte) (DanjiOpenRequest, error) {
	var r DanjiOpenRequest
	if len(p) != 24 {
		return r, fmt.Errorf("danji open request must contain 22 bytes padded to 24, got %d", len(p))
	}
	if p[22] != 0 || p[23] != 0 {
		return r, fmt.Errorf("nonzero trailing padding in danji open request")
	}
	r.Slot = int32(binary.LittleEndian.Uint32(p[13:17]))
	r.Count = int32(binary.LittleEndian.Uint32(p[17:21]))
	r.Tier = p[21]
	if r.Tier < 1 || r.Tier > 3 {
		return r, fmt.Errorf("unsupported danji tier %d", r.Tier)
	}
	if r.Count != 1 && r.Count != 10 {
		return r, fmt.Errorf("unsupported danji open count %d", r.Count)
	}
	return r, nil
}

// DANJI reward grade values read by the US100 client UI (0x14039cbee, 0x14039cc86):
//   0 = Grade S (gold/rainbow), 1 = Grade A (purple glow), 2 = Grade B (normal).
const (
	DanjiGradeS byte = 0
	DanjiGradeA byte = 1
	DanjiGradeB byte = 2
)

// DanjiReward is one 6-byte reward entry read by ACK 2052 handler 0x140398f60
// and rendered by 0x14039cbee / 0x14039da06 / 0x14039da3c / 0x14039ef41 / 0x14039ef77:
//   - [0..3]: u32 itemTemplateID
//   - [4]:    u8  grade (0=S, 1=A, 2=B)
//   - [5]:    u8  itemCount (1..255)
type DanjiReward struct {
	Template uint32
	Grade    byte
	Count    uint16
}

// DanjiOpenSuccess encodes ACK 2052 (0x804, handler 0x140398f60):
// u8 success=1, u32 rewardCount, then rewardCount * (u32 template, u8 grade, u8 count).
func DanjiOpenSuccess(rewards []DanjiReward) ([]byte, error) {
	if len(rewards) == 0 {
		return nil, fmt.Errorf("danji open success requires at least one reward")
	}
	p := make([]byte, 0, 5+len(rewards)*6)
	p = append(p, 1)
	p = add32(p, uint32(len(rewards)))
	for _, r := range rewards {
		if r.Template == 0 || r.Count == 0 || r.Grade > DanjiGradeB {
			return nil, fmt.Errorf("invalid danji reward template=%d grade=%d count=%d", r.Template, r.Grade, r.Count)
		}
		cnt := r.Count
		if cnt > 255 {
			cnt = 255
		}
		p = add32(p, r.Template)
		p = append(p, r.Grade, byte(cnt))
	}
	return p, nil
}
