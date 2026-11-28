package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	skinStorageTable         = "skin_storage_v224"
	actionTypeAddSkinStorage = 170 // 0xaa: [action type] `[add skin storage]`
)

type persistedSkinStorage struct {
	Owned      map[byte][]uint32 `json:"owned,omitempty"`
	Equipped   map[byte][]uint32 `json:"equipped,omitempty"`
	Backfilled bool              `json:"backfilled,omitempty"`
}

func skinStorageKey(account, charID int64) string {
	return fmt.Sprintf("%d/%d", account, charID)
}

func loadSkinStorage(store *storage.Store, boosters *catalog.BoosterCatalog, account, charID int64) (persistedSkinStorage, bool) {
	st := persistedSkinStorage{
		Owned:    map[byte][]uint32{},
		Equipped: map[byte][]uint32{},
	}
	if store == nil || charID == 0 {
		return st, false
	}
	_, _ = store.Database().Get(skinStorageTable, skinStorageKey(account, charID), &st)
	if st.Owned == nil {
		st.Owned = map[byte][]uint32{}
	}
	if st.Equipped == nil {
		st.Equipped = map[byte][]uint32{}
	}
	changed := false
	// One-time backfill for characters who already consumed [add skin storage] items
	// via CMD 507 (action=170) before Skin Storage persistence was enabled.
	if !st.Backfilled && boosters != nil && len(boosters.SkinItems) > 0 {
		st.Backfilled = true
		changed = true
		prefix := fmt.Sprintf("%d/consume507/", charID)
		type evRow struct {
			Outcome struct {
				Action   uint32 `json:"action"`
				Template uint32 `json:"template"`
			} `json:"outcome"`
		}
		store.Database().Each("character_events", func(k string, raw json.RawMessage) bool {
			if !strings.HasPrefix(k, prefix) {
				return true
			}
			var rec evRow
			if json.Unmarshal(raw, &rec) != nil || rec.Outcome.Action != actionTypeAddSkinStorage || rec.Outcome.Template == 0 {
				return true
			}
			if item, ok := boosters.SkinItems[rec.Outcome.Template]; ok {
				addOwnedSkin(&st, item.Category, item.Index)
				if item.Category == protocol.SkinCatDamageFont && len(st.Equipped[protocol.SkinCatDamageFont]) == 0 {
					st.Equipped[protocol.SkinCatDamageFont] = []uint32{item.Index, item.Index}
				}
			}
			return true
		})
		_ = saveSkinStorage(store, account, charID, st)
	}
	return st, changed
}

func saveSkinStorage(store *storage.Store, account, charID int64, st persistedSkinStorage) error {
	if store == nil || charID == 0 {
		return nil
	}
	return store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put(skinStorageTable, skinStorageKey(account, charID), st)
	})
}

func addOwnedSkin(st *persistedSkinStorage, cat byte, skinID uint32) bool {
	if skinID == 0 {
		return false
	}
	if st.Owned == nil {
		st.Owned = map[byte][]uint32{}
	}
	for _, existing := range st.Owned[cat] {
		if existing == skinID {
			return false
		}
	}
	list := append(st.Owned[cat], skinID)
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	st.Owned[cat] = list
	return true
}

func skinStoragePacketsOnEntry(store *storage.Store, boosters *catalog.BoosterCatalog, account, charID int64) []outboundPacket {
	if store == nil || charID == 0 {
		return nil
	}
	st, _ := loadSkinStorage(store, boosters, account, charID)
	var out []outboundPacket
	cats := []byte{
		protocol.SkinCatDamageFont,
		protocol.SkinCatSkillCutscene,
		protocol.SkinCatPartyFrame,
		protocol.SkinCatEmoticon,
		protocol.SkinCatWeapon,
		protocol.SkinCatPartyRequestFrame,
	}
	for _, cat := range cats {
		owned := st.Owned[cat]
		eq := st.Equipped[cat]
		if len(owned) > 0 {
			out = append(out, outboundPacket{
				"skin_storage_list_restored", 0, protocol.NotiSkinStorageList,
				protocol.SkinStorageOwnedList(cat, owned, nil),
			})
		}
		if len(eq) > 0 {
			out = append(out, outboundPacket{
				"skin_storage_equip_restored", 0, protocol.NotiSkinStorageEquip,
				protocol.SkinStorageEquipped(cat, eq),
			})
		}
	}
	return out
}

func (w *worldSession) unlockSkinStorageItem(template uint32) []outboundPacket {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.loot == nil || w.loot.Boosters == nil || w.role.ID == 0 {
		return nil
	}
	item, ok := w.loot.Boosters.SkinItems[template]
	if !ok || item.Index == 0 {
		return nil
	}
	st, _ := loadSkinStorage(w.characters.Store, w.loot.Boosters, w.account, w.role.ID)
	addOwnedSkin(&st, item.Category, item.Index)
	// For Damage Font, auto-equip as default normal+cumulative font when none is equipped yet
	// so the player sees it take effect immediately upon using the item.
	if item.Category == protocol.SkinCatDamageFont && len(st.Equipped[item.Category]) == 0 {
		st.Equipped[item.Category] = []uint32{item.Index, item.Index}
	}
	_ = saveSkinStorage(w.characters.Store, w.account, w.role.ID, st)
	out := []outboundPacket{
		{
			"skin_storage_list_updated", 0, protocol.NotiSkinStorageList,
			protocol.SkinStorageOwnedList(item.Category, st.Owned[item.Category], nil),
		},
	}
	if len(st.Equipped[item.Category]) > 0 {
		out = append(out, outboundPacket{
			"skin_storage_equip_updated", 0, protocol.NotiSkinStorageEquip,
			protocol.SkinStorageEquipped(item.Category, st.Equipped[item.Category]),
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
	if catU32 > 6 {
		return nil, fmt.Errorf("unsupported skin storage category %d", catU32)
	}
	cat := byte(catU32)
	var boosters *catalog.BoosterCatalog
	if w.loot != nil {
		boosters = w.loot.Boosters
	}
	st, _ := loadSkinStorage(w.characters.Store, boosters, w.account, w.role.ID)
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
	default:
		active = []uint32{slots[0]}
	}
	st.Equipped[cat] = active
	if err := saveSkinStorage(w.characters.Store, w.account, w.role.ID, st); err != nil {
		return nil, err
	}
	return []outboundPacket{
		{"skin_storage_equip_ack", 1, protocol.CmdSkinStorageEquip, protocol.SkinStorageEquipSuccess(catU32, slots)},
		{"skin_storage_equip_updated", 0, protocol.NotiSkinStorageEquip, protocol.SkinStorageEquipped(cat, active)},
	}, nil
}
