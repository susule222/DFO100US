package protocol

import (
	"bytes"
	"testing"
)

func TestUS224AccountMaterialsEncoding(t *testing.T) {
	// Reset with two cube entries (3035 Red x2, 3037 Clear x45)
	got, err := US224AccountMaterialsFromMap(true, map[uint32]uint32{
		3037: 45,
		3035: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0,          // mode = 0 (reset)
		2, 0, 0, 0, // count = 2
		0xdb, 0x0b, 0, 0, 2, 0, 0, 0, // 3035, 2
		0xdd, 0x0b, 0, 0, 45, 0, 0, 0, // 3037, 45
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("US224AccountMaterials mismatch:\n got: %x\nwant: %x", got, want)
	}

	// Incremental update mode = 1
	upd, err := US224AccountMaterials(false, []AccountMaterialEntry{{Template: 3037, Amount: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if upd[0] != 1 || len(upd) != 13 {
		t.Fatalf("unexpected incremental update: %x", upd)
	}
}
