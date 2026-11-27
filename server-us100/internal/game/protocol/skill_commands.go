package protocol

import (
	"encoding/binary"
	"fmt"
)

// US100 Customize sender 143ef47a0: count, then skill ID (u16 LE),
// command length and that many byte tokens. An empty row resets this skill.
type SkillCommands map[uint16][]byte

func ValidSkillCommand(keys []byte) bool {
	if len(keys) > 5 {
		return false
	}
	if len(keys) == 0 {
		return true
	}
	for i, v := range keys {
		if i == len(keys)-1 {
			return (v == 4 || v == 5 || v == 6 || v == 8) && !(i == 0 && (v == 5 || v == 6))
		}
		if v > 12 || (v >= 4 && v <= 8) {
			return false
		}
	}
	return false
}

func DecodeSkillCommands(p []byte) (SkillCommands, error) {
	if len(p) == 0 {
		return nil, fmt.Errorf("missing command count")
	}
	o := 1
	rows := SkillCommands{}
	for i := 0; i < int(p[0]); i++ {
		if len(p)-o < 3 {
			return nil, fmt.Errorf("short command row")
		}
		id := binary.LittleEndian.Uint16(p[o:])
		n := int(p[o+2])
		o += 3
		_, duplicate := rows[id]
		if duplicate || id == 0 || id == 65535 || n > len(p)-o || !ValidSkillCommand(p[o:o+n]) {
			return nil, fmt.Errorf("invalid command row")
		}
		rows[id] = append([]byte(nil), p[o:o+n]...)
		o += n
	}
	if err := padding(p[o:], 16); err != nil {
		return nil, err
	}
	return rows, nil
}
