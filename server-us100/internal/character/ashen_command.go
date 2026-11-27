package character

import (
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
)

const ashenForkID uint16 = 16

// This is the server catalogue paired with the tested US100 dungeon-jump client resource.
func (s *Service) ashenCommandProfile(job byte) bool {
	if job != 0 || s.Learning == nil {
		return false
	}
	d, ok := s.Learning.index[job][ashenForkID]
	return ok && d.Path == "skill/swordman/ashenfork.skl" && d.SHA256 == "031f5a7abdf406eb28f9d6782c748d80fd80ee32e7a9eaf1d91deec4cbd1c8be"
}

// Omit the preview only after allocating all book slots. The stock client
// can learn from its own definition; this placeholder competes with ground Z.
func omitUnlearnedAshen(rows []protocol.LearnedSkill) []protocol.LearnedSkill {
	for i, r := range rows {
		if r.ID == ashenForkID && r.Level == 0 {
			return append(rows[:i:i], rows[i+1:]...)
		}
	}
	return rows
}

// Only the proven single first-rank acquisition uses the native ACK without a
// second skill-table rebuild. All retries, edits, refunds and batches retain
// their original synchronization. Caller separately checks the DB commit.
func (s *Service) AshenFirstRankAckOnly(role storage.Character, r protocol.SkillPurchase) bool {
	if r.Tree != 0 || len(r.Entries) != 1 || r.Entries[0] != (protocol.SkillPurchaseEntry{ID: ashenForkID, Delta: 1}) || !s.ashenCommandProfile(role.Profession) {
		return false
	}
	var st State
	return json.Unmarshal(role.State, &st) == nil && st.LearnedSkills[0][ashenForkID] == 1 && len(st.SkillCommands[0][ashenForkID]) == 0
}
