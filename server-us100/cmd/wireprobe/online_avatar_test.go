package main

import (
	"context"
	"dfolan/internal/admin"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlineAvatarGrantPushAndEquip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "avatar.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "avatar-gm-test")
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
	wearRules, err := inventory.LoadWearRules(filepath.Join(cfg, "rules.wear.json"), items.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	chars, err := catalog.LoadCharacters(filepath.Join(cfg, "characters.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{AccountID: account, Name: "stylish", ConfigVersion: items.Source.Checksum, State: json.RawMessage(`{"level":1}`)}, 10)
	if err != nil {
		t.Fatal(err)
	}

	w := worldSession{role: role, loot: &loot.Service{Store: store, Catalog: items}}
	w.role.WireID = 91
	var hub gmSessions
	ch := make(chan struct{}, 1)
	hub.bind(ch, role.ID, hub.beforeSelect())

	svc := admin.Service{Store: store, Awarder: &inventory.Awarder{Catalog: items, Rules: rules, Equipment: gear}}
	if err = os.Mkdir(filepath.Join(dir, "pending"), 0700); err != nil {
		t.Fatal(err)
	}
	opBody, _ := json.Marshal(map[string]any{
		"action":       "avatar",
		"character_id": role.ID,
		"avatar_ids":   []uint32{114550006, 114500000, 106590227},
		"reason":       "时装试衣间发放",
	})
	if err = os.WriteFile(filepath.Join(dir, "pending", "11111111111111111111111111111111.json"), opBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err = admin.DrainAndNotify(dir, &svc, account, hub.committed); err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 {
		t.Fatal("online session was not notified on avatar grant")
	}
	<-ch

	var packets [][]byte
	send := func(kind byte, id uint16, p []byte) error {
		if kind == 0 && id == 14 {
			packets = append(packets, append([]byte(nil), p...))
		}
		return nil
	}
	if err = w.syncGrantedBag(account, role.ID, send, func(map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	// Must have sent space 0 AND space 1 (Avatar Inventory) NOTI14 updates!
	var avatarPkt []byte
	for _, p := range packets {
		if len(p) > 0 && p[0] == 1 {
			avatarPkt = p
		}
	}
	if avatarPkt == nil {
		t.Fatalf("missing NOTI14 space 1 avatar push; got %d packets", len(packets))
	}
	if binary.LittleEndian.Uint16(avatarPkt[1:3]) != 3 || len(avatarPkt) != 3+3*(177+8) {
		t.Fatalf("unexpected US224 NOTI14 space 1 layout: len=%d", len(avatarPkt))
	}

	// Verify NOTI13 login restore payload for space 1 (177 + 8 bytes per avatar row)
	restore, err := inventory.AvatarPayload(w.role.State)
	if err != nil || restore[0] != 1 || binary.LittleEndian.Uint16(restore[1:3]) != inventory.AvatarBagCapacity || binary.LittleEndian.Uint16(restore[3:5]) != 3 || len(restore) != 5+3*(177+8) {
		t.Fatalf("unexpected NOTI13 space 1 restore: err=%v len=%d", err, len(restore))
	}

	// Verify equipping avatar from space 1 slot 0 -> space 3 slot 0 (hat)
	wearSvc := &inventory.WearService{Store: store, Catalog: gear, Professions: chars, BagRules: rules, Rules: wearRules}
	saved, _, err := wearSvc.Move(ctx, w.role, "equip-avatar-1", protocol.ItemMoveRequest{
		SourceList:      1,
		SourceSlot:      0,
		SourceItem:      114550006,
		DestinationList: 3,
		DestinationSlot: 0,
		Count:           1,
		Selection:       0xffffffff,
	})
	if err != nil {
		t.Fatal("equip avatar failed:", err)
	}
	b, err := inventory.ReadBag(saved.State)
	if err != nil || len(b.Avatars) != 2 || len(b.WornAvatars) != 1 || b.WornAvatars[0].Template != 114550006 {
		t.Fatalf("unexpected bag after avatar equip: %+v err=%v", b, err)
	}
	w.role = saved
	var moves equipmentSession
	for i, step := range []struct {
		from, to byte
		src, dst uint16
		want     byte
	}{{3, 1, 0, 5, 0}, {1, 3, 5, 0, 1}} {
		p := make([]byte, 29)
		p[0], p[11] = step.from, step.to
		binary.LittleEndian.PutUint16(p[1:], step.src)
		binary.LittleEndian.PutUint16(p[12:], step.dst)
		binary.LittleEndian.PutUint32(p[3:], 114550006)
		binary.LittleEndian.PutUint32(p[7:], 1)
		binary.LittleEndian.PutUint32(p[22:], 0xffffffff)
		plan, err := moves.handle(wearSvc, &w, p, p)
		if err != nil || len(plan) != 5 || plan[0].ID != 19 || plan[1].ID != 14 || plan[2].ID != 14 || plan[3].Kind != 0 || plan[3].ID != protocol.AdventurerFameNotiID || plan[4].Kind != 0 || plan[4].ID != 2 || plan[4].Name != "town_appearance_updated" {
			t.Fatalf("step %d: plan=%v err=%v", i, plan, err)
		}
		latest, err := inventory.ReadBag(w.role.State)
		if err != nil || len(latest.WornAvatars) != int(step.want) {
			t.Fatalf("step %d: worn state %v / %v", i, latest.WornAvatars, err)
		}
		if step.want == 1 && latest.WornAvatars[0].Template != 114550006 {
			t.Fatal("wrong re-equipped hat")
		}
		gotActor := binary.LittleEndian.Uint16(plan[3].Payload[0:2])
		gotFame := binary.LittleEndian.Uint32(plan[3].Payload[4:8])
		wantFame := gear.BagAdventurerFame(latest)
		if gotActor != w.role.WireID || gotFame != wantFame || (step.want == 0 && gotFame != 0) || (step.want == 1 && gotFame == 0) {
			t.Fatalf("step %d: fame pkt actor=%d fame=%d want=%d", i, gotActor, gotFame, wantFame)
		}
		eqCountOffset := 138 + len(w.role.Name) + 5
		if gotCount := plan[4].Payload[eqCountOffset]; gotCount != step.want {
			t.Fatalf("step %d: mode-0 appearance count=%d want=%d", i, gotCount, step.want)
		}
	}
}
