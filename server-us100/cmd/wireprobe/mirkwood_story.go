package main

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/quest"
	"fmt"
)

func (w *worldSession) activeTracker(ctx context.Context, name string) ([]outboundPacket, error) {
	active, e := w.quests.Active(ctx, w.role)
	if e != nil {
		return nil, e
	}
	body, e := protocol.QuestTriggers(active)
	if e != nil {
		return nil, e
	}
	return []outboundPacket{{name, 0, 291, body}}, nil
}

func (w *worldSession) mirkwoodRoom(room uint32) bool {
	s := w.activeDungeon
	return s != nil && s.Loaded && s.Definition.ID == 3 && s.Maze.Quest == 3146 && s.Room.Map == room &&
		w.quests != nil && w.dungeons != nil && w.quests.Catalog.Source.Checksum == w.role.ConfigVersion && w.dungeons.Source.Checksum == w.role.ConfigVersion &&
		w.quests.Catalog.Quests[3146].Script.SHA256 == "806555d2d36b987cc68aff0b2896b696d07909040f36ecc2cd0484852056d1f2" &&
		w.quests.Catalog.Quests[3054].Script.SHA256 == "24e0d1a9459a998b282a015900e6452c4fb61acd33248ff05a5ba6add6b9b7e0" &&
		w.quests.Catalog.Quests[3157].Script.SHA256 == "bb60af65355bcfdbb69c094ceca59e11f8f503e45d81f63278564b2189862bd1" &&
		w.dungeons.Maps[76131].SHA256 == "c5428d90880cfeb8af0ef2219f2d048a4b2ddacaed40c3d5312c00634e9959ba"
}

// Hidden child objectives belong to the accepted epic. Some clients request
// only the first child. Enroll a missing child only at its own gameplay event.
func (w *worldSession) storyChild(ctx context.Context, id uint16) (bool, error) {
	rows, e := w.quests.Store.Quests(ctx, w.role.AccountID, w.role.ID)
	if e != nil {
		return false, e
	}
	parent, child := false, ""
	for _, r := range rows {
		if r.ConfigVersion != w.role.ConfigVersion {
			continue
		}
		if r.ID == 3146 {
			parent = r.Status == "accepted"
		}
		if r.ID == id {
			child = r.Status
		}
	}
	if !parent || child == "completed" {
		return false, nil
	}
	if child == "" {
		_, e = w.quests.Accept(ctx, w.role, id)
	}
	return e == nil, e
}

func (w *worldSession) settleStoryChild(ctx context.Context, id uint16) ([]outboundPacket, error) {
	// Use normal durable reward/parent refresh. Do not send a fabricated
	// command-34 success when the player hasn't submitted a quest in the UI.
	p, e := w.finishQuest(protocol.QuestSubmitRequest{ID: id, Option: 1, RewardSelection: 65535})
	if e != nil {
		return nil, fmt.Errorf("settle story child %d: %w", id, e)
	}
	out := make([]outboundPacket, 0, len(p))
	for _, v := range p {
		if v.Kind != 1 || v.ID != 34 {
			out = append(out, v)
		}
	}
	return out, nil
}

func (w *worldSession) sceneObjectDeath(ctx context.Context) ([]outboundPacket, error) {
	plan := []outboundPacket{{"mirkwood_object_ack", 1, 39, []byte{1}}}
	ok, e := w.storyChild(ctx, 3157)
	if e != nil || !ok {
		return plan, e
	}
	if _, e = w.quests.Store.RecordMirkwoodScene(ctx, w.role, w.activeDungeon.RunID, quest.MirkwoodSceneObject); e != nil {
		return nil, e
	}
	more, e := w.settleStoryChild(ctx, 3157)
	return append(plan, more...), e
}
