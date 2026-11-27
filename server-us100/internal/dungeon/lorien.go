package dungeon

import "fmt"

// CompleteLorienFinale is only for the no-boss 53130.act monster-team-clear
// trigger. Re-entering an empty room does not prove any kills: require the
// actual source roster and its death ledger for this run, not LivingMonsters.
func (s *Session) CompleteLorienFinale() error {
	if s == nil || !s.Definition.Tutorial || s.Definition.ID != 7115 || s.Room.Map != 53130 || !s.Room.Boss || [2]byte{s.Room.X, s.Room.Y} != s.Maze.Boss || !s.Loaded {
		return fmt.Errorf("not the loaded Lorien tutorial finale")
	}
	roster := s.Visited[s.Room.Map]
	if len(roster) != 5 {
		return fmt.Errorf("unexpected Lorien finale roster")
	}
	expected := map[uint32]bool{62971: true, 62965: true, 62966: true, 62967: true, 62968: true}
	seen := map[uint16]bool{}
	for _, m := range roster {
		if !expected[m.Template] || m.Entity == 0 || seen[m.Entity] || m.Rank != 0 || m.NonCombat || !s.Dead[m.Entity] {
			return fmt.Errorf("Lorien finale encounter incomplete")
		}
		seen[m.Entity] = true
		delete(expected, m.Template)
	}
	if len(expected) != 0 || !s.RoomCleared() || s.completionTarget != 0 {
		return fmt.Errorf("conflicting Lorien completion")
	}
	s.completed = true
	return nil
}
