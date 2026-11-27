package inventory

import (
	"context"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestWearConquerorContractAllowsPlusTenLevel(t *testing.T) {
	s, role := wearFixture(t)
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "conqueror.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.Store = store

	// Find any wearable item in the loaded equipment catalog with minimum level >= 15
	// for Slayer (job 0) so a level-6 character needs Conqueror's Contract (+10 Lv).
	var template uint32
	var destSlot uint16
	var reqLv int
	for id, d := range s.Catalog.index {
		if _, err := s.Catalog.Reward(id); err != nil {
			continue
		}
		kind := d.Fields["[equipment type]"]
		if len(kind) == 0 {
			continue
		}
		slot, ok := s.Rules.Slots[kind[0].Text]
		if !ok {
			continue
		}
		if WearableBy(d.Fields, "[swordman]", 0, 6) == nil {
			continue
		}
		if WearableBy(d.Fields, "[swordman]", 0, 16) != nil {
			continue
		}
		ml := d.Fields["[minimum level]"]
		if len(ml) > 0 && ml[0].Value >= 7 && ml[0].Value <= 16 {
			template, destSlot, reqLv = id, slot, int(ml[0].Value)
			break
		}
	}
	if template == 0 {
		t.Fatal("could not find level 7..16 swordman gear in fixture catalog")
	}

	bag := Bag{Version: "ordinary-bag-v1", Equipment: []BagEquipment{{Slot: 9, Template: template}}}
	raw, err := SaveBag(json.RawMessage(`{"level":6,"advancement":0}`), bag)
	if err != nil {
		t.Fatal(err)
	}
	role.ID = 101
	role.AccountID = 41
	role.State = raw
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "101", map[string]any{
			"id":             101,
			"account_id":     41,
			"profession":     role.Profession,
			"wire_id":        1,
			"config_version": role.ConfigVersion,
			"state":          raw,
		})
	}); err != nil {
		t.Fatal(err)
	}

	req := protocol.ItemMoveRequest{
		SourceList:      0,
		SourceSlot:      9,
		SourceItem:      template,
		DestinationList: 3,
		DestinationSlot: destSlot,
		Count:           1,
		Selection:       0xffffffff,
	}

	// Without Conqueror's Contract (22), level 6 cannot wear level reqLv gear.
	if _, _, err := s.Move(ctx, role, "wear:no-contract", req); err == nil {
		t.Fatalf("expected lv6 wear of lv%d item %d to fail without Conqueror's Contract", reqLv, template)
	}

	// Activate Conqueror's Contract (Kind 22) -> effective level 16 succeeds atomically via Move (no RWMutex deadlock).
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("us224_account_premium", "41", map[byte]int64{
			storage.ConquerorContractKind: time.Now().Unix() + 3600,
		})
	}); err != nil {
		t.Fatal(err)
	}
	saved, applied, err := s.Move(ctx, role, "wear:with-contract", req)
	if err != nil || !applied {
		t.Fatalf("expected lv6 wear of lv%d item %d with Conqueror's Contract to succeed: applied=%t err=%v", reqLv, template, applied, err)
	}
	after, err := ReadBag(saved.State)
	if err != nil || len(after.Worn) != 1 || after.Worn[0].Template != template {
		t.Fatalf("unexpected bag after conqueror wear: %+v %v", after, err)
	}

	// Expired Conqueror's Contract must reject again.
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("us224_account_premium", "41", map[byte]int64{
			storage.ConquerorContractKind: time.Now().Unix() - 10,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveOrdinary(role, req); err == nil {
		t.Fatalf("expected expired Conqueror's Contract to reject lv%d item %d", reqLv, template)
	}
}
