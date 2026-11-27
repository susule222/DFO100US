package main

import (
	"context"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/game/wire"
	"dfolan/internal/inventory"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Only the connection owner modifies worldSession or writes inventory packets.
// The inbox worker only invalidates snapshots; one wakeup covers any number of commits.
type gmSessions struct {
	mu         sync.Mutex
	version    uint64
	lastChange map[int64]uint64
	members    map[chan struct{}]int64
}

func (s *gmSessions) beforeSelect() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}
func gmWake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
func (s *gmSessions) bind(ch chan struct{}, id int64, since uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-ch:
	default:
	}
	if id == 0 {
		delete(s.members, ch)
		return
	}
	if s.members == nil {
		s.members = make(map[chan struct{}]int64)
	}
	s.members[ch] = id
	// A grant can commit after the entry snapshot but before entry finishes.
	if s.lastChange[id] > since {
		gmWake(ch)
	}
}
func (s *gmSessions) committed(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	if s.lastChange == nil {
		s.lastChange = make(map[int64]uint64)
	}
	s.lastChange[id] = s.version
	for ch, role := range s.members {
		if role == id {
			gmWake(ch)
		}
	}
}

type gmInput struct {
	frame wire.Frame
	err   error
}

func gmReadFrames(r io.Reader, stop <-chan struct{}) <-chan gmInput {
	out := make(chan gmInput)
	go func() {
		defer close(out)
		for {
			frame, err := wire.ReadClient(r)
			select {
			case out <- gmInput{frame, err}:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

func (w *worldSession) syncGrantedBag(account, id int64, send func(byte, uint16, []byte) error, event func(map[string]any)) error {
	if w == nil || w.loot == nil || w.role.ID != id || w.role.AccountID != account {
		return fmt.Errorf("no matching active role")
	}
	role, err := w.loot.Store.LiveCharacter(account, id)
	if err != nil {
		return err
	}
	if role.ConfigVersion != w.loot.Catalog.Source.Checksum {
		return fmt.Errorf("inventory source mismatch")
	}
	prevBag, _ := inventory.ReadBag(w.role.State)
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return err
	}
	rows := bag.Rows()
	payload, err := protocol.US224InventoryUpdate(rows)
	if err != nil {
		return err
	}
	if err = send(0, 14, payload); err != nil {
		return err
	}
	if len(bag.Avatars) > 0 || len(prevBag.Avatars) > 0 {
		avatarRows := bag.AvatarRows()
		// Clear any previously occupied avatar slots that were removed
		present := make(map[uint16]bool, len(bag.Avatars))
		for _, a := range bag.Avatars {
			present[a.Slot] = true
		}
		for _, old := range prevBag.Avatars {
			if !present[old.Slot] {
				avatarRows = append(avatarRows, protocol.EmptySlotItem(old.Slot))
			}
		}
		if len(avatarRows) > 0 {
			ap, aerr := protocol.US224AvatarUpdate(avatarRows)
			if aerr != nil {
				return aerr
			}
			if err = send(0, 14, ap); err != nil {
				return err
			}
			event(map[string]any{"kind": "gm_avatar_update_sent", "character_id": id, "rows": len(avatarRows)})
		}
	}
	var prevState, nextState character.State
	_ = json.Unmarshal(w.role.State, &prevState)
	_ = json.Unmarshal(role.State, &nextState)
	role.WireID = w.role.WireID
	w.role = role
	if nextState.Level > 0 {
		w.level = nextState.Level
	}
	event(map[string]any{"kind": "gm_inventory_update_sent", "character_id": id, "rows": len(rows)})
	if w.progression != nil && (nextState.Level != prevState.Level || nextState.Experience != prevState.Experience || nextState.SkillPoints != prevState.SkillPoints || nextState.TechniquePoints != prevState.TechniquePoints) {
		if expPayload, err := character.ExperiencePayload(role); err == nil {
			if err = send(0, 37, expPayload); err != nil {
				return err
			}
			event(map[string]any{"kind": "gm_experience_update_sent", "character_id": id, "level": nextState.Level})
		}
	}
	if w.characters != nil && (nextState.Level != prevState.Level || nextState.Advancement != prevState.Advancement || nextState.SkillPoints != prevState.SkillPoints || nextState.TechniquePoints != prevState.TechniquePoints) {
		if skillPayload, err := w.characters.EntrySkills(role); err == nil {
			if err = send(0, 19, skillPayload); err != nil {
				return err
			}
			event(map[string]any{"kind": "gm_skills_update_sent", "character_id": id, "advancement": nextState.Advancement})
		}
	}
	if w.quests != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := w.quests.RefreshLedger(ctx, role); err != nil {
			event(map[string]any{"kind": "gm_quest_refresh_error", "character_id": id, "error": err.Error()})
			return nil
		}
		if active, err := w.quests.Active(ctx, role); err == nil {
			if p, err := protocol.QuestTriggers(active); err == nil {
				if err = send(0, 291, p); err != nil {
					return err
				}
			}
		}
		if done, err := w.quests.Completed(ctx, role); err == nil {
			if p, err := protocol.CompletedQuests(done); err == nil {
				if err = send(0, 342, p); err != nil {
					return err
				}
			}
		}
		if available, err := w.quests.Available(ctx, role); err == nil && w.level > 0 {
			if p, err := protocol.AvailableQuests(w.level, available); err == nil {
				if err = send(0, 21, p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
