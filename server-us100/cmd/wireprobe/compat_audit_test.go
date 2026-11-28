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
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestConsumeBagMaterialDoesNotBurnKeyOnInsufficientFunds(t *testing.T) {
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	charsCat, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "audit-acct")
	if err != nil {
		t.Fatal(err)
	}
	charSvc, err := character.New(st, charsCat, character.Rules{MaxCharacters: 4, InitialLevel: 1})
	if err != nil {
		t.Fatal(err)
	}
	initState := json.RawMessage(fmt.Sprintf(`{"level":18,"attributes":{"[hp max]":500,"[mp max]":400},"inventory":{"version":"ordinary-bag-v1","gold":50000,"items":[{"slot":65,"Template":%d,"Amount":2}]}}`, ev4189AltCoinItem))
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "AuditCap",
		Profession:    0,
		ConfigVersion: bagRules.Source,
		State:         initState,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 7
	w := &worldSession{
		account:    accID,
		role:       role,
		characters: charSvc,
		loot:       &loot.Service{Store: st, Catalog: lootCat, BagRules: bagRules},
	}

	// Primary coin (590005793) is absent -> must return paid=false WITHOUT burning key.
	_, paid, err := w.consumeBagMaterial(ctx, "ev4189_coin:1", "us100-ev4189-coin-v1", ev4189CoinItem, 1)
	if err != nil || paid {
		t.Fatalf("primary coin absent: got paid=%v err=%v, want false/nil", paid, err)
	}
	// Fallback coin (590005794) using the SAME key must succeed!
	pkts, paid, err := w.consumeBagMaterial(ctx, "ev4189_coin:1", "us100-ev4189-coin-v1", ev4189AltCoinItem, 1)
	if err != nil || !paid || len(pkts) == 0 {
		t.Fatalf("fallback coin with same key failed: paid=%v pkts=%d err=%v", paid, len(pkts), err)
	}
	bag, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Items) != 1 || bag.Items[0].Template != ev4189AltCoinItem || bag.Items[0].Amount != 1 {
		t.Fatalf("unexpected bag after fallback coin spend: %+v", bag.Items)
	}
}

func TestOnlineGMSyncCoversAllInventorySpacesAndRemovals(t *testing.T) {
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "gm.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "gm-acct")
	if err != nil {
		t.Fatal(err)
	}
	prevState := json.RawMessage(`{"level":18,"attributes":{"[hp max]":500,"[mp max]":400},"inventory":{"version":"ordinary-bag-v1","gold":1000,"items":[{"slot":65,"Template":2660671,"Amount":5}]}}`)
	nextState := json.RawMessage(`{"level":18,"attributes":{"[hp max]":500,"[mp max]":400},"inventory":{"version":"ordinary-bag-v1","gold":1000,"items":[{"slot":66,"Template":590005793,"Amount":3}],"creatures":[{"slot":0,"template":500990710,"creature_id":1}],"insignias":[{"slot":0,"template":100350001}]}}`)
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "GMSyncChar",
		Profession:    0,
		ConfigVersion: bagRules.Source,
		State:         prevState,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 12
	w := &worldSession{
		account: accID,
		role:    role,
		loot:    &loot.Service{Store: st, Catalog: lootCat, BagRules: bagRules},
	}
	// Simulate GM writing updated state directly in DB.
	if _, _, err := st.CommitCharacterEvent(ctx, accID, role.ID, bagRules.Source, "gm-test-1", "test", func(storage.Character) (json.RawMessage, json.RawMessage, error) {
		return nextState, json.RawMessage(`{}`), nil
	}); err != nil {
		t.Fatal(err)
	}

	var sentNoti105, sentSpace0Clear65, sentSpace2A, sentSpace7, sentSpace25 bool
	send := func(kind byte, id uint16, payload []byte) error {
		if kind == 0 && id == protocol.NotiCreatureInfo {
			sentNoti105 = true
		}
		if kind == 0 && id == 14 && len(payload) >= 3 {
			switch payload[0] {
			case 0:
				cnt := int(binary.LittleEndian.Uint16(payload[1:3]))
				for i := 0; i < cnt; i++ {
					off := 3 + i*0xb1
					slot := binary.LittleEndian.Uint16(payload[off : off+2])
					tpl := binary.LittleEndian.Uint32(payload[off+2 : off+6])
					if slot == 65 && tpl == 0xffffffff {
						sentSpace0Clear65 = true
					}
				}
			case protocol.DanjiInventorySpace:
				sentSpace2A = true
			case protocol.CreatureSpace:
				sentSpace7 = true
			case protocol.InsigniaSpace:
				sentSpace25 = true
			}
		}
		return nil
	}
	if err := w.syncGrantedBag(accID, role.ID, send, func(map[string]any) {}); err != nil {
		t.Fatalf("syncGrantedBag failed: %v", err)
	}
	if !sentSpace0Clear65 || !sentSpace2A || !sentNoti105 || !sentSpace7 || !sentSpace25 {
		t.Fatalf("GM sync missing updates: clear65=%v space2a=%v noti105=%v space7=%v space25=%v",
			sentSpace0Clear65, sentSpace2A, sentNoti105, sentSpace7, sentSpace25)
	}
}

func TestGrantActivityRewardRoutesSpecializedSpacesAndSpace2A(t *testing.T) {
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "act.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lootCat, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	charsCat, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "act-acct")
	if err != nil {
		t.Fatal(err)
	}
	charSvc, err := character.New(st, charsCat, character.Rules{MaxCharacters: 4, InitialLevel: 1})
	if err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "ActRewardChar",
		Profession:    0,
		ConfigVersion: bagRules.Source,
		State:         json.RawMessage(`{"level":18,"attributes":{"[hp max]":500,"[mp max]":400},"inventory":{"version":"ordinary-bag-v1","gold":0,"items":[]}}`),
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 9
	w := &worldSession{
		account:    accID,
		role:       role,
		characters: charSvc,
		loot:       &loot.Service{Store: st, Catalog: lootCat, BagRules: bagRules},
	}

	// 1. Space 0x2a capsule coin reward
	pkts, err := w.grantActivityReward(ctx, "act-coin-1", "test-v1", 0, ev4189CoinItem, 2, 0, "2026-11-28", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got2A bool
	for _, p := range pkts {
		if p.ID == 14 && len(p.Payload) > 0 && p.Payload[0] == protocol.DanjiInventorySpace {
			got2A = true
		}
	}
	if !got2A {
		t.Fatalf("grantActivityReward did not sync Space 0x2a for capsule coin %d", ev4189CoinItem)
	}

	// 2. Creature reward must route to Space 7 + emit NOTI 105
	pkts, err = w.grantActivityReward(ctx, "act-cre-1", "test-v1", 0, 500990710, 1, 0, "2026-11-28", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got105, gotCre13 bool
	for _, p := range pkts {
		if p.ID == protocol.NotiCreatureInfo {
			got105 = true
		}
		if p.ID == 13 && len(p.Payload) > 0 && p.Payload[0] == protocol.CreatureSpace {
			gotCre13 = true
		}
	}
	if !got105 || !gotCre13 {
		t.Fatalf("grantActivityReward creature sync missing: noti105=%v space7=%v", got105, gotCre13)
	}
	bag, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bag.Creatures) != 1 || bag.Creatures[0].CreatureID == 0 {
		t.Fatalf("creature not persisted in Space 7 with CreatureID: %+v", bag.Creatures)
	}
	_ = time.Second
}
