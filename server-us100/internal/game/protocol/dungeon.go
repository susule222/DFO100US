package protocol

import (
	"encoding/binary"
	"fmt"
)

// CMD15: normal town gate sends u32(0); the tutorial sender at146cce650
// uses a source dungeon ID instead. Slot1 pads the four-byte body to8.
func DecodeDungeonGate(p []byte) (uint32, error) {
	if len(p) != 8 {
		return 0, fmt.Errorf("dungeon gate requires four bytes padded to8")
	}
	if e := padding(p[4:], 8); e != nil {
		return 0, e
	}
	return binary.LittleEndian.Uint32(p), nil
}

// EnterDungeonSelection is NOTI27, handler145303260. This is the solo,
// non-relay path with no optional event/party collections. Scalar meanings
// beyond verified mode fields remain unnamed, rather than guessed IDs.
// The full 36-byte reader sequence is checked with the original native code.
// EnterSelectUserStateUS is S0/0003 USER_STATE for the enter-select context:
// u8 count=1, u16 user id, u8 state.
//
// The state byte is the caller's, and for this client it must be the TOWN value.
// The 90 US reference sends 0x01 here (build_enter_select_user_state), but in
// this client's own vocabulary 0x01 is "in a dungeon" — see UserStateTown and
// UserStateDungeon in user_state.go, where the town flow sends 0 and the room
// does not load unless the dungeon value is in place. Sending 1 while the actor
// is still standing at the town gate tells the client the local actor is inside
// a dungeon, which is why the character left the screen and the selection UI
// never appeared.
func EnterSelectUserStateUS(actorID uint16, state byte) []byte {
	return append(add16([]byte{1}, actorID), state)
}

// UdpHostUnavailableUS is S0/001A: the reference sends a single zero byte.
func UdpHostUnavailableUS() []byte { return []byte{0} }

// LicenseDungeonClearInfoUS is S0/02FE: one u8 schedule key, where 0 selects
// the authored all-open set.
func LicenseDungeonClearInfoUS() []byte { return []byte{0} }

// DungeonPermissionRow is one entry of S0/0005 DUNGEON_PERMISSION.
type DungeonPermissionRow struct {
	ID    uint32
	State byte
}

// DungeonPermissionUS is S0/0005 DUNGEON_PERMISSION in the US layout:
//
//	u16 row count, then per row: u32 dungeon id, u8 clear state
//
// The reference explicitly forbids the older short rows (u16 id + u8), and this
// packet is what makes the selection UI list dungeons as available.
func DungeonPermissionUS(rows []DungeonPermissionRow) ([]byte, error) {
	if len(rows) > 0xffff {
		return nil, fmt.Errorf("permission list exceeds native count")
	}
	p := add16(nil, uint16(len(rows)))
	for _, r := range rows {
		if r.ID == 0 {
			return nil, fmt.Errorf("invalid permission dungeon id")
		}
		p = add32(p, r.ID)
		p = append(p, r.State)
	}
	return p, nil
}

// GuildDungeonRepeatBonusUS is S0/02E6 with the feature disabled: u8 enable = 0
// followed by the reference's three u32 scalars and an empty u32 pair count.
// The reference builder (build_guild_dungeon_repeat_bonus_info_02e6) uses this
// shape, and the enter-select flow sends it before the UI opens.
func GuildDungeonRepeatBonusUS() []byte {
	p := append([]byte{0}, add32(nil, 0)...)
	p = add32(add32(add32(p, 0), 0), 0)
	return append(p, 0, 0, 0, 0) // u32 pair count = 0
}

// ClearQuestListUS is S0/0164 CLEAR_QUEST from the reference source
// (quest_common.build_clear_quest_list_0164): u32 payload length followed by a
// bitmap where index = quest id and value 1 means finished. An empty list is the
// live-safe form (payload length 0).
func ClearQuestListUS(finished []uint32) ([]byte, error) {
	const limit = 30000
	maxID := 0
	for _, id := range finished {
		if id == 0 {
			continue
		}
		if int(id) > maxID {
			maxID = int(id)
		}
	}
	if maxID == 0 {
		return add32(nil, 0), nil
	}
	length := maxID + 1
	if length > limit {
		length = limit
	}
	blob := make([]byte, length)
	for _, id := range finished {
		if id > 0 && int(id) < length {
			blob[id] = 1
		}
	}
	return append(add32(nil, uint32(length)), blob...), nil
}

// LicenseDungeonPermissionUS is S0/02FD with no licensed dungeons: a u16 row
// count of zero (the reference's snapshot format is count plus u32 triples).
func LicenseDungeonPermissionUS() []byte { return add16(nil, 0) }

// EnterDungeonSelectionUS100 is NOTI27 (ENTER_SELECT_DUNGEON) in the shape this
// client's own reader consumes (handler 0x14351e670):
//
//	u8 A | u8 B | u32 | u32 | u8 count | count × u16 keys | ... (padded)
//
// Offset 0 is a single byte, not the reference's 32-bit mode. A=1 takes a
// different branch (0x144e39a70 + 0x144e4c080(obj, 2)) and the client then skips
// the selection screen entirely, which is what the 2026-09-18 20:35 archive
// shows; A=0 is the branch that loads the page.
//
// The reader copies the keys into a 16-byte local array (eight u16 slots before
// the stack cookie), so the list is capped there rather than trusted.
func EnterDungeonSelectionUS100(dungeonIDs []uint16) ([]byte, error) {
	const capacity = 8
	if len(dungeonIDs) > capacity {
		return nil, fmt.Errorf("enter-select candidate list holds at most %d entries", capacity)
	}
	p := append([]byte{0, 0}, make([]byte, 4)...) // A, B, u32
	p = add32(p, 0)
	p = append(p, byte(len(dungeonIDs)))
	for _, id := range dungeonIDs {
		p = add16(p, id)
	}
	// The reader continues past the list (u32, a second counted group, flags), so
	// the body is padded with zeros: every remaining count it consumes is zero.
	if len(p) < enterSelectUS100BodyLength {
		p = append(p, make([]byte, enterSelectUS100BodyLength-len(p))...)
	}
	return p, nil
}

// enterSelectUS100BodyLength matches the length the empty body has always had, so
// the padded tail is unchanged from the runs that reached the transition.
const enterSelectUS100BodyLength = 48

// EnterDungeonSelectionReferenceUS is the 90 US reference's NOTI27 body, kept for
// reference only — it is NOT what this client reads (see
// EnterDungeonSelectionUS100 above for the shape this client's reader takes).
func EnterDungeonSelectionReferenceUS() []byte {
	p := add32(add32(nil, 1), 0) // mode 1, reserved 0
	p = append(p, 0)             // no blocked hell-quest members
	p = append(p, 0, 0)          // list2 count, list3 count
	p = add32(p, 0)              // field_a
	p = add16(add16(p, 0), 0)    // field_b, field_c
	p = add32(p, 0)              // field_d
	p = append(p, 0, 0)          // flag_e, n4 (must stay < 4)
	return add16(p, 0)           // field_f
}

// EnterDungeonSelection is the zero-filled NOTI27 body that opens this client's
// dungeon selection screen. Its shape is the 2.38 one and its content is
// deliberately empty: every count the client's reader consumes is zero, so the
// screen comes up from the client's own data. Sending it is what the 2026-09-18
// 20:0x sessions confirmed — the screen opened (the client's state report sits
// at 0x1b while it is up) and the actor stayed in town.
//
// What it does NOT do is give the client a dungeon list: pressing the enter
// button then does nothing, which is the open item. The reference keeps that
// list in S0/0005's permission rows, which this server already sends.
func EnterDungeonSelection() []byte {
	p := []byte{0, 0, 0}      // first/relay/relay-extra, 1453032f6/307/7c4
	p = add32(add32(p, 0), 0) // 145303871/8ec
	p = append(p, 0)          // u16 collection, 145303ac3
	p = add32(p, 0)           // 145303b8f
	p = append(p, 0, 0, 0)    // u16 collections, 145303bc6/c38/d58
	p = add16(add16(p, 0), 0) // 145303f7b/fa2
	p = add32(p, 0)           // 145303fc7
	p = append(p, 0)          // mode flag, 145303ff4
	p = add32(p, 0)           // mode0, 145304033 (native clamps to0..1)
	p = add16(p, 0)           // 1453040b4
	return append(p, 0, 0)    // u32/u16 collections, 145304140/1b9
}

// DungeonSelection mirrors the US Stage3 (90/100) SELECT_DUNGEON body exactly as
// the reference server parses it (flow-dungeon_enter.parse_0010_plain):
//
//	u32 dungeon_id | u8 difficulty | u16 entry_option | u8 mode2 | u8 sso |
//	u16 extra16 | u32 extra32 | u8 tail0 | u8 tail1        (= 17 bytes)
//
// Party stays in the struct only because the solo-party normalisation call site
// reads it; the US layout carries no party field, so it is always zero.
type DungeonSelection struct {
	ID          uint32
	Difficulty  byte
	EntryOption uint16
	Mode2       byte
	SSO         byte
	Extra16     uint16
	Extra32     uint32
	Tail0       byte
	Tail1       byte
	Party       uint16
}

// DecodeDungeonSelection parses the US (Stage3) select body. The client writes
// 17 bytes and the codec pads the frame to 32, so 17..32 are accepted and the
// fields are read from a zero-padded copy; the 2.38-era fields beyond byte 17
// (options/event) are gone in this build.
func DecodeDungeonSelection(p []byte) (r DungeonSelection, e error) {
	if len(p) < 17 || len(p) > 32 {
		return r, fmt.Errorf("select dungeon must contain 17..32 bytes")
	}
	if len(p) > 26 {
		if e = padding(p[26:], 16); e != nil {
			return r, e
		}
	}
	body := make([]byte, 32)
	copy(body, p)
	r.ID = binary.LittleEndian.Uint32(body)
	r.Difficulty = body[4]
	r.EntryOption = binary.LittleEndian.Uint16(body[5:])
	r.Mode2 = body[7]
	r.SSO = body[8]
	r.Extra16 = binary.LittleEndian.Uint16(body[9:])
	r.Extra32 = binary.LittleEndian.Uint32(body[11:])
	r.Tail0 = body[15]
	r.Tail1 = body[16]
	return r, nil
}

// DungeonSelectAckUS100 is the success result of CMD16. The US100 command
// handler registered at 0x143486c19 receives the decoded result from CORE_CMD
// and reads no other fields on success (0x143481810..0x1434818fb). NOTI16
// is the separate CANCEL_ITEMTRADE message.
func DungeonSelectAckUS100() []byte { return []byte{1} }

type DungeonInfoState struct {
	ID               uint32
	Difficulty, Maze byte
	Boss             [2]byte
	HasRandomHell    bool
	RandomHell       [2]byte
}

// DungeonInfo encodes the ordinary solo branch of US100 NOTI28. The group
// count at offset 13 and optional row count at 29 are both zero. See the
// native cursor fixture for the field widths at these two branches.
func DungeonInfo(s DungeonInfoState) []byte {
	p := make([]byte, 41)
	binary.LittleEndian.PutUint32(p, s.ID)
	p[4] = s.Difficulty
	p[7] = s.Maze
	p[8], p[9] = s.Boss[0], s.Boss[1]
	if s.HasRandomHell {
		p[10], p[11] = s.RandomHell[0], s.RandomHell[1]
	} else {
		p[10], p[11] = 0xff, 0xff // no random-hell room
	}
	binary.LittleEndian.PutUint16(p[16:], 0x000c)
	binary.LittleEndian.PutUint32(p[19:], 0xffffffff) // skip seed application
	return p
}

type DungeonMonster struct {
	Entity      uint16
	SourceIndex uint32
	Level       byte
	Template    uint32
	Rank        byte     // Original map parser1471e92ac..9368: normal0, champion1, super2, boss3.
	Team        uint32   // Current actor affiliation: source team0 friendly, default100 enemy.
	NonCombat   bool     // Source team0/dummy; still spawned for native cinematic scripts.
	SourceTail  [2]int32 // Map row fields6/7, retained separately from spawn count.
}
type StartMapState struct {
	Position  [2]byte
	Seed, Map uint32
	Monsters  []DungeonMonster
}

// StartMapUS100 creates a room with its current actors. In the US100 reader
// 0x1434e2c5f, flag 1 selects the map and its counted actor list. The loop
// at 0x1434e2d50 consumes 25 bytes per actor, then 0x1434e30d4 resumes
// reading the room trailer even when the actor list is empty.
func StartMapUS100(s StartMapState) ([]byte, error) {
	if s.Map == 0 {
		return nil, fmt.Errorf("invalid start map")
	}
	p := []byte{s.Position[0], s.Position[1], 0}
	p = add32(p, s.Seed)
	p = append(p, 0, 0)
	p = add32(p, 1)
	p = append(p, 1) // First load of this room
	p = add32(p, s.Map)
	// US100 reads two u16 fields and one byte before the source map index.
	p, err := appendStartMapMonsters(p, s.Monsters, []byte{0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}
	return append(p, 0, 0, 0, 0), nil // No extra drops, fog or groups. Solo party slot 0.
}

func StartMap(s StartMapState) ([]byte, error) {
	if s.Map == 0 {
		return nil, fmt.Errorf("invalid start map")
	}
	p := append([]byte{}, s.Position[0], s.Position[1], 0)
	p = add32(p, s.Seed)
	p = append(p, 0, 0)
	p = add32(p, 0xffffffff)
	// Native transition record defaults at1452b7494..4a7, consumed18 bytes.
	p = append(p, 0, 0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0)
	p = append(p, 1)
	p = add32(p, s.Map)
	p, err := appendStartMapMonsters(p, s.Monsters, []byte{0, 0})
	if err != nil {
		return nil, err
	}
	return append(p, 0, 0, 0, 255), nil // no extra APC/group rows; no other-party actor
}

func appendStartMapMonsters(p []byte, monsters []DungeonMonster, rowPrefix []byte) ([]byte, error) {
	if len(monsters) > 255 {
		return nil, fmt.Errorf("too many start map monsters: %d", len(monsters))
	}
	p = append(p, byte(len(monsters)))
	seen := make(map[uint16]bool, len(monsters))
	for _, m := range monsters {
		if m.Entity == 0 || m.Entity == 65535 || seen[m.Entity] || m.Level == 0 || m.Template == 0 || m.Rank > 3 || (m.Team != 0 && m.Team != 100) {
			return nil, fmt.Errorf("invalid monster identity")
		}
		seen[m.Entity] = true
		p = append(p, rowPrefix...)
		p = add32(p, m.SourceIndex)
		p = add16(p, m.Entity)
		p = add32(p, m.Template)
		p = append(p, m.Level, m.Rank, 0, 0, 255)
		p = add32(p, m.Team)
		p = append(p, 0)
	}
	return p, nil
}
func DungeonLoaded() []byte { return []byte{0, 0, 0, 0, 0} }

// NOTI132, native1452aee40, normal solo return-to-town branch.
func DungeonSelectionReturn() []byte { return []byte{0} }

type MonsterDeathReport struct {
	Entity uint32
	Killer uint16
}

func DecodeMonsterDeath(p []byte) (MonsterDeathReport, error) {
	// Native145dc9bf4..145dca2af. The following combat/check fields are opaque;
	// do not interpret them as HP, damage, item IDs or authoritative rewards.
	if len(p) < 64 || len(p) > 4096 || len(p)%8 != 0 {
		return MonsterDeathReport{}, fmt.Errorf("invalid monster death envelope")
	}
	if 31+int(p[30])*14+7 > len(p) {
		return MonsterDeathReport{}, fmt.Errorf("truncated death participant list")
	}
	return MonsterDeathReport{binary.LittleEndian.Uint32(p), binary.LittleEndian.Uint16(p[4:])}, nil
}

// The no-item NOTI38 branch is independently consumed by1452bb930.
// Drops are a separate generated outcome; this response does not grant any.
func MonsterDeathConfirmed(entity uint16) []byte {
	return append(add16(add16(nil, entity), 0), 0, 0, 255, 0)
}

func DecodeMoveDungeonRoom(p []byte) ([2]byte, error) {
	// Both clients put the target maze coordinates and transition flag in
	// the same prefix. Only the target is used by the room traversal rules.
	switch len(p) {
	case 144: // US 2.24.0.124, observed on the Mirkwood (0,1) -> (1,1) exit
	case 160: // 2.38 writes 155 bytes before block padding
		if err := padding(p[155:], 16); err != nil {
			return [2]byte{}, err
		}
	default:
		return [2]byte{}, fmt.Errorf("unexpected room move length %d", len(p))
	}
	if p[10] > 1 {
		return [2]byte{}, fmt.Errorf("invalid room transition flag")
	}
	return [2]byte{p[0], p[1]}, nil
}
