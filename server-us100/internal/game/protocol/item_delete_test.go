package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestDecodeDeleteItemsLiveCapture(t *testing.T) {
	// Live US100 client CMD 18 capture (deleting slot 80 = 0x50, template 50 = 0x32, count 1).
	raw, err := hex.DecodeString("0e00000010001a0808011050183220012000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	req, err := DecodeDeleteItems(raw)
	if err != nil {
		t.Fatalf("DecodeDeleteItems failed: %v", err)
	}
	if req.Space != 0 || req.Flag != 0 || len(req.Items) != 1 {
		t.Fatalf("unexpected header: %+v", req)
	}
	it := req.Items[0]
	if it.DeleteType != 1 || it.Slot != 80 || it.Template != 50 || it.Count != 1 {
		t.Fatalf("unexpected item entry: %+v", it)
	}
}

func TestDeleteItemsAckEncoding(t *testing.T) {
	ack := DeleteItemsSuccess(0, []DeletedItem{{Slot: 80, Count: 1, DeleteType: 1}})
	if len(ack) < 6 || ack[0] != 1 {
		t.Fatalf("unexpected success envelope: %x", ack)
	}
	pbLen := int(binary.LittleEndian.Uint32(ack[1:5]))
	if pbLen != len(ack)-5 {
		t.Fatalf("pb length mismatch: header=%d actual=%d", pbLen, len(ack)-5)
	}
	// Expected PB: field1=0 (08 00), field2={f1=80(08 50), f2=1(10 01), f3=1(18 01)} (12 06 08 50 10 01 18 01), field3=0 (18 00)
	wantPB := "080012060850100118011800"
	if got := hex.EncodeToString(ack[5:]); got != wantPB {
		t.Fatalf("success pb = %s, want %s", got, wantPB)
	}

	ref := DeleteItemsRefused(0, 0x13)
	if len(ref) < 8 || ref[0] != 0 || binary.LittleEndian.Uint16(ref[1:3]) != 0x13 {
		t.Fatalf("unexpected refusal envelope: %x", ref)
	}
	refLen := int(binary.LittleEndian.Uint32(ref[3:7]))
	if refLen != len(ref)-7 {
		t.Fatalf("refusal pb length mismatch: header=%d actual=%d", refLen, len(ref)-7)
	}
	if got := hex.EncodeToString(ref[7:]); got != "08001813" {
		t.Fatalf("refusal pb = %s, want 08001813", got)
	}
}
