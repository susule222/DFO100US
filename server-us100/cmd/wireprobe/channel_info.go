package main

import (
	"dfolan/internal/game/wire"
	"encoding/binary"
	"fmt"
)

const channelBannerKeyBytes = 1024

func appendProtoVarint(dst []byte, v uint64) []byte {
	for v >= 128 {
		dst = append(dst, byte(v&127|128))
		v >>= 7
	}
	return append(dst, byte(v))
}

func appendProtoUint(dst []byte, field uint64, v uint64) []byte {
	dst = appendProtoVarint(dst, field<<3)
	return appendProtoVarint(dst, v)
}

func appendProtoBytes(dst []byte, field uint64, b []byte) []byte {
	dst = appendProtoVarint(dst, field<<3|2)
	dst = appendProtoVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

// buildChannelInfoFrame builds the initial PB_ENUM_NOTIPACKET_CHANNELINFO
// server frame (kind=0, id=1) carrying the accept flag (field 2), 1024-byte
// session key table (field 3), channel display name (field 4), server group ID
// (field 7 -> g_curServerID), and channel number (field 8 -> g_curChannelID)
// using the exact US100 v2.24 XOR/ROL2 + CRC32 framing.
func buildChannelInfoFrame(serverID, channelID uint32, channelName string, keys []byte) ([]byte, error) {
	if len(keys) != channelBannerKeyBytes {
		return nil, fmt.Errorf("channel banner requires %d key bytes, got %d", channelBannerKeyBytes, len(keys))
	}
	if serverID == 0 || channelID == 0 {
		return nil, fmt.Errorf("channel banner requires positive server_id (%d) and channel_id (%d)", serverID, channelID)
	}
	var pb []byte
	pb = appendProtoUint(pb, 2, 1)
	pb = appendProtoBytes(pb, 3, keys)
	pb = appendProtoBytes(pb, 4, []byte(channelName))
	pb = appendProtoUint(pb, 7, uint64(serverID))
	pb = appendProtoUint(pb, 8, uint64(channelID))
	plain := make([]byte, 4+len(pb))
	binary.LittleEndian.PutUint32(plain[:4], uint32(len(pb)))
	copy(plain[4:], pb)
	cipher := make([]byte, len(plain))
	for i, x := range plain {
		y := x ^ 0xb5
		cipher[i] = (y >> 6) | (y << 2)
	}
	return wire.ServerFrame(0, 1, cipher)
}
