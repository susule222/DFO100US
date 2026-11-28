package quest

import (
	"context"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/progression"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

type FinishReceipt struct {
	Quest      uint16                   `json:"quest"`
	Experience uint32                   `json:"experience"`
	Gold       uint32                   `json:"gold,omitempty"`
	Source     string                   `json:"source"`
	Model      string                   `json:"model"`
	Items      []inventory.AwardReceipt `json:"items,omitempty"`
	Consumed   bool                     `json:"consumed,omitempty"`
}
type FinishResult struct {
	Role    storage.Character
	Receipt FinishReceipt
	Applied bool
}

func cells(c []pvf.Token, name string) []pvf.Token {
	var a []pvf.Token
	on := false
	for _, v := range c {
		if v.Type == 3 {
			on = v.Text == name
			continue
		}
		if on {
			a = append(a, v)
		}
	}
	return a
}

func (s *Service) Finish(ctx context.Context, role storage.Character, r protocol.QuestSubmitRequest) (FinishResult, error) {
	var out FinishResult
	if s.Progression == nil {
		return out, ErrRewardPending
	}
	if r.Option != 1 {
		return out, fmt.Errorf("quest reward selection requires inventory settlement")
	}
	d, ok := s.Catalog.Quests[uint32(r.ID)]
	if !ok {
		return out, fmt.Errorf("quest source missing")
	}
	if tag := s.Index().Entries[uint32(r.ID)].Unverified; tag != "" {
		return out, fmt.Errorf("quest requires unverified source section: %s", tag)
	}
	_, model, e := InitialProgress(d)
	if e != nil {
		return out, e
	}
	// Current3145 omits reward type but supplies typed [job] item tuples.
	// Matching item tuples use the shared inventory awarder. An absent type
	// never implies a profession change or a fabricated reward. [none] carries
	// no items and settles on experience alone. The same test gates the
	// available list, so a quest that cannot settle is never offered.
	if !rewardUsable(d) {
		return out, ErrRewardPending
	}
	commit, e := s.Store.CommitQuestReward(ctx, role.AccountID, role.ID, r.ID, s.Catalog.Source.Checksum, model, s.Progression.Rules.Model, func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		var state character.State
		consumed := model == CollectItems || model == SeekAndMeetNPC
		if consumed {
			needs := collection(d)
			if model == SeekAndMeetNPC {
				obj, _ := SeekMeet(d)
				needs = obj.Items
			}
			bag, err := inventory.ReadBag(current.State)
			if err != nil {
				return nil, nil, err
			}
			bag, enough := takeItems(bag, needs)
			if !enough {
				return nil, nil, fmt.Errorf("required quest items missing")
			}
			current.State, err = inventory.SaveBag(current.State, bag)
			if err != nil {
				return nil, nil, err
			}
		}
		if e := json.Unmarshal(current.State, &state); e != nil {
			return nil, nil, e
		}
		reward := d.RewardCells
		kind := cells(d.Script.Cells, "[reward type]")
		percent := len(kind) == 1 && kind[0].Text == "[percent exp]"
		var awakeningReward byte
		if percent {
			reward = nil
		} else if len(kind) == 1 && kind[0].Text == "[awakening type]" && len(d.RewardCells) == 1 && d.RewardCells[0].Type == 0 && d.RewardCells[0].Value > 0 {
			awakeningReward = byte(d.RewardCells[0].Value)
			reward = nil
		}
		awards, e := progression.ItemRewards(reward, current.Profession, state.Advancement)
		if e != nil {
			return nil, nil, e
		}
		choices, e := progression.ItemRewards(cells(d.Script.Cells, "[reward selection int data]"), current.Profession, state.Advancement)
		if e != nil {
			return nil, nil, e
		}
		if len(choices) > 0 {
			if int(r.RewardSelection) >= len(choices) {
				return nil, nil, fmt.Errorf("choose an eligible quest reward")
			}
			awards = append(awards, choices[r.RewardSelection])
		} else if r.RewardSelection != 65535 {
			return nil, nil, fmt.Errorf("unexpected reward selection")
		}
		if len(awards) > 0 && s.Inventory == nil {
			return nil, nil, ErrRewardPending
		}
		// EXP, gold, inventory and completion are prepared within one
		// transaction.
		gain, e := progression.QuestExperience(s.Progression.Catalog, d, state.Level)
		if percent {
			gain, e = percentExperience(s.Progression.Catalog.Thresholds, state.Level, s.Progression.Rules.LevelCap, uint64(d.RewardCells[0].Value))
		}
		for _, attribute := range cells(d.Script.Cells, "[attribute]") {
			if attribute.Text == "not give exp quest" {
				gain = 0
			}
		}
		if e != nil {
			return nil, nil, e
		}
		gold, e := progression.QuestGold(s.Progression.Catalog, d, state.Level)
		if e != nil {
			return nil, nil, e
		}
		saved, _, e := s.Progression.ApplyGain(current, uint64(gain))
		if e != nil {
			return nil, nil, e
		}
		if awakeningReward > state.Awakening {
			var doc map[string]json.RawMessage
			if e := json.Unmarshal(saved.State, &doc); e != nil {
				return nil, nil, e
			}
			rawAwak, e := json.Marshal(awakeningReward)
			if e != nil {
				return nil, nil, e
			}
			doc["awakening"] = rawAwak
			saved.State, e = json.Marshal(doc)
			if e != nil {
				return nil, nil, e
			}
		}
		// Completion gold comes from the [gold reward table], not from the
		// quest's own cells; it is credited to the wallet (award id 0) in the
		// same transaction. Item awards need the inventory service; gold does
		// not, so a quest that pays only gold still settles when items are
		// unconfigured, as long as the wallet can take it.
		if gold > 0 {
			if s.Inventory == nil {
				return nil, nil, ErrRewardPending
			}
			saved.State, _, e = s.Inventory.Grant(saved.State, 0, gold)
			if e != nil {
				return nil, nil, e
			}
		}
		var items []inventory.AwardReceipt
		for _, a := range awards {
			var item inventory.AwardReceipt
			saved.State, item, e = s.Inventory.Grant(saved.State, a.Template, a.Amount)
			if e != nil {
				return nil, nil, e
			}
			items = append(items, item)
		}
		receipt, e := json.Marshal(FinishReceipt{Quest: r.ID, Experience: gain, Gold: gold, Source: s.Catalog.Source.Checksum, Model: s.Progression.Rules.Model, Items: items, Consumed: consumed})
		return saved.State, receipt, e
	}, s.Index().Entries[uint32(r.ID)].Links)
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(commit.Receipt, &out.Receipt); e != nil {
		return out, e
	}
	if out.Receipt.Quest != r.ID || out.Receipt.Source != s.Catalog.Source.Checksum {
		return out, fmt.Errorf("quest reward receipt mismatch")
	}
	out.Role, out.Applied = commit.Character, commit.Applied
	return out, nil
}

func (s *Service) Completed(ctx context.Context, role storage.Character) ([]uint32, error) {
	states, e := s.Store.Quests(ctx, role.AccountID, role.ID)
	if e != nil {
		return nil, e
	}
	var ids []uint32
	for _, q := range states {
		if q.Status == "completed" {
			if q.ConfigVersion != s.Catalog.Source.Checksum {
				return nil, fmt.Errorf("completed quest source mismatch")
			}
			ids = append(ids, uint32(q.ID))
		}
	}
	return ids, nil
}
