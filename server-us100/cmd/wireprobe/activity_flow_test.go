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
	"time"
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

func TestActiveEventsCalendarHUDAndLiveActivation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "live_act.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	acctID, err := store.DevelopmentAccount(ctx, "probe-us100")
	if err != nil {
		t.Fatal(err)
	}
	// Grant active Neo Premium Plus so we verify event fatigue bonus stacks on top of 273!
	if err := store.ExtendPremiumContracts(ctx, acctID, map[byte]int64{storage.NeoPremiumPlusKind: 30}); err != nil {
		t.Fatal(err)
	}

	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"experience":0}`), inventory.Bag{Version: "ordinary-bag-v1"})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "EventTester",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 9

	evPath := filepath.Join(dir, "events.json")
	pol := events.DefaultActive()
	pol.Extra.PCBangBonusEnabled = true
	rawPol, _ := json.Marshal(pol)
	if err := os.WriteFile(evPath, rawPol, 0600); err != nil {
		t.Fatal(err)
	}

	// 1) Verify buildActiveEventsPayload emits all active calendar/HUD entries with HasDetail=1 and ShowOnCalendar=1
	now := time.Date(2026, 11, 27, 21, 0, 0, 0, time.Local)
	evBytes, err := buildActiveEventsPayload(pol, now)
	if err != nil {
		t.Fatal(err)
	}
	if count := binary.LittleEndian.Uint16(evBytes[0:2]); count < 10 {
		t.Fatalf("expected >=10 active events in NOTI 108, got %d", count)
	}
	if evBytes[len(evBytes)-1] != 0 {
		t.Fatalf("expected trailing paramCount=0 in NOTI 108")
	}

	// 2) Verify syncGrantedBag hot-pushes CMD 1959 (time sync), NOTI 108 (events),
	// NOTI 36 (fatigue 156+30+117 = 303), and automatic extra.* login perks!
	bagRules := inventory.BagRules{
		Source:            checksum,
		EquipmentSlots:    [2]uint16{9, 64},
		Slots:             map[string][2]uint16{"[throw]": {65, 120}, "[material]": {121, 176}},
		MissingStackLimit: 1000,
	}
	lootCat := catalog.LootCatalog{Items: map[uint32]catalog.LootItem{}}
	lootCat.Source.Checksum = checksum
	fatigueSvc := &character.FatigueService{
		Store:      store,
		Rules:      character.FatigueRules{DailyLimit: 156, RoomCost: 1},
		Location:   time.Local,
		EventsPath: evPath,
	}
	w := &worldSession{
		account:    acctID,
		role:       role,
		eventsPath: evPath,
		characters: &character.Service{Store: store},
		fatigue:    fatigueSvc,
		loot:       &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
	}

	var sent []outboundPacket
	err = w.syncGrantedBag(acctID, role.ID, func(kind byte, id uint16, payload []byte) error {
		sent = append(sent, outboundPacket{Kind: kind, ID: id, Payload: append([]byte(nil), payload...)})
		return nil
	}, func(map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}

	var saw1959, saw108, saw36 bool
	var fpLimit uint16
	for _, p := range sent {
		if p.Kind == 1 && p.ID == 1959 {
			saw1959 = true
		}
		if p.Kind == 0 && p.ID == 108 {
			saw108 = true
		}
		if p.Kind == 0 && p.ID == 36 && len(p.Payload) >= 4 {
			saw36 = true
			fpLimit = binary.LittleEndian.Uint16(p.Payload[2:4])
		}
	}
	if !saw1959 || !saw108 || !saw36 {
		t.Fatalf("expected live sync to push CMD 1959 + NOTI 108 + NOTI 36, got 1959=%v 108=%v 36=%v", saw1959, saw108, saw36)
	}
	// Weekday (2026-11-27 Fri): 156 base + 30 PC Bang + 117 Neo Plus = 303 (or 459 if weekend).
	if fpLimit < 303 {
		t.Fatalf("expected fatigue limit >= 303 (Neo Plus + PC Bang stacked), got %d", fpLimit)
	}

	// Verify automatic extra.* perks were granted and materials landed in [material] slots 121..176
	bag, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if bag.Gold != 950000 { // 100k + 300k + 500k + 50k
		t.Fatalf("expected 950000 gold from auto extra perks, got %d", bag.Gold)
	}
	var hasRyanMat, hasCrystalMat bool
	for _, it := range bag.Items {
		if it.Template == 10327281 && it.Amount == 20 && it.Slot >= 121 && it.Slot <= 176 {
			hasRyanMat = true
		}
		if it.Template == 10327282 && it.Amount == 50 && it.Slot >= 121 && it.Slot <= 176 {
			hasCrystalMat = true
		}
	}
	if !hasRyanMat || !hasCrystalMat {
		t.Fatalf("expected materials in [material] tab, bag=%+v", bag.Items)
	}

	// Second login/sync on same day must be strictly idempotent (no duplicate gold or items)
	dupPkts := w.onActivityLogin(ctx)
	if len(dupPkts) != 0 {
		t.Fatalf("expected idempotent second onActivityLogin to emit 0 packets, got %d", len(dupPkts))
	}
}
