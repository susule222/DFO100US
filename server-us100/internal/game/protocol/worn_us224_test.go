package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Walk the row lengths independently by template type, as the client does.
// Interleave the record kinds so surplus bytes cannot hide at the end.
func TestUS224WornMixedRestoreAndUpdate(t *testing.T) {
	rows := [][CurrentItemRecordSize]byte{OrdinaryItem(8, 42215, 0), OrdinaryItem(19, 20002, 0), EmptySlotItem(0), OrdinaryItem(1, 39592, 0), OrdinaryItem(21, 24002, 0)}
	for _, encode := range []func([][CurrentItemRecordSize]byte) ([]byte, error){US224WornRestore, US224WornUpdate, func(r [][CurrentItemRecordSize]byte) ([]byte, error) { return US224InventorySpaceUpdate(3, r) }} {
		p, e := encode(rows)
		if e != nil {
			t.Fatal(e)
		}
		if len(p) < 3 || p[0] != 3 || int(binary.LittleEndian.Uint16(p[1:])) != len(rows) {
			t.Fatal("header")
		}
		n := 3
		for _, r := range rows {
			if len(p)-n < 177 || !bytes.Equal(r[:177], p[n:n+177]) {
				t.Fatalf("bad record at %d", n)
			}
			n += 177
			switch binary.LittleEndian.Uint32(r[2:]) {
			case 42215, 39592:
				if len(p)-n < 8 || binary.LittleEndian.Uint64(p[n:]) != 0 {
					t.Fatal("missing avatar extensions")
				}
				n += 8
			}
		}
		if n != len(p) {
			t.Fatalf("trailing %d bytes", len(p)-n)
		}
	}
}
