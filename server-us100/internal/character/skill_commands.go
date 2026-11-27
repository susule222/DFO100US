package character

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

// The supported skill style is I (as in Learn and MoveSkill). Preferences
// carry no grants and can never alter levels, points or hotbar slots.
func (s *Service) SaveSkillCommands(ctx context.Context, role storage.Character, key string, rows protocol.SkillCommands) (storage.Character, bool, error) {
	if s.Learning == nil || s.Learning.Source.Checksum != role.ConfigVersion {
		return role, false, fmt.Errorf("skill command source mismatch")
	}
	saved, applied, e := s.Store.CommitCharacterEvent(ctx, role.AccountID, role.ID, role.ConfigVersion, key, "us100-skill-commands-v1", func(cur storage.Character) (json.RawMessage, json.RawMessage, error) {
		var state State
		if e := json.Unmarshal(cur.State, &state); e != nil {
			return nil, nil, e
		}
		known, e := knownSkills(state, 0)
		if e != nil {
			return nil, nil, e
		}
		if state.SkillCommands[0] == nil {
			state.SkillCommands[0] = make(map[uint16][]byte)
		}
		for id, keys := range rows {
			d, ok := s.Learning.index[cur.Profession][id]
			if !ok || !d.Active() || known[id] == 0 || !protocol.ValidSkillCommand(keys) {
				return nil, nil, fmt.Errorf("invalid or unlearned command skill %d", id)
			}
			if len(keys) == 0 {
				delete(state.SkillCommands[0], id)
			} else {
				state.SkillCommands[0][id] = append([]byte(nil), keys...)
			}
		}
		// Mutate only this preference; no re-encoding of unrelated state.
		var obj map[string]json.RawMessage
		if e = json.Unmarshal(cur.State, &obj); e != nil {
			return nil, nil, e
		}
		obj["custom_skill_commands"], e = json.Marshal(state.SkillCommands)
		if e != nil {
			return nil, nil, e
		}
		raw, e := json.Marshal(obj)
		return raw, json.RawMessage("{}"), e
	})
	saved.WireID = role.WireID
	return saved, applied, e
}
