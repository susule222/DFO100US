package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestDecodeSortItemsLiveCapture(t *testing.T) {
	// Exact live-captured CMD 20 from interactive/events.jsonl (11:39:01Z):
	// space=0, tab=3 (Materials), count=2, pairs=[(125->133), (133->125)]
	raw, err := hex.DecodeString("000302007d00850085007d0000000000")
	if err != nil {
		t.Fatal(err)
	}
	req, err := DecodeSortItems(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.Space != 0 || req.Tab != 3 || len(req.Pairs) != 2 {
		t.Fatalf("unexpected request: %+v", req)
	}
	if req.Pairs[0] != (SortItemPair{From: 125, To: 133}) || req.Pairs[1] != (SortItemPair{From: 133, To: 125}) {
		t.Fatalf("unexpected pairs: %+v", req.Pairs)
	}
}

func TestSortItemsAckEncoding(t *testing.T) {
	ack := SortItemsSuccess(0)
	want := []byte{
		1,                // success flag
		4, 0, 0, 0,       // protobuf length = 4
		0x08, 0x00,       // field 1: space = 0
		0x10, 0x00,       // field 2: errCode = 0
	}
	if !bytes.Equal(ack, want) {
		t.Fatalf("SortItemsSuccess(0) = %x, want %x", ack, want)
	}

	ref := SortItemsRefused(1, 2)
	if ref[0] != 0 || binary.LittleEndian.Uint16(ref[1:3]) != 2 {
		t.Fatalf("unexpected refusal header: %x", ref[:3])
	}
	pbLen := binary.LittleEndian.Uint32(ref[3:7])
	wantPB := []byte{0x08, 0x01, 0x10, 0x02}
	if int(pbLen) != len(wantPB) || !bytes.Equal(ref[7:], wantPB) {
		t.Fatalf("unexpected refusal pb: len=%d bytes=%x", pbLen, ref[7:])
	}
}
