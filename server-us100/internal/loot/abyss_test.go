package loot

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestLauncherAbyssScope(t *testing.T) {
	for _, test := range []struct {
		room          uint32
		percent, want int
	}{{3, 100, 1}, {4, 100, 0}, {3, 0, 0}} {
		out := Outcome{NextSeed: 123, Awards: []Award{{12, 1}}}
		r := AbyssRule{Maps: []uint32{3, 3}, Template: 456, Percent: test.percent}
		r.bonus(test.room, &out)
		if len(out.Awards) != 1+test.want || out.Awards[0].Template != 12 {
			t.Fatal(out)
		}
		if test.want == 0 && out.NextSeed != 123 {
			t.Fatal("normal RNG changed")
		}
		if test.want == 1 && out.Awards[1] != (Award{456, 1}) {
			t.Fatal(out)
		}
	}
}

func TestAbyssExtraGearPickup(t *testing.T) {
	config := filepath.Join("..", "..", "configs", "us100")
	ctx := context.Background()
	temp := t.TempDir()
	s, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(temp, "fixture.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	acct, err := s.DevelopmentAccount(ctx, "abyss-test")
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
	role, err := s.CreateCharacter(ctx, storage.Character{AccountID: acct, Name: "test", ConfigVersion: c.Source.Checksum, State: []byte("{}")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	run := &dungeon.Session{RunID: "fixture-run", Room: catalog.DungeonRoom{Map: 58548}, Loaded: true, Dead: map[uint16]bool{7: true},
		Monsters: []protocol.DungeonMonster{{Entity: 7, Level: 3}}, NextEntity: 8}
	session := NewSession(c, tables, rules, gear, run.RunID, acct, role.ID, role.WireID)
	policy := filepath.Join(temp, "abyss.json")
	if err = os.WriteFile(policy, []byte(`{"maps":[58548],"template":10018,"percent":100}`), 0600); err != nil {
		t.Fatal(err)
	}
	session.Abyss, err = LoadAbyss(policy, gear)
	if err != nil {
		t.Fatal(err)
	}
	session.seeds[58548] = 125
	drops, err := session.Death(run, 7)
	if err != nil || len(drops) == 0 {
		t.Fatal(drops, err)
	}
	retry, err := session.Death(run, 7)
	if err != nil || len(retry) != len(drops) {
		t.Fatal("death retry", err)
	}
	object := drops[len(drops)-1].Object
	if retry[len(retry)-1].Object != object || session.Objects[object].Award.Template != 10018 {
		t.Fatal("missing bonus")
	}
	service := Service{Store: s, Catalog: c, Rules: rules, BagRules: bagRules, Equipment: gear}
	saved, _, applied, err := service.Pickup(ctx, role, session, run, protocol.PickupRequest{Object: object})
	if err != nil || !applied {
		t.Fatal(applied, err)
	}
	bag, err := inventory.ReadBag(saved.State)
	if err != nil || len(bag.Equipment) != 1 || bag.Equipment[0].Template != 10018 {
		t.Fatal(bag, err)
	}
}
