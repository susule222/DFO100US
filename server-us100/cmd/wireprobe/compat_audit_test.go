package main

import (
	"context"
	"dfolan/internal/admin"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/progression"
	"dfolan/internal/quest"
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

func appendPBVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func buildCmd18Payload(space byte, slot uint16, template, count uint32) []byte {
	var item []byte
	item = appendPBVarint(append(item, 0x08), 1)                // delete_type=1
	item = appendPBVarint(append(item, 0x10), uint64(slot))     // slot
	item = appendPBVarint(append(item, 0x18), uint64(template)) // template
	item = appendPBVarint(append(item, 0x20), uint64(count))    // count

	var pb []byte
	pb = appendPBVarint(append(pb, 0x10), uint64(space)) // space
	pb = append(pb, 0x1a)
	pb = appendPBVarint(pb, uint64(len(item)))
	pb = append(pb, item...)
	pb = append(pb, 0x20, 0x00)

	out := make([]byte, 4+len(pb))
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(pb)))
	copy(out[4:], pb)
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	return out
}

func TestDeleteItemSupportsCreatureAndInsigniaSpacesAndSafetyLocks(t *testing.T) {
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "del_spaces.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "del-acct")
	if err != nil {
		t.Fatal(err)
	}
	initBag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Avatars: []inventory.BagEquipment{
			{Slot: 0, Template: 39165, ItemLock: 1}, // safety-locked avatar
		},
		Creatures: []inventory.BagEquipment{
			{Slot: 0, Template: 500990710, CreatureID: 1},
			{Slot: 1, Template: 500990711, CreatureID: 2, ItemLock: 1}, // safety-locked creature
		},
		CreatureItems: []inventory.BagItem{
			{Slot: inventory.CreatureItemSlotStart, Template: 24, Amount: 5},
		},
		Insignias: []inventory.BagEquipment{
			{Slot: 0, Template: 100380017},
		},
		Gems: []inventory.BagItem{
			{Slot: inventory.GemSlotStart, Template: 90000, Amount: 4},
		},
	}
	rawState, err := inventory.SaveBag(json.RawMessage(`{"level":100}`), initBag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "DelMultiSpace",
		Profession:    0,
		ConfigVersion: "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69",
		State:         rawState,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Locked Avatar in Space 1 must be refused with lock code 0x13.
	if _, _, _, code, err := deleteInventoryItems(ctx, st, accID, role, []byte("del-av-locked"), buildCmd18Payload(1, 0, 39165, 1)); err == nil || code != 0x13 {
		t.Fatalf("expected locked avatar refusal (0x13), got code=%#x err=%v", code, err)
	}
	// 2. Locked Creature in Space 7 must be refused with lock code 0x13.
	if _, _, _, code, err := deleteInventoryItems(ctx, st, accID, role, []byte("del-cr-locked"), buildCmd18Payload(protocol.CreatureSpace, 1, 500990711, 1)); err == nil || code != 0x13 {
		t.Fatalf("expected locked creature refusal (0x13), got code=%#x err=%v", code, err)
	}
	// 3. Deleting unlocked Creature in Space 7 slot 0 succeeds and emits ACK 18 + NOTI 105 + NOTI 13 Space 7.
	saved, plan, sp, code, err := deleteInventoryItems(ctx, st, accID, role, []byte("del-cr-0"), buildCmd18Payload(protocol.CreatureSpace, 0, 500990710, 1))
	if err != nil || sp != protocol.CreatureSpace || code != 0 {
		t.Fatalf("delete Space 7 creature failed: sp=%d code=%#x err=%v", sp, code, err)
	}
	var gotAck18, gotNoti105, gotRestore7 bool
	for _, p := range plan {
		if p.Kind == 1 && p.ID == protocol.CmdDeleteItem {
			gotAck18 = true
		}
		if p.Kind == 0 && p.ID == protocol.NotiCreatureInfo {
			gotNoti105 = true
		}
		if p.Kind == 0 && p.ID == 13 && len(p.Payload) > 0 && p.Payload[0] == protocol.CreatureSpace {
			gotRestore7 = true
		}
	}
	if !gotAck18 || !gotNoti105 || !gotRestore7 {
		t.Fatalf("delete Space 7 packets incomplete: ack18=%v noti105=%v restore7=%v", gotAck18, gotNoti105, gotRestore7)
	}
	// 4. Deleting partial Gem stack in Space 0x25 succeeds and emits NOTI 13 Space 0x25.
	saved, plan, sp, code, err = deleteInventoryItems(ctx, st, accID, saved, []byte("del-gem"), buildCmd18Payload(protocol.InsigniaSpace, inventory.GemSlotStart, 90000, 2))
	if err != nil || sp != protocol.InsigniaSpace || code != 0 {
		t.Fatalf("delete Space 0x25 gem failed: sp=%d code=%#x err=%v", sp, code, err)
	}
	bagAfter, err := inventory.ReadBag(saved.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bagAfter.Creatures) != 1 || bagAfter.Creatures[0].Slot != 1 {
		t.Fatalf("unexpected remaining creatures: %+v", bagAfter.Creatures)
	}
	if len(bagAfter.Gems) != 1 || bagAfter.Gems[0].Amount != 2 {
		t.Fatalf("unexpected remaining gems: %+v", bagAfter.Gems)
	}
}

func TestNPCShopBuyClearsEmptiedMaterialSlotInNoti14(t *testing.T) {
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "shop_clear.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "shop-clear-acct")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	bag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    50000,
		Items: []inventory.BagItem{
			{Slot: 121, Template: 10327281, Amount: 10},  // occupies first material slot so 122 stays empty
			{Slot: 122, Template: 50052385, Amount: 300}, // exact cost of 50052389 -> slot 122 becomes empty
		},
	}
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":100}`), bag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "ShopClearer",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	lootCat := catalog.LootCatalog{Items: map[uint32]catalog.LootItem{}}
	lootCat.Source.Checksum = checksum
	w := &worldSession{
		account:    accID,
		role:       role,
		characters: &character.Service{Store: st},
		loot:       &loot.Service{Catalog: lootCat, BagRules: rules},
	}
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	pkts, code, err := w.handleNPCShopBuy(ctx, nil, []byte("raw-buy-exact-300"), makeNPCBuyCMD(50052389, 1, 100000399, 100000827, 0), now)
	if err != nil || code != 0 {
		t.Fatalf("exact-material NPC buy failed: code=%d err=%v", code, err)
	}
	var cleared122 bool
	for _, p := range pkts {
		if p.Kind == 0 && p.ID == 14 && len(p.Payload) >= 3 && p.Payload[0] == 0 {
			cnt := int(binary.LittleEndian.Uint16(p.Payload[1:3]))
			for i := 0; i < cnt; i++ {
				off := 3 + i*0xb1
				slot := binary.LittleEndian.Uint16(p.Payload[off : off+2])
				tpl := binary.LittleEndian.Uint32(p.Payload[off+2 : off+6])
				if slot == 122 && tpl == 0xffffffff {
					cleared122 = true
				}
			}
		}
	}
	if !cleared122 {
		t.Fatalf("NPC shop buy did not emit EmptySlotItem(122) in Space 0 NOTI 14 when material hit 0")
	}
}

func TestAwakeningWireEncodingAndAdminEpicSkip(t *testing.T) {
	ctx := context.Background()
	dir := "../../configs/us100"
	qc, err := catalog.LoadQuests(filepath.Join(dir, "quests.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	chars, err := catalog.LoadCharacters(filepath.Join(dir, "characters.generated.json"))
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
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "awak_sync.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "awak-acct")
	if err != nil {
		t.Fatal(err)
	}
	charSvc, err := character.New(st, chars, character.Rules{MaxCharacters: 4, InitialLevel: 1})
	if err != nil {
		t.Fatal(err)
	}
	p0 := chars.Professions[0]
	initState, err := json.Marshal(character.State{
		Level:         75,
		Advancement:   1,
		Awakening:     0,
		SourceSHA256:  p0.RawSHA256,
		Attributes:    p0.InitialAttributes,
		InitialSkills: p0.InitialSkills,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "AwakSync",
		Profession:    0,
		ConfigVersion: qc.Source.Checksum,
		State:         initState,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 15

	// 1. Admin complete_epic_to_level(75) must advance Awakening >= 1 via [awakening type] epic rewards.
	adminSvc := &admin.Service{
		Store:       st,
		Characters:  charSvc,
		Progression: &character.ProgressionService{Store: st, Catalog: progCat, Professions: chars, Rules: expRules},
		Quests:      &quest.Service{Store: st, Catalog: qc, Professions: chars},
		Operator:    "audit-test",
	}
	rec, applied, err := adminSvc.ApplyOp(ctx, admin.CharacterOp{
		ID:          "op-skip-epic-75",
		AccountID:   accID,
		CharacterID: role.ID,
		Action:      "quest",
		Op:          "complete_epic_to_level",
		MaxLevel:    75,
	})
	if err != nil || !applied {
		t.Fatalf("complete_epic_to_level failed: applied=%v err=%v", applied, err)
	}
	if !rec.RelogHint {
		t.Fatalf("expected RelogHint=true when Awakening advanced during complete_epic_to_level")
	}
	updated, err := st.LiveCharacter(accID, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.WireID = role.WireID
	var after character.State
	if err := json.Unmarshal(updated.State, &after); err != nil {
		t.Fatal(err)
	}
	if after.Awakening < 1 {
		t.Fatalf("expected Awakening >= 1 after skipping Lv.75 epics, got %d", after.Awakening)
	}

	// 2. EntryBasicProbe packs grow_packed = (Advancement & 0x0f) | ((Awakening & 0x07) << 4).
	basic, err := charSvc.EntryBasicProbe(updated, [2]byte{1, 20})
	if err != nil {
		t.Fatal(err)
	}
	nameLen := int(binary.LittleEndian.Uint32(basic[134:138]))
	growPacked := basic[138+nameLen+1]
	wantGrowPacked := (after.Advancement & 0x0f) | ((after.Awakening & 0x07) << 4)
	if growPacked != wantGrowPacked {
		t.Fatalf("NOTI 2 mode-0 grow_packed = %#x, want %#x (adv=%d awak=%d)", growPacked, wantGrowPacked, after.Advancement, after.Awakening)
	}
}

func TestCreatureSkinSlot32WearAndTownAppearance(t *testing.T) {
	ctx := context.Background()
	dir := "../../configs/us100"
	chars, err := catalog.LoadCharacters(filepath.Join(dir, "characters.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog(filepath.Join(dir, "equipment.generated.json"), chars.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	bagRules, err := inventory.LoadBagRules(filepath.Join(dir, "rules.bag.json"))
	if err != nil {
		t.Fatal(err)
	}
	wearRules, err := inventory.LoadWearRules(filepath.Join(dir, "rules.wear.json"), chars.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	st, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "cre_skin32.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	accID, err := st.DevelopmentAccount(ctx, "skin32-acct")
	if err != nil {
		t.Fatal(err)
	}
	initBag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Creatures: []inventory.BagEquipment{
			{Slot: 0, Template: 500990710, CreatureID: 7},
		},
	}
	rawState, err := inventory.SaveBag(json.RawMessage(`{"level":100,"attributes":{"[hp max]":500,"[mp max]":400}}`), initBag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := st.CreateCharacter(ctx, storage.Character{
		AccountID:     accID,
		Name:          "Skin32User",
		Profession:    0,
		ConfigVersion: chars.Source.Checksum,
		State:         rawState,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	role.WireID = 22
	charSvc, err := character.New(st, chars, character.Rules{MaxCharacters: 4, InitialLevel: 1})
	if err != nil {
		t.Fatal(err)
	}
	wearSvc := &inventory.WearService{
		Store:       st,
		Catalog:     gear,
		Professions: chars,
		BagRules:    bagRules,
		Rules:       wearRules,
	}
	w := &worldSession{
		account:    accID,
		role:       role,
		characters: charSvc,
		channelCtx: [2]byte{1, 20},
	}
	// CMD 19: move creature 500990710 from Space 7 slot 0 -> Space 3 slot 32 (WornCreatureSkinSlot).
	movePayload := encodeCMD19(protocol.CreatureSpace, 0, 500990710, 3, inventory.WornCreatureSkinSlot, 0)

	var eqSess equipmentSession
	plan, err := eqSess.handle(wearSvc, w, movePayload, append([]byte{1, 19}, movePayload...))
	if err != nil {
		t.Fatalf("equipping creature skin slot 32 failed: %v", err)
	}
	var gotNoti105, gotMode0Skin bool
	for _, p := range plan {
		if p.Kind == 0 && p.ID == protocol.NotiCreatureInfo {
			gotNoti105 = true
		}
		if p.Kind == 0 && p.ID == 2 && len(p.Payload) > 5+0x51 && p.Payload[0] == 0 {
			if binary.LittleEndian.Uint32(p.Payload[5+0x4d:5+0x51]) == 500990710 {
				gotMode0Skin = true
			}
		}
	}
	if !gotNoti105 || !gotMode0Skin {
		t.Fatalf("slot 32 equip sync incomplete: noti105=%v mode0Skin=%v", gotNoti105, gotMode0Skin)
	}
	// Reload via ReadBag to confirm slot 32 persists without failing ReadBag validation.
	reloaded, err := st.LiveCharacter(accID, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloadedBag, err := inventory.ReadBag(reloaded.State)
	if err != nil {
		t.Fatalf("ReadBag failed after saving slot 32: %v", err)
	}
	if len(reloadedBag.Worn) != 1 || reloadedBag.Worn[0].Slot != inventory.WornCreatureSkinSlot {
		t.Fatalf("expected worn slot 32 persisted, got %+v", reloadedBag.Worn)
	}
}
