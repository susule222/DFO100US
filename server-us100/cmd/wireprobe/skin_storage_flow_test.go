package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"path/filepath"
	"slices"
	"testing"
)

func decodeNoti1545(t *testing.T, p []byte) (cat byte, charSkins, acctSkins []uint32) {
	t.Helper()
	if len(p) < 5 {
		t.Fatalf("NOTI 1545 too short: %x", p)
	}
	cat = p[0]
	off := 1
	charCount := int(binary.LittleEndian.Uint16(p[off : off+2]))
	off += 2
	for i := 0; i < charCount; i++ {
		charSkins = append(charSkins, binary.LittleEndian.Uint32(p[off:off+4]))
		off += 4
		if cat != protocol.SkinCatWeapon {
			off += 4 // expire u32
		}
	}
	acctCount := int(binary.LittleEndian.Uint16(p[off : off+2]))
	off += 2
	for i := 0; i < acctCount; i++ {
		acctSkins = append(acctSkins, binary.LittleEndian.Uint32(p[off:off+4]))
		off += 8 // id + expire
	}
	if off != len(p) {
		t.Fatalf("NOTI 1545 trailing bytes: consumed %d of %d", off, len(p))
	}
	return cat, charSkins, acctSkins
}

func decodeNoti1547(t *testing.T, p []byte) []protocol.SkinRecentEntry {
	t.Helper()
	if len(p) < 1 {
		t.Fatalf("NOTI 1547 empty")
	}
	n := int(p[0])
	if len(p) != 1+n*5 {
		t.Fatalf("NOTI 1547 length mismatch: count=%d len=%d", n, len(p))
	}
	out := make([]protocol.SkinRecentEntry, 0, n)
	off := 1
	for i := 0; i < n; i++ {
		out = append(out, protocol.SkinRecentEntry{
			Category: p[off],
			SkinID:   binary.LittleEndian.Uint32(p[off+1 : off+5]),
		})
		off += 5
	}
	return out
}

func TestSkinStorageUnlockOrderingAccountShareCutSceneAndBackfillV2(t *testing.T) {
	ctx := context.Background()
	c, err := catalog.LoadLoot("../../configs/us100/loot.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := inventory.LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatal(err)
	}
	bc, err := catalog.LoadBoosters("../../configs/us100/boosters.generated.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	dfItem, ok := bc.SkinItems[10333091]
	if !ok || dfItem.Category != protocol.SkinCatDamageFont || dfItem.Index != 53 {
		t.Fatalf("expected 10333091 as DamageFont #53, got ok=%v item=%+v", ok, dfItem)
	}
	emoItem, ok := bc.SkinItems[50081685]
	if !ok || emoItem.Category != protocol.SkinCatEmoticon || !emoItem.AccountBound || len(emoItem.SkinIndices()) != 4 {
		t.Fatalf("expected 50081685 as account-bound 4-emoticon bundle, got ok=%v item=%+v", ok, emoItem)
	}

	path := filepath.Join(t.TempDir(), "skin.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.Database().Mutate(func(tx *db100.Tx) error {
		if err := tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 1, "name": "HeroA", "profession": 0,
			"config_version": r.Source,
			"state": map[string]any{
				"level": 100,
				"inventory": inventory.Bag{
					Version: "ordinary-bag-v1",
					Items: []inventory.BagItem{
						{Slot: 85, Template: 10333091, Amount: 1},
						{Slot: 86, Template: 50081685, Amount: 1},
					},
				},
			},
		}); err != nil {
			return err
		}
		return tx.Put("characters", "49", map[string]any{
			"id": 49, "account_id": 42, "wire_id": 2, "name": "HeroB", "profession": 1,
			"config_version": r.Source,
			"state": map[string]any{
				"level":     100,
				"inventory": inventory.Bag{Version: "ordinary-bag-v1"},
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.LiveCharacter(42, 48)
	if err != nil {
		t.Fatal(err)
	}

	w := &worldSession{
		account:    42,
		role:       role,
		characters: &character.Service{Store: store},
		loot:       &loot.Service{Store: store, Catalog: c, BagRules: r, Boosters: bc},
	}

	// 1. Use Damage Font 10333091 via CMD 507 (action=170): NOTI 1545/1547/1546 must precede ACK 507.
	cmd507 := make([]byte, 32)
	binary.LittleEndian.PutUint16(cmd507[0:2], 85)
	binary.LittleEndian.PutUint32(cmd507[7:11], actionTypeAddSkinStorage)
	plan, err := w.useStackableAction(cmd507, []byte("raw-507-df53"))
	if err != nil {
		t.Fatalf("useStackableAction(10333091) failed: %v", err)
	}
	listIdx, recentIdx, equipIdx, ackIdx := -1, -1, -1, -1
	for i, p := range plan {
		switch p.ID {
		case protocol.NotiSkinStorageList:
			cat, charSkins, _ := decodeNoti1545(t, p.Payload)
			if cat != protocol.SkinCatDamageFont || !slices.Contains(charSkins, 53) || !slices.Contains(charSkins, protocol.DefaultDamageFontNormal) || !slices.Contains(charSkins, protocol.DefaultDamageFontSum) {
				t.Fatalf("unexpected NOTI 1545 for DamageFont: cat=%d charSkins=%v", cat, charSkins)
			}
			listIdx = i
		case protocol.NotiSkinStorageRecent:
			recent := decodeNoti1547(t, p.Payload)
			if len(recent) != 1 || recent[0] != (protocol.SkinRecentEntry{Category: 0, SkinID: 53}) {
				t.Fatalf("unexpected NOTI 1547: %+v", recent)
			}
			recentIdx = i
		case protocol.NotiSkinStorageEquip:
			if len(p.Payload) != 9 || p.Payload[0] != 0 || binary.LittleEndian.Uint32(p.Payload[1:5]) != 53 || binary.LittleEndian.Uint32(p.Payload[5:9]) != 53 {
				t.Fatalf("unexpected NOTI 1546 auto-equip: %x", p.Payload)
			}
			equipIdx = i
		case protocol.CmdUseStackableAction:
			ackIdx = i
		}
	}
	if listIdx < 0 || recentIdx < 0 || equipIdx < 0 || ackIdx < 0 || !(listIdx < ackIdx && recentIdx < ackIdx && equipIdx < ackIdx) {
		t.Fatalf("expected NOTI 1545/1547/1546 before ACK 507, got indices list=%d recent=%d equip=%d ack=%d", listIdx, recentIdx, equipIdx, ackIdx)
	}

	// 2. Use multi-emoticon account-bound bundle 50081685 via CMD 507.
	cmd507Emo := make([]byte, 32)
	binary.LittleEndian.PutUint16(cmd507Emo[0:2], 86)
	binary.LittleEndian.PutUint32(cmd507Emo[7:11], actionTypeAddSkinStorage)
	emoPlan, err := w.useStackableAction(cmd507Emo, []byte("raw-507-emo50081685"))
	if err != nil {
		t.Fatalf("useStackableAction(50081685) failed: %v", err)
	}
	var gotAcctEmos bool
	var recentCount int
	for _, p := range emoPlan {
		if p.ID == protocol.NotiSkinStorageList {
			cat, charSkins, acctSkins := decodeNoti1545(t, p.Payload)
			if cat == protocol.SkinCatEmoticon && len(charSkins) == 0 && len(acctSkins) == 4 && acctSkins[0] == 500030126 && acctSkins[3] == 500030129 {
				gotAcctEmos = true
			}
		}
		if p.ID == protocol.NotiSkinStorageRecent {
			recentCount = len(decodeNoti1547(t, p.Payload))
		}
	}
	if !gotAcctEmos || recentCount != 5 {
		t.Fatalf("expected 4 account-bound emoticons and 5 recent entries, got acct=%v recent=%d", gotAcctEmos, recentCount)
	}

	// 3. Second character on same account shares account-bound emoticons + gets default owned skins on entry.
	charBEntry := skinStoragePacketsOnEntry(store, bc, 42, 49)
	seenDefaults := map[byte][]uint32{}
	var charBAcctEmos []uint32
	var charBRecent []protocol.SkinRecentEntry
	for _, p := range charBEntry {
		if p.ID == protocol.NotiSkinStorageList {
			cat, charSkins, acctSkins := decodeNoti1545(t, p.Payload)
			seenDefaults[cat] = charSkins
			if cat == protocol.SkinCatEmoticon {
				charBAcctEmos = acctSkins
			}
		}
		if p.ID == protocol.NotiSkinStorageRecent {
			charBRecent = decodeNoti1547(t, p.Payload)
		}
	}
	if !slices.Contains(seenDefaults[protocol.SkinCatSkillCutscene], protocol.DefaultSkillCutscene) {
		t.Fatalf("expected default SkillCutscene 30000 in cat 1 owned list, got %v", seenDefaults[protocol.SkinCatSkillCutscene])
	}
	if !slices.Contains(seenDefaults[protocol.SkinCatPartyFrame], protocol.DefaultPartyFrame) || !slices.Contains(seenDefaults[protocol.SkinCatPartyFrame], protocol.DefaultPartyRequestFrame) {
		t.Fatalf("expected default PartyFrame 20000 and PartyRequestFrame 50000 in cat 2 owned list, got %v", seenDefaults[protocol.SkinCatPartyFrame])
	}
	if len(charBAcctEmos) != 4 || len(charBRecent) != 4 {
		t.Fatalf("expected HeroB to inherit 4 account emoticons & recent entries, got acct=%v recent=%+v", charBAcctEmos, charBRecent)
	}

	// 4. Equip default + second Skill Cut-Scene via CMD 1565 cat 1.
	cmdCut := make([]byte, 44)
	binary.LittleEndian.PutUint32(cmdCut[0:4], uint32(protocol.SkinCatSkillCutscene))
	binary.LittleEndian.PutUint32(cmdCut[4:8], protocol.DefaultSkillCutscene)
	binary.LittleEndian.PutUint32(cmdCut[8:12], 30001)
	cutPlan, err := w.handleSkinStorageEquip(cmdCut)
	if err != nil {
		t.Fatalf("handleSkinStorageEquip(cutscene) failed: %v", err)
	}
	if len(cutPlan) != 2 || cutPlan[1].ID != protocol.NotiSkinStorageEquip ||
		len(cutPlan[1].Payload) != 11 || cutPlan[1].Payload[0] != 1 ||
		binary.LittleEndian.Uint16(cutPlan[1].Payload[1:3]) != 2 ||
		binary.LittleEndian.Uint32(cutPlan[1].Payload[3:7]) != 30000 ||
		binary.LittleEndian.Uint32(cutPlan[1].Payload[7:11]) != 30001 {
		t.Fatalf("unexpected cutscene NOTI 1546: %x", cutPlan[1].Payload)
	}

	// 5. Backfill v1 -> v2 migration: simulate legacy v1 DB row (like char 48 before fix).
	err = store.Database().Mutate(func(tx *db100.Tx) error {
		tx.Delete(skinStorageAccountTable, skinStorageAccountKey(42))
		return tx.Put(skinStorageTable, skinStorageKey(42, 48), map[string]any{
			"owned":      map[string][]uint32{"0": {53}, "3": {500030126}},
			"equipped":   map[string][]uint32{"0": {53, 53}},
			"backfilled": true,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	migrated := skinStoragePacketsOnEntry(store, bc, 42, 48)
	var migratedEmos []uint32
	var migratedRecent []protocol.SkinRecentEntry
	for _, p := range migrated {
		if p.ID == protocol.NotiSkinStorageList {
			cat, charSkins, acctSkins := decodeNoti1545(t, p.Payload)
			if cat == protocol.SkinCatEmoticon {
				if len(charSkins) != 0 {
					t.Fatalf("expected legacy char-owned 500030126 moved to acctSkins, still in charSkins=%v", charSkins)
				}
				migratedEmos = acctSkins
			}
		}
		if p.ID == protocol.NotiSkinStorageRecent {
			migratedRecent = decodeNoti1547(t, p.Payload)
		}
	}
	if len(migratedEmos) != 4 || len(migratedRecent) != 5 {
		t.Fatalf("v2 backfill failed: migratedEmos=%v migratedRecent=%+v", migratedEmos, migratedRecent)
	}
}

func TestSkinStorageEmoticonSlotsBroadcastAndKeywords(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "emo.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	w := &worldSession{
		account:    42,
		role:       storage.Character{ID: 48, AccountID: 42, WireID: 7, Name: "EmoHero"},
		characters: &character.Service{Store: store},
	}

	// 1. Equip 4 Emoticon quick-slots via CMD 1565 cat 3.
	cmdEq := make([]byte, 44)
	binary.LittleEndian.PutUint32(cmdEq[0:4], uint32(protocol.SkinCatEmoticon))
	binary.LittleEndian.PutUint32(cmdEq[4:8], 500030126)
	binary.LittleEndian.PutUint32(cmdEq[8:12], 500030127)
	binary.LittleEndian.PutUint32(cmdEq[12:16], 500030128)
	binary.LittleEndian.PutUint32(cmdEq[16:20], 500030129)
	eqPlan, err := w.handleSkinStorageEquip(cmdEq)
	if err != nil {
		t.Fatalf("handleSkinStorageEquip(emoticon) failed: %v", err)
	}
	if len(eqPlan) != 2 || eqPlan[1].ID != protocol.NotiSkinStorageEquip || len(eqPlan[1].Payload) != 17 {
		t.Fatalf("expected 17-byte NOTI 1546 for 4 emoticon slots, got %+v", eqPlan)
	}
	for i, want := range []uint32{500030126, 500030127, 500030128, 500030129} {
		got := binary.LittleEndian.Uint32(eqPlan[1].Payload[1+i*4 : 5+i*4])
		if got != want {
			t.Fatalf("slot %d mismatch: got %d want %d", i, got, want)
		}
	}

	// 2. Trigger Instant Emoticon via CMD 1551 -> NOTI 1523 (actorServerID=7, skinID=500030128).
	cmdUse := make([]byte, 4)
	binary.LittleEndian.PutUint32(cmdUse, 500030128)
	bcast, err := w.handleInstantEmoticon(cmdUse)
	if err != nil {
		t.Fatalf("handleInstantEmoticon failed: %v", err)
	}
	if len(bcast) != 1 || bcast[0].ID != protocol.NotiInstantEmoticon || len(bcast[0].Payload) != 6 ||
		binary.LittleEndian.Uint16(bcast[0].Payload[0:2]) != 7 ||
		binary.LittleEndian.Uint32(bcast[0].Payload[2:6]) != 500030128 {
		t.Fatalf("unexpected NOTI 1523 broadcast: %+v", bcast)
	}

	// 3. Save Emoticon chat keyword triggers via CMD 2038 -> ACK 2038 + NOTI 2250 + entry restore.
	var e1, e2 protocol.EmoticonKeywordEntry
	e1.EmoticonID = 500030126
	copy(e1.Keyword[:], "gg")
	e2.EmoticonID = 500030127
	copy(e2.Keyword[:], "hello")
	cmdKW := protocol.EmoticonKeywordListNoti([]protocol.EmoticonKeywordEntry{e1, e2})
	kwPlan, err := w.handleEmoticonKeywordSave(cmdKW)
	if err != nil {
		t.Fatalf("handleEmoticonKeywordSave failed: %v", err)
	}
	if len(kwPlan) != 2 || kwPlan[0].ID != protocol.CmdEmoticonKeywordSave || len(kwPlan[0].Payload) != 1 || kwPlan[0].Payload[0] != 1 {
		t.Fatalf("unexpected ACK 2038: %+v", kwPlan)
	}
	if kwPlan[1].ID != protocol.NotiEmoticonKeywords || len(kwPlan[1].Payload) != 51 {
		t.Fatalf("unexpected NOTI 2250: len=%d", len(kwPlan[1].Payload))
	}
	entry := skinStoragePacketsOnEntry(store, nil, 42, 48)
	var restoredKW bool
	for _, p := range entry {
		if p.ID == protocol.NotiEmoticonKeywords && len(p.Payload) == 51 && binary.LittleEndian.Uint32(p.Payload[1:5]) == 500030126 {
			restoredKW = true
		}
	}
	if !restoredKW {
		t.Fatalf("expected NOTI 2250 restored on entry, got %+v", entry)
	}
}

func TestSkinStoragePartyFramesWeaponMoldAndTownAppearance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mold.db")
	store, err := storage.Open(ctx, storage.Config{DBPath: path, SkipSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put("characters", "48", map[string]any{
			"id": 48, "account_id": 42, "wire_id": 5, "name": "MoldHero", "profession": 0,
			"config_version": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"state": map[string]any{
				"level": 100,
				"inventory": inventory.Bag{
					Version: "ordinary-bag-v1",
					Equipment: []inventory.BagEquipment{
						{Slot: 9, Template: 100001, Durability: 35},
					},
					Items: []inventory.BagItem{
						{Slot: 85, Template: 10190852, Amount: 2},
					},
				},
			},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.LiveCharacter(42, 48)
	if err != nil {
		t.Fatal(err)
	}
	w := &worldSession{
		account:    42,
		role:       role,
		channelCtx: [2]byte{1, 1},
		characters: &character.Service{Store: store},
	}

	// 1. Linus Weapon Mold (CMD 1564): extract weapon at slot 9 using 1 mold at slot 85.
	cmdMold := make([]byte, 9)
	binary.LittleEndian.PutUint32(cmdMold[0:4], 9)
	cmdMold[4] = 0
	binary.LittleEndian.PutUint32(cmdMold[5:9], 85)
	moldPlan, err := w.handleWeaponSkinMold(cmdMold, []byte("raw-mold-1564"))
	if err != nil {
		t.Fatalf("handleWeaponSkinMold failed: %v", err)
	}
	var gotWeaponList, gotWeaponRecent, gotInv, gotAck bool
	for _, p := range moldPlan {
		switch p.ID {
		case protocol.NotiSkinStorageList:
			cat, charSkins, _ := decodeNoti1545(t, p.Payload)
			if cat == protocol.SkinCatWeapon && len(p.Payload) == 9 && len(charSkins) == 1 && charSkins[0] == 100001 {
				gotWeaponList = true
			}
		case protocol.NotiSkinStorageRecent:
			recent := decodeNoti1547(t, p.Payload)
			if len(recent) == 1 && recent[0] == (protocol.SkinRecentEntry{Category: 4, SkinID: 100001}) {
				gotWeaponRecent = true
			}
		case 14:
			gotInv = true
		case protocol.CmdWeaponSkinMold:
			if len(p.Payload) == 1 && p.Payload[0] == 1 {
				gotAck = true
			}
		}
	}
	if !gotWeaponList || !gotWeaponRecent || !gotInv || !gotAck {
		t.Fatalf("weapon mold plan incomplete: list=%v recent=%v inv=%v ack=%v", gotWeaponList, gotWeaponRecent, gotInv, gotAck)
	}
	bagAfter, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(bagAfter.Equipment) != 0 || len(bagAfter.Items) != 1 || bagAfter.Items[0].Amount != 1 {
		t.Fatalf("unexpected bag after weapon mold: eq=%+v items=%+v", bagAfter.Equipment, bagAfter.Items)
	}

	// 2. Equip Party Frame (cat 2 = 20010), Party Request Frame (cat 6 = 50010), Weapon Skin (cat 4 = 100001)
	// and verify live NOTI 2 mode 0 town appearance fixed-block offsets 0x30, 0x34, 0x62.
	equipOne := func(cat byte, id uint32) []byte {
		cmd := make([]byte, 44)
		binary.LittleEndian.PutUint32(cmd[0:4], uint32(cat))
		binary.LittleEndian.PutUint32(cmd[4:8], id)
		plan, e := w.handleSkinStorageEquip(cmd)
		if e != nil {
			t.Fatalf("equip cat %d id %d failed: %v", cat, id, e)
		}
		if len(plan) != 3 || plan[2].ID != 2 || plan[2].Name != "town_appearance_updated" {
			t.Fatalf("expected town_appearance_updated (NOTI 2) for cat %d, got %+v", cat, plan)
		}
		return plan[2].Payload
	}
	equipOne(protocol.SkinCatPartyFrame, 20010)
	equipOne(protocol.SkinCatPartyRequestFrame, 50010)
	noti2 := equipOne(protocol.SkinCatWeapon, 100001)

	// NOTI 2 mode 0 header is 5 bytes (mode=0, u16 count=1, ctx[2]), followed by 127-byte fixed block.
	fixed := noti2[5 : 5+127]
	if got := binary.LittleEndian.Uint32(fixed[0x30:0x34]); got != 20010 {
		t.Fatalf("NOTI 2 fixed+0x30 PartyFrame: got %d want 20010", got)
	}
	if got := binary.LittleEndian.Uint32(fixed[0x34:0x38]); got != 50010 {
		t.Fatalf("NOTI 2 fixed+0x34 PartyRequestFrame: got %d want 50010", got)
	}
	if got := binary.LittleEndian.Uint32(fixed[0x62:0x66]); got != 100001 {
		t.Fatalf("NOTI 2 fixed+0x62 WeaponSkin: got %d want 100001", got)
	}

	// 3. Unequip Weapon Skin (cat 4 = 0) clears fixed+0x62 in live town appearance.
	noti2Cleared := equipOne(protocol.SkinCatWeapon, 0)
	if got := binary.LittleEndian.Uint32(noti2Cleared[5+0x62 : 5+0x66]); got != 0 {
		t.Fatalf("NOTI 2 fixed+0x62 after unequip: got %d want 0", got)
	}
}
