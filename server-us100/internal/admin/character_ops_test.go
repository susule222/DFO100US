package admin

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/inventory"
	"dfolan/internal/progression"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupCharacterOpHarness(t *testing.T) (*Service, *storage.Store, int64, storage.Character, string) {
	t.Helper()
	ctx := context.Background()
	temp := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(temp, "ops.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "probe-ops")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join("..", "..", "configs", "us100")
	charsCat, err := catalog.LoadCharacters(filepath.Join(cfg, "characters.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	progCat, err := catalog.LoadProgression(filepath.Join(cfg, "progression.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	progRules, err := progression.LoadRules(filepath.Join(cfg, "rules.experience.json"))
	if err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot(filepath.Join(cfg, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gearCat, err := inventory.LoadEquipmentCatalog(filepath.Join(cfg, "equipment.generated.json"), lootCat.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules(filepath.Join(cfg, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	questCat, err := catalog.LoadQuests(filepath.Join(cfg, "quests.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	charSvc, err := character.New(store, charsCat, character.Rules{MaxCharacters: 20, InitialLevel: 1})
	if err != nil {
		t.Fatal(err)
	}
	progSvc := &character.ProgressionService{Store: store, Catalog: progCat, Professions: charsCat, Rules: progRules}
	awarder := &inventory.Awarder{Catalog: lootCat, Rules: bagRules, Equipment: gearCat}
	questSvc := &quest.Service{Store: store, Catalog: questCat, Professions: charsCat, Progression: progSvc, Inventory: awarder}

	prof := charsCat.Professions[0]
	initState, err := json.Marshal(character.State{
		Level:         1,
		Attributes:    prof.InitialAttributes,
		InitialSkills: prof.InitialSkills,
		SourcePath:    prof.Path,
		SourceSHA256:  prof.RawSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "ophero",
		Profession:    0,
		ConfigVersion: charsCat.Source.Checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		Store:       store,
		Awarder:     awarder,
		Characters:  charSvc,
		Progression: progSvc,
		Quests:      questSvc,
		Operator:    "test",
	}
	return svc, store, account, role, temp
}

func TestCharacterOpsEndToEnd(t *testing.T) {
	svc, store, account, role, temp := setupCharacterOpHarness(t)
	ctx := context.Background()
	pending := filepath.Join(temp, "pending")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}

	runJob := func(hexID string, op CharacterOp) OpReceipt {
		t.Helper()
		b, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pending, hexID+".json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		notified := int64(0)
		if err := DrainAndNotify(temp, svc, account, func(id int64) { notified = id }); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(temp, "receipts", hexID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Error     string    `json:"error"`
			OpReceipt OpReceipt `json:"op_receipt"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || env.Error != "" {
			t.Fatalf("job %s failed: %s (%v)", hexID, string(raw), err)
		}
		if op.CharacterID > 0 && notified != op.CharacterID {
			t.Fatalf("expected notify %d, got %d", op.CharacterID, notified)
		}
		return env.OpReceipt
	}

	// 1. Currency: add cera + set gold
	rec := runJob("11111111111111111111111111111111", CharacterOp{
		CharacterID: role.ID, Action: "currency",
		CeraMode: "add", Cera: 50000,
		GoldMode: "set", Gold: 1234567,
		Reason: "top up",
	})
	if rec.CeraBalance != 50000 || rec.GoldAfter != 1234567 {
		t.Fatalf("unexpected currency receipt: %+v", rec)
	}
	// Idempotency replay must not double-add cera.
	recReplay := runJob("11111111111111111111111111111111", CharacterOp{
		CharacterID: role.ID, Action: "currency",
		CeraMode: "add", Cera: 50000,
		Reason: "top up replay",
	})
	if recReplay.CeraBalance != 50000 {
		t.Fatalf("cera replay mutated balance: %+v", recReplay)
	}
	cera, _ := store.AccountCera(ctx, account)
	if cera != 50000 {
		t.Fatalf("stored cera=%d, want 50000", cera)
	}

	// 2. Level + SP + TP
	tpVal := 45
	rec = runJob("22222222222222222222222222222222", CharacterOp{
		CharacterID: role.ID, Action: "level",
		Level: 100, SyncSP: true, TP: &tpVal,
		Reason: "boost 100",
	})
	if rec.Level != 100 || rec.TP != 45 || rec.SP == 0 {
		t.Fatalf("unexpected level receipt: %+v", rec)
	}

	// 3. Advancement (Swordman -> 3 Berserker) with skill reset
	rec = runJob("33333333333333333333333333333333", CharacterOp{
		CharacterID: role.ID, Action: "advancement",
		Advancement: 3, ResetSkills: true,
		Reason: "job change",
	})
	if rec.Advancement != 3 {
		t.Fatalf("unexpected advancement receipt: %+v", rec)
	}
	// Verify advanced character can still receive ApplyGain (no longer blocked by advancement != 0)
	live, _ := store.LiveCharacter(account, role.ID)
	if _, _, err := svc.Progression.ApplyGain(live, 100); err != nil {
		t.Fatalf("advanced character ApplyGain failed: %v", err)
	}

	// 4. Bag management: grant items, set amount, sort, repair, delete, clear
	g := storage.Grant{ID: "seed-item-1", AccountID: account, Character: role.ID, Reason: "seed", Operator: "test", Items: []storage.GrantItem{{Template: 10165055, Amount: 2}}}
	if _, _, err := svc.Apply(ctx, g); err != nil {
		t.Fatal(err)
	}
	g2 := storage.Grant{ID: "seed-eq-1", AccountID: account, Character: role.ID, Reason: "seed", Operator: "test", Items: []storage.GrantItem{{Template: 10018, Amount: 1}}}
	if _, _, err := svc.Apply(ctx, g2); err != nil {
		t.Fatal(err)
	}
	live, _ = store.LiveCharacter(account, role.ID)
	bag, _ := inventory.ReadBag(live.State)
	if len(bag.Items) != 1 || len(bag.Equipment) != 1 {
		t.Fatalf("unexpected initial bag: %+v", bag)
	}
	itemSlot := bag.Items[0].Slot
	eqSlot := bag.Equipment[0].Slot

	runJob("44444444444444444444444444444444", CharacterOp{
		CharacterID: role.ID, Action: "bag", Op: "set_amount", Slot: itemSlot, Amount: 25, Reason: "edit qty",
	})
	live, _ = store.LiveCharacter(account, role.ID)
	bag, _ = inventory.ReadBag(live.State)
	if bag.Items[0].Amount != 25 {
		t.Fatalf("amount not updated: %+v", bag.Items)
	}
	runJob("44444444444444444444444444444445", CharacterOp{
		CharacterID: role.ID, Action: "bag", Op: "sort", Reason: "sort bag",
	})
	runJob("44444444444444444444444444444446", CharacterOp{
		CharacterID: role.ID, Action: "bag", Op: "delete_slot", Slot: eqSlot, List: "equipment", Reason: "del eq",
	})
	live, _ = store.LiveCharacter(account, role.ID)
	bag, _ = inventory.ReadBag(live.State)
	if len(bag.Equipment) != 0 || bag.Gold != 1234567 {
		t.Fatalf("delete_slot unexpected bag: %+v", bag)
	}

	// 5. Quest management: accept, complete_objective, finish, complete_epic_to_level, abandon
	runJob("55555555555555555555555555555551", CharacterOp{
		CharacterID: role.ID, Action: "quest", Op: "accept", QuestID: 3145, Force: true, Reason: "accept",
	})
	runJob("55555555555555555555555555555552", CharacterOp{
		CharacterID: role.ID, Action: "quest", Op: "complete_objective", QuestID: 3145, Reason: "ready",
	})
	qs, _ := store.Quests(ctx, account, role.ID)
	if len(qs) != 1 || qs[0].Status != "accepted" || qs[0].Progress != 0 {
		t.Fatalf("quest objective not settled: %+v", qs)
	}
	runJob("55555555555555555555555555555553", CharacterOp{
		CharacterID: role.ID, Action: "quest", Op: "finish", QuestID: 3145, Reason: "finish",
	})
	qs, _ = store.Quests(ctx, account, role.ID)
	if len(qs) != 1 || qs[0].Status != "completed" {
		t.Fatalf("quest not completed: %+v", qs)
	}
	recEpic := runJob("55555555555555555555555555555554", CharacterOp{
		CharacterID: role.ID, Action: "quest", Op: "complete_epic_to_level", MaxLevel: 10, Reason: "skip epic",
	})
	if recEpic.Affected == 0 {
		t.Fatalf("expected epic quests completed, got 0")
	}
	runJob("55555555555555555555555555555555", CharacterOp{
		CharacterID: role.ID, Action: "quest", Op: "abandon", QuestID: 3145, Reason: "reset",
	})
	qs, _ = store.Quests(ctx, account, role.ID)
	for _, q := range qs {
		if q.ID == 3145 {
			t.Fatalf("quest 3145 was not abandoned")
		}
	}
}
