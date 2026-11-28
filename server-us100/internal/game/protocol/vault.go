package protocol

import "fmt"

// PersonalVaultSpace / PersonalVault2Space / AccountVaultSpace are the US224
// NOTI 13 inventory space IDs verified in DFO_unpacked.exe 0x1434fc079..0x1434fc18a.
const (
	PersonalVaultSpace  byte = 0x02
	AccountVaultSpace   byte = 0x0c
	PersonalVault2Space byte = 0x2d

	CmdOpenAccountVault    uint16 = 305 // 0x131 (0x144ae10d6 -> ACK 0x1434a3770)
	CmdUpgradeAccountVault uint16 = 306 // 0x132 (0x144ae0fac -> ACK 0x1434c76c0)
)

// IsVaultSpace reports whether space is one of the three US224 vault spaces:
// Personal Safe I (0x02), Personal Safe II (0x2d), or Account Vault (0x0c).
func IsVaultSpace(space byte) bool {
	return space == PersonalVaultSpace || space == PersonalVault2Space || space == AccountVaultSpace
}

// US224PersonalVaultRestore encodes NOTI 13 space 2 (Personal Safe I / 安全I),
// verified at DFO_unpacked.exe 0x1434fc079..0x1434fc0dc:
//   u8 space=0x02, u16 slotCapacity, u16 itemCount, itemCount * 0xb1 bytes.
func US224PersonalVaultRestore(slots uint16, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if slots == 0 {
		return nil, fmt.Errorf("vault capacity must select a valid client grade")
	}
	return us224OrdinaryList(add16([]byte{PersonalVaultSpace}, slots), rows)
}

// EmptyPersonalVault encodes an empty NOTI 13 space 2 payload.
func EmptyPersonalVault(slots uint16) ([]byte, error) {
	return US224PersonalVaultRestore(slots, nil)
}

// US224PersonalVault2Restore encodes NOTI 13 space 0x2d (Personal Safe II / 安全II),
// verified at DFO_unpacked.exe 0x1434fc0e1..0x1434fc14c:
//   u8 space=0x2d, u16 slotCapacity, u16 itemCount, itemCount * 0xb1 bytes.
func US224PersonalVault2Restore(slots uint16, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if slots == 0 {
		return nil, fmt.Errorf("vault2 capacity must select a valid client grade")
	}
	return us224OrdinaryList(add16([]byte{PersonalVault2Space}, slots), rows)
}

// EmptyPersonalVault2 encodes an empty NOTI 13 space 0x2d payload.
func EmptyPersonalVault2(slots uint16) ([]byte, error) {
	return US224PersonalVault2Restore(slots, nil)
}

// US224AccountVaultRestore encodes NOTI 13 space 0x0c (Account Vault / 账号金库),
// verified at DFO_unpacked.exe 0x1434fc14e..0x1434fc18a:
//   u8 space=0x0c, u16 slotCapacity, u32 gold, u16 itemCount, itemCount * 0xb1 bytes.
func US224AccountVaultRestore(slots uint16, gold uint32, rows [][CurrentItemRecordSize]byte) ([]byte, error) {
	if slots == 0 {
		return nil, fmt.Errorf("account vault capacity must select a valid client grade")
	}
	hdr := add32(add16([]byte{AccountVaultSpace}, slots), gold)
	return us224OrdinaryList(hdr, rows)
}

// EmptyAccountVault encodes an empty NOTI 13 space 0x0c payload.
func EmptyAccountVault(slots uint16, gold uint32) ([]byte, error) {
	return US224AccountVaultRestore(slots, gold, nil)
}

// PersonalVaultUpgradeNoti encodes NOTI 66 (0x42) subType 1 (Personal Safe I
// capacity upgrade), verified at DFO_unpacked.exe 0x1434ef324..0x1434ef3ab:
//   u16 subType=1, u16 newSlotCapacity -> SetPersonalVaultCapacity(cap, 0) + UI refresh.
func PersonalVaultUpgradeNoti(slots uint16) []byte {
	return add16(add16(nil, 1), slots)
}

// PersonalVault2UpgradeNoti encodes NOTI 66 (0x42) subType 22 (0x16, Personal
// Safe II capacity upgrade), verified at DFO_unpacked.exe 0x1434ef3b0..0x1434ef400:
//   u16 subType=22, u16 newSlotCapacity -> SetPersonalVaultCapacity(cap, 1) + UI refresh.
func PersonalVault2UpgradeNoti(slots uint16) []byte {
	return add16(add16(nil, 22), slots)
}

// AccountVaultAckSuccess encodes the 1-byte success reply consumed by ACK 305
// (0x1434a3770) and ACK 306 (0x1434c76c0): byte 0 = 1 advances client Account
// Vault capacity and refreshes tab 2 immediately.
func AccountVaultAckSuccess() []byte {
	return []byte{1}
}
