package protocol

import (
	"encoding/binary"
	"testing"
)

func TestMileageRestoreAndUpdatePayloads(t *testing.T) {
	restore := MileageRestore(511, 14)
	if len(restore) != 9 {
		t.Fatalf("MileageRestore len = %d, want 9", len(restore))
	}
	if restore[0] != 1 {
		t.Fatalf("MileageRestore flag = %d, want 1", restore[0])
	}
	if got := binary.LittleEndian.Uint32(restore[1:5]); got != 511 {
		t.Fatalf("MileageRestore mileage = %d, want 511", got)
	}
	if got := binary.LittleEndian.Uint32(restore[5:9]); got != 14 {
		t.Fatalf("MileageRestore daily = %d, want 14", got)
	}

	upd := MileageUpdate(MileageReasonShop, 511, 531, 14)
	if len(upd) != 13 {
		t.Fatalf("MileageUpdate len = %d, want 13", len(upd))
	}
	if upd[0] != MileageReasonShop {
		t.Fatalf("MileageUpdate reason = %d, want %d", upd[0], MileageReasonShop)
	}
	if got := binary.LittleEndian.Uint32(upd[1:5]); got != 511 {
		t.Fatalf("MileageUpdate oldMileage = %d, want 511", got)
	}
	if got := binary.LittleEndian.Uint32(upd[5:9]); got != 531 {
		t.Fatalf("MileageUpdate newMileage = %d, want 531", got)
	}
	if got := binary.LittleEndian.Uint32(upd[9:13]); got != 14 {
		t.Fatalf("MileageUpdate daily = %d, want 14", got)
	}
}
