package protocol

import (
	"encoding/binary"
	"fmt"
)

// CmdDeleteItem is CMD/ACK 18 (0x12, ENUM_CMDPACKET_DELETE_ITEM).
// Verified from DFO_unpacked.exe:
//   - Sender:  0x143b25700..0x143b25b36 (pushes InvenMgr transaction lock at 0x143b25b31)
//   - Handler: 0x1434a3d10..0x1434a4cbb (closes window 0x3bf, deletes items via 0x143b194c0,
//     and pops the InvenMgr lock via 0x143b19a80 so booster opening / bag ops are unlocked).
const CmdDeleteItem uint16 = 18

// DeleteItemEntry is one item deletion entry in PB_ENUM_CMDPACKET_DELETE_ITEM.DELETE_REQUEST.
type DeleteItemEntry struct {
	DeleteType uint16
	Slot       uint16
	Template   uint32
	Count      uint32
}

// DeleteItemsRequest is the decoded CMD 18 (0x12) body:
//   - u32 LE protobuf byte length N
//   - N bytes of protobuf PB_ENUM_CMDPACKET_DELETE_ITEM
//   - zero padding to 8-byte cipher block boundary
type DeleteItemsRequest struct {
	Space byte
	Items []DeleteItemEntry
	Flag  uint32
}

// DeletedItem is one confirmed deletion entry in PB_ENUM_CMDPACKET_DELETE_ITEM.RETURN.DELETE_ITEMS.
type DeletedItem struct {
	Slot       uint16
	Count      uint32
	DeleteType uint16
}

func readPBVarint(buf []byte, off int) (uint64, int, error) {
	var v uint64
	var shift uint
	for off < len(buf) {
		b := buf[off]
		off++
		if shift >= 64 {
			return 0, off, fmt.Errorf("protobuf varint overflow")
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, off, nil
		}
		shift += 7
	}
	return 0, off, fmt.Errorf("truncated protobuf varint")
}

func skipPBField(buf []byte, off int, wireType byte) (int, error) {
	switch wireType {
	case 0:
		_, next, err := readPBVarint(buf, off)
		return next, err
	case 1:
		if off+8 > len(buf) {
			return off, fmt.Errorf("truncated fixed64")
		}
		return off + 8, nil
	case 2:
		ln, next, err := readPBVarint(buf, off)
		if err != nil || next+int(ln) > len(buf) {
			return off, fmt.Errorf("truncated length-delimited field")
		}
		return next + int(ln), nil
	case 5:
		if off+4 > len(buf) {
			return off, fmt.Errorf("truncated fixed32")
		}
		return off + 4, nil
	default:
		return off, fmt.Errorf("unsupported protobuf wire type %d", wireType)
	}
}

func decodeDeleteItemEntry(pb []byte) (DeleteItemEntry, error) {
	// Verified from DFO_unpacked.exe 0x1401513c0 (DELETE_REQUEST::SerializeWithCachedSizes):
	//   field 1 (varint): delete type / reason ([rcx+0x20])
	//   field 2 (varint): slot ([rcx+0x24])
	//   field 3 (varint): item template ID ([rcx+0x28])
	//   field 4 (varint): count ([rcx+0x2c])
	var entry DeleteItemEntry
	off := 0
	for off < len(pb) {
		tag, next, err := readPBVarint(pb, off)
		if err != nil {
			return entry, err
		}
		off = next
		fieldNum := tag >> 3
		wireType := byte(tag & 7)
		if wireType == 0 {
			val, n2, err := readPBVarint(pb, off)
			if err != nil {
				return entry, err
			}
			off = n2
			switch fieldNum {
			case 1:
				entry.DeleteType = uint16(val)
			case 2:
				entry.Slot = uint16(val)
			case 3:
				entry.Template = uint32(val)
			case 4:
				entry.Count = uint32(val)
			}
		} else {
			n2, err := skipPBField(pb, off, wireType)
			if err != nil {
				return entry, err
			}
			off = n2
		}
	}
	if entry.Count == 0 {
		return entry, fmt.Errorf("delete item request has zero count for slot %d", entry.Slot)
	}
	return entry, nil
}

// DecodeDeleteItems decodes CMD 18 (0x12, PB_ENUM_CMDPACKET_DELETE_ITEM).
// Verified from DFO_unpacked.exe 0x140151320 (SerializeWithCachedSizes):
//   field 2 (varint): inventory space ([rcx+0x38])
//   field 3 (repeated message): DELETE_REQUEST ([rcx+0x28]/[rcx+0x30])
//   field 4 (varint): flag ([rcx+0x3c])
func DecodeDeleteItems(p []byte) (DeleteItemsRequest, error) {
	var r DeleteItemsRequest
	if len(p) < 8 || len(p)%8 != 0 {
		return r, fmt.Errorf("delete items request must be 8-byte aligned, got %d", len(p))
	}
	pbLen := int(binary.LittleEndian.Uint32(p[0:4]))
	if pbLen <= 0 || 4+pbLen > len(p) {
		return r, fmt.Errorf("invalid delete items protobuf length %d in %d bytes", pbLen, len(p))
	}
	for _, b := range p[4+pbLen:] {
		if b != 0 {
			return r, fmt.Errorf("nonzero padding in delete items request")
		}
	}
	pb := p[4 : 4+pbLen]
	off := 0
	for off < len(pb) {
		tag, next, err := readPBVarint(pb, off)
		if err != nil {
			return r, err
		}
		off = next
		fieldNum := tag >> 3
		wireType := byte(tag & 7)
		switch {
		case fieldNum == 2 && wireType == 0:
			val, n2, err := readPBVarint(pb, off)
			if err != nil {
				return r, err
			}
			off = n2
			r.Space = byte(val)
		case fieldNum == 3 && wireType == 2:
			ln, n2, err := readPBVarint(pb, off)
			if err != nil || n2+int(ln) > len(pb) {
				return r, fmt.Errorf("truncated DELETE_REQUEST entry")
			}
			item, err := decodeDeleteItemEntry(pb[n2 : n2+int(ln)])
			if err != nil {
				return r, err
			}
			r.Items = append(r.Items, item)
			off = n2 + int(ln)
		case fieldNum == 4 && wireType == 0:
			val, n2, err := readPBVarint(pb, off)
			if err != nil {
				return r, err
			}
			off = n2
			r.Flag = uint32(val)
		default:
			n2, err := skipPBField(pb, off, wireType)
			if err != nil {
				return r, err
			}
			off = n2
		}
	}
	if len(r.Items) == 0 {
		return r, fmt.Errorf("delete items request contains no items")
	}
	return r, nil
}

func encodeDeletedItemPB(it DeletedItem) []byte {
	// Verified from DFO_unpacked.exe 0x140151500 (DELETE_ITEMS::SerializeWithCachedSizes):
	//   field 1 (tag 0x08, int32 varint): slot ([rcx+0x20])
	//   field 2 (tag 0x10, int32 varint): deleted count ([rcx+0x24])
	//   field 3 (tag 0x18, int32 varint): delete type ([rcx+0x28])
	var pb []byte
	pb = append(pb, 0x08)
	pb = appendPBVarint(pb, uint64(it.Slot))
	pb = append(pb, 0x10)
	pb = appendPBVarint(pb, uint64(it.Count))
	pb = append(pb, 0x18)
	pb = appendPBVarint(pb, uint64(it.DeleteType))
	return pb
}

func encodeDeleteItemsReturnPB(space byte, items []DeletedItem, errCode uint32) []byte {
	// Verified from DFO_unpacked.exe 0x140151460 (RETURN::SerializeWithCachedSizes)
	// and 0x1434a3d10 (CMDFUNC_ENUM_CMDPACKET_DELETE_ITEM):
	//   field 1 (tag 0x08, int32 varint): inventory space ([rcx+0x38])
	//   field 2 (tag 0x12, repeated message): DELETE_ITEMS ([rcx+0x28]/[rcx+0x30])
	//   field 3 (tag 0x18, int32 varint): error code ([rcx+0x3c])
	var pb []byte
	pb = append(pb, 0x08)
	pb = appendPBVarint(pb, uint64(space))
	for _, it := range items {
		sub := encodeDeletedItemPB(it)
		pb = append(pb, 0x12)
		pb = appendPBVarint(pb, uint64(len(sub)))
		pb = append(pb, sub...)
	}
	pb = append(pb, 0x18)
	pb = appendPBVarint(pb, uint64(errCode))
	return pb
}

// DeleteItemsSuccess encodes ACK 18 (0x12, handler 0x1434a3d10):
// u8(1) success flag + u32(len(pb)) + protobuf RETURN{space, items, errCode=0}.
// Client closes window 0x3bf, deletes slots via InvenMgr::DeleteItems (0x143b194c0),
// and pops the inventory lock via 0x143b19a80.
func DeleteItemsSuccess(space byte, items []DeletedItem) []byte {
	pb := encodeDeleteItemsReturnPB(space, items, 0)
	p := make([]byte, 0, 5+len(pb))
	p = append(p, 1)
	p = add32(p, uint32(len(pb)))
	return append(p, pb...)
}

// DeleteItemsRefused encodes ACK 18 failure:
// u8(0) + u16(code) + u32(len(pb)) + protobuf RETURN{space, errCode=code}.
// Handler closes window 0x3bf and pops the inventory lock via 0x143b19a80.
func DeleteItemsRefused(space byte, code uint16) []byte {
	pb := encodeDeleteItemsReturnPB(space, nil, uint32(code))
	p := make([]byte, 0, 7+len(pb))
	p = append(p, Refusal(code)...)
	p = add32(p, uint32(len(pb)))
	return append(p, pb...)
}
