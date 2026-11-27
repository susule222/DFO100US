package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDecodeItemSlotTargetAndUnlock(t *testing.T) {
	raw := []byte{0x00, 0x39, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	got, err := DecodeItemSlotTarget(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Space != 0 || got.Slot != 57 {
		t.Fatalf("got %+v, want space=0 slot=57", got)
	}
	if _, err := DecodeItemSlotTarget([]byte{0x00, 0x39, 0x00, 0x01, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected error for nonzero padding")
	}

	uRaw := []byte{0x01, 0x05, 0x00, 0x01, 0, 0, 0, 0}
	uGot, err := DecodeItemUnlock(uRaw)
	if err != nil {
		t.Fatal(err)
	}
	if uGot.Space != 1 || uGot.Slot != 5 || uGot.Flag != 1 {
		t.Fatalf("got %+v, want space=1 slot=5 flag=1", uGot)
	}
}

func TestItemLockEncoders(t *testing.T) {
	if got := SortLockSuccess(0, 57); !bytes.Equal(got, []byte{1, 0, 57, 0}) {
		t.Fatalf("SortLockSuccess = %x", got)
	}
	if got := SortLockRefused(4); !bytes.Equal(got, []byte{0, 4, 0, 0}) {
		t.Fatalf("SortLockRefused = %x", got)
	}
	if got := ItemLockSuccess(0, 12); !bytes.Equal(got, []byte{1, 0, 12, 0}) {
		t.Fatalf("ItemLockSuccess = %x", got)
	}
	if got := ItemUnlockSuccess(0, 12, 3600); len(got) != 8 || got[0] != 1 || got[1] != 0 || binary.LittleEndian.Uint16(got[2:4]) != 12 || binary.LittleEndian.Uint32(got[4:8]) != 3600 {
		t.Fatalf("ItemUnlockSuccess = %x", got)
	}
	if got := ItemUnlockRefused(0x13, 0); len(got) != 7 || got[0] != 0 || binary.LittleEndian.Uint16(got[1:3]) != 0x13 {
		t.Fatalf("ItemUnlockRefused = %x", got)
	}
	if got := ItemUnlockedNoti(3, 12); !bytes.Equal(got, []byte{3, 12, 0}) {
		t.Fatalf("ItemUnlockedNoti = %x", got)
	}

	noti, err := ItemLockListNoti([]ItemLockEntry{
		{Space: 0, Slot: 9, State: 1},
		{Space: 1, Slot: 2, State: 2, RemainingSeconds: 120},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		2, 0, // count=2
		0, 9, 0, 1, // space=0, slot=9, state=1
		1, 2, 0, 2, 120, 0, 0, 0, // space=1, slot=2, state=2, rem=120
	}
	if !bytes.Equal(noti, want) {
		t.Fatalf("ItemLockListNoti = %x, want %x", noti, want)
	}
}
