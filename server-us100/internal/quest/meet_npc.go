package quest

import (
	"context"
	"dfolan/internal/inventory"
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

// SettleReachRange settles an accepted [reach the range] quest (both sub=1
// town/area rectangle and sub=0 NPC proximity) after the world layer has
// verified the character is in the target area or at the target NPC.
func (s *Service) SettleReachRange(ctx context.Context, role storage.Character, id uint16) error {
	d, ok := s.Catalog.Quests[uint32(id)]
	if !ok {
		return fmt.Errorf("unknown quest")
	}
	_, model, e := InitialProgress(d)
	if e != nil || model != SingleReachRange {
		return fmt.Errorf("quest is not a reach-the-range objective")
	}
	ok, e = s.Store.SettleQuestObjective(ctx, role.AccountID, role.ID, id, s.Catalog.Source.Checksum, model)
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("reach-range quest is not active for this owner")
	}
	return nil
}

// SettleSeekAndMeetNPC settles an accepted [seek n meet npc] quest once the
// character's bag contains all required items and the world layer has verified
// the target NPC's area.
func (s *Service) SettleSeekAndMeetNPC(ctx context.Context, role storage.Character, id uint16) error {
	d, ok := s.Catalog.Quests[uint32(id)]
	if !ok {
		return fmt.Errorf("unknown quest")
	}
	seek, ok := SeekMeet(d)
	if !ok {
		return fmt.Errorf("quest is not a seek-and-meet objective")
	}
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return err
	}
	if _, enough := takeItems(bag, seek.Items); !enough {
		return fmt.Errorf("required quest items missing")
	}
	settled, e := s.Store.SettleQuestObjective(ctx, role.AccountID, role.ID, id, s.Catalog.Source.Checksum, SeekAndMeetNPC)
	if e != nil {
		return e
	}
	if !settled {
		return fmt.Errorf("seek-and-meet quest is not active for this owner")
	}
	return nil
}
