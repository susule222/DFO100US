package quest

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"errors"
	"fmt"
)

const SingleClearMap = "single-clear-map-remaining-v1"
const SingleMeetNPC = "single-meet-npc-remaining-v1"
const SingleReachRange = "single-reach-range-remaining-v1"
const SeekAndMeetNPC = "seek-items-and-meet-npc-remaining-v1"
const LookCinematic = "look-cinematic-client-gated-v1"
const CollectItems = "collect-items-v1"
const ClearDungeon = "clear-dungeon-v1"
const ClearNamedQuest = "completed-quest-v1"
const HuntMonster = "confirmed-target-kills-v1"
const HuntEnemy = "hunt-enemy-v1"
const MirkwoodSceneObject = "mirkwood-scene-object-trigger-v1"
const QuestionClientGated = "question-client-gated-v1"
const UseItemClientGated = "use-item-client-gated-v1"

func collection(d catalog.QuestDefinition) []ItemNeed {
	c := d.ObjectiveCells
	if len(c) == 0 || len(c)%2 != 0 {
		return nil
	}
	var needs []ItemNeed
	for i := 0; i < len(c); i += 2 {
		if c[i].Type != 0 || c[i+1].Type != 0 || c[i].Value < 0 || c[i+1].Value <= 0 {
			return nil
		}
		needs = append(needs, ItemNeed{uint32(c[i].Value), uint32(c[i+1].Value)})
	}
	return needs
}

// RangeObjective is a six-integer [reach the range] objective: a town, an
// area and an axis-aligned rectangle. Origin plus extent is the only reading
// consistent with every current low-level source row (3156 "38 3 445 115 400
// 300", 3160 "40 3 1034 180 100 100", 21094 "40 3 132 160 900 200"); a
// centre-and-half-extent reading puts 3156 at a negative Y. This has not been
// confirmed against the native handler, so the decode stays strict and any
// other shape remains unimplemented rather than guessed.
type RangeObjective struct {
	Town, Area uint32
	NPC        uint32
	X, Y, W, H int32
}

func ReachRange(d catalog.QuestDefinition) (RangeObjective, bool) {
	c := d.ObjectiveCells
	if len(d.Pending) != 0 || d.Kind != "[reach the range]" {
		return RangeObjective{}, false
	}
	if len(c) == 3 {
		for _, t := range c {
			if t.Type != 0 || t.Value <= 0 {
				return RangeObjective{}, false
			}
		}
		return RangeObjective{NPC: uint32(c[0].Value), W: c[1].Value, H: c[2].Value}, true
	}
	if len(c) != 6 {
		return RangeObjective{}, false
	}
	for _, t := range c {
		if t.Type != 0 {
			return RangeObjective{}, false
		}
	}
	if c[0].Value <= 0 || c[1].Value < 0 || c[4].Value <= 0 || c[5].Value <= 0 {
		return RangeObjective{}, false
	}
	return RangeObjective{Town: uint32(c[0].Value), Area: uint32(c[1].Value), X: c[2].Value, Y: c[3].Value, W: c[4].Value, H: c[5].Value}, true
}

// RangeProximityMargin accounts for the client's character reach/hitbox overlap
// against [reach the range] trigger boxes (e.g. Q3160 in 40/3 starts at X=1034
// while the Imperial Guard barricade stops walking at X=1033 and the client
// triggers CMD 33 at X=994).
const RangeProximityMargin int32 = 80

func (r RangeObjective) Contains(p storage.WorldPosition) bool {
	return p.Town == r.Town && p.Area == r.Area &&
		int32(p.X) >= r.X-RangeProximityMargin && int32(p.X) <= r.X+r.W+RangeProximityMargin &&
		int32(p.Y) >= r.Y-RangeProximityMargin && int32(p.Y) <= r.Y+r.H+RangeProximityMargin
}

type ItemNeed struct{ Template, Amount uint32 }

// SeekObjective is an odd-length [seek n meet npc] objective: repeated
// (item, amount) pairs followed by the NPC identity. Source 3173
// "10164787 1 10164789 1 29" collects the two items awarded by its own
// prerequisites 3169 and 3171 and then reports to NPC 29.
type SeekObjective struct {
	Items []ItemNeed
	NPC   uint32
}

func SeekMeet(d catalog.QuestDefinition) (SeekObjective, bool) {
	c := d.ObjectiveCells
	if len(d.Pending) != 0 || d.Kind != "[seek n meet npc]" || len(c) < 3 || len(c)%2 == 0 {
		return SeekObjective{}, false
	}
	for _, t := range c {
		if t.Type != 0 || t.Value <= 0 {
			return SeekObjective{}, false
		}
	}
	var out SeekObjective
	for i := 0; i+2 < len(c); i += 2 {
		out.Items = append(out.Items, ItemNeed{uint32(c[i].Value), uint32(c[i+1].Value)})
	}
	out.NPC = uint32(c[len(c)-1].Value)
	return out, len(out.Items) > 0
}

var ErrObjectiveIncomplete = errors.New("quest still requires a verified map clear")
var ErrRewardPending = errors.New("quest reward application is not implemented")

// Native addAcceptQuest144f38b60 and updateQuest144f69770 treat zero as
// ready to submit. For the supported single map objective, one means pending.
// Broader condition encodings must be recovered before accepting other types.
func InitialProgress(d catalog.QuestDefinition) (uint32, string, error) {
	if len(d.Pending) == 0 {
		c := d.ObjectiveCells
		switch d.Kind {
		case "[hunt enemy]":
			// US100 quest 3157 is a passive scene object with a dedicated Mirkwood
			// scene-object ledger. Keep its exact model unchanged.
			if d.ID == 3157 && d.Script.SHA256 == "bb60af65355bcfdbb69c094ceca59e11f8f503e45d81f63278564b2189862bd1" && len(c) == 5 {
				for i, want := range []int32{3, -1, 13099, 3, 1} {
					if c[i].Type != 0 || c[i].Value != want {
						return 0, "", fmt.Errorf("unexpected Mirkwood scene objective")
					}
				}
				return 1, MirkwoodSceneObject, nil
			}
			// General [hunt enemy] consists of 5-cell tuples:
			// (dungeonID, minDifficulty, targetID, targetType[1..3], count).
			if len(c) >= 5 && len(c)%5 == 0 {
				var total uint32
				valid := true
				for i := 0; i < len(c); i += 5 {
					if c[i].Type != 0 || c[i].Value <= 0 ||
						c[i+1].Type != 0 || c[i+1].Value < -1 || c[i+1].Value > 5 ||
						c[i+2].Type != 0 || c[i+2].Value <= 0 ||
						c[i+3].Type != 0 || c[i+3].Value < 1 || c[i+3].Value > 3 ||
						c[i+4].Type != 0 || c[i+4].Value <= 0 {
						valid = false
						break
					}
					total += uint32(c[i+4].Value)
				}
				if valid && total > 0 {
					return total, HuntEnemy, nil
				}
			}
		case "[hunt monster]":
			// Repeated 4-cell tuples: (dungeonID, minDifficulty, monsterTemplateOrWildcard, count).
			if len(c) >= 4 && len(c)%4 == 0 {
				var total uint32
				valid := true
				for i := 0; i < len(c); i += 4 {
					if c[i].Type != 0 || c[i].Value <= 0 ||
						c[i+1].Type != 0 || c[i+1].Value < -1 || c[i+1].Value > 5 ||
						c[i+2].Type != 0 || c[i+2].Value == 0 ||
						c[i+3].Type != 0 || c[i+3].Value <= 0 {
						valid = false
						break
					}
					total += uint32(c[i+3].Value)
				}
				if valid && total > 0 {
					return total, HuntMonster, nil
				}
			}
		case "[seeking]":
			if len(collection(d)) > 0 {
				return 1, CollectItems, nil
			}
		case "[clear a dungeon]":
			if len(c) == 2 && c[0].Type == 0 && c[0].Value > 0 && c[1].Type == 0 && c[1].Value == -1 {
				return 1, ClearDungeon, nil
			}
		case "[clear dungeon index]":
			if len(c) == 2 && c[0].Type == 0 && c[0].Value > 0 && c[1].Type == 0 && c[1].Value > 0 {
				return uint32(c[1].Value), ClearDungeon, nil
			}
		case "[condition under clear]":
			sub := cells(d.Script.Cells, "[sub type]")
			if len(sub) == 1 && sub[0].Type == 0 && sub[0].Value == 6 && (len(c) == 2 || len(c) == 3) &&
				c[0].Type == 0 && c[0].Value > 0 && c[1].Type == 0 && c[1].Value >= -1 && c[1].Value <= 5 &&
				len(cells(d.Script.Cells, "[condition data]")) == 0 {
				if len(c) == 2 {
					return 1, ClearDungeon, nil
				}
				if c[2].Type == 0 && c[2].Value > 0 {
					return uint32(c[2].Value), ClearDungeon, nil
				}
			}
			if len(sub) == 1 && sub[0].Type == 0 && sub[0].Value == 11 && len(c) == 3 &&
				c[0].Type == 0 && c[0].Value > 0 && c[1].Type == 0 && c[1].Value >= -1 && c[1].Value <= 5 &&
				c[2].Type == 0 {
				return 1, ClearDungeon, nil
			}
		case "[clear quest]":
			if len(c) == 1 && c[0].Type == 0 && c[0].Value > 0 && c[0].Value < 40000 {
				return 1, ClearNamedQuest, nil
			}
		case "[quest clear]":
			sub := cells(d.Script.Cells, "[sub type]")
			if len(c) >= 1 && len(sub) == 1 && sub[0].Type == 0 && sub[0].Value == 2 {
				for _, ref := range c {
					if ref.Type != 0 || ref.Value <= 0 || ref.Value >= 40000 {
						return 0, "", fmt.Errorf("unsupported child quest identity")
					}
				}
				return 1, ClearNamedQuest, nil
			}
		case "[question]":
			if len(c) == 1 && c[0].Type == 0 && c[0].Value > 0 {
				return 0, QuestionClientGated, nil
			}
		case "[use item]":
			sub := cells(d.Script.Cells, "[sub type]")
			if len(sub) == 1 && sub[0].Type == 0 && sub[0].Value == 1 && len(c) == 3 &&
				c[0].Type == 0 && c[1].Type == 0 && c[1].Value > 0 && c[2].Type == 0 && c[2].Value > 0 {
				return 0, UseItemClientGated, nil
			}
		}
	}
	if len(d.Pending) == 0 && d.Kind == "[meet npc]" && len(d.ObjectiveCells) == 1 && d.ObjectiveCells[0].Type == 0 && d.ObjectiveCells[0].Value > 0 {
		return 1, SingleMeetNPC, nil
	}
	if len(d.Pending) == 0 && d.Kind == "[clear map]" &&
		(len(d.ObjectiveCells) == 1 || (len(d.ObjectiveCells) == 2 && d.ObjectiveCells[1].Type == 0 && d.ObjectiveCells[1].Value >= -1 && d.ObjectiveCells[1].Value <= 5)) &&
		d.ObjectiveCells[0].Type == 0 && d.ObjectiveCells[0].Value > 0 {
		return 1, SingleClearMap, nil
	}
	if _, ok := ReachRange(d); ok {
		return 1, SingleReachRange, nil
	}
	if _, ok := SeekMeet(d); ok {
		return 1, SeekAndMeetNPC, nil
	}
	// A [look cinematic] objective has no server-verifiable condition: the
	// client plays the cutscene locally and only then lets the player submit.
	// Progress 0 means the server accepts that submit; the single cell is the
	// cinematic id, kept only to validate the source shape. This is the sole
	// completion path a cinematic has, and it mints nothing beyond the quest's
	// own configured reward. Without it the main story stalls at the first
	// cutscene beat (quest 21029, level 22).
	if len(d.Pending) == 0 && d.Kind == "[look cinematic]" && len(d.ObjectiveCells) == 1 && d.ObjectiveCells[0].Type == 0 && d.ObjectiveCells[0].Value > 0 {
		return 0, LookCinematic, nil
	}
	return 0, "", fmt.Errorf("quest objective type or structure is not implemented: %s", d.Kind)
}
func (s *Service) Active(ctx context.Context, role storage.Character) ([]protocol.ActiveQuest, error) {
	states, e := s.Store.Quests(ctx, role.AccountID, role.ID)
	if e != nil {
		return nil, e
	}
	out := []protocol.ActiveQuest{}
	for _, q := range states {
		if q.Status != "accepted" {
			continue
		}
		d, ok := s.Catalog.Quests[uint32(q.ID)]
		if !ok || q.ConfigVersion != s.Catalog.Source.Checksum {
			return nil, fmt.Errorf("quest %d requires source migration", q.ID)
		}
		initial, model, e := InitialProgress(d)
		if e != nil || q.ProgressModel != model || q.Progress > initial {
			return nil, fmt.Errorf("quest %d requires progress migration", q.ID)
		}
		out = append(out, protocol.ActiveQuest{ID: q.ID, Progress: q.Progress})
	}
	return out, nil
}

// A completion request is not evidence of a map clear. No client-provided
// counter or submit option can mint rewards or alter persistent objectives.
func (s *Service) Submit(ctx context.Context, role storage.Character, id uint16) error {
	active, e := s.Active(ctx, role)
	if e != nil {
		return e
	}
	for _, q := range active {
		if q.ID == id {
			if q.Progress != 0 {
				return ErrObjectiveIncomplete
			}
			return ErrRewardPending
		}
	}
	return errors.New("quest is not active for this character")
}
