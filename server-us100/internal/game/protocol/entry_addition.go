package protocol

import (
	"encoding/binary"
	"fmt"
	"math"
)

// PackedEntryStats stores the native wire units, not display units. Mapping
// source attributes into these units is a separate character/rules concern.
// Current native conversion: 147555f80, exactly 91 bytes -> 40 protected words.
type PackedEntryStats struct {
	HP, MP        uint32
	Core          [4]uint16
	Element       [4]int16
	Status        [19]int16
	Inventory     int32
	Regeneration  [2]int16
	Movement      uint32
	AttackCasting [2]uint16
	RecoveryJump  [2]int16
	Weight        int32
	BasePercent   byte
	Extra         float32
}

func (s PackedEntryStats) Bytes() ([]byte, error) {
	if s.HP == 0 || s.MP == 0 || s.BasePercent == 0 || math.IsNaN(float64(s.Extra)) || math.IsInf(float64(s.Extra), 0) {
		return nil, fmt.Errorf("incomplete or non-finite entry stats")
	}
	p := add32(add32(nil, s.HP), s.MP)
	for _, v := range s.Core {
		p = add16(p, v)
	}
	for _, v := range s.Element {
		p = add16(p, uint16(v))
	}
	for _, v := range s.Status {
		p = add16(p, uint16(v))
	}
	p = add32(p, uint32(s.Inventory))
	for _, v := range s.Regeneration {
		p = add16(p, uint16(v))
	}
	p = add32(p, s.Movement)
	for _, v := range s.AttackCasting {
		p = add16(p, v)
	}
	for _, v := range s.RecoveryJump {
		p = add16(p, uint16(v))
	}
	p = add32(p, uint32(s.Weight))
	p = add32(append(p, s.BasePercent), math.Float32bits(s.Extra))
	return p, nil
}

// UserInfoAdditionMarkerUS100 builds a mode-1 payload in the US100 entry shape
// (23-byte block, actor id, two bytes, 64-bit experience, two bytes, u16, byte,
// one equipment-style entry, then the tail fields) with a distinct marker in
// every field. The US100 build reads a different mode-1 entry than the 2.38
// layout the gameplay encoder emits, and the field meanings are still unknown:
// one frida read trace against this payload maps offsets to markers so the real
// values can be placed. Semantic recovery only; never used for gameplay.
func UserInfoAdditionMarkerUS100(actorID uint16, exp uint64) ([]byte, error) {
	if actorID == 0 || actorID == 0xffff {
		return nil, fmt.Errorf("invalid marker addition actor identity")
	}
	p := append(add16([]byte{1}, 1), 0, 0) // mode 1, one entry, two context bytes
	for i := 0; i < 23; i++ {
		p = append(p, byte(0x11+i)) // block markers 0x11..0x27
	}
	p = add16(p, actorID)
	p = append(p, 0x31, 0x32)
	p = add32(add32(p, uint32(exp)), uint32(exp>>32))
	p = append(p, 0x35, 0x36)
	p = add16(p, 0x3738)
	p = append(p, 0x39)
	p = append(p, 0x41) // one equipment-style entry
	p = add16(p, 0x4243)
	p = append(p, 0x44)
	p = append(p, 0x45, 0x46)
	p = add32(p, 0x47484950)
	p = append(p, 0x51, 0x52, 0x53, 0x54, 0x55)
	return p, nil
}

// UserInfoAdditionUS100 encodes the mode-1 addition entry the way the US100
// build parses it: a 23-byte block, then the u16 actor id, then the tail the
// parser reads past the key.
// EntryAdditionBlock is the 23-byte entry block with the field widths this
// client's own parser reads. The widths are not a guess: every reference to the
// block's global (0x149477580) in the whole .text is inside the mode-1 entry
// parser 0x1438068a0, and they read exactly these offsets
// (tools/xref_range.py, 15 references, no gaps: 4+8+1+1+1+1+1+1+1+4 = 23).
//
// The old "11 × u16 appearance indices" reading is therefore wrong; the block is
// a mixed struct whose fields are applied to the actor as:
//
//	+0  u32 -> actor+0x434, also handed to 0x142b58dc0(actor, value)
//	+4  u64 -> actor+0x438, also handed to 0x142b58e30(actor, value)
//	+12 u8  -> flag (value > 0) -> 0x143f0b990, sets a bit in object+0x1d8
//	+13 u8  -> flag (value > 0) -> 0x143f0b930, sets bit 0x10 (shl 4, or)
//	+14 u8  -> flag (value > 0) -> 0x143f0b8d0
//	+15 u8  -> 0x143d4ae00(object, value)
//	+16 u8  -> 0x143d4ae10(object, value)
//	+17 i8  -> 0x143eeff90 / 0x143d4ae20, first pass of a two-pass loop
//	+18 i8  -> same calls, second pass
//	+19 u32 -> 0x143f0b9f0(actor, value)
//
// What the two big values are is still not settled, and the two candidates are
// recorded because the field names follow them:
//
//   - level + experience. The store targets are actor+0x50c (u32) and actor+0x510
//     (u64), and both are mirrored into a per-thread singleton at +0x4c / +0x50 —
//     a (u32, u64) adjacent pair updated only while the actor is the current one,
//     which is the shape of a local player's level and 64-bit experience.
//   - maximum HP + MP. The two reference servers carry a character's combat
//     values in USERINFO mode/subtype 1 as a blob that starts with hp_max, mp_max
//     scaled by ten.
//
// The block is sent with level/experience by default; -entry-addition-hp puts the
// hp/mp pair in the same two slots instead, so one run can tell them apart by
// what the HUD shows.
type EntryAdditionBlock struct {
	Level      uint32 // +0 -> actor+0x50c (u32)
	Experience uint64 // +4 -> actor+0x510 (u64)
	Flag12     byte
	Flag13     byte
	Flag14     byte
	Value15    byte
	Value16    byte
	Signed17   int8
	Signed18   int8
	Value19    uint32
}

func (b EntryAdditionBlock) bytes() []byte {
	out := make([]byte, 23)
	binary.LittleEndian.PutUint32(out[0:], b.Level)
	binary.LittleEndian.PutUint64(out[4:], b.Experience)
	out[12], out[13], out[14] = b.Flag12, b.Flag13, b.Flag14
	out[15], out[16] = b.Value15, b.Value16
	out[17], out[18] = byte(b.Signed17), byte(b.Signed18)
	binary.LittleEndian.PutUint32(out[19:], b.Value19)
	return out
}

// entryAdditionTail is the fixed part of the entry after the block and the key:
// the parser reads u8, u8, u32, u32, u8, u8 (see the extraction of 0x1438068a0),
// which is 15 bytes with the u8/u16 trailer the earlier field table lists.
func entryAdditionTail() []byte {
	return make([]byte, 15)
}

// EntryAdditionBodyMinimum is how many bytes the entry body must carry for the
// parser to stay inside it.
//
// The read extraction (tools/extract_reads_sized.py 0x38068a0) shows the parser
// does NOT stop at the experience pair when the key matches: past the fixed tail
// it keeps reading (u8@40, u16@41, u8@43, u8@44, u8@45, u32@46, u8@50..u8@53 and
// deeper inside a per-part loop). The extraction also shows the read chain is
// branch-dependent, so the exact end is not pinned — but every configuration
// this project has ever sent stopped at 40 bytes, i.e. inside the region the
// client reads. A reader that walks off the end of the plaintext reads whatever
// follows in the frame buffer, and a garbage count inside a loop is one way to
// end up alive but silent at the loading screen.
//
// 256 is deliberately larger than any observed read: the remaining fields are
// unknown, so the choice is "zeros, and enough of them to cover every read",
// not "the exact length".
const EntryAdditionBodyMinimum = 256

// UserInfoAdditionUS100 encodes the mode-1 entry: u8 mode, u16 count, two context
// bytes, the 23-byte block, the u16 actor key, then the fixed tail.
func UserInfoAdditionUS100(actorID uint16, block EntryAdditionBlock) ([]byte, error) {
	return UserInfoAdditionPaddedUS100(actorID, block, 0)
}

// UserInfoAdditionPaddedUS100 encodes the same entry with its body zero-padded to
// at least pad bytes (everything already known is kept; only the unknown region
// grows). pad <= 0 keeps the fixed length.
func UserInfoAdditionPaddedUS100(actorID uint16, block EntryAdditionBlock, pad int) ([]byte, error) {
	if actorID == 0 || actorID == 0xffff {
		return nil, fmt.Errorf("invalid addition actor identity")
	}
	if pad < 0 || pad > 4096 {
		return nil, fmt.Errorf("addition pad %d out of range", pad)
	}
	p := append(add16([]byte{1}, 1), 0, 0)
	p = append(p, block.bytes()...)
	p = add16(p, actorID)
	p = append(p, entryAdditionTail()...)
	for len(p) < pad {
		p = append(p, 0)
	}
	return p, nil
}

// UserInfoStatProbeUS100 encodes the mode-6 entry: u8 mode 6, u16 count, two
// context bytes, a u16 key and a 45-byte block that the native applier
// 0x143f0c2f0 copies onto actor+0x7f2.
//
// The block's first four u32s are two pairs, read by 0x143c21110 — the function
// the stat settlement (0x143f03330) calls with actor+0x7f2:
//
//	mov r8d, dword [block+4] ; mov edx, dword [block+0] ; call 0x143bf4090(stat, edx, r8d)
//	mov r8d, dword [block+12]; mov edx, dword [block+8] ; call 0x143bf48c0(stat, edx, r8d)
//	call 0x143bf42b0(stat)
//
// 0x143bf4090 stores the second value unconditionally and only rebuilds when the
// first differs from the stored one, so a pair reads as (value, maximum). The two
// pairs are HP and MP with high confidence; the earlier probe put HP in the first
// slot and MP in the second, i.e. one HP value and one MP value inside a single
// pair, which is why the client showed no change.
//
// This probe writes the maximum into BOTH slots of each pair: with value ==
// maximum the HUD reads 100% whichever slot it treats as the current value, so a
// single run settles whether this is the attribute channel at all.
func UserInfoStatProbeUS100(actorID uint16, hp, mp uint32) ([]byte, error) {
	if actorID == 0 || actorID == 0xffff {
		return nil, fmt.Errorf("invalid stat actor identity")
	}
	block := make([]byte, 45)
	binary.LittleEndian.PutUint32(block[0:], hp)
	binary.LittleEndian.PutUint32(block[4:], hp)
	binary.LittleEndian.PutUint32(block[8:], mp)
	binary.LittleEndian.PutUint32(block[12:], mp)
	p := append(add16([]byte{6}, 1), 0, 0)
	p = add16(p, actorID)
	p = append(p, block...)
	return p, nil
}

// UserInfoAdditionStubUS100 sends the mode-1 entry with the correct US100 shape
// but a key that matches no actor. The client needs the packet to leave its
// loading state (with no mode-1 packet at all the town never becomes
// interactive), while a matching key makes it apply an entry whose remaining
// fields are not recovered yet, which hangs the loading screen. Not applying
// anything is therefore the only state known to be both interactive and safe
// until the entry's real content is recovered.
func UserInfoAdditionStubUS100() []byte {
	p := append(add16([]byte{1}, 1), 0, 0)
	p = append(p, make([]byte, 23)...)
	p = add16(p, 0) // key that never matches an actor
	p = append(p, 0, 0, 0, 0)
	return p
}

// UserInfoAdditionRampUS100 is the mode-1 entry with a byte ramp in the 23-byte
// block. The block was tested mid-session with u32 and byte markers without any
// effect on the HUD, but re-sent information does not re-run the client's stat
// settlement: this variant exists to run the same probe during the entry
// sequence, where the actor is created.
func UserInfoAdditionRampUS100(actorID uint16) []byte {
	block := make([]byte, 23)
	for i := range block {
		block[i] = byte((i + 1) & 0xff)
	}
	p := append(add16([]byte{1}, 1), 0, 0)
	p = append(p, block...)
	p = add16(p, actorID)
	p = append(p, 0, 0, 0, 0)
	return p
}

// UserInfoAdditionLongUS100 pads the mode-1 entry with a byte-ramp tail. The
// client reads a 38-byte entry and stops, then never leaves its loading state
// when the key matches, which points at the entry being longer than what this
// build sends: a long ramp tail shows how far the client actually reads and
// whether the loading then completes. Recovery only.
func UserInfoAdditionLongUS100(actorID uint16, tailLen int) ([]byte, error) {
	if tailLen < 0 || tailLen > 4096 {
		return nil, fmt.Errorf("entry tail out of range")
	}
	p := append(add16([]byte{1}, 1), 0, 0)
	p = append(p, make([]byte, 23)...)
	p = add16(p, actorID)
	p = append(p, 0, 0, 0, 0)
	for i := 0; i < tailLen; i++ {
		p = append(p, byte((i+1)&0xff))
	}
	return p, nil
}

// UserInfoAdditionNoneAppearanceUS100 sends the mode-1 entry with every
// appearance slot marked "none" (0xffff). A character without equipment should
// reference no appearance items, so this is the semantically correct block for
// the current probe characters; the earlier zero block and the catalog default
// list both broke the loading, and this variant is the remaining candidate.
func UserInfoAdditionNoneAppearanceUS100(actorID uint16) []byte {
	block := make([]byte, 23)
	for i := 0; i < 11; i++ {
		block[i*2] = 0xff
		block[i*2+1] = 0xff
	}
	p := append(add16([]byte{1}, 1), 0, 0)
	p = append(p, block...)
	p = add16(p, actorID)
	p = append(p, 0, 0, 0, 0)
	return p
}

// UserInfoAdditionNoneLongUS100 is the none-appearance entry plus a byte-ramp
// tail. With valid appearance slots the client reads on past the experience
// pair (the zero ramp and the catalog list both stopped it), so the tail
// exposes the sections it expects and how they are shaped.
func UserInfoAdditionNoneLongUS100(actorID uint16, tailLen int) ([]byte, error) {
	if tailLen < 0 || tailLen > 4096 {
		return nil, fmt.Errorf("entry tail out of range")
	}
	p := UserInfoAdditionNoneAppearanceUS100(actorID)
	for i := 0; i < tailLen; i++ {
		p = append(p, byte((i+1)&0xff))
	}
	return p, nil
}

// EntryStatBlobUS100 is the 2.24 native base-stat record. The matching
// expansion at 14571a4c0 reads eighteen status resistances and a float
// immediately after the one-byte base percentage.
type EntryStatBlobUS100 struct {
	HPMax, MPMax                             uint32
	Strength, Vitality, Intelligence, Spirit uint16 // wire = display × 10
	Fire, Water, Dark, Light                 int16
	Status                                   [18]int16
	InventoryLimit                           uint32
	HPRegen, MPRegen                         uint16
	MoveSpeed                                uint32
	AttackSpeed, CastSpeed                   uint16
	HitRecovery, JumpPower                   uint16
	Weight                                   uint32
	BasePercent                              byte
	Extra                                    float32
}

const EntryStatBlobUS100Size = 89

func (b EntryStatBlobUS100) Bytes() []byte {
	out := make([]byte, 0, EntryStatBlobUS100Size)
	out = add32(add32(out, b.HPMax), b.MPMax)
	for _, v := range []uint16{b.Strength, b.Vitality, b.Intelligence, b.Spirit} {
		out = add16(out, v)
	}
	for _, v := range []int16{b.Fire, b.Water, b.Dark, b.Light} {
		out = add16(out, uint16(v))
	}
	for _, v := range b.Status {
		out = add16(out, uint16(v))
	}
	out = add32(out, b.InventoryLimit)
	out = add16(add16(out, b.HPRegen), b.MPRegen)
	out = add32(out, b.MoveSpeed)
	out = add16(add16(out, b.AttackSpeed), b.CastSpeed)
	out = add16(add16(out, b.HitRecovery), b.JumpPower)
	out = add32(out, b.Weight)
	out = append(out, b.BasePercent)
	return add32(out, math.Float32bits(b.Extra))
}

// UserInfoAdditionStatsUS100 encodes a 2.24 mode-1 row with empty skill trees.
func UserInfoAdditionStatsUS100(actorID uint16, block EntryAdditionBlock, blob []byte) ([]byte, error) {
	return UserInfoAdditionStatsWithSkillsUS100(actorID, block, blob, [2][]EntrySkill{})
}

func UserInfoAdditionStatsWithSkillsUS100(actorID uint16, block EntryAdditionBlock, blob []byte, skills [2][]EntrySkill) ([]byte, error) {
	return UserInfoAdditionWithWornUS224(actorID, block, blob, skills, nil)
}

func UserInfoAdditionWithWornUS224(actorID uint16, block EntryAdditionBlock, blob []byte, skills [2][]EntrySkill, worn []ActorWornUS224) ([]byte, error) {
	if actorID == 0 || actorID == 0xffff {
		return nil, fmt.Errorf("invalid addition actor identity")
	}
	if len(blob) != EntryStatBlobUS100Size {
		return nil, fmt.Errorf("stat record is %d bytes, want %d", len(blob), EntryStatBlobUS100Size)
	}
	p := append(add16([]byte{1}, 1), 0, 0)
	p = append(p, block.bytes()...)
	p = add16(p, actorID)
	// 143803970 consumes both halves of experience. The next field is the
	// length read by 144e6eda0 at offset 38 from the start of the payload.
	p = binary.LittleEndian.AppendUint64(p, block.Experience)
	p = add32(p, uint32(len(blob)))
	p = append(p, blob...)
	p = append(p, 0) // actor status
	var err error
	p, err = appendActorWornUS224(p, worn)
	if err != nil {
		return nil, err
	}
	p = append(add16(p, 0), 0) // switching inventory ID and count
	p = add32(add32(p, 0), 0)
	p = append(p, 0xff) // unset selected skill tree
	for _, tree := range skills {
		var err error
		p, err = appendEntrySkillTree(p, tree)
		if err != nil {
			return nil, err
		}
	}
	p = append(p, 0, 0, 0, 0)   // creature, extended skills and flags
	p = add32(add32(p, 0), 0)   // empty u32 list and scalar
	return append(p, 0, 0), nil // final empty list and option byte
}

// EntryStatBlobUS100None is a record whose fields are all zero except the base
// percentage. The percentage is the gate the current-protocol client applies to
// the four dimensions and to current HP, so a record with only that field set
// asks "does the panel react to the gate at all" without claiming any stat
// value: the rows stay at zero if the gate is what blocks them.
func EntryStatBlobUS100None() []byte {
	return EntryStatBlobUS100{BasePercent: 100}.Bytes()
}

// UserInfoTimeSyncUS100 encodes the local player's time sync command (CMD 1959).
//
// The handler writes three secure-global pairs that the game's calendar/clock
// getters read: 0x149787ce0/ce4 = the server-time value carried by the packet,
// 0x149787ce8/cec = the play-time (a FILETIME-derived minute count), and
// 0x149787cf0/cf4 = the current tick. A getter (0x143a76820) returns
// decode(ce0) + (tick - decode(cf0)) * 0.001 — the in-game date/time, NOT the
// character's HP. Verified live: the packet value round-trips through the
// fields, and the getter's per-frame callers feed the calendar code
// (0x14607dc40, Gregorian year/month/day math). The display's HP comes from a
// different source that is still being located.
//
//	body = [u8 1][u32 value]   kind 1 (CMD), id 1959
func UserInfoTimeSyncUS100(value uint32) []byte {
	p := []byte{1}
	p = add32(p, value)
	return p
}

type EntrySkill struct {
	ID    uint16
	Level byte
}

// EntryAdditionProbe is the minimum current mode 1 layout with explicitly
// absent optional equipment/collections. It must follow a matching mode 0.
// Source base stats are connected to the detailed probe. Unknown fixed-prefix
// fields are experimental zeros; optional skill/equipment data remains pending.
type EntryAdditionProbe struct {
	ActorServerID uint16
	Context       [2]byte
	Experience    uint64
	Stats         PackedEntryStats
	SkillTrees    [2][]EntrySkill
}

func UserInfoAdditionProbe(s EntryAdditionProbe) ([]byte, error) {
	if s.ActorServerID == 0 || s.ActorServerID == 65535 {
		return nil, fmt.Errorf("invalid addition actor identity")
	}
	stats, e := s.Stats.Bytes()
	if e != nil {
		return nil, e
	}
	p := append(add16([]byte{1}, 1), s.Context[:]...)
	p = append(p, make([]byte, 250)...)
	p = add16(p, s.ActorServerID)
	p = add32(add32(p, uint32(s.Experience)), uint32(s.Experience>>32))
	p = append(add32(p, uint32(len(stats))), stats...)
	p = append(p, 0) // 14563d692
	// 14563d6cb -> 1452c1540 always consumes an equipment block, even
	// when empty: u8 rows, u32 scalar, u8 collection count, u64 flags.
	// Omitting these 14 bytes shifts switching inventory and skill trees.
	p = append(p, make([]byte, 14)...)
	p = append(add16(p, 0), 0) // 14563c1f0: switching-inventory ID + count
	p = add32(add32(p, 0), 0)  // 14563d6dd / 14563d717
	p = append(p, 0xff)        // native unset selected skill-tree byte
	// 14563d9aa increments the outer tree index; both self and other branches
	// consume TWO sets of count + skills + three pairs + five triples.
	for _, tree := range s.SkillTrees {
		p, e = appendEntrySkillTree(p, tree)
		if e != nil {
			return nil, e
		}
		p = append(p, make([]byte, 3*6+5*7)...)
	}
	p = append(p, 0, 0, 0, 0) // creature byte, extended skills count, two skill flags
	p = add32(add32(p, 0), 0) // 14563dba0 count, 14563dbf8 scalar
	p = append(p, 0, 0)       // 1456395a0 count, 14563dd04 byte
	return p, nil
}

func appendEntrySkillTree(p []byte, tree []EntrySkill) ([]byte, error) {
	if len(tree) > 255 {
		return nil, fmt.Errorf("skill tree exceeds native count")
	}
	p = append(p, byte(len(tree)))
	seen := map[uint16]bool{}
	for _, skill := range tree {
		if skill.ID == 0 || skill.Level == 0 || seen[skill.ID] {
			return nil, fmt.Errorf("invalid or duplicate skill")
		}
		seen[skill.ID] = true
		p = append(add16(p, skill.ID), skill.Level)
	}
	return p, nil
}
