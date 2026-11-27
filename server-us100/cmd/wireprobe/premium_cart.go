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
				local.Items[l.Template] = catalog.LootItem{ID: l.Template, Kind: "stackable", StackableType: "[throw]", StackLimit: limit}
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
