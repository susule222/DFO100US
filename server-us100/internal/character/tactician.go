package character

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Service) settleTactician(ctx context.Context, c storage.Character) (storage.Character, error) {
	var initial State
	if e := json.Unmarshal(c.State, &initial); e != nil {
		return c, e
	}
	if len(initial.TacticianRanks) == 0 {
		return c, nil
	}
	end, e := s.Store.TacticianEnd(c.AccountID)
	if e != nil || end > time.Now().Unix() {
		return c, e
	}
	if s.Learning == nil || s.Learning.Source.Checksum != c.ConfigVersion {
		return c, fmt.Errorf("tactician settlement missing skill catalog")
	}
	ledger, _ := json.Marshal(initial.TacticianRanks)
	key := fmt.Sprintf("tactician-expiry/%d/%x", end, sha256.Sum256(ledger))
	role, _, e := s.Store.CommitCharacterEvent(ctx, c.AccountID, c.ID, c.ConfigVersion, key, "source-tactician-expiry-v1", func(cur storage.Character) (json.RawMessage, json.RawMessage, error) {
		var st State
		if err := json.Unmarshal(cur.State, &st); err != nil {
			return nil, nil, err
		}
		same, _ := json.Marshal(st.TacticianRanks)
		if string(same) != string(ledger) {
			return nil, nil, fmt.Errorf("skill state changed during settlement")
		}
		points := int(st.SkillPoints[0])
		for id, ranks := range st.TacticianRanks {
			d, ok := s.Learning.index[c.Profession][id]
			if !ok {
				return nil, nil, fmt.Errorf("missing skill for settlement")
			}
			level := int(st.LearnedSkills[0][id])
			for level > 0 && d.rankNeed(level) > int(st.Level) {
				cost, ok := ranks[level]
				if !ok {
					return nil, nil, fmt.Errorf("missing paid skill rank")
				}
				points += cost
				level--
			}
			if level == 0 {
				delete(st.LearnedSkills[0], id)
				delete(st.SkillSlots[0], id)
			} else {
				st.LearnedSkills[0][id] = byte(level)
			}
		}
		if points > 65535 {
			return nil, nil, fmt.Errorf("skill refund overflow")
		}
		st.SkillPoints[0] = uint16(points)
		st.TacticianRanks = nil
		state, err := mergeSkillState(cur.State, st)
		return state, json.RawMessage("{}"), err
	})
	role.WireID = c.WireID
	return role, e
}
