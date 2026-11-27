package main

import (
	"encoding/hex"
	"testing"
)

func TestAshenFirstPurchaseAndRetry(t *testing.T) {
	cs, w := skillReplayFixture(t) // independent role/account, not the original test save
	payload, e := hex.DecodeString("0001100000010002183cbb0000000000")
	if e != nil {
		t.Fatal(e)
	}
	var sess skillSession
	for i := 0; i < 2; i++ {
		plan, err := sess.handle(cs, w, 29, payload, payload)
		if err != nil || len(plan) != 1+i || plan[0].ID != 29 {
			t.Fatalf("attempt %d: %+v %v", i, plan, err)
		}
		b := plan[0].Payload
		if len(b) != 15 || b[2] != 105 || b[3] != 0 || b[8] != 16 || b[10] != 1 {
			t.Fatalf("unexpected ACK %x", b)
		}
		if i == 1 && plan[1].ID != 19 {
			t.Fatal("retry must resynchronize")
		}
	}
}
