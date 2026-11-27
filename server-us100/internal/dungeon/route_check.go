package dungeon

import (
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"fmt"
)

// Validate the actual room encoder, not just the presence of a map ID.
func CheckEntryRoute(c catalog.DungeonCatalog, d catalog.DungeonDefinition, m catalog.DungeonMaze) error {
	if len(m.Pending) > 0 {
		return fmt.Errorf("%s", m.Pending[0])
	}
	coords := map[[2]int]bool{}
	ids := map[uint32]bool{}
	for _, r := range m.Rooms {
		pos := [2]int{int(r.X), int(r.Y)}
		if coords[pos] || ids[r.Map] {
			return fmt.Errorf("repeated coordinate or map requires instance-aware rooms")
		}
		coords[pos], ids[r.Map] = true, true
		script, ok := c.Maps[r.Map]
		if !ok {
			return fmt.Errorf("missing map %d", r.Map)
		}
		actors, e := fixedMonsters(script, d.BasisLevel)
		if e != nil {
			return fmt.Errorf("map %d: %w", r.Map, e)
		}
		if _, e = protocol.StartMapUS100(protocol.StartMapState{Map: r.Map, Position: [2]byte{r.X, r.Y}, Monsters: actors}); e != nil {
			return e
		}
	}
	start, boss := [2]int{int(m.Start[0]), int(m.Start[1])}, [2]int{int(m.Boss[0]), int(m.Boss[1])}
	if !coords[start] || !coords[boss] {
		return fmt.Errorf("start or boss outside room set")
	}
	q := [][2]int{start}
	seen := map[[2]int]bool{start: true}
	for i := 0; i < len(q); i++ {
		for _, s := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			next := [2]int{q[i][0] + s[0], q[i][1] + s[1]}
			if coords[next] && !seen[next] {
				seen[next] = true
				q = append(q, next)
			}
		}
	}
	if len(seen) != len(coords) {
		return fmt.Errorf("disconnected room set")
	}
	return nil
}
