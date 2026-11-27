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
	"dfolan/internal/world"
	"encoding/json"
	"fmt"
	"time"
)

func questChainCheck(ctx context.Context, s *storage.Store, other int64) error {
	c, e := catalog.LoadCharacters("configs/characters.next25.json")
	if e != nil {
		return e
	}
	cs, e := character.New(s, c, character.Rules{MaxCharacters: 1, InitialLevel: 1})
	if e != nil {
		return e
	}
	a, e := s.DevelopmentAccount(ctx, "temporary-quest-chain")
	if e != nil {
		return e
	}
	r, e := cs.Create(ctx, a, request("ChainTest"))
	if e != nil {
		return e
	}
	q, e := catalog.LoadQuests("configs/quests.generated.json")
	if e != nil {
		return e
	}
	p, e := catalog.LoadProgression("configs/progression.next25.json")
	if e != nil {
		return e
	}
	rules, e := progression.LoadRules("configs/experience.compat90.json")
	if e != nil {
		return e
	}
	qs := quest.Service{Store: s, Catalog: q, Professions: c, Progression: &character.ProgressionService{Store: s, Catalog: p, Professions: c, Rules: rules}}
	contains := func(ids []uint32, id uint32) bool {
		for _, v := range ids {
			if v == id {
				return true
			}
		}
		return false
	}
	ids, e := qs.Available(ctx, r)
	if e != nil {
		return e
	}
	if contains(ids, 4873) {
		return fmt.Errorf("underlevel successor visible")
	}
	var state character.State
	if e = json.Unmarshal(r.State, &state); e != nil {
		return e
	}
	state.Level = 5
	state.Experience = p.Thresholds[3]
	r.State, e = json.Marshal(state)
	if e != nil {
		return e
	}
	if e = setCharState(s, r.ID, r.State); e != nil {
		return e
	}
	if _, e = qs.Accept(ctx, r, 4873); e == nil {
		return fmt.Errorf("successor accepted without source prerequisite")
	}
	// Prerequisite completion is a fixture exclusively in this test database.
	if e = rowInsert(s, "character_quests", idKey(r.ID)+"/3145", map[string]any{
		"character_id": r.ID, "quest_id": 3145, "status": "completed", "progress": 0,
		"config_version": r.ConfigVersion, "progress_model": quest.SingleClearMap,
		"accepted_at": time.Now().UTC().Format(time.RFC3339Nano),
	}); e != nil {
		return e
	}
	ids, e = qs.Available(ctx, r)
	if e != nil || !contains(ids, 4873) || contains(ids, 3146) {
		return fmt.Errorf("source successor eligibility failed: %v", e)
	}
	if _, e = qs.Accept(ctx, r, 4873); e != nil {
		return e
	}
	wrong := r
	wrong.AccountID = other
	if e = qs.MeetNPC(ctx, wrong, 4873, 1); e == nil {
		return fmt.Errorf("NPC objective crossed owner")
	}
	if e = qs.MeetNPC(ctx, r, 4873, 2); e == nil {
		return fmt.Errorf("wrong NPC completed objective")
	}
	wc, e := catalog.LoadWorld("configs/world.generated.json")
	if e != nil {
		return e
	}
	ws := world.Service{Catalog: wc}
	if !ws.HasNPC(storage.WorldPosition{Town: 38, Area: 0}, 1) || ws.HasNPC(storage.WorldPosition{Town: 38, Area: 1}, 1) {
		return fmt.Errorf("NPC area source mismatch")
	}
	if e = qs.MeetNPC(ctx, r, 4873, 1); e != nil {
		return e
	}
	if e = qs.MeetNPC(ctx, r, 4873, 1); e != nil {
		return e
	}
	result, e := qs.Finish(ctx, r, protocol.QuestSubmitRequest{ID: 4873, RewardSelection: 65535, Option: 1})
	if e != nil {
		return e
	}
	if !result.Applied {
		return fmt.Errorf("NPC quest not completed")
	}
	ids, e = qs.Available(ctx, result.Role)
	if e != nil || contains(ids, 4873) || !contains(ids, 3146) {
		return fmt.Errorf("NPC completion did not unlock next source quest: %v", e)
	}
	lc, e := catalog.LoadLoot("configs/loot.next25.json")
	if e != nil {
		return e
	}
	br, e := inventory.LoadBagRules("configs/inventory.next29.json")
	if e != nil {
		return e
	}
	ec, e := inventory.LoadEquipmentCatalog("configs/quest-equipment.next29.json", r.ConfigVersion)
	if e != nil {
		return e
	}
	qs.Inventory = &inventory.Awarder{Catalog: lc, Rules: br, Equipment: ec}
	r = result.Role
	if _, e = qs.Accept(ctx, r, 3146); e != nil {
		return e
	}
	// A completed-map fixture isolates inventory/EXP/completion atomicity.
	zero := 0
	if e = setQuest(s, r.ID, 3146, &zero, nil); e != nil {
		return e
	}
	full := inventory.Bag{Version: "ordinary-bag-v1", Gold: 123}
	for slot := uint16(9); slot <= 64; slot++ {
		full.Equipment = append(full.Equipment, inventory.BagEquipment{Slot: slot, Template: 20002})
	}
	fullState, e := inventory.SaveBag(r.State, full)
	if e != nil {
		return e
	}
	if e = setCharState(s, r.ID, fullState); e != nil {
		return e
	}
	finish := protocol.QuestSubmitRequest{ID: 3146, RewardSelection: 65535, Option: 1}
	if _, e = qs.Finish(ctx, r, finish); e == nil {
		return fmt.Errorf("full bag accepted a partial quest reward")
	}
	if receipts := rowCount(s, "character_quest_rewards", idKey(r.ID)+"/3146"); receipts != 0 {
		return fmt.Errorf("failed quest kept a reward receipt")
	}
	charRow, e := rowGet(s, "characters", idKey(r.ID))
	if e != nil {
		return e
	}
	unchanged, e := json.Marshal(charRow["state"])
	if e != nil {
		return e
	}
	var before, after character.State
	if e = json.Unmarshal(fullState, &before); e != nil {
		return e
	}
	if e = json.Unmarshal(unchanged, &after); e != nil {
		return e
	}
	checkBag, e := inventory.ReadBag(unchanged)
	if e != nil {
		return e
	}
	if before.Experience != after.Experience || before.Level != after.Level || before.SkillPoints != after.SkillPoints || checkBag.Gold != 123 || len(checkBag.Equipment) != 56 {
		return fmt.Errorf("full-bag refusal partially changed EXP, SP or inventory")
	}
	emptyState, e := inventory.SaveBag(r.State, inventory.Bag{Version: "ordinary-bag-v1", Gold: 123})
	if e != nil {
		return e
	}
	if e = setCharState(s, r.ID, emptyState); e != nil {
		return e
	}
	result, e = qs.Finish(ctx, r, finish)
	if e != nil {
		return e
	}
	if !result.Applied || len(result.Receipt.Items) != 3 {
		return fmt.Errorf("source equipment rewards missing")
	}
	settled := result
	result, e = qs.Finish(ctx, result.Role, finish)
	if e != nil || result.Applied {
		return fmt.Errorf("equipment quest replay failed: %v", e)
	}
	bag, e := inventory.ReadBag(result.Role.State)
	if e != nil {
		return e
	}
	// Completion gold comes from the source [gold reward table]; the replay
	// must not pay it twice, and nothing else may touch the 123 baseline.
	if bag.Gold != 123+settled.Receipt.Gold || len(bag.Equipment) != 3 {
		return fmt.Errorf("quest reward replay changed balance: gold=%d want=%d equipment=%d", bag.Gold, 123+settled.Receipt.Gold, len(bag.Equipment))
	}
	for i, id := range []uint32{20002, 24002, 22002} {
		if bag.Equipment[i].Template != id || bag.Equipment[i].Slot != uint16(9+i) {
			return fmt.Errorf("quest equipment source/order changed")
		}
	}
	fmt.Println("QUEST_EQUIPMENT_PASS three_source_accessories=true atomic_bag_full_refusal=true receipt_once=true source_gold_paid_once=true")
	fmt.Println("QUEST_CHAIN_PASS source_pre_required=true level5_gate=true NPC1_area=true owner_checked=true next_3146=true")
	return nil
}
