package world

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"errors"
	"path/filepath"
	"testing"
)

func TestTransitionAuthority(t *testing.T) {
	s := Service{Catalog: catalog.WorldCatalog{Areas: map[string]catalog.WorldArea{
		"38/0": {Town: 38, Area: 0, MinimumLevel: 1, Walkable: [][4]int32{{0, 0, 1000, 500}}, Portals: []catalog.Portal{{Bounds: [4]int32{500, 200, 80, 80}, Town: 38, Area: 1}, {Town: 38, Area: 3}}},
		"38/1": {Town: 38, Area: 1, MinimumLevel: 1, Walkable: [][4]int32{{400, 100, 250, 300}}, SeriaReturnWarp: true, ReturnWarpBounds: [][4]int32{{470, 320, 150, 40}}},
		"38/3": {Town: 38, Area: 3, MinimumLevel: 14, Walkable: [][4]int32{{0, 0, 1000, 500}}},
	}}, Rules: Rules{RequirePortalProximity: true, PortalMargin: 10}}
	old := storage.WorldPosition{Town: 38, Area: 0, X: 550, Y: 230}
	req := protocol.AreaChangeRequest{Town: 38, Area: 1, X: 544, Y: 311, PreviousTown: 38, PreviousArea: 0}
	next, e := s.Transition(1, old, req)
	if e != nil || next.Return == nil {
		t.Fatalf("valid entry: %+v %v", next, e)
	}
	req.Area = 3
	if _, e = s.Transition(1, old, req); !errors.Is(e, ErrLevel) {
		t.Fatal("level gate bypass", e)
	}
	req.Area = 1
	req.X = 65535
	if _, e = s.Transition(1, old, req); e == nil {
		t.Fatal("outside coordinates accepted")
	}
	req.X = 544
	req.PreviousTown = 39
	if _, e = s.Transition(1, old, req); e == nil {
		t.Fatal("stale origin accepted")
	}
	req.PreviousTown = 38
	old.X = 10
	if _, e = s.Transition(1, old, req); e == nil {
		t.Fatal("remote portal bypass")
	}
	next.X = 550
	next.Y = 340
	back := protocol.AreaChangeRequest{Town: 38, Area: 0, X: 561, Y: 234, PreviousTown: 38, PreviousArea: 1}
	returned, e := s.Transition(1, next, back)
	if e != nil || returned.Return != nil {
		t.Fatalf("return warp: %+v %v", returned, e)
	}
	back.Area = 3
	if _, e = s.Transition(20, next, back); e == nil {
		t.Fatal("return warp arbitrary destination")
	}
}

func TestSourceSeriaRoundTrip(t *testing.T) {
	cat, e := catalog.LoadWorld("../../configs/world.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	s := Service{Catalog: cat, Rules: Rules{RequirePortalProximity: true, PortalMargin: 10}}
	start := storage.WorldPosition{Town: 38, Area: 0, X: 622, Y: 196}
	inside, e := s.Transition(1, start, protocol.AreaChangeRequest{Town: 38, Area: 1, X: 544, Y: 311, PreviousTown: 38, PreviousArea: 0})
	if e != nil {
		t.Fatal(e)
	}
	inside.Y = 340
	// Captured map-selector request supplies a non-walkable generic landing
	// point. Only an authorized Seria return resolves to the saved origin.
	out, e := s.Transition(1, inside, protocol.AreaChangeRequest{Town: 38, Area: 0, X: 746, Y: 157, PreviousTown: 38, PreviousArea: 1})
	if e != nil || out.Return != nil || out.Town != start.Town || out.Area != start.Area {
		t.Fatalf("source return warp failed: %+v %v", out, e)
	}
	if out.X != start.X || out.Y != start.Y {
		t.Fatal("saved return coordinates lost", out)
	}
	inside.Return.X = 65535
	if _, e = s.Transition(1, inside, protocol.AreaChangeRequest{Town: 38, Area: 0, X: 561, Y: 234, PreviousTown: 38, PreviousArea: 1}); e == nil {
		t.Fatal("invalid saved origin accepted")
	}
}

func TestSeriaRoomEntryMapsEveryTownToItsSeriaRoom(t *testing.T) {
	cat, e := catalog.LoadWorld("../../configs/us100/world.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	s := Service{Catalog: cat, Rules: Rules{RequirePortalProximity: false, PortalMargin: 32}}
	fallback := storage.WorldPosition{Town: 38, Area: 0, X: 561, Y: 234}

	// 1. Elvengard street (38/0) -> Elvengard Seria's Room (38/1) with Return to 38/0.
	p, changed := s.SeriaRoomEntry(1, storage.WorldPosition{Town: 38, Area: 0, X: 561, Y: 234}, fallback)
	if !changed || p.Town != 38 || p.Area != 1 || p.Return == nil || p.Return.Town != 38 || p.Return.Area != 0 {
		t.Fatalf("38/0 -> Seria failed: changed=%v pos=%+v", changed, p)
	}
	if !Walkable(cat.Areas["38/1"], p.X, p.Y) || inReturnWarpBounds(cat.Areas["38/1"], p.X, p.Y) {
		t.Fatalf("38/1 spawn invalid or inside warp: %+v", p)
	}

	// 2. Elvengard dungeon gate (38/2) -> Elvengard Seria's Room (38/1) with Return to 38/0 (Seria gate).
	p2, changed := s.SeriaRoomEntry(1, storage.WorldPosition{Town: 38, Area: 2, X: 410, Y: 236}, fallback)
	if !changed || p2.Town != 38 || p2.Area != 1 || p2.Return == nil || p2.Return.Town != 38 || p2.Return.Area != 0 {
		t.Fatalf("38/2 -> Seria failed: changed=%v pos=%+v", changed, p2)
	}

	// 3. Dedicated Seria-room town (Silver Crown 1/1 -> 1/0, Behemoth 4/1 -> 4/0, Beast Central Park 51/1 -> 51/0).
	for _, tc := range []struct {
		level      byte
		cur        storage.WorldPosition
		wantTown   uint32
		wantArea   uint32
	}{
		{10, storage.WorldPosition{Town: 1, Area: 1, X: 500, Y: 250}, 1, 0},
		{50, storage.WorldPosition{Town: 4, Area: 1, X: 500, Y: 250}, 4, 0},
		{100, storage.WorldPosition{Town: 51, Area: 1, X: 500, Y: 250}, 51, 0},
		{100, storage.WorldPosition{Town: 83, Area: 1, X: 500, Y: 250}, 83, 0},
		{100, storage.WorldPosition{Town: 98, Area: 1, X: 500, Y: 250}, 98, 0},
	} {
		got, ch := s.SeriaRoomEntry(tc.level, tc.cur, fallback)
		if !ch || got.Town != tc.wantTown || got.Area != tc.wantArea {
			t.Fatalf("town %d/%d -> got changed=%v %+v, want %d/%d", tc.cur.Town, tc.cur.Area, ch, got, tc.wantTown, tc.wantArea)
		}
		if err := s.ValidatePosition(tc.level, got); err != nil {
			t.Fatalf("town %d/%d Seria spawn failed validation: %v (%+v)", tc.wantTown, tc.wantArea, err, got)
		}
	}

	// 4. Town without dedicated Seria map (Death Valley 44/0) -> shared 38/1 with Return to 44/0,
	//    and walking out of 38/1 returns cleanly to 44/0.
	dv := storage.WorldPosition{Town: 44, Area: 0, X: 500, Y: 250}
	if err := s.ValidatePosition(50, dv); err != nil {
		t.Fatalf("44/0 baseline invalid: %v", err)
	}
	got, ch := s.SeriaRoomEntry(50, dv, fallback)
	if !ch || got.Town != 38 || got.Area != 1 || got.Return == nil || got.Return.Town != 44 || got.Return.Area != 0 {
		t.Fatalf("44/0 -> shared Seria failed: changed=%v %+v", ch, got)
	}
	out, err := s.Transition(50, got, protocol.AreaChangeRequest{Town: 44, Area: 0, X: 746, Y: 157, PreviousTown: 38, PreviousArea: 1})
	if err != nil || out.Town != 44 || out.Area != 0 || out.X != 500 || out.Y != 250 || out.Return != nil {
		t.Fatalf("exit shared Seria back to 44/0 failed: out=%+v err=%v", out, err)
	}
}

func TestEnterPersistsSeriaRoomSpawn(t *testing.T) {
	cat, e := catalog.LoadWorld("../../configs/us100/world.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	acctID, err := store.DevelopmentAccount(ctx, "seria-test")
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{AccountID: acctID, Name: "Tester", ConfigVersion: cat.Source.Checksum, State: []byte(`{"level":100}`)}, 12)
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: store, Catalog: cat, Rules: Rules{RequirePortalProximity: false, PortalMargin: 32}}
	spawn := storage.WorldPosition{Town: 38, Area: 0, X: 561, Y: 234}

	// First town entry -> lands in 38/1 (Elvengard Seria's Room) with Return to 38/0.
	st, err := svc.Enter(ctx, acctID, role.ID, 100, spawn)
	if err != nil {
		t.Fatal(err)
	}
	if st.Position.Town != 38 || st.Position.Area != 1 || st.Position.Return == nil || st.Position.Return.Town != 38 || st.Position.Return.Area != 0 {
		t.Fatalf("first Enter did not spawn in Seria's Room: %+v", st.Position)
	}

	// Simulate character moving out into Beast Central Park 51/1, then re-entering town -> lands in 51/0!
	park := storage.WorldPosition{Town: 51, Area: 1, X: 500, Y: 250}
	if _, err := store.SaveWorld(ctx, acctID, role.ID, st, park); err != nil {
		t.Fatal(err)
	}
	st2, err := svc.Enter(ctx, acctID, role.ID, 100, spawn)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Position.Town != 51 || st2.Position.Area != 0 {
		t.Fatalf("re-entry from 51/1 should land in 51/0 Seria's Room, got %+v", st2.Position)
	}
}

func TestElvengardToHendonMyreAndWestCoastTransitions(t *testing.T) {
	cat, err := catalog.LoadWorld("../../configs/us100/world.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "town_nav.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	acctID, err := store.DevelopmentAccount(ctx, "nav-test")
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "Walker",
		ConfigVersion: cat.Source.Checksum,
		State:         []byte(`{"level":19}`),
	}, 12)
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: store, Catalog: cat, Rules: Rules{RequirePortalProximity: true, PortalMargin: 64}}
	owner := QuestAccess{Account: acctID, Character: role.ID}

	// 1. Elvengard 38/0 -> 38/3 with landing coordinate inside 38/3's return portal [207,134,120,40]
	//    just above 38/3's walkable [207,165,120,30] must succeed and clamp to walkable.
	from38 := storage.WorldPosition{Town: 38, Area: 0, X: 1393, Y: 364}
	to383, err := svc.Transition(19, from38, protocol.AreaChangeRequest{
		Town: 38, Area: 3, X: 267, Y: 154, PreviousTown: 38, PreviousArea: 0,
	}, owner)
	if err != nil {
		t.Fatalf("38/0 -> 38/3 transition failed: %v", err)
	}
	if to383.Town != 38 || to383.Area != 3 || !Walkable(cat.Areas["38/3"], to383.X, to383.Y) {
		t.Fatalf("38/3 clamped position invalid: %+v", to383)
	}

	// 2. Recovered portals after [quest condition] in Hendon Myre 39/0 must include
	//    West Coast (40/0), Elvengard (38/3), and animated Seria's Room door (38/1).
	hm := cat.Areas["39/0"]
	for _, want := range [][2]uint32{{40, 0}, {38, 3}, {38, 1}} {
		if !areaHasPortalTo(hm, want[0], want[1]) {
			t.Fatalf("Hendon Myre 39/0 missing recovered portal to %d/%d", want[0], want[1])
		}
	}
	// 3. Elvengard 38/0 must also recover walkable rectangles after [quest condition] (e.g. [571,172,210,80]).
	if !Walkable(cat.Areas["38/0"], 650, 180) {
		t.Fatalf("Elvengard 38/0 missing recovered walkable rectangle around (650,180)")
	}
}
