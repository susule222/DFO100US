package main

import (
	"bytes"
	"dfolan/internal/catalog"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

func cartListing(id uint32) (storage.CartLine, bool) {
	if id == neoShopSale.Product {
		return storage.CartLine{Product: id, Template: neoShopSale.Template, Cera: neoShopSale.Cera}, true
	}
	if v, ok := premiumListings[id]; ok {
		return v, true
	}
	v, ok := ceraShopListings[id]
	return v, ok
}
func premiumOrder(p []byte) ([]storage.CartLine, error) {
	if len(p) < 4 || p[0] != 0 || p[1] != 0 {
		return nil, fmt.Errorf("unsupported checkout mode")
	}
	n := int(binary.LittleEndian.Uint16(p[2:]))
	end := 4 + n*14
	if n < 1 || n > 10 || len(p) != (end+7)/8*8 || !allZero(p[end:]) {
		return nil, fmt.Errorf("invalid cart dimensions")
	}
	var out []storage.CartLine
	for i := 0; i < n; i++ {
		b := p[4+14*i : 18+14*i]
		if b[0] != 255 || (b[1] >= 10 && !(n == 1 && b[1] == 255)) {
			return nil, fmt.Errorf("unsupported gift or item option")
		}
		id := binary.LittleEndian.Uint32(b[2:])
		l, ok := cartListing(id)
		if !ok {
			return nil, fmt.Errorf("unverified sale %d", id)
		}
		// Avatar purchases carry chosen stat option bytes in b[6..13];
		// non-avatar stackable/contract items must keep b[6..13] zero.
		if l.Kind != "avatar" && !allZero(b[6:]) {
			return nil, fmt.Errorf("unsupported gift or item option")
		}
		out = append(out, l)
	}
	return out, nil
}
func allZero(b []byte) bool { return bytes.Count(b, []byte{0}) == len(b) }

// ACK64 uses one result per line, with 1-based position and total.
func cartAck(id uint32, reason uint16, index, total int) []byte {
	p := neoShopAck(id, reason)
	offset := 10
	if reason != 0 {
		offset += 2
	}
	binary.LittleEndian.PutUint32(p[offset:], uint32(index+1))
	binary.LittleEndian.PutUint32(p[offset+4:], uint32(total))
	return p
}
// inventoryExpandTarget maps inventory expansion ticket templates to the
// target expanded slot count (8 = Stage 1/+1 row, 16 = Stage 2/+2 rows,
// 0 = next-stage +8 slots up to 16).
// Verified from DFO_unpacked.exe 0x142a526f2..0x142a52746 and PVF .stk files.
func inventoryExpandTarget(template uint32) (uint16, bool) {
	switch template {
	case 2660296: // 物品栏扩展券 (1阶段) - stackable/cash/inven_upgradekit1.stk
		return 8, true
	case 2660297: // 物品栏扩展券 (2阶段) - stackable/cash/inven_upgradekit2.stk
		return 16, true
	case 2660298, // 物品栏扩展券 - stackable/cash/inven_next_upgradekit.stk
		50005238, 50009075, 50010169, 50010396, 50020607,
		50007130, 50007177,
		10164965, 10313712, 10320798, 10323256, 10323880, 10333025:
		return 0, true
	}
	return 0, false
}

const (
	vaultUpgradeSafe1   byte = 1 // Personal Safe I (space 0x02, NOTI 66 subType 1)
	vaultUpgradeSafe2   byte = 2 // Personal Safe II (space 0x2d, NOTI 66 subType 22)
	vaultUpgradeAccount byte = 3 // Account Vault (space 0x0c, NOTI 13 space 0x0c)
)

// vaultUpgradeTarget maps US100 Safe I, Safe II, and Account Vault upgrade
// templates to (kind, targetSlots, ok). Verified from DFO_unpacked.exe
// 0x142a3f200..0x142a3f644 (Safe I/II) and PVF etc/accountcargo.etc.
// targetSlots == 0 means "advance by one tier".
func vaultUpgradeTarget(template uint32) (byte, uint16, bool) {
	// Personal Safe I cash shop & event kits (8 -> 24 -> 40 -> ... -> 264)
	switch template {
	case 50, 110: // safe_upgradekit.stk (prod 3000129), safe_upgradekitsilver.stk
		return vaultUpgradeSafe1, 24, true
	case 57, 68: // safe_upgradekit2.stk (prod 3000130), safe_upgradekitgold.stk
		return vaultUpgradeSafe1, 40, true
	case 58: // safe_upgradekit3.stk (prod 3000131)
		return vaultUpgradeSafe1, 56, true
	case 59: // safe_upgradekit4.stk (prod 3000132)
		return vaultUpgradeSafe1, 72, true
	case 60: // safe_upgradekit5.stk (prod 3000133)
		return vaultUpgradeSafe1, 88, true
	case 61: // safe_upgradekit6.stk (prod 3000134)
		return vaultUpgradeSafe1, 104, true
	case 62: // safe_upgradekit7.stk (prod 3000135)
		return vaultUpgradeSafe1, 120, true
	case 2660235: // safe_upgradekit8.stk (prod 3000136)
		return vaultUpgradeSafe1, 136, true
	case 2660236: // safe_upgradekit9.stk (prod 3000137)
		return vaultUpgradeSafe1, 152, true
	case 10098631: // prod 3001227
		return vaultUpgradeSafe1, 168, true
	case 10098632: // prod 3001228
		return vaultUpgradeSafe1, 184, true
	case 10098633: // prod 3001229
		return vaultUpgradeSafe1, 200, true
	case 10304724: // prod 3001693
		return vaultUpgradeSafe1, 216, true
	case 10304725: // prod 3001694
		return vaultUpgradeSafe1, 232, true
	case 10304726: // prod 3001695
		return vaultUpgradeSafe1, 248, true
	case 10304727: // prod 3001696
		return vaultUpgradeSafe1, 264, true
	// Event Safe I next-tier kits ([action type] [upgrade cargo])
	case 50002236, 50007176, 50020608, 50021124, 50021185, 50021252,
		50021670, 50021948, 50022587, 50023500, 50023669, 50023851,
		50024367, 50050400:
		return vaultUpgradeSafe1, 0, true
	// Event one-shot max Personal Safe I kits (oneshotvault.stk)
	case 50040978, 50041642, 590005096, 590005110, 590700258,
		590701112, 590701708, 590701759, 590703171, 590703312, 590704002:
		return vaultUpgradeSafe1, inventory.MaxPersonalVaultSlots, true
	}

	// Personal Safe II (0x142a3f4ab..0x142a3f514: 10333724 unlock, 10333160..10333174 tiers 1..15)
	if template == 10333724 {
		return vaultUpgradeSafe2, 8, true
	}
	if template >= 10333160 && template <= 10333174 {
		tier := uint16(template-10333160) + 1
		return vaultUpgradeSafe2, 8 + tier*16, true
	}

	// Account Vault (etc/accountcargo.etc: 8..240 in steps of 8)
	if template == 10000659 {
		return vaultUpgradeAccount, 8, true
	}
	if template >= 10000660 && template <= 10000668 {
		return vaultUpgradeAccount, 16 + uint16(template-10000660)*8, true
	}
	if template >= 10098634 && template <= 10098643 {
		return vaultUpgradeAccount, 88 + uint16(template-10098634)*8, true
	}
	if template >= 10304728 && template <= 10304737 {
		return vaultUpgradeAccount, 168 + uint16(template-10304728)*8, true
	}
	switch template {
	case 50021125, 50021666, 590703176: // event account vault upgrade tickets
		return vaultUpgradeAccount, 0, true
	}
	return 0, 0, false
}

func premiumDelivery(src catalog.LootCatalog, r inventory.BagRules) storage.CartDeliver {
	return func(c storage.Character, lines []storage.CartLine) (json.RawMessage, json.RawMessage, error) {
		if c.ConfigVersion != "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69" || c.ConfigVersion != src.Source.Checksum || r.Source != c.ConfigVersion {
			return nil, nil, fmt.Errorf("unsupported sale source")
		}
		b, e := inventory.ReadBag(c.State)
		if e != nil {
			return nil, nil, e
		}
		local := catalog.LootCatalog{Source: src.Source, Items: map[uint32]catalog.LootItem{}}
		var total uint64
		for _, l := range lines {
			if known, ok := cartListing(l.Product); !ok || known != l {
				return nil, nil, fmt.Errorf("invalid listing")
			}
			total += uint64(l.Gold)
			if l.Kind == "avatar" {
				continue
			}
			if _, ok := inventoryExpandTarget(l.Template); ok {
				continue
			}
			if _, _, ok := vaultUpgradeTarget(l.Template); ok {
				continue
			}
			qty := l.Quantity
			if qty == 0 {
				qty = 1
			}
			limit := uint32(1000)
			if qty > limit {
				limit = qty
			}
			if existing, ok := src.Items[l.Template]; ok && existing.Kind == "stackable" {
				if existing.StackLimit < limit {
					existing.StackLimit = limit
				}
				local.Items[l.Template] = existing
			} else {
				local.Items[l.Template] = catalog.LootItem{ID: l.Template, Kind: "stackable", StackableType: stackableSlotType(src, l.Template), StackLimit: limit}
			}
		}
		if total > uint64(b.Gold) {
			return nil, nil, fmt.Errorf("insufficient gold")
		}
		b.Gold -= uint32(total)
		var del []map[string]any
		for _, l := range lines {
			if term, ok := l.Contract(); ok {
				del = append(del, map[string]any{"product": l.Product, "template": l.Template, "contract_kind": term.Kind, "days": term.Days})
				continue
			}
			if target, ok := inventoryExpandTarget(l.Template); ok {
				nextSlots, expErr := b.ExpandInventory(target)
				if expErr != nil {
					return nil, nil, expErr
				}
				del = append(del, map[string]any{"product": l.Product, "template": l.Template, "expanded_slots": nextSlots})
				continue
			}
			if vkind, vtarget, ok := vaultUpgradeTarget(l.Template); ok {
				del = append(del, map[string]any{"product": l.Product, "template": l.Template, "vault_upgrade_kind": vkind, "vault_target_slots": vtarget})
				continue
			}
			if l.Kind == "avatar" {
				var avatarSlots []uint16
				b, avatarSlots, e = b.AddAvatars([]uint32{l.Template})
				if e != nil {
					return nil, nil, e
				}
				del = append(del, map[string]any{"product": l.Product, "template": l.Template, "avatar_slot": avatarSlots[0]})
				continue
			}
			qty := l.Quantity
			if qty == 0 {
				qty = 1
			}
			var slots uint16
			b, slots, e = b.Add(local, r, l.Template, qty)
			if e != nil {
				return nil, nil, e
			}
			del = append(del, map[string]any{"product": l.Product, "template": l.Template, "quantity": qty, "slot": slots})
		}
		state, e := inventory.SaveBag(c.State, b)
		if e != nil {
			return nil, nil, e
		}
		raw, e := json.Marshal(del)
		return state, raw, e
	}
}
