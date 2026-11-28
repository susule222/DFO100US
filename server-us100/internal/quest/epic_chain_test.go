package quest

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/progression"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func TestAllUS100EpicQuestsAreImplementedAndUnblocked(t *testing.T) {
	dir := filepath.Join("..", "..", "configs", "us100")
	qc, err := catalog.LoadQuests(filepath.Join(dir, "quests.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	idx := BuildIndex(qc)
	epicCount := 0
	for _, id := range idx.Ordered {
		d := qc.Quests[id]
		isEpic := false
		for _, tok := range cells(d.Script.Cells, "[grade]") {
			if tok.Text == "[epic]" {
				isEpic = true
				break
			}
		}
		if !isEpic {
			continue
		}
		epicCount++
		en := idx.Entries[id]
		if !en.Implemented || !en.RewardUsable || !en.GrowUsable || en.Unverified != "" {
			t.Errorf("epic quest %d (lv %d, kind %s) blocked: impl=%v reward=%v grow=%v unverified=%q",
				id, en.MinimumLevel, en.Kind, en.Implemented, en.RewardUsable, en.GrowUsable, en.Unverified)
		}
	}
	if epicCount < 1000 {
		t.Fatalf("expected >1000 epic quests in US100 catalog, got %d", epicCount)
	}
}

func TestEpicProgressionLv15ToLv100ChainsAndBranching(t *testing.T) {
	dir := filepath.Join("..", "..", "configs", "us100")
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "epic_chain.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	qc, err := catalog.LoadQuests(filepath.Join(dir, "quests.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	prof, err := catalog.LoadCharacters(filepath.Join(dir, "characters.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	progCat, err := catalog.LoadProgression(filepath.Join(dir, "progression.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	expRules, err := progression.LoadRules(filepath.Join(dir, "rules.experience.json"))
	if err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot(filepath.Join(dir, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gearCat, err := inventory.LoadEquipmentCatalog(filepath.Join(dir, "equipment.generated.json"), qc.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules(filepath.Join(dir, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}

	progSvc := &character.ProgressionService{Store: store, Catalog: progCat, Professions: prof, Rules: expRules}
	awarder := &inventory.Awarder{Catalog: lootCat, Equipment: gearCat, Rules: bagRules}
	svc := &Service{Store: store, Catalog: qc, Professions: prof, Progression: progSvc, Inventory: awarder}

	accID, err := store.DevelopmentAccount(ctx, "epic_tester")
	if err != nil {
		t.Fatal(err)
	}
	p0 := prof.Professions[0]
	initState := character.State{
		Level:         19,
		Experience:    progCat.Thresholds[17],
		Advancement:   0,
		SourceSHA256:  p0.RawSHA256,
		Attributes:    p0.InitialAttributes,
		InitialSkills: p0.InitialSkills,
	}
	rawState, err := json.Marshal(initState)
	if err != nil {
		t.Fatal(err)
	}
	rawState, err = inventory.SaveBag(rawState, inventory.Bag{Version: "ordinary-bag-v1", Gold: 1000})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "EpicRunner",
		Profession:    0,
		ConfigVersion: qc.Source.Checksum,
		State:         rawState,
	}, 16)
	if err != nil {
		t.Fatal(err)
	}

	markCompleted := func(qids ...uint16) {
		t.Helper()
		if err := store.Database().Mutate(func(tx *db100.Tx) error {
			for _, qid := range qids {
				if err := tx.Put("character_quests", fmt.Sprintf("%d/%d", role.ID, qid), map[string]any{
					"character_id":   role.ID,
					"quest_id":       int(qid),
					"status":         "completed",
					"progress":       0,
					"config_version": qc.Source.Checksum,
					"progress_model": SingleClearMap,
				}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. After completing Q3154 at Lv.19, Q3155 (with [collision accept quest] 12497) MUST be offered and acceptable.
	markCompleted(3154)
	avail, err := svc.Available(ctx, role)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(avail, 3155) {
		t.Fatalf("expected epic quest 3155 to be available after 3154 at Lv.19")
	}
	if _, err := svc.Accept(ctx, role, 3155); err != nil {
		t.Fatalf("accept Q3155 failed: %v", err)
	}

	// 2. Q3162 ([reach the range] sub=0, NPC 13) must be offered after 3161 and settle via ProximityProgress near NPC 13.
	markCompleted(3155, 3156, 3158, 3159, 3160, 3161)
	avail, err = svc.Available(ctx, role)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(avail, 3162) {
		t.Fatalf("expected epic quest 3162 ([reach the range] sub=0) to be available after 3161")
	}
	if _, err := svc.Accept(ctx, role, 3162); err != nil {
		t.Fatalf("accept Q3162 failed: %v", err)
	}
	advQ, err := svc.ProximityProgress(ctx, role, storage.WorldPosition{Town: 40, Area: 3, X: 500, Y: 200}, func(npc uint32) ([2]uint16, bool) {
		if npc == 13 {
			return [2]uint16{520, 210}, true
		}
		return [2]uint16{}, false
	})
	if err != nil || !slices.Contains(advQ, 3162) {
		t.Fatalf("expected Q3162 to settle near NPC 13, got advanced=%v err=%v", advQ, err)
	}

	// 3. Lv.50 multi-block [pre required quest] OR-of-ANDs (Q3427) + [target character] (Q3367) + [awakening type] (Q3368).
	setLevelAdv := func(lv, adv, awak byte) {
		t.Helper()
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(role.State, &doc); err != nil {
			t.Fatal(err)
		}
		doc["level"], _ = json.Marshal(lv)
		doc["advancement"], _ = json.Marshal(adv)
		doc["awakening"], _ = json.Marshal(awak)
		if lv > 1 && int(lv-2) < len(progCat.Thresholds) {
			doc["experience"], _ = json.Marshal(progCat.Thresholds[lv-2])
		}
		role.State, _ = json.Marshal(doc)
		if err := store.Database().Mutate(func(tx *db100.Tx) error {
			var row map[string]any
			if _, err := tx.Get("characters", strconv.FormatInt(role.ID, 10), &row); err != nil {
				return err
			}
			row["state"] = json.RawMessage(role.State)
			return tx.Put("characters", strconv.FormatInt(role.ID, 10), row)
		}); err != nil {
			t.Fatal(err)
		}
	}

	setLevelAdv(50, 0, 0)
	markCompleted(3356)
	avail, err = svc.Available(ctx, role)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(avail, 3367) {
		t.Fatalf("expected Lv.50 swordman adv=0 to receive Q3367 ([target character])")
	}
	if slices.Contains(avail, 3368) {
		t.Fatalf("adv=0 swordman should not receive adv=1 Q3368")
	}

	// Switch to adv=1, awakening=0: Q3368 ([awakening type] 1) offered, Finish sets awakening=1 and unlocks Q3427!
	setLevelAdv(50, 1, 0)
	avail, err = svc.Available(ctx, role)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(avail, 3368) {
		t.Fatalf("expected Lv.50 swordman adv=1 to receive Q3368 ([awakening type])")
	}
	if _, err := svc.Accept(ctx, role, 3368); err != nil {
		t.Fatalf("accept Q3368 failed: %v", err)
	}
	if _, err := store.CompleteQuestObjective(ctx, accID, role.ID, 3368, qc.Source.Checksum, SingleClearMap); err != nil {
		t.Fatalf("complete Q3368 objective failed: %v", err)
	}
	fin, err := svc.Finish(ctx, role, protocol.QuestSubmitRequest{ID: 3368, Option: 1, RewardSelection: 65535})
	if err != nil || !fin.Applied {
		t.Fatalf("finish Q3368 failed: %+v err=%v", fin, err)
	}
	role = fin.Role
	var stAfter character.State
	if err := json.Unmarshal(role.State, &stAfter); err != nil {
		t.Fatal(err)
	}
	if stAfter.Awakening != 1 {
		t.Fatalf("expected Awakening=1 after Q3368, got %d", stAfter.Awakening)
	}
	bagAfter, err := inventory.ReadBag(role.State)
	if err != nil || bagAfter.Gold < 1000 {
		t.Fatalf("expected inventory preserved after awakening reward, got bag=%+v err=%v", bagAfter, err)
	}

	// Q3427 has 79 [pre required quest] blocks; completing ONLY Q3368 must unlock and accept Q3427.
	avail, err = svc.Available(ctx, role)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(avail, 3427) {
		t.Fatalf("expected Q3427 to unlock after completing one of its 79 prereq blocks (3368)")
	}
	if _, err := svc.Accept(ctx, role, 3427); err != nil {
		t.Fatalf("accept Q3427 failed: %v", err)
	}
}
