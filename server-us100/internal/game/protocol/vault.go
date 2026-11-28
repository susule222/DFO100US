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

// NOTI13, native 0x1434fc079: inventory space 2, u16 slot capacity, u16 item
// count. Empty rows skip 0xb1-byte item structures and have no further reads.
func EmptyPersonalVault(slots uint16) ([]byte, error) {
	if slots == 0 {
		return nil, fmt.Errorf("vault capacity must select a valid client grade")
	}
	return add16(add16([]byte{PersonalVaultSpace}, slots), 0), nil
}

// EmptyPersonalVault2 encodes NOTI 13 space 0x2d (Personal Safe II / 安全II),
// verified at DFO_unpacked.exe 0x1434fc0e1..0x1434fc14c:
//   u8 space=0x2d, u16 slotCapacity, u16 itemCount=0.
func EmptyPersonalVault2(slots uint16) ([]byte, error) {
	if slots == 0 {
		return nil, fmt.Errorf("vault2 capacity must select a valid client grade")
	}
	return add16(add16([]byte{PersonalVault2Space}, slots), 0), nil
}

// EmptyAccountVault encodes NOTI 13 space 0x0c (Account Vault / 账号金库),
// verified at DFO_unpacked.exe 0x1434fc14e..0x1434fc18a:
//   u8 space=0x0c, u16 slotCapacity, u32 gold, u16 itemCount=0.
func EmptyAccountVault(slots uint16, gold uint32) ([]byte, error) {
	if slots == 0 {
		return nil, fmt.Errorf("account vault capacity must select a valid client grade")
	}
	p := add16([]byte{AccountVaultSpace}, slots)
	p = add32(p, gold)
	return add16(p, 0), nil
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
