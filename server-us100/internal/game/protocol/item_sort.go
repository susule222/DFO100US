package protocol

import (
	"encoding/binary"
	"fmt"
)

// SortItemPair is one (fromSlot -> toSlot) entry in CMD 20 (0x14).
// Verified from DFO_unpacked.exe 0x143b3a01f..0x143b3a1ef: the client computes
// a simultaneous slot permutation and writes (u16 oldSlot, u16 newSlot) pairs.
type SortItemPair struct {
	From uint16
	To   uint16
}

// SortItemsRequest is the decoded CMD 20 (0x14, ENUM_CMDPACKET_SORT_ITEM) body:
//   - u8  space (0 = ordinary bag, 1 = avatar bag, 2 = vault, ...)
//   - u8  tab   (tab index within the space)
//   - u16 count (number of (from, to) pairs)
//   - count * (u16 fromSlot, u16 toSlot)
//   - zero padding to 8-byte cipher block boundary
type SortItemsRequest struct {
	Space byte
	Tab   byte
	Pairs []SortItemPair
}

func DecodeSortItems(p []byte) (SortItemsRequest, error) {
	var r SortItemsRequest
	if len(p) < 8 || len(p)%8 != 0 {
		return r, fmt.Errorf("sort items request must be 8-byte aligned, got %d", len(p))
	}
	r.Space = p[0]
	r.Tab = p[1]
	count := int(binary.LittleEndian.Uint16(p[2:4]))
	rawLen := 4 + count*4
	paddedLen := (rawLen + 7) / 8 * 8
	if count == 0 || len(p) != paddedLen {
		return r, fmt.Errorf("invalid sort items dimensions: count=%d len=%d want=%d", count, len(p), paddedLen)
	}
	for _, b := range p[rawLen:] {
		if b != 0 {
			return r, fmt.Errorf("nonzero padding in sort items request")
		}
	}
	r.Pairs = make([]SortItemPair, count)
	for i := 0; i < count; i++ {
		off := 4 + i*4
		r.Pairs[i] = SortItemPair{
			From: binary.LittleEndian.Uint16(p[off : off+2]),
			To:   binary.LittleEndian.Uint16(p[off+2 : off+4]),
		}
	}
	return r, nil
}

func appendPBVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func encodeSortItemsPB(space uint32, errCode uint32) []byte {
	// Verified from DFO_unpacked.exe 0x140151780 (SerializeWithCachedSizes):
	//   field 1 (tag 0x08, int32 varint): inventory space ([rcx+0x20])
	//   field 2 (tag 0x10, int32 varint): error code ([rcx+0x24])
	var pb []byte
	pb = append(pb, 0x08)
	pb = appendPBVarint(pb, uint64(space))
	pb = append(pb, 0x10)
	pb = appendPBVarint(pb, uint64(errCode))
	return pb
}

// SortItemsSuccess encodes ACK 20 (0x14, CMDFUNC_ENUM_CMDPACKET_SORT_ITEM at 0x1434c4860):
// u8(1) success flag + u32(len(pb)) + protobuf{space, errCode=0}.
// Handler unlocks the inventory space (0x143b19a80) and plays ARRANGE_FINISH.
func SortItemsSuccess(space byte) []byte {
	pb := encodeSortItemsPB(uint32(space), 0)
	p := make([]byte, 0, 5+len(pb))
	p = append(p, 1)
	p = add32(p, uint32(len(pb)))
	return append(p, pb...)
}

// SortItemsRefused encodes ACK 20 failure: u8(0) + u16(code) + u32(len(pb)) + protobuf{space, errCode}.
func SortItemsRefused(space byte, code uint16) []byte {
	pb := encodeSortItemsPB(uint32(space), uint32(code))
	p := make([]byte, 0, 7+len(pb))
	p = append(p, Refusal(code)...)
	p = add32(p, uint32(len(pb)))
	return append(p, pb...)
}
