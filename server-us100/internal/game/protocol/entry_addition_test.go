package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestPackedStatsAgainstNativeExpansion(t *testing.T) {
	raw, e := os.ReadFile("testdata/native_packed91.json")
	if e != nil {
		t.Fatal(e)
	}
	var vectors []struct {
		WireHex string   `json:"wire_hex"`
		Words   []uint32 `json:"expanded_words"`
	}
	if e = json.Unmarshal(raw, &vectors); e != nil {
		t.Fatal(e)
	}
	for _, v := range vectors {
		if len(v.Words) != 40 {
			t.Fatal("native vector field count")
		}
		i := 0
		next := func() uint32 { x := v.Words[i]; i++; return x }
		s := PackedEntryStats{HP: next(), MP: next()}
		for j := range s.Core {
			s.Core[j] = uint16(next())
		}
		for j := range s.Element {
			s.Element[j] = int16(next())
		}
		for j := range s.Status {
			s.Status[j] = int16(next())
		}
		s.Inventory = int32(next())
		for j := range s.Regeneration {
			s.Regeneration[j] = int16(next())
		}
		s.Movement = next()
		for j := range s.AttackCasting {
			s.AttackCasting[j] = uint16(next())
		}
		for j := range s.RecoveryJump {
			s.RecoveryJump[j] = int16(next())
		}
		s.Weight = int32(next())
		s.BasePercent = byte(next())
		s.Extra = math.Float32frombits(next())
		got, e := s.Bytes()
		if e != nil {
			t.Fatal(e)
		}
		want, e := hex.DecodeString(v.WireHex)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("native expansion reconstruction mismatch: %x", got)
		}
	}
}

func TestAdditionV224Layout(t *testing.T) {
	// The mode-1 entry block is a mixed 23-byte struct, not an appearance list:
	// every reference to its global in .text is inside the entry parser, and they
	// read u32@0, u64@4, u8@12/13/14/15/16, i8@17/18 and u32@19 — which fills the
	// block exactly. This test pins that field map and the tail that follows it.
	// +0 and +4 are the two big slots (level+experience, or the reference hp/mp
	// pair — see the type's comment); the remaining fields are unnamed.
	block := EntryAdditionBlock{
		Level:      42,
		Experience: 0x1122334455667788,
		Flag13:     1,
		Value15:    7,
		Signed17:   -3,
		Value19:    0xdeadbeef,
	}
	p, e := UserInfoAdditionUS100(3, block)
	if e != nil {
		t.Fatal(e)
	}
	if len(p) != 5+23+2+15 {
		t.Fatalf("addition size=%d want=45", len(p))
	}
	if p[0] != 1 || binary.LittleEndian.Uint16(p[1:]) != 1 {
		t.Fatal("mode/count header")
	}
	body := p[5:28]
	if binary.LittleEndian.Uint32(body[0:]) != 42 {
		t.Fatalf("level at block+0 = %#x", binary.LittleEndian.Uint32(body[0:]))
	}
	if binary.LittleEndian.Uint64(body[4:]) != 0x1122334455667788 {
		t.Fatalf("experience at block+4 = %#x", binary.LittleEndian.Uint64(body[4:]))
	}
	if body[12] != 0 || body[13] != 1 || body[14] != 0 {
		t.Fatalf("flags = %v, want 0/1/0", body[12:15])
	}
	if body[15] != 7 || body[16] != 0 {
		t.Fatalf("values 15/16 = %v", body[15:17])
	}
	if int8(body[17]) != -3 || int8(body[18]) != 0 {
		t.Fatalf("signed pair = %d/%d", int8(body[17]), int8(body[18]))
	}
	if binary.LittleEndian.Uint32(body[19:]) != 0xdeadbeef {
		t.Fatalf("value19 = %#x", binary.LittleEndian.Uint32(body[19:]))
	}
	if binary.LittleEndian.Uint16(p[28:]) != 3 {
		t.Fatal("actor id offset")
	}
	if _, e := UserInfoAdditionUS100(0, block); e == nil {
		t.Fatal("accepted an invalid actor")
	}
	// The parser keeps reading past the fixed tail once the key matches, so the
	// padded body must cover the region and keep every known field where it was.
	padded, e := UserInfoAdditionPaddedUS100(3, block, EntryAdditionBodyMinimum)
	if e != nil {
		t.Fatal(e)
	}
	if len(padded) != EntryAdditionBodyMinimum {
		t.Fatalf("padded size=%d want=%d", len(padded), EntryAdditionBodyMinimum)
	}
	if !bytes.Equal(padded[:len(p)], p) {
		t.Fatal("padding changed the known part of the body")
	}
	for i, b := range padded[len(p):] {
		if b != 0 {
			t.Fatalf("padding byte %d = %#x", i, b)
		}
	}
	if _, e := UserInfoAdditionPaddedUS100(3, block, 8192); e == nil {
		t.Fatal("accepted an out-of-range pad")
	}
}

func TestAdditionConsumesBothNativeSkillTrees(t *testing.T) {
	s := EntryAdditionProbe{ActorServerID: 3, Experience: 0x1122334455667788, Stats: PackedEntryStats{HP: 5200, MP: 4800, BasePercent: 100}, SkillTrees: [2][]EntrySkill{{{ID: 179, Level: 7}}, {{ID: 174, Level: 1}, {ID: 169, Level: 1}}}}
	p, e := UserInfoAdditionProbe(s)
	if e != nil {
		t.Fatal(e)
	}
	if len(p) != 518 {
		t.Fatalf("addition size %d", len(p))
	}
	fixture, e := os.ReadFile("testdata/native_addition_cursor.json")
	if e != nil {
		t.Fatal(e)
	}
	var native struct {
		Payload  string `json:"payload_hex"`
		Consumed int    `json:"consumed"`
	}
	if e = json.Unmarshal(fixture, &native); e != nil {
		t.Fatal(e)
	}
	want, e := hex.DecodeString(native.Payload)
	if e != nil || !bytes.Equal(p, want) || native.Consumed != len(p) {
		t.Fatal("packet differs from independent native cursor fixture")
	}
	if binary.LittleEndian.Uint16(p[255:]) != 3 || binary.LittleEndian.Uint64(p[257:]) != s.Experience || binary.LittleEndian.Uint32(p[265:]) != 91 {
		t.Fatal("prefix/identity/experience/stats boundary")
	}
	if !bytes.Equal(p[386:391], []byte{255, 1, 179, 0, 7}) {
		t.Fatal("first skill tree boundary")
	}
	if !bytes.Equal(p[444:451], []byte{2, 174, 0, 1, 169, 0, 1}) {
		t.Fatal("second skill tree boundary")
	}
	s.SkillTrees[1] = append(s.SkillTrees[1], s.SkillTrees[1][0])
	if _, e = UserInfoAdditionProbe(s); e == nil {
		t.Fatal("duplicate skill accepted")
	}
	s.Stats.BasePercent = 0
	if _, e = s.Stats.Bytes(); e == nil {
		t.Fatal("zero base percentage accepted")
	}
}

func TestStatBlobV224Layout(t *testing.T) {
	blob := EntryStatBlobUS100{
		HPMax:        1800,
		MPMax:        1400,
		Strength:     110,
		Vitality:     220,
		Intelligence: 330,
		Spirit:       440,
		MoveSpeed:    850,
		Weight:       50000,
		BasePercent:  100,
		Extra:        1,
	}
	raw := blob.Bytes()
	if len(raw) != 89 {
		t.Fatalf("record is %d bytes, want %d", len(raw), EntryStatBlobUS100Size)
	}
	if binary.LittleEndian.Uint32(raw[0:]) != 1800 || binary.LittleEndian.Uint32(raw[4:]) != 1400 {
		t.Fatalf("hp/mp = %d/%d", binary.LittleEndian.Uint32(raw[0:]), binary.LittleEndian.Uint32(raw[4:]))
	}
	for idx, want := range []uint16{110, 220, 330, 440} {
		if got := binary.LittleEndian.Uint16(raw[8+idx*2:]); got != want {
			t.Fatalf("core attribute %d = %d, want %d", idx, got, want)
		}
	}
	if raw[0x54] != 100 {
		t.Fatalf("base percentage at record+0x54 = %d, want 100", raw[0x54])
	}

	if binary.LittleEndian.Uint32(raw[0x55:]) != math.Float32bits(1) {
		t.Fatal("misaligned trailing float")
	}
	block := EntryAdditionBlock{Level: 7, Experience: 1<<32 + 13}
	p, e := UserInfoAdditionStatsUS100(3, block, raw)
	if e != nil {
		t.Fatal(e)
	}
	const recordEnd = 42 + 89
	if len(p) != recordEnd+43 || !bytes.Equal(p[:5], []byte{1, 1, 0, 0, 0}) {
		t.Fatalf("unexpected mode-1 envelope or length: %d bytes", len(p))
	}
	if binary.LittleEndian.Uint16(p[28:]) != 3 || binary.LittleEndian.Uint64(p[30:]) != block.Experience {
		t.Fatal("actor key or experience pair misplaced")
	}
	if binary.LittleEndian.Uint32(p[38:]) != 89 || !bytes.Equal(p[42:recordEnd], raw) {
		t.Fatal("misaligned length-prefixed base stats")
	}
	wantTail := make([]byte, 43)
	wantTail[26] = 0xff
	if !bytes.Equal(p[recordEnd:], wantTail) {
		t.Fatalf("empty collections not aligned: %x", p[recordEnd:])
	}
	skills := [2][]EntrySkill{{{ID: 0x1234, Level: 2}}, {{ID: 0x4321, Level: 3}}}
	withSkills, e := UserInfoAdditionStatsWithSkillsUS100(3, block, raw, skills)
	if e != nil {
		t.Fatal(e)
	}
	want := append([]byte(nil), p[:recordEnd+27]...)
	want = append(want, 1, 0x34, 0x12, 2, 1, 0x21, 0x43, 3)
	want = append(want, wantTail[29:]...)
	if !bytes.Equal(withSkills, want) {
		t.Fatal("incorrect 2.24 skill trees or fields after the trees")
	}
	if _, e = UserInfoAdditionStatsUS100(3, EntryAdditionBlock{}, raw[:80]); e == nil {
		t.Fatal("short record accepted")
	}
}
