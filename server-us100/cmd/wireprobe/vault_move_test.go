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
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

func encodeMove19(srcList byte, srcSlot uint16, srcItem, count uint32, dstList byte, dstSlot uint16, dstItem uint32) []byte {
	p := make([]byte, 32)
	p[0] = srcList
	binary.LittleEndian.PutUint16(p[1:3], srcSlot)
	binary.LittleEndian.PutUint32(p[3:7], srcItem)
	binary.LittleEndian.PutUint32(p[7:11], count)
	p[11] = dstList
	binary.LittleEndian.PutUint16(p[12:14], dstSlot)
	binary.LittleEndian.PutUint32(p[14:18], dstItem)
	binary.LittleEndian.PutUint32(p[22:26], 0xffffffff)
	return p
}

func TestVaultMoveLiveCaptureSafe1Safe2AccountVaultShareAndSort(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "vault_move.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	account, err := store.DevelopmentAccount(ctx, "vault-move-test")
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
	ver := lootCat.Source.Checksum

	// Character 1 holds equipment 100180873 (+12, amplify) in slot 10 (exact live capture),
	// and stackable 10327281 x500 in slot 121.
	role1, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "VaultChar1",
		Profession:    0,
		ConfigVersion: ver,
		State: json.RawMessage(`{
			"level": 100,
			"inventory": {
				"version": "ordinary-bag-v1",
				"gold": 50000,
				"items": [{"slot": 121, "Template": 10327281, "Amount": 500}],
				"equipment": [{"slot": 10, "template": 100180873, "durability": 35, "upgrade": 12, "amplify_type": 1, "amplify_value": 45}]
			}
		}`),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	role2, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     account,
		Name:          "VaultChar2",
		Profession:    1,
		ConfigVersion: ver,
		State:         json.RawMessage(`{"level":100,"inventory":{"version":"ordinary-bag-v1","gold":0,"items":[]}}`),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}

	vaultSvc := &inventory.VaultService{Store: store, Rules: vaultRules}
	// Upgrade Safe I to 264, unlock Safe II (24), and open Account Vault (16).
	if _, err = vaultSvc.UpgradePersonal(ctx, role1, 0, 264); err != nil {
		t.Fatal(err)
	}
	if _, err = vaultSvc.UpgradePersonal(ctx, role1, 1, 24); err != nil {
		t.Fatal(err)
	}
	if _, err = vaultSvc.UpgradeAccount(ctx, account, 16); err != nil {
		t.Fatal(err)
	}

	wearSvc := &inventory.WearService{Store: store, BagRules: bagRules}
	w1 := &worldSession{
		account:    account,
		role:       role1,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
		vaults:     vaultSvc,
	}
	var eqSess equipmentSession

	// 1) Replay exact live-captured CMD 19 payload: Space 0 slot 10 (100180873) -> Space 0x02 slot 44.
	liveHex := "000a0089a3f80501000000022c000000000000000000ffffffff000000000000"
	livePlain, err := hex.DecodeString(liveHex)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := eqSess.handle(wearSvc, w1, livePlain, append([]byte("live19-1"), livePlain...))
	if err != nil {
		t.Fatalf("live vault deposit failed: %v", err)
	}
	if len(plan) != 3 || plan[0].Kind != 1 || plan[0].ID != 19 || plan[1].ID != 14 || plan[2].ID != 14 {
		t.Fatalf("expected ACK19 + 2 NOTI14 packets, got %+v", plan)
	}
	if plan[1].Payload[0] != 0 || plan[2].Payload[0] != protocol.PersonalVaultSpace {
		t.Fatalf("unexpected NOTI14 spaces: src=%d dst=%d", plan[1].Payload[0], plan[2].Payload[0])
	}
	// Verify equipment removed from Bag and restored in Safe I on reconnect with +12 & amplify intact.
	b1, _ := inventory.ReadBag(w1.role.State)
	if len(b1.Equipment) != 0 {
		t.Fatalf("expected equipment removed from Bag, got %+v", b1.Equipment)
	}
	v1Restore, _, _, err := vaultSvc.BootstrapAll(ctx, w1.role)
	if err != nil {
		t.Fatalf("BootstrapAll after Safe I deposit failed: %v", err)
	}
	if binary.LittleEndian.Uint16(v1Restore[3:5]) != 1 {
		t.Fatalf("expected 1 item in Safe I NOTI13 restore, got count=%d", binary.LittleEndian.Uint16(v1Restore[3:5]))
	}
	rowOff := 5
	if gotSlot := binary.LittleEndian.Uint16(v1Restore[rowOff : rowOff+2]); gotSlot != 44 {
		t.Fatalf("expected Safe I slot 44, got %d", gotSlot)
	}
	if gotTpl := binary.LittleEndian.Uint32(v1Restore[rowOff+2 : rowOff+6]); gotTpl != 100180873 {
		t.Fatalf("expected Safe I template 100180873, got %d", gotTpl)
	}
	if v1Restore[rowOff+10] != 12 || v1Restore[rowOff+19] != 1 || binary.LittleEndian.Uint16(v1Restore[rowOff+20:rowOff+22]) != 45 {
		t.Fatalf("expected upgrade=12 ampType=1 ampVal=45 preserved, got upg=%d amp=%d/%d",
			v1Restore[rowOff+10], v1Restore[rowOff+19], binary.LittleEndian.Uint16(v1Restore[rowOff+20:rowOff+22]))
	}

	// 2) Stackable partial split (200 of 500) Bag slot 121 -> Safe I slot 0, then merge +100.
	splitP := encodeMove19(0, 121, 10327281, 200, protocol.PersonalVaultSpace, 0, 0)
	if _, err = eqSess.handle(wearSvc, w1, splitP, append([]byte("split-200"), splitP...)); err != nil {
		t.Fatalf("split stack to Safe I failed: %v", err)
	}
	mergeP := encodeMove19(0, 121, 10327281, 100, protocol.PersonalVaultSpace, 0, 10327281)
	if _, err = eqSess.handle(wearSvc, w1, mergeP, append([]byte("merge-100"), mergeP...)); err != nil {
		t.Fatalf("merge stack to Safe I failed: %v", err)
	}
	b1, _ = inventory.ReadBag(w1.role.State)
	if len(b1.Items) != 1 || b1.Items[0].Amount != 200 {
		t.Fatalf("expected 200 remaining in Bag, got %+v", b1.Items)
	}

	// 3) Move equipment from Safe I (slot 44) -> Safe II (slot 3) -> Account Vault (slot 5).
	toSafe2 := encodeMove19(protocol.PersonalVaultSpace, 44, 100180873, 1, protocol.PersonalVault2Space, 3, 0)
	if _, err = eqSess.handle(wearSvc, w1, toSafe2, append([]byte("safe1-safe2"), toSafe2...)); err != nil {
		t.Fatalf("Safe I -> Safe II move failed: %v", err)
	}
	toAcc := encodeMove19(protocol.PersonalVault2Space, 3, 100180873, 1, protocol.AccountVaultSpace, 5, 0)
	if _, err = eqSess.handle(wearSvc, w1, toAcc, append([]byte("safe2-acc"), toAcc...)); err != nil {
		t.Fatalf("Safe II -> Account Vault move failed: %v", err)
	}

	// 4) Upgrade Account Vault while non-empty must preserve the stored item in NOTI 13.
	upPkts, err := applyVaultUpgrade(ctx, vaultSvc, account, w1.role, vaultUpgradeAccount, 24, true)
	if err != nil || len(upPkts) != 1 || binary.LittleEndian.Uint16(upPkts[0].Payload[1:3]) != 24 || binary.LittleEndian.Uint16(upPkts[0].Payload[7:9]) != 1 {
		t.Fatalf("expected Account Vault upgrade to 24 slots preserving 1 item, got err=%v pkts=%+v", err, upPkts)
	}

	// 5) Character 2 on same account sees the item in Account Vault and withdraws it into Bag slot 9.
	_, _, accRestore2, err := vaultSvc.BootstrapAll(ctx, role2)
	if err != nil || binary.LittleEndian.Uint16(accRestore2[7:9]) != 1 {
		t.Fatalf("expected Character 2 to see 1 shared item in Account Vault, err=%v", err)
	}
	w2 := &worldSession{
		account:    account,
		role:       role2,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: lootCat, BagRules: bagRules},
		vaults:     vaultSvc,
	}
	withdrawP := encodeMove19(protocol.AccountVaultSpace, 5, 100180873, 1, 0, 9, 0)
	if _, err = eqSess.handle(wearSvc, w2, withdrawP, append([]byte("acc-withdraw"), withdrawP...)); err != nil {
		t.Fatalf("Character 2 withdraw from Account Vault failed: %v", err)
	}
	b2, _ := inventory.ReadBag(w2.role.State)
	if len(b2.Equipment) != 1 || b2.Equipment[0].Slot != 9 || b2.Equipment[0].Template != 100180873 || b2.Equipment[0].Upgrade != 12 {
		t.Fatalf("expected Character 2 Bag slot 9 to receive +12 100180873, got %+v", b2.Equipment)
	}

	// 6) CMD 20 Sort inside Safe I: move slot 0 -> slot 2.
	sortPlain := make([]byte, 8)
	sortPlain[0] = protocol.PersonalVaultSpace
	sortPlain[1] = 0
	binary.LittleEndian.PutUint16(sortPlain[2:4], 1) // 1 pair
	binary.LittleEndian.PutUint16(sortPlain[4:6], 0) // from 0
	binary.LittleEndian.PutUint16(sortPlain[6:8], 2) // to 2
	_, sortPlan, space, err := sortInventoryWithVaults(ctx, store, vaultSvc, account, w1.role, []byte("sort-safe1"), sortPlain)
	if err != nil || space != protocol.PersonalVaultSpace {
		t.Fatalf("sort Safe I failed: space=%d err=%v", space, err)
	}
	if len(sortPlan) < 2 || sortPlan[0].ID != 13 || binary.LittleEndian.Uint16(sortPlan[0].Payload[5:7]) != 2 {
		t.Fatalf("expected Safe I sort restore with item moved to slot 2, got %+v", sortPlan)
	}
}
