package protocol

import (
	"encoding/binary"
	"fmt"
)

// RosterCounterFirst is the first u32 of the 848 counter response (development
// knob; the native reader 0x14022af70 consumes exactly this field).
var RosterCounterFirst uint32

// EmptyMercenaryInfo answers the initial account's empty mercenary collection.
// Request 0x143d5c070: u8 count, count bytes, then slot-13 block padding.
// Success 0x143d59f80: u8 collection count; zero skips all nested records.
func EmptyMercenaryInfo(request []byte) ([]byte, error) {
	if len(request) == 0 || int(request[0])+1 > len(request) {
		return nil, fmt.Errorf("short mercenary request")
	}
	if err := padding(request[1+int(request[0]):], 8); err != nil {
		return nil, err
	}
	// A session on the reference build received eight bytes for MERCENARY_INFO
	// (client_trace.txt: "[RECV] ENUM_CMDPACKET_MERCENARY_INFO (Size : 8) Result
	// : Ok"). The two-byte body this used to return is shorter than the reader
	// consumes; the trailing zeros keep success plus an empty collection.
	return append([]byte{1}, make([]byte, 7)...), nil
}

// ProbeRosterCounters is the development account's zeroed counter fixture.
// 0x145258d00 consumes exactly 19 bytes after success. 0x14022af70 uses
// the first u32; remaining meanings are unresolved. No paid/event slots are
// granted. This is kept separate from the data-driven character capacity.
func ProbeRosterCounters(request []byte) ([]byte, error) {
	if len(request) != 0 {
		return nil, fmt.Errorf("roster counter request must be empty")
	}
	// The reference session received 24 bytes for SEASON_SERVER_USER_INFO
	// (client_trace.txt: "(Size : 24) Result : Ok"); the counter block is padded
	// to that size so the reader never runs off the end of the body.
	//
	// The native consumer 0x14022af70 uses the first u32, so the value is a
	// runtime experiment rather than a fixed zero.
	p := append([]byte{1}, make([]byte, 23)...)
	binary.LittleEndian.PutUint32(p[1:], uint32(RosterCounterFirst))
	return p, nil
}
