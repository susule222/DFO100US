package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	skinStorageTable           = "skin_storage_v224"
	skinStorageAccountTable    = "skin_storage_acct_v224"
	skinStorageBackfillVersion = 2
	skinStorageRecentMax       = 30
	actionTypeAddSkinStorage   = 170 // 0xaa: [action type] `[add skin storage]`
)

type persistedSkinStorage struct {
	Owned            map[byte][]uint32               `json:"owned,omitempty"`
	Equipped         map[byte][]uint32               `json:"equipped,omitempty"`
	Recent           []protocol.SkinRecentEntry      `json:"recent,omitempty"`
	EmoticonKeywords []protocol.EmoticonKeywordEntry `json:"emoticon_keywords,omitempty"`
	Backfilled       bool                            `json:"backfilled,omitempty"`
	BackfillVersion  int                             `json:"backfill_version,omitempty"`
}

type persistedAccountSkinStorage struct {
	Owned  map[byte][]uint32          `json:"owned,omitempty"`
	Recent []protocol.SkinRecentEntry `json:"recent,omitempty"`
}

func skinStorageKey(account, charID int64) string {
	return fmt.Sprintf("%d/%d", account, charID)
}

func skinStorageAccountKey(account int64) string {
	return fmt.Sprintf("%d", account)
}

// ownedSkinCategory maps equip-only category 6 (PartyRequestFrame) to its
// native CSkinStorageManager owned table index 2 (PartyFrame).
// Verified in DFO_unpacked.exe: SkinUI (0x14251ed9a) and NOTI 1546 case 6
// (0x142849cb8) both look up owned PartyRequestFrames in owned[2], and Summary
// tab (0x14251d01a) checks skinData.type == entry.Category (2).
func ownedSkinCategory(cat byte) byte {
	if cat == protocol.SkinCatPartyRequestFrame {
		return protocol.SkinCatPartyFrame
	}
	return cat
}

// defaultOwnedSkins returns built-in default skin IDs that MUST be present in
// NOTI 1545 so NOTI 1546's GetOwnedSkin check (0x142847d20) never drops them.
func defaultOwnedSkins(cat byte) []uint32 {
	switch cat {
	case protocol.SkinCatDamageFont:
		return []uint32{protocol.DefaultDamageFontNormal, protocol.DefaultDamageFontSum}
	case protocol.SkinCatSkillCutscene:
		return []uint32{protocol.DefaultSkillCutscene}
	case protocol.SkinCatPartyFrame:
		return []uint32{protocol.DefaultPartyFrame, protocol.DefaultPartyRequestFrame, protocol.UnsetPartyRequestFrame}
	default:
		return nil
	}
}

func isDefaultSkin(cat byte, id uint32) bool {
	for _, d := range defaultOwnedSkins(ownedSkinCategory(cat)) {
		if d == id {
			return true
		}
	}
	return false
}

func mergeSortedUnique(a, b []uint32) []uint32 {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[uint32]struct{}, len(a)+len(b))
	out := make([]uint32, 0, len(a)+len(b))
	for _, id := range a {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	for _, id := range b {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func addOwnedToMap(m *map[byte][]uint32, cat byte, skinID uint32) bool {
	if skinID == 0 {
		return false
	}
	cat = ownedSkinCategory(cat)
	if *m == nil {
		*m = map[byte][]uint32{}
	}
	for _, existing := range (*m)[cat] {
		if existing == skinID {
			return false
		}
	}
	list := append((*m)[cat], skinID)
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	(*m)[cat] = list
	return true
}

func removeOwnedFromMap(m map[byte][]uint32, cat byte, skinID uint32) bool {
	if m == nil || skinID == 0 {
		return false
	}
	cat = ownedSkinCategory(cat)
	list := m[cat]
	for i, existing := range list {
		if existing == skinID {
			list = append(list[:i], list[i+1:]...)
			if len(list) == 0 {
				delete(m, cat)
			} else {
				m[cat] = list
			}
			return true
		}
	}
	return false
}

func appendRecentSkin(list []protocol.SkinRecentEntry, cat byte, skinID uint32) []protocol.SkinRecentEntry {
	cat = ownedSkinCategory(cat)
	if skinID == 0 || isDefaultSkin(cat, skinID) {
		return list
	}
	out := make([]protocol.SkinRecentEntry, 0, len(list)+1)
	for _, e := range list {
		if ownedSkinCategory(e.Category) == cat && e.SkinID == skinID {
			continue
		}
		out = append(out, protocol.SkinRecentEntry{Category: ownedSkinCategory(e.Category), SkinID: e.SkinID})
	}
	out = append(out, protocol.SkinRecentEntry{Category: cat, SkinID: skinID})
	if len(out) > skinStorageRecentMax {
		out = out[len(out)-skinStorageRecentMax:]
	}
	return out
}

func mergedRecentSkins(charRecent, acctRecent []protocol.SkinRecentEntry) []protocol.SkinRecentEntry {
	var out []protocol.SkinRecentEntry
	for _, e := range acctRecent {
		out = appendRecentSkin(out, e.Category, e.SkinID)
	}
	for _, e := range charRecent {
		out = appendRecentSkin(out, e.Category, e.SkinID)
	}
	return out
}

func hasNonZeroSkin(slots []uint32) bool {
	for _, id := range slots {
		if id != 0 {
			return true
		}
	}
	return false
}

func loadSkinStorage(store *storage.Store, boosters *catalog.BoosterCatalog, account, charID int64) (persistedSkinStorage, persistedAccountSkinStorage, bool) {
	st := persistedSkinStorage{
		Owned:    map[byte][]uint32{},
		Equipped: map[byte][]uint32{},
	}
	acct := persistedAccountSkinStorage{
		Owned: map[byte][]uint32{},
	}
	if store == nil || charID == 0 {
		return st, acct, false
	}
	_, _ = store.Database().Get(skinStorageTable, skinStorageKey(account, charID), &st)
	if account > 0 {
		_, _ = store.Database().Get(skinStorageAccountTable, skinStorageAccountKey(account), &acct)
	}
	if st.Owned == nil {
		st.Owned = map[byte][]uint32{}
	}
	if st.Equipped == nil {
		st.Equipped = map[byte][]uint32{}
	}
	if acct.Owned == nil {
		acct.Owned = map[byte][]uint32{}
	}

	changed := false
	acctChanged := false

	// Migrate any legacy PartyRequestFrame owned entries from cat 6 -> cat 2.
	if legacy := st.Owned[protocol.SkinCatPartyRequestFrame]; len(legacy) > 0 {
		for _, id := range legacy {
			addOwnedToMap(&st.Owned, protocol.SkinCatPartyFrame, id)
		}
		delete(st.Owned, protocol.SkinCatPartyRequestFrame)
		changed = true
	}
	if legacy := acct.Owned[protocol.SkinCatPartyRequestFrame]; len(legacy) > 0 {
		for _, id := range legacy {
			addOwnedToMap(&acct.Owned, protocol.SkinCatPartyFrame, id)
		}
		delete(acct.Owned, protocol.SkinCatPartyRequestFrame)
		acctChanged = true
	}

	// Backfill (v2): scan consume507 action=170 history so multi-skin bundles
	// (e.g. 4-emoticon packs), account-bound flags, and Recently Acquired entries
	// are all populated for characters that consumed items under earlier builds.
	if st.BackfillVersion < skinStorageBackfillVersion && boosters != nil && len(boosters.SkinItems) > 0 {
		st.Backfilled = true
		st.BackfillVersion = skinStorageBackfillVersion
		changed = true
		prefix := fmt.Sprintf("%d/consume507/", charID)
		type evRow struct {
			Outcome struct {
				Action   uint32 `json:"action"`
				Template uint32 `json:"template"`
			} `json:"outcome"`
		}
		var keys []string
		rows := map[string]evRow{}
		store.Database().Each("character_events", func(k string, raw json.RawMessage) bool {
			if !strings.HasPrefix(k, prefix) {
				return true
			}
			var rec evRow
			if json.Unmarshal(raw, &rec) != nil || rec.Outcome.Action != actionTypeAddSkinStorage || rec.Outcome.Template == 0 {
				return true
			}
			keys = append(keys, k)
			rows[k] = rec
			return true
		})
		sort.Strings(keys)
		for _, k := range keys {
			rec := rows[k]
			item, ok := boosters.SkinItems[rec.Outcome.Template]
			if !ok {
				continue
			}
			ownedCat := ownedSkinCategory(item.Category)
			indices := item.SkinIndices()
			for _, skinID := range indices {
				if item.AccountBound && account > 0 {
					if addOwnedToMap(&acct.Owned, ownedCat, skinID) {
						acctChanged = true
					}
					if removeOwnedFromMap(st.Owned, ownedCat, skinID) {
						changed = true
					}
					acct.Recent = appendRecentSkin(acct.Recent, ownedCat, skinID)
					acctChanged = true
					st.Recent = appendRecentSkin(st.Recent, ownedCat, skinID)
				} else {
					addOwnedToMap(&st.Owned, ownedCat, skinID)
					st.Recent = appendRecentSkin(st.Recent, ownedCat, skinID)
				}
			}
			if ownedCat == protocol.SkinCatDamageFont && len(st.Equipped[protocol.SkinCatDamageFont]) == 0 && len(indices) > 0 {
				st.Equipped[protocol.SkinCatDamageFont] = []uint32{indices[0], indices[0]}
			}
		}
		// Seed Recent from any remaining non-default character-owned skins if still empty.
		if len(st.Recent) == 0 {
			for _, cat := range []byte{
				protocol.SkinCatDamageFont,
				protocol.SkinCatSkillCutscene,
				protocol.SkinCatPartyFrame,
				protocol.SkinCatEmoticon,
				protocol.SkinCatWeapon,
			} {
				for _, id := range st.Owned[cat] {
					st.Recent = appendRecentSkin(st.Recent, cat, id)
				}
			}
		}
	}

	if changed || acctChanged {
		_ = saveSkinStorage(store, account, charID, st, acct, acctChanged)
	}
	return st, acct, changed || acctChanged
}

func saveSkinStorage(store *storage.Store, account, charID int64, st persistedSkinStorage, acct persistedAccountSkinStorage, saveAcct bool) error {
	if store == nil || charID == 0 {
		return nil
	}
	return store.Database().Mutate(func(tx *db100.Tx) error {
		if err := tx.Put(skinStorageTable, skinStorageKey(account, charID), st); err != nil {
			return err
		}
		if saveAcct && account > 0 {
			if err := tx.Put(skinStorageAccountTable, skinStorageAccountKey(account), acct); err != nil {
				return err
			}
		}
		return nil
	})
}

func buildOwnedListPacket(name string, cat byte, st persistedSkinStorage, acct persistedAccountSkinStorage) (outboundPacket, bool) {
	cat = ownedSkinCategory(cat)
	charSkins := mergeSortedUnique(defaultOwnedSkins(cat), st.Owned[cat])
	acctSkins := mergeSortedUnique(nil, acct.Owned[cat])
	if len(charSkins) == 0 && len(acctSkins) == 0 {
		return outboundPacket{}, false
	}
	return outboundPacket{
		name, 0, protocol.NotiSkinStorageList,
		protocol.SkinStorageOwnedList(cat, charSkins, acctSkins),
	}, true
}

func skinStoragePacketsOnEntry(store *storage.Store, boosters *catalog.BoosterCatalog, account, charID int64) []outboundPacket {
	if store == nil || charID == 0 {
		return nil
	}
	st, acct, _ := loadSkinStorage(store, boosters, account, charID)
	var out []outboundPacket
	// 1. Send NOTI 1545 (owned lists) BEFORE NOTI 1546 (equipped), because
	// NOTI 1546 validates every equipped ID against owned[] (0x142847d20).
	for _, cat := range []byte{
		protocol.SkinCatDamageFont,
		protocol.SkinCatSkillCutscene,
		protocol.SkinCatPartyFrame,
		protocol.SkinCatEmoticon,
		protocol.SkinCatWeapon,
	} {
		if pkt, ok := buildOwnedListPacket("skin_storage_list_restored", cat, st, acct); ok {
			out = append(out, pkt)
		}
	}
	// 2. Restore equipped state across all 6 equip categories (0, 1, 2, 3, 4, 6).
	for _, cat := range []byte{
		protocol.SkinCatDamageFont,
		protocol.SkinCatSkillCutscene,
		protocol.SkinCatPartyFrame,
		protocol.SkinCatEmoticon,
		protocol.SkinCatWeapon,
		protocol.SkinCatPartyRequestFrame,
	} {
		eq := st.Equipped[cat]
		if hasNonZeroSkin(eq) {
			out = append(out, outboundPacket{
				"skin_storage_equip_restored", 0, protocol.NotiSkinStorageEquip,
				protocol.SkinStorageEquipped(cat, eq),
			})
		}
	}
	// 3. Restore Recently Acquired list for Summary tab (NOTI 1547).
	if recent := mergedRecentSkins(st.Recent, acct.Recent); len(recent) > 0 {
		out = append(out, outboundPacket{
			"skin_storage_recent_restored", 0, protocol.NotiSkinStorageRecent,
			protocol.SkinStorageRecentlyAcquired(recent),
		})
	}
	// 4. Restore Emoticon chat keyword triggers (NOTI 2250).
	if len(st.EmoticonKeywords) > 0 {
		out = append(out, outboundPacket{
			"emoticon_keyword_list_restored", 0, protocol.NotiEmoticonKeywords,
			protocol.EmoticonKeywordListNoti(st.EmoticonKeywords),
		})
	}
	return out
}

func (w *worldSession) unlockSkinStorageItem(template uint32) []outboundPacket {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.loot == nil || w.loot.Boosters == nil || w.role.ID == 0 {
		return nil
	}
	item, ok := w.loot.Boosters.SkinItems[template]
	if !ok {
		return nil
	}
	indices := item.SkinIndices()
	if len(indices) == 0 {
		return nil
	}
	ownedCat := ownedSkinCategory(item.Category)
	st, acct, _ := loadSkinStorage(w.characters.Store, w.loot.Boosters, w.account, w.role.ID)
	saveAcct := false
	for _, skinID := range indices {
		if item.AccountBound && w.account > 0 {
			addOwnedToMap(&acct.Owned, ownedCat, skinID)
			removeOwnedFromMap(st.Owned, ownedCat, skinID)
			acct.Recent = appendRecentSkin(acct.Recent, ownedCat, skinID)
			saveAcct = true
		} else {
			addOwnedToMap(&st.Owned, ownedCat, skinID)
		}
		st.Recent = appendRecentSkin(st.Recent, ownedCat, skinID)
	}
	// For Damage Font, auto-equip as default normal+cumulative font when none is equipped yet
	// so the player sees it take effect immediately upon using the item.
	autoEquipped := false
	if ownedCat == protocol.SkinCatDamageFont && !hasNonZeroSkin(st.Equipped[protocol.SkinCatDamageFont]) {
		st.Equipped[protocol.SkinCatDamageFont] = []uint32{indices[0], indices[0]}
		autoEquipped = true
	}
	_ = saveSkinStorage(w.characters.Store, w.account, w.role.ID, st, acct, saveAcct)

	var out []outboundPacket
	if pkt, ok := buildOwnedListPacket("skin_storage_list_updated", ownedCat, st, acct); ok {
		out = append(out, pkt)
	}
	if recent := mergedRecentSkins(st.Recent, acct.Recent); len(recent) > 0 {
		out = append(out, outboundPacket{
			"skin_storage_recent_updated", 0, protocol.NotiSkinStorageRecent,
			protocol.SkinStorageRecentlyAcquired(recent),
		})
	}
	if autoEquipped || (ownedCat == protocol.SkinCatDamageFont && hasNonZeroSkin(st.Equipped[ownedCat])) {
		out = append(out, outboundPacket{
			"skin_storage_equip_updated", 0, protocol.NotiSkinStorageEquip,
			protocol.SkinStorageEquipped(ownedCat, st.Equipped[ownedCat]),
		})
	}
	return out
}

func (w *worldSession) handleSkinStorageEquip(plaintext []byte) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("world session not ready for skin storage equip")
	}
	catU32, slots, err := protocol.DecodeSkinStorageEquipCmd(plaintext)
	if err != nil {
		return nil, err
	}
	if catU32 > 6 || catU32 == 5 {
		return nil, fmt.Errorf("unsupported skin storage category %d", catU32)
	}
	cat := byte(catU32)
	var boosters *catalog.BoosterCatalog
	if w.loot != nil {
		boosters = w.loot.Boosters
	}
	st, acct, _ := loadSkinStorage(w.characters.Store, boosters, w.account, w.role.ID)
	var active []uint32
	switch cat {
	case protocol.SkinCatDamageFont:
		active = []uint32{slots[0], slots[1]}
	case protocol.SkinCatSkillCutscene:
		for _, id := range slots {
			if id != 0 {
				active = append(active, id)
			}
		}
	case protocol.SkinCatEmoticon:
		active = []uint32{slots[0], slots[1], slots[2], slots[3]}
	default:
		// Categories 2 (PartyFrame), 4 (Weapon), 6 (PartyRequestFrame).
		active = []uint32{slots[0]}
	}
	st.Equipped[cat] = active
	if err := saveSkinStorage(w.characters.Store, w.account, w.role.ID, st, acct, false); err != nil {
		return nil, err
	}
	plan := []outboundPacket{
		{"skin_storage_equip_ack", 1, protocol.CmdSkinStorageEquip, protocol.SkinStorageEquipSuccess(catU32, slots)},
		{"skin_storage_equip_updated", 0, protocol.NotiSkinStorageEquip, protocol.SkinStorageEquipped(cat, active)},
	}
	// Sync live town actor appearance (NOTI 2 mode 0) when equipping or unequipping
	// Weapon Skin (0x62), Party Frame (0x30), or Party Request Frame (0x34).
	if (cat == protocol.SkinCatWeapon || cat == protocol.SkinCatPartyFrame || cat == protocol.SkinCatPartyRequestFrame) &&
		w.activeDungeon == nil && !w.selectingDungeon && w.role.WireID != 0 && w.role.WireID != 0xffff && w.role.Name != "" {
		if appearanceBody, err := w.characters.EntryBasicProbe(w.role, w.channelCtx); err == nil {
			plan = append(plan, outboundPacket{"town_appearance_updated", 0, 2, appearanceBody})
		}
	}
	return plan, nil
}

// handleInstantEmoticon answers CMD 1551 (0x60f) sent when clicking an Emoticon
// quick-slot or typing a bound Emoticon chat keyword, broadcasting NOTI 1523
// (0x5f3) so the animated emoticon renders above the actor's head.
func (w *worldSession) handleInstantEmoticon(plaintext []byte) ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 || w.role.WireID == 0 || w.role.WireID == 0xffff {
		return nil, fmt.Errorf("world session not ready for instant emoticon")
	}
	emoticonID, err := protocol.DecodeInstantEmoticonCmd(plaintext)
	if err != nil {
		return nil, err
	}
	if emoticonID == 0 {
		return nil, fmt.Errorf("instant emoticon id must be non-zero")
	}
	return []outboundPacket{
		{
			"instant_emoticon_broadcast", 0, protocol.NotiInstantEmoticon,
			protocol.InstantEmoticonBroadcast(w.role.WireID, emoticonID),
		},
	}, nil
}

// handleEmoticonKeywordSave answers CMD 2038 (0x7f6) to persist Emoticon chat
// keyword triggers and push ACK 2038 + NOTI 2250 (0x8ca).
func (w *worldSession) handleEmoticonKeywordSave(plaintext []byte) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("world session not ready for emoticon keyword save")
	}
	entries, err := protocol.DecodeEmoticonKeywordSaveCmd(plaintext)
	if err != nil {
		return nil, err
	}
	var boosters *catalog.BoosterCatalog
	if w.loot != nil {
		boosters = w.loot.Boosters
	}
	st, acct, _ := loadSkinStorage(w.characters.Store, boosters, w.account, w.role.ID)
	st.EmoticonKeywords = entries
	if err := saveSkinStorage(w.characters.Store, w.account, w.role.ID, st, acct, false); err != nil {
		return nil, err
	}
	return []outboundPacket{
		{"emoticon_keyword_save_ack", 1, protocol.CmdEmoticonKeywordSave, protocol.EmoticonKeywordSaveSuccess()},
		{"emoticon_keyword_list_updated", 0, protocol.NotiEmoticonKeywords, protocol.EmoticonKeywordListNoti(entries)},
	}, nil
}

// handleWeaponSkinMold answers CMD 1564 (0x61c) for Linus's Weapon Mold
// ([make weapon skin]): extracts a weapon from Space 0 bag.Equipment into
// Skin Storage category 4 (Weapon), consumes 1 mold stackable from MaterialSlot
// when provided, and emits NOTI 1545 + NOTI 1547 + NOTI 14 + ACK 1564.
func (w *worldSession) handleWeaponSkinMold(plaintext, rawFrame []byte) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("world session not ready for weapon skin mold")
	}
	req, err := protocol.DecodeWeaponSkinMoldCmd(plaintext)
	if err != nil {
		return nil, err
	}
	if req.WeaponSlot == 0 || req.WeaponSlot > 0xffff {
		return []outboundPacket{{"weapon_skin_mold_refused", 1, protocol.CmdWeaponSkinMold, protocol.WeaponSkinMoldRefused(1)}}, nil
	}
	weaponSlot := uint16(req.WeaponSlot)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("weaponmold1564/%d/%d/%d/%d/%x", w.role.ID, req.WeaponSlot, req.MoldMode, req.MaterialSlot, sha256.Sum256(rawFrame))

	var weaponTemplate uint32
	var materialSlot uint16
	var materialConsumed bool
	var materialEmptied bool
	var refuseCode uint16
	saved, _, err := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, key, "us100-weapon-mold-1564-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, berr := inventory.ReadBag(current.State)
		if berr != nil {
			return nil, nil, berr
		}
		eqIdx := -1
		for i, eq := range b.Equipment {
			if eq.Slot == weaponSlot && eq.Template != 0 {
				eqIdx = i
				break
			}
		}
		if eqIdx < 0 {
			refuseCode = 1
			return nil, nil, fmt.Errorf("no weapon at equipment slot %d", weaponSlot)
		}
		weaponTemplate = b.Equipment[eqIdx].Template
		if req.MaterialSlot != 0 && req.MaterialSlot != 0xffffffff && req.MaterialSlot <= 0xffff {
			ms := uint16(req.MaterialSlot)
			matIdx := -1
			for i, it := range b.Items {
				if it.Slot == ms && it.Amount > 0 {
					matIdx = i
					break
				}
			}
			if matIdx < 0 {
				refuseCode = 2
				return nil, nil, fmt.Errorf("no mold material at stackable slot %d", ms)
			}
			materialSlot = ms
			materialConsumed = true
			if b.Items[matIdx].Amount > 1 {
				b.Items[matIdx].Amount--
			} else {
				b.Items = append(b.Items[:matIdx], b.Items[matIdx+1:]...)
				materialEmptied = true
			}
		}
		b.Equipment = append(b.Equipment[:eqIdx], b.Equipment[eqIdx+1:]...)
		updated, serr := inventory.SaveBag(current.State, b)
		if serr != nil {
			return nil, nil, serr
		}
		receipt, merr := json.Marshal(map[string]any{
			"weapon_slot":       weaponSlot,
			"weapon_template":   weaponTemplate,
			"mold_mode":         req.MoldMode,
			"material_slot":     req.MaterialSlot,
			"material_consumed": materialConsumed,
			"material_emptied":  materialEmptied,
		})
		return updated, receipt, merr
	})
	if err != nil {
		if refuseCode != 0 {
			return []outboundPacket{{"weapon_skin_mold_refused", 1, protocol.CmdWeaponSkinMold, protocol.WeaponSkinMoldRefused(refuseCode)}}, nil
		}
		return nil, err
	}
	saved.WireID = w.role.WireID
	w.role = saved

	b, err := inventory.ReadBag(saved.State)
	if err != nil {
		return nil, err
	}
	rows := b.Rows()
	rows = append(rows, protocol.EmptySlotItem(weaponSlot))
	if materialEmptied {
		rows = append(rows, protocol.EmptySlotItem(materialSlot))
	}
	sort.Slice(rows, func(i, j int) bool {
		return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
	})
	invUpdate, err := protocol.US224InventoryUpdate(rows)
	if err != nil {
		return nil, err
	}

	var boosters *catalog.BoosterCatalog
	if w.loot != nil {
		boosters = w.loot.Boosters
	}
	st, acct, _ := loadSkinStorage(w.characters.Store, boosters, w.account, w.role.ID)
	addOwnedToMap(&st.Owned, protocol.SkinCatWeapon, weaponTemplate)
	st.Recent = appendRecentSkin(st.Recent, protocol.SkinCatWeapon, weaponTemplate)
	if err := saveSkinStorage(w.characters.Store, w.account, w.role.ID, st, acct, false); err != nil {
		return nil, err
	}

	var plan []outboundPacket
	if pkt, ok := buildOwnedListPacket("skin_storage_list_updated", protocol.SkinCatWeapon, st, acct); ok {
		plan = append(plan, pkt)
	}
	if recent := mergedRecentSkins(st.Recent, acct.Recent); len(recent) > 0 {
		plan = append(plan, outboundPacket{
			"skin_storage_recent_updated", 0, protocol.NotiSkinStorageRecent,
			protocol.SkinStorageRecentlyAcquired(recent),
		})
	}
	plan = append(plan,
		outboundPacket{"weapon_skin_mold_inventory_updated", 0, 14, invUpdate},
		outboundPacket{"weapon_skin_mold_ack", 1, protocol.CmdWeaponSkinMold, protocol.WeaponSkinMoldSuccess()},
	)
	return plan, nil
}
