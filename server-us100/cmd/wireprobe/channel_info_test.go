package main

import (
	"bytes"
	"dfolan/internal/game/wire"
	"encoding/binary"
	"testing"
)

func decodeChannelInfoProto(t *testing.T, frame []byte) (uints map[uint64]uint64, blobs map[uint64][]byte) {
	t.Helper()
	if len(frame) < 20 {
		t.Fatalf("frame too short: %d", len(frame))
	}
	cipher := frame[16:]
	plain := make([]byte, len(cipher))
	for i, c := range cipher {
		plain[i] = ((c >> 2) | (c << 6)) ^ 0xb5
	}
	pbLen := binary.LittleEndian.Uint32(plain[:4])
	if int(pbLen) != len(plain)-4 {
		t.Fatalf("pb length mismatch: header=%d actual=%d", pbLen, len(plain)-4)
	}
	pb := plain[4:]
	uints = map[uint64]uint64{}
	blobs = map[uint64][]byte{}
	readVarint := func(b []byte) (uint64, int) {
		var v uint64
		var shift uint
		for i, x := range b {
			v |= uint64(x&127) << shift
			if x < 128 {
				return v, i + 1
			}
			shift += 7
		}
		t.Fatal("truncated varint")
		return 0, 0
	}
	for len(pb) > 0 {
		tag, n := readVarint(pb)
		pb = pb[n:]
		field, wt := tag>>3, tag&7
		switch wt {
		case 0:
			v, m := readVarint(pb)
			pb = pb[m:]
			uints[field] = v
		case 2:
			l, m := readVarint(pb)
			pb = pb[m:]
			blobs[field] = append([]byte(nil), pb[:l]...)
			pb = pb[l:]
		default:
			t.Fatalf("unexpected wire type %d for field %d", wt, field)
		}
	}
	return uints, blobs
}

func TestBuildChannelInfoFrame(t *testing.T) {
	keys := make([]byte, channelBannerKeyBytes)
	for i := range keys {
		keys[i] = byte(i%127 + 1)
	}
	raw, err := buildChannelInfoFrame(1, 24, "Alfhlyra 1", keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.ValidateServer(raw); err != nil {
		t.Fatal(err)
	}
	if raw[0] != 0 || raw[1] != 1 || raw[2] != 0 {
		t.Fatalf("unexpected header kind/id: %x", raw[:4])
	}
	uints, blobs := decodeChannelInfoProto(t, raw)
	if uints[2] != 1 {
		t.Fatalf("field 2 (accept flag) = %d, want 1", uints[2])
	}
	if uints[7] != 1 {
		t.Fatalf("field 7 (server_group) = %d, want 1", uints[7])
	}
	if uints[8] != 24 {
		t.Fatalf("field 8 (channel_no) = %d, want 24", uints[8])
	}
	if !bytes.Equal(blobs[3], keys) {
		t.Fatalf("field 3 (keys) mismatch: len=%d", len(blobs[3]))
	}
	if string(blobs[4]) != "Alfhlyra 1" {
		t.Fatalf("field 4 (channel_name) = %q, want %q", string(blobs[4]), "Alfhlyra 1")
	}
}
