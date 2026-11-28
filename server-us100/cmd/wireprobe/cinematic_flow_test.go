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
