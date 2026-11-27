package protocol

import (
	"bytes"
	"testing"
)

func TestMercenaryRequestBounds(t *testing.T) {
	for _, p := range [][]byte{nil, {8, 0, 0, 0, 0, 0, 0, 0}, {0, 1, 0, 0, 0, 0, 0, 0}} {
		if _, e := EmptyMercenaryInfo(p); e == nil {
			t.Fatalf("accepted malformed %x", p)
		}
	}
	p, e := EmptyMercenaryInfo([]byte{5, 0, 1, 2, 3, 4, 0, 0})
	// The reference client records an 8-byte successful empty response.
	// Check the empty count and every trailing byte, not just the prefix.
	if e != nil || !bytes.Equal(p, []byte{1, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("empty collection: %x %v", p, e)
	}
}
