package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestActiveEvents(t *testing.T) {
	p, err := ActiveEvents([]uint16{EventIDDanji})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x01, 0x00, // count = 1
		0x57, 0x10, // eventID = 0x1057 (4183)
		0x00,       // hasDetail = 0
	}
	if !bytes.Equal(p, want) {
		t.Fatalf("ActiveEvents got %x, want %x", p, want)
	}
}

func TestActiveEventsDetailed(t *testing.T) {
	p, err := ActiveEventsDetailed([]ActiveEventEntry{
		{
			EventID:        EventIDDanji,
			HasDetail:      true,
			EventType:      1,
			ColorType:      4,
			Title:          "DANJI",
			Subtitle:       "Sub",
			Description:    "Desc",
			StartTime:      1700000000,
			EndTime:        1701000000,
			Link:           "",
			Notice:         "",
			ShowOnCalendar: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Parse back strictly following DFO_unpacked.exe 0x143851af0 + 0x1438510e0.
	off := 0
	if got := binary.LittleEndian.Uint16(p[off : off+2]); got != 1 {
		t.Fatalf("count=%d, want 1", got)
	}
	off += 2
	if got := binary.LittleEndian.Uint16(p[off : off+2]); got != EventIDDanji {
		t.Fatalf("eventID=%d, want %d", got, EventIDDanji)
	}
	off += 2
	if p[off] != 1 || p[off+1] != 1 || p[off+2] != 4 {
		t.Fatalf("detail header=%v, want [1 1 4]", p[off:off+3])
	}
	off += 3
	readStr := func() string {
		n := int(binary.LittleEndian.Uint32(p[off : off+4]))
		off += 4
		s := string(p[off : off+n])
		off += n
		return s
	}
	if s1, s2, s3 := readStr(), readStr(), readStr(); s1 != "DANJI" || s2 != "Sub" || s3 != "Desc" {
		t.Fatalf("strings=(%q,%q,%q)", s1, s2, s3)
	}
	st := int32(binary.LittleEndian.Uint32(p[off : off+4]))
	off += 4
	et := int32(binary.LittleEndian.Uint32(p[off : off+4]))
	off += 4
	if st != 1700000000 || et != 1701000000 {
		t.Fatalf("times=(%d,%d)", st, et)
	}
	if s4, s5 := readStr(), readStr(); s4 != "" || s5 != "" {
		t.Fatalf("tail strings=(%q,%q)", s4, s5)
	}
	if p[off] != 1 {
		t.Fatalf("showOnCalendar=%d, want 1", p[off])
	}
	off++
	if off != len(p)-1 || p[off] != 0 {
		t.Fatalf("trailing paramCount: off=%d len=%d val=%x", off, len(p), p[off:])
	}
}

func TestDanjiStatus(t *testing.T) {
	p := DanjiStatus(236, 0, 2)
	if len(p) != 6 {
		t.Fatalf("DanjiStatus len=%d, want 6", len(p))
	}
	if got := binary.LittleEndian.Uint32(p[0:4]); got != 236 {
		t.Fatalf("stack=%d, want 236", got)
	}
	if p[4] != 0 || p[5] != 2 {
		t.Fatalf("flag/anim=%d/%d, want 0/2", p[4], p[5])
	}
	// Clamping animLevel 0 -> 1 and 9 -> 5
	if DanjiStatus(0, 0, 0)[5] != 1 {
		t.Fatalf("expected animLevel 0 to clamp to 1")
	}
	if DanjiStatus(900, 0, 9)[5] != 5 {
		t.Fatalf("expected animLevel 9 to clamp to 5")
	}
}

func TestDecodeDanjiOpenAndAck(t *testing.T) {
	raw := make([]byte, 24)
	// Native client leaves uninitialized stack bytes in [0..12]; decoder must tolerate them.
	for i := 0; i < 13; i++ {
		raw[i] = byte(0xaa + i)
	}
	binary.LittleEndian.PutUint32(raw[13:17], 122)
	binary.LittleEndian.PutUint32(raw[17:21], 10)
	raw[21] = 3

	req, err := DecodeDanjiOpen(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.Slot != 122 || req.Count != 10 || req.Tier != 3 {
		t.Fatalf("unexpected decoded request: %+v", req)
	}

	ack, err := DanjiOpenSuccess([]DanjiReward{
		{Template: 7740, Grade: DanjiGradeB, Count: 10},
		{Template: 590704452, Grade: DanjiGradeS, Count: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ack) != 1+4+2*6 {
		t.Fatalf("ack len=%d, want 17", len(ack))
	}
	if ack[0] != 1 || binary.LittleEndian.Uint32(ack[1:5]) != 2 {
		t.Fatalf("unexpected ack header: %x", ack[:5])
	}
	if binary.LittleEndian.Uint32(ack[5:9]) != 7740 || ack[9] != DanjiGradeB || ack[10] != 10 {
		t.Fatalf("unexpected first reward: %x", ack[5:11])
	}
	if binary.LittleEndian.Uint32(ack[11:15]) != 590704452 || ack[15] != DanjiGradeS || ack[16] != 1 {
		t.Fatalf("unexpected second reward: %x", ack[11:17])
	}
}

func TestUS224DanjiInventory(t *testing.T) {
	p, err := US224DanjiInventory([][CurrentItemRecordSize]byte{
		OrdinaryItem(0, 590004385, 200),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 3+0xb1 {
		t.Fatalf("len=%d, want %d", len(p), 3+0xb1)
	}
	if p[0] != DanjiInventorySpace || binary.LittleEndian.Uint16(p[1:3]) != 1 {
		t.Fatalf("unexpected header: %x", p[:3])
	}
	if binary.LittleEndian.Uint16(p[3:5]) != 0 || binary.LittleEndian.Uint32(p[5:9]) != 590004385 || binary.LittleEndian.Uint32(p[9:13]) != 200 {
		t.Fatalf("unexpected item row: %x", p[3:13])
	}
}
