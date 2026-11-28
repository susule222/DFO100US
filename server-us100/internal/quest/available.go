package quest

import (
	"context"
	"dfolan/internal/character"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

func matchesCharacterFilter(f CharacterFilter, job string, adv, awakening byte) bool {
	if f.Job != "[all]" && f.Job != job {
		return false
	}
	if f.Grow >= 0 && f.Grow != int32(adv) {
		return false
	}
	if f.Awakening == 0 && awakening != 0 {
		return false
	}
	if f.Awakening > 0 && int32(awakening) < f.Awakening {
		return false
	}
	return true
}

// CharacterEligible reports whether a character with the given job, advancement,
// and awakening stage satisfies the quest's [target character], [non target character],
// [job], and [grow type] filters.
func CharacterEligible(en *Entry, job string, adv, awakening byte) bool {
	if en == nil {
		return false
	}
	return characterEligible(en, job, adv, awakening)
}

func characterEligible(en *Entry, job string, adv, awakening byte) bool {
	if job == "" {
		return false
	}
	if len(en.TargetCharacters) > 0 {
		matched := false
		for _, f := range en.TargetCharacters {
			if matchesCharacterFilter(f, job, adv, awakening) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	} else {
		allowed := len(en.Jobs) == 0
		for _, j := range en.Jobs {
			if j == "[all]" || j == job {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	for _, f := range en.NonTargetCharacters {
		if matchesCharacterFilter(f, job, adv, awakening) {
			return false
		}
	}
	for _, g := range en.GrowTypes {
		if g >= 0 && g != int32(adv) {
			return false
		}
	}
	return true
}

func prerequisitesSatisfied(en *Entry, status map[uint32]string) bool {
	if len(en.PrerequisiteGroups) > 0 {
		for _, group := range en.PrerequisiteGroups {
			allDone := true
			for _, p := range group {
				if status[p] != "completed" {
					allDone = false
					break
				}
			}
			if allDone {
				return true
			}
		}
		return false
	}
	for _, p := range en.Prerequisites {
		if status[p] != "completed" {
			return false
		}
	}
	return true
}

func (s *Service) Available(ctx context.Context, role storage.Character) ([]uint32, error) {
	var state character.State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	if role.ConfigVersion != s.Catalog.Source.Checksum {
		return nil, fmt.Errorf("quest availability source mismatch")
	}
	states, e := s.Store.Quests(ctx, role.AccountID, role.ID)
	if e != nil {
		return nil, e
	}
	status := map[uint32]string{}
	for _, q := range states {
		status[uint32(q.ID)] = q.Status
	}
	job := s.Professions.Professions[role.Profession].Job
	x := s.Index()
	var ids []uint32
	for _, id := range x.Ordered {
		if id == 0 || id >= 40000 || status[id] == "completed" {
			continue
		}
		// Ongoing quests are restored in SELECT, never offered again in NOTI21.
		if status[id] == "accepted" {
			continue
		}
		en := x.Entries[id]
		// A quest whose objective or reward this build cannot settle must not
		// be offered: accepting one strands the character on a quest that can
		// be handed in forever without ever completing.
		if !en.Implemented || !en.RewardUsable || !en.GrowUsable || en.Unverified != "" {
			continue
		}
		if !en.Links.Allows(func(id uint16) string { return status[uint32(id)] }) {
			continue
		}
		if uint32(state.Level) < en.MinimumLevel || uint32(state.Level) > en.MaximumLevel {
			continue
		}
		if !characterEligible(en, job, state.Advancement, state.Awakening) {
			continue
		}
		if !prerequisitesSatisfied(en, status) {
			continue
		}
		if en.PreAnswerQuest != 0 && (status[en.PreAnswerQuest] != "completed" || en.PreAnswerChoice != 0) {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}
