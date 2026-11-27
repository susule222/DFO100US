package main

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/inventory"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"dfolan/internal/world"
)

func TestLinasBraceletMeeting(t *testing.T) {
	ctx := context.Background()
	qc, err := catalog.LoadQuests("../../configs/us100/quests.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := catalog.LoadWorld("../../configs/us100/world.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "meeting.db")
	input := os.Getenv("US100_TEST_BRACELET_DB")
	if input != "" {
		b, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := storage.Open(ctx, storage.Config{DBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if input == "" {
		err = store.Database().Mutate(func(tx *db100.Tx) error {
			bag := inventory.Bag{Version: "ordinary-bag-v1", Items: []inventory.BagItem{{Slot: 177, Template: 10165055, Amount: 1}}}
			if err := tx.Put("characters", "48", map[string]any{"id": 48, "account_id": 41, "wire_id": 1, "config_version": qc.Source.Checksum, "state": map[string]any{"inventory": bag}}); err != nil {
				return err
			}
			return tx.Put("character_quests", "48/4873", map[string]any{"character_id": 48, "quest_id": 4873, "status": "accepted", "progress": 1, "config_version": qc.Source.Checksum, "progress_model": quest.SingleMeetNPC})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	role, err := store.LiveCharacter(41, 48)
	if err != nil {
		t.Fatal(err)
	}
	w := worldSession{role: role, service: &world.Service{Store: store, Catalog: wc}, quests: &quest.Service{Store: store, Catalog: qc}}
	check := func(left uint32) {
		t.Helper()
		active, err := w.quests.Active(ctx, role)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, q := range active {
			if q.ID == 4873 && q.Progress == left {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected active quest 4873 remaining %d, got %+v", left, active)
		}
		r, err := store.LiveCharacter(41, 48)
		if err != nil {
			t.Fatal(err)
		}
		bag, err := inventory.ReadBag(r.State)
		if err != nil {
			t.Fatal(err)
		}
		var count uint32
		for _, item := range bag.Items {
			if item.Template == 10165055 {
				if item.Slot != 177 {
					t.Fatalf("unexpected quest slot %d", item.Slot)
				}
				count += item.Amount
			}
		}
		if count != 1 {
			t.Fatalf("bracelet quantity %d", count)
		}
	}
	short := []byte{0x21, 0, 9, 0x13, 0, 0, 0, 0}
	check(1)
	w.state.Position = storage.WorldPosition{Town: 65535}
	if _, err = w.questInteraction(short); err == nil {
		t.Fatal("accepted absent NPC")
	}
	w.state.Position = storage.WorldPosition{Town: 38, Area: 0, X: 1027, Y: 237}
	for _, invalid := range [][]byte{short[:7], append(append([]byte{}, short...), 0), {33, 0, 9, 0x13, 1, 0, 0, 0}, {34, 0, 9, 0x13, 0, 0, 0, 0}} {
		if _, err = w.questInteraction(invalid); err == nil {
			t.Fatal("accepted malformed check")
		}
	}
	check(1)
	for _, p := range [][]byte{short, short, append(append([]byte{}, short...), make([]byte, 8)...)} {
		packets, err := w.questInteraction(p)
		if err != nil || len(packets) != 1 || packets[0].ID != 291 {
			t.Fatalf("NPC check response: %+v, %v", packets, err)
		}
		b := packets[0].Payload
		found := false
		for i := 0; i < int(binary.LittleEndian.Uint16(b)); i++ {
			row := b[2+6*i:]
			if binary.LittleEndian.Uint16(row) == 4873 && binary.LittleEndian.Uint32(row[2:]) == 0 {
				found = true
			}
		}
		if !found {
			t.Fatal("missing ready-to-submit trigger")
		}
		check(0)
	}
}
