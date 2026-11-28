package character

import (
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// EntryAdditionUS100 emits the US100 (2.24) mode-1 shape with the block fields
// this client's parser actually reads (protocol.EntryAdditionBlock). The block
// carries the character's level and experience, which land in the actor's
// +0x50c/+0x510 pair — the same pair a per-thread singleton mirrors as its own
// +0x4c/+0x50, the shape of a local player's level and 64-bit experience.
//
// The body is padded to protocol.EntryAdditionBodyMinimum because the parser
// keeps reading past the fixed tail when the key matches (see the constant).
func (s *Service) EntryAdditionUS100(role storage.Character) ([]byte, error) {
	return s.entryAdditionUS100(role, false, protocol.EntryAdditionBodyMinimum)
}

// EntryAdditionHPMaxUS100 fills the block's two big slots with the character's
// maximum HP and MP (HP ×10, the reference servers' wire unit) instead of the
// level/experience pair. Same block, same offsets: the two readings of the fields
// are the open question, so this variant exists to tell them apart by what the
// HUD shows.
func (s *Service) EntryAdditionHPMaxUS100(role storage.Character) ([]byte, error) {
	return s.entryAdditionUS100(role, true, protocol.EntryAdditionBodyMinimum)
}

// EntryAdditionZeroBlockUS100 sends the entry with every block field zero and the
// same padded body. Not equivalent to "empty": the client still applies it.
func (s *Service) EntryAdditionZeroBlockUS100(role storage.Character) ([]byte, error) {
	return s.entryAdditionUS100(role, false, 0)
}

// EntryAdditionStatsUS100 uses the selected character's persisted attributes
// and experience. On town entry the base-stat multiplier is neutral (100%).
func (s *Service) EntryAdditionStatsUS100(role storage.Character) ([]byte, error) {
	var state State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	v := state.Attributes
	hp, err := s.entryVitalMaximum(role, state, "[hp max]")
	if err != nil {
		return nil, err
	}
	mp, err := s.entryVitalMaximum(role, state, "[mp max]")
	if err != nil {
		return nil, err
	}
	// The four core dimensions have no field of their own in this client's
	// character data: the job .chr carries [physical attack], [physical defense],
	// [magical attack] and [magical defense], and this generation's ability
	// string table labels exactly those four rows strength, vitality,
	// intelligence and spirit. The CN90 server maps them the same way for the
	// same reason ("部分 .chr 只写旧标签，所以这里按客户端能力表补齐四维"), so the core
	// values are taken from the attack and defense attributes instead of keys
	// that the import does not carry.
	blob := protocol.EntryStatBlobUS100{
		HPMax:          hp,
		MPMax:          mp,
		Strength:       attributeWireU16(v, "[physical attack]"),
		Vitality:       attributeWireU16(v, "[physical defense]"),
		Intelligence:   attributeWireU16(v, "[magical attack]"),
		Spirit:         attributeWireU16(v, "[magical defense]"),
		InventoryLimit: attributeWireU32(v, "[inventory limit]"),
		Weight:         attributeWireU32(v, "[weight]"),
		MoveSpeed:      attributeWireU32(v, "[move speed]"),
		Fire:           attributeWireS16(v, "[fire resistance]"),
		Water:          attributeWireS16(v, "[water resistance]"),
		Dark:           attributeWireS16(v, "[dark resistance]"),
		Light:          attributeWireS16(v, "[light resistance]"),
		HPRegen:        attributeWireU16(v, "[hp regen speed]"),
		MPRegen:        attributeWireU16(v, "[mp regen speed]"),
		AttackSpeed:    attributeWireU16(v, "[attack speed]"),
		CastSpeed:      attributeWireU16(v, "[cast speed]"),
		HitRecovery:    attributeWireU16(v, "[hit recovery]"),
		JumpPower:      attributeWireU16(v, "[jump power]"),
		BasePercent:    100,
	}
	trees, err := entrySkillTrees(state)
	if err != nil {
		return nil, err
	}
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, err
	}
	var fame uint32
	if s != nil {
		fame = s.Equipment.BagAdventurerFame(bag)
	}
	block := protocol.EntryAdditionBlock{Level: uint32(state.Level), Experience: state.Experience, Value19: fame}
	worn := make([]protocol.ActorWornUS224, 0, len(bag.WornAvatars)+len(bag.Worn))
	for _, item := range bag.WornAvatars {
		if item.Slot >= 12 || item.Template == 0 || item.Template == 0xffffffff {
			continue
		}
		worn = append(worn, protocol.ActorWornUS224{
			Slot:       item.Slot,
			Template:   item.Template,
			Durability: item.Durability,
			Upgrade:    item.EffectiveUpgrade(),
			Avatar:     true,
		})
	}
	for _, item := range bag.Worn {
		// NOTI 13 ignores Space 3 (GetInventoryBySpace(3)==NULL at 0x143a74b60)
		// and NOTI 14 Space 3 (0x143510a50) only mutates already-equipped slots
		// (leaving empty avatar 0..11, weapon 12, and special 26..35 slots blank).
		// NOTI 2 mode-1 (0x1434eae00) is the sole login initializer for all 36
		// worn slots (0..35), including Creature (slot 26, type 0x1a with its
		// 5-byte creatureInstanceID+flag block), Artifacts (27..29), Insignia (31),
		// and Talismans (33..35).
		if item.Slot < 12 || item.Slot >= 36 || item.Template == 0 || item.Template == 0xffffffff {
			continue
		}
		worn = append(worn, protocol.ActorWornUS224{
			Slot:       item.Slot,
			Template:   item.Template,
			Durability: item.Durability,
			Upgrade:    item.EffectiveUpgrade(),
			Avatar:     false,
			CreatureID: item.CreatureID,
		})
	}
	sort.Slice(worn, func(i, j int) bool { return worn[i].Slot < worn[j].Slot })
	return protocol.UserInfoAdditionWithWornUS224(role.WireID, block, blob.Bytes(), trees, worn)
}

// Old saves may lack an individual maximum. Reconstruct only the missing
// field, using the same per-level fixed-point growth as ApplyGain.
func (s *Service) entryVitalMaximum(role storage.Character, state State, name string) (uint32, error) {
	value, saved := state.Attributes[name]
	wire := math.Round(float64(value) * 10)
	if !saved {
		prof, ok := s.Catalog.Professions[role.Profession]
		if !ok || (role.ConfigVersion != "" && role.ConfigVersion != s.Catalog.Source.Checksum) ||
			(state.SourceSHA256 != "" && state.SourceSHA256 != prof.RawSHA256) {
			return 0, fmt.Errorf("missing %s and matching profession source for character %d", name, role.ID)
		}
		base, ok := prof.InitialAttributes[name]
		if !ok || s.Rules.InitialLevel == 0 || state.Level < s.Rules.InitialLevel {
			return 0, fmt.Errorf("missing base %s or initial level for character %d", name, role.ID)
		}
		wire = math.Round(float64(base) * 10)
		if state.Level > s.Rules.InitialLevel {
			growth, ok := prof.BaseGrowth[name]
			if !ok || state.Advancement > 5 {
				return 0, fmt.Errorf("missing applicable %s growth for character %d", name, role.ID)
			}
			wire += float64(state.Level-s.Rules.InitialLevel) * math.Round(float64(growth)*10)
		}
	}
	if !(wire >= 1 && wire <= math.MaxUint32) {
		return 0, fmt.Errorf("invalid %s for character %d", name, role.ID)
	}
	return uint32(wire), nil
}

// attributeWireU32 reads one attribute in the record's wire units. Every value
// in this client's record is ten times the display value, and an attribute the
// catalog does not carry reads as zero rather than failing the whole entry: the
// record exists to make the panel show the values it does carry.
func attributeWireU32(attributes map[string]float32, name string) uint32 {
	n := float64(attributes[name]) * 10
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return 0
	}
	if n > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(math.Round(n))
}

func attributeWireU16(attributes map[string]float32, name string) uint16 {
	if n := attributeWireU32(attributes, name); n <= math.MaxUint16 {
		return uint16(n)
	}
	return math.MaxUint16
}

func attributeWireS16(attributes map[string]float32, name string) int16 {
	n := float64(attributes[name]) * 10
	switch {
	case math.IsNaN(n) || math.IsInf(n, 0):
		return 0
	case n > math.MaxInt16:
		return math.MaxInt16
	case n < math.MinInt16:
		return math.MinInt16
	}
	return int16(math.Round(n))
}

// TimeSyncUS100 encodes the CMD 1959 time sync. Kept because the command and
// its field layout are verified end to end, but the fields feed the client's
// in-game calendar, not the character's HP; the HP display's real source is
// still being located. Not wired into any entry plan.
func (s *Service) TimeSyncUS100(role storage.Character) ([]byte, error) {
	var state State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	hp := attributeWireU32(state.Attributes, "[hp max]")
	return protocol.UserInfoTimeSyncUS100(hp), nil
}

func (s *Service) entryAdditionUS100(role storage.Character, hpMax bool, pad int) ([]byte, error) {
	return s.entryAdditionUS100Flag13(role, hpMax, pad, false)
}

// EntryAdditionHPFlag13US100 is the hp/mp variant with block byte 13 set to 1.
// The settlement entry 0x143f03330 receives flag=block[13]>0, and the 2026-09-22
// entry capture showed the panel writers (0x142eb48d0/0x142ebaf70, the only
// code that fills the panel's slot array +0x304..+0x334) never fire on a
// flag=0 entry — this variant tests whether flag=1 triggers them.
func (s *Service) EntryAdditionHPFlag13US100(role storage.Character) ([]byte, error) {
	return s.entryAdditionUS100Flag13(role, true, protocol.EntryAdditionBodyMinimum, true)
}

func (s *Service) entryAdditionUS100Flag13(role storage.Character, hpMax bool, pad int, flag13 bool) ([]byte, error) {
	var state State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	block := protocol.EntryAdditionBlock{}
	if flag13 {
		block.Flag13 = 1
	}
	if hpMax {
		v := state.Attributes
		raw := func(name string) (uint32, error) {
			n := float64(v[name]) * 10
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > math.MaxUint32 {
				return 0, fmt.Errorf("attribute %s out of native bounds", name)
			}
			return uint32(math.Round(n)), nil
		}
		hp, e := raw("[hp max]")
		if e != nil {
			return nil, e
		}
		mp, e := raw("[mp max]")
		if e != nil {
			return nil, e
		}
		// +4 is a u64 on the wire: the MP value occupies its low half and the
		// high half stays zero, which is how the reference hp/mp reading looks.
		block.Level, block.Experience = hp, uint64(mp)
	} else if pad > 0 {
		block.Level, block.Experience = uint32(state.Level), state.Experience
		if s != nil {
			block.Value19 = s.Equipment.AdventurerFame(role.State)
		}
	}
	return protocol.UserInfoAdditionPaddedUS100(role.WireID, block, pad)
}

// EntryStatProbeUS100 sends the source HP and MP maxima through the mode-6 entry,
// which copies its 45-byte block onto actor+0x7f2 — the struct the stat
// settlement consumes. The values are sent raw (no 2.38 ×10 scaling): the wire
// unit for this build is not established yet, and one run with a raw value
// settles it — a HUD reading 180/140 means raw, 18/14 means the fixed-point scale.
func (s *Service) EntryStatProbeUS100(role storage.Character) ([]byte, error) {
	var state State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	v := state.Attributes
	raw := func(name string) (uint32, error) {
		n := float64(v[name])
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > math.MaxUint32 {
			return 0, fmt.Errorf("attribute %s out of native bounds", name)
		}
		return uint32(math.Round(n)), nil
	}
	hp, e := raw("[hp max]")
	if e != nil {
		return nil, e
	}
	mp, e := raw("[mp max]")
	if e != nil {
		return nil, e
	}
	return protocol.UserInfoStatProbeUS100(role.WireID, hp, mp)
}

// EntryAddition uses persisted source attributes and initial skills in native
// wire units. Equipment and advancement-specific skill learning are separate.
func (s *Service) EntryAddition(role storage.Character) ([]byte, error) {
	var state State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	if state.SourceSHA256 == "" {
		return nil, fmt.Errorf("missing source character data")
	}
	v := state.Attributes
	var failure error
	scaled := func(name string, multiplier, max float64) uint32 {
		n := float64(v[name]) * multiplier
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > max {
			failure = fmt.Errorf("attribute %s out of native bounds", name)
			return 0
		}
		return uint32(math.Round(n))
	}
	signed := func(name string, multiplier float64) int16 {
		n := float64(v[name]) * multiplier
		if math.IsNaN(n) || math.IsInf(n, 0) || n < math.MinInt16 || n > math.MaxInt16 {
			failure = fmt.Errorf("attribute %s out of signed native bounds", name)
			return 0
		}
		return int16(math.Round(n))
	}
	stats := protocol.PackedEntryStats{
		HP: scaled("[hp max]", 10, math.MaxUint32), MP: scaled("[mp max]", 10, math.MaxUint32),
		Core: [4]uint16{uint16(scaled("[physical attack]", 10, math.MaxUint16)), uint16(scaled("[physical defense]", 10, math.MaxUint16)), uint16(scaled("[magical attack]", 10, math.MaxUint16)), uint16(scaled("[magical defense]", 10, math.MaxUint16))},
		// Native 145d2e760 divides these wire values by ten before applying
		// actor attributes. Source parser 147557e90 scales all input by ten,
		// including MP regeneration, whose effective runtime unit differs.
		Element:       [4]int16{signed("[fire resistance]", 10), signed("[water resistance]", 10), signed("[dark resistance]", 10), signed("[light resistance]", 10)},
		Inventory:     int32(scaled("[inventory limit]", 10, math.MaxInt32)),
		Regeneration:  [2]int16{signed("[hp regen speed]", 10), signed("[mp regen speed]", 10)},
		Movement:      scaled("[move speed]", 10, math.MaxUint32),
		AttackCasting: [2]uint16{uint16(scaled("[attack speed]", 10, math.MaxUint16)), uint16(scaled("[cast speed]", 10, math.MaxUint16))},
		RecoveryJump:  [2]int16{signed("[hit recovery]", 10), signed("[jump power]", 10)},
		Weight:        int32(scaled("[weight]", 10, math.MaxInt32)), BasePercent: 100,
	}
	if failure != nil {
		return nil, failure
	}
	trees, err := entrySkillTrees(state)
	if err != nil {
		return nil, err
	}
	return protocol.UserInfoAdditionProbe(protocol.EntryAdditionProbe{ActorServerID: role.WireID, Experience: state.Experience, Stats: stats, SkillTrees: trees})
}

func entrySkillTrees(state State) ([2][]protocol.EntrySkill, error) {
	var trees [2][]protocol.EntrySkill
	for i := range trees {
		known, e := knownSkills(state, i)
		if e != nil {
			return trees, e
		}
		ids := skillOrder(state, known)
		for _, id := range ids {
			trees[i] = append(trees[i], protocol.EntrySkill{ID: uint16(id), Level: known[uint16(id)]})
		}
	}
	return trees, nil
}

// The exact .chr loader at 147559d80 stores (ID, first value) in the
// initial-skill vector and (ID, second value) in a separate vector. All 17
// imported professions use second value=1 in their initial section. Preserve
// that supported shape; do not interpret arbitrary growth/PvP conditions.
func initialSkills(s State) ([]protocol.EntrySkill, error) {
	if len(s.InitialSkills)%3 != 0 {
		return nil, fmt.Errorf("unsupported initial skill state")
	}
	var out []protocol.EntrySkill
	for i := 0; i < len(s.InitialSkills); i += 3 {
		id, level, condition := s.InitialSkills[i], s.InitialSkills[i+1], s.InitialSkills[i+2]
		if id <= 0 || id > 65535 || level <= 0 || level > 255 || condition != 1 {
			return nil, fmt.Errorf("unsupported source initial skill tuple")
		}
		out = append(out, protocol.EntrySkill{ID: uint16(id), Level: byte(level)})
	}
	return out, nil
}

func (s *Service) EntrySkills(role storage.Character) ([]byte, error) {
	var state State
	if e := json.Unmarshal(role.State, &state); e != nil {
		return nil, e
	}
	for id, keys := range state.SkillCommands[0] {
		if sameNativeCommand(s.nativeCommand(role.Profession, id), keys) {
			delete(state.SkillCommands[0], id)
		}
	}
	var trees [2]protocol.SkillTree
	for i := range trees {
		rows, e := s.skillRows(role, state, i)
		if e != nil {
			return nil, e
		}
		if s.Learning != nil {
			// Instantiate own-job skill definitions for the learning window.
			// Zero ranks are unlearned and never occupy a shortcut or grant use.
			seen := map[uint16]bool{}
			occupied := map[uint16]bool{}
			for _, r := range rows {
				seen[r.ID] = true
				occupied[r.Slot] = true
			}
			var missing []int
			for id, d := range s.Learning.index[role.Profession] {
				if !seen[id] && d.ForAdvancement(int(state.Advancement)) {
					missing = append(missing, int(id))
				}
			}
			sort.Ints(missing)
			for _, id := range missing {
				slot := uint16(14)
				for slot < 255 && occupied[slot] {
					slot++
				}
				if slot == 255 {
					return nil, fmt.Errorf("unlearned base-profession palette exceeds current wire slots")
				}
				occupied[slot] = true
				rows = append(rows, protocol.LearnedSkill{ID: uint16(id), Slot: slot})
			}
		}
		if s.ashenCommandProfile(role.Profession) {
			rows = omitUnlearnedAshen(rows)
		}
		if i == 1 {
			rows = omitStyleIICommandShadows(trees[0].Skills, rows, func(id uint16) bool { return s.nativeCommand(role.Profession, id) != "" })
		}
		trees[i] = protocol.SkillTree{SP: state.SkillPoints[i], TP: state.TechniquePoints[i], Skills: rows, Commands: state.SkillCommands[i]}
	}
	return protocol.SkillInfoTrees(state.Level, trees)
}
