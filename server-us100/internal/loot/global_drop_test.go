package loot

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalDropByDifficultyAndAbyssTiers(t *testing.T) {
	config := filepath.Join("..", "..", "configs", "us100")
	ctx := context.Background()
	temp := t.TempDir()
	s, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(temp, "gd.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	acct, err := s.DevelopmentAccount(ctx, "gd-test")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.LoadLoot(filepath.Join(config, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog(filepath.Join(config, "equipment.generated.json"), c.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	if len(gear.RewardPool()) == 0 {
		t.Fatal("reward pool should be populated")
	}
	rules, err := LoadRules(filepath.Join(config, "rules.drop.json"))
	if err != nil {
		t.Fatal(err)
	}
	tables, err := Parse(c)
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules(filepath.Join(config, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := s.CreateCharacter(ctx, storage.Character{AccountID: acct, Name: "gdhero", ConfigVersion: c.Source.Checksum, State: []byte("{}")}, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Pick two distinct real bag-usable equipment IDs from RewardPool
	var idDiff0, idDiff4, idNormalAbyss, idLv100Abyss uint32
	for _, d := range gear.RewardPool() {
		if c.Items[d.ID].Kind == "stackable" {
			continue
		}
		if idDiff0 == 0 {
			idDiff0 = d.ID
		} else if idDiff4 == 0 && d.ID != idDiff0 {
			idDiff4 = d.ID
		} else if idNormalAbyss == 0 && d.ID != idDiff0 && d.ID != idDiff4 {
			idNormalAbyss = d.ID
		} else if idLv100Abyss == 0 && d.ID != idDiff0 && d.ID != idDiff4 && d.ID != idNormalAbyss {
			idLv100Abyss = d.ID
			break
		}
	}
	if idLv100Abyss == 0 {
		t.Fatal("need 4 equipment IDs")
	}

	policy := GlobalDropPolicy{
		Version: 1,
		Global: GlobalEquipmentPolicy{
			Enabled: true,
			Difficulties: []DifficultyEquipmentDrop{
				{Difficulty: 0, Name: "普通", Enabled: true, DropRateMultiplier: 1, EquipDropChance: 100, DropCount: 1, CustomItemIDs: []uint32{idDiff0}},
				{Difficulty: 1, Name: "冒险", Enabled: false, DropRateMultiplier: 1, EquipDropChance: 0, DropCount: 1},
				{Difficulty: 2, Name: "勇士", Enabled: false, DropRateMultiplier: 1, EquipDropChance: 0, DropCount: 1},
				{Difficulty: 3, Name: "王者", Enabled: false, DropRateMultiplier: 1, EquipDropChance: 0, DropCount: 1},
				{Difficulty: 4, Name: "噩梦", Enabled: true, DropRateMultiplier: 1, EquipDropChance: 100, DropCount: 1, CustomItemIDs: []uint32{idDiff4}},
			},
		},
		NormalAbyss: NormalAbyssPolicy{
			Enabled:       true,
			Scope:         "all",
			DropChance:    100,
			DropCount:     1,
			PoolMode:      "custom",
			CustomItemIDs: []uint32{idNormalAbyss},
		},
		Lv100Abyss: Lv100AbyssPolicy{
			Enabled:       true,
			Scope:         "lv100_dungeons",
			DropChance:    100,
			DropCount:     1,
			EpicWeight:    80,
			MythicWeight:  20,
			PoolMode:      "custom",
			CustomItemIDs: []uint32{idLv100Abyss},
		},
	}
	raw, _ := json.Marshal(policy)
	path := filepath.Join(temp, "global_drop.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGlobalDrop(path, gear, c)
	if err != nil || loaded == nil {
		t.Fatal("load global drop failed:", err)
	}

	// Run 1: Difficulty 0 (普通) in Lv.5 dungeon -> gets idDiff0 + idNormalAbyss, NOT idDiff4, NOT idLv100Abyss
	runLow := &dungeon.Session{
		RunID:      "run-low",
		Difficulty: 0,
		Definition: catalog.DungeonDefinition{BasisLevel: 5, MinimumLevel: 1},
		Room:       catalog.DungeonRoom{Map: 58548},
		Loaded:     true,
		Dead:       map[uint16]bool{7: true},
		Monsters:   []protocol.DungeonMonster{{Entity: 7, Level: 5}},
		NextEntity: 8,
	}
	sessLow := NewSession(c, tables, rules, gear, runLow.RunID, acct, role.ID, role.WireID)
	sessLow.GlobalDrop = loaded
	sessLow.seeds[58548] = 999
	dropsLow, err := sessLow.Death(runLow, 7)
	if err != nil {
		t.Fatal(err)
	}
	seenLow := map[uint32]bool{}
	for _, row := range dropsLow {
		seenLow[sessLow.Objects[row.Object].Award.Template] = true
	}
	if !seenLow[idDiff0] || !seenLow[idNormalAbyss] {
		t.Fatalf("expected diff0 (%d) and normal abyss (%d), got %v", idDiff0, idNormalAbyss, seenLow)
	}
	if seenLow[idDiff4] || seenLow[idLv100Abyss] {
		t.Fatalf("unexpected diff4 or lv100 drop in low run: %v", seenLow)
	}

	// Run 2: Difficulty 4 (噩梦) in Lv.100 dungeon -> gets idDiff4 + idNormalAbyss + idLv100Abyss, and pickup works!
	run100 := &dungeon.Session{
		RunID:      "run-100",
		Difficulty: 4,
		Definition: catalog.DungeonDefinition{BasisLevel: 100, MinimumLevel: 94},
		Room:       catalog.DungeonRoom{Map: 100004195, Boss: true},
		Loaded:     true,
		Dead:       map[uint16]bool{9: true},
		Monsters:   []protocol.DungeonMonster{{Entity: 9, Level: 100, Rank: 3}},
		NextEntity: 10,
	}
	sess100 := NewSession(c, tables, rules, gear, run100.RunID, acct, role.ID, role.WireID)
	sess100.GlobalDrop = loaded
	sess100.seeds[100004195] = 777
	drops100, err := sess100.Death(run100, 9)
	if err != nil {
		t.Fatal(err)
	}
	var lv100Obj uint32
	seen100 := map[uint32]bool{}
	for _, row := range drops100 {
		tpl := sess100.Objects[row.Object].Award.Template
		seen100[tpl] = true
		if tpl == idLv100Abyss {
			lv100Obj = row.Object
		}
	}
	if !seen100[idDiff4] || !seen100[idNormalAbyss] || !seen100[idLv100Abyss] {
		t.Fatalf("expected diff4 (%d), normal abyss (%d), lv100 abyss (%d); got %v", idDiff4, idNormalAbyss, idLv100Abyss, seen100)
	}
	svc := Service{Store: s, Catalog: c, Rules: rules, BagRules: bagRules, Equipment: gear}
	saved, _, applied, err := svc.Pickup(ctx, role, sess100, run100, protocol.PickupRequest{Object: lv100Obj})
	if err != nil || !applied {
		t.Fatal("pickup failed:", applied, err)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil || len(bag.Equipment) != 1 || bag.Equipment[0].Template != idLv100Abyss {
		t.Fatalf("unexpected bag after lv100 abyss pickup: %+v err=%v", bag, err)
	}
}
