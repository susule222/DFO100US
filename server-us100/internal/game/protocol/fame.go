package protocol

import "fmt"

// AdventurerFameNotiID is the US100 (2.24) NOTI packet (0x8d8) handled at
// 0x1434f12e0: reads u16 actor_server_id, u16 unused, u32 fame and applies it
// through 0x143f0b9f0 -> 0x143d4e100 ([actor+0x1724c]).
const AdventurerFameNotiID uint16 = 2264

func AdventurerFameUpdate(actorID uint16, fame uint32) ([]byte, error) {
	if actorID == 0 || actorID == 0xffff {
		return nil, fmt.Errorf("invalid fame actor identity")
	}
	p := add16(nil, actorID)
	p = add16(p, 0)
	return add32(p, fame), nil
}
