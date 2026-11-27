package loot

import (
	"dfolan/internal/inventory"
	"encoding/json"
	"fmt"
	"os"
)

// Local bonus roll. No map is implicitly classified as an abyss encounter.
type AbyssRule struct {
	Maps     []uint32 `json:"maps"`
	Template uint32   `json:"template"`
	Percent  int      `json:"percent"`
}

func LoadAbyss(path string, gear *inventory.EquipmentCatalog) (*AbyssRule, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r AbyssRule
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Percent == 0 {
		return nil, nil
	}
	if r.Percent < 0 || r.Percent > 100 || len(r.Maps) == 0 || gear == nil {
		return nil, fmt.Errorf("invalid abyss rule")
	}
	if _, err = gear.Reward(r.Template); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *AbyssRule) bonus(room uint32, result *Outcome) {
	if r == nil || r.Percent == 0 {
		return
	}
	for _, id := range r.Maps {
		if id != room {
			continue
		}
		rng := RNG{Seed: result.NextSeed}
		if rng.Next(100) < uint32(r.Percent) {
			result.Awards = append(result.Awards, Award{r.Template, 1})
		}
		result.NextSeed = rng.Seed
		return
	}
}
