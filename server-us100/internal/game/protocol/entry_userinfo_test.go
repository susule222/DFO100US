package protocol

import (
	"encoding/binary"
	"testing"
)

// Offsets here are counted independently from the native reader sequence in
// docs/protocol/entry-userinfo.md, not by decoding through the serializer.
//
// The two client generations differ in the fixed block that precedes the actor
// id: 2.38 reads 160 bytes, US100 (2.24) reads 127. Native row reader
// 0x143807f80 reads the block first and the actor id second, and the 160-byte
// variation drove the id to 0 in this build, so the crash at DFO.exe+0x4F1F2A0
// was an enter-world lookup of the selected actor id failing. These offsets are
// the 2.24 layout.
// Measured offsets for the 2.24 layout:
// fame at 124 (fixed block + 0x77), actor id at 132, name length at 134,
// name at 138, profession at name+0, packed grow at name+1, level at name+2.
const (
	fameOffsetV224 = 5 + fixedRowBlockFameOffset
	actorIDV224    = 132
	nameLenV224    = 134
	nameTextV224   = 138
	// Exact US100 (2.24) payload length excluding the name and equipment rows,
	// verified against readUserInfoMinimum (0x143807f80..0x143809e00).
	payloadWithoutNameV224 = 261
)

func TestEntryBasicNativeBoundaries(t *testing.T) {
	for _, name := range []string{"LanTest01", "角色一"} {
		p, err := UserInfoBasicProbe(EntryBasicProbe{
			ActorServerID: 503,
			Context:       [2]byte{4, 7},
			Fame:          777,
			Character:     CharacterRow{Name: name, Profession: 3, Level: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := payloadWithoutNameV224 + len(name); len(p) != want {
			t.Fatalf("minimum actor size=%d, want=%d", len(p), want)
		}
		if p[0] != 0 || binary.LittleEndian.Uint16(p[1:]) != 1 || p[3] != 4 || p[4] != 7 {
			t.Fatal("actor notification header")
		}
		if got := binary.LittleEndian.Uint32(p[fameOffsetV224:]); got != 777 {
			t.Fatalf("fame=%d want=777", got)
		}
		if binary.LittleEndian.Uint16(p[actorIDV224:]) != 503 ||
			binary.LittleEndian.Uint32(p[nameLenV224:]) != uint32(len(name)) ||
			string(p[nameTextV224:nameTextV224+len(name)]) != name {
			t.Fatal("fixed prefix shifted actor or UTF-8 name")
		}
		after := nameTextV224 + len(name)
		// Native 0x1438081cf..0x14380820d reads u8 job, u8 packed grow, u8 level.
		if p[after] != 3 || p[after+1] != 0 || p[after+2] != 1 {
			t.Fatalf("job/grow/level bytes = %d,%d,%d", p[after], p[after+1], p[after+2])
		}
		if packed, err := UserInfoBasicProbe(EntryBasicProbe{
			ActorServerID: 503,
			Context:       [2]byte{4, 7},
			Character:     CharacterRow{Name: name, Profession: 3, Advancement: 2, Level: 1},
		}); err != nil {
			t.Fatal(err)
		} else if packed[after] != 3 || packed[after+1] != 2 {
			t.Fatalf("job/grow = %#x/%#x want 0x3/0x2", packed[after], packed[after+1])
		}
		// Exact US100 (2.24) native tail sentinels:
		//   198+len(name): 0x1438086da ([tmp+0x370]=1)
		//   233+len(name): 0x14380ace0 ([tmp+0x371]=1)
		//   247+len(name): 0x143804240 (unset sentinel 0xff)
		if p[198+len(name)] != 1 || p[233+len(name)] != 1 || p[247+len(name)] != 0xff {
			t.Fatalf("tail sentinels = %#x,%#x,%#x", p[198+len(name)], p[233+len(name)], p[247+len(name)])
		}
	}
}

func TestUS224TownAppearanceEncoding(t *testing.T) {
	eq := []Equipment{{Slot: 0, Item: 39165}, {Slot: 3, Item: 40813}, {Slot: 12, Item: 100001}}
	p, err := UserInfoBasicProbe(EntryBasicProbe{
		ActorServerID: 10,
		Fame:          777,
		Character:     CharacterRow{Name: "2", Profession: 0, Level: 15, Equipment: eq},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLen := payloadWithoutNameV224 + 1 + len(eq)*mode0AppearanceRowBytes
	if len(p) != wantLen {
		t.Fatalf("len=%d want=%d", len(p), wantLen)
	}
	eqStart := nameTextV224 + 1 + 5
	if p[eqStart] != byte(len(eq)) {
		t.Fatalf("equip count=%d want=%d", p[eqStart], len(eq))
	}
	for i, want := range eq {
		row := p[eqStart+1+i*mode0AppearanceRowBytes : eqStart+1+(i+1)*mode0AppearanceRowBytes]
		if row[0] != want.Slot || binary.LittleEndian.Uint32(row[1:5]) != want.Item || binary.LittleEndian.Uint32(row[5:9]) != 0 {
			t.Fatalf("row %d mismatch: slot=%d item=%d dyeLen=%d", i, row[0], binary.LittleEndian.Uint32(row[1:5]), binary.LittleEndian.Uint32(row[5:9]))
		}
	}
	shift := len(eq) * mode0AppearanceRowBytes
	if p[198+1+shift] != 1 || p[233+1+shift] != 1 || p[247+1+shift] != 0xff {
		t.Fatal("tail sentinels misaligned after appearance rows")
	}
}

func TestUS224RejectInvalidTownAppearance(t *testing.T) {
	for _, eq := range [][]Equipment{
		{{Slot: 36, Item: 39165}},
		{{Slot: 0, Item: 0}},
		{{Slot: 0, Item: 39165}, {Slot: 0, Item: 40813}},
	} {
		if _, err := UserInfoBasicProbe(EntryBasicProbe{
			ActorServerID: 10,
			Character:     CharacterRow{Name: "2", Level: 1, Equipment: eq},
		}); err == nil {
			t.Fatalf("accepted invalid appearance %+v", eq)
		}
	}
}

func TestAdventurerFameUpdate(t *testing.T) {
	p, err := AdventurerFameUpdate(91, 777)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 8 || binary.LittleEndian.Uint16(p[0:2]) != 91 || binary.LittleEndian.Uint16(p[2:4]) != 0 || binary.LittleEndian.Uint32(p[4:8]) != 777 {
		t.Fatalf("unexpected fame packet: %x", p)
	}
	for _, bad := range []uint16{0, 0xffff} {
		if _, err := AdventurerFameUpdate(bad, 100); err == nil {
			t.Fatalf("accepted invalid actor %d", bad)
		}
	}
}

func TestEntryBasicRejectsInvalidActor(t *testing.T) {
	for _, id := range []uint16{0, 0xffff} {
		if _, err := UserInfoBasicProbe(EntryBasicProbe{ActorServerID: id, Character: CharacterRow{Name: "LanTest01", Level: 1}}); err == nil {
			t.Fatal("accepted reserved actor ID")
		}
	}
}

func TestWithUserInfoContext(t *testing.T) {
	ctx := [2]byte{1, 20}
	for _, tc := range []struct {
		name string
		in   []byte
		off  int
	}{
		{"mode0", []byte{0, 1, 0, 0, 0, 0xaa}, 3},
		{"mode1", []byte{1, 1, 0, 0, 0, 0xbb}, 3},
		{"mode2", []byte{2, 0, 0, 0x10, 0x00}, 1},
		{"mode6", []byte{6, 1, 0, 0, 0, 0xcc}, 3},
	} {
		orig := append([]byte(nil), tc.in...)
		got := WithUserInfoContext(tc.in, ctx)
		if got[tc.off] != ctx[0] || got[tc.off+1] != ctx[1] {
			t.Fatalf("%s: context at [%d:%d] = %v, want %v", tc.name, tc.off, tc.off+2, got[tc.off:tc.off+2], ctx)
		}
		if tc.in[tc.off] != orig[tc.off] || tc.in[tc.off+1] != orig[tc.off+1] {
			t.Fatalf("%s: mutated input slice", tc.name)
		}
	}
}
