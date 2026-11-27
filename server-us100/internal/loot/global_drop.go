package loot

import (
	"dfolan/internal/catalog"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
)

// DifficultyEquipmentDrop configures global equipment drops for one DNF dungeon
// difficulty tier (0=普通, 1=冒险, 2=勇士, 3=王者, 4=噩梦).
type DifficultyEquipmentDrop struct {
	Difficulty         int            `json:"difficulty"`
	Name               string         `json:"name,omitempty"`
	Enabled            bool           `json:"enabled"`
	DropRateMultiplier float64        `json:"drop_rate_multiplier"`
	EquipDropChance    int            `json:"equip_drop_chance"`
	DropCount          int            `json:"drop_count"`
	LevelMatch         bool           `json:"level_match"`
	RarityWeights      map[string]int `json:"rarity_weights"`
	CustomItemIDs      []uint32       `json:"custom_item_ids"`
}

// GlobalEquipmentPolicy controls per-difficulty equipment drops across all runs.
type GlobalEquipmentPolicy struct {
	Enabled      bool                      `json:"enabled"`
	Difficulties []DifficultyEquipmentDrop `json:"difficulties"`
}

// NormalAbyssPolicy controls ordinary (Lv.1–95 / classic) abyss equipment drops.
type NormalAbyssPolicy struct {
	Enabled       bool           `json:"enabled"`
	Scope         string         `json:"scope"` // "all", "boss_only", "maps"
	Maps          []uint32       `json:"maps"`
	DropChance    int            `json:"drop_chance"` // 0-100
	DropCount     int            `json:"drop_count"`  // 1-5
	PoolMode      string         `json:"pool_mode"`   // "level_epic", "all_epic", "custom"
	RarityWeights map[string]int `json:"rarity_weights"`
	CustomItemIDs []uint32       `json:"custom_item_ids"`
}

// Lv100AbyssPolicy controls Lv.100 Wisdom's Guide (智慧的引导) epic & mythic drops.
type Lv100AbyssPolicy struct {
	Enabled       bool     `json:"enabled"`
	Scope         string   `json:"scope"` // "all", "lv100_dungeons", "boss_only", "maps"
	Maps          []uint32 `json:"maps"`
	DropChance    int      `json:"drop_chance"`   // 0-100
	DropCount     int      `json:"drop_count"`    // 1-5
	EpicWeight    int      `json:"epic_weight"`   // 0-100
	MythicWeight  int      `json:"mythic_weight"` // 0-100
	PoolMode      string   `json:"pool_mode"`     // "lv100_all", "lv100_epic", "lv100_mythic", "custom"
	CustomItemIDs []uint32 `json:"custom_item_ids"`
}

// GlobalDropPolicy is the unified gateway drop policy saved by the launcher to
// runtime/launcher-gm/global_drop.json.
type GlobalDropPolicy struct {
	Version     int                   `json:"version"`
	Global      GlobalEquipmentPolicy `json:"global"`
	NormalAbyss NormalAbyssPolicy     `json:"normal_abyss"`
	Lv100Abyss  Lv100AbyssPolicy      `json:"lv100_abyss"`
}

// LoadGlobalDrop reads and sanitizes global_drop.json. Missing files return
// (nil, nil) so default source drops remain unchanged.
func LoadGlobalDrop(path string, gear *inventory.EquipmentCatalog, cat catalog.LootCatalog) (*GlobalDropPolicy, error) {
	if path == "" || gear == nil {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p GlobalDropPolicy
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	filterIDs := func(ids []uint32) []uint32 {
		out := make([]uint32, 0, len(ids))
		seen := map[uint32]bool{}
		for _, id := range ids {
			if id == 0 || seen[id] {
				continue
			}
			if cat.Items[id].Kind == "stackable" {
				continue
			}
			if _, err := gear.Reward(id); err != nil {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		return out
	}
	for i := range p.Global.Difficulties {
		d := &p.Global.Difficulties[i]
		if d.EquipDropChance < 0 {
			d.EquipDropChance = 0
		}
		if d.EquipDropChance > 100 {
			d.EquipDropChance = 100
		}
		if d.DropRateMultiplier <= 0 || math.IsNaN(d.DropRateMultiplier) || math.IsInf(d.DropRateMultiplier, 0) {
			d.DropRateMultiplier = 1.0
		}
		if d.DropCount < 1 {
			d.DropCount = 1
		}
		if d.DropCount > 5 {
			d.DropCount = 5
		}
		d.CustomItemIDs = filterIDs(d.CustomItemIDs)
	}
	clampAbyss := func(chance, count *int) {
		if *chance < 0 {
			*chance = 0
		}
		if *chance > 100 {
			*chance = 100
		}
		if *count < 1 {
			*count = 1
		}
		if *count > 5 {
			*count = 5
		}
	}
	clampAbyss(&p.NormalAbyss.DropChance, &p.NormalAbyss.DropCount)
	p.NormalAbyss.CustomItemIDs = filterIDs(p.NormalAbyss.CustomItemIDs)
	clampAbyss(&p.Lv100Abyss.DropChance, &p.Lv100Abyss.DropCount)
	if p.Lv100Abyss.EpicWeight < 0 {
		p.Lv100Abyss.EpicWeight = 0
	}
	if p.Lv100Abyss.MythicWeight < 0 {
		p.Lv100Abyss.MythicWeight = 0
	}
	p.Lv100Abyss.CustomItemIDs = filterIDs(p.Lv100Abyss.CustomItemIDs)
	if !p.Global.Enabled && !p.NormalAbyss.Enabled && !p.Lv100Abyss.Enabled {
		return nil, nil
	}
	return &p, nil
}

func mapIncluded(maps []uint32, room uint32) bool {
	for _, id := range maps {
		if id == room {
			return true
		}
	}
	return false
}

func pickWeightedRarity(rng *RNG, weights map[string]int, fallback int32) int32 {
	type pair struct {
		rar    int32
		weight int
	}
	// Fixed rarity iteration order for deterministic RNG behavior.
	order := []int32{0, 1, 2, 3, 5, 6, 4, 7}
	var pairs []pair
	total := 0
	for _, r := range order {
		w := weights[strconv.Itoa(int(r))]
		if w > 0 {
			pairs = append(pairs, pair{r, w})
			total += w
		}
	}
	if total <= 0 {
		return fallback
	}
	roll := int(rng.Next(uint32(total)))
	acc := 0
	for _, p := range pairs {
		acc += p.weight
		if roll < acc {
			return p.rar
		}
	}
	return pairs[len(pairs)-1].rar
}

func filterRewardPool(pool []inventory.RewardDrop, cat catalog.LootCatalog, pred func(inventory.RewardDrop) bool) []inventory.RewardDrop {
	var out []inventory.RewardDrop
	for _, d := range pool {
		if cat.Items[d.ID].Kind == "stackable" {
			continue
		}
		if pred(d) {
			out = append(out, d)
		}
	}
	return out
}

func (p *GlobalDropPolicy) difficultyConfig(diff byte) *DifficultyEquipmentDrop {
	idx := int(diff)
	if idx > 4 {
		idx = 4
	}
	for i := range p.Global.Difficulties {
		if p.Global.Difficulties[i].Difficulty == idx {
			return &p.Global.Difficulties[i]
		}
	}
	if idx < len(p.Global.Difficulties) {
		return &p.Global.Difficulties[idx]
	}
	return nil
}

func (p *GlobalDropPolicy) apply(d *dungeon.Session, monster *protocol.DungeonMonster, gear *inventory.EquipmentCatalog, cat catalog.LootCatalog, result *Outcome) {
	if p == nil || gear == nil || d == nil || monster == nil {
		return
	}
	pool := gear.RewardPool()
	rng := RNG{Seed: result.NextSeed}
	isBossOrElite := monster.Rank >= 2 || d.Room.Boss

	// 1. Global per-difficulty equipment drop
	if p.Global.Enabled {
		if cfg := p.difficultyConfig(d.Difficulty); cfg != nil && cfg.Enabled {
			effChance := int(math.Round(float64(cfg.EquipDropChance) * cfg.DropRateMultiplier))
			if effChance > 100 {
				effChance = 100
			}
			if effChance > 0 && int(rng.Next(100)) < effChance {
				for n := 0; n < cfg.DropCount; n++ {
					if id := p.pickGlobalItem(&rng, cfg, monster.Level, pool, cat); id != 0 {
						result.Awards = append(result.Awards, Award{id, 1})
					}
				}
			}
		}
	}

	// 2. Normal Abyss drop (普通深渊掉落)
	if p.NormalAbyss.Enabled && p.NormalAbyss.DropChance > 0 {
		inScope := false
		switch p.NormalAbyss.Scope {
		case "", "all":
			inScope = true
		case "boss_only":
			inScope = isBossOrElite
		case "maps":
			inScope = mapIncluded(p.NormalAbyss.Maps, d.Room.Map)
		}
		if inScope && int(rng.Next(100)) < p.NormalAbyss.DropChance {
			for n := 0; n < p.NormalAbyss.DropCount; n++ {
				if id := p.pickNormalAbyssItem(&rng, monster.Level, pool, cat); id != 0 {
					result.Awards = append(result.Awards, Award{id, 1})
				}
			}
		}
	}

	// 3. Lv.100 Abyss / Guide of Wisdom drop (100级深渊掉落)
	if p.Lv100Abyss.Enabled && p.Lv100Abyss.DropChance > 0 {
		inScope := false
		switch p.Lv100Abyss.Scope {
		case "", "all":
			inScope = true
		case "lv100_dungeons":
			inScope = d.Definition.BasisLevel >= 95 || d.Definition.MinimumLevel >= 94 || monster.Level >= 95 || mapIncluded(p.Lv100Abyss.Maps, d.Room.Map)
		case "boss_only":
			inScope = isBossOrElite
		case "maps":
			inScope = mapIncluded(p.Lv100Abyss.Maps, d.Room.Map)
		}
		if inScope && int(rng.Next(100)) < p.Lv100Abyss.DropChance {
			for n := 0; n < p.Lv100Abyss.DropCount; n++ {
				if id := p.pickLv100AbyssItem(&rng, pool, cat); id != 0 {
					result.Awards = append(result.Awards, Award{id, 1})
				}
			}
		}
	}

	result.NextSeed = rng.Seed
}

func (p *GlobalDropPolicy) pickGlobalItem(rng *RNG, cfg *DifficultyEquipmentDrop, monsterLevel byte, pool []inventory.RewardDrop, cat catalog.LootCatalog) uint32 {
	// If the user configured custom items for this difficulty, draw from them 50%
	// of the time (or 100% if no rarity weights are active).
	hasWeights := false
	for _, w := range cfg.RarityWeights {
		if w > 0 {
			hasWeights = true
			break
		}
	}
	if len(cfg.CustomItemIDs) > 0 && (!hasWeights || rng.Next(100) < 50) {
		return cfg.CustomItemIDs[rng.Next(uint32(len(cfg.CustomItemIDs)))]
	}
	if len(pool) == 0 {
		return 0
	}
	rar := pickWeightedRarity(rng, cfg.RarityWeights, 2)
	lv := int32(monsterLevel)
	var cands []inventory.RewardDrop
	if cfg.LevelMatch && lv > 0 && rar != 7 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == rar && d.MinLevel >= lv-15 && d.MinLevel <= lv+15
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == rar
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool { return true })
	}
	if len(cands) == 0 {
		return 0
	}
	return cands[rng.Next(uint32(len(cands)))].ID
}

func (p *GlobalDropPolicy) pickNormalAbyssItem(rng *RNG, monsterLevel byte, pool []inventory.RewardDrop, cat catalog.LootCatalog) uint32 {
	na := &p.NormalAbyss
	if na.PoolMode == "custom" && len(na.CustomItemIDs) > 0 {
		return na.CustomItemIDs[rng.Next(uint32(len(na.CustomItemIDs)))]
	}
	// If custom items are provided alongside an auto pool, give them a 35% spotlight.
	if len(na.CustomItemIDs) > 0 && rng.Next(100) < 35 {
		return na.CustomItemIDs[rng.Next(uint32(len(na.CustomItemIDs)))]
	}
	rar := pickWeightedRarity(rng, na.RarityWeights, 4)
	lv := int32(monsterLevel)
	var cands []inventory.RewardDrop
	if na.PoolMode == "level_epic" && lv > 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == rar && d.MinLevel < 100 && d.MinLevel >= lv-20 && d.MinLevel <= lv+20
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == rar && d.MinLevel < 100
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return (d.Rarity == 4 || d.Rarity == 6 || d.Rarity == 3) && d.MinLevel < 100
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool { return d.Rarity >= 2 })
	}
	if len(cands) == 0 {
		return 0
	}
	return cands[rng.Next(uint32(len(cands)))].ID
}

func (p *GlobalDropPolicy) pickLv100AbyssItem(rng *RNG, pool []inventory.RewardDrop, cat catalog.LootCatalog) uint32 {
	la := &p.Lv100Abyss
	if la.PoolMode == "custom" && len(la.CustomItemIDs) > 0 {
		return la.CustomItemIDs[rng.Next(uint32(len(la.CustomItemIDs)))]
	}
	if len(la.CustomItemIDs) > 0 && rng.Next(100) < 35 {
		return la.CustomItemIDs[rng.Next(uint32(len(la.CustomItemIDs)))]
	}
	wantMythic := false
	switch la.PoolMode {
	case "lv100_mythic":
		wantMythic = true
	case "lv100_epic":
		wantMythic = false
	default: // "lv100_all"
		total := la.EpicWeight + la.MythicWeight
		if total > 0 && la.MythicWeight > 0 {
			wantMythic = int(rng.Next(uint32(total))) < la.MythicWeight
		}
	}
	var cands []inventory.RewardDrop
	if wantMythic {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == 7
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == 4 && (d.MinLevel >= 100 || d.Grade >= 100)
		})
	}
	if len(cands) == 0 {
		// Graceful fallback when running against a small test catalog without Lv.100 rows
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool {
			return d.Rarity == 4 || d.Rarity == 6
		})
	}
	if len(cands) == 0 {
		cands = filterRewardPool(pool, cat, func(d inventory.RewardDrop) bool { return true })
	}
	if len(cands) == 0 {
		return 0
	}
	return cands[rng.Next(uint32(len(cands)))].ID
}

// ValidateGlobalDropPolicy checks structural sanity for diagnostics/tests.
func ValidateGlobalDropPolicy(p GlobalDropPolicy) error {
	if p.Version <= 0 {
		return fmt.Errorf("invalid global drop version")
	}
	return nil
}
