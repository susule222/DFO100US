package quest

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"errors"
	"fmt"
)

type Service struct {
	Store       *storage.Store
	Catalog     catalog.QuestCatalog
	Professions catalog.Characters
	Progression *character.ProgressionService
	Inventory   *inventory.Awarder
	index       *Index
}

func (s *Service) Accept(ctx context.Context, role storage.Character, id uint16) (storage.QuestState, error) {
	d, ok := s.Catalog.Quests[uint32(id)]
	if !ok {
		return storage.QuestState{}, errors.New("quest absent from source index")
	}
	if len(d.Pending) > 0 {
		return storage.QuestState{}, fmt.Errorf("quest data unresolved: %s", d.Pending[0])
	}
	en := s.Index().Entries[uint32(id)]
	if en.Unverified != "" || !en.RewardUsable {
		return storage.QuestState{}, fmt.Errorf("quest requires unsupported condition or reward: %s", en.Unverified)
	}
	var charState character.State
	if e := json.Unmarshal(role.State, &charState); e != nil {
		return storage.QuestState{}, e
	}
	job := s.Professions.Professions[role.Profession].Job
	if !characterEligible(en, job, charState.Advancement, charState.Awakening) {
		return storage.QuestState{}, errors.New("quest profession or advancement requirement not met")
	}
	if en.PreAnswerQuest != 0 {
		states, e := s.Store.Quests(ctx, role.AccountID, role.ID)
		if e != nil {
			return storage.QuestState{}, e
		}
		done := false
		for _, q := range states {
			if uint32(q.ID) == en.PreAnswerQuest && q.Status == "completed" {
				done = true
				break
			}
		}
		if !done {
			return storage.QuestState{}, fmt.Errorf("prerequisite answer quest %d incomplete", en.PreAnswerQuest)
		}
	}
	initial, model, e := InitialProgress(d)
	if e != nil {
		return storage.QuestState{}, e
	}
	return s.Store.AcceptQuest(ctx, role.AccountID, role.ID, id, s.Catalog.Source.Checksum, d.MinimumLevel, d.MaximumLevel, d.Prerequisites, initial, model, en.Links)
}
