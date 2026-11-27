package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/dungeon"
	"dfolan/internal/events"
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

func TestHellPartyNoti28AndActivityClearFlow(t *testing.T) {
	// 1. Verify DungeonInfo encodes RandomHell coordinates at offsets 10..11
	infoNoHell := protocol.DungeonInfo(protocol.DungeonInfoState{ID: 3, Maze: 1, Boss: [2]byte{3, 0}})
	if infoNoHell[10] != 0xff || infoNoHell[11] != 0xff {
		t.Fatalf("default DungeonInfo hell = (%d,%d), want (255,255)", infoNoHell[10], infoNoHell[11])
	}
	infoHell := protocol.DungeonInfo(protocol.DungeonInfoState{
		ID: 3, Maze: 1, Boss: [2]byte{3, 0},
		HasRandomHell: true, RandomHell: [2]byte{2, 0},
	})
	if infoHell[10] != 2 || infoHell[11] != 0 {
		t.Fatalf("hell DungeonInfo hell = (%d,%d), want (2,0)", infoHell[10], infoHell[11])
	}

	// 2. Verify Dungeon Session AssignRandomHell picks a non-start non-boss room
	ds := &dungeon.Session{
		RunID:      "0123456789abcdef0123456789abcdef",
		Definition: catalog.DungeonDefinition{ID: 3, BasisLevel: 50},
		Maze: catalog.DungeonMaze{
			Start: [2]byte{0, 0},
			Boss:  [2]byte{2, 0},
			Rooms: []catalog.DungeonRoom{
				{X: 0, Y: 0, Map: 100},
				{X: 1, Y: 0, Map: 101},
				{X: 2, Y: 0, Map: 102, Boss: true},
			},
		},
		Room: catalog.DungeonRoom{X: 1, Y: 0, Map: 101},
	}
	if !ds.AssignRandomHell(42, 100, 1) {
		t.Fatalf("expected AssignRandomHell with 100%% chance to trigger")
	}
	if ds.HellRoom != [2]byte{1, 0} || !ds.IsHellRoom() {
		t.Fatalf("expected HellRoom (1,0), got %v (IsHellRoom=%v)", ds.HellRoom, ds.IsHellRoom())
	}

	// 3. Verify onActivityKill & onActivityClear credit mileage, daily challenge, and weekly reward
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "act.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	acctID, err := store.DevelopmentAccount(ctx, "probe-us100")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"experience":0}`), inventory.Bag{Version: "ordinary-bag-v1"})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "Runner",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}

	evPath := filepath.Join(dir, "events.json")
	pol := events.DefaultActive()
	pol.DailyChallenge.KillTarget = 1
	pol.DailyChallenge.ClearTarget = 1
	pol.WeeklyDungeon.WeeklyLimit = 6
	rawPol, _ := json.Marshal(pol)
	if err := os.WriteFile(evPath, rawPol, 0600); err != nil {
		t.Fatal(err)
	}

	bagRules := inventory.BagRules{
		Source:            checksum,
		EquipmentSlots:    [2]uint16{9, 64},
		Slots:             map[string][2]uint16{"[throw]": {65, 120}, "[material]": {121, 176}},
		MissingStackLimit: 1000,
	}
	lootCat := catalog.LootCatalog{
		Items: map[uint32]catalog.LootItem{
			10327281: {ID: 10327281, Kind: "stackable", StackableType: "[material]", StackLimit: 1000},
			10327282: {ID: 10327282, Kind: "stackable", StackableType: "[material]", StackLimit: 1000},
		},
	}
	lootCat.Source.Checksum = checksum
	w := &worldSession{
		account:       acctID,
		role:          role,
		activeDungeon: ds,
		eventsPath:    evPath,
		characters:    &character.Service{Store: store},
		loot:          &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
	}

	killPkts := w.onActivityKill(ctx)
	if len(killPkts) < 2 {
		t.Fatalf("expected inventory + mileage packets on daily kill completion, got %d", len(killPkts))
	}
	clearPkts := w.onActivityClear(ctx)
	if len(clearPkts) < 3 {
		t.Fatalf("expected mileage + daily clear + weekly packets, got %d", len(clearPkts))
	}

	_, mileage, err := store.AccountBalances(ctx, acctID)
	// 10 (daily kill) + 2 (dungeon clear) + 10 (daily clear) + 15 (weekly bonus) = 37
	if err != nil || mileage != 37 {
		t.Fatalf("mileage after kill+clear = %d, want 37, err=%v", mileage, err)
	}
	fp := fundsPayload(1234, mileage)
	if len(fp) != 9 || fp[0] != 1 || binary.LittleEndian.Uint32(fp[1:5]) != 1234 || binary.LittleEndian.Uint32(fp[5:9]) != 37 {
		t.Fatalf("unexpected NOTI 53 fundsPayload: %x", fp)
	}
}
