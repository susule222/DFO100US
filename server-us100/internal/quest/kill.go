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
	x := s.Index()
	var monsterIDs []uint16
	var enemyIDs []uint16
	for _, m := range run.Monsters {
		if m.Entity != entity || m.NonCombat {
			continue
		}
		for _, id := range x.ByMonster[m.Template] {
			c := s.Catalog.Quests[uint32(id)].ObjectiveCells
			for i := 0; i < len(c); i += 4 {
				if uint32(c[i].Value) == run.Definition.ID && int32(run.Difficulty) >= c[i+1].Value && uint32(c[i+2].Value) == m.Template {
					monsterIDs = appendUniqueU16(monsterIDs, id)
					break
				}
			}
		}
		for _, id := range x.ByWildcardDungeon[run.Definition.ID] {
			c := s.Catalog.Quests[uint32(id)].ObjectiveCells
			for i := 0; i < len(c); i += 4 {
				if uint32(c[i].Value) == run.Definition.ID && int32(run.Difficulty) >= c[i+1].Value && c[i+2].Value < 0 {
					monsterIDs = appendUniqueU16(monsterIDs, id)
					break
				}
			}
		}
		for _, id := range x.ByEnemyMonster[m.Template] {
			c := s.Catalog.Quests[uint32(id)].ObjectiveCells
			for i := 0; i < len(c); i += 5 {
				if uint32(c[i].Value) == run.Definition.ID && int32(run.Difficulty) >= c[i+1].Value && uint32(c[i+2].Value) == m.Template {
					enemyIDs = appendUniqueU16(enemyIDs, id)
					break
				}
			}
		}
		break
	}
	changed := false
	if len(monsterIDs) > 0 {
		ok, err := s.Store.CreditQuestKill(ctx, role.AccountID, role.ID, run.RunID, run.Room.Map, entity, s.Catalog.Source.Checksum, HuntMonster, monsterIDs)
		if err != nil {
			return false, err
		}
		changed = changed || ok
	}
	if len(enemyIDs) > 0 {
		ok, err := s.Store.CreditQuestKill(ctx, role.AccountID, role.ID, run.RunID, run.Room.Map, entity, s.Catalog.Source.Checksum, HuntEnemy, enemyIDs)
		if err != nil {
			return false, err
		}
		changed = changed || ok
	}
	return changed, nil
}
