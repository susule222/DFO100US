package admin

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLauncherQueueCommitsOnce(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	s, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(temp, "isolation.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := s.DevelopmentAccount(ctx, "queue-test")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join("..", "..", "configs", "us100")
	items, err := catalog.LoadLoot(filepath.Join(config, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog(filepath.Join(config, "equipment.generated.json"), items.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := inventory.LoadBagRules(filepath.Join(config, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := s.CreateCharacter(ctx, storage.Character{AccountID: account, Name: "fixture", ConfigVersion: items.Source.Checksum, State: json.RawMessage("{}")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: s, Awarder: &inventory.Awarder{Catalog: items, Equipment: gear, Rules: rules}}
	name := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json"
	pending := filepath.Join(temp, "pending")
	if err = os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(storage.Grant{Character: role.ID, Reason: "fixture", Items: []storage.GrantItem{{Template: 10018, Amount: 1}}})
	for i := 0; i < 2; i++ {
		if err = os.WriteFile(filepath.Join(pending, name), req, 0600); err != nil {
			t.Fatal(err)
		}
		if err = Drain(temp, svc, account); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(temp, "receipts", name))
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Receipt Receipt
			Error   string
		}
		if err = json.Unmarshal(b, &r); err != nil || r.Error != "" || len(r.Receipt.Items) != 1 {
			t.Fatalf("receipt %s: %v", b, err)
		}
	}
	chars, err := s.Characters(ctx, account)
	if err != nil || len(chars) != 1 {
		t.Fatal(err)
	}
	bag, err := inventory.ReadBag(chars[0].State)
	if err != nil || len(bag.Equipment) != 1 || bag.Equipment[0].Template != 10018 {
		t.Fatalf("duplicate grant: %+v %v", bag, err)
	}
}
