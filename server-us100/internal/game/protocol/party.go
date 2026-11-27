package protocol

import (
	"encoding/binary"
	"fmt"
)

// SoloPartyInfo is v2.24 NOTI9 for a member on the local gateway's own
// channel with zero context bytes.
func SoloPartyInfo(actor uint16) ([]byte, error) {
	return SoloPartyInfoContext(actor, [2]byte{})
}

// SoloPartyInfoContext is v2.24 NOTI9 for a member on the local gateway's
// [serverID, channelID] channel. Bytes 6 and 7 are read into [rsp+0x65]
// (serverID) and [rsp+0x64] (channelID) at 143500ec3/143500ed0 and compared
// against g_curServerID/g_curChannelID at 143501509/143501561. The ordinary
// block's count, slot and actor are read at 14350143b, 14350149b and
// 1435014b0. The local-member branch ends at byte 72.
func SoloPartyInfoContext(actor uint16, context [2]byte) ([]byte, error) {
	if actor == 0 || actor == 65535 {
		return nil, fmt.Errorf("invalid solo party actor")
	}
	p := make([]byte, 72)
	binary.LittleEndian.PutUint16(p, 1)
	binary.LittleEndian.PutUint16(p[4:], 1)
	p[6], p[7] = context[0], context[1]
	p[15] = 1 // one-player capacity
	p[47] = 1 // one member, slot 0 at offset 48
	binary.LittleEndian.PutUint16(p[49:], actor)
	return p, nil
}
