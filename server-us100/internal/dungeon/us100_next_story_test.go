package dungeon

import (
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
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
