package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestUS100DisjointLiveCaptureFlow(t *testing.T) {
	if !observedGameRequest(26) {
		t.Fatal("CMD 26 must be in observedGameRequest so it stays decodable past BodySampleLimit")
	}

	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "disjoint.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}

	chars, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog("../../configs/us100/equipment.generated.json", chars.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}

	// Include legacy cubes in Bag.Items to verify entry migration moves them
	// into the Account Cube Warehouse (NOTI 1140) without touching other materials.
	initBag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    5000,
		Items: []inventory.BagItem{
			{Slot: 121, Template: 10092947, Amount: 60},
			{Slot: 122, Template: inventory.ClearCubeFragmentID, Amount: 12},
		},
		Equipment: []inventory.BagEquipment{
			{Slot: 9, Template: 20002, Durability: 0},
			{Slot: 10, Template: 20003, Durability: 0},
			{Slot: 11, Template: 31906, Durability: 40},
			{Slot: 12, Template: 24002, Durability: 0},
			{Slot: 13, Template: 20002, Durability: 0},
			{Slot: 14, Template: 24006, Durability: 0},
			{Slot: 15, Template: 22002, Durability: 0},
		},
	}
	stateRaw, err := inventory.SaveBag(json.RawMessage(`{"level":10}`), initBag)
	if err != nil {
		t.Fatal(err)
	}
	var stateObj map[string]any
	if err := json.Unmarshal(stateRaw, &stateObj); err != nil {
		t.Fatal(err)
	}
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id":             48,
			"account_id":     41,
			"profession":     0,
			"wire_id":        1,
			"config_version": chars.Source.Checksum,
			"state":          stateObj,
		})
	}); err != nil {
		t.Fatal(err)
	}

	role, err := store.LiveCharacter(41, 48)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 1

	// Entry migration: moves slot 122 (12 Clear Cubes) out of Bag.Items into account warehouse.
	role, migratedTotals, err := inventory.MigrateBagCubes(ctx, store, role)
	if err != nil {
		t.Fatalf("MigrateBagCubes failed: %v", err)
	}
	if migratedTotals[inventory.ClearCubeFragmentID] != 12 {
		t.Fatalf("expected 12 migrated Clear Cubes, got %+v", migratedTotals)
	}
	migratedBag, err := inventory.ReadBag(role.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(migratedBag.Items) != 1 || migratedBag.Items[0].Template != 10092947 {
		t.Fatalf("expected only non-cube material left in Bag.Items, got %+v", migratedBag.Items)
	}

	ws := &inventory.WearService{
		Store:       store,
		Catalog:     gear,
		Professions: chars,
		BagRules:    bagRules,
	}
	w := &worldSession{role: role}
	var sess disjointSession

	// Exact decrypted CMD 26 payload captured from US100 client.
	plain, err := hex.DecodeString("00ffff060c00c25d00000d00224e00000f00f25500000a00234e00000b00a27c00000e00c65d00000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	rawFrame := append([]byte{1, 26, 0}, plain...)

	plan, err := sess.handle(ws, w, plain, rawFrame)
	if err != nil {
		t.Fatalf("disjointSession.handle failed: %v", err)
	}
	if len(plan) != 3 {
		t.Fatalf("expected 3 outbound packets (ACK26 + NOTI14 + NOTI1140), got %d", len(plan))
	}
	if plan[0].Kind != 1 || plan[0].ID != 26 {
		t.Fatalf("plan[0] = kind=%d id=%d, want 1/26", plan[0].Kind, plan[0].ID)
	}
	if plan[1].Kind != 0 || plan[1].ID != 14 {
		t.Fatalf("plan[1] = kind=%d id=%d, want 0/14", plan[1].Kind, plan[1].ID)
	}
	if plan[2].Kind != 0 || plan[2].ID != 1140 {
		t.Fatalf("plan[2] = kind=%d id=%d, want 0/1140", plan[2].Kind, plan[2].ID)
	}

	// Verify ACK 26 binary format matches DFO_unpacked.exe 0x1434a5710.
	ack := plan[0].Payload
	if ack[0] != 1 || ack[1] != 6 {
		t.Fatalf("unexpected ACK26 header: %x", ack[:4])
	}
	spaceOff := 2 + 6*2
	if ack[spaceOff] != 0 || binary.LittleEndian.Uint16(ack[spaceOff+1:spaceOff+3]) != 0xffff {
		t.Fatalf("unexpected space/disassembler in ACK26: %x", ack[spaceOff:spaceOff+3])
	}
	rewardCount := int(ack[spaceOff+3])
	if rewardCount < 2 {
		t.Fatalf("expected at least 2 cube types, got %d", rewardCount)
	}
	if len(ack) != spaceOff+4+rewardCount*10 {
		t.Fatalf("ACK26 length %d != expected %d", len(ack), spaceOff+4+rewardCount*10)
	}

	// Verify NOTI 1140 updates the Account Cube Warehouse (mode=1, count=rewardCount,
	// first entry 3037 with 12 + delta).
	noti := plan[2].Payload
	if noti[0] != 1 || int(binary.LittleEndian.Uint32(noti[1:5])) != rewardCount {
		t.Fatalf("unexpected NOTI 1140 header: %x", noti[:5])
	}
	clearTpl := binary.LittleEndian.Uint32(noti[5:9])
	clearTotal := binary.LittleEndian.Uint32(noti[9:13])
	if clearTpl != inventory.ClearCubeFragmentID || clearTotal != 12+45 {
		t.Fatalf("NOTI 1140 first entry = (%d, %d), want (3037, 57)", clearTpl, clearTotal)
	}

	// Verify persisted character bag AND persisted account material warehouse.
	reloaded, err := store.LiveCharacter(41, 48)
	if err != nil {
		t.Fatal(err)
	}
	afterBag, err := inventory.ReadBag(reloaded.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterBag.Equipment) != 1 || afterBag.Equipment[0].Slot != 9 {
		t.Fatalf("expected only untouched slot 9 to remain, got %+v", afterBag.Equipment)
	}
	if len(afterBag.Items) != 1 || afterBag.Items[0].Template != 10092947 {
		t.Fatalf("cube fragments must not be added to ordinary Bag.Items, got %+v", afterBag.Items)
	}
	storedMats, err := store.AccountMaterials(41)
	if err != nil {
		t.Fatal(err)
	}
	if storedMats[inventory.ClearCubeFragmentID] != 57 {
		t.Fatalf("stored AccountMaterials[3037] = %d, want 57", storedMats[inventory.ClearCubeFragmentID])
	}

	// Attempting to disassemble the already-removed items with a fresh frame must fail.
	if _, err := sess.handle(ws, w, plain, append(rawFrame, 0x99)); err == nil {
		t.Fatal("expected second disassembly of already-removed items to fail")
	}
}
