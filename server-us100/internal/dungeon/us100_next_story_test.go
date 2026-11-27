package dungeon

import (
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"encoding/hex"
	"testing"
)

func TestUS100NextMirkwoodStoryRooms(t *testing.T) {
	c, e := catalog.LoadDungeons("../../configs/us100/dungeons.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []struct {
		q         uint16
		d, target uint32
	}{{3148, 9, 76156}, {3149, 7, 76166}, {3150, 8, 76176}} {
		s, e := Select(c, protocol.DungeonSelection{ID: r.d}, 20, map[uint16]bool{r.q: true})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, room := range s.Maze.Rooms {
			if _, e := fixedMonsters(c.Maps[room.Map], s.Definition.BasisLevel); e != nil {
				t.Fatal(e)
			}
			if room.Map == r.target && room.Boss {
				found = true
			}
		}
		if !found || s.Maze.Quest != r.q {
			t.Fatalf("quest %d missing required boss map %d", r.q, r.target)
		}
	}
}

func TestUS100DarkThunderlandLayeredBossEntryAndClearMaps(t *testing.T) {
	c, e := catalog.LoadDungeons("../../configs/us100/dungeons.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	// Exact live captured CMD 16 frame for Dark Thunderland (dungeon 1000, quest 3151).
	raw, _ := hex.DecodeString("e80300000000000000ffff00000000004f0c0000000000000000000000000000")
	req, e := protocol.DecodeDungeonSelection(raw)
	if e != nil {
		t.Fatalf("decode live CMD 16: %v", e)
	}
	for _, tc := range []struct {
		quest      uint16
		bossMap    uint32
		layeredMap uint32
	}{
		{quest: 3151, bossMap: 76186, layeredMap: 76187},
		{quest: 3154, bossMap: 76196, layeredMap: 76197},
	} {
		s, err := Select(c, req, 15, map[uint16]bool{tc.quest: true})
		if err != nil {
			t.Fatalf("quest %d select dungeon 1000 failed: %v", tc.quest, err)
		}
		if err := CheckEntryRoute(c, s.Definition, s.Maze); err != nil {
			t.Fatalf("quest %d CheckEntryRoute failed: %v", tc.quest, err)
		}
		// Simulate reaching the boss room and confirm ClearedMaps includes both
		// the base boss map and its paired layered map (quest 3151 checks 76187,
		// quest 3154 checks 76196).
		s.Visited[tc.bossMap] = nil
		cleared := map[uint32]bool{}
		for _, id := range s.ClearedMaps() {
			cleared[id] = true
		}
		if !cleared[tc.bossMap] || !cleared[tc.layeredMap] {
			t.Fatalf("quest %d cleared maps=%v, want both %d and %d", tc.quest, s.ClearedMaps(), tc.bossMap, tc.layeredMap)
		}
	}
}
