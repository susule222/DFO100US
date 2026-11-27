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
		{Template: 7740, Count: 10},
		{Template: 590704452, Count: 1},
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
	if binary.LittleEndian.Uint32(ack[5:9]) != 7740 || binary.LittleEndian.Uint16(ack[9:11]) != 10 {
		t.Fatalf("unexpected first reward: %x", ack[5:11])
	}
	if binary.LittleEndian.Uint32(ack[11:15]) != 590704452 || binary.LittleEndian.Uint16(ack[15:17]) != 1 {
		t.Fatalf("unexpected second reward: %x", ack[11:17])
	}
}
