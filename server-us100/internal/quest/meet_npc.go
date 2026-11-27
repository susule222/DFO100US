package quest

import (
	"context"
	"dfolan/internal/storage"
	"fmt"
)

// The world handler supplies a source NPC present in the owned town area.
// A request never chooses its own progress, reward, or arbitrary NPC identity.
func (s *Service) MeetNPC(ctx context.Context, role storage.Character, id uint16, npc uint32) error {
	d, ok := s.Catalog.Quests[uint32(id)]
	if !ok {
		return fmt.Errorf("unknown quest")
	}
	_, model, e := InitialProgress(d)
	if e != nil || model != SingleMeetNPC || uint32(d.ObjectiveCells[0].Value) != npc {
		return fmt.Errorf("NPC does not match quest objective")
	}
	// Ownership, accepted status and source/model gates live in storage; the
	// settle is idempotent, so a replayed meeting is not an error.
	ok, e = s.Store.SettleQuestObjective(ctx, role.AccountID, role.ID, id, s.Catalog.Source.Checksum, model)
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("NPC quest is not active for this owner")
	}
	return nil
}
