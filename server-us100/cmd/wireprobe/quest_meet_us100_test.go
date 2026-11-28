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

func TestQuestInteractionHandlesReachRangeSeekAndPhaseNPCs(t *testing.T) {
	ctx := context.Background()
	qc, err := catalog.LoadQuests("../../configs/us100/quests.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := catalog.LoadWorld("../../configs/us100/world.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "trigger_all.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	bag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Items: []inventory.BagItem{
			{Slot: 177, Template: 10164787, Amount: 1},
			{Slot: 178, Template: 10164789, Amount: 1},
		},
	}
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		if err := tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 10, "config_version": qc.Source.Checksum,
			"state": map[string]any{"level": 100, "inventory": bag},
		}); err != nil {
			return err
		}
		for _, q := range []struct {
			id    uint16
			model string
		}{
			{3160, quest.SingleReachRange},  // 40/3 [1034,180,100,100] (barricade stops at X=1033)
			{3162, quest.SingleReachRange},  // sub=0 NPC 13
			{3173, quest.SeekAndMeetNPC},    // items 10164787+10164789 + NPC 29
			{12152, quest.SingleMeetNPC},    // Lv.99 phase-only NPC 100000369
		} {
			if err := tx.Put("character_quests", "48/"+itoa(int(q.id)), map[string]any{
				"character_id": 48, "quest_id": int(q.id), "status": "accepted",
				"progress": 1, "config_version": qc.Source.Checksum, "progress_model": q.model,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	role, err := store.LiveCharacter(42, 48)
	if err != nil {
		t.Fatal(err)
	}
	w := worldSession{
		role:    role,
		service: &world.Service{Store: store, Catalog: wc},
		quests:  &quest.Service{Store: store, Catalog: qc},
	}
	cmd33 := func(qid uint16) []byte {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint16(b[0:2], 33)
		binary.LittleEndian.PutUint16(b[2:4], qid)
		return b
	}
	assertSettled := func(qid uint16) {
		t.Helper()
		active, err := w.quests.Active(ctx, role)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range active {
			if q.ID == qid {
				if q.Progress != 0 {
					t.Fatalf("quest %d still pending (progress=%d), want 0", qid, q.Progress)
				}
				return
			}
		}
		t.Fatalf("quest %d missing from active list", qid)
	}

	// 1a. Q3160 via CMD 33 in wrong area must be refused.
	w.state.Position = storage.WorldPosition{Town: 38, Area: 0, X: 561, Y: 234}
	if _, err := w.questInteraction(cmd33(3160)); err == nil {
		t.Fatal("accepted Q3160 CMD 33 from wrong area 38/0")
	}
	// 1b. Q3160 via ProximityProgress right at the barricade X=1033 (1px before 1034) must settle via RangeProximityMargin.
	if en := w.quests.Index().Entries[3160]; !en.Range.Contains(storage.WorldPosition{Town: 40, Area: 3, X: 1033, Y: 213}) {
		t.Fatal("Q3160 Range.Contains rejected barricade position (1033,213)")
	}
	// 1c. Q3160 via CMD 33 in target area 40/3 (even if last CMD 35 lagged at X=592) must settle and return NOTI 291.
	w.state.Position = storage.WorldPosition{Town: 40, Area: 3, X: 592, Y: 213}
	pkts, err := w.questInteraction(cmd33(3160))
	if err != nil || len(pkts) != 1 || pkts[0].ID != 291 {
		t.Fatalf("Q3160 CMD 33 failed: pkts=%+v err=%v", pkts, err)
	}
	assertSettled(3160)

	// 2. Q3162 ([reach the range] sub=0, NPC 13) via CMD 33 in 40/0 (where NPC 13 stands) must settle.
	w.state.Position = storage.WorldPosition{Town: 40, Area: 0, X: 500, Y: 250}
	if !w.service.HasNPC(w.state.Position, 13) {
		// Locate actual area containing NPC 13 in US100 catalog.
		for _, a := range wc.Areas {
			if w.service.HasNPC(storage.WorldPosition{Town: a.Town, Area: a.Area}, 13) {
				w.state.Position = storage.WorldPosition{Town: a.Town, Area: a.Area, X: 500, Y: 250}
				break
			}
		}
	}
	pkts, err = w.questInteraction(cmd33(3162))
	if err != nil || len(pkts) != 1 || pkts[0].ID != 291 {
		t.Fatalf("Q3162 CMD 33 failed: pkts=%+v err=%v", pkts, err)
	}
	assertSettled(3162)

	// 3. Q3173 ([seek n meet npc], items + NPC 29) via CMD 33 in NPC 29's area must settle.
	for _, a := range wc.Areas {
		if w.service.HasNPC(storage.WorldPosition{Town: a.Town, Area: a.Area}, 29) {
			w.state.Position = storage.WorldPosition{Town: a.Town, Area: a.Area, X: 500, Y: 250}
			break
		}
	}
	pkts, err = w.questInteraction(cmd33(3173))
	if err != nil || len(pkts) != 1 || pkts[0].ID != 291 {
		t.Fatalf("Q3173 CMD 33 failed: pkts=%+v err=%v", pkts, err)
	}
	assertSettled(3173)

	// 4. Q12152 (Lv.99 phase-only NPC 100000369 absent from base-phase catalog) via CMD 33 in a valid town area must settle.
	w.state.Position = storage.WorldPosition{Town: 38, Area: 0, X: 561, Y: 234}
	pkts, err = w.questInteraction(cmd33(12152))
	if err != nil || len(pkts) != 1 || pkts[0].ID != 291 {
		t.Fatalf("Q12152 phase-NPC CMD 33 failed: pkts=%+v err=%v", pkts, err)
	}
	assertSettled(12152)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
