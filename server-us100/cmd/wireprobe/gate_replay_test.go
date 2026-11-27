package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"dfolan/internal/world"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Explicit catalog path permits testing a staged candidate before deployment.
// All character state lives in the test's own database.
func TestUS100RealGateReplay(t *testing.T) {
	path := os.Getenv("US100_GATE_REPLAY")
	if path == "" {
		t.Skip("set US100_GATE_REPLAY to a generated candidate")
	}
	c, e := catalog.LoadDungeons(path)
	if e != nil {
		t.Fatal(e)
	}
	wc, e := catalog.LoadWorld("../../configs/us100/world.generated.json")
	if e != nil || wc.Source.Checksum != c.Source.Checksum {
		t.Fatalf("world source: %v", e)
	}
	store, e := storage.Open(context.Background(), storage.Config{DBPath: filepath.Join(t.TempDir(), "gate.db")})
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if e = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "1", map[string]any{"id": 1, "account_id": 1, "wire_id": 1, "config_version": c.Source.Checksum, "state": map[string]any{"level": 50}})
	}); e != nil {
		t.Fatal(e)
	}
	w := worldSession{role: storage.Character{ID: 1, WireID: 1, AccountID: 1}, account: 1, level: 1, dungeons: &c, service: &world.Service{Catalog: wc, Store: store}}
	w.gateSet.MinimalGate = true
	w.gateSet.ReferenceOrder = true
	w.gateSet.GateAck = true
	w.gateSet.EnterSelect = true
	setPosition := func(town, area uint32) {
		a := wc.Areas[catalog.AreaKey(town, area)]
		r := a.Walkable[0]
		w.state.Position = storage.WorldPosition{Town: town, Area: area, X: uint16(r[0] + 1), Y: uint16(r[1] + 1)}
	}
	click := func(id uint32, ok bool) {
		p := make([]byte, 17)
		binary.LittleEndian.PutUint32(p, id)
		binary.LittleEndian.PutUint16(p[9:], 65535)
		run, packets, e := w.selectDungeon(p)
		if !ok {
			if e == nil {
				t.Fatalf("unexpected entry %d", id)
			}
			return
		}
		if e != nil || run.Definition.ID != id || len(packets) < 3 || packets[0].Kind != 1 || packets[0].ID != 16 || packets[len(packets)-2].ID != 28 || packets[len(packets)-1].ID != 29 {
			t.Fatalf("entry %d: %v", id, e)
		}
		w.activeDungeon = run
		loaded, e := w.finishDungeonLoading(make([]byte, 16))
		if e != nil || len(loaded) < 3 || loaded[0].ID != 37 || loaded[2].ID != 30 {
			t.Fatalf("loading %d: %v", id, e)
		}
		w.activeDungeon = nil
	}
	checkMenu := func(id uint32) {
		plan, e := w.dungeonGate(make([]byte, 8))
		if e != nil {
			t.Fatal(e)
		}
		seen := false
		opened := false
		for _, p := range plan {
			if p.ID == 5 {
				for i := 2; i+5 <= len(p.Payload); i += 5 {
					seen = seen || binary.LittleEndian.Uint32(p.Payload[i:]) == id
				}
			}
			if p.ID == 27 {
				opened = true
				if !seen {
					t.Fatalf("gate opened before permission for %d", id)
				}
			}
		}
		if !opened {
			t.Fatal("gate did not open")
		}
	}
	setPosition(38, 2)
	checkMenu(3)
	click(3, true)
	click(144, false)
	setPosition(46, 6)
	w.level = 50
	click(144, false)
	if e = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("character_quests", "1/3428", map[string]any{"character_id": 1, "quest_id": 3428, "config_version": c.Source.Checksum, "status": "completed"})
	}); e != nil {
		t.Fatal(e)
	}
	w.level = 49
	click(144, false)
	w.level = 50
	checkMenu(144)
	click(144, true)
	click(3, false)
	click(7156, false)
	count := 0
	for id, indices := range c.Expanded {
		d := c.Dungeons[id]
		for _, i := range indices {
			m := d.Mazes[i]
			if err := dungeon.CheckEntryRoute(c, d, m); err != nil {
				t.Fatalf("%d/%d: %v", id, i, err)
			}
			q := map[uint16]bool{}
			if m.Quest != 0 {
				q[m.Quest] = true
			}
			s, err := dungeon.Select(c, protocol.DungeonSelection{ID: id}, byte(d.MinimumLevel), q)
			if err != nil || s.Maze.Quest != m.Quest {
				t.Fatalf("route %d/%d quest %d: %v", id, i, m.Quest, err)
			}
			count++
		}
	}
	t.Logf("%d expanded dungeons, %d room-checked routes; two owned gate -> select -> loading acknowledgement replays passed", len(c.Expanded), count)
}
