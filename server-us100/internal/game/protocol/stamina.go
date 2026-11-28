package protocol

import "encoding/binary"

// Verified US100 stamina / weakness recovery opcodes from DFO_unpacked.exe:
//   - CMD 9 (0x09, sender 0x14492a119): 0-byte payload, Shusia Stamina Recovery confirm
//   - ACK 9 (0x09, handler 0x1434b83d0):
//       success: u8(1) + u32(newGold) -> Inventory::SetGold(newGold, true),
//                LocalPlayer::SetStamina(100), LocalPlayer::RestoreTownHPMP(true),
//                system chat 0x2b7, SFX "STAMINA_RECOVER"
//       refused: u8(0) + u16(code) (0x16=insufficient gold 0x2b9, 0x12=cannot recover 0x2b8)
//   - NOTI 33 (0x21, handler 0x1434db540): u8(stamina 0..100), silent stamina + town HP/MP sync
//   - NOTI 4  (0x04, handler 0x143527ca0): u8(stamina 0..100), stamina + town HP/MP + SFX if 100
const (
	CmdRecoverStamina uint16 = 9
	NotiStaminaUpdate uint16 = 4
	NotiStaminaSync   uint16 = 33

	StaminaRecoveryErrCannotRecover    uint16 = 0x12 // 18 -> string 0x2b8
	StaminaRecoveryErrInsufficientGold uint16 = 0x16 // 22 -> string 0x2b9
)

// StaminaRecoveryCostTable is the exact 110-level [stamina recovery cost] array
// extracted from US100 Script.pvf (etc/(f)serverparameter.etc), indexed by level-1.
var StaminaRecoveryCostTable = [110]uint32{
	0, 0, 0, 0, 0, 253, 713, 1173, 1633, 2092,
	2552, 3012, 3472, 3932, 4392, 4852, 5312, 5772, 8309, 8922,
	9535, 10148, 10761, 11375, 11988, 12601, 13214, 13827, 14441, 15054,
	15667, 16280, 16893, 17507, 18120, 18733, 19346, 19959, 20573, 21186,
	21799, 22412, 23025, 23639, 24252, 24865, 25478, 26091, 26705, 30733,
	31422, 32112, 32802, 33492, 34182, 34872, 35562, 36251, 36941, 37631,
	38321, 39011, 39701, 40390, 41080, 41770, 42460, 43150, 43840, 44530,
	45219, 45909, 46599, 47289, 47979, 48669, 49358, 50048, 50738, 51428,
	52118, 52808, 53498, 54187, 54877, 55567, 56257, 56947, 57637, 58327,
	59016, 59706, 60396, 61086, 61776, 62466, 63156, 63845, 64535, 65225,
	65915, 66605, 67295, 67985, 68675, 69365, 70055, 70745, 71435, 72125,
}

// StaminaRecoveryCost computes the exact Shusia weakness recovery fee matching
// DFO_unpacked.exe 0x14592ba90: ((100 - stamina) * table[level-1]) / 90.
func StaminaRecoveryCost(level, stamina uint32) uint32 {
	if stamina >= 100 || level < 6 {
		return 0
	}
	idx := level - 1
	if idx >= uint32(len(StaminaRecoveryCostTable)) {
		idx = uint32(len(StaminaRecoveryCostTable)) - 1
	}
	return ((100 - stamina) * StaminaRecoveryCostTable[idx]) / 90
}

// StaminaRecoverySuccess encodes ACK 9 (0x09) success: u8(1) + u32(newGold).
func StaminaRecoverySuccess(newGold uint32) []byte {
	p := make([]byte, 5)
	p[0] = 1
	binary.LittleEndian.PutUint32(p[1:5], newGold)
	return p
}

// StaminaRecoveryRefused encodes ACK 9 (0x09) failure: u8(0) + u16(errorCode).
func StaminaRecoveryRefused(code uint16) []byte {
	p := make([]byte, 3)
	p[0] = 0
	binary.LittleEndian.PutUint16(p[1:3], code)
	return p
}

// StaminaSync encodes NOTI 33 (0x21) or NOTI 4 (0x04): 1-byte u8 stamina (0..100).
func StaminaSync(stamina byte) []byte {
	if stamina > 100 {
		stamina = 100
	}
	return []byte{stamina}
}
