package quest

import (
	"context"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"fmt"
)

func (s *Service) MapClear(ctx context.Context, role storage.Character, run *dungeon.Session, source string) ([]protocol.ActiveQuest, error) {
	if run == nil || !run.Completed() || source != s.Catalog.Source.Checksum {
		return nil, fmt.Errorf("map clear requires confirmed owned source dungeon completion")
	}
	// A room transition requires the previous room to be cleared, so by the
	// time a run completes every room it entered is cleared. A source
	// objective naming any of those maps is satisfied; settling only the final
	// boss room silently stalls objectives set on an earlier room of the run.
	x := s.Index()
	if matches := x.ByDungeon[run.Definition.ID]; len(matches) > 0 {
		var eligible []uint16
		for _, id := range matches {
			d := s.Catalog.Quests[uint32(id)]
			minDiff := d.ObjectiveCells[1].Value
			if d.Kind == "[clear dungeon index]" {
				minDiff = -1
			}
			if int32(run.Difficulty) >= minDiff {
				eligible = append(eligible, id)
			}
		}
		if _, err := s.Store.RecordQuestDungeonClear(ctx, role.AccountID, role.ID, run.RunID, run.Definition.ID, source, ClearDungeon, eligible); err != nil {
			return nil, err
		}
	}
	// [hunt enemy] quests often target destructible/passive scene objects (type 3)
	// or multi-monster groups inside a specific dungeon; clearing that dungeon at
	// the required difficulty settles any remaining progress cleanly.
	if enemies := x.ByEnemyDungeon[run.Definition.ID]; len(enemies) > 0 {
		for _, id := range enemies {
			c := s.Catalog.Quests[uint32(id)].ObjectiveCells
			matched := false
			for i := 0; i < len(c); i += 5 {
				if uint32(c[i].Value) == run.Definition.ID && int32(run.Difficulty) >= c[i+1].Value {
					matched = true
					break
				}
			}
			if matched {
				if _, err := s.Store.CompleteQuestObjective(ctx, role.AccountID, role.ID, id, source, HuntEnemy); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, mapID := range run.ClearedMaps() {
		var eligible []uint16
		for _, id := range x.ByClearMap[mapID] {
			c := s.Catalog.Quests[uint32(id)].ObjectiveCells
			if len(c) == 2 && int32(run.Difficulty) < c[1].Value {
				continue
			}
			eligible = append(eligible, id)
		}
		if _, err := s.Store.RecordQuestMapClear(ctx, role.AccountID, role.ID,
			run.RunID, mapID, source, SingleClearMap, eligible); err != nil {
			return nil, err
		}
	}
	return s.Active(ctx, role)
}
