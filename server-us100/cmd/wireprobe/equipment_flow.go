package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"fmt"
	"time"
)

type equipmentSession struct {
	nonce       [16]byte
	initialized bool
}

func (s *equipmentSession) handle(service *inventory.WearService, w *worldSession, p, raw []byte) ([]outboundPacket, error) {
	if service == nil || w == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("equipment move requires owned character")
	}
	r, e := protocol.DecodeItemMove(p)
	if e != nil {
		return nil, e
	}
	// CMD19 carries every bag move, not only equipment. A stack going onto
	// the quick-use belt belongs to the stackable path; anything it does not
	// recognise falls through to the equipment move unchanged.
	if plan, handled, e := w.moveStack(service.BagRules, r); handled {
		return plan, e
	}
	if !s.initialized {
		if _, e = rand.Read(s.nonce[:]); e != nil {
			return nil, e
		}
		s.initialized = true
	}
	hash := sha256.Sum256(raw)
	key := fmt.Sprintf("equipment:%x:%x", s.nonce, hash)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved, _, e := service.Move(ctx, w.role, key, r)
	if e != nil {
		return nil, e
	}
	b, e := inventory.ReadBag(saved.State)
	if e != nil {
		return nil, e
	}
	// US100 v2.24 ACK 19 (0x1434b3a20) only dispatches EquipSwap (0x143b149d0)
	// when SourceList is a bag (0/1/0x2b/...) and DestinationList is 3 (worn); it has
	// no branch for SourceList=3 -> DestinationList=0/1/0x2b. Normalize drag-unequip
	// replies so EquipSwap always unlocks the bag, updates CUser, and recalculates
	// character stats in real time.
	ackReq := r
	if ackReq.SourceList == 3 && (ackReq.DestinationList == 0 || ackReq.DestinationList == 1 || ackReq.DestinationList == protocol.TalismanSpace) {
		ackReq.SourceList, ackReq.DestinationList = ackReq.DestinationList, ackReq.SourceList
		ackReq.SourceSlot, ackReq.DestinationSlot = ackReq.DestinationSlot, ackReq.SourceSlot
		ackReq.SourceItem, ackReq.DestinationItem = ackReq.DestinationItem, ackReq.SourceItem
	}
	plan := []outboundPacket{{"equipment_move_committed", 1, 19, protocol.ItemMoveSuccess(ackReq, 1)}}
	isAvatarMove := r.SourceList == 1 || r.DestinationList == 1
	for _, space := range []byte{0, 1, 3, protocol.TalismanSpace} {
		var rows [][protocol.CurrentItemRecordSize]byte
		for _, loc := range []struct {
			space byte
			slot  uint16
		}{{r.SourceList, r.SourceSlot}, {r.DestinationList, r.DestinationSlot}} {
			if loc.space != space {
				continue
			}
			// US100 v2.24 NOTI 14 (0x1435106e0) checks template == 0xffffffff (-1)
			// across spaces 0, 1, 3, and 0x2b to delete/skip an emptied slot cleanly.
			row := protocol.EmptySlotItem(loc.slot)
			if space == protocol.TalismanSpace && loc.slot >= inventory.RuneSlotStart {
				for _, rn := range b.Runes {
					if rn.Slot == loc.slot {
						row = protocol.OrdinaryItem(rn.Slot, rn.Template, rn.Amount)
						if rn.SortLocked {
							row[protocol.ItemSortLockOffset] = 1
						}
						break
					}
				}
				rows = append(rows, row)
				continue
			}
			items := b.Equipment
			if space == 1 {
				items = b.Avatars
			} else if space == protocol.TalismanSpace {
				items = b.Talismans
			} else if space == 3 {
				if isAvatarMove || loc.slot <= 11 {
					items = b.WornAvatars
				} else {
					items = b.Worn
				}
			}
			for _, item := range items {
				if item.Slot == loc.slot {
					row = inventory.EquipmentRow(item)
					break
				}
			}
			rows = append(rows, row)
		}
		if len(rows) > 0 {
			body, e := protocol.US224InventorySpaceUpdate(space, rows)
			if e != nil {
				return nil, e
			}
			plan = append(plan, outboundPacket{"equipment_slots_updated", 0, 14, body})
		}
	}
	w.role = saved
	if (r.SourceList == 3 || r.DestinationList == 3) && w.role.WireID != 0 && w.role.WireID != 0xffff {
		fameBody, err := protocol.AdventurerFameUpdate(w.role.WireID, service.Catalog.BagAdventurerFame(b))
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"adventurer_fame_updated", 0, protocol.AdventurerFameNotiID, fameBody})
	}
	// In STATE_TOWN (GameMode == 1), ACK 19 -> 0x143b149d0 explicitly skips
	// updating CUserInfo and CTownUser (0x143b15282: cmp eax, 1; je skip), and
	// NOTI 14 space 3 neither sets nor clears CTownUser once ACK 19 has already
	// mutated CUser. Re-emitting NOTI 2 mode 0 calls 0x143801610 ->
	// CTownUser::InitFromUserInfo(&tmpUserInfo) so avatars (0..11), weapon (12),
	// and title (13) update live in town on equip, swap, and unequip.
	if touchesTownVisualSlot(r) && w.activeDungeon == nil && !w.selectingDungeon && w.role.WireID != 0 && w.role.WireID != 0xffff && w.role.Name != "" {
		charSvc := w.characters
		if charSvc == nil {
			charSvc = &character.Service{Equipment: service.Catalog, Store: service.Store}
		}
		appearanceBody, err := charSvc.EntryBasicProbe(saved, w.channelCtx)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"town_appearance_updated", 0, 2, appearanceBody})
	}
	return plan, nil
}

func touchesTownVisualSlot(r protocol.ItemMoveRequest) bool {
	return isTownVisualWornSlot(r.SourceList, r.SourceSlot) || isTownVisualWornSlot(r.DestinationList, r.DestinationSlot)
}

func isTownVisualWornSlot(list byte, slot uint16) bool {
	return list == 3 && (slot <= 13 || slot == 26 || slot == 32)
}
