package main

import (
	"bytes"
	"context"
	"dfolan/internal/admin"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGMSelectSwitchAndCoalesce(t *testing.T) {
	var s gmSessions
	ch, other := make(chan struct{}, 1), make(chan struct{}, 1)
	before := s.beforeSelect()
	s.committed(39)
	s.bind(other, 40, before)
	s.bind(ch, 39, before)
	if len(ch) != 1 || len(other) != 0 {
		t.Fatal("missed grant during entry or wrong recipient")
	}
	s.committed(39)
	s.committed(39)
	if len(ch) != 1 {
		t.Fatal("snapshot refreshes did not coalesce")
	}
	s.bind(ch, 40, s.beforeSelect())
	s.committed(39)
	if len(ch) != 0 {
		t.Fatal("stale push after switching characters")
	}
	s.bind(other, 0, 0)
	s.committed(40)
	if len(ch) != 1 || len(other) != 0 {
		t.Fatal("incorrect logout routing")
	}
}

func TestOnlineGMCommitAndRefresh(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "online-gm-test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join("..", "..", "configs", "us100")
	items, err := catalog.LoadLoot(filepath.Join(cfg, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := inventory.LoadBagRules(filepath.Join(cfg, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog(filepath.Join(cfg, "equipment.generated.json"), items.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{AccountID: account, Name: "online", ConfigVersion: items.Source.Checksum, State: json.RawMessage("{}")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	w := worldSession{role: role, loot: &loot.Service{Store: store, Catalog: items}}
	w.role.WireID = 88
	var hub gmSessions
	ch := make(chan struct{}, 1)
	hub.bind(ch, role.ID, hub.beforeSelect())
	svc := admin.Service{Store: store, Awarder: &inventory.Awarder{Catalog: items, Rules: rules, Equipment: gear}}
	if err = os.Mkdir(filepath.Join(dir, "pending"), 0700); err != nil {
		t.Fatal(err)
	}
	grant := func(key string, id uint32) {
		t.Helper()
		b, _ := json.Marshal(storage.Grant{Character: role.ID, Reason: "test", Items: []storage.GrantItem{{Template: id, Amount: 1}}})
		if err := os.WriteFile(filepath.Join(dir, "pending", key+".json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if err := admin.DrainAndNotify(dir, &svc, account, hub.committed); err != nil {
			t.Fatal(err)
		}
	}
	grant("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 10165055)
	grant("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 10165055)
	grant("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 10018)
	if len(ch) != 1 {
		t.Fatal("no active session update")
	}
	<-ch
	var sent []byte
	send := func(kind byte, id uint16, p []byte) error {
		if kind != 0 || id != 14 {
			t.Fatalf("unexpected inventory message %d/%d", kind, id)
		}
		sent = append([]byte(nil), p...)
		return nil
	}
	if err = w.syncGrantedBag(account, role.ID, send, func(map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	current, err := store.LiveCharacter(account, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	bag, err := inventory.ReadBag(current.State)
	if err != nil || len(bag.Items) != 1 || bag.Items[0].Amount != 1 || bag.Items[0].Template != 10165055 || len(bag.Equipment) != 1 {
		t.Fatalf("wrong saved bag %+v %v", bag, err)
	}
	want, err := protocol.US224InventoryUpdate(bag.Rows())
	if err != nil || !bytes.Equal(want, sent) || !bytes.Equal(current.State, w.role.State) || w.role.WireID != 88 {
		t.Fatal("online contents or actor changed")
	}
	grant("cccccccccccccccccccccccccccccccc", 0)
	if len(ch) != 0 {
		t.Fatal("rejected grant signaled success")
	}
}
