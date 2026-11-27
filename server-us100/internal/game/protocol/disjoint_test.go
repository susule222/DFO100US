package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestDecodeDisjointLiveCapture(t *testing.T) {
	// Live US100 capture from runtime/interactive/events.jsonl (2026-11-27T03:10:10Z).
	raw, err := hex.DecodeString("00ffff060c00c25d00000d00224e00000f00f25500000a00234e00000b00a27c00000e00c65d00000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	req, err := DecodeDisjoint(raw)
	if err != nil {
		t.Fatalf("DecodeDisjoint failed: %v", err)
	}
	if req.Space != 0 || req.DisassemblerID != 0xffff || len(req.Items) != 6 {
		t.Fatalf("unexpected header: %+v", req)
	}
	want := []DisjointItem{
		{Slot: 12, Template: 24002},
		{Slot: 13, Template: 20002},
		{Slot: 15, Template: 22002},
		{Slot: 10, Template: 20003},
		{Slot: 11, Template: 31906},
		{Slot: 14, Template: 24006},
	}
	for i, w := range want {
		if req.Items[i] != w {
			t.Fatalf("item[%d] = %+v, want %+v", i, req.Items[i], w)
		}
	}
}

func TestDecodeDisjointRejectsInvalid(t *testing.T) {
	cases := [][]byte{
		{},
		{0, 0xff, 0xff, 0},
		{2, 0xff, 0xff, 1, 10, 0, 1, 0, 0, 0},
		{0, 0xff, 0xff, 1, 10, 0, 0, 0, 0, 0},                               // zero template
		{0, 0xff, 0xff, 2, 10, 0, 1, 0, 0, 0, 10, 0, 2, 0, 0, 0},            // duplicate slot
		{0, 0xff, 0xff, 1, 10, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0},             // non-zero padding
		{0, 0xff, 0xff, 1, 0, 0, 1, 0, 0, 0},                                // slot 0 in space 0
	}
	for i, tc := range cases {
		if _, err := DecodeDisjoint(tc); err == nil {
			t.Fatalf("case %d unexpectedly succeeded", i)
		}
	}
}

func TestDisjointSuccessLayout(t *testing.T) {
	req := DisjointRequest{
		Space:          0,
		DisassemblerID: 0xffff,
		Items: []DisjointItem{
			{Slot: 10, Template: 20003},
			{Slot: 12, Template: 24002},
		},
	}
	rewards := []DisjointReward{
		{Slot: 121, Template: 3037, Count: 15},
		{Slot: 122, Template: 3035, Count: 3},
	}
	got, err := DisjointSuccess(req, rewards)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		1,          // result = ok
		2,          // disjointed count
		10, 0,      // slot 10
		12, 0,      // slot 12
		0,          // space 0
		0xff, 0xff, // disassembler_id = 0xffff
		2,                      // reward count
		121, 0, 0xdd, 0x0b, 0, 0, 15, 0, 0, 0, // slot 121, 3037, +15
		122, 0, 0xdb, 0x0b, 0, 0, 3, 0, 0, 0, // slot 122, 3035, +3
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DisjointSuccess mismatch:\n got: %x\nwant: %x", got, want)
	}
	if !bytes.Equal(DisjointRefused(22), []byte{0, 22, 0}) {
		t.Fatalf("DisjointRefused(22) = %x", DisjointRefused(22))
	}
}
