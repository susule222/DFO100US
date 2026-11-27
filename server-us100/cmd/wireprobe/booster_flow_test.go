package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"path/filepath"
	"testing"
)

func setupBoosterTestStore(t *testing.T, items []inventory.BagItem) (*storage.Store, *loot.Service, storage.Character) {
	t.Helper()
	c, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	svc := &loot.Service{Catalog: c, BagRules: r}
	path := filepath.Join(t.TempDir(), "booster.db")
	s, err := storage.Open(context.Background(), storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	err = s.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 1, "profession": 0,
			"config_version": r.Source,
			"state": map[string]any{
				"inventory": inventory.Bag{Version: "ordinary-bag-v1", Items: items},
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := s.LiveCharacter(42, 48)
	if err != nil {
		t.Fatal(err)
	}
	return s, svc, role
}

func cmd160Choice(slot, count uint16, category, chosen uint32) []byte {
	p := make([]byte, 16)
	binary.LittleEndian.PutUint16(p[0:2], slot)
	binary.LittleEndian.PutUint16(p[2:4], count)
	binary.LittleEndian.PutUint32(p[4:8], category)
	binary.LittleEndian.PutUint32(p[8:12], chosen)
	return p
}

func cmd160Plain(slot, count uint16, category uint32) []byte {
	p := make([]byte, 8)
	binary.LittleEndian.PutUint16(p[0:2], slot)
	binary.LittleEndian.PutUint16(p[2:4], count)
	binary.LittleEndian.PutUint32(p[4:8], category)
	return p
}

func TestOpenDanjiWeaponAvatarSelectionBox590006307(t *testing.T) {
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{{Slot: 84, Template: 590006307, Amount: 1}})
	plain := cmd160Choice(84, 1, 0, 501600810) // [丹津] 雷神索尔的莫尔尼尔
	saved, plan, box, award, qty, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, role, []byte("raw-590006307"), plain, nil,
	)
	if err != nil {
		t.Fatalf("openBoosterBox 590006307 failed: %v", err)
	}
	if !applied || box != 590006307 || award != 501600810 || qty != 1 {
		t.Fatalf("unexpected result applied=%v box=%d award=%d qty=%d", applied, box, award, qty)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Items) != 0 {
		t.Fatalf("expected consumed box removed from ordinary bag, got %+v", bag.Items)
	}
	if len(bag.Avatars) != 1 || bag.Avatars[0].Template != 501600810 {
		t.Fatalf("expected weapon avatar 501600810 in Space 1 Avatar Bag, got %+v", bag.Avatars)
	}
	hasAvatarRestore := false
	for _, pkt := range plan {
		if pkt.Name == "booster_avatar_restored" && pkt.Kind == 0 && pkt.ID == 13 && len(pkt.Payload) > 0 && pkt.Payload[0] == 1 {
			hasAvatarRestore = true
		}
	}
	if !hasAvatarRestore {
		t.Fatalf("expected NOTI 13 space-1 avatar restore packet in plan, got %+v", plan)
	}
}

func TestOpenDanjiMultiCategoryWeaponAvatarBox590006308(t *testing.T) {
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{{Slot: 85, Template: 590006308, Amount: 1}})
	plain := cmd160Choice(85, 1, 2, 503600170) // non-zero category 2
	saved, _, _, award, _, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, role, []byte("raw-590006308"), plain, nil,
	)
	if err != nil || !applied || award != 503600170 {
		t.Fatalf("openBoosterBox 590006308 failed: err=%v applied=%v award=%d", err, applied, award)
	}
	bag, _ := inventory.ReadBag(saved.State)
	if len(bag.Avatars) != 1 || bag.Avatars[0].Template != 503600170 {
		t.Fatalf("expected avatar 503600170 in avatar bag, got %+v", bag.Avatars)
	}
}

func TestOpenDanjiFullAvatarSetBox590006335AndNeoLook590005794(t *testing.T) {
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{
		{Slot: 86, Template: 590006335, Amount: 1},
		{Slot: 87, Template: 590005794, Amount: 1},
	})
	// Open 8-piece DANJI avatar set (category 1 = Female Slayer)
	saved, plan, _, _, _, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, role, []byte("raw-590006335"), cmd160Plain(86, 1, 1), nil,
	)
	if err != nil || !applied {
		t.Fatalf("openBoosterBox 590006335 failed: err=%v applied=%v", err, applied)
	}
	bag, _ := inventory.ReadBag(saved.State)
	if len(bag.Avatars) != 8 {
		t.Fatalf("expected 8 avatars from set box 590006335, got %d", len(bag.Avatars))
	}
	if len(plan) < 3 {
		t.Fatalf("expected ACK 160 + NOTI 13 bag + NOTI 13 avatar restore, got %d packets", len(plan))
	}

	// Open Neo Awakening Look Avatar Set 590005794 (category 0)
	saved2, _, _, _, _, applied2, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, saved, []byte("raw-590005794"), cmd160Plain(87, 1, 0), nil,
	)
	if err != nil || !applied2 {
		t.Fatalf("openBoosterBox 590005794 failed: err=%v applied=%v", err, applied2)
	}
	bag2, _ := inventory.ReadBag(saved2.State)
	if len(bag2.Avatars) <= 8 {
		t.Fatalf("expected Neo look box 590005794 to add avatars to Space 1, got total %d", len(bag2.Avatars))
	}
}

func TestOpenDanjiAuraBox590006323AwardsAvatarAndTicket(t *testing.T) {
	s, svc, role := setupBoosterTestStore(t, []inventory.BagItem{{Slot: 88, Template: 590006323, Amount: 1}})
	saved, _, _, _, _, applied, err := openBoosterBox(
		context.Background(), s, nil, svc, 42, role, []byte("raw-590006323"), cmd160Plain(88, 1, 0), nil,
	)
	if err != nil || !applied {
		t.Fatalf("openBoosterBox 590006323 failed: err=%v applied=%v", err, applied)
	}
	bag, _ := inventory.ReadBag(saved.State)
	if len(bag.Avatars) != 1 || bag.Avatars[0].Template != 501590308 {
		t.Fatalf("expected aura avatar 501590308 in Space 1, got %+v", bag.Avatars)
	}
	foundTicket := false
	for _, it := range bag.Items {
		if it.Template == 590006328 && it.Amount == 1 {
			foundTicket = true
		}
	}
	if !foundTicket {
		t.Fatalf("expected aura skin ticket 590006328 in Space 0, got %+v", bag.Items)
	}
}
