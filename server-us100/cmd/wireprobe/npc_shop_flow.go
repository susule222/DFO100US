package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

//go:embed npcshops_us100.json.gz
var npcShopsCatalogGZ []byte

const npcShopLimitsTable = "npc_shop_limits_v224"

type npcShopMaterialCost struct {
	ID    uint32 `json:"id"`
	Count uint32 `json:"c"`
}

type npcShopPurchaseLimit struct {
	Scope  string `json:"s"` // "account" or "character"
	Period string `json:"p"` // "accumulate", "weekly", "daily"
	Max    uint32 `json:"m"`
}

type npcShopItemEntry struct {
	Kind         string                `json:"k,omitempty"` // "s"=stackable, "e"=equipment
	StackType    string                `json:"t,omitempty"`
	StackLimit   uint32                `json:"sl,omitempty"`
	Gold         uint32                `json:"g,omitempty"`
	Materials    []npcShopMaterialCost `json:"mat,omitempty"`
	VarNeedCount uint32                `json:"vc,omitempty"`
	VarMaterials []npcShopMaterialCost `json:"vmat,omitempty"`
	Limit        *npcShopPurchaseLimit `json:"lim,omitempty"`
}

type npcShopEntry struct {
	NPC   uint32   `json:"npc"`
	Items []uint32 `json:"items"`
}

type npcShopCatalog struct {
	Source     string                      `json:"source"`
	Shops      map[uint32]npcShopEntry     `json:"shops"`
	Items      map[uint32]npcShopItemEntry `json:"items"`
	npcToShops map[uint32][]uint32
}

var (
	npcShopCatalogOnce sync.Once
	npcShopCatalogData *npcShopCatalog
)

func loadNPCShopCatalog() *npcShopCatalog {
	npcShopCatalogOnce.Do(func() {
		r, err := gzip.NewReader(bytes.NewReader(npcShopsCatalogGZ))
		if err != nil {
			panic(fmt.Errorf("open npcshops_us100.json.gz: %w", err))
		}
		defer r.Close()
		var cat npcShopCatalog
		if err := json.NewDecoder(r).Decode(&cat); err != nil {
			panic(fmt.Errorf("decode npcshops_us100.json.gz: %w", err))
		}
		cat.npcToShops = make(map[uint32][]uint32, len(cat.Shops))
		for sid, sh := range cat.Shops {
			if sh.NPC != 0 {
				cat.npcToShops[sh.NPC] = append(cat.npcToShops[sh.NPC], sid)
			}
		}
		for npc := range cat.npcToShops {
			sort.Slice(cat.npcToShops[npc], func(i, j int) bool {
				return cat.npcToShops[npc][i] < cat.npcToShops[npc][j]
			})
		}
		npcShopCatalogData = &cat
	})
	return npcShopCatalogData
}

func (c *npcShopCatalog) lookupItem(shopID, npcID, template uint32) (npcShopItemEntry, bool) {
	if c == nil || template == 0 {
		return npcShopItemEntry{}, false
	}
	inShop := func(sid uint32) bool {
		sh, ok := c.Shops[sid]
		if !ok {
			return false
		}
		for _, id := range sh.Items {
			if id == template {
				return true
			}
		}
		return false
	}
	foundInShop := false
	if shopID != 0 && inShop(shopID) {
		foundInShop = true
	}
	if !foundInShop && npcID != 0 {
		for _, sid := range c.npcToShops[npcID] {
			if inShop(sid) {
				foundInShop = true
				break
			}
		}
	}
	if !foundInShop {
		return npcShopItemEntry{}, false
	}
	it, ok := c.Items[template]
	return it, ok
}

type persistedNPCShopLimits struct {
	Accum  map[uint32]uint32            `json:"accum,omitempty"`
	Daily  map[string]map[uint32]uint32 `json:"daily,omitempty"`
	Weekly map[string]map[uint32]uint32 `json:"weekly,omitempty"`
}

func npcShopLimitKey(scope string, account, charID int64) string {
	if scope == "character" {
		return fmt.Sprintf("char:%d", charID)
	}
	return fmt.Sprintf("account:%d", account)
}

func (w *worldSession) loadNPCShopLimits(scope string) persistedNPCShopLimits {
	var st persistedNPCShopLimits
	if w == nil || w.characters == nil || w.characters.Store == nil {
		return st
	}
	_, _ = w.characters.Store.Database().Get(npcShopLimitsTable, npcShopLimitKey(scope, w.account, w.role.ID), &st)
	if st.Accum == nil {
		st.Accum = make(map[uint32]uint32)
	}
	if st.Daily == nil {
		st.Daily = make(map[string]map[uint32]uint32)
	}
	if st.Weekly == nil {
		st.Weekly = make(map[string]map[uint32]uint32)
	}
	return st
}

func (w *worldSession) saveNPCShopLimits(scope string, st persistedNPCShopLimits) error {
	if w == nil || w.characters == nil || w.characters.Store == nil {
		return nil
	}
	return w.characters.Store.Database().Put(npcShopLimitsTable, npcShopLimitKey(scope, w.account, w.role.ID), st)
}

func (st *persistedNPCShopLimits) currentCount(template uint32, period, dayKey, weekKey string) uint32 {
	switch period {
	case "daily":
		if m := st.Daily[dayKey]; m != nil {
			return m[template]
		}
		return 0
	case "weekly":
		if m := st.Weekly[weekKey]; m != nil {
			return m[template]
		}
		return 0
	default:
		return st.Accum[template]
	}
}

func (st *persistedNPCShopLimits) addCount(template, delta uint32, period, dayKey, weekKey string) {
	switch period {
	case "daily":
		if st.Daily[dayKey] == nil {
			st.Daily[dayKey] = make(map[uint32]uint32)
		}
		st.Daily[dayKey][template] += delta
	case "weekly":
		if st.Weekly[weekKey] == nil {
			st.Weekly[weekKey] = make(map[uint32]uint32)
		}
		st.Weekly[weekKey][template] += delta
	default:
		st.Accum[template] += delta
	}
}

func (w *worldSession) buildNPCShopLimitsPayload(npcID uint32, now time.Time) []byte {
	cat := loadNPCShopCatalog()
	dayKey := w.activityDay(now)
	weekKey := w.eventPolicy().WeeklyCycleKey(now)
	charSt := w.loadNPCShopLimits("character")
	acctSt := w.loadNPCShopLimits("account")

	// Collect all limited items sold by this NPC (or all tracked limits if NPC has no catalog shops).
	itemSet := make(map[uint32]bool)
	for _, sid := range cat.npcToShops[npcID] {
		for _, id := range cat.Shops[sid].Items {
			itemSet[id] = true
		}
	}
	collect := func(src map[uint32]uint32) []protocol.NPCShopLimitCount {
		var out []protocol.NPCShopLimitCount
		for id, cnt := range src {
			if cnt == 0 {
				continue
			}
			if len(itemSet) > 0 && !itemSet[id] {
				continue
			}
			out = append(out, protocol.NPCShopLimitCount{Template: id, Count: cnt})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Template < out[j].Template })
		return out
	}
	mergePeriodic := func(st persistedNPCShopLimits) []protocol.NPCShopLimitCount {
		merged := make(map[uint32]uint32)
		for id, cnt := range st.Daily[dayKey] {
			if cnt > 0 {
				merged[id] = cnt
			}
		}
		for id, cnt := range st.Weekly[weekKey] {
			if cnt > 0 {
				merged[id] = cnt
			}
		}
		return collect(merged)
	}
	return protocol.NPCShopLimitsSuccess(
		collect(charSt.Accum),
		collect(acctSt.Accum),
		mergePeriodic(charSt),
		mergePeriodic(acctSt),
	)
}

func (w *worldSession) handleNPCShopLimits(plaintext []byte, now time.Time) ([]outboundPacket, uint32, error) {
	npcID, err := protocol.DecodeNPCShopLimitsRequest(plaintext)
	if err != nil {
		return nil, 0, err
	}
	return []outboundPacket{
		{Name: "npc_shop_limits_ack", Kind: 1, ID: protocol.CmdNPCShopLimits, Payload: w.buildNPCShopLimitsPayload(npcID, now)},
	}, npcID, nil
}

func countMaterialInBag(bag inventory.Bag, template uint32) uint32 {
	var total uint64
	for _, it := range bag.Items {
		if it.Template == template {
			total += uint64(it.Amount)
		}
	}
	if total > 0xffffffff {
		return 0xffffffff
	}
	return uint32(total)
}

func deductMaterialFromBag(bag inventory.Bag, template, need uint32) (inventory.Bag, uint32, bool) {
	if need == 0 {
		return bag, countMaterialInBag(bag, template), true
	}
	if countMaterialInBag(bag, template) < need {
		return bag, 0, false
	}
	next := make([]inventory.BagItem, 0, len(bag.Items))
	remNeed := need
	var remaining uint32
	for _, it := range bag.Items {
		if it.Template == template && remNeed > 0 {
			if it.Amount <= remNeed {
				remNeed -= it.Amount
				continue
			}
			it.Amount -= remNeed
			remNeed = 0
		}
		if it.Template == template {
			remaining += it.Amount
		}
		next = append(next, it)
	}
	bag.Items = next
	return bag, remaining, true
}

func shopCatalogLootEntry(base catalog.LootCatalog, rules inventory.BagRules, id uint32, entry npcShopItemEntry, qty uint32) catalog.LootItem {
	resolveType := func(t string) string {
		if t != "" {
			if _, ok := rules.Slots[t]; ok {
				return t
			}
			if _, ok := rules.SlotAliases[t]; ok {
				return t
			}
		}
		return "[throw]"
	}
	if it, ok := base.Items[id]; ok {
		if it.StackLimit == 0 && entry.StackLimit > 0 {
			it.StackLimit = entry.StackLimit
		}
		if it.StackableType == "" {
			it.StackableType = entry.StackType
		}
		it.StackableType = resolveType(it.StackableType)
		return it
	}
	st := resolveType(entry.StackType)
	sl := entry.StackLimit
	if sl == 0 {
		sl = 1000
		if entry.StackType == "[booster]" || entry.StackType == "[booster selection]" {
			sl = 100
		}
	}
	if sl < qty {
		sl = qty
	}
	return catalog.LootItem{
		ID:            id,
		Kind:          "stackable",
		StackableType: st,
		StackLimit:    sl,
	}
}

func findRowBySlot(rows [][protocol.CurrentItemRecordSize]byte, slot uint16) ([protocol.CurrentItemRecordSize]byte, bool) {
	for _, r := range rows {
		if binary.LittleEndian.Uint16(r[0:2]) == slot {
			return r, true
		}
	}
	return [protocol.CurrentItemRecordSize]byte{}, false
}

func (w *worldSession) handleNPCShopBuy(ctx context.Context, wear *inventory.WearService, rawFrame, plaintext []byte, now time.Time) ([]outboundPacket, uint16, error) {
	if w == nil || w.characters == nil || w.loot == nil || w.role.ID == 0 {
		return nil, 9, fmt.Errorf("world session not ready for npc shop buy")
	}
	req, err := protocol.DecodeNPCShopBuyRequest(plaintext)
	if err != nil {
		return nil, 9, err
	}
	if req.ItemTemplate == 0 || req.Quantity == 0 || req.Quantity > 1000 {
		return nil, 9, fmt.Errorf("invalid npc shop buy request template=%d qty=%d", req.ItemTemplate, req.Quantity)
	}

	cat := loadNPCShopCatalog()
	entry, ok := cat.lookupItem(req.ShopID, req.NPCID, req.ItemTemplate)
	if !ok {
		return nil, 9, fmt.Errorf("item %d not found in shop %d (npc %d)", req.ItemTemplate, req.ShopID, req.NPCID)
	}

	dayKey := w.activityDay(now)
	weekKey := w.eventPolicy().WeeklyCycleKey(now)
	if entry.Limit != nil && entry.Limit.Max > 0 {
		st := w.loadNPCShopLimits(entry.Limit.Scope)
		bought := st.currentCount(req.ItemTemplate, entry.Limit.Period, dayKey, weekKey)
		if uint64(bought)+uint64(req.Quantity) > uint64(entry.Limit.Max) {
			return nil, 14, fmt.Errorf("purchase limit reached for %d (%d+%d > %d)", req.ItemTemplate, bought, req.Quantity, entry.Limit.Max)
		}
	}

	totalGold64 := uint64(entry.Gold) * uint64(req.Quantity)
	if totalGold64 > 0xffffffff {
		return nil, 4, fmt.Errorf("gold cost overflow")
	}
	totalGold := uint32(totalGold64)

	eventKey := fmt.Sprintf("npc_buy/%d/%x", w.role.ID, sha256.Sum256(rawFrame))
	var (
		refuseCode  uint16
		boughtSpace byte
		boughtSlot  uint16
		matUpdates  []protocol.NPCShopMaterialUpdate
		afterGold   uint32
		afterRows   [][protocol.CurrentItemRecordSize]byte
		boughtRow   [protocol.CurrentItemRecordSize]byte
		spaceRows   [][protocol.CurrentItemRecordSize]byte
	)

	saved, applied, err := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, eventKey, "us100-npc-shop-buy-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		bag, err := inventory.ReadBag(current.State)
		if err != nil {
			refuseCode = 9
			return nil, nil, err
		}
		if bag.Gold < totalGold {
			refuseCode = 4
			return nil, nil, fmt.Errorf("insufficient gold: have %d need %d", bag.Gold, totalGold)
		}
		bag.Gold -= totalGold

		matUpdates = matUpdates[:0]
		// 1. Deduct fixed [need material] requirements.
		for _, m := range entry.Materials {
			need64 := uint64(m.Count) * uint64(req.Quantity)
			if need64 > 0xffffffff {
				refuseCode = 4
				return nil, nil, fmt.Errorf("material cost overflow")
			}
			var rem uint32
			var ok bool
			bag, rem, ok = deductMaterialFromBag(bag, m.ID, uint32(need64))
			if !ok {
				refuseCode = 4
				return nil, nil, fmt.Errorf("insufficient material %d: need %d", m.ID, need64)
			}
			matUpdates = append(matUpdates, protocol.NPCShopMaterialUpdate{Template: m.ID, Remaining: rem})
		}

		// 2. Deduct [variable need material] (any listed alternative currency with enough balance).
		if len(entry.VarMaterials) > 0 {
			satisfied := false
			for _, vm := range entry.VarMaterials {
				unit := vm.Count
				if unit == 0 {
					unit = entry.VarNeedCount
				}
				if unit == 0 {
					unit = 1
				}
				need64 := uint64(unit) * uint64(req.Quantity)
				if need64 > 0xffffffff {
					continue
				}
				if countMaterialInBag(bag, vm.ID) >= uint32(need64) {
					var rem uint32
					bag, rem, _ = deductMaterialFromBag(bag, vm.ID, uint32(need64))
					matUpdates = append(matUpdates, protocol.NPCShopMaterialUpdate{Template: vm.ID, Remaining: rem})
					satisfied = true
					break
				}
			}
			if !satisfied {
				refuseCode = 4
				return nil, nil, fmt.Errorf("insufficient variable material for item %d", req.ItemTemplate)
			}
		}

		// 3. Deliver purchased item(s) to their authentic inventory space & tab.
		repeatIDs := func(id, qty uint32) []uint32 {
			ids := make([]uint32, qty)
			for i := range ids {
				ids[i] = id
			}
			return ids
		}
		switch {
		case inventory.IsCreatureTemplate(req.ItemTemplate):
			var added []uint16
			bag, added, err = bag.AddCreatures(repeatIDs(req.ItemTemplate, req.Quantity))
			if err != nil || len(added) == 0 {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought creature %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.CreatureSpace, added[len(added)-1]
			spaceRows = bag.CreatureRows()
		case inventory.IsCreatureArtifactTemplate(req.ItemTemplate):
			var added []uint16
			bag, added, err = bag.AddCreatureArtifacts(repeatIDs(req.ItemTemplate, req.Quantity))
			if err != nil || len(added) == 0 {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought creature artifact %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.CreatureSpace, added[len(added)-1]
			spaceRows = bag.CreatureRows()
		case inventory.IsCreatureStackableTemplate(req.ItemTemplate):
			var slot uint16
			bag, slot, err = bag.AddCreatureItems(req.ItemTemplate, req.Quantity)
			if err != nil {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought creature item %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.CreatureSpace, slot
			spaceRows = bag.CreatureRows()
		case inventory.IsInsigniaTemplate(req.ItemTemplate):
			var added []uint16
			bag, added, err = bag.AddInsignias(repeatIDs(req.ItemTemplate, req.Quantity))
			if err != nil || len(added) == 0 {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought insignia %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.InsigniaSpace, added[len(added)-1]
			spaceRows = bag.InsigniaRows()
		case inventory.IsFlagGemTemplate(req.ItemTemplate):
			var slot uint16
			bag, slot, err = bag.AddGems(req.ItemTemplate, req.Quantity)
			if err != nil {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought flag gem %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.InsigniaSpace, slot
			spaceRows = bag.InsigniaRows()
		case inventory.IsTalismanTemplate(req.ItemTemplate):
			var added []uint16
			bag, added, err = bag.AddTalismans(repeatIDs(req.ItemTemplate, req.Quantity))
			if err != nil || len(added) == 0 {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought talisman %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.TalismanSpace, added[len(added)-1]
			spaceRows = bag.TalismanRows()
		case inventory.IsRuneTemplate(req.ItemTemplate):
			var added []uint16
			bag, added, err = bag.AddRunes(req.ItemTemplate, req.Quantity)
			if err != nil || len(added) == 0 {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought rune %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = protocol.TalismanSpace, added[len(added)-1]
			spaceRows = bag.TalismanRows()
		case entry.Kind == "e":
			equipSlots := w.loot.BagRules.EquipmentSlots
			if equipSlots[0] == 0 {
				equipSlots = [2]uint16{9, 64}
			}
			var added []uint16
			if wear != nil && wear.Catalog != nil {
				bag, added, err = bag.AddEquipment(wear.Catalog, equipSlots, req.ItemTemplate, req.Quantity)
			} else {
				bag, added, err = bag.AddRawEquipment(equipSlots, req.ItemTemplate, 35, req.Quantity)
			}
			if err != nil || len(added) == 0 {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought equipment %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = 0, added[len(added)-1]
		default:
			local := catalog.LootCatalog{
				Source: w.loot.Catalog.Source,
				Items: map[uint32]catalog.LootItem{
					req.ItemTemplate: shopCatalogLootEntry(w.loot.Catalog, w.loot.BagRules, req.ItemTemplate, entry, req.Quantity),
				},
			}
			var slot uint16
			bag, slot, err = bag.Add(local, w.loot.BagRules, req.ItemTemplate, req.Quantity)
			if err != nil {
				refuseCode = 10
				return nil, nil, fmt.Errorf("add bought stackable %d: %v", req.ItemTemplate, err)
			}
			boughtSpace, boughtSlot = 0, slot
		}

		afterGold = bag.Gold
		afterRows = bag.Rows()
		if boughtSpace == 0 {
			boughtRow, _ = findRowBySlot(afterRows, boughtSlot)
		} else {
			boughtRow, _ = findRowBySlot(spaceRows, boughtSlot)
		}
		nextState, err := inventory.SaveBag(current.State, bag)
		if err != nil {
			refuseCode = 9
			return nil, nil, err
		}
		receipt, err := json.Marshal(map[string]any{
			"shop_id": req.ShopID,
			"npc_id":  req.NPCID,
			"item":    req.ItemTemplate,
			"qty":     req.Quantity,
			"gold":    totalGold,
			"slot":    boughtSlot,
		})
		return nextState, receipt, err
	})
	if err != nil {
		if refuseCode == 0 {
			refuseCode = 9
		}
		return nil, refuseCode, err
	}
	if !applied {
		return nil, 9, fmt.Errorf("duplicate npc shop buy event")
	}

	saved.WireID = w.role.WireID
	w.role = saved

	if entry.Limit != nil && entry.Limit.Max > 0 {
		st := w.loadNPCShopLimits(entry.Limit.Scope)
		st.addCount(req.ItemTemplate, req.Quantity, entry.Limit.Period, dayKey, weekKey)
		_ = w.saveNPCShopLimits(entry.Limit.Scope, st)
	}

	if binary.LittleEndian.Uint32(boughtRow[2:6]) == 0 {
		return nil, 9, fmt.Errorf("bought item slot %d missing from updated bag", boughtSlot)
	}
	ackPayload, err := protocol.NPCShopBuySuccess(afterGold, 0, 0, 0, boughtRow, matUpdates)
	if err != nil {
		return nil, 9, err
	}
	out := []outboundPacket{
		{Name: "npc_shop_buy_ack", Kind: 1, ID: protocol.CmdNPCShopBuy, Payload: ackPayload},
	}
	if upd, err := protocol.US224InventoryUpdate(afterRows); err == nil {
		out = append(out, outboundPacket{Name: "npc_shop_inventory_sync", Kind: 0, ID: 14, Payload: upd})
	}
	if boughtSpace != 0 && len(spaceRows) > 0 {
		if boughtSpace == protocol.CreatureSpace {
			if ciPayload, cerr := inventory.CreatureInfoPayload(saved.State); cerr == nil && len(ciPayload) > 0 {
				out = append(out, outboundPacket{Name: "npc_shop_creature_info_sync", Kind: 0, ID: protocol.NotiCreatureInfo, Payload: ciPayload})
			}
		}
		if upd, err := protocol.US224InventorySpaceUpdate(boughtSpace, spaceRows); err == nil {
			out = append(out, outboundPacket{Name: "npc_shop_space_sync", Kind: 0, ID: 14, Payload: upd})
		}
	}
	if entry.Limit != nil && entry.Limit.Max > 0 {
		out = append(out, outboundPacket{
			Name:    "npc_shop_limits_sync",
			Kind:    1,
			ID:      protocol.CmdNPCShopLimits,
			Payload: w.buildNPCShopLimitsPayload(req.NPCID, now),
		})
	}
	return out, 0, nil
}
