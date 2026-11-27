package main

import (
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

// PVF 50002547: [booster selection num] 1. Client chooses the template;
// the source, not the request, determines quantity and the box that is spent.
func dailyGiftQuantity(id uint32) (uint32, bool) {
	switch id {
	case 10327281:
		return 60, true
	case 10327282:
		return 30, true
	}
	return 0, false
}

func neoBoxGrant(lootService *loot.Service, slot uint16, box, award, quantity uint32) func(storage.Character) (json.RawMessage, json.RawMessage, error) {
	return func(cur storage.Character) (json.RawMessage, json.RawMessage, error) {
		if cur.ConfigVersion != "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69" || cur.ConfigVersion != lootService.BagRules.Source {
			return nil, nil, fmt.Errorf("source mismatch")
		}
		b, e := inventory.ReadBag(cur.State)
		if e != nil {
			return nil, nil, e
		}
		found := false
		for i, v := range b.Items {
			if v.Slot == slot && v.Template == box && v.Amount > 0 {
				if v.Amount == 1 {
					b.Items = append(b.Items[:i], b.Items[i+1:]...)
				} else {
					b.Items[i].Amount--
				}
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("slot does not hold the requested Neo child box")
		}
		b, _, e = b.Add(lootService.Catalog, lootService.BagRules, award, quantity)
		if e != nil {
			return nil, nil, e
		}
		if _, e = protocol.US224InventoryRestore(b.Rows()); e != nil {
			return nil, nil, e
		}
		state, e := inventory.SaveBag(cur.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"slot": slot, "box": box, "award": award, "quantity": quantity})
		return state, receipt, e
	}
}

func (w *worldSession) claimNeo(cmd uint16, p []byte, account, id int64, store *storage.Store, lootSvc *loot.Service, wear *inventory.WearService) ([]outboundPacket, error) {
	if w == nil || w.role.ID != id || id == 0 || account == 0 || w.role.AccountID != account || w.activeDungeon != nil || store == nil || lootSvc == nil || wear == nil {
		return nil, fmt.Errorf("claim requires owned selected town character")
	}
	daily := cmd == 1201
	if daily {
		if len(p) != 0 {
			return nil, fmt.Errorf("invalid daily request")
		}
	} else if cmd != 1200 || len(p) != 4 || p[0] != 1 || p[1] != 1 || p[2] != 0 {
		return nil, fmt.Errorf("invalid single rental selection")
	}
	var update []byte
	role, e := store.ClaimNeoBenefit(account, id, w.role.ConfigVersion, daily, func(cur storage.Character, end int64) (json.RawMessage, error) {
		b, e := inventory.ReadBag(cur.State)
		if e != nil {
			return nil, e
		}
		var slot uint16
		if daily {
			b, slot, e = b.NeoGift(lootSvc.Catalog, lootSvc.BagRules)
		} else {
			job, ok := wear.Professions.Professions[cur.Profession]
			if !ok {
				return nil, fmt.Errorf("unknown rental profession")
			}
			b, slot, e = b.NeoWeapon(job.Job, p[3], end, wear.Catalog, wear.BagRules)
		}
		if e != nil {
			return nil, e
		}
		for _, r := range b.Rows() {
			if uint16(r[0])|uint16(r[1])<<8 == slot {
				update, e = protocol.US224InventoryUpdate([][protocol.CurrentItemRecordSize]byte{r})
				break
			}
		}
		if e != nil || update == nil {
			return nil, fmt.Errorf("could not project benefit to inventory")
		}
		return inventory.SaveBag(cur.State, b)
	})
	if e != nil {
		return nil, e
	}
	role.WireID = w.role.WireID
	w.role = role
	ack := []byte{1}
	if !daily {
		ack = append(ack, 0)
	} // zero previous rental slots to remove
	return []outboundPacket{{"neo_claim_inventory", 0, 14, update}, {"neo_claim_ack", 1, cmd, ack}}, nil
}
