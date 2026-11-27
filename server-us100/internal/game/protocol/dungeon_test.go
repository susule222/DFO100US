package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestDecodeMoveDungeonRoom(t *testing.T) {
	// First verified CMD45 from the 100US Mirkwood east exit.
	const capture = "0101eb030000c4000000003d900e00000000000000000000000000000000000000000000" +
		"00000000000000000000000000000000000000000000000000b2009c000000eb030000c4" +
		"000000000000000000000000000000000000000000000000000000000000000000000000" +
		"000000000000000000000000000000000000000000000002000000000000000000000000"
	body, err := hex.DecodeString(capture)
	if err != nil {
		t.Fatal(err)
	}
	legacy := make([]byte, 160)
	legacy[0], legacy[1] = 1, 1
	for _, p := range [][]byte{body, legacy} {
		if xy, err := DecodeMoveDungeonRoom(p); err != nil || xy != [2]byte{1, 1} {
			t.Fatalf("move with %d bytes: xy=%v err=%v", len(p), xy, err)
		}
	}
	body[10] = 2
	legacy[155] = 1
	for _, p := range [][]byte{body, legacy, make([]byte, 143), make([]byte, 145)} {
		if _, err := DecodeMoveDungeonRoom(p); err == nil {
			t.Fatalf("accepted invalid %d-byte room move", len(p))
		}
	}
}

func TestDungeonGateNativeCursor(t *testing.T) {
	b, e := os.ReadFile("testdata/native_dungeon_gate_cursor.json")
	if e != nil {
		t.Fatal(e)
	}
	var native struct {
		Payload  string                       `json:"payload_hex"`
		Consumed int                          `json:"consumed"`
		Reads    []struct{ Offset, Size int } `json:"reads"`
	}
	if e = json.Unmarshal(b, &native); e != nil {
		t.Fatal(e)
	}
	want, e := hex.DecodeString(native.Payload)
	if e != nil {
		t.Fatal(e)
	}
	got := EnterDungeonSelection()
	if !bytes.Equal(got, want) || len(got) != native.Consumed {
		t.Fatalf("native cursor mismatch: %x", got)
	}
	for _, p := range [][]byte{nil, make([]byte, 7), make([]byte, 9), {0, 0, 0, 0, 1, 0, 0, 0}} {
		if _, e = DecodeDungeonGate(p); e == nil {
			t.Fatalf("accepted invalid gate %x", p)
		}
	}
	if id, e := DecodeDungeonGate(make([]byte, 8)); e != nil || id != 0 {
		t.Fatalf("live19 gate: %d %v", id, e)
	}
}

func TestStartMapUS100RoomInitialisation(t *testing.T) {
	s := StartMapState{Position: [2]byte{0, 1}, Seed: 12345, Map: 76121}
	const header = "000100393000000000010000000159290100"
	const trailer = "00000000"
	// The flag at offset 13 selects a map and a counted list. The native
	// loop at 0x1434e2d50..30ce skips all 25 row bytes when the count is zero.
	body, err := StartMapUS100(s)
	if err != nil || hex.EncodeToString(body) != header+"00"+trailer {
		t.Fatalf("empty room: %x, %v", body, err)
	}
	s.Monsters = []DungeonMonster{
		{SourceIndex: 7, Entity: 0x1002, Template: 0x12345678, Level: 3, Rank: 1, Team: 100},
		{SourceIndex: 9, Entity: 0x1003, Template: 0x22334455, Level: 5, Team: 0},
	}
	const first = "0000000000" + "07000000" + "0210" + "78563412" + "03010000ff" + "64000000" + "00"
	const second = "0000000000" + "09000000" + "0310" + "55443322" + "05000000ff" + "00000000" + "00"
	body, err = StartMapUS100(s)
	if err != nil || hex.EncodeToString(body) != header+"02"+first+second+trailer {
		t.Fatalf("populated room: %x, %v", body, err)
	}
	s.Monsters[1].Entity = s.Monsters[0].Entity
	if _, err := StartMapUS100(s); err == nil {
		t.Fatal("accepted duplicate monster entities")
	}
	if _, err := StartMapUS100(StartMapState{Map: 0}); err == nil {
		t.Fatal("accepted an empty map")
	}
}

func TestDungeonEntryNativeCursors(t *testing.T) {
	// start_map keeps its byte-exact comparison: that encoder is untouched.
	start, e := StartMap(StartMapState{Position: [2]byte{0, 1}, Seed: 12345, Map: 76121, Monsters: []DungeonMonster{{Entity: 1, SourceIndex: 0, Level: 3, Rank: 3, Team: 100, Template: 109014870}}})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("testdata/native_start_map_cursor.json")
	if e != nil {
		t.Fatal(e)
	}
	var native struct {
		Payload  string `json:"payload_hex"`
		Consumed int    `json:"consumed"`
	}
	if e = json.Unmarshal(b, &native); e != nil {
		t.Fatal(e)
	}
	want, e := hex.DecodeString(native.Payload)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(start, want) || len(start) != native.Consumed {
		t.Fatalf("start_map cursor mismatch\ngot %x\nwant%x", start, want)
	}

	if got := DungeonSelectAckUS100(); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("CMD16 success: got %x, want 01", got)
	}
	info := DungeonInfo(DungeonInfoState{ID: 3, Maze: 1, Boss: [2]byte{3, 0}})
	cursor, e := os.ReadFile("testdata/native_dungeon_info_cursor.json")
	if e != nil {
		t.Fatal(e)
	}
	var chain struct {
		Payload  string                       `json:"payload_hex"`
		Consumed int                          `json:"consumed"`
		Reads    []struct{ Offset, Size int } `json:"reads"`
	}
	if e = json.Unmarshal(cursor, &chain); e != nil {
		t.Fatal(e)
	}
	want, e = hex.DecodeString(chain.Payload)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(info, want) {
		t.Fatalf("US100 NOTI28: got %x, want %x", info, want)
	}
	offset := 0
	for _, read := range chain.Reads {
		if read.Offset != offset {
			t.Fatalf("US100 NOTI28 cursor: got offset %d, want %d", read.Offset, offset)
		}
		offset += read.Size
	}
	if offset != chain.Consumed || offset != len(info) {
		t.Fatalf("US100 NOTI28 cursor ends at %d, fixture %d, encoded %d", offset, chain.Consumed, len(info))
	}

	live, _ := hex.DecodeString("030000000000000000ffff0000000000490c0000000000000000000000000000")
	r, e := DecodeDungeonSelection(live)
	if e != nil || r.ID != 3 || r.Difficulty != 0 || r.Extra16 != 0xffff {
		t.Fatalf("live20 selection: %+v %v", r, e)
	}
	live[31] = 1
	if _, e = DecodeDungeonSelection(live); e == nil {
		t.Fatal("accepted nonzero padding")
	}
	// Stage3 live bodies (90 US reference: flow-dungeon_enter.parse_0010_plain
	// evidence). 17 bytes, extra16 = 0xffff, and the helper byte is 0x00 on the
	// ordinary pages and 0x01 on the special one.
	for _, c := range []struct {
		hex        string
		id         uint32
		difficulty byte
		sso        byte
	}{
		{"900000000000000000ffff000000000000", 0x90, 0, 0x00},
		{"881300000000000001ffff000000000000", 5000, 0, 0x01},
		{"d90700000100000000ffff000000000000", 2009, 1, 0x00},
	} {
		body, e := hex.DecodeString(c.hex)
		if e != nil {
			t.Fatal(e)
		}
		if len(body) != 17 {
			t.Fatalf("stage3 fixture %s is %d bytes, not 17", c.hex, len(body))
		}
		got, e := DecodeDungeonSelection(body)
		if e != nil {
			t.Fatalf("stage3 %s: %v", c.hex, e)
		}
		if got.ID != c.id || got.Difficulty != c.difficulty || got.SSO != c.sso || got.Extra16 != 0xffff {
			t.Fatalf("stage3 %s decoded %+v", c.hex, got)
		}
	}
}

func TestSourceTeamsReachNativeSpawnReader(t *testing.T) {
	p, e := StartMap(StartMapState{Position: [2]byte{0, 1}, Seed: 12345, Map: 76121, Monsters: []DungeonMonster{
		{Entity: 1, SourceIndex: 0, Level: 3, Rank: 3, Team: 100, Template: 109014870},
		{Entity: 2, SourceIndex: 1, Level: 3, Team: 0, Template: 109014870, NonCombat: true},
	}})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("testdata/native_start_map_teams.json")
	if e != nil {
		t.Fatal(e)
	}
	var native struct {
		Payload  string `json:"payload_hex"`
		Consumed int    `json:"consumed"`
		Spawns   []struct{ Team uint32 }
	}
	if e = json.Unmarshal(b, &native); e != nil {
		t.Fatal(e)
	}
	want, e := hex.DecodeString(native.Payload)
	if e != nil || !bytes.Equal(p, want) || len(p) != native.Consumed || len(native.Spawns) != 2 || native.Spawns[0].Team != 100 || native.Spawns[1].Team != 0 {
		t.Fatal("source teams do not match native reader")
	}
}
