package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"dfolan/internal/catalog"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Source-backed DANJI configuration from US100 Script.pvf:
// Live/Else/Univ/2022/0712_DANJI/script/NewLostTreasure.evt
const (
	danjiPotTemplate uint32 = 590004385
	danjiStackMax    uint32 = 942
	danjiStackTable         = "danji_stack_v224"
	danjiRateScale   uint32 = 1000000
)

type danjiWeightedItem struct {
	Template uint32
	Count    uint16
	Weight   uint32
}

type danjiMilestone struct {
	Threshold uint32
	Template  uint32
	Count     uint16
}

type danjiTierConfig struct {
	Cost     uint32
	StackAdd uint32
	RateS    uint32 // out of danjiRateScale (from [level] in NewLostTreasure.evt)
	RateA    uint32 // out of danjiRateScale
}

var danjiTiers = map[byte]danjiTierConfig{
	1: {Cost: 1, StackAdd: 1, RateS: 680, RateA: 50000},
	2: {Cost: 2, StackAdd: 2, RateS: 2000, RateA: 120000},
	3: {Cost: 3, StackAdd: 3, RateS: 6200, RateA: 250000},
}

// [DANJI Stack Reward] from NewLostTreasure.evt
var danjiMilestones = []danjiMilestone{
	{Threshold: 236, Template: 590006306, Count: 1},
	{Threshold: 471, Template: 590006314, Count: 1},
	{Threshold: 706, Template: 590006322, Count: 1},
	{Threshold: 942, Template: 590006329, Count: 1},
}

// [Reward level S table] from NewLostTreasure.evt
var danjiGradeSPool = []danjiWeightedItem{
	{590704452, 1, 400000},
	{590704455, 1, 600000},
}

// [Reward level A table] from NewLostTreasure.evt
var danjiGradeAPool = []danjiWeightedItem{
	{590006229, 1, 300000},
	{590006230, 1, 200000},
	{590006231, 1, 100000},
	{590006232, 1, 100000},
	{590006233, 1, 50000},
	{590006247, 1, 50000},
	{590006261, 1, 20000},
	{590006262, 1, 30000},
	{590006263, 1, 20000},
	{590006264, 1, 50000},
	{590006265, 1, 50000},
	{590006266, 1, 30000},
}

// [Reward level B table] from NewLostTreasure.evt
var danjiGradeBPool = []danjiWeightedItem{
	{7740, 10, 50000},
	{7740, 20, 40000},
	{7740, 50, 30000},
	{7741, 10, 50000},
	{7741, 20, 40000},
	{7741, 50, 30000},
	{7742, 10, 50000},
	{7742, 20, 40000},
	{7742, 50, 30000},
	{590006267, 1, 40000},
	{590006268, 1, 20000},
	{2660671, 10, 50000},
	{2660671, 25, 25000},
	{590006269, 4, 20000},
	{590006270, 10, 10000},
	{590006271, 20, 5000},
	{590006272, 1, 19500},
	{590006273, 1, 19500},
	{590006274, 1, 19500},
	{590006275, 1, 5000},
	{590006276, 1, 5000},
	{590006277, 1, 5000},
	{590006278, 1, 2000},
	{590006290, 1, 2000},
	{590006302, 1, 10000},
	{590006303, 1, 10000},
	{590006304, 1, 10000},
	{590006305, 1, 10000},
	{10327283, 5, 50000},
	{10327283, 10, 50000},
	{10327283, 15, 30000},
	{10327283, 20, 20000},
	{10327283, 25, 15000},
	{10327283, 30, 12500},
	{10327286, 5, 50000},
	{10327286, 10, 50000},
	{10327286, 15, 30000},
	{10327286, 20, 20000},
	{10327286, 25, 15000},
	{10327286, 30, 10000},
}

type danjiStateRecord struct {
	Stack uint32 `json:"stack"`
}

func danjiAnimLevel(stack uint32) byte {
	switch {
	case stack >= 820:
		return 5
	case stack >= 706:
		return 4
	case stack >= 471:
		return 3
	case stack >= 236:
		return 2
	default:
		return 1
	}
}

func danjiAccountKey(account int64) string {
	return strconv.FormatInt(account, 10)
}

func loadDanjiStack(store *storage.Store, account int64) (uint32, error) {
	if store == nil {
		return 0, nil
	}
	var rec danjiStateRecord
	ok, err := store.Database().Get(danjiStackTable, danjiAccountKey(account), &rec)
	if err != nil || !ok {
		return 0, err
	}
	if rec.Stack >= danjiStackMax {
		rec.Stack %= danjiStackMax
	}
	return rec.Stack, nil
}

func saveDanjiStack(store *storage.Store, account int64, stack uint32) error {
	if store == nil {
		return nil
	}
	return store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put(danjiStackTable, danjiAccountKey(account), danjiStateRecord{Stack: stack})
	})
}

func danjiStatusOnEntry(store *storage.Store, account int64) ([]byte, error) {
	stack, err := loadDanjiStack(store, account)
	if err != nil {
		return nil, err
	}
	return protocol.DanjiStatus(stack, 0, danjiAnimLevel(stack)), nil
}

func isAccountEventSpace2AItem(tpl uint32) bool {
	return tpl == danjiPotTemplate || tpl == ev4189CoinItem || tpl == ev4189AltCoinItem
}

func danjiPotTotal(b inventory.Bag) uint32 {
	var total uint64
	for _, it := range b.Items {
		if it.Template == danjiPotTemplate {
			total += uint64(it.Amount)
		}
	}
	if total > 0xffffffff {
		return 0xffffffff
	}
	return uint32(total)
}

func capsuleCoinTotal(b inventory.Bag) uint32 {
	var total uint64
	for _, it := range b.Items {
		if it.Template == ev4189CoinItem || it.Template == ev4189AltCoinItem {
			total += uint64(it.Amount)
		}
	}
	if total > 0xffffffff {
		return 0xffffffff
	}
	return uint32(total)
}

// danjiSpaceRestorePayload builds NOTI 13 for inventory space 0x2a (42, global
// 0x149788e68, [stack in account inventory]), which both the DANJI UI
// (590004385) and Event 4189 Neo Capsule Machine (590005793, 0x140242445 /
// 0x140242e7a) query via GetItemCount(0x143b1e470).
func danjiSpaceRestorePayload(state json.RawMessage) ([]byte, error) {
	b, err := inventory.ReadBag(state)
	if err != nil {
		return nil, err
	}
	var rows [][protocol.CurrentItemRecordSize]byte
	if n := danjiPotTotal(b); n > 0 {
		rows = append(rows, protocol.OrdinaryItem(0, danjiPotTemplate, n))
	}
	if n := capsuleCoinTotal(b); n > 0 {
		rows = append(rows, protocol.OrdinaryItem(1, ev4189CoinItem, n))
	}
	return protocol.US224DanjiInventory(rows)
}

// danjiSpaceUpdatePayload builds NOTI 14 for inventory space 0x2a (42) after
// DANJI pot or Event 4189 capsule coin acquisition/consumption, clearing slot 0
// or slot 1 when its count hits 0.
func danjiSpaceUpdatePayload(b inventory.Bag) ([]byte, error) {
	row0 := protocol.EmptySlotItem(0)
	if n := danjiPotTotal(b); n > 0 {
		row0 = protocol.OrdinaryItem(0, danjiPotTemplate, n)
	}
	row1 := protocol.EmptySlotItem(1)
	if n := capsuleCoinTotal(b); n > 0 {
		row1 = protocol.OrdinaryItem(1, ev4189CoinItem, n)
	}
	return protocol.US224DanjiInventory([][protocol.CurrentItemRecordSize]byte{row0, row1})
}

func cryptoRandUint32(bound uint32) uint32 {
	if bound <= 1 {
		return 0
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano()) % bound
	}
	return binary.LittleEndian.Uint32(b[:]) % bound
}

func pickWeighted(pool []danjiWeightedItem, grade byte, nextRand func(uint32) uint32) protocol.DanjiReward {
	var total uint32
	for _, it := range pool {
		total += it.Weight
	}
	r := nextRand(total)
	var acc uint32
	for _, it := range pool {
		acc += it.Weight
		if r < acc {
			return protocol.DanjiReward{Template: it.Template, Grade: grade, Count: it.Count}
		}
	}
	last := pool[len(pool)-1]
	return protocol.DanjiReward{Template: last.Template, Grade: grade, Count: last.Count}
}

func rollDanjiRewards(tier byte, count int, nextRand func(uint32) uint32) []protocol.DanjiReward {
	cfg := danjiTiers[tier]
	out := make([]protocol.DanjiReward, 0, count)
	for i := 0; i < count; i++ {
		gradeRoll := nextRand(danjiRateScale)
		switch {
		case gradeRoll < cfg.RateS:
			out = append(out, pickWeighted(danjiGradeSPool, protocol.DanjiGradeS, nextRand))
		case gradeRoll < cfg.RateS+cfg.RateA:
			out = append(out, pickWeighted(danjiGradeAPool, protocol.DanjiGradeA, nextRand))
		default:
			out = append(out, pickWeighted(danjiGradeBPool, protocol.DanjiGradeB, nextRand))
		}
	}
	return out
}

// advanceDanjiStack increments the DANJI stack by add and returns the updated
// stack (wrapped mod danjiStackMax) along with any crossed milestone rewards.
func advanceDanjiStack(prev, add uint32) (uint32, []protocol.DanjiReward) {
	var bonus []protocol.DanjiReward
	cur := prev
	for rem := add; rem > 0; {
		step := rem
		if cur+step > danjiStackMax {
			step = danjiStackMax - cur
		}
		next := cur + step
		for _, m := range danjiMilestones {
			if cur < m.Threshold && next >= m.Threshold {
				bonus = append(bonus, protocol.DanjiReward{Template: m.Template, Grade: protocol.DanjiGradeS, Count: m.Count})
			}
		}
		cur = next
		if cur >= danjiStackMax {
			cur = 0
		}
		rem -= step
	}
	return cur, bonus
}

type danjiOpenReceipt struct {
	Tier        byte                   `json:"tier"`
	Count       int32                  `json:"count"`
	Cost        uint32                 `json:"cost"`
	Rewards     []protocol.DanjiReward `json:"rewards"`
	Milestones  []protocol.DanjiReward `json:"milestones,omitempty"`
	StackAfter  uint32                 `json:"stack_after"`
	EmptySlots  []uint16               `json:"empty_slots,omitempty"`
}

// errDanjiBagFull and errDanjiInsufficientPots map cleanly to ACK 2052 refusal codes.
var (
	errDanjiBagFull         = fmt.Errorf("bag category is full")
	errDanjiInsufficientPot = fmt.Errorf("insufficient danji pots")
)

func openDanjiPot(
	ctx context.Context,
	store *storage.Store,
	account int64,
	role storage.Character,
	src catalog.LootCatalog,
	rules inventory.BagRules,
	rawFrame []byte,
	plaintext []byte,
	nextRand func(uint32) uint32,
) (storage.Character, []outboundPacket, uint16, error) {
	if nextRand == nil {
		nextRand = cryptoRandUint32
	}
	req, err := protocol.DecodeDanjiOpen(plaintext)
	if err != nil {
		return role, nil, 0x101, err
	}
	cfg, ok := danjiTiers[req.Tier]
	if !ok {
		return role, nil, 0x101, fmt.Errorf("unknown danji tier %d", req.Tier)
	}
	need := cfg.Cost * uint32(req.Count)
	stackAdd := cfg.StackAdd * uint32(req.Count)

	prevStack, err := loadDanjiStack(store, account)
	if err != nil {
		return role, nil, 0x101, err
	}

	rewards := rollDanjiRewards(req.Tier, int(req.Count), nextRand)
	nextStack, milestoneRewards := advanceDanjiStack(prevStack, stackAdd)
	allDeliveries := append([]protocol.DanjiReward(nil), rewards...)
	allDeliveries = append(allDeliveries, milestoneRewards...)

	// Contracts rolled from DANJI jars (590006272..590006277) activate directly
	// on the account instead of occupying bag slots.
	contractDays := map[byte]int64{}
	var bagDeliveries []protocol.DanjiReward
	for _, rw := range allDeliveries {
		if kind, days, ok := directContractItem(rw.Template); ok {
			contractDays[kind] += days * int64(rw.Count)
		} else {
			bagDeliveries = append(bagDeliveries, rw)
		}
	}

	var receipt danjiOpenReceipt
	key := fmt.Sprintf("danji2052/%d/%x", role.ID, sha256.Sum256(rawFrame))

	saved, applied, err := store.CommitCharacterEvent(ctx, account, role.ID, src.Source.Checksum, key, "US224-danji-open-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		if rules.Source != current.ConfigVersion || src.Source.Checksum != current.ConfigVersion {
			return nil, nil, fmt.Errorf("danji source mismatch")
		}
		b, e := inventory.ReadBag(current.State)
		if e != nil {
			return nil, nil, e
		}
		// Verify total DANJI pots across bag >= need, prioritizing req.Slot first.
		var total uint64
		for _, it := range b.Items {
			if it.Template == danjiPotTemplate {
				total += uint64(it.Amount)
			}
		}
		if total < uint64(need) {
			return nil, nil, errDanjiInsufficientPot
		}

		// Consume DANJI pots starting from the client's requested slot, then remaining slots in order.
		rem := need
		var emptySlots []uint16
		consumeAt := func(idx int) {
			if rem == 0 {
				return
			}
			take := rem
			if b.Items[idx].Amount < take {
				take = b.Items[idx].Amount
			}
			b.Items[idx].Amount -= take
			rem -= take
			if b.Items[idx].Amount == 0 {
				emptySlots = append(emptySlots, b.Items[idx].Slot)
			}
		}
		if req.Slot > 0 {
			for i := range b.Items {
				if b.Items[i].Slot == uint16(req.Slot) && b.Items[i].Template == danjiPotTemplate {
					consumeAt(i)
					break
				}
			}
		}
		for i := range b.Items {
			if rem == 0 {
				break
			}
			if b.Items[i].Template == danjiPotTemplate && b.Items[i].Amount > 0 {
				consumeAt(i)
			}
		}
		filtered := b.Items[:0]
		for _, it := range b.Items {
			if it.Amount > 0 {
				filtered = append(filtered, it)
			}
		}
		b.Items = filtered

		// Build local catalog with exact PVF bag tabs ([material], [material expert job], [throw]).
		local := catalog.LootCatalog{Source: src.Source, Items: map[uint32]catalog.LootItem{}}
		for _, rw := range bagDeliveries {
			local.Items[rw.Template] = localCatalogEntry(src, rw.Template, uint32(rw.Count))
		}
		for _, rw := range bagDeliveries {
			b, _, e = b.Add(local, rules, rw.Template, uint32(rw.Count))
			if e != nil {
				return nil, nil, e
			}
		}
		state, e := inventory.SaveBag(current.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt = danjiOpenReceipt{
			Tier:       req.Tier,
			Count:      req.Count,
			Cost:       need,
			Rewards:    rewards,
			Milestones: milestoneRewards,
			StackAfter: nextStack,
			EmptySlots: emptySlots,
		}
		rawReceipt, e := json.Marshal(receipt)
		return state, rawReceipt, e
	})
	if err != nil {
		code := uint16(0x101)
		if err.Error() == errDanjiBagFull.Error() {
			code = 0x16 // 22: bag full (checked at 0x140398f8d -> string 0x89c6)
		}
		return role, nil, code, err
	}

	if applied {
		if err = saveDanjiStack(store, account, nextStack); err != nil {
			return saved, nil, 0x101, err
		}
		if len(contractDays) > 0 {
			if err = store.ExtendPremiumContracts(ctx, account, contractDays); err != nil {
				return saved, nil, 0x101, err
			}
		}
	} else {
		// Replayed identical frame: keep current persisted stack and reload receipt if needed.
		nextStack, _ = loadDanjiStack(store, account)
		if len(receipt.Rewards) == 0 {
			receipt.Rewards = rewards
		}
	}

	bag, err := inventory.ReadBag(saved.State)
	if err != nil {
		return saved, nil, 0x101, err
	}
	rows := bag.Rows()
	occupied := make(map[uint16]bool, len(rows))
	for _, r := range rows {
		occupied[binary.LittleEndian.Uint16(r[:])] = true
	}
	for _, s := range receipt.EmptySlots {
		if !occupied[s] {
			rows = append(rows, protocol.EmptySlotItem(s))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return binary.LittleEndian.Uint16(rows[i][:]) < binary.LittleEndian.Uint16(rows[j][:])
	})
	invUpdate, err := protocol.US224InventoryUpdate(rows)
	if err != nil {
		return saved, nil, 0x101, err
	}
	danjiSpaceUpdate, err := danjiSpaceUpdatePayload(bag)
	if err != nil {
		return saved, nil, 0x101, err
	}
	statusPayload := protocol.DanjiStatus(nextStack, 0, danjiAnimLevel(nextStack))
	ackPayload, err := protocol.DanjiOpenSuccess(receipt.Rewards)
	if err != nil {
		return saved, nil, 0x101, err
	}

	saved.WireID = role.WireID
	plan := []outboundPacket{
		{"danji_inventory_updated", 0, 14, invUpdate},
		{"danji_space_updated", 0, 14, danjiSpaceUpdate},
		{"danji_status_updated", 0, 2279, statusPayload},
		{"danji_open_ack", 1, 2052, ackPayload},
	}
	if len(contractDays) > 0 {
		changed := make(map[byte]bool, len(contractDays))
		for k := range contractDays {
			changed[k] = true
		}
		if cpkts, cerr := buildContractRefreshPackets(ctx, store, nil, account, saved.ID, changed); cerr == nil {
			plan = append(plan, cpkts...)
		}
	}
	return saved, plan, 0, nil
}
