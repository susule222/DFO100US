package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/progression"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"sync"
)

func questRewardCheck(ctx context.Context, s, reopened *storage.Store, role storage.Character, other int64) error {
	if e := s.MigrateQuestRewards(ctx); e != nil {
		return e
	}
	c, e := catalog.LoadProgression("configs/progression.next25.json")
	if e != nil {
		return e
	}
	prof, e := catalog.LoadCharacters("configs/characters.next25.json")
	if e != nil {
		return e
	}
	rules, e := progression.LoadRules("configs/experience.compat90.json")
	if e != nil {
		return e
	}
	q, e := catalog.LoadQuests("configs/quests.generated.json")
	if e != nil {
		return e
	}
	p := &character.ProgressionService{Store: s, Catalog: c, Professions: prof, Rules: rules}
	service := quest.Service{Store: s, Catalog: q, Professions: prof, Progression: p}
	// The current source pays completion gold from the [gold reward table],
	// and gold settlement goes through the shared inventory awarder — the
	// same source files questChainCheck uses.
	lc, e := catalog.LoadLoot("configs/loot.next25.json")
	if e != nil {
		return e
	}
	br, e := inventory.LoadBagRules("configs/inventory.next29.json")
	if e != nil {
		return e
	}
	ec, e := inventory.LoadEquipmentCatalog("configs/quest-equipment.next29.json", role.ConfigVersion)
	if e != nil {
		return e
	}
	service.Inventory = &inventory.Awarder{Catalog: lc, Rules: br, Equipment: ec}
	req := protocol.QuestSubmitRequest{ID: 3145, RewardSelection: 65535, Option: 1}
	foreign := role
	foreign.AccountID = other
	if _, e = service.Finish(ctx, foreign, req); e == nil {
		return fmt.Errorf("quest reward crossed account")
	}
	// Keep every mutation inside the established throwaway test database.
	one := 1
	if e = setQuest(s, role.ID, 3145, &one, nil); e != nil {
		return e
	}
	if _, e = service.Finish(ctx, role, req); e == nil {
		return fmt.Errorf("unfinished objective rewarded")
	}
	zero := 0
	if e = setQuest(s, role.ID, 3145, &zero, nil); e != nil {
		return e
	}
	rows, e := s.Characters(ctx, role.AccountID)
	if e != nil || len(rows) != 1 {
		return fmt.Errorf("quest test role missing: %v", e)
	}
	saved := rows[0].State
	var state character.State
	if e = json.Unmarshal(saved, &state); e != nil {
		return e
	}
	state.Advancement = 1
	advanced, e := json.Marshal(state)
	if e != nil {
		return e
	}
	if e = setCharState(s, role.ID, advanced); e != nil {
		return e
	}
	if _, e = service.Finish(ctx, role, req); e == nil {
		return fmt.Errorf("matching asset rewards silently discarded")
	}
	if e = setCharState(s, role.ID, saved); e != nil {
		return e
	}
	type result struct {
		r quest.FinishResult
		e error
	}
	results := make(chan result, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := service.Finish(ctx, role, req); results <- result{r, e} }()
	}
	wg.Wait()
	close(results)
	applied := 0
	for res := range results {
		if res.e != nil {
			return res.e
		}
		if res.r.Applied {
			applied++
		}
		if res.r.Receipt.Experience != 1200 {
			return fmt.Errorf("quest source experience mismatch")
		}
		if e = json.Unmarshal(res.r.Role.State, &state); e != nil {
			return e
		}
		if state.Experience != 2303 || state.Level != 3 || state.SkillPoints != [2]uint16{60, 60} {
			return fmt.Errorf("quest retry state mismatch exp=%d level=%d SP=%v", state.Experience, state.Level, state.SkillPoints)
		}
	}
	if applied != 1 {
		return fmt.Errorf("quest duplicate reward applications=%d", applied)
	}
	if count := rowCount(s, "character_quest_rewards", idKey(role.ID)+"/"); count != 1 {
		return fmt.Errorf("quest receipts=%d", count)
	}
	service.Store = reopened
	p.Store = reopened
	rows, e = reopened.Characters(ctx, role.AccountID)
	if e != nil || len(rows) != 1 {
		return fmt.Errorf("reopened quest role missing: %v", e)
	}
	if e = json.Unmarshal(rows[0].State, &state); e != nil {
		return e
	}
	if state.Experience != 2303 || state.Level != 3 {
		return fmt.Errorf("quest reward reopen failed")
	}
	active, e := service.Active(ctx, role)
	if e != nil {
		return e
	}
	for _, a := range active {
		if a.ID == 3145 {
			return fmt.Errorf("completed quest remained active")
		}
	}
	completed, e := service.Completed(ctx, role)
	if e != nil || len(completed) != 1 || completed[0] != 3145 {
		return fmt.Errorf("completed quest bitmap mismatch: %v", e)
	}
	if _, e = service.Accept(ctx, role, 3145); e == nil {
		return fmt.Errorf("completed quest accepted twice")
	}
	fmt.Println("QUEST_REWARD_STORAGE_PASS concurrent_reward_once=true atomic_completion_exp=true source_job_filter=true unfinished_refused=true owner_checked=true reopen=true")
	return nil
}
