package main

import (
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"
)

func TestNameHistoryPayloadDefaultAndStored(t *testing.T) {
	now := time.Unix(1795773578, 0)
	role := storage.Character{
		ID:        48,
		WireID:    10,
		Name:      "2",
		CreatedAt: time.Unix(1795600000, 0),
		State:     json.RawMessage(`{"level":15}`),
	}
	p, err := nameHistoryPayload(role, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	// mode=5, outer=1, flag=0, actor=10, count=1, len=1, "2", server=1, ts=1795600000
	if len(p) != 7+4+1+1+4 {
		t.Fatalf("unexpected default payload len=%d hex=%x", len(p), p)
	}
	if p[0] != 5 || binary.LittleEndian.Uint16(p[4:6]) != 10 || p[6] != 1 {
		t.Fatalf("bad header: %x", p[:7])
	}
	if binary.LittleEndian.Uint32(p[7:11]) != 1 || p[11] != '2' || p[12] != 1 || binary.LittleEndian.Uint32(p[13:17]) != 1795600000 {
		t.Fatalf("bad default entry: %x", p[7:])
	}

	role.State = json.RawMessage(`{"name_history":[{"name":"OldName","server":2,"timestamp":1795000000},{"name":"2","server":0,"timestamp":1795600000}]}`)
	p2, err := nameHistoryPayload(role, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if p2[6] != 2 {
		t.Fatalf("expected 2 stored history entries, got %d", p2[6])
	}
	// First row: "OldName" (7B), server 2, ts 1795000000.
	if string(p2[11:18]) != "OldName" || p2[18] != 2 || binary.LittleEndian.Uint32(p2[19:23]) != 1795000000 {
		t.Fatalf("bad first stored row: %x", p2[7:23])
	}
	// Second row: "2" (1B), fallback server 3, ts 1795600000.
	if string(p2[27:28]) != "2" || p2[28] != 3 || binary.LittleEndian.Uint32(p2[29:33]) != 1795600000 {
		t.Fatalf("bad second stored row: %x", p2[23:])
	}
}
