package admin

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestActivityContractMileageAndClaimOps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	acctID, err := store.DevelopmentAccount(ctx, "probe-us100")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"experience":0}`), inventory.Bag{Version: "ordinary-bag-v1", Gold: 1000})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "Tester",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}

	bagRules := inventory.BagRules{
		Source:         checksum,
		EquipmentSlots: [2]uint16{9, 64},
		Slots: map[string][2]uint16{
			"[throw]":    {65, 120},
			"[material]": {121, 176},
		},
		MissingStackLimit: 1000,
	}
	lootCat := catalog.LootCatalog{
		Items: map[uint32]catalog.LootItem{
			10327281: {ID: 10327281, Kind: "stackable", StackableType: "[material]", StackLimit: 1000},
		},
	}
	lootCat.Source.Checksum = checksum
	svc := &Service{
		Store:    store,
		Awarder:  &inventory.Awarder{Catalog: lootCat, Rules: bagRules},
		Operator: "test",
	}

	// 1. Activate all 8 contracts for 7 days
	rec, applied, err := svc.ApplyOp(ctx, CharacterOp{
		ID:           "op-contract-all",
		AccountID:    acctID,
		CharacterID:  role.ID,
		Action:       "contract",
		ContractKind: 255,
		ContractDays: 7,
		Reason:       "test all contracts",
	})
	if err != nil || !applied || rec.Affected != 8 {
		t.Fatalf("contract all op failed: rec=%+v applied=%v err=%v", rec, applied, err)
	}
	rows, err := store.NeoPremiumSeconds(acctID)
	if err != nil || len(rows) != 8 {
		t.Fatalf("expected 8 active contracts, got %d, err=%v", len(rows), err)
	}

	// 2. Credit CERA and Mileage via currency op and verify neither clobbers the other
	if _, _, err := svc.ApplyOp(ctx, CharacterOp{
		ID:          "op-cera-1",
		AccountID:   acctID,
		CharacterID: role.ID,
		Action:      "currency",
		CeraMode:    "set",
		Cera:        5000,
		MileageMode: "set",
		Mileage:     120,
		Reason:      "seed balances",
	}); err != nil {
		t.Fatalf("currency set failed: %v", err)
	}
	if _, _, err := svc.ApplyOp(ctx, CharacterOp{
		ID:          "op-cera-2",
		AccountID:   acctID,
		CharacterID: role.ID,
		Action:      "currency",
		CeraMode:    "add",
		Cera:        500,
		Reason:      "add cera preserves mileage",
	}); err != nil {
		t.Fatalf("currency add failed: %v", err)
	}
	cera, mileage, err := store.AccountBalances(ctx, acctID)
	if err != nil || cera != 5500 || mileage != 120 {
		t.Fatalf("balances = (%d, %d), want (5500, 120), err=%v", cera, mileage, err)
	}

	// 3. Mileage redeem (supports PVF stackable fallback item 590704678)
	redeemRec, applied, err := svc.ApplyOp(ctx, CharacterOp{
		ID:          "op-redeem-1",
		AccountID:   acctID,
		CharacterID: role.ID,
		Action:      "mileage_redeem",
		MileageCost: 50,
		RewardItems: []storage.GrantItem{{Template: 590704678, Amount: 2}},
		Reason:      "mileage shop redeem",
	})
	if err != nil || !applied || redeemRec.MileageBalance != 70 {
		t.Fatalf("mileage redeem failed: rec=%+v applied=%v err=%v", redeemRec, applied, err)
	}

	// 4. Activity claim (daily check-in) succeeds once and blocks duplicate claim
	claimRec, applied, err := svc.ApplyOp(ctx, CharacterOp{
		ID:            "op-claim-1",
		AccountID:     acctID,
		CharacterID:   role.ID,
		Action:        "activity_claim",
		ActivityKey:   "daily_checkin",
		ClaimDate:     "2026-11-27",
		RewardGold:    100000,
		RewardMileage: 15,
		RewardItems:   []storage.GrantItem{{Template: 10327281, Amount: 10}},
		Reason:        "daily checkin",
	})
	if err != nil || !applied || claimRec.MileageBalance != 85 || claimRec.GoldAfter != 101000 {
		t.Fatalf("activity claim failed: rec=%+v applied=%v err=%v", claimRec, applied, err)
	}
	if _, _, err := svc.ApplyOp(ctx, CharacterOp{
		ID:          "op-claim-dup",
		AccountID:   acctID,
		CharacterID: role.ID,
		Action:      "activity_claim",
		ActivityKey: "daily_checkin",
		ClaimDate:   "2026-11-27",
		RewardGold:  100000,
		Reason:      "duplicate checkin",
	}); err == nil {
		t.Fatalf("expected duplicate daily_checkin claim to be rejected")
	}
}
