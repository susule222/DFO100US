package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	// CmdRecordSeenCinematic (0x589, sender 0x141f974dd) reports a u16 cinematic ID
	// watched by the client (< 40000).
	CmdRecordSeenCinematic uint16 = 1417

	// NotiSeenCinematics (0x548, handler 0x141f9a8d0 -> 0x144e6eda0) pushes the
	// 40000-bit seen-cinematic table (u32 5000 + 5000 bitmask bytes) into
	// CinematicManager+0x150.
	NotiSeenCinematics uint16 = 1352

	// NotiStoryDigestLevel (0x55a, registered at 0x140015756 -> handler 0x142851280)
	// pushes a u32 level into StoryDigestManager+0x8c. On NOTI 124
	// (ENTER_GAMEWORLD_COMPLETE -> 0x14351dbd6 -> 0x142852ec0), the client compares
	// Digest.etc entries for the character's current level against this value and
	// opens StoryDigest (Type 1723) + MoviePlayer (Type 1455) if not covered.
	NotiStoryDigestLevel uint16 = 1370

	// CmdStoryDigestUpdate (0x59e, ENUM_CMDPACKET_STORY_DIGEST_UPDATE, sender
	// 0x142853093) notifies the server (0 payload bytes) when the client starts
	// StoryDigest for the character's current level so it is persisted across
	// client launches.
	CmdStoryDigestUpdate uint16 = 1438

	SeenCinematicMaxID = 40000 // 0x9c40 entries in CinematicManager+0x150
	SeenCinematicBytes = SeenCinematicMaxID / 8
)

// StoryDigestLevelPayload encodes NOTI 1370 (0x55a): u32 last-seen character level.
func StoryDigestLevelPayload(level uint32) []byte {
	p := make([]byte, 4)
	binary.LittleEndian.PutUint32(p[0:4], level)
	return p
}

// SeenCinematicsPayload encodes NOTI 1352 (0x548): u32 5000 + 5000 raw bitmask bytes.
func SeenCinematicsPayload(mask []byte) []byte {
	p := make([]byte, 4+SeenCinematicBytes)
	binary.LittleEndian.PutUint32(p[0:4], SeenCinematicBytes)
	if len(mask) > 0 {
		n := len(mask)
		if n > SeenCinematicBytes {
			n = SeenCinematicBytes
		}
		copy(p[4:4+n], mask[:n])
	}
	return p
}

// SeenCinematicsAllPayload encodes NOTI 1352 with every cinematic ID (0..39999)
// marked seen (all 0xFF) so Option 82 == 1 ("跳过所有过场动画") skips every
// skippable cutscene immediately, including first-time cutscenes.
func SeenCinematicsAllPayload() []byte {
	p := make([]byte, 4+SeenCinematicBytes)
	binary.LittleEndian.PutUint32(p[0:4], SeenCinematicBytes)
	for i := 4; i < len(p); i++ {
		p[i] = 0xff
	}
	return p
}

// DecodeRecordSeenCinematic decodes CMD 1417 (0x589): u16 cinematicID.
func DecodeRecordSeenCinematic(p []byte) (uint16, error) {
	if len(p) < 2 {
		return 0, fmt.Errorf("record seen cinematic requires at least 2 bytes, got %d", len(p))
	}
	id := binary.LittleEndian.Uint16(p[0:2])
	if int(id) >= SeenCinematicMaxID {
		return 0, fmt.Errorf("cinematic id %d out of range [0,%d)", id, SeenCinematicMaxID)
	}
	return id, nil
}
