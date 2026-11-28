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
	"time"
)

func makeNPCBuyCMD(item, qty, shopID, npcID uint32, tab byte) []byte {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[0:4], item)
	binary.LittleEndian.PutUint32(b[4:8], qty)
	binary.LittleEndian.PutUint32(b[8:12], shopID)
	binary.LittleEndian.PutUint32(b[12:16], npcID)
	b[16] = tab
	return b
}

func makeNPCLimitsCMD(npcID uint32) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b[0:4], npcID)
	return b
}

func TestNPCShopHimanStellaAndCoinShop(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "npc_shop.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	acctID, err := store.DevelopmentAccount(ctx, "shop-tester")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	bag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    50000,
		Items: []inventory.BagItem{
			{Slot: 121, Template: 50052385, Amount: 500},  // 500 Music Notes
			{Slot: 122, Template: 590005890, Amount: 2},   // 2 Neo Webshop Coins
		},
	}
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"experience":0}`), bag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "ShopBuyer",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}

	rules, err := inventory.LoadBagRules(filepath.Join("..", "..", "configs", "us100", "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	lootCat := catalog.LootCatalog{Items: map[uint32]catalog.LootItem{}}
	lootCat.Source.Checksum = checksum

	w := &worldSession{
		account:    acctID,
		role:       role,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Catalog: lootCat, BagRules: rules},
	}

	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	// 1. Query limits on Himan Stella (NPC 100000827) -> empty 4 lists
	limPkts, npcID, err := w.handleNPCShopLimits(makeNPCLimitsCMD(100000827), now)
	if err != nil || npcID != 100000827 || len(limPkts) != 1 || limPkts[0].ID != protocol.CmdNPCShopLimits {
		t.Fatalf("handleNPCShopLimits failed: pkts=%+v npc=%d err=%v", limPkts, npcID, err)
	}
	if len(limPkts[0].Payload) != 17 || limPkts[0].Payload[0] != 1 {
		t.Fatalf("unexpected empty ACK 707 payload: %x", limPkts[0].Payload)
	}

	// 2. Buy Gold Music Note Box (1,500) [50052389] from Himan Stella (shop 100000399):
	// costs 300 Music Notes (50052385), account weekly limit 1.
	buyPkts, code, err := w.handleNPCShopBuy(ctx, nil, []byte("raw-buy-50052389-1"), makeNPCBuyCMD(50052389, 1, 100000399, 100000827, 0), now)
	if err != nil || code != 0 {
		t.Fatalf("buy 50052389 failed: code=%d err=%v", code, err)
	}
	if len(buyPkts) != 3 || buyPkts[0].ID != protocol.CmdNPCShopBuy || buyPkts[1].ID != 14 || buyPkts[2].ID != protocol.CmdNPCShopLimits {
		t.Fatalf("unexpected buy packets: %+v", buyPkts)
	}
	// Verify remaining Music Notes = 200 and bought box 50052389 is in bag.
	afterBag, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if got := countMaterialInBag(afterBag, 50052385); got != 200 {
		t.Fatalf("Music Notes remaining=%d, want 200", got)
	}
	if got := countMaterialInBag(afterBag, 50052389); got != 1 {
		t.Fatalf("bought box 50052389 count=%d, want 1", got)
	}
	// Verify ACK 21 reports consumed material 50052385 -> remaining 200.
	ack21 := buyPkts[0].Payload
	matOff := 1 + 16 + 0xb1
	if ack21[matOff] != 1 || binary.LittleEndian.Uint32(ack21[matOff+1:matOff+5]) != 50052385 || binary.LittleEndian.Uint32(ack21[matOff+5:matOff+9]) != 200 {
		t.Fatalf("unexpected ACK 21 material update: %x", ack21[matOff:])
	}

	// 3. Re-buying 50052389 in the same week hits purchase limit (code 14).
	_, code2, err2 := w.handleNPCShopBuy(ctx, nil, []byte("raw-buy-50052389-2"), makeNPCBuyCMD(50052389, 1, 100000399, 100000827, 0), now)
	if err2 == nil || code2 != 14 {
		t.Fatalf("expected purchase limit refusal (code 14), got code=%d err=%v", code2, err2)
	}

	// 4. Buying 50052392 (costs 4000 Music Notes) with only 200 fails with code 4.
	_, code3, err3 := w.handleNPCShopBuy(ctx, nil, []byte("raw-buy-50052392"), makeNPCBuyCMD(50052392, 1, 100000399, 100000827, 0), now)
	if err3 == nil || code3 != 4 {
		t.Fatalf("expected insufficient material refusal (code 4), got code=%d err=%v", code3, err3)
	}

	// 5. Coin Shop (NPC 20673, shop 1529) variable-material item 590005974 (1x 590005792 or 590005890).
	buyCoinPkts, code4, err4 := w.handleNPCShopBuy(ctx, nil, []byte("raw-buy-590005974"), makeNPCBuyCMD(590005974, 1, 1529, 20673, 0), now)
	if err4 != nil || code4 != 0 || len(buyCoinPkts) < 2 {
		t.Fatalf("coin shop variable-material buy failed: code=%d err=%v", code4, err4)
	}
	afterBag2, _ := inventory.ReadBag(w.role.State)
	if got := countMaterialInBag(afterBag2, 590005890); got != 1 {
		t.Fatalf("Neo coin remaining=%d, want 1", got)
	}
	if got := countMaterialInBag(afterBag2, 590005974); got != 1 {
		t.Fatalf("bought coin shop box count=%d, want 1", got)
	}
}
