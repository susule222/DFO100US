package main

import (
	"dfolan/internal/game/protocol"
	"dfolan/internal/game/wire"
	"testing"
)

// TestDungeonRequestsReachVerifiedHandlers pins the ids that have a handler in
// the dispatcher. A command with a handler that is missing from the retention
// list keeps working only until BodySampleLimit occurrences, after which its
// body is never decoded and it is dispatched as if the checksum had failed.
// Id 217 is in this list for exactly that reason: it is the US100 client's
// dungeon scene report, and it used to be reported as a checksum failure from
// the ninth frame of a connection onward.
func TestDungeonRequestsReachVerifiedHandlers(t *testing.T) {
	keys := make([]byte, wire.SessionKeyBytes)
	for i := range keys {
		keys[i] = byte(i%127 + 1)
	}
	for _, id := range []uint16{6, 15, 16, 28, 29, 37, 39, 42, 43, 45, 46, 69, 70, 71, 72, 117, 132, 191, 217, 637} {
		if !observedGameRequest(id) {
			t.Fatalf("implemented command%d never decrypted", id)
		}
	}
	if observedGameRequest(2127) {
		t.Fatal("retaining process scan")
	}
	p := make([]byte, 21)
	p[0] = 9
	p[1] = 16
	c, e := wire.EncryptPayload(keys, 43, p)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := wire.DecryptPayload(keys, 43, c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = protocol.DecodePickup(decoded); e != nil {
		t.Fatal("pickup decoder refuses actual cipher padding", len(decoded), e)
	}
}
