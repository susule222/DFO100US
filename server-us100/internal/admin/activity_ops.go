package admin

import (
	"dfolan/internal/catalog"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

var allContractKinds = []byte{22, 27, 73, 79, 92, 118, 120, 125}

// deliverStackableOrEquipment adds an item to a bag, allowing both standard
// loot/equipment catalog entries and US100 PVF stackables (e.g. mileageshop.etc
// or event boxes) via the same safe [throw] fallback used by premium_cart.go.
func (s *Service) deliverActivityItem(bag inventory.Bag, tpl, qty uint32) (inventory.Bag, error) {
	if tpl == 0 || qty == 0 {
		return bag, nil
	}
	if s.Awarder == nil {
		return bag, fmt.Errorf("inventory awarder not configured")
	}
	if inventory.IsTalismanTemplate(tpl) {
		if qty > uint32(inventory.TalismanSlotCount) {
			return bag, fmt.Errorf("invalid talisman award quantity %d", qty)
		}
		ids := make([]uint32, qty)
		for i := range ids {
			ids[i] = tpl
		}
		next, _, addErr := bag.AddTalismans(ids)
		return next, addErr
	}
	if _, ok := s.Awarder.Catalog.Items[tpl]; ok {
		next, _, err := bag.Add(s.Awarder.Catalog, s.Awarder.Rules, tpl, qty)
		return next, err
	}
	if s.Awarder.Equipment != nil {
		if _, err := s.Awarder.Equipment.Reward(tpl); err == nil {
			next, _, addErr := bag.AddEquipment(s.Awarder.Equipment, s.Awarder.Rules.EquipmentSlots, tpl, qty)
			return next, addErr
		}
	}
	limit := uint32(1000)
	if qty > limit {
		limit = qty
	}
	st := "[throw]"
	if inventory.IsEmblemTemplate(tpl) {
		st = inventory.EmblemBagCategory
	}
	local := catalog.LootCatalog{
		Source: s.Awarder.Catalog.Source,
		Items: map[uint32]catalog.LootItem{
			tpl: {ID: tpl, Kind: "stackable", StackableType: st, StackLimit: limit},
		},
	}
	next, _, err := bag.Add(local, s.Awarder.Rules, tpl, qty)
	return next, err
}

func (s *Service) execContract(atx *storage.AdminTx, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	nowUnix := time.Now().Unix()
	var kinds []byte
	if op.ContractKind == 255 {
		kinds = allContractKinds
	} else {
		if op.ContractKind == 0 || op.ContractKind >= 126 {
			return nil, nil, fmt.Errorf("无效的契约类型 %d", op.ContractKind)
		}
		kinds = []byte{op.ContractKind}
	}
	for _, k := range kinds {
		if _, err := atx.SetAccountPremiumContract(k, op.ContractDays, nowUnix); err != nil {
			return nil, nil, err
		}
	}
	var summary string
	if op.ContractDays == 0 {
		if op.ContractKind == 255 {
			summary = "已清除全部 8 项契约特权"
		} else {
			summary = fmt.Sprintf("已关闭契约类型 %d", op.ContractKind)
		}
	} else if op.ContractKind == 255 {
		summary = fmt.Sprintf("已为账号激活/延长全部 8 项契约 +%d 天", op.ContractDays)
	} else {
		summary = fmt.Sprintf("契约类型 %d 已激活/延长 +%d 天", op.ContractKind, op.ContractDays)
	}
	rec := OpReceipt{
		ID:          op.ID,
		Action:      "contract",
		CharacterID: op.CharacterID,
		Affected:    len(kinds),
		Summary:     summary,
	}
	b, err := json.Marshal(rec)
	return nil, b, err
}

func (s *Service) execMileageRedeem(atx *storage.AdminTx, role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil {
		return nil, nil, fmt.Errorf("积分兑换需要指定接收角色")
	}
	if op.MileageCost <= 0 || len(op.RewardItems) == 0 {
		return nil, nil, fmt.Errorf("无效的积分兑换请求")
	}
	cur, err := atx.AccountMileage()
	if err != nil {
		return nil, nil, err
	}
	if cur < op.MileageCost {
		return nil, nil, fmt.Errorf("商城积分不足（当前 %d，需要 %d）", cur, op.MileageCost)
	}
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, nil, err
	}
	for _, it := range op.RewardItems {
		if it.Template == 0 || it.Amount == 0 || it.Amount > 999 {
			return nil, nil, fmt.Errorf("无效的兑换物品")
		}
		bag, err = s.deliverActivityItem(bag, it.Template, it.Amount)
		if err != nil {
			return nil, nil, err
		}
	}
	if err = atx.SetAccountMileage(cur - op.MileageCost); err != nil {
		return nil, nil, err
	}
	newState, err := inventory.SaveBag(role.State, bag)
	if err != nil {
		return nil, nil, err
	}
	rec := OpReceipt{
		ID:             op.ID,
		Action:         "mileage_redeem",
		CharacterID:    role.ID,
		MileageBalance: uint64(cur - op.MileageCost),
		GoldAfter:      bag.Gold,
		Affected:       len(op.RewardItems),
		Summary:        fmt.Sprintf("已消耗 %d 积分兑换 %d 项商品（剩余 %d 积分）", op.MileageCost, len(op.RewardItems), cur-op.MileageCost),
	}
	b, err := json.Marshal(rec)
	return newState, b, err
}

func (s *Service) execActivityClaim(atx *storage.AdminTx, role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	if role == nil {
		return nil, nil, fmt.Errorf("活动领奖需要指定角色")
	}
	key := strings.TrimSpace(op.ActivityKey)
	if key == "" {
		return nil, nil, fmt.Errorf("缺少活动领取标识")
	}
	if op.ClaimDate != "" {
		key = key + ":" + strings.TrimSpace(op.ClaimDate)
	}
	if !op.Force {
		already, err := atx.ClaimCharacterActivity(key)
		if err != nil {
			return nil, nil, err
		}
		if already {
			return nil, nil, fmt.Errorf("该活动奖励（%s）已领取过", key)
		}
	} else {
		_, _ = atx.ClaimCharacterActivity(key)
	}
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, nil, err
	}
	var parts []string
	if op.RewardGold > 0 {
		nextGold := uint64(bag.Gold) + uint64(op.RewardGold)
		if nextGold > math.MaxUint32 {
			nextGold = math.MaxUint32
		}
		bag.Gold = uint32(nextGold)
		parts = append(parts, fmt.Sprintf("金币 +%d", op.RewardGold))
	}
	for _, it := range op.RewardItems {
		if it.Template == 0 || it.Amount == 0 || it.Amount > 999 {
			return nil, nil, fmt.Errorf("无效的活动奖励物品")
		}
		bag, err = s.deliverActivityItem(bag, it.Template, it.Amount)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, fmt.Sprintf("物品 %d × %d", it.Template, it.Amount))
	}
	var mileageAfter uint64
	if op.RewardMileage > 0 {
		cur, err := atx.AccountMileage()
		if err != nil {
			return nil, nil, err
		}
		next := cur + op.RewardMileage
		if next > math.MaxUint32 {
			next = math.MaxUint32
		}
		if err = atx.SetAccountMileage(next); err != nil {
			return nil, nil, err
		}
		mileageAfter = uint64(next)
		parts = append(parts, fmt.Sprintf("积分 +%d", op.RewardMileage))
	} else if cur, err := atx.AccountMileage(); err == nil {
		mileageAfter = uint64(cur)
	}
	if len(op.RewardContracts) > 0 && op.RewardContractDays > 0 {
		nowUnix := time.Now().Unix()
		for _, k := range op.RewardContracts {
			if _, err = atx.SetAccountPremiumContract(k, op.RewardContractDays, nowUnix); err != nil {
				return nil, nil, err
			}
		}
		parts = append(parts, fmt.Sprintf("%d 项契约 +%d 天", len(op.RewardContracts), op.RewardContractDays))
	}
	newState, err := inventory.SaveBag(role.State, bag)
	if err != nil {
		return nil, nil, err
	}
	if len(parts) == 0 {
		parts = append(parts, "活动状态已同步")
	}
	rec := OpReceipt{
		ID:             op.ID,
		Action:         "activity_claim",
		Op:             key,
		CharacterID:    role.ID,
		MileageBalance: mileageAfter,
		GoldAfter:      bag.Gold,
		Affected:       len(op.RewardItems),
		Summary:        "活动领取成功：" + strings.Join(parts, "，"),
	}
	b, err := json.Marshal(rec)
	return newState, b, err
}

func (s *Service) execRefreshSync(atx *storage.AdminTx, role *storage.Character, op CharacterOp) (json.RawMessage, json.RawMessage, error) {
	cera, _ := atx.AccountCera()
	mileage, _ := atx.AccountMileage()
	rec := OpReceipt{
		ID:             op.ID,
		Action:         "refresh_sync",
		CharacterID:    op.CharacterID,
		CeraBalance:    uint64(cera),
		MileageBalance: uint64(mileage),
		Summary:        "已触发在线网关活动/疲劳/积分/契约即时刷新",
	}
	b, err := json.Marshal(rec)
	return nil, b, err
}
