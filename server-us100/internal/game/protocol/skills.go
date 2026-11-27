package protocol

import (
	"encoding/binary"
	"fmt"
)

type LearnedSkill struct {
	ID    uint16
	Level byte
	Slot  uint16
}

// NOTI19 (v224 SKILLINFO handler RVA 0x350e140) is u32 byte length followed by protobuf, unlike the
// skill arrays in NOTI2 which the client skips for its own character.
// Parser addresses are build-specific; use the NOTI registration for this image.
func SkillInfo(level byte, skills []LearnedSkill) ([]byte, error) {
	return SkillInfoTrees(level, [2]SkillTree{{Skills: skills}, {Skills: skills}})
}

type SkillTree struct {
	SP, TP   uint16
	Skills   []LearnedSkill
	Commands SkillCommands
}

func SkillInfoTrees(level byte, trees [2]SkillTree) ([]byte, error) {
	if level == 0 {
		return nil, fmt.Errorf("invalid skill bootstrap")
	}
	pbint := func(p []byte, tag byte, v uint64) []byte { p = append(p, tag); return binary.AppendUvarint(p, v) }
	body := binary.AppendUvarint([]byte{8}, uint64(level))
	for _, state := range trees {
		if len(state.Skills) > 1024 {
			return nil, fmt.Errorf("too many skills")
		}
		tree := pbint(nil, 8, uint64(state.SP))
		tree = pbint(tree, 16, uint64(state.TP))
		tree = pbint(tree, 32, 0)
		seen := map[uint16]bool{}
		for _, s := range state.Skills {
			if s.ID == 0 || seen[s.ID] || (s.Level == 0 && s.Slot < 14) {
				return nil, fmt.Errorf("invalid learned skill")
			}
			seen[s.ID] = true
			item := pbint(nil, 8, uint64(s.Slot))
			item = pbint(item, 16, uint64(s.ID))
			item = pbint(item, 24, uint64(s.Level))
			if keys := state.Commands[s.ID]; s.Level > 0 && len(keys) > 0 {
				if !ValidSkillCommand(keys) {
					return nil, fmt.Errorf("invalid saved skill command")
				}
				// SkillInfo entry protobuf field 4, native repeated integer tokens.
				for _, k := range keys {
					item = pbint(item, 32, uint64(k))
				}
			}
			tree = pbint(tree, 26, uint64(len(item)))
			tree = append(tree, item...)
		}
		body = pbint(body, 18, uint64(len(tree)))
		body = append(body, tree...)
	}
	return append(add32(nil, uint32(len(body))), body...), nil
}
