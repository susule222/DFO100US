package main

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/binary"
	"path/filepath"
	"testing"
)

func TestSeenCinematicsAndSkipAllOption(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{DBPath: filepath.Join(t.TempDir(), "cinematic.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const account int64 = 42

	// 1. Default state (no saved option 82, no seen cinematics): NOTI 1352 is 5004 bytes all zero mask.
	p0, err := seenCinematicsOnEntry(store, account)
	if err != nil {
		t.Fatal(err)
	}
	if len(p0) != 4+protocol.SeenCinematicBytes || binary.LittleEndian.Uint32(p0[0:4]) != protocol.SeenCinematicBytes {
		t.Fatalf("unexpected NOTI 1352 header/size: len=%d", len(p0))
	}
	if p0[4] != 0 || p0[len(p0)-1] != 0 {
		t.Fatalf("expected empty mask by default")
	}

	// 2. CMD 1417 records cinematic ID 1234 (byte 154, bit 2) and persists across entry reads.
	id, err := protocol.DecodeRecordSeenCinematic([]byte{0xd2, 0x04})
	if err != nil || id != 1234 {
		t.Fatalf("DecodeRecordSeenCinematic failed: id=%d err=%v", id, err)
	}
	if err := recordSeenCinematic(store, account, id); err != nil {
		t.Fatal(err)
	}
	p1, err := seenCinematicsOnEntry(store, account)
	if err != nil {
		t.Fatal(err)
	}
	if (p1[4+(1234>>3)] & (1 << (1234 & 7))) == 0 {
		t.Fatalf("expected cinematic 1234 bit set in NOTI 1352")
	}
	if (p1[4+(1235>>3)] & (1 << (1235 & 7))) != 0 {
		t.Fatalf("unexpected bit 1235 set")
	}

	// 3. Enabling Option 82 == 1 ("跳过所有过场动画") via CMD 197 makes NOTI 1352 return all 0xFF.
	body := make([]byte, 448)
	binary.LittleEndian.PutUint32(body[0:4], generalOptionBytes)
	binary.LittleEndian.PutUint16(body[4+uiSizeIndex*2:], 75)
	binary.LittleEndian.PutUint16(body[4+cinematicSkipOptionIndex*2:], cinematicSkipModeAll)
	if err := saveGeneralOptions(store, account, body); err != nil {
		t.Fatal(err)
	}
	pAll, err := seenCinematicsOnEntry(store, account)
	if err != nil {
		t.Fatal(err)
	}
	if len(pAll) != 4+protocol.SeenCinematicBytes {
		t.Fatalf("unexpected all-skip NOTI 1352 size: %d", len(pAll))
	}
	for i := 4; i < len(pAll); i++ {
		if pAll[i] != 0xff {
			t.Fatalf("expected all 0xff mask at byte %d, got 0x%02x", i, pAll[i])
		}
	}

	// 4. Switching Option 82 back to 0 restores the persisted seen bitmask (only 1234 set).
	binary.LittleEndian.PutUint16(body[4+cinematicSkipOptionIndex*2:], 0)
	if err := saveGeneralOptions(store, account, body); err != nil {
		t.Fatal(err)
	}
	pBack, err := seenCinematicsOnEntry(store, account)
	if err != nil {
		t.Fatal(err)
	}
	if (pBack[4+(1234>>3)] & (1 << (1234 & 7))) == 0 {
		t.Fatalf("persisted cinematic 1234 bit lost after disabling skip-all")
	}
	if (pBack[4+(1235>>3)] & (1 << (1235 & 7))) != 0 {
		t.Fatalf("expected non-seen cinematic 1235 cleared after disabling skip-all")
	}
}

func TestStoryDigestLevelAndSkipAllOption(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{DBPath: filepath.Join(t.TempDir(), "storydigest.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const account int64 = 42
	const charID int64 = 48

	// 1. Default state: NOTI 1370 sends 0 when skip-all is off and no CMD 1438 recorded.
	p0, err := storyDigestLevelOnEntry(store, account, charID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(p0) != 4 || binary.LittleEndian.Uint32(p0) != 0 {
		t.Fatalf("expected default NOTI 1370 level 0, got %x", p0)
	}

	// 2. CMD 1438 records character level 100 and is monotonic.
	if err := recordStoryDigestLevel(store, charID, 100); err != nil {
		t.Fatal(err)
	}
	if err := recordStoryDigestLevel(store, charID, 50); err != nil {
		t.Fatal(err)
	}
	p1, err := storyDigestLevelOnEntry(store, account, charID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(p1) != 100 {
		t.Fatalf("expected persisted level 100, got %d", binary.LittleEndian.Uint32(p1))
	}

	// 3. Option 82 == 1 ("跳过所有过场动画") returns the character's exact current level
	// even for a fresh character with no CMD 1438 record so StoryDigest never opens.
	body := make([]byte, 448)
	binary.LittleEndian.PutUint32(body[0:4], generalOptionBytes)
	binary.LittleEndian.PutUint16(body[4+uiSizeIndex*2:], 75)
	binary.LittleEndian.PutUint16(body[4+cinematicSkipOptionIndex*2:], cinematicSkipModeAll)
	if err := saveGeneralOptions(store, account, body); err != nil {
		t.Fatal(err)
	}
	pSkip, err := storyDigestLevelOnEntry(store, account, 999, 100)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(pSkip) != 100 {
		t.Fatalf("expected skip-all NOTI 1370 level 100, got %d", binary.LittleEndian.Uint32(pSkip))
	}

	// 4. Verify NOTI 1370 is scheduled BEFORE NOTI 124 (ENTER_GAMEWORLD_COMPLETE).
	plan := entryPayloads{
		SeenCinematics:   protocol.SeenCinematicsAllPayload(),
		StoryDigestLevel: protocol.StoryDigestLevelPayload(100),
		Complete:         protocol.EnterGameworldComplete(),
	}
	pkts := plan.packets()
	digestIdx, completeIdx := -1, -1
	for i, pkt := range pkts {
		if pkt.ID == protocol.NotiStoryDigestLevel {
			digestIdx = i
		}
		if pkt.ID == 124 {
			completeIdx = i
		}
	}
	if digestIdx < 0 || completeIdx < 0 || digestIdx >= completeIdx {
		t.Fatalf("expected NOTI 1370 before NOTI 124, got digestIdx=%d completeIdx=%d", digestIdx, completeIdx)
	}
}
