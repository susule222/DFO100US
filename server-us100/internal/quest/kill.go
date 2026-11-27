package quest

import (
	"context"
	"dfolan/internal/dungeon"
	"dfolan/internal/storage"
)

func (s *Service) MonsterKilled(ctx context.Context, role storage.Character, run *dungeon.Session, entity uint16) (bool, error) {
	if run == nil || !run.Loaded || !run.Dead[entity] || run.Unowned[entity] {
		return false, nil
	}
	var ids []uint16
	for _, m := range run.Monsters {
		if m.Entity != entity || m.NonCombat {
			continue
		}
		for _, id := range s.Index().ByMonster[m.Template] {
			c := s.Catalog.Quests[uint32(id)].ObjectiveCells
			if uint32(c[0].Value) == run.Definition.ID && int32(run.Difficulty) >= c[1].Value {
				ids = append(ids, id)
			}
		}
		break
	}
	if len(ids) == 0 {
		return false, nil
	}
	return s.Store.CreditQuestKill(ctx, role.AccountID, role.ID, run.RunID, run.Room.Map, entity, s.Catalog.Source.Checksum, HuntMonster, ids)
}
