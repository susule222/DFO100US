package main

import (
	"context"
	"crypto/rand"
	"dfolan/internal/catalog"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const (
	gabrielContractKind      byte = 73
	unstableRiftContractKind byte = 125
)

func fundsPayload(cera, mileage uint64) []byte {
	if cera > math.MaxInt32 {
		cera = math.MaxInt32
	}
	if mileage > math.MaxInt32 {
		mileage = math.MaxInt32
	}
	p := binary.LittleEndian.AppendUint32([]byte{1}, uint32(cera))
	return binary.LittleEndian.AppendUint32(p, uint32(mileage))
}

func (w *worldSession) eventPolicy() events.Policy {
	if w == nil {
		return events.Neutral()
	}
	return events.LoadBestEffort(w.eventsPath)
}

func (w *worldSession) activityDay(now time.Time) string {
	if w != nil && w.fatigue != nil {
		return w.fatigue.Day(now)
	}
	return now.UTC().Format("2006-01-02")
}

func (w *worldSession) grantActivityReward(ctx context.Context, eventKey, model string, gold, template, amount uint32, mileage int64, dayKey string, dailyCap int64) ([]outboundPacket, error) {
	if w == nil || w.characters == nil || w.loot == nil || w.role.ID == 0 {
		return nil, nil
	}
	var out []outboundPacket
	if gold > 0 || (template > 0 && amount > 0) {
		saved, applied, err := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, eventKey, model, func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
			bag, err := inventory.ReadBag(current.State)
			if err != nil {
				return nil, nil, err
			}
			if gold > 0 {
				nextGold := uint64(bag.Gold) + uint64(gold)
				if nextGold > math.MaxUint32 {
					nextGold = math.MaxUint32
				}
				bag.Gold = uint32(nextGold)
			}
			if template > 0 && amount > 0 {
				if _, ok := w.loot.Catalog.Items[template]; ok {
					bag, _, err = bag.Add(w.loot.Catalog, w.loot.BagRules, template, amount)
				} else {
					limit := uint32(1000)
					if amount > limit {
						limit = amount
					}
					local := catalog.LootCatalog{
						Source: w.loot.Catalog.Source,
						Items: map[uint32]catalog.LootItem{
							template: {ID: template, Kind: "stackable", StackableType: "[throw]", StackLimit: limit},
						},
					}
					bag, _, err = bag.Add(local, w.loot.BagRules, template, amount)
				}
				if err != nil {
					return nil, nil, err
				}
			}
			state, err := inventory.SaveBag(current.State, bag)
			if err != nil {
				return nil, nil, err
			}
			receipt, err := json.Marshal(map[string]any{"gold": gold, "template": template, "amount": amount})
			return state, receipt, err
		})
		if err != nil {
			return nil, err
		}
		if applied {
			saved.WireID = w.role.WireID
			w.role = saved
			if bag, err := inventory.ReadBag(saved.State); err == nil {
				if upd, err := protocol.US224InventoryUpdate(bag.Rows()); err == nil {
					out = append(out, outboundPacket{"activity_reward_inventory", 0, 14, upd})
				}
			}
		}
	}
	if mileage > 0 {
		added, total, err := w.characters.Store.AddAccountMileage(ctx, w.account, mileage, dayKey, dailyCap)
		if err == nil && added > 0 {
			if cera, err := w.characters.Store.AccountCera(ctx, w.account); err == nil {
				out = append(out, outboundPacket{"activity_mileage_updated", 0, 53, fundsPayload(cera, total)})
			}
		}
	}
	return out, nil
}

func (w *worldSession) onActivityKill(ctx context.Context) []outboundPacket {
	if w == nil || w.characters == nil || w.role.ID == 0 {
		return nil
	}
	ev := w.eventPolicy()
	if !ev.DailyChallenge.Enabled {
		return nil
	}
	now := time.Now()
	day := w.activityDay(now)
	_, reached, err := w.characters.Store.RecordActivityKill(ctx, w.account, w.role.ID, day, ev.DailyChallenge.KillTarget)
	if err != nil || !reached {
		return nil
	}
	pkts, _ := w.grantActivityReward(
		ctx,
		fmt.Sprintf("daily_kill:%s", day),
		"us100-daily-challenge-kill-v1",
		ev.DailyChallenge.RewardGold,
		ev.DailyChallenge.RewardTemplate,
		ev.DailyChallenge.RewardAmount,
		ev.DailyChallenge.RewardMileage,
		day,
		0, // challenge bonus mileage is not capped by dungeon grind cap
	)
	return pkts
}

func (w *worldSession) onActivityClear(ctx context.Context) []outboundPacket {
	if w == nil || w.characters == nil || w.role.ID == 0 || w.activeDungeon == nil {
		return nil
	}
	ev := w.eventPolicy()
	now := time.Now()
	day := w.activityDay(now)
	week := ev.WeeklyCycleKey(now)
	var out []outboundPacket

	// 1. Mileage on dungeon clear (PVF etc/mileageshop.etc)
	if ev.Mileage.Enabled && ev.Mileage.DungeonClearPoints > 0 {
		added, total, err := w.characters.Store.AddAccountMileage(ctx, w.account, ev.Mileage.DungeonClearPoints, day, ev.Mileage.DailyCap)
		if err == nil && added > 0 {
			if cera, err := w.characters.Store.AccountCera(ctx, w.account); err == nil {
				out = append(out, outboundPacket{"clear_mileage_updated", 0, 53, fundsPayload(cera, total)})
			}
		}
	}

	// 2. Daily Clear Challenge & Weekly Dungeon counter (Tuesday reset, 6/week)
	clearTarget := 0
	if ev.DailyChallenge.Enabled {
		clearTarget = ev.DailyChallenge.ClearTarget
	}
	weeklyLimit := 0
	if ev.WeeklyDungeon.Enabled {
		weeklyLimit = ev.WeeklyDungeon.WeeklyLimit
	}
	if clearTarget > 0 || weeklyLimit > 0 || w.activeDungeon.HasHellRoom {
		prog, dailyReached, weeklyGranted, err := w.characters.Store.RecordActivityClear(
			ctx, w.account, w.role.ID, day, week, clearTarget, weeklyLimit, w.activeDungeon.HasHellRoom,
		)
		if err == nil {
			if dailyReached {
				if pkts, rerr := w.grantActivityReward(
					ctx,
					fmt.Sprintf("daily_clear:%s", day),
					"us100-daily-challenge-clear-v1",
					ev.DailyChallenge.RewardGold,
					ev.DailyChallenge.RewardTemplate,
					ev.DailyChallenge.RewardAmount,
					ev.DailyChallenge.RewardMileage,
					day,
					0,
				); rerr == nil {
					out = append(out, pkts...)
				}
			}
			if weeklyGranted {
				if pkts, rerr := w.grantActivityReward(
					ctx,
					fmt.Sprintf("weekly_clear:%s:%d", week, prog.WeeklyClears),
					"us100-weekly-dungeon-v1",
					ev.WeeklyDungeon.RewardGold,
					ev.WeeklyDungeon.RewardTemplate,
					ev.WeeklyDungeon.RewardAmount,
					ev.WeeklyDungeon.RewardMileage,
					day,
					0,
				); rerr == nil {
					out = append(out, pkts...)
				}
			}
		}
	}

	// 3. Gabriel's Contract (kind 73) clear supply bonus
	if ev.Contracts.GabrielBonus {
		if active, err := w.characters.Store.ActivePremiumKinds(w.account, now); err == nil && active[gabrielContractKind] {
			var roll uint32
			if binary.Read(rand.Reader, binary.LittleEndian, &roll) == nil && roll%100 < 35 {
				if pkts, rerr := w.grantActivityReward(
					ctx,
					fmt.Sprintf("gabriel:%s", w.activeDungeon.RunID),
					"us100-gabriel-contract-v1",
					50000,
					10327281, // Ryan Core
					15,
					0,
					day,
					0,
				); rerr == nil {
					out = append(out, pkts...)
				}
			}
		}
	}

	return out
}
