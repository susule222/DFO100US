package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// gateSetOptions controls which packets the dungeon gate answers CMD15 with.
//
// The reference builds this set conditionally and the conditionality matters:
// S0/02FD and S0/02FE are the Dark Elf Ruins license-dungeon snapshot and the
// reference sends them only when the gate's worldmap is
// LICENSE_DUNGEON_WORLDMAP_ID (38); S0/02E6 is the guild dungeon table and goes
// only for worldmap 37. This server sent 02FD/02FE for every gate, including
// GranFloris (worldmap 2), which is a different map screen entirely.
//
// The worldmap condition is derived from the town area, not configured. The
// switches below are for the packets the reference always sends, so a variant
// can be tried by editing JSON and restarting instead of rebuilding.
type gateSetOptions struct {
	// UserAreaSelect is S0/0017 with area = 0xFF, which the reference documents
	// as the signal that raises the selection screen ("Force area 0xFF for
	// select UI (doc: critical signal)").
	UserAreaSelect bool `json:"user_area_select"`
	// EnterSelect is S0/001B, the notification that opens the selection UI.
	EnterSelect bool `json:"enter_select"`
	// PartyPreload is S0/0009 with dungeon_id 0, sent while the selection UI is
	// up so the local party slot is warm before a dungeon is granted.
	PartyPreload bool `json:"party_preload"`
	// EnterSelectState is the S0/0003 state byte sent with the gate set. It is 0
	// (town) by default because this client reads 1 as "the local actor is in a
	// dungeon"; the reference's 0x01 does not carry that meaning here. Set it to
	// 1 only to reproduce the old behaviour.
	EnterSelectState int `json:"enter_select_state"`
	// ReferenceOrder sends the gate set in the reference's sequence, where the
	// permission rows and the area arrive BEFORE S0/0003. The reference's own
	// comment makes the order load-bearing: "install one complete mode1 snapshot
	// before USER_STATE changes the selector context". Our long-standing order
	// sends 0003 first; this switch exists to test the difference.
	ReferenceOrder bool `json:"reference_order"`
	// CandidateList puts the gate's dungeon ids into S0/001B's u16 array.
	//
	// It is OFF, and that is a measured result, not a default: with the array
	// filled the client answers the click with nothing at all (its scene report
	// moves to 0x1b and SELECT_DUNGEON never arrives), while with the empty array
	// the click works and the client sends SELECT_DUNGEON. The reference names
	// that array "missing_hell_quest_user_keys" — party member keys for a
	// blocking hell-mode condition, empty for a solo player — so filling it with
	// dungeon ids was a misreading of this project's own earlier note that the
	// client's dungeon container is filled from a server list. Keep it off; the
	// switch remains for anyone who wants to re-measure it.
	CandidateList bool `json:"candidate_list"`
	// MinimalGate answers the gate the way the run of 2026-09-18 06:24 did, which
	// is the only retained session in which the selection screen demonstrably
	// opened: an ACK for command 15 plus ENTER_SELECT with the all-zero body, and
	// no companions at all. Its log (runtime/cursor/events.jsonl, lines 75-87)
	// shows the client answering with SELECT_DUNGEON and a 32-byte body for
	// dungeon 3. Only CMD37 confirms that the selected room loaded.
	//
	// Every companion packet this server now sends (0003, 001A, 0005, 0164,
	// 0017, 0009) was added afterwards, from the reference, so one of them is
	// what stopped the screen from opening. It defaults to true: the minimal
	// answer is the reproduction, and the companions can be re-enabled one at a
	// time to find which one breaks it.
	MinimalGate bool `json:"minimal_gate"`
	// GateAck is the kind-1 acknowledgement for command 15 that the same run
	// sent. It is part of the minimal answer.
	GateAck bool `json:"gate_ack"`
}

func defaultGateSet() gateSetOptions {
	return gateSetOptions{
		UserAreaSelect:   true,
		EnterSelect:      true,
		PartyPreload:     true,
		EnterSelectState: 0,
		CandidateList:    false,
		MinimalGate:      true,
		GateAck:          true,
	}
}

// loadGateSet reads an optional JSON file. A missing file means the defaults,
// and an unreadable file is an error so a typo cannot silently change the
// packet set under test. It reports whether the file was actually read, because
// a silent fallback to defaults is how a config edit can appear to have no
// effect: the first version defaulted to a relative path that did not exist
// under the server's working directory, and every run then used the defaults.
func loadGateSet(path string) (gateSetOptions, bool, error) {
	options := defaultGateSet()
	if path == "" {
		return options, false, nil
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		if os.IsNotExist(e) {
			return options, false, nil
		}
		return options, false, fmt.Errorf("gate set %s: %w", path, e)
	}
	if e = json.Unmarshal(raw, &options); e != nil {
		return options, false, fmt.Errorf("gate set %s: %w", path, e)
	}
	return options, true, nil
}

// Worldmap ids the reference special-cases. Kept as constants with the source
// they came from so the numbers are not magic.
const (
	// licenseDungeonWorldmap is flow-dungeon_enter.LICENSE_DUNGEON_WORLDMAP_ID.
	licenseDungeonWorldmap = 38
	// guildDungeonWorldmap is helper-guild_dungeon_purification.GUILD_DUNGEON_WORLDMAP_ID.
	guildDungeonWorldmap = 37
)
