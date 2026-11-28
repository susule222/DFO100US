package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestVaultUpgradeShopPurchaseSafe1Safe2AndAccountVault(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "vault.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.MigrateGrants(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "vault-upgrade-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApplyGrant(ctx, storage.Grant{ID: "fund-vault-test", AccountID: account, Cera: 50000, Reason: "test", Operator: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join("..", "..", "configs")
	vaultRules, err := inventory.LoadVaultRules(filepath.Join(cfg, "vault.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot(filepath.Join(cfg, "us100", "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules(filepath.Join(cfg, "us100", "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "VaultTester",
		Profession:    0,
		ConfigVersion: lootCat.Source.Checksum,
		State:         json.RawMessage(`{"level":100,"inventory":{"version":"ordinary-bag-v1","gold":1000000,"items":[]}}`),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	vaultSvc := &inventory.VaultService{Store: store, Rules: vaultRules}

	// 1. Purchase Safe I upgrade product 3000129 (template 50, exact live capture product).
	line1, ok := cartListing(3000129)
	if !ok || line1.Template != 50 {
		t.Fatalf("expected product 3000129 -> template 50 in cart listings, got ok=%v line=%+v", ok, line1)
	}
	_, applied, err := store.CompletePremiumCart(ctx, account, role.ID, "buy-safe1-tier1", []storage.CartLine{line1}, premiumDelivery(lootCat, bagRules))
	if err != nil || !applied {
		t.Fatalf("CompletePremiumCart(3000129) failed: err=%v applied=%v", err, applied)
	}
	latest, err := store.LiveCharacter(account, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	bag, err := inventory.ReadBag(latest.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Items) != 0 {
		t.Fatalf("safe upgrade kit 50 must apply immediately without cluttering bag, got %+v", bag.Items)
	}
	vkind, vtarget, ok := vaultUpgradeTarget(line1.Template)
	if !ok || vkind != vaultUpgradeSafe1 || vtarget != 24 {
		t.Fatalf("unexpected vaultUpgradeTarget(50): ok=%v kind=%d target=%d", ok, vkind, vtarget)
	}
	pkts, err := applyVaultUpgrade(ctx, vaultSvc, account, latest, vkind, vtarget, applied)
	if err != nil || len(pkts) != 1 {
		t.Fatalf("applyVaultUpgrade Safe I failed: err=%v pkts=%+v", err, pkts)
	}
	if pkts[0].Kind != 0 || pkts[0].ID != 66 || len(pkts[0].Payload) != 4 ||
		binary.LittleEndian.Uint16(pkts[0].Payload[0:2]) != 1 ||
		binary.LittleEndian.Uint16(pkts[0].Payload[2:4]) != 24 {
		t.Fatalf("expected NOTI 66 subType=1 slots=24, got %+v hex=%x", pkts[0], pkts[0].Payload)
	}

	// 2. Purchase Safe I tier 2 (3000130 -> 40 slots) and verify sequential upgrade.
	line2, ok := cartListing(3000130)
	if !ok || line2.Template != 57 {
		t.Fatalf("expected product 3000130 -> template 57, got %+v", line2)
	}
	vkind2, vtarget2, _ := vaultUpgradeTarget(line2.Template)
	pkts2, err := applyVaultUpgrade(ctx, vaultSvc, account, latest, vkind2, vtarget2, true)
	if err != nil || len(pkts2) != 1 || binary.LittleEndian.Uint16(pkts2[0].Payload[2:4]) != 40 {
		t.Fatalf("expected Safe I upgraded to 40 slots, got err=%v pkts=%+v", err, pkts2)
	}

	// 3. Safe II upgrade (product 3001202 -> template 10333160 -> 24 slots, NOTI 66 subType 22).
	lineS2, ok := cartListing(3001202)
	if !ok || lineS2.Template != 10333160 {
		t.Fatalf("expected product 3001202 -> template 10333160, got %+v", lineS2)
	}
	vkS2, vtS2, ok := vaultUpgradeTarget(lineS2.Template)
	if !ok || vkS2 != vaultUpgradeSafe2 || vtS2 != 24 {
		t.Fatalf("unexpected vaultUpgradeTarget(10333160): ok=%v kind=%d target=%d", ok, vkS2, vtS2)
	}
	pktsS2, err := applyVaultUpgrade(ctx, vaultSvc, account, latest, vkS2, vtS2, true)
	if err != nil || len(pktsS2) != 1 || pktsS2[0].ID != 66 ||
		binary.LittleEndian.Uint16(pktsS2[0].Payload[0:2]) != 22 ||
		binary.LittleEndian.Uint16(pktsS2[0].Payload[2:4]) != 24 {
		t.Fatalf("expected NOTI 66 subType=22 slots=24, got err=%v pkts=%+v", err, pktsS2)
	}

	// 4. Account Vault open (8) + upgrade (16 -> space 0x0c).
	av1, err := vaultSvc.UpgradeAccount(ctx, account, 0)
	if err != nil || av1.Slots != 8 {
		t.Fatalf("expected account vault open to 8 slots, got err=%v av=%+v", err, av1)
	}
	vkAcc, vtAcc, ok := vaultUpgradeTarget(10000660)
	if !ok || vkAcc != vaultUpgradeAccount || vtAcc != 16 {
		t.Fatalf("unexpected vaultUpgradeTarget(10000660): ok=%v kind=%d target=%d", ok, vkAcc, vtAcc)
	}
	pktsAcc, err := applyVaultUpgrade(ctx, vaultSvc, account, latest, vkAcc, vtAcc, true)
	if err != nil || len(pktsAcc) != 1 || pktsAcc[0].ID != 13 || pktsAcc[0].Payload[0] != protocol.AccountVaultSpace ||
		binary.LittleEndian.Uint16(pktsAcc[0].Payload[1:3]) != 16 {
		t.Fatalf("expected NOTI 13 space 0x0c slots=16, got err=%v pkts=%+v", err, pktsAcc)
	}

	// 5. Reconnect BootstrapAll must restore Safe I=40, Safe II=24, Account Vault=16.
	v1Payload, v2Payload, accPayload, err := vaultSvc.BootstrapAll(ctx, latest)
	if err != nil {
		t.Fatalf("BootstrapAll failed after upgrades: %v", err)
	}
	if len(v1Payload) != 5 || v1Payload[0] != protocol.PersonalVaultSpace || binary.LittleEndian.Uint16(v1Payload[1:3]) != 40 {
		t.Fatalf("unexpected Safe I restore: %x", v1Payload)
	}
	if len(v2Payload) != 5 || v2Payload[0] != protocol.PersonalVault2Space || binary.LittleEndian.Uint16(v2Payload[1:3]) != 24 {
		t.Fatalf("unexpected Safe II restore: %x", v2Payload)
	}
	if len(accPayload) != 9 || accPayload[0] != protocol.AccountVaultSpace || binary.LittleEndian.Uint16(accPayload[1:3]) != 16 {
		t.Fatalf("unexpected Account Vault restore: %x", accPayload)
	}
}

func TestUseBaggedOneShotVaultKitViaCMD507(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "vault507.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "vault-507-test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join("..", "..", "configs")
	vaultRules, err := inventory.LoadVaultRules(filepath.Join(cfg, "vault.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot(filepath.Join(cfg, "us100", "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules(filepath.Join(cfg, "us100", "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "OneShotVault",
		Profession:    0,
		ConfigVersion: lootCat.Source.Checksum,
		State:         json.RawMessage(`{"level":100,"inventory":{"version":"ordinary-bag-v1","items":[{"slot":80,"Template":590701759,"Amount":1}]}}`),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	vaultSvc := &inventory.VaultService{Store: store, Rules: vaultRules}
	w := &worldSession{
		account:    account,
		role:       role,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
		vaults:     vaultSvc,
	}
	// CMD 507: [0..1]=slot, [2]=list, [3..6]=arg0, [7..10]=actionType
	plain := make([]byte, 16)
	binary.LittleEndian.PutUint16(plain[0:2], 80)
	plain[2] = 0
	plan, err := w.useStackableAction(plain, append([]byte("oneshot-507"), plain...))
	if err != nil {
		t.Fatalf("useStackableAction(oneshotvault) failed: %v", err)
	}
	var gotMax bool
	for _, pkt := range plan {
		if pkt.Kind == 0 && pkt.ID == 66 && len(pkt.Payload) == 4 &&
			binary.LittleEndian.Uint16(pkt.Payload[0:2]) == 1 &&
			binary.LittleEndian.Uint16(pkt.Payload[2:4]) == inventory.MaxPersonalVaultSlots {
			gotMax = true
		}
	}
	if !gotMax {
		t.Fatalf("expected NOTI 66 subType=1 max slots (%d), got %+v", inventory.MaxPersonalVaultSlots, plan)
	}
}
