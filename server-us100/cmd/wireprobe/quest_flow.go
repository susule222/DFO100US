package main

import (
	"context"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"encoding/binary"
	"fmt"
	"time"
)

func (w *worldSession) finishQuest(r protocol.QuestSubmitRequest) ([]outboundPacket, error) {
	if w == nil || w.quests == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("quest submit before character selection")
	}
	if w.answeredQuests[r.ID] {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, e := w.quests.Finish(ctx, w.role, r)
	if e != nil {
		return nil, e
	}
	result.Role.WireID = w.role.WireID
	if _, e = w.quests.RefreshLedger(ctx, result.Role); e != nil {
		return nil, e
	}
	active, e := w.quests.Active(ctx, result.Role)
	if e != nil {
		return nil, e
	}
	done, e := w.quests.Completed(ctx, result.Role)
	if e != nil {
		return nil, e
	}
	triggers, e := protocol.QuestTriggers(active)
	if e != nil {
		return nil, e
	}
	completed, e := protocol.CompletedQuests(done)
	if e != nil {
		return nil, e
	}
	experience, e := character.ExperiencePayload(result.Role)
	if e != nil {
		return nil, e
	}
	var plan []outboundPacket
	// The native terminal ACK mutates a live quest object. After a reconnect,
	// restore saved state without dereferencing that already-completed object.
	if result.Applied {
		ack, err := protocol.QuestFinishedNoItems(r.ID, result.Receipt.Experience)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"quest_finished", 1, 34, ack})
	}
	// Resend the ordinary bag whenever the reward changed it - items OR gold.
	// The gold balance rides the bag as the slot-0/template-0 row (Bag.Rows),
	// so a quest that pays only gold (its [job] item tuples do not match this
	// character) must still refresh it; otherwise the wallet never ticks up in
	// town even though the balance is committed. Live capture 20260911T215854
	// shows quest 3149 crediting 4300 gold with no items and no NOTI13, so the
	// on-screen number stayed put until the next relog.
	if len(result.Receipt.Items) > 0 || result.Receipt.Gold > 0 || result.Receipt.Consumed {
		bag, e := inventory.ReadBag(result.Role.State)
		if e != nil {
			return nil, e
		}
		body, e := protocol.US224InventoryRestoreWithExpansion(bag.ExpandedSlots, bag.Rows())
		if e != nil {
			return nil, e
		}
		plan = append(plan, outboundPacket{"quest_inventory_committed", 0, 13, body})
	}
	plan = append(plan, outboundPacket{"quest_experience", 0, 37, experience}, outboundPacket{"quest_triggers_updated", 0, 291, triggers}, outboundPacket{"completed_quests_updated", 0, 342, completed})
	w.role, w.level = result.Role, experience[0]
	available, e := w.availableQuestPayload(ctx)
	if e != nil {
		return nil, e
	}
	plan = append(plan, outboundPacket{"available_quests_updated", 0, 21, available})
	return plan, nil
}

func (w *worldSession) availableQuestPayload(ctx context.Context) ([]byte, error) {
	ids, e := w.quests.Available(ctx, w.role)
	if e != nil {
		return nil, e
	}
	return protocol.AvailableQuests(w.level, ids)
}

func (w *worldSession) questInteraction(p []byte) ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 || w.quests == nil || w.activeDungeon != nil {
		return nil, fmt.Errorf("NPC interaction requires an owned town character")
	}
	// US100 sends the short, 8-byte check on NPC dialogue as well as
	// the 16-byte form. Both carry the same ID and zero reserved fields.
	if (len(p) != 8 && len(p) != 16) || binary.LittleEndian.Uint16(p) != 33 {
		return nil, fmt.Errorf("unsupported quest check request")
	}
	for _, v := range p[4:] {
		if v != 0 {
			return nil, fmt.Errorf("unsupported quest check fields")
		}
	}
	id := binary.LittleEndian.Uint16(p[2:])
	en := w.quests.Index().Entries[uint32(id)]
	if en == nil || !en.Implemented {
		return nil, nil
	}
	npcPresentInArea := func(npc uint32) bool {
		if w.service.HasNPC(w.state.Position, npc) {
			return true
		}
		// Phase-only NPCs (spawned on alternate [phase] maps absent from the
		// base-phase world catalog, e.g. Lv.82+ and Lv.95-100 scenario NPCs)
		// cannot appear in base [NPC] lists; allow them in any valid town area.
		return !w.service.CatalogHasNPC(npc) && w.service.HasArea(w.state.Position)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch en.Model {
	case "single-meet-npc-remaining-v1":
		if !npcPresentInArea(en.NPC) {
			return nil, fmt.Errorf("quest NPC is absent from current source area")
		}
		if e := w.quests.MeetNPC(ctx, w.role, id, en.NPC); e != nil {
			return nil, e
		}
	case "single-reach-range-remaining-v1":
		if en.Range.NPC != 0 {
			if !npcPresentInArea(en.Range.NPC) {
				return nil, fmt.Errorf("quest range NPC is absent from current source area")
			}
		} else if w.state.Position.Town != en.Range.Town || w.state.Position.Area != en.Range.Area {
			return nil, fmt.Errorf("character is not in target quest area %d/%d", en.Range.Town, en.Range.Area)
		}
		if e := w.quests.SettleReachRange(ctx, w.role, id); e != nil {
			return nil, e
		}
	case "seek-items-and-meet-npc-remaining-v1":
		if !npcPresentInArea(en.NPC) {
			return nil, fmt.Errorf("quest NPC is absent from current source area")
		}
		if e := w.quests.SettleSeekAndMeetNPC(ctx, w.role, id); e != nil {
			return nil, e
		}
	case "look-cinematic-client-gated-v1", "question-client-gated-v1", "use-item-client-gated-v1":
		// Already ready at Progress=0; fall through to emit NOTI 291 confirmation.
	default:
		return nil, nil
	}
	active, e := w.quests.Active(ctx, w.role)
	if e != nil {
		return nil, e
	}
	body, e := protocol.QuestTriggers(active)
	if e != nil {
		return nil, e
	}
	return []outboundPacket{{"quest_npc_objective", 0, 291, body}}, nil
}
