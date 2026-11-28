package main

import (
	"bytes"
	"dfolan/internal/game/wire"
	"fmt"
	"io"
	"time"
)

type outboundPacket struct {
	Name    string
	Kind    byte
	ID      uint16
	Payload []byte
}
type preparedPacket struct {
	outboundPacket
	Raw []byte
}
type entryPayloads struct {
	Select, Basic, Addition, Skills, Vault, Vault2, AccountVault, UserArea, Area, Fatigue, Complete []byte
	Experience, CompletedQuests, Inventory                                    []byte
	DanjiInventory                                                            []byte
	Avatars                                                                   []byte
	Talismans                                                                 []byte
	AvailableQuests                                                           []byte
	Worn                                                                      []byte
	ItemLocks                                                                 []byte
	AccountMaterials                                                          []byte
	GeneralOptions                                                            []byte
	AccountOptions                                                            []byte
	TimeSync                                                                  []byte
	Events                                                                    []byte
	DanjiStatus                                                               []byte
	WornUpdate                                                                []byte
	Stat                                                                      []byte
	// StatResend is the end-of-sequence mode-6 copy. It must carry DIFFERENT
	// marker values than Stat: the pair setters skip unchanged values, so an
	// identical resend never runs the consumption chain — and that chain is the
	// only known re-settle trigger after the mode-1 flag bits were applied
	// (the entry settlement itself fires during mode-0 processing, before any
	// mode-1 row can be applied). Empty falls back to Stat.
	StatResend []byte
}

func (p entryPayloads) packets() []outboundPacket {
	// Packet order is client-parse order, and two timeline captures
	// (tools/probe_apply_order.py, 2026-09-22) pinned the constraints:
	//
	//   run 1 (basic, stat, addition):  SETTLE flag=0 -> PARSE mode-1 -> F13/F12
	//       consumers ran, but AFTER the only settlement; panel writers never
	//       fired. The settlement is triggered by the mode-6 stat probe's
	//       consumption chain (applier -> pair consumer -> setters -> recompute).
	//   run 2 (addition, stat, basic):  PARSE mode-1 ran first but the F13/F12
	//       consumers NEVER ran — parsed before the actor row (mode 0) the apply
	//       key lookup needs, so the row was silently dropped.
	//
	// So the only order that gets block[13] applied before the settlement is:
	// mode 0 (creates/registers the actor) -> mode 1 (applies, sets the actor's
	// flag bits; settlement takes flag=block[13]>0) -> mode 6 (fills actor+0x7f2
	// AND triggers the settlement that now sees flag=1). The mode-6 resend stays
	// at the end: a copy that arrives after the actor exists also lands its
	// values idempotently (setter skips unchanged).
	return []outboundPacket{
		{"select_parser_response", 1, 4, p.Select},
		{"account_options_restored", 0, 2826, p.AccountOptions},
		{"entry_basic_probe_sent", 0, 2, p.Basic},
		{"entry_addition_sent", 0, 2, p.Addition},
		{"entry_stat_probe_sent", 0, 2, p.Stat},
		{"entry_skills_sent", 0, 19, p.Skills},
		{"vault_initialized", 0, 13, p.Vault},
		{"vault2_initialized", 0, 13, p.Vault2},
		{"account_vault_initialized", 0, 13, p.AccountVault},
		{"inventory_restored", 0, 13, p.Inventory},
		{"danji_inventory_restored", 0, 13, p.DanjiInventory},
		{"avatar_inventory_restored", 0, 13, p.Avatars},
		{"talisman_inventory_restored", 0, 13, p.Talismans},
		{"worn_equipment_restored", 0, 13, p.Worn},
		{"item_locks_restored", 0, 251, p.ItemLocks},
		{"account_materials_restored", 0, 1140, p.AccountMaterials},
		// Restore the saved scale before the first town scene, not after world entry.
		{"general_options_initialized", 0, 173, p.GeneralOptions},
		// Sync server clock (CMD 1959) before NOTI 108 so EventCalendar renders
		// today's real date and active event date ranges instead of 1970-01-01.
		{"server_time_synced", 1, 1959, p.TimeSync},
		{"active_events_initialized", 0, 108, p.Events},
		{"danji_status_initialized", 0, 2279, p.DanjiStatus},
		{"user_area_sent", 0, 23, p.UserArea},
		{"town_entry_probe_sent", 0, 24, p.Area},
		{"fatigue_sent", 0, 36, p.Fatigue},
		{"enter_gameworld_complete_sent", 0, 124, p.Complete},
		{"entry_experience_restored", 0, 37, p.Experience},
		{"completed_quests_restored", 0, 342, p.CompletedQuests},
		{"available_quests_restored", 0, 21, p.AvailableQuests},
		{"entry_stat_resend_sent", 0, 2, p.statResend()},
		{"worn_equipment_visuals_restored", 0, 14, p.WornUpdate},
	}
}

func (p entryPayloads) statResend() []byte {
	if len(p.StatResend) > 0 {
		return p.StatResend
	}
	return p.Stat
}

// Prepare every frame before the first write. In particular, a missing cipher
// must not disconnect an actor after SELECT but before scene/fatigue bootstrap.
func preparePackets(keys []byte, packets []outboundPacket) ([]preparedPacket, error) {
	var prepared []preparedPacket
	for _, p := range packets {
		if len(p.Payload) == 0 {
			continue
		}
		encrypted, err := wire.EncryptPayload(keys, p.ID, p.Payload)
		if err != nil {
			return nil, fmt.Errorf("%s type=%d id=%d encode: %w", p.Name, p.Kind, p.ID, err)
		}
		raw, err := wire.ServerFrame(p.Kind, p.ID, encrypted)
		if err != nil {
			return nil, fmt.Errorf("%s type=%d id=%d frame: %w", p.Name, p.Kind, p.ID, err)
		}
		prepared = append(prepared, preparedPacket{p, raw})
	}
	return prepared, nil
}

func writePackets(w io.Writer, packets []preparedPacket, sent func(preparedPacket)) error {
	// A previous reply may have left an expired socket deadline. Refresh it
	// for every batch, including quest replies after an idle conversation.
	if conn, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return fmt.Errorf("packet batch deadline: %w", err)
		}
	}
	for _, p := range packets {
		if _, err := io.Copy(w, bytes.NewReader(p.Raw)); err != nil {
			return fmt.Errorf("%s type=%d id=%d write: %w", p.Name, p.Kind, p.ID, err)
		}
		if sent != nil {
			sent(p)
		}
	}
	return nil
}
