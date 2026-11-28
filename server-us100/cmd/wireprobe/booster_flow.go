package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"
)

// directContractItem maps contract items (both DANJI contract boosters
// 590006272..590006277 and raw contract stackables from etc/premiumlist_new.etc)
// to their (premium kind, duration in days) so they activate directly on the
// account instead of sitting unused in the bag.
func directContractItem(tpl uint32) (kind byte, days int64, ok bool) {
	switch tpl {
	// DANJI contract boosters (Live/Else/Univ/2022/0712_DANJI)
	case 590006272:
		return storage.ConquerorContractKind, 7, true
	case 590006273:
		return storage.TacticianContractKind, 7, true
	case 590006274:
		return storage.GrowthContractKind, 7, true
	case 590006275:
		return storage.ConquerorContractKind, 15, true
	case 590006276:
		return storage.TacticianContractKind, 15, true
	case 590006277:
		return storage.GrowthContractKind, 15, true

	// Conqueror's Contract (kind 22, etc/premiumlist_new.etc)
	case 30, 2660704:
		return storage.ConquerorContractKind, 1, true
	case 741:
		return storage.ConquerorContractKind, 2, true
	case 31:
		return storage.ConquerorContractKind, 3, true
	case 32:
		return storage.ConquerorContractKind, 5, true
	case 33:
		return storage.ConquerorContractKind, 7, true
	case 2660050:
		return storage.ConquerorContractKind, 10, true
	case 34:
		return storage.ConquerorContractKind, 15, true
	case 2660012:
		return storage.ConquerorContractKind, 30, true

	// Tactician's Contract (kind 27)
	case 43, 2660705:
		return storage.TacticianContractKind, 1, true
	case 742:
		return storage.TacticianContractKind, 2, true
	case 44:
		return storage.TacticianContractKind, 3, true
	case 200:
		return storage.TacticianContractKind, 5, true
	case 45:
		return storage.TacticianContractKind, 7, true
	case 2660051:
		return storage.TacticianContractKind, 10, true
	case 46:
		return storage.TacticianContractKind, 15, true
	case 2660013:
		return storage.TacticianContractKind, 30, true

	// Growth's Contract (kind 79)
	case 2660703:
		return storage.GrowthContractKind, 1, true
	case 2660409:
		return storage.GrowthContractKind, 3, true
	case 2660410:
		return storage.GrowthContractKind, 7, true
	case 2660411:
		return storage.GrowthContractKind, 15, true
	case 10327725:
		return storage.GrowthContractKind, 30, true

	// Cube's Contract (kind 92)
	case 10000391:
		return storage.CubeContractKind, 1, true
	case 10000388:
		return storage.CubeContractKind, 3, true
	case 10000389:
		return storage.CubeContractKind, 7, true
	case 10000390:
		return storage.CubeContractKind, 15, true
	case 10327726:
		return storage.CubeContractKind, 30, true

	// Gabriel's Contract (kind 73)
	case 10157955:
		return gabrielContractKind, 1, true
	case 10092497:
		return gabrielContractKind, 5, true
	case 590004603, 590004900:
		return gabrielContractKind, 7, true
	case 10151653, 590004901:
		return gabrielContractKind, 15, true
	case 2660354, 590004902:
		return gabrielContractKind, 30, true

	// Unstable Rift Contract (kind 125)
	case 590004687:
		return unstableRiftContractKind, 7, true
	case 590004689:
		return unstableRiftContractKind, 15, true
	case 590004691:
		return unstableRiftContractKind, 30, true

	// Neo Premium Plus (kind 118)
	case 50002533:
		return storage.NeoPremiumPlusKind, 3, true
	case 50002534:
		return storage.NeoPremiumPlusKind, 7, true
	case 50002535:
		return storage.NeoPremiumPlusKind, 15, true
	case 50002918:
		return storage.NeoPremiumPlusKind, 30, true

	// VIP Premium Contract (kind 120)
	case 50042664:
		return storage.VIPContractKind, 7, true
	case 50042665:
		return storage.VIPContractKind, 14, true
	}
	return 0, 0, false
}

// stackableSlotType resolves the US100 bag tab for stackables not listed in
// loot.generated.json (verified against PVF stackable/*.stk [stackable type]).
func stackableSlotType(src catalog.LootCatalog, tpl uint32) string {
	if existing, ok := src.Items[tpl]; ok && existing.Kind == "stackable" && existing.StackableType != "" {
		return existing.StackableType
	}
	if inventory.IsEmblemTemplate(tpl) {
		return inventory.EmblemBagCategory
	}
	switch tpl {
	case 10327281, 10327282, 10327283, 10327286, 590006231, 590006232:
		return "[material]"
	case 590006264:
		return "[material expert job]"
	}
	return "[throw]"
}

func localCatalogEntry(src catalog.LootCatalog, tpl, qty uint32) catalog.LootItem {
	limit := uint32(1000)
	if qty > limit {
		limit = qty
	}
	if existing, ok := src.Items[tpl]; ok && existing.Kind == "stackable" {
		if existing.StackLimit < limit {
			existing.StackLimit = limit
		}
		return existing
	}
	return catalog.LootItem{
		ID:            tpl,
		Kind:          "stackable",
		StackableType: stackableSlotType(src, tpl),
		StackLimit:    limit,
	}
}

func localCatalogEntryWithHint(src catalog.LootCatalog, r inventory.BagRules, tpl, qty uint32, hintType string) catalog.LootItem {
	item := localCatalogEntry(src, tpl, qty)
	if safe := resolveBagTab(r, item.StackableType); safe != "" && src.Items[tpl].Kind == "stackable" {
		return item
	}
	st := resolveBagTab(r, hintType)
	if st == "" {
		st = resolveBagTab(r, item.StackableType)
	}
	if st == "" {
		st = "[throw]"
	}
	item.StackableType = st
	return item
}

func resolveBagTab(r inventory.BagRules, st string) string {
	if st == "" {
		return ""
	}
	if _, ok := r.Slots[st]; ok {
		return st
	}
	if alias, ok := r.SlotAliases[st]; ok {
		if _, ok2 := r.Slots[alias]; ok2 {
			return st
		}
	}
	return ""
}

type boosterEntry struct {
	Template uint32
	Count    uint32
	Weight   uint32
}

// Source-backed DANJI [booster] reward tables from US100 PVF stackable/590006001/*.stk.
var danjiBoosterTables = func() map[uint32][]boosterEntry {
	emblems := make([]boosterEntry, 0, 197)
	for id := uint32(590004703); id <= 590004899; id++ {
		emblems = append(emblems, boosterEntry{Template: id, Count: 1, Weight: 1000})
	}
	return map[uint32][]boosterEntry{
		590006229: {{15, 10, 1000}},       // Kaleido Box (10)
		590006267: {{15, 2, 1000}},        // Kaleido Box (2)
		590006268: {{15, 4, 1000}},        // Kaleido Box (4)
		590006262: {{10327283, 100, 1000}},
		590006263: {{10327283, 500, 1000}},
		590006266: {{10327286, 100, 1000}},
		590006269: {{10000844, 4, 1000}},  // Echo Megaphone (4)
		590006270: {{10000844, 10, 1000}}, // Echo Megaphone (10)
		590006271: {{10000844, 20, 1000}}, // Echo Megaphone (20)
		590006302: {{10327281, 50, 1000}}, // Ryan Core (50)
		590006303: {{10327281, 100, 1000}},
		590006304: {{10327282, 50, 1000}}, // Harmonious Crystal (50)
		590006305: {{10327282, 100, 1000}},
		590704455: {{590704456, 20, 1000}},
		590006265: emblems, // Random Emblem Box (197 emblems)
		590006233: {
			{590006234, 1, 10000}, {590006235, 1, 1000}, {590006236, 1, 500}, {590006237, 1, 100},
			{590006238, 1, 10000}, {590006239, 1, 1000}, {590006240, 1, 500}, {590006241, 1, 100},
			{590006242, 1, 1000}, {590006243, 1, 500}, {590006244, 1, 100}, {590006245, 1, 50}, {590006246, 1, 10},
		},
		590006247: {
			{590006248, 1, 10000}, {590006249, 1, 1000}, {590006250, 1, 500}, {590006251, 1, 100},
			{590006252, 1, 10000}, {590006253, 1, 1000}, {590006254, 1, 500}, {590006255, 1, 100},
			{590006256, 1, 1000}, {590006257, 1, 500}, {590006258, 1, 100}, {590006259, 1, 50}, {590006260, 1, 10},
		},
		590006278: {
			{590006279, 1, 10000}, {590006280, 1, 1000}, {590006281, 1, 500}, {590006282, 1, 100},
			{590006283, 1, 10000}, {590006284, 1, 1000}, {590006285, 1, 500}, {590006286, 1, 100},
			{590006287, 1, 1000}, {590006288, 1, 500}, {590006289, 1, 100},
		},
		590006290: {
			{590006291, 1, 10000}, {590006292, 1, 1000}, {590006293, 1, 500}, {590006294, 1, 100},
			{590006295, 1, 10000}, {590006296, 1, 1000}, {590006297, 1, 500}, {590006298, 1, 100},
			{590006299, 1, 1000}, {590006300, 1, 500}, {590006301, 1, 100},
		},
	}
}()

// Source-backed DANJI [booster selection] boxes from US100 PVF.
var danjiSelectionTables = map[uint32]map[uint32]uint32{
	590006306: {
		590006307: 1, 590006308: 1, 590006309: 1, 590006310: 1,
		590006311: 1, 590006312: 1, 590006313: 1, 590006344: 1,
	},
	590006314: {
		590006315: 1, 590006316: 1, 590006317: 1, 590006318: 1,
		590006319: 1, 590006320: 1, 590006345: 1,
	},
	590006322: {
		590006323: 1, 590006324: 1, 590006325: 1, 590006326: 1,
		590006327: 1, 590006346: 1, 590006348: 1,
	},
	590006329: {
		590006330: 1, 590006335: 1, 590006336: 1, 590006339: 1,
		590006340: 1, 590006341: 1, 590006342: 1, 590006343: 1, 590006347: 1,
	},
	590704452: {
		590704454: 2, 590704453: 3,
	},
}

func pickBoosterEntry(pool []boosterEntry, nextRand func(uint32) uint32) boosterEntry {
	var total uint32
	for _, it := range pool {
		total += it.Weight
	}
	if total == 0 {
		return pool[0]
	}
	r := nextRand(total)
	var acc uint32
	for _, it := range pool {
		acc += it.Weight
		if r < acc {
			return it
		}
	}
	return pool[len(pool)-1]
}

// buildContractRefreshPackets returns NOTI 66 (and NOTI 36 if Neo Plus changed)
// for every active contract kind in changed.
func buildContractRefreshPackets(ctx context.Context, store *storage.Store, fatigue *character.FatigueService, account, charID int64, changed map[byte]bool) ([]outboundPacket, error) {
	if store == nil || len(changed) == 0 {
		return nil, nil
	}
	var out []outboundPacket
	entries, err := store.NeoPremiumSeconds(account)
	if err != nil {
		return nil, err
	}
	for _, v := range entries {
		if changed[v.Kind] {
			msg := binary.LittleEndian.AppendUint64([]byte{2, 0, v.Kind}, v.Seconds)
			out = append(out, outboundPacket{"contract_premium_updated", 0, 66, msg})
		}
	}
	if changed[storage.NeoPremiumPlusKind] && fatigue != nil && charID > 0 {
		if fp, ferr := fatigue.State(ctx, account, charID, time.Now()); ferr == nil {
			if fpBody, e := protocol.Fatigue(fp.Used, fp.Limit, fp.UsedMax); e == nil {
				out = append(out, outboundPacket{"contract_fatigue_updated", 0, 36, fpBody})
			}
		}
	}
	return out, nil
}

// openBoosterBox handles CMD 160 for Neo boxes (590005791, 590005785, 50002547),
// all DANJI [booster] and [booster selection] boxes, and bagged contract boosters.
func openBoosterBox(
	ctx context.Context,
	store *storage.Store,
	fatigue *character.FatigueService,
	lootSvc *loot.Service,
	account int64,
	role storage.Character,
	rawFrame []byte,
	plaintext []byte,
	nextRand func(uint32) uint32,
) (storage.Character, []outboundPacket, uint32, uint32, uint32, bool, error) {
	if nextRand == nil {
		nextRand = cryptoRandUint32
	}
	var openCount uint16
	var isChoice bool
	var category uint32
	var chosen uint32
	var chosenList []uint32
	// US100 CMD 160 wire layout (verified at 0x144003db2 / 0x14198abe5):
	//   [0..1] u16 slot, [2..5] u32 openCount (1..100), [6] u8 catDim0, [7] u8 catDim1,
	//   [8..]  optional N*u32 chosen template IDs + 2 trailing zero bytes.
	decodeCat := func(p []byte) uint32 {
		if len(p) < 8 {
			return 0
		}
		// Real client packs (catDim0, catDim1) in bytes [6..7] as LE uint16.
		if p[6] != 0 || p[7] != 0 {
			return uint32(binary.LittleEndian.Uint16(p[6:8]))
		}
		// Fallback for legacy test payloads that place u16/u32 category at [4..7].
		if legacy := binary.LittleEndian.Uint16(p[4:6]); legacy != 0 {
			return uint32(legacy)
		}
		return 0
	}
	switch {
	case len(plaintext) == 8:
		openCount = binary.LittleEndian.Uint16(plaintext[2:4])
		category = decodeCat(plaintext)
	case len(plaintext) >= 12 && len(plaintext) <= 128:
		openCount = binary.LittleEndian.Uint16(plaintext[2:4])
		category = decodeCat(plaintext)
		for off := 8; off+4 <= len(plaintext); off += 4 {
			v := binary.LittleEndian.Uint32(plaintext[off : off+4])
			if v != 0 {
				chosenList = append(chosenList, v)
			}
		}
		if len(chosenList) > 0 {
			chosen = chosenList[0]
		}
		isChoice = true
	}
	if openCount == 0 || openCount > 100 {
		return role, nil, 0, 0, 0, false, fmt.Errorf("invalid booster open request")
	}
	slot := binary.LittleEndian.Uint16(plaintext)
	bagNow, err := inventory.ReadBag(role.State)
	if err != nil {
		return role, nil, 0, 0, 0, false, err
	}
	var box uint32
	var have uint32
	for _, it := range bagNow.Items {
		if it.Slot == slot {
			box, have = it.Template, it.Amount
			break
		}
	}
	if box == 0 || have < uint32(openCount) {
		return role, nil, 0, 0, 0, false, fmt.Errorf("slot %d does not hold requested box", slot)
	}

	// Preserve exact existing Neo contract/look/daily gift box flows.
	if box == 590005791 && !isChoice && category == 0 && openCount == 1 {
		award, qty := uint32(590005793), uint32(1)
		grant := neoBoxGrant(lootSvc, slot, box, award, qty)
		key := fmt.Sprintf("neo160/%x", sha256.Sum256(rawFrame))
		saved, applied, err := store.CommitNeoContractBox(ctx, account, role.ID, lootSvc.Catalog.Source.Checksum, key, grant)
		if err != nil {
			return role, nil, box, award, qty, false, err
		}
		return finalizeBoosterPackets(ctx, store, fatigue, account, saved, box, slot, []boosterAward{{Template: award, Quantity: qty}}, applied, map[byte]bool{
			storage.ConquerorContractKind: true,
			storage.TacticianContractKind: true,
			storage.GrowthContractKind:    true,
			storage.CubeContractKind:      true,
		}, false, false)
	}
	if isChoice && openCount == 1 && (box == 590005785 || box == 50002547) {
		qty := uint32(1)
		if box == 50002547 {
			n, ok := dailyGiftQuantity(chosen)
			if !ok {
				return role, nil, box, chosen, 0, false, fmt.Errorf("invalid daily gift selection %d", chosen)
			}
			qty = n
		} else if chosen < 590005794 || chosen > 590005807 {
			return role, nil, box, chosen, 0, false, fmt.Errorf("invalid neo look selection %d", chosen)
		}
		grant := neoBoxGrant(lootSvc, slot, box, chosen, qty)
		key := fmt.Sprintf("neo160/%x", sha256.Sum256(rawFrame))
		model := "US224-neo-look-v1"
		if box == 50002547 {
			model = "US224-neo-daily-open-v1"
		}
		saved, applied, err := store.CommitCharacterEvent(ctx, account, role.ID, lootSvc.Catalog.Source.Checksum, key, model, grant)
		if err != nil {
			return role, nil, box, chosen, qty, false, err
		}
		return finalizeBoosterPackets(ctx, store, fatigue, account, saved, box, slot, []boosterAward{{Template: chosen, Quantity: qty}}, applied, nil, false, false)
	}

	// Bagged contract booster (e.g. 590006272..590006277 opened from bag):
	// consume from bag and activate directly on account without putting contract item in bag.
	if kind, days, isContract := directContractItem(box); isContract && !isChoice {
		totalDays := days * int64(openCount)
		key := fmt.Sprintf("booster160/%x", sha256.Sum256(rawFrame))
		saved, applied, err := store.CommitCharacterEvent(ctx, account, role.ID, lootSvc.Catalog.Source.Checksum, key, "US224-contract-booster-v1", func(cur storage.Character) (json.RawMessage, json.RawMessage, error) {
			b, e := inventory.ReadBag(cur.State)
			if e != nil {
				return nil, nil, e
			}
			if e = deductBagSlot(&b, slot, box, uint32(openCount)); e != nil {
				return nil, nil, e
			}
			state, e := inventory.SaveBag(cur.State, b)
			if e != nil {
				return nil, nil, e
			}
			receipt, e := json.Marshal(map[string]any{"slot": slot, "box": box, "contract_kind": kind, "days": totalDays})
			return state, receipt, e
		})
		if err != nil {
			return role, nil, box, box, uint32(openCount), false, err
		}
		if applied {
			if err = store.ExtendPremiumContracts(ctx, account, map[byte]int64{kind: totalDays}); err != nil {
				return saved, nil, box, box, uint32(openCount), false, err
			}
		}
		display := contractBoosterAward(box, uint32(openCount))
		return finalizeBoosterPackets(ctx, store, fatigue, account, saved, box, slot, []boosterAward{display}, applied, map[byte]bool{kind: true}, false, false)
	}

	// Universal US100 PVF booster/selection/package resolution (41,394+ boxes),
	// with compiled DANJI/Neo tables as fallback when BoosterCatalog is not loaded.
	var awards []boosterAward
	if lootSvc.Boosters != nil {
		if cbox, ok := lootSvc.Boosters.Boxes[box]; ok {
			resolved, rerr := resolveCatalogBoosterBox(cbox, category, chosenList, isChoice, openCount, nextRand)
			if rerr != nil {
				return role, nil, box, chosen, 0, false, fmt.Errorf("box %d: %w", box, rerr)
			}
			awards = resolved
		}
	}
	if len(awards) == 0 {
		if def, ok := danjiAvatarSelectionBoxes[box]; ok {
			resolved, rerr := resolveSelectionBox(def, category, chosen, isChoice, openCount)
			if rerr != nil {
				return role, nil, box, chosen, 0, false, fmt.Errorf("box %d: %w", box, rerr)
			}
			awards = resolved
		} else if fixedMulti, ok := danjiFixedMultiBoosters[box]; ok && !isChoice {
			for _, it := range fixedMulti {
				awards = append(awards, boosterAward{
					Template: it.Template,
					Quantity: it.Quantity * uint32(openCount),
					Avatar:   it.Avatar,
				})
			}
		} else if isChoice {
			opts, ok := danjiSelectionTables[box]
			if !ok {
				return role, nil, box, chosen, 0, false, fmt.Errorf("unsupported selection box %d", box)
			}
			perQty, ok := opts[chosen]
			if !ok {
				return role, nil, box, chosen, 0, false, fmt.Errorf("invalid choice %d for box %d", chosen, box)
			}
			awards = append(awards, boosterAward{Template: chosen, Quantity: perQty * uint32(openCount)})
		} else {
			// DANJI [booster] fixed & weighted random boxes
			pool, ok := danjiBoosterTables[box]
			if !ok {
				return role, nil, box, 0, 0, false, fmt.Errorf("unsupported booster box %d", box)
			}
			totals := map[uint32]uint32{}
			var order []uint32
			for i := uint16(0); i < openCount; i++ {
				pick := pickBoosterEntry(pool, nextRand)
				if _, seen := totals[pick.Template]; !seen {
					order = append(order, pick.Template)
				}
				totals[pick.Template] += pick.Count
			}
			for _, tpl := range order {
				awards = append(awards, boosterAward{Template: tpl, Quantity: totals[tpl]})
			}
		}
	}

	contractDays := map[byte]int64{}
	var avatarIDs []uint32
	var talismanIDs []uint32
	var runeAwards []boosterAward
	var equipAwards []boosterAward
	var bagAwards []boosterAward
	for _, a := range awards {
		if a.Avatar {
			for i := uint32(0); i < a.Quantity; i++ {
				avatarIDs = append(avatarIDs, a.Template)
			}
		} else if a.Talisman || inventory.IsTalismanTemplate(a.Template) {
			for i := uint32(0); i < a.Quantity; i++ {
				talismanIDs = append(talismanIDs, a.Template)
			}
		} else if a.Rune || a.StackableType == "[rune]" {
			runeAwards = append(runeAwards, a)
		} else if a.Equipment {
			equipAwards = append(equipAwards, a)
		} else if kind, days, ok := directContractItem(a.Template); ok {
			contractDays[kind] += days * int64(a.Quantity)
		} else {
			bagAwards = append(bagAwards, a)
		}
	}

	key := fmt.Sprintf("booster160/%x", sha256.Sum256(rawFrame))
	saved, applied, err := store.CommitCharacterEvent(ctx, account, role.ID, lootSvc.Catalog.Source.Checksum, key, "US224-danji-booster-v1", func(cur storage.Character) (json.RawMessage, json.RawMessage, error) {
		b, e := inventory.ReadBag(cur.State)
		if e != nil {
			return nil, nil, e
		}
		if e = deductBagSlot(&b, slot, box, uint32(openCount)); e != nil {
			return nil, nil, e
		}
		if len(avatarIDs) > 0 {
			b, _, e = b.AddAvatars(avatarIDs)
			if e != nil {
				return nil, nil, e
			}
		}
		if len(talismanIDs) > 0 {
			b, _, e = b.AddTalismans(talismanIDs)
			if e != nil {
				return nil, nil, e
			}
		}
		if len(runeAwards) > 0 {
			for _, a := range runeAwards {
				b, _, e = b.AddRunes(a.Template, a.Quantity)
				if e != nil {
					return nil, nil, e
				}
			}
		}
		if len(equipAwards) > 0 {
			eqSlots := lootSvc.BagRules.EquipmentSlots
			if eqSlots[0] == 0 {
				eqSlots = [2]uint16{9, 64}
			}
			for _, a := range equipAwards {
				if lootSvc.Equipment != nil {
					if nb, _, eqErr := b.AddEquipment(lootSvc.Equipment, eqSlots, a.Template, a.Quantity); eqErr == nil {
						b = nb
						continue
					}
				}
				dur := a.Durability
				if dur == 0 {
					dur = 35
				}
				b, _, e = b.AddRawEquipment(eqSlots, a.Template, dur, a.Quantity)
				if e != nil {
					return nil, nil, e
				}
			}
		}
		if len(bagAwards) > 0 {
			local := catalog.LootCatalog{Source: lootSvc.Catalog.Source, Items: map[uint32]catalog.LootItem{}}
			for _, a := range bagAwards {
				local.Items[a.Template] = localCatalogEntryWithHint(lootSvc.Catalog, lootSvc.BagRules, a.Template, a.Quantity, a.StackableType)
			}
			for _, a := range bagAwards {
				b, _, e = b.Add(local, lootSvc.BagRules, a.Template, a.Quantity)
				if e != nil {
					return nil, nil, e
				}
			}
		}
		state, e := inventory.SaveBag(cur.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"slot": slot, "box": box, "count": openCount, "awards": awards})
		return state, receipt, e
	})
	if err != nil {
		return role, nil, box, 0, 0, false, err
	}
	changedKinds := map[byte]bool{}
	if applied && len(contractDays) > 0 {
		if err = store.ExtendPremiumContracts(ctx, account, contractDays); err != nil {
			return saved, nil, box, 0, 0, false, err
		}
		for k := range contractDays {
			changedKinds[k] = true
		}
	}
	return finalizeBoosterPackets(ctx, store, fatigue, account, saved, box, slot, awards, applied, changedKinds, len(avatarIDs) > 0, len(talismanIDs) > 0 || len(runeAwards) > 0)
}

func catalogToBoosterAward(a catalog.BoosterAward, mult uint32) boosterAward {
	q := a.Quantity
	if q == 0 {
		q = 1
	}
	isTal := a.Kind == "t" || inventory.IsTalismanTemplate(a.Template)
	isRune := a.StackableType == "[rune]"
	return boosterAward{
		Template:      a.Template,
		Quantity:      q * mult,
		Avatar:        a.Kind == "a",
		Talisman:      isTal,
		Rune:          isRune,
		Equipment:     a.Kind == "e" && !isTal,
		StackableType: a.StackableType,
		Durability:    a.Durability,
	}
}

func pickCatalogEntry(pool []catalog.BoosterAward, nextRand func(uint32) uint32) catalog.BoosterAward {
	if len(pool) == 1 {
		return pool[0]
	}
	var total uint32
	for _, it := range pool {
		w := it.Weight
		if w == 0 {
			w = 1
		}
		total += w
	}
	if total == 0 {
		return pool[0]
	}
	r := nextRand(total)
	var acc uint32
	for _, it := range pool {
		w := it.Weight
		if w == 0 {
			w = 1
		}
		acc += w
		if r < acc {
			return it
		}
	}
	return pool[len(pool)-1]
}

func resolveCatalogBoosterBox(
	box catalog.BoosterBox,
	category uint32,
	chosenList []uint32,
	isChoice bool,
	openCount uint16,
	nextRand func(uint32) uint32,
) ([]boosterAward, error) {
	if box.Mode == "sel" {
		if box.SelectNum == 0 {
			items, ok := box.Categories[category]
			if isChoice && len(chosenList) > 0 {
				want := chosenList[0]
				matched := false
				if ok {
					for _, it := range items {
						if it.Template == want {
							matched = true
							break
						}
					}
				}
				if !matched {
					for _, catItems := range box.Categories {
						for _, it := range catItems {
							if it.Template == want {
								items = catItems
								ok = true
								matched = true
								break
							}
						}
						if matched {
							break
						}
					}
				}
			}
			if !ok || len(items) == 0 {
				return nil, fmt.Errorf("invalid category %d", category)
			}
			out := make([]boosterAward, 0, len(items))
			for _, it := range items {
				out = append(out, catalogToBoosterAward(it, uint32(openCount)))
			}
			return out, nil
		}
		// SelectNum >= 1: pick 1 or N distinct choices
		if len(chosenList) > 0 {
			wantN := box.SelectNum
			if wantN <= 0 {
				wantN = 1
			}
			if len(chosenList) > wantN {
				chosenList = chosenList[:wantN]
			}
			out := make([]boosterAward, 0, len(chosenList))
			for _, want := range chosenList {
				var found *catalog.BoosterAward
				if items, ok := box.Categories[category]; ok {
					for i := range items {
						if items[i].Template == want {
							found = &items[i]
							break
						}
					}
				}
				if found == nil {
					for _, items := range box.Categories {
						for i := range items {
							if items[i].Template == want {
								found = &items[i]
								break
							}
						}
						if found != nil {
							break
						}
					}
				}
				if found == nil {
					return nil, fmt.Errorf("invalid choice %d (category %d)", want, category)
				}
				out = append(out, catalogToBoosterAward(*found, uint32(openCount)))
			}
			return out, nil
		}
		if items, ok := box.Categories[category]; ok && len(items) == 1 {
			return []boosterAward{catalogToBoosterAward(items[0], uint32(openCount))}, nil
		}
		return nil, fmt.Errorf("missing item selection (category %d)", category)
	}

	// "bst" or "pkg": draw 1 weighted pick per group for each opened box.
	if len(box.Groups) == 0 {
		return nil, fmt.Errorf("empty booster box")
	}
	type aggKey struct {
		tpl      uint32
		avatar   bool
		talisman bool
		runeItem bool
		equip    bool
	}
	totals := map[aggKey]boosterAward{}
	var order []aggKey
	for i := uint16(0); i < openCount; i++ {
		for _, grp := range box.Groups {
			if len(grp) == 0 {
				continue
			}
			pick := pickCatalogEntry(grp, nextRand)
			aw := catalogToBoosterAward(pick, 1)
			k := aggKey{tpl: aw.Template, avatar: aw.Avatar, talisman: aw.Talisman, runeItem: aw.Rune, equip: aw.Equipment}
			if cur, seen := totals[k]; seen {
				cur.Quantity += aw.Quantity
				totals[k] = cur
			} else {
				order = append(order, k)
				totals[k] = aw
			}
		}
	}
	out := make([]boosterAward, 0, len(order))
	for _, k := range order {
		out = append(out, totals[k])
	}
	return out, nil
}

func resolveSelectionBox(def selectionBoxDef, category, chosen uint32, isChoice bool, openCount uint16) ([]boosterAward, error) {
	if def.SelectNum == 0 {
		// Full-set category box: awards every item in the selected category.
		items, ok := def.Categories[category]
		if isChoice && chosen != 0 {
			matched := false
			if ok {
				for _, it := range items {
					if it.Template == chosen {
						matched = true
						break
					}
				}
			}
			if !matched {
				for _, catItems := range def.Categories {
					for _, it := range catItems {
						if it.Template == chosen {
							items = catItems
							ok = true
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
			}
		}
		if !ok || len(items) == 0 {
			return nil, fmt.Errorf("invalid category %d", category)
		}
		out := make([]boosterAward, 0, len(items))
		for _, it := range items {
			out = append(out, boosterAward{
				Template: it.Template,
				Quantity: it.Quantity * uint32(openCount),
				Avatar:   it.Avatar,
			})
		}
		return out, nil
	}

	// Single-item selection box (SelectNum >= 1).
	if chosen != 0 {
		if items, ok := def.Categories[category]; ok {
			for _, it := range items {
				if it.Template == chosen {
					return []boosterAward{{
						Template: it.Template,
						Quantity: it.Quantity * uint32(openCount),
						Avatar:   it.Avatar,
					}}, nil
				}
			}
		}
		for _, items := range def.Categories {
			for _, it := range items {
				if it.Template == chosen {
					return []boosterAward{{
						Template: it.Template,
						Quantity: it.Quantity * uint32(openCount),
						Avatar:   it.Avatar,
					}}, nil
				}
			}
		}
		return nil, fmt.Errorf("invalid choice %d (category %d)", chosen, category)
	}
	if items, ok := def.Categories[category]; ok && len(items) == 1 {
		it := items[0]
		return []boosterAward{{
			Template: it.Template,
			Quantity: it.Quantity * uint32(openCount),
			Avatar:   it.Avatar,
		}}, nil
	}
	return nil, fmt.Errorf("missing item selection (category %d)", category)
}

type boosterAward struct {
	Template      uint32 `json:"template"`
	Quantity      uint32 `json:"quantity"`
	Avatar        bool   `json:"avatar,omitempty"`
	Talisman      bool   `json:"talisman,omitempty"`
	Rune          bool   `json:"rune,omitempty"`
	Equipment     bool   `json:"equipment,omitempty"`
	StackableType string `json:"-"`
	Durability    uint16 `json:"-"`
}

func contractBoosterAward(box, openCount uint32) boosterAward {
	switch box {
	case 590006272:
		return boosterAward{Template: 30, Quantity: 7 * openCount}
	case 590006273:
		return boosterAward{Template: 43, Quantity: 7 * openCount}
	case 590006274:
		return boosterAward{Template: 2660703, Quantity: 7 * openCount}
	case 590006275:
		return boosterAward{Template: 34, Quantity: openCount}
	case 590006276:
		return boosterAward{Template: 46, Quantity: openCount}
	case 590006277:
		return boosterAward{Template: 2660411, Quantity: openCount}
	default:
		return boosterAward{Template: box, Quantity: openCount}
	}
}

func deductBagSlot(b *inventory.Bag, slot uint16, template, count uint32) error {
	for i, it := range b.Items {
		if it.Slot == slot && it.Template == template && it.Amount >= count {
			if it.Amount == count {
				b.Items = append(b.Items[:i], b.Items[i+1:]...)
			} else {
				b.Items[i].Amount -= count
			}
			return nil
		}
	}
	return fmt.Errorf("slot %d does not hold %d x%d", slot, template, count)
}

func finalizeBoosterPackets(
	ctx context.Context,
	store *storage.Store,
	fatigue *character.FatigueService,
	account int64,
	saved storage.Character,
	box uint32,
	slot uint16,
	awards []boosterAward,
	applied bool,
	changedKinds map[byte]bool,
	syncAvatars bool,
	syncTalismans bool,
) (storage.Character, []outboundPacket, uint32, uint32, uint32, bool, error) {
	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		return saved, nil, box, 0, 0, applied, err
	}
	inv, err := protocol.US224InventoryRestoreWithExpansion(bag.ExpandedSlots, bag.Rows())
	if err != nil {
		return saved, nil, box, 0, 0, applied, err
	}
	// v224 CMD160 success: flags(2B), source item(u32), slot(u16), extra(u32=0),
	// reward count(u16), then per reward: template(u32), quantity(u32), display flags(2B=0,0).
	ack := binary.LittleEndian.AppendUint32([]byte{1, 0}, box)
	ack = binary.LittleEndian.AppendUint16(ack, slot)
	ack = binary.LittleEndian.AppendUint32(ack, 0)
	ack = binary.LittleEndian.AppendUint16(ack, uint16(len(awards)))
	for _, a := range awards {
		ack = binary.LittleEndian.AppendUint32(ack, a.Template)
		ack = binary.LittleEndian.AppendUint32(ack, a.Quantity)
		ack = append(ack, 0, 0)
	}
	var plan []outboundPacket
	if applied {
		plan = append(plan, outboundPacket{"booster_open_ack", 1, 160, ack})
	}
	plan = append(plan, outboundPacket{"booster_inventory_restored", 0, 13, inv})
	if syncAvatars {
		avInv, aerr := protocol.US224AvatarRestore(inventory.AvatarBagCapacity, bag.AvatarRows())
		if aerr != nil {
			return saved, nil, box, 0, 0, applied, aerr
		}
		plan = append(plan, outboundPacket{"booster_avatar_restored", 0, 13, avInv})
	}
	if syncTalismans {
		talInv, terr := protocol.US224TalismanRestore(bag.TalismanRows())
		if terr != nil {
			return saved, nil, box, 0, 0, applied, terr
		}
		plan = append(plan, outboundPacket{"booster_talisman_restored", 0, 13, talInv})
	}
	if len(changedKinds) > 0 {
		if cpkts, cerr := buildContractRefreshPackets(ctx, store, fatigue, account, saved.ID, changedKinds); cerr == nil {
			plan = append(plan, cpkts...)
		}
	}
	var firstAward, firstQty uint32
	if len(awards) > 0 {
		firstAward, firstQty = awards[0].Template, awards[0].Quantity
	}
	return saved, plan, box, firstAward, firstQty, applied, nil
}
