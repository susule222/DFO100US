package main

import (
	"context"
	"dfolan/internal/pluginhost"
	"dfolan/internal/storage"
	"path/filepath"
	"testing"
)

func TestWirePluginBackendKVAndSession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "plugin_test.db")
	store, err := storage.Open(context.Background(), storage.Config{DBPath: dbPath, SkipSeed: true})
	if err != nil {
		t.Fatalf("open db100 store: %v", err)
	}
	defer store.Close()

	var gm gmSessions
	var events []map[string]any
	backend := newWirePluginBackend(store, nil, 1001, &gm, func(ev map[string]any) {
		events = append(events, ev)
	})

	// Test KV put / get / delete isolation
	if err := backend.KVPut("my_mod", "counter", []byte(`{"n":123}`)); err != nil {
		t.Fatalf("KVPut failed: %v", err)
	}
	got, ok, err := backend.KVGet("my_mod", "counter")
	if err != nil || !ok || string(got) != `{"n":123}` {
		t.Fatalf("KVGet = (%q, %v, %v)", string(got), ok, err)
	}
	if err := backend.KVDelete("my_mod", "counter"); err != nil {
		t.Fatalf("KVDelete failed: %v", err)
	}
	if _, ok, err := backend.KVGet("my_mod", "counter"); err != nil || ok {
		t.Fatalf("expected deleted key to be absent, ok=%v err=%v", ok, err)
	}
	if err := backend.KVPut("../escape", "k", []byte(`1`)); err == nil {
		t.Fatalf("expected invalid namespace error")
	}

	// Test session registration, send, broadcast, and gm notify
	var sentCount int
	sid := backend.register(
		func(kind byte, id uint16, payload []byte) error {
			sentCount++
			return nil
		},
		func() pluginhost.SessionSnapshot {
			return pluginhost.SessionSnapshot{AccountID: 1001, CharacterID: 88, Level: 100, CharName: "TestRole"}
		},
	)
	ids := backend.ListSessions()
	if len(ids) != 1 || ids[0] != sid {
		t.Fatalf("ListSessions = %v, want [%d]", ids, sid)
	}
	snap, ok := backend.GetSessionInfo(sid)
	if !ok || snap.CharacterID != 88 || snap.CharName != "TestRole" {
		t.Fatalf("GetSessionInfo = (%+v, %v)", snap, ok)
	}
	if err := backend.SendPacket(sid, 0, 14, []byte{1, 2, 3}); err != nil {
		t.Fatalf("SendPacket: %v", err)
	}
	if err := backend.BroadcastPacket(0, 14, []byte{4, 5}); err != nil {
		t.Fatalf("BroadcastPacket: %v", err)
	}
	if sentCount != 2 {
		t.Fatalf("sentCount = %d, want 2", sentCount)
	}

	ch := make(chan struct{}, 1)
	gm.bind(ch, 88, gm.beforeSelect())
	if err := backend.NotifyCharacterSync(88); err != nil {
		t.Fatalf("NotifyCharacterSync: %v", err)
	}
	select {
	case <-ch:
	default:
		t.Fatalf("expected gm session channel wakeup")
	}

	backend.Log(pluginhost.LogInfo, "unit", "hello")
	if len(events) != 1 || events[0]["kind"] != "plugin_log" {
		t.Fatalf("expected plugin_log event, got %+v", events)
	}
	backend.unregister(sid)
	if len(backend.ListSessions()) != 0 {
		t.Fatalf("expected 0 sessions after unregister")
	}
}
