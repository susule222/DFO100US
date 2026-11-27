package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	// Sort-lock / sort-unlock (Ctrl + Left-Click in Inventory / Safe).
	// Verified from DFO_unpacked.exe:
	//   CMD 705 (0x2c1) sent at 0x14480d9e3..0x14480da3d & 0x1434b44c6..0x1434b44ff
	//   CMD 706 (0x2c2) sent at 0x14480d9cc..0x14480da3d
	//   ACK 705 (0x2c1) handler at 0x14348ec70 (sets item+0x62b = 1)
	//   ACK 706 (0x2c2) handler at 0x14348ecd0 (sets item+0x62b = 0)
	CmdSortLock   uint16 = 705
	CmdSortUnlock uint16 = 706

	// Item safety lock / unlock / cancel-unlock / instant-unlock (Alt + Left-Click).
	// Verified from DFO_unpacked.exe:
	//   CMD 267 (0x10b) sent at 0x14480d94a..0x14480d998; ACK 267 at 0x1434bcef0
	//   CMD 268 (0x10c) sent at 0x14480d943..0x14480d998; ACK 268 at 0x1434bcff0
	//   CMD 269 (0x10d) sent at 0x14480d93c..0x14480d998; ACK 269 at 0x1434bd160
	//   CMD 270 (0x10e) sent at 0x1432ec613..0x1432ec697; ACK 270 at 0x1434bd240
	//   NOTI 251 (0xfb) login restore at 0x1434fd080
	//   NOTI 252 (0xfc) unlock completion at 0x1434fd150
	CmdItemLock         uint16 = 267
	CmdItemUnlock       uint16 = 268
	CmdItemCancelUnlock uint16 = 269
	CmdItemUnlockNow    uint16 = 270
	NotiItemLockList    uint16 = 251
	NotiItemUnlocked    uint16 = 252
)

// ItemSlotTarget is the decoded 3-byte (u8 space, u16 slot) request body used
// by CMD 705 (sort lock), CMD 706 (sort unlock), CMD 267 (item lock), and
// CMD 269 (cancel item unlock).
type ItemSlotTarget struct {
	Space byte
	Slot  uint16
}

// ItemUnlockRequest is the decoded 4-byte (u8 space, u16 slot, u8 flag) body
// sent by CMD 268 (0x10c).
type ItemUnlockRequest struct {
	Space byte
	Slot  uint16
	Flag  byte
}

// ItemLockEntry is one row in NOTI 251 (0xfb, 0x1434fd080).
// State 1 = locked; State 2 = unlocking with RemainingSeconds countdown.
type ItemLockEntry struct {
	Space            byte
	Slot             uint16
	State            byte
	RemainingSeconds uint32
}

func DecodeItemSlotTarget(p []byte) (ItemSlotTarget, error) {
	var r ItemSlotTarget
	if len(p) != 3 && len(p) != 8 {
		return r, fmt.Errorf("item lock target requires 3 or 8 bytes, got %d", len(p))
	}
	for _, b := range p[3:] {
		if b != 0 {
			return r, fmt.Errorf("nonzero item lock padding")
		}
	}
	r.Space = p[0]
	r.Slot = binary.LittleEndian.Uint16(p[1:3])
	return r, nil
}

func DecodeItemUnlock(p []byte) (ItemUnlockRequest, error) {
	var r ItemUnlockRequest
	if len(p) != 4 && len(p) != 8 {
		return r, fmt.Errorf("item unlock requires 4 or 8 bytes, got %d", len(p))
	}
	for _, b := range p[4:] {
		if b != 0 {
			return r, fmt.Errorf("nonzero item unlock padding")
		}
	}
	r.Space = p[0]
	r.Slot = binary.LittleEndian.Uint16(p[1:3])
	r.Flag = p[3]
	return r, nil
}

func DecodeItemUnlockNow(p []byte) (ItemSlotTarget, error) {
	var r ItemSlotTarget
	if len(p) < 3 {
		return r, fmt.Errorf("item instant unlock requires at least 3 bytes, got %d", len(p))
	}
	r.Space = p[0]
	r.Slot = binary.LittleEndian.Uint16(p[1:3])
	return r, nil
}

// SortLockSuccess encodes ACK 705 (0x2c1) / ACK 706 (0x2c2) success:
// u8(1), u8 space, u16 slot (0x14348ec70 / 0x14348ecd0).
func SortLockSuccess(space byte, slot uint16) []byte {
	return add16([]byte{1, space}, slot)
}

// SortLockRefused encodes ACK 705 / ACK 706 failure: u8(0), u16 code, u8 err
// (0x14348ecbd / 0x14348ed1d reads one extra u8 when dl == 0).
func SortLockRefused(code uint16) []byte {
	return append(Refusal(code), 0)
}

// ItemLockSuccess encodes ACK 267 (0x10b), ACK 269 (0x10d), and ACK 270 (0x10e)
// success: u8(1), u8 space, u16 slot (0x1434bcef0 / 0x1434bd160 / 0x1434bd240).
func ItemLockSuccess(space byte, slot uint16) []byte {
	return add16([]byte{1, space}, slot)
}

// ItemUnlockSuccess encodes ACK 268 (0x10c) success:
// u8(1), u8 space, u16 slot, u32 remainingSeconds (0x1434bcff0).
func ItemUnlockSuccess(space byte, slot uint16, remainingSeconds uint32) []byte {
	return add32(add16([]byte{1, space}, slot), remainingSeconds)
}

// ItemUnlockRefused encodes ACK 268 (0x10c) failure:
// u8(0), u16 code, u32 param (0x1434bd07e reads one u32 when dl == 0).
func ItemUnlockRefused(code uint16, param uint32) []byte {
	return add32(Refusal(code), param)
}

// ItemLockListNoti encodes NOTI 251 (0xfb, 0x1434fd080):
// u16 count + count * (u8 space, u16 slot, u8 state [, u32 remainingSeconds if state==2]).
func ItemLockListNoti(entries []ItemLockEntry) ([]byte, error) {
	if len(entries) > 65535 {
		return nil, fmt.Errorf("too many locked item entries")
	}
	p := add16(nil, uint16(len(entries)))
	for _, e := range entries {
		p = append(p, e.Space)
		p = add16(p, e.Slot)
		p = append(p, e.State)
		if e.State == 2 {
			p = add32(p, e.RemainingSeconds)
		}
	}
	return p, nil
}

// ItemUnlockedNoti encodes NOTI 252 (0xfc, 0x1434fd150):
// u8 space, u16 slot -> clears client item lock state to 0 immediately.
func ItemUnlockedNoti(space byte, slot uint16) []byte {
	return add16([]byte{space}, slot)
}
