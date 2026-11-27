package admin

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// CharacterOp describes an extended launcher GM command processed by the gateway inbox.
type CharacterOp struct {
	ID          string `json:"id,omitempty"`
	AccountID   int64  `json:"account_id,omitempty"`
	CharacterID int64  `json:"character_id,omitempty"`
	Operator    string `json:"operator,omitempty"`
	Reason      string `json:"reason"`
	Action      string `json:"action"` // currency | level | advancement | bag | quest

	// currency
	CeraMode    string `json:"cera_mode,omitempty"`    // add | set
	Cera        int64  `json:"cera,omitempty"`
	MileageMode string `json:"mileage_mode,omitempty"` // add | set
	Mileage     int64  `json:"mileage,omitempty"`
	GoldMode    string `json:"gold_mode,omitempty"`    // add | set
	Gold        int64  `json:"gold,omitempty"`

	// contract / activity ops
	ContractKind       byte                `json:"contract_kind,omitempty"` // 22/27/73/79/92/118/120/125 or 255=all
	ContractDays       int64               `json:"contract_days,omitempty"` // 0=clear, >0=extend days
	ActivityKey        string              `json:"activity_key,omitempty"`
	ClaimDate          string              `json:"claim_date,omitempty"`
	MileageCost        int64               `json:"mileage_cost,omitempty"`
	RewardGold         uint32              `json:"reward_gold,omitempty"`
	RewardMileage      int64               `json:"reward_mileage,omitempty"`
	RewardItems        []storage.GrantItem `json:"reward_items,omitempty"`
	RewardContracts    []byte              `json:"reward_contracts,omitempty"`
	RewardContractDays int64               `json:"reward_contract_days,omitempty"`

	// level / sp / tp
	Level      int     `json:"level,omitempty"`
	Experience *uint64 `json:"experience,omitempty"`
	SP         *int    `json:"sp,omitempty"`
	TP         *int    `json:"tp,omitempty"`
	SyncSP     bool    `json:"sync_sp,omitempty"`

	// advancement
	Advancement int  `json:"advancement,omitempty"`
	ResetSkills bool `json:"reset_skills,omitempty"`

	// bag
	Op       string `json:"op,omitempty"`
	Slot     uint16 `json:"slot,omitempty"`
	List     string `json:"list,omitempty"` // items | equipment
	Amount   uint32 `json:"amount,omitempty"`
	Category string `json:"category,omitempty"`

	// quest
	QuestID     uint16 `json:"quest_id,omitempty"`
	MaxLevel    uint32 `json:"max_level,omitempty"`
	GrantReward bool   `json:"grant_reward,omitempty"`
	Force       bool   `json:"force,omitempty"`

	// avatar (space 1 / 装扮栏)
	AvatarIDs []uint32 `json:"avatar_ids,omitempty"`
}

// OpReceipt summarizes a completed CharacterOp for the launcher UI and audit log.
type OpReceipt struct {
	ID             string `json:"id"`
	Action         string `json:"action"`
	Op             string `json:"op,omitempty"`
	CharacterID    int64  `json:"character_id,omitempty"`
	Summary        string `json:"summary"`
	CeraBalance    uint64 `json:"cera_balance,omitempty"`
	MileageBalance uint64 `json:"mileage_balance,omitempty"`
	GoldAfter      uint32 `json:"gold_after,omitempty"`
	Level          byte   `json:"level,omitempty"`
	Experience     uint64 `json:"experience,omitempty"`
	SP             uint16 `json:"sp,omitempty"`
	TP             uint16 `json:"tp,omitempty"`
	Advancement    byte   `json:"advancement,omitempty"`
	Affected       int    `json:"affected,omitempty"`
	RelogHint      bool   `json:"relog_hint,omitempty"`
}

func mergeCharacterState(raw json.RawMessage, state character.State) (json.RawMessage, error) {
	p, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var old, fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &old); err != nil {
		return nil, err
	}
	if old == nil {
		old = map[string]json.RawMessage{}
	}
	if err = json.Unmarshal(p, &fields); err != nil {
		return nil, err
	}
	for k, v := range fields {
		old[k] = v
	}
	return json.Marshal(old)
}

func cumulativeSP(c catalog.Progression, level byte) uint16 {
	var total uint32
	for lv := 2; lv <= int(level); lv++ {
		total += uint32(c.SkillPoints[uint16(lv)])
	}
	if total > 65535 {
		return 65535
	}
	return uint16(total)
}

func rebuildAttributes(prof catalog.Profession, level byte) map[string]float32 {
	attrs := make(map[string]float32, len(prof.InitialAttributes))
	for k, v := range prof.InitialAttributes {
		attrs[k] = v
	}
	levels := int(level) - 1
	if levels > 0 {
		for name, g := range prof.BaseGrowth {
			attrs[name] = float32((math.Round(float64(attrs[name])*10) + float64(levels)*math.Round(float64(g)*10)) / 10)
		}
	}
	return attrs
}

// ApplyOp executes a CharacterOp atomically and idempotently.
func (s *Service) ApplyOp(ctx context.Context, op CharacterOp) (OpReceipt, bool, error) {
	var out OpReceipt
	if s == nil || s.Store == nil {
		return out, false, fmt.Errorf("admin service is not configured")
	}
	if op.Operator == "" {
		op.Operator = s.Operator
	}
	if strings.TrimSpace(op.Reason) == "" {
		op.Reason = "GM 角色属性操作"
	}
	// Special path: finish a quest WITH real reward settlement uses Quests.Finish
	// (which runs its own db100 transaction), so settle progress first without nesting Mutate.
	if op.Action == "quest" && op.Op == "finish" && op.GrantReward {
		return s.applyQuestFinishWithReward(ctx, op)
	}
	reqBytes, err := json.Marshal(op)
	if err != nil {
		return out, false, err
	}
	res, err := s.Store.ApplyAdminOp(ctx, storage.AdminOpRequest{
		ID:          op.ID,
		AccountID:   op.AccountID,
		CharacterID: op.CharacterID,
		Operator:    op.Operator,
		Reason:      op.Reason,
		Request:     reqBytes,
	}, func(atx *storage.AdminTx, role *storage.Character) (json.RawMessage, json.RawMessage, error) {
		switch op.Action {
		case "currency":
			return s.execCurrency(atx, role, op)
		case "level":
			return s.execLevel(role, op)
		case "advancement":
			return s.execAdvancement(role, op)
		case "bag":
			return s.execBag(role, op)
		case "quest":
			return s.execQuest(atx, role, op)
		case "avatar":
			return s.execAvatar(role, op)
		case "contract":
			return s.execContract(atx, op)
		case "mileage_redeem":
			return s.execMileageRedeem(atx, role, op)
		case "activity_claim":
			return s.execActivityClaim(atx, role, op)
		case "refresh_sync":
			return s.execRefreshSync(atx, role, op)
		default:
			return nil, nil, fmt.Errorf("unsupported character op action %q", op.Action)
		}
	})
	if err != nil {
		return out, false, err
	}
	if len(res.Receipt) > 0 {
		if err = json.Unmarshal(res.Receipt, &out); err != nil {
			return out, res.Applied, err
		}
	}
	out.ID = op.ID
	if out.Action == "" {
		out.Action = op.Action
	}
	return out, res.Applied, nil
}

func (s *Service) execCurrency(atx *storage.AdminTx, role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if op.CeraMode == "" && op.GoldMode == "" && op.MileageMode == "" {
		return nil, nil, fmt.Errorf("currency op requires cera_mode, mileage_mode or gold_mode")
	}
	var parts []string
	rec := OpReceipt{ID: op.ID, Action: "currency", CharacterID: op.CharacterID}
	ceraCur, err := atx.AccountCera()
	if err != nil {
		return nil, nil, err
	}
	if op.CeraMode != "" {
		var next int64
		switch op.CeraMode {
		case "add":
			next = ceraCur + op.Cera
		case "set":
			next = op.Cera
		default:
			return nil, nil, fmt.Errorf("invalid cera_mode %q", op.CeraMode)
		}
		if next < 0 || next > math.MaxUint32 {
			return nil, nil, fmt.Errorf("点券余额超出范围 (0..%d)", uint32(math.MaxUint32))
		}
		if err = atx.SetAccountCera(next); err != nil {
			return nil, nil, err
		}
		ceraCur = next
		parts = append(parts, fmt.Sprintf("点券余额 → %d", next))
	}
	rec.CeraBalance = uint64(ceraCur)

	mileageCur, err := atx.AccountMileage()
	if err != nil {
		return nil, nil, err
	}
	if op.MileageMode != "" {
		var next int64
		switch op.MileageMode {
		case "add":
			next = mileageCur + op.Mileage
		case "set":
			next = op.Mileage
		default:
			return nil, nil, fmt.Errorf("invalid mileage_mode %q", op.MileageMode)
		}
		if next < 0 || next > math.MaxUint32 {
			return nil, nil, fmt.Errorf("商城积分超出范围 (0..%d)", uint32(math.MaxUint32))
		}
		if err = atx.SetAccountMileage(next); err != nil {
			return nil, nil, err
		}
		mileageCur = next
		parts = append(parts, fmt.Sprintf("商城积分 → %d", next))
	}
	rec.MileageBalance = uint64(mileageCur)

	var newState json.RawMessage
	if op.GoldMode != "" {
		if role == nil {
			return nil, nil, fmt.Errorf("gold op requires character_id")
		}
		bag, err := inventory.ReadBag(role.State)
		if err != nil {
			return nil, nil, err
		}
		var next int64
		switch op.GoldMode {
		case "add":
			next = int64(bag.Gold) + op.Gold
		case "set":
			next = op.Gold
		default:
			return nil, nil, fmt.Errorf("invalid gold_mode %q", op.GoldMode)
		}
		if next < 0 || next > math.MaxUint32 {
			return nil, nil, fmt.Errorf("金币超出范围 (0..%d)", uint32(math.MaxUint32))
		}
		bag.Gold = uint32(next)
		newState, err = inventory.SaveBag(role.State, bag)
		if err != nil {
			return nil, nil, err
		}
		rec.GoldAfter = bag.Gold
		parts = append(parts, fmt.Sprintf("金币 → %d", bag.Gold))
	}
	rec.Summary = strings.Join(parts, "；")
	b, err := json.Marshal(rec)
	return newState, b, err
}

func (s *Service) execLevel(role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil {
		return nil, nil, fmt.Errorf("level op requires character_id")
	}
	if s.Progression == nil || s.Characters == nil {
		return nil, nil, fmt.Errorf("progression service not configured")
	}
	if role.ConfigVersion != s.Progression.Catalog.Source.Checksum {
		return nil, nil, fmt.Errorf("character config version mismatch")
	}
	var state character.State
	if err := json.Unmarshal(role.State, &state); err != nil {
		return nil, nil, err
	}
	prof, ok := s.Characters.Catalog.Professions[role.Profession]
	if !ok {
		return nil, nil, fmt.Errorf("profession %d absent from catalog", role.Profession)
	}
	capLv := int(s.Progression.Rules.LevelCap)
	if capLv < 1 {
		capLv = 100
	}
	targetLevel := int(state.Level)
	levelChanged := false
	if op.Level > 0 {
		if op.Level < 1 || op.Level > capLv {
			return nil, nil, fmt.Errorf("等级必须在 1 到 %d 之间", capLv)
		}
		targetLevel = op.Level
		levelChanged = byte(targetLevel) != state.Level
		state.Level = byte(targetLevel)
		state.Attributes = rebuildAttributes(prof, state.Level)
		if state.SourceSHA256 == "" {
			state.SourceSHA256 = prof.RawSHA256
		}
		if targetLevel == 1 {
			state.Experience = 0
		} else if targetLevel-2 < len(s.Progression.Catalog.Thresholds) {
			state.Experience = s.Progression.Catalog.Thresholds[targetLevel-2]
		}
		if op.SP == nil && op.SyncSP {
			sp := cumulativeSP(s.Progression.Catalog, state.Level)
			state.SkillPoints = [2]uint16{sp, sp}
		}
	}
	if op.Experience != nil {
		floor := uint64(0)
		if targetLevel > 1 && targetLevel-2 < len(s.Progression.Catalog.Thresholds) {
			floor = s.Progression.Catalog.Thresholds[targetLevel-2]
		}
		if *op.Experience < floor {
			return nil, nil, fmt.Errorf("经验值不能低于 Lv.%d 下限 %d", targetLevel, floor)
		}
		state.Experience = *op.Experience
	}
	if op.SP != nil {
		if *op.SP < 0 || *op.SP > 65535 {
			return nil, nil, fmt.Errorf("SP 必须在 0 到 65535 之间")
		}
		v := uint16(*op.SP)
		state.SkillPoints = [2]uint16{v, v}
	}
	if op.TP != nil {
		if *op.TP < 0 || *op.TP > 65535 {
			return nil, nil, fmt.Errorf("TP 必须在 0 到 65535 之间")
		}
		v := uint16(*op.TP)
		state.TechniquePoints = [2]uint16{v, v}
	}
	newState, err := mergeCharacterState(role.State, state)
	if err != nil {
		return nil, nil, err
	}
	check := *role
	check.State = newState
	if _, err = s.Characters.EntryAddition(check); err != nil {
		return nil, nil, err
	}
	summary := fmt.Sprintf("Lv.%d · EXP %d · SP %d · TP %d", state.Level, state.Experience, state.SkillPoints[0], state.TechniquePoints[0])
	rec := OpReceipt{
		ID:          op.ID,
		Action:      "level",
		CharacterID: role.ID,
		Summary:     summary,
		Level:       state.Level,
		Experience:  state.Experience,
		SP:          state.SkillPoints[0],
		TP:          state.TechniquePoints[0],
		RelogHint:   levelChanged,
	}
	b, err := json.Marshal(rec)
	return newState, b, err
}

func (s *Service) execAdvancement(role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil {
		return nil, nil, fmt.Errorf("advancement op requires character_id")
	}
	if s.Characters == nil {
		return nil, nil, fmt.Errorf("character service not configured")
	}
	if op.Advancement < 0 || op.Advancement > 5 {
		return nil, nil, fmt.Errorf("转职分支编号必须在 0 到 5 之间")
	}
	var state character.State
	if err := json.Unmarshal(role.State, &state); err != nil {
		return nil, nil, err
	}
	state.Advancement = byte(op.Advancement)
	if op.ResetSkills {
		state.LearnedSkills = [2]map[uint16]byte{}
		state.SkillSlots = [2]map[uint16]uint16{}
		if s.Progression != nil {
			sp := cumulativeSP(s.Progression.Catalog, state.Level)
			state.SkillPoints = [2]uint16{sp, sp}
		}
	}
	newState, err := mergeCharacterState(role.State, state)
	if err != nil {
		return nil, nil, err
	}
	summary := fmt.Sprintf("转职分支 → %d", state.Advancement)
	if op.ResetSkills {
		summary += fmt.Sprintf("（已重置技能，SP %d）", state.SkillPoints[0])
	}
	rec := OpReceipt{
		ID:          op.ID,
		Action:      "advancement",
		CharacterID: role.ID,
		Summary:     summary,
		Level:       state.Level,
		SP:          state.SkillPoints[0],
		Advancement: state.Advancement,
		RelogHint:   true,
	}
	b, err := json.Marshal(rec)
	return newState, b, err
}

func (s *Service) execBag(role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil {
		return nil, nil, fmt.Errorf("bag op requires character_id")
	}
	if s.Awarder == nil {
		return nil, nil, fmt.Errorf("inventory awarder not configured")
	}
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, nil, err
	}
	rules := s.Awarder.Rules
	affected := 0
	var summary string

	switch op.Op {
	case "set_amount":
		if op.Slot == 0 || op.Amount == 0 || op.Amount > 9999 {
			return nil, nil, fmt.Errorf("数量必须在 1 到 9999 之间")
		}
		found := false
		for i := range bag.Items {
			if bag.Items[i].Slot == op.Slot {
				limit := rules.MissingStackLimit
				if def, ok := s.Awarder.Catalog.Items[bag.Items[i].Template]; ok && def.StackLimit > 0 {
					limit = def.StackLimit
				}
				if limit > 0 && op.Amount > limit {
					return nil, nil, fmt.Errorf("数量 %d 超过该物品堆叠上限 %d", op.Amount, limit)
				}
				bag.Items[i].Amount = op.Amount
				found = true
				affected = 1
				summary = fmt.Sprintf("槽位 %d 数量 → %d", op.Slot, op.Amount)
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("背包槽位 %d 不存在可堆叠物品", op.Slot)
		}

	case "delete_slot":
		if op.Slot == 0 {
			return nil, nil, fmt.Errorf("invalid bag slot 0")
		}
		if op.List == "" || op.List == "items" {
			kept := bag.Items[:0]
			for _, it := range bag.Items {
				if it.Slot == op.Slot {
					affected++
					continue
				}
				kept = append(kept, it)
			}
			bag.Items = kept
		}
		if affected == 0 && (op.List == "" || op.List == "equipment") {
			kept := bag.Equipment[:0]
			for _, eq := range bag.Equipment {
				if eq.Slot == op.Slot {
					affected++
					continue
				}
				kept = append(kept, eq)
			}
			bag.Equipment = kept
		}
		if affected == 0 {
			return nil, nil, fmt.Errorf("槽位 %d 为空或不存在", op.Slot)
		}
		summary = fmt.Sprintf("已删除槽位 %d 物品", op.Slot)

	case "unequip_slot":
		if op.Slot < 12 || op.Slot > 25 {
			return nil, nil, fmt.Errorf("穿戴槽位 %d 无效 (12..25)", op.Slot)
		}
		idx := -1
		for i, eq := range bag.Worn {
			if eq.Slot == op.Slot {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, nil, fmt.Errorf("穿戴槽位 %d 未穿戴装备", op.Slot)
		}
		occupied := map[uint16]bool{}
		for _, it := range bag.Items {
			occupied[it.Slot] = true
		}
		for _, eq := range bag.Equipment {
			occupied[eq.Slot] = true
		}
		freeSlot := uint16(0)
		for n := uint32(rules.EquipmentSlots[0]); n <= uint32(rules.EquipmentSlots[1]); n++ {
			if !occupied[uint16(n)] {
				freeSlot = uint16(n)
				break
			}
		}
		if freeSlot == 0 {
			return nil, nil, fmt.Errorf("装备背包已满，无法卸下")
		}
		piece := bag.Worn[idx]
		bag.Worn = append(bag.Worn[:idx], bag.Worn[idx+1:]...)
		piece.Slot = freeSlot
		bag.Equipment = append(bag.Equipment, piece)
		affected = 1
		summary = fmt.Sprintf("已卸下槽位 %d 装备至背包槽位 %d", op.Slot, freeSlot)

	case "clear_category":
		inRange := func(slot uint16, r [2]uint16) bool {
			return r != [2]uint16{} && slot >= r[0] && slot <= r[1]
		}
		switch op.Category {
		case "equipment":
			affected = len(bag.Equipment)
			bag.Equipment = nil
			summary = fmt.Sprintf("已清空装备栏 (%d 件)", affected)
		case "throw", "material", "quest", "expert", "quick":
			var rng [2]uint16
			switch op.Category {
			case "throw":
				rng = rules.Slots["[throw]"]
			case "material":
				rng = rules.Slots["[material]"]
			case "quest":
				rng = rules.Slots["[quest]"]
			case "expert":
				rng = rules.Slots["[material expert job]"]
			case "quick":
				rng = rules.QuickSlots
			}
			kept := bag.Items[:0]
			for _, it := range bag.Items {
				if inRange(it.Slot, rng) {
					affected++
					continue
				}
				kept = append(kept, it)
			}
			bag.Items = kept
			summary = fmt.Sprintf("已清空 %s 栏 (%d 格)", op.Category, affected)
		case "all":
			affected = len(bag.Equipment) + len(bag.Items)
			bag.Equipment = nil
			bag.Items = nil
			summary = fmt.Sprintf("已清空全部背包物品 (%d 项，保留金币与已穿戴装备)", affected)
		default:
			return nil, nil, fmt.Errorf("unknown bag category %q", op.Category)
		}

	case "sort":
		// Compact equipment bag into EquipmentSlots in template order.
		sort.Slice(bag.Equipment, func(i, j int) bool {
			if bag.Equipment[i].Template != bag.Equipment[j].Template {
				return bag.Equipment[i].Template < bag.Equipment[j].Template
			}
			return bag.Equipment[i].Slot < bag.Equipment[j].Slot
		})
		for i := range bag.Equipment {
			bag.Equipment[i].Slot = rules.EquipmentSlots[0] + uint16(i)
		}
		// Keep quick slots untouched; merge & compact stackables per category range.
		var quick, other []inventory.BagItem
		for _, it := range bag.Items {
			if rules.Quick(it.Slot) {
				quick = append(quick, it)
			} else {
				other = append(other, it)
			}
		}
		ranges := [][2]uint16{
			rules.Slots["[throw]"],
			rules.Slots["[material]"],
			rules.Slots["[quest]"],
			rules.Slots["[material expert job]"],
		}
		compacted := append([]inventory.BagItem(nil), quick...)
		for _, rng := range ranges {
			if rng[0] == 0 {
				continue
			}
			totals := map[uint32]uint64{}
			var order []uint32
			for _, it := range other {
				if it.Slot >= rng[0] && it.Slot <= rng[1] {
					if _, seen := totals[it.Template]; !seen {
						order = append(order, it.Template)
					}
					totals[it.Template] += uint64(it.Amount)
				}
			}
			sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
			slot := rng[0]
			for _, tpl := range order {
				limit := uint64(rules.MissingStackLimit)
				if def, ok := s.Awarder.Catalog.Items[tpl]; ok && def.StackLimit > 0 {
					limit = uint64(def.StackLimit)
				}
				if limit == 0 {
					limit = 1000
				}
				rem := totals[tpl]
				for rem > 0 && slot <= rng[1] {
					chunk := rem
					if chunk > limit {
						chunk = limit
					}
					compacted = append(compacted, inventory.BagItem{Slot: slot, Template: tpl, Amount: uint32(chunk)})
					rem -= chunk
					slot++
				}
			}
		}
		affected = len(bag.Equipment) + len(compacted)
		bag.Items = compacted
		summary = fmt.Sprintf("背包已整理（装备 %d · 道具 %d）", len(bag.Equipment), len(bag.Items))

	case "repair_all":
		if s.Awarder.Equipment == nil {
			return nil, nil, fmt.Errorf("equipment catalog unavailable")
		}
		for i := range bag.Equipment {
			if dur, err := s.Awarder.Equipment.Reward(bag.Equipment[i].Template); err == nil && dur > bag.Equipment[i].Durability {
				bag.Equipment[i].Durability = dur
				affected++
			}
		}
		for i := range bag.Worn {
			if dur, err := s.Awarder.Equipment.Reward(bag.Worn[i].Template); err == nil && dur > bag.Worn[i].Durability {
				bag.Worn[i].Durability = dur
				affected++
			}
		}
		summary = fmt.Sprintf("耐久度已修复 (%d 件装备)", affected)

	default:
		return nil, nil, fmt.Errorf("unsupported bag op %q", op.Op)
	}

	newState, err := inventory.SaveBag(role.State, bag)
	if err != nil {
		return nil, nil, err
	}
	rec := OpReceipt{
		ID:          op.ID,
		Action:      "bag",
		Op:          op.Op,
		CharacterID: role.ID,
		GoldAfter:   bag.Gold,
		Affected:    affected,
		Summary:     summary,
	}
	b, err := json.Marshal(rec)
	return newState, b, err
}

func questIsEpic(d catalog.QuestDefinition) bool {
	for i, c := range d.Script.Cells {
		if c.Type == 3 && c.Text == "[grade]" && i+1 < len(d.Script.Cells) {
			return d.Script.Cells[i+1].Text == "[epic]"
		}
	}
	return false
}

func questGrowMatches(d catalog.QuestDefinition, adv byte) bool {
	on := false
	for _, c := range d.Script.Cells {
		if c.Type == 3 {
			on = c.Text == "[grow type]"
			continue
		}
		if on && c.Type == 0 && c.Value >= 0 && c.Value != int32(adv) {
			return false
		}
	}
	return true
}

func questJobMatches(d catalog.QuestDefinition, job string) bool {
	if len(d.Jobs) == 0 {
		return job != ""
	}
	for _, j := range d.Jobs {
		if j == "[all]" || j == job {
			return true
		}
	}
	return false
}

func (s *Service) execQuest(atx *storage.AdminTx, role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil {
		return nil, nil, fmt.Errorf("quest op requires character_id")
	}
	if s.Quests == nil {
		return nil, nil, fmt.Errorf("quest service not configured")
	}
	checksum := s.Quests.Catalog.Source.Checksum
	affected := 0
	var summary string

	switch op.Op {
	case "complete_objective":
		quests, err := atx.Quests()
		if err != nil {
			return nil, nil, err
		}
		for _, q := range quests {
			if q.Status != "accepted" {
				continue
			}
			if op.QuestID != 0 && q.ID != op.QuestID {
				continue
			}
			q.Progress = 0
			if err = atx.PutQuest(q); err != nil {
				return nil, nil, err
			}
			affected++
		}
		if affected == 0 {
			return nil, nil, fmt.Errorf("没有符合条件的进行中任务")
		}
		if op.QuestID != 0 {
			summary = fmt.Sprintf("任务 %d 目标已完成（可立即交付）", op.QuestID)
		} else {
			summary = fmt.Sprintf("已将 %d 个进行中任务目标设为可交付", affected)
		}

	case "finish":
		if op.QuestID == 0 || op.QuestID == 65535 {
			return nil, nil, fmt.Errorf("invalid quest_id")
		}
		d, ok := s.Quests.Catalog.Quests[uint32(op.QuestID)]
		if !ok {
			return nil, nil, fmt.Errorf("任务 %d 不存在于目录中", op.QuestID)
		}
		_, model, err := quest.InitialProgress(d)
		if err != nil {
			model = "gm-completed-v1"
		}
		if err = atx.PutQuest(storage.QuestState{
			ID:            op.QuestID,
			Status:        "completed",
			Progress:      0,
			ConfigVersion: checksum,
			ProgressModel: model,
		}); err != nil {
			return nil, nil, err
		}
		affected = 1
		summary = fmt.Sprintf("任务 %d 已标记完成", op.QuestID)

	case "finish_all_active":
		quests, err := atx.Quests()
		if err != nil {
			return nil, nil, err
		}
		for _, q := range quests {
			if q.Status != "accepted" {
				continue
			}
			q.Status = "completed"
			q.Progress = 0
			if err = atx.PutQuest(q); err != nil {
				return nil, nil, err
			}
			affected++
		}
		summary = fmt.Sprintf("已直接完成 %d 个进行中任务", affected)

	case "complete_epic_to_level":
		var state character.State
		if err := json.Unmarshal(role.State, &state); err != nil {
			return nil, nil, err
		}
		maxLv := op.MaxLevel
		if maxLv == 0 {
			maxLv = uint32(state.Level)
		}
		job := ""
		if s.Characters != nil {
			job = s.Characters.Catalog.Professions[role.Profession].Job
		}
		existing, err := atx.Quests()
		if err != nil {
			return nil, nil, err
		}
		doneMap := map[uint16]bool{}
		for _, q := range existing {
			if q.Status == "completed" {
				doneMap[q.ID] = true
			}
		}
		var ids []int
		for id := range s.Quests.Catalog.Quests {
			if id > 0 && id < 40000 {
				ids = append(ids, int(id))
			}
		}
		sort.Ints(ids)
		for _, rawID := range ids {
			qid := uint16(rawID)
			if doneMap[qid] {
				continue
			}
			d := s.Quests.Catalog.Quests[uint32(qid)]
			if !questIsEpic(d) || d.MinimumLevel > maxLv {
				continue
			}
			if !questJobMatches(d, job) || !questGrowMatches(d, state.Advancement) {
				continue
			}
			_, model, err := quest.InitialProgress(d)
			if err != nil {
				model = "gm-completed-v1"
			}
			if err = atx.PutQuest(storage.QuestState{
				ID:            qid,
				Status:        "completed",
				Progress:      0,
				ConfigVersion: checksum,
				ProgressModel: model,
			}); err != nil {
				return nil, nil, err
			}
			affected++
		}
		summary = fmt.Sprintf("已完成 Lv.%d 及以下主线任务 %d 个", maxLv, affected)

	case "accept":
		if op.QuestID == 0 || op.QuestID == 65535 {
			return nil, nil, fmt.Errorf("invalid quest_id")
		}
		d, ok := s.Quests.Catalog.Quests[uint32(op.QuestID)]
		if !ok {
			return nil, nil, fmt.Errorf("任务 %d 不存在于目录中", op.QuestID)
		}
		initial, model, err := quest.InitialProgress(d)
		if err != nil {
			return nil, nil, fmt.Errorf("任务 %d 类型(%s)暂不支持作为进行中任务接取，可直接标记完成", op.QuestID, d.Kind)
		}
		if !op.Force {
			var state character.State
			if err := json.Unmarshal(role.State, &state); err != nil {
				return nil, nil, err
			}
			if uint32(state.Level) < d.MinimumLevel || uint32(state.Level) > d.MaximumLevel {
				return nil, nil, fmt.Errorf("角色等级不满足任务要求 (Lv.%d..%d)，可勾选强制接取", d.MinimumLevel, d.MaximumLevel)
			}
		}
		if err = atx.PutQuest(storage.QuestState{
			ID:            op.QuestID,
			Status:        "accepted",
			Progress:      initial,
			ConfigVersion: checksum,
			ProgressModel: model,
		}); err != nil {
			return nil, nil, err
		}
		affected = 1
		summary = fmt.Sprintf("已接取任务 %d（初始进度 %d）", op.QuestID, initial)

	case "abandon":
		if op.QuestID == 0 || op.QuestID == 65535 {
			return nil, nil, fmt.Errorf("invalid quest_id")
		}
		if _, ok, err := atx.GetQuest(op.QuestID); err != nil {
			return nil, nil, err
		} else if !ok {
			return nil, nil, fmt.Errorf("角色没有任务 %d 的记录", op.QuestID)
		}
		atx.DeleteQuest(op.QuestID)
		affected = 1
		summary = fmt.Sprintf("已放弃/重置任务 %d", op.QuestID)

	default:
		return nil, nil, fmt.Errorf("unsupported quest op %q", op.Op)
	}

	rec := OpReceipt{
		ID:          op.ID,
		Action:      "quest",
		Op:          op.Op,
		CharacterID: role.ID,
		Affected:    affected,
		Summary:     summary,
	}
	b, err := json.Marshal(rec)
	return nil, b, err
}

func (s *Service) applyQuestFinishWithReward(ctx context.Context, op CharacterOp) (OpReceipt, bool, error) {
	var out OpReceipt
	if op.CharacterID == 0 || op.QuestID == 0 || op.QuestID == 65535 || s.Quests == nil {
		return out, false, fmt.Errorf("invalid quest finish request")
	}
	// Step 1: ensure quest is accepted with progress=0 so Quests.Finish can settle it.
	settleOp := op
	settleOp.ID = op.ID + ":prep"
	settleOp.Op = "complete_objective"
	reqBytes, _ := json.Marshal(settleOp)
	_, err := s.Store.ApplyAdminOp(ctx, storage.AdminOpRequest{
		ID:          settleOp.ID,
		AccountID:   op.AccountID,
		CharacterID: op.CharacterID,
		Operator:    op.Operator,
		Reason:      op.Reason,
		Request:     reqBytes,
	}, func(atx *storage.AdminTx, role *storage.Character) (json.RawMessage, json.RawMessage, error) {
		d, ok := s.Quests.Catalog.Quests[uint32(op.QuestID)]
		if !ok {
			return nil, nil, fmt.Errorf("任务 %d 不存在", op.QuestID)
		}
		_, model, err := quest.InitialProgress(d)
		if err != nil {
			return nil, nil, err
		}
		if err = atx.PutQuest(storage.QuestState{
			ID:            op.QuestID,
			Status:        "accepted",
			Progress:      0,
			ConfigVersion: s.Quests.Catalog.Source.Checksum,
			ProgressModel: model,
		}); err != nil {
			return nil, nil, err
		}
		return nil, json.RawMessage(`{"prepared":true}`), nil
	})
	if err != nil {
		return out, false, err
	}
	role, err := s.Store.LiveCharacter(op.AccountID, op.CharacterID)
	if err != nil {
		return out, false, err
	}
	fin, err := s.Quests.Finish(ctx, role, protocol.QuestSubmitRequest{ID: op.QuestID, RewardSelection: 65535, Option: 1})
	if err != nil {
		return out, false, err
	}
	out = OpReceipt{
		ID:          op.ID,
		Action:      "quest",
		Op:          "finish",
		CharacterID: op.CharacterID,
		Affected:    1,
		Summary:     fmt.Sprintf("任务 %d 已结案并发放奖励（EXP +%d，金币 +%d，物品 %d 件）", op.QuestID, fin.Receipt.Experience, fin.Receipt.Gold, len(fin.Receipt.Items)),
	}
	return out, fin.Applied, nil
}

func (s *Service) execAvatar(role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil || op.CharacterID <= 0 {
		return nil, nil, fmt.Errorf("发放时装需要指定有效角色")
	}
	if len(op.AvatarIDs) == 0 || len(op.AvatarIDs) > 16 {
		return nil, nil, fmt.Errorf("单次发放时装数量需在 1 到 16 件之间")
	}
	b, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, nil, err
	}
	b, slots, err := b.AddAvatars(op.AvatarIDs)
	if err != nil {
		return nil, nil, err
	}
	nextState, err := inventory.SaveBag(role.State, b)
	if err != nil {
		return nil, nil, err
	}
	slotStrs := make([]string, len(slots))
	for i, sl := range slots {
		slotStrs[i] = fmt.Sprintf("%d", sl)
	}
	rec := OpReceipt{
		ID:          op.ID,
		Action:      "avatar",
		Op:          "grant",
		CharacterID: role.ID,
		Affected:    len(slots),
		Summary:     fmt.Sprintf("已实时发放 %d 件时装至装扮栏（槽位 %s）", len(slots), strings.Join(slotStrs, ", ")),
	}
	recBytes, err := json.Marshal(rec)
	return nextState, recBytes, err
}
