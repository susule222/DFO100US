package main

import (
	"bytes"
	"dfolan/internal/dungeon"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
)

func TestTownWornSlotsOnceAfterActorReady(t *testing.T) {
	st := json.RawMessage(`{"inventory":{"version":"ordinary-bag-v1","avatars":[{"slot":0,"template":39129}],"worn_avatars":[{"slot":8,"template":42215},{"slot":0,"template":39165}],"worn":[{"slot":19,"template":20002}]}}`)
	w := worldSession{role: storage.Character{ID: 48, State: st}, wornScenePending: true}
	before := append([]byte(nil), st...)
	calls, events := 0, 0
	send := func(kind byte, id uint16, p []byte) error {
		calls++
		if kind != 0 || id != 14 || len(p) != 3+185*2+177 || p[0] != 3 || binary.LittleEndian.Uint16(p[1:]) != 3 {
			t.Fatal("wrong worn replay")
		}
		restore, err := inventory.WornPayload(st)
		if err != nil || !bytes.Equal(restore, p) {
			t.Fatal("slot data disagrees with restore", err)
		}
		return nil
	}
	log := func(e map[string]any) {
		events++
		if e["character_id"] != int64(48) || e["avatar_count"] != 2 {
			t.Fatal(e)
		}
	}
	w.activeDungeon = &dungeon.Session{}
	if e := w.refreshWornAfterPosition(send, log); e != nil || calls != 0 || !w.wornScenePending {
		t.Fatal("sent in dungeon", e)
	}
	w.activeDungeon = nil
	if e := w.refreshWornAfterPosition(func(byte, uint16, []byte) error { return errors.New("write failed") }, log); e == nil || !w.wornScenePending || events != 0 {
		t.Fatal("failed write was acknowledged")
	}
	for i := 0; i < 3; i++ {
		if e := w.refreshWornAfterPosition(send, log); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 1 || events != 1 || !bytes.Equal(before, w.role.State) {
		t.Fatal("duplicate send or character mutation")
	}
	w.role = storage.Character{ID: 49, State: json.RawMessage(`{"level":1}`)}
	w.wornScenePending = true
	if e := w.refreshWornAfterPosition(send, log); e != nil || calls != 1 || w.wornScenePending {
		t.Fatal("stale projection following role switch", e)
	}
}
