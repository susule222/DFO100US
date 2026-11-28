package protocol

// Verified US100 Mileage notification opcodes recovered from DFO_unpacked.exe:
//   - NOTI 413 (0x19d, 0x1434fff20): silent Mileage balance restore (9 bytes).
//     Reads u8 flag (1), u32 mileage (-> g_mileage 0x14a210e40 & g_prevMileage
//     0x14a210e50), u32 dailyContentsMileage (-> 0x14a210e48). No chat toast.
//   - NOTI 524 (0x20c, 0x143512e70): live Mileage earned update + toast (13 bytes).
//     Reads u8 reason (10=Cash Shop rebate 0x98bb, 7..9=dungeon/raid/pvp 0x816f,
//     other=generic 0x8a03), u32 oldMileage, u32 newMileage, u32 dailyContentsMileage.
//     Computes delta = newMileage - oldMileage, posts system chat notice, and
//     updates g_mileage (0x14a210e40), g_prevMileage (0x14a210e50), and
//     g_dailyContentsMileage (0x14a210e48).
const (
	NotiMileageRestore uint16 = 413
	NotiMileageUpdate  uint16 = 524

	MileageReasonGeneric byte = 0
	MileageReasonDungeon byte = 7
	MileageReasonShop    byte = 10
)

// MileageRestore encodes NOTI 413 (0x19d, 9 bytes) to silently sync the
// account's current Mileage balance and daily contents Mileage on login or GM update.
func MileageRestore(mileage, dailyContents uint32) []byte {
	p := make([]byte, 0, 9)
	p = append(p, 1)
	p = add32(p, mileage)
	p = add32(p, dailyContents)
	return p
}

// MileageUpdate encodes NOTI 524 (0x20c, 13 bytes) to update the client's
// Mileage balance and emit the native system chat rebate/reward notification.
func MileageUpdate(reason byte, oldMileage, newMileage, dailyContents uint32) []byte {
	p := make([]byte, 0, 13)
	p = append(p, reason)
	p = add32(p, oldMileage)
	p = add32(p, newMileage)
	p = add32(p, dailyContents)
	return p
}
