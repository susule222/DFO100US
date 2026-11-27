package character

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"

	"dfolan/internal/game/protocol"
)

//go:embed native_commands.generated.json
var commandManifest []byte

var nativeSource = func() (v struct {
	Source   string
	Commands map[string]string
}) { if e := json.Unmarshal(commandManifest, &v); e != nil {
	panic(e)
}; return }()

func (s *Service) nativeCommand(job byte, id uint16) string {
	if l := s.Learning; l != nil && l.Source.Checksum == nativeSource.Source {
		d, ok := l.index[job][id]
		if !ok || !d.Active() {
			return ""
		}
		return nativeSource.Commands[d.SHA256]
	}
	// Preserve the two existing fixes in the legacy no-metadata path.
	if job == 0 {
		switch id {
		case 25:
			return "(RIGHT),(BUFF)"
		case 175:
			return "(UP),(BUFF)"
		}
	}
	return ""
}

// Only compare straight sequences with known on-wire actions. In particular
// "hold UP and Z" is not equivalent to "tap UP, then Z".
func sameNativeCommand(rule string, keys []byte) bool {
	if rule == "" || len(keys) == 0 || !protocol.ValidSkillCommand(keys) {
		return false
	}
	parts := strings.Split(rule, ",")
	if len(parts) != len(keys) {
		return false
	}
	var buf [5]byte
	for i, p := range parts {
		switch p {
		case "(UP)":
			buf[i] = 0
		case "(DOWN)":
			buf[i] = 1
		case "(LEFT)":
			buf[i] = 2
		case "(RIGHT)":
			buf[i] = 3
		case "(SKILL)":
			buf[i] = 4
		case "(BUFF)":
			buf[i] = 8
		default:
			return false
		}
	}
	return bytes.Equal(buf[:len(parts)], keys)
}

// Work on the wire projection, after slot assignment, never on stored ranks.
func omitStyleIICommandShadows(first, second []protocol.LearnedSkill, eligible func(uint16) bool) []protocol.LearnedSkill {
	owned := map[uint16]bool{}
	for _, s := range first {
		if s.Level > 0 && eligible(s.ID) {
			owned[s.ID] = true
		}
	}
	var out []protocol.LearnedSkill
	for i, s := range second {
		if s.Level == 0 && owned[s.ID] {
			if out == nil {
				out = make([]protocol.LearnedSkill, 0, len(second))
				out = append(out, second[:i]...)
			}
			continue
		}
		if out != nil {
			out = append(out, s)
		}
	}
	if out == nil {
		return second
	}
	return out
}
