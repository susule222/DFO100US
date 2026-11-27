package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestUS100SkillDrag(t *testing.T) {
	for _, tc := range []struct {
		payload  string
		from, to byte
	}{
		{"000012ffffffff00992ea16100000000", 0, 18},
		{"000112ffffffff0068af94ce00000000", 1, 18},
	} {
		p, _ := hex.DecodeString(tc.payload)
		got, err := DecodeSkillMove(p)
		if err != nil || got != (SkillMove{From: tc.from, To: tc.to}) {
			t.Fatalf("move: %+v %v", got, err)
		}
		for _, pos := range []int{3, 7, 8, 11, 15} {
			bad := bytes.Clone(p)
			bad[pos] ^= 1
			if _, err := DecodeSkillMove(bad); err == nil {
				t.Fatalf("accepted changed byte %d", pos)
			}
		}
	}
}

func TestLive27SkillDragDigest(t *testing.T) {
	for _, h := range []string{"000100ffffffff78f9b72d0000000000", "000200ffffffffce77974c0000000000"} {
		p, _ := hex.DecodeString(h)
		r, e := DecodeSkillMove(p)
		if e != nil || r.To != 0 {
			t.Fatal(r, e)
		}
		p[7] ^= 1
		if _, e = DecodeSkillMove(p); e == nil {
			t.Fatal("corrupted native digest accepted")
		}
	}
}
func TestLive27ClearResultDigest(t *testing.T) {
	b, e := os.ReadFile("testdata/live27_play_result.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Payload string `json:"payload_hex"`
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	p, e := hex.DecodeString(f.Payload)
	if e != nil {
		t.Fatal(e)
	}
	r, e := DecodePlayResult(p)
	if e != nil || r.Actor != 1 || r.RankPoint != 90 {
		t.Fatal(r, e)
	}
	p[113] ^= 1
	if _, e = DecodePlayResult(p); e == nil {
		t.Fatal("forged settlement digest accepted")
	}
}
