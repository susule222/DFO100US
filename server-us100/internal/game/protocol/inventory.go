package protocol

import (
	"encoding/binary"
	"fmt"
)

// ItemSortLockOffset is the byte index within the 0xb1-byte US224 item row
// that maps to CItem::sortLocked (+0x62b). Verified from DFO_unpacked.exe:
// 0x14391b6e6 reads [raw+0x55] -> [decoded+0xd2], then 0x143260f95 writes
// [decoded+0xd2] -> [item+0x62b].
const ItemSortLockOffset = 0x55

// OrdinaryItem is the current 181-byte base row. Special equipment branches
// require extra source/type validation and are deliberately separate.
func OrdinaryItem(slot uint16, template, amount uint32) [CurrentItemRecordSize]byte {
	var p [CurrentItemRecordSize]byte
	binary.LittleEndian.PutUint16(p[:], slot)
	binary.LittleEndian.PutUint32(p[2:], template)
	binary.LittleEndian.PutUint32(p[6:], amount)
	return p
}

// EmptySlotItem encodes the native -1 (0xFFFFFFFF) template sentinel that
// NOTI14 (0x143511224) uses to clear an emptied slot in non-gold spaces.
func EmptySlotItem(slot uint16) [CurrentItemRecordSize]byte {
	return OrdinaryItem(slot, 0xffffffff, 0)
}

func itemRows(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if len(rows) > 65535 {
		return nil, fmt.Errorf("too many inventory rows")
	}
	p := add16(nil, uint16(len(rows)))
	seen := map[uint16]bool{}
	for _, r := range rows {
		slot := binary.LittleEndian.Uint16(r[:])
		if seen[slot] {
			return nil, fmt.Errorf("duplicate inventory slot")
		}
		seen[slot] = true
		p = append(p, r[:]...)
	}
	return p, nil
}

// US224 uses 0xb1 bytes per ordinary (space 0) row in both client
// readers: NOTI13 at 1434fc638 and NOTI14 at 1435109b0. Keep the
// reference row builders unchanged for other record types and fixtures.
//
// In NOTI13 space 0 (0x1434fc195..0x1434fc2a5), the client reads u8 space=0,
// then u16 expandedSlots (shifted right by 3 to compute expanded rows: 0, 8, 16),
// then u16 rowCount followed by 0xb1-byte rows.
func US224InventoryRestore(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return US224InventoryRestoreWithExpansion(0, rows)
}

func US224InventoryRestoreWithExpansion(expandedSlots uint16, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return us224OrdinaryList(add16([]byte{0}, expandedSlots), rows)
}

func US224InventoryUpdate(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return us224OrdinaryList([]byte{0}, rows)
}

// CmdExpandInventory is CMD 422 (0x1a6), sent by DFO_unpacked.exe (0x1439b1136)
// when using an inventory expansion ticket from the bag: u16 slot.
const CmdExpandInventory uint16 = 422

// DecodeExpandInventory decodes CMD 422 (u16 slot + Blowfish 8-byte zero padding).
func DecodeExpandInventory(p []byte) (uint16, error) {
	if len(p) != 2 && len(p) != 8 {
		return 0, fmt.Errorf("expand inventory requires 2 or 8 bytes, got %d", len(p))
	}
	for _, b := range p[2:] {
		if b != 0 {
			return 0, fmt.Errorf("unexpected expand inventory padding")
		}
	}
	return binary.LittleEndian.Uint16(p[:2]), nil
}

// InventoryExpandNoti encodes NOTI 66 (0x42) subType 12 (0x000c), verified at
// DFO_unpacked.exe 0x1434ef2b0 -> 0x1434ef96b:
//   u16 subType=12, u16 expandedSlots (8 = stage 1/+1 row, 16 = stage 2/+2 rows).
func InventoryExpandNoti(expandedSlots uint16) []byte {
	return add16(add16(nil, 12), expandedSlots)
}

// us224AvatarList encodes US224 space 1 (Avatar Inventory) rows.
// Native readers NOTI13 (0x1434fc9c5/0x1434fc9e7) and NOTI14 (0x1435109e5/0x143510a08)
// both call 0x143a4cd70 and 0x143a4cdb0 -> 0x144e6eda0 after every 0xb1-byte row:
// each reads a u32 byte-length followed by that many bytes (emblem sockets + avatar extra).
// Writing two u32(0) trailers yields exact 185-byte (0xb1+8) aligned avatar rows.
func us224AvatarList(header []byte, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	const size = 0xb1
	if len(rows) > 65535 {
		return nil, fmt.Errorf("too many inventory rows")
	}
	seen := make(map[uint16]bool, len(rows))
	p := add16(header, uint16(len(rows)))
	for _, r := range rows {
		slot := binary.LittleEndian.Uint16(r[:])
		if seen[slot] {
			return nil, fmt.Errorf("duplicate inventory slot")
		}
		seen[slot] = true
		for _, tail := range r[size:] {
			if tail != 0 {
				return nil, fmt.Errorf("unsupported US224 avatar extension at slot %d", slot)
			}
		}
		p = append(p, r[:size]...)
		p = add32(p, 0) // 0x143a4cd70: emblem socket payload length = 0
		p = add32(p, 0) // 0x143a4cdb0: avatar extra payload length = 0
	}
	return p, nil
}

// US224AvatarRestore encodes NOTI13 space 1 (Avatar Inventory / 装扮栏):
// u8 space=1, u16 capacity (0x1434fc321), u16 count, count * (0xb1 + 8) bytes.
func US224AvatarRestore(capacity uint16, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if capacity == 0 {
		capacity = 280
	}
	return us224AvatarList(add16([]byte{1}, capacity), rows)
}

// US224AvatarUpdate encodes NOTI14 space 1 (live online Avatar Inventory update):
// u8 space=1, u16 count, count * (0xb1 + 8) bytes (0x14351074b..0x143510a08).
func US224AvatarUpdate(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return us224AvatarList([]byte{1}, rows)
}

// Space 3 has the same item framing in the US224 restore and update
// readers: 0xb1 bytes; two length-prefixed extras for an avatar template.
// The inventory layer keeps avatars and ordinary equipment in disjoint slots.
func us224WornList(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	const size = 0xb1
	if len(rows) > 65535 {
		return nil, fmt.Errorf("too many inventory rows")
	}
	seen := make(map[uint16]bool, len(rows))
	p := add16([]byte{3}, uint16(len(rows)))
	for _, r := range rows {
		slot := binary.LittleEndian.Uint16(r[:])
		if slot > 25 {
			return nil, fmt.Errorf("unsupported worn slot %d", slot)
		}
		if seen[slot] {
			return nil, fmt.Errorf("duplicate inventory slot")
		}
		seen[slot] = true
		for _, tail := range r[size:] {
			if tail != 0 {
				return nil, fmt.Errorf("unsupported US224 worn extension at slot %d", slot)
			}
		}
		p = append(p, r[:size]...)
		tpl := binary.LittleEndian.Uint32(r[2:])
		if slot <= 11 && tpl != 0 && tpl != 0xffffffff {
			p = add32(p, 0)
			p = add32(p, 0)
		}
	}
	return p, nil
}

func US224WornRestore(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return us224WornList(rows)
}

func US224WornUpdate(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	return us224WornList(rows)
}

func US224InventorySpaceUpdate(space byte, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if space == 0 {
		return US224InventoryUpdate(rows)
	}
	if space == 1 {
		return US224AvatarUpdate(rows)
	}
	if space == 3 {
		return US224WornUpdate(rows)
	}
	return InventorySpaceUpdate(space, rows)
}

func us224OrdinaryList(header []byte, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	const size = 0xb1
	if len(rows) > 65535 {
		return nil, fmt.Errorf("too many inventory rows")
	}
	seen := make(map[uint16]bool, len(rows))
	p := add16(header, uint16(len(rows)))
	for _, r := range rows {
		slot := binary.LittleEndian.Uint16(r[:])
		if seen[slot] {
			return nil, fmt.Errorf("duplicate inventory slot")
		}
		seen[slot] = true
		// Never silently discard extensions if the reference builders change.
		for _, tail := range r[size:] {
			if tail != 0 {
				return nil, fmt.Errorf("unsupported US224 inventory extension at slot %d", slot)
			}
		}
		p = append(p, r[:size]...)
	}
	return p, nil
}

// NOTI13 list0 first reads a u16 count of locked slots, then item count.
func InventoryRestore(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	p, e := itemRows(rows)
	if e != nil {
		return nil, e
	}
	return append([]byte{0, 0, 0}, p...), nil
}
func InventoryUpdate(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	p, e := itemRows(rows)
	if e != nil {
		return nil, e
	}
	return append([]byte{0}, p...), nil
}

func InventorySpaceUpdate(space byte, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if space != 0 && space != 3 {
		return nil, fmt.Errorf("unsupported equipment update space")
	}
	p, e := itemRows(rows)
	if e != nil {
		return nil, e
	}
	return append([]byte{space}, p...), nil
}

// Current NOTI13 list3 omits list0's lock count and adds a u32 period after
// every181-byte row (1452d6a0b). Period0 represents ordinary permanent gear.
func WornRestore(rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if _, e := itemRows(rows); e != nil {
		return nil, e
	}
	p := add16([]byte{3}, uint16(len(rows)))
	for _, r := range rows {
		p = append(p, r[:]...)
		p = add32(p, 0)
	}
	return p, nil
}

type PickupRequest struct {
	Object                       uint32
	ActorX, ActorY, DropX, DropY uint16
	Context, Flag                byte // Opaque client observations; never item IDs or amounts.
}

func DecodePickup(p []byte) (PickupRequest, error) {
	var r PickupRequest
	if len(p) != 21 && len(p) != 24 && len(p) != 32 {
		return r, fmt.Errorf("pickup requires21 bytes with cipher block padding")
	}
	for _, b := range p[21:] {
		if b != 0 {
			return r, fmt.Errorf("pickup padding")
		}
	}
	if p[4] != 0 || p[20] > 1 {
		return r, fmt.Errorf("unsupported pickup option")
	}
	r = PickupRequest{Object: binary.LittleEndian.Uint32(p), ActorX: binary.LittleEndian.Uint16(p[6:]), ActorY: binary.LittleEndian.Uint16(p[8:]), DropX: binary.LittleEndian.Uint16(p[12:]), DropY: binary.LittleEndian.Uint16(p[14:]), Context: p[5], Flag: p[20]}
	if r.Object == 0 {
		return r, fmt.Errorf("zero scene object")
	}
	return r, nil
}

// NOTI39 gold consumes eight (u8 flag,u32 amount) rows and has no ordinary
// tail. Neutral flags avoid applying an unverified currency delta; NOTI14
// restores the authoritative gold balance before this scene removal.
func PickupConfirmed(object uint32, actor, slot uint16, gold bool) ([]byte, error) {
	if object == 0 || actor == 0 || actor == 65535 {
		return nil, fmt.Errorf("invalid pickup identity")
	}
	p := add16(add32(nil, object), actor)
	if gold {
		return append(p, make([]byte, 40)...), nil
	}
	p = append(p, make([]byte, 8)...)
	p = add16(add16(p, actor), slot)
	return append(p, 0), nil
}

// Current solo party0 gold row includes a flag, amount and an extra-count
// byte. This invokes both the local currency delta and overhead number;
// the remaining seven party rows are absent (flag0, amount0).
func GoldPickupConfirmed(object uint32, actor uint16, amount uint32) ([]byte, error) {
	if object == 0 || actor == 0 || actor == 65535 || amount == 0 {
		return nil, fmt.Errorf("invalid gold pickup")
	}
	p := add16(add32(nil, object), actor)
	p = add32(append(p, 1), amount)
	return append(p, make([]byte, 36)...), nil
}

// Failure consumes the scene object after the dispatcher's u16 error code.
// A generic Refusal alone truncates145244e5d and leaves pickup in flight.
func PickupRefused(object uint32) ([]byte, error) {
	if object == 0 {
		return nil, fmt.Errorf("zero pickup refusal object")
	}
	return add32(Refusal(4), object), nil
}

func MonsterDeathDrops(entity uint16, drops []SceneDrop) ([]byte, error) {
	if entity == 0 || entity == 65535 || len(drops) > 65535 {
		return nil, fmt.Errorf("invalid death/drop count")
	}
	p := add16(add16(nil, entity), uint16(len(drops)))
	seen := map[uint32]bool{}
	for _, d := range drops {
		if seen[d.Object] {
			return nil, fmt.Errorf("duplicate scene drop")
		}
		seen[d.Object] = true
		v, e := OrdinarySceneDropRecord(d)
		if e != nil {
			return nil, e
		}
		p = append(p, v...)
	}
	return append(p, 0, 0, 255, 0), nil
}
