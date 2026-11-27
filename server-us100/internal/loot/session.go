package loot

import (
	"crypto/rand"
	"dfolan/internal/catalog"
	"dfolan/internal/dungeon"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"encoding/binary"
	"fmt"
	"sync"
)

type Drop struct {
	Run    string
	Map    uint32
	Owner  uint16
	Object uint32
	Slot   uint16
	Award  Award
}
type Session struct {
	Abyss              *AbyssRule
	GlobalDrop         *GlobalDropPolicy
	HellParty          *events.HellPartyPolicy
	DropMultiplier     float64
	GoldMultiplier     float64
	UnstableRiftActive bool
	hellRoomDropped    bool
	mu                 sync.Mutex
	Catalog            catalog.LootCatalog
	Tables             Tables
	Rules              Rules
	Equipment          *inventory.EquipmentCatalog
	Run                string
	Account, Character int64
	Actor              uint16
	next               uint32
	seeds              map[uint32]uint32
	deaths             map[uint16][]protocol.SceneDrop
	Objects            map[uint32]Drop
	Skipped            map[uint16][]string
}

func NewSession(c catalog.LootCatalog, t Tables, r Rules, equipment *inventory.EquipmentCatalog, run string, account, character int64, actor uint16) *Session {
	return &Session{Catalog: c, Tables: t, Rules: r, Equipment: equipment, Run: run, Account: account, Character: character, Actor: actor, next: 1, seeds: map[uint32]uint32{}, deaths: map[uint16][]protocol.SceneDrop{}, Objects: map[uint32]Drop{}, Skipped: map[uint16][]string{}}
}
func (s *Session) Death(d *dungeon.Session, entity uint16) ([]protocol.SceneDrop, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d == nil || d.RunID != s.Run || !d.Loaded || !d.Dead[entity] {
		return nil, fmt.Errorf("drop without owned confirmed death")
	}
	var monster *protocol.DungeonMonster
	for _, m := range d.Monsters {
		if m.Entity == entity {
			v := m
			monster = &v
			break
		}
	}
	if monster == nil {
		return nil, fmt.Errorf("drop target outside current room")
	}
	if p, ok := s.deaths[entity]; ok {
		return append([]protocol.SceneDrop(nil), p...), nil
	}
	if monster.NonCombat {
		s.deaths[entity] = nil
		return nil, nil
	}
	seed, ok := s.seeds[d.Room.Map]
	if !ok {
		if e := binary.Read(rand.Reader, binary.LittleEndian, &seed); e != nil {
			return nil, e
		}
	}
	diff := d.Difficulty
	if int(diff) >= len(s.Rules.DifficultyBonus) {
		diff = byte(len(s.Rules.DifficultyBonus) - 1)
	}
	rollLevel := monster.Level
	if (s.GlobalDrop != nil || (s.HellParty != nil && s.HellParty.Enabled)) && s.Catalog.MaximumGrade > 3 && uint32(rollLevel)+3 > s.Catalog.MaximumGrade {
		rollLevel = byte(s.Catalog.MaximumGrade - 3)
	}
	dropMult := s.DropMultiplier
	if dropMult <= 0 {
		dropMult = 1.0
	}
	goldMult := s.GoldMultiplier
	if goldMult <= 0 {
		goldMult = 1.0
	}
	result, e := RollWithMultipliers(s.Catalog, s.Tables, s.Rules, s.Equipment.DropPool(), seed, rollLevel, monster.Rank, diff, dropMult, goldMult)
	if e != nil {
		return nil, e
	}
	s.GlobalDrop.apply(d, monster, s.Equipment, s.Catalog, &result)
	s.applyHellPartyDrop(d, monster, &result)
	s.Abyss.bonus(d.Room.Map, &result)
	if d.NextEntity == 0 || uint64(d.NextEntity)+uint64(len(result.Awards)) >= 65535 || uint64(s.next)+uint64(len(result.Awards)) >= 65535 {
		return nil, fmt.Errorf("drop identity exhausted")
	}
	var rows []protocol.SceneDrop
	for _, a := range result.Awards {
		// Scene drops and monsters share the native object namespace. Consume
		// the run's allocator so a later room cannot reuse a drop identity.
		object := uint32(d.NextEntity)
		d.NextEntity++
		slot := uint16(s.next)
		s.next++
		drop := Drop{s.Run, d.Room.Map, s.Actor, object, slot, a}
		s.Objects[object] = drop
		// Gear carries its source durability in the scene row, exactly as it
		// does in a bag row; a stackable carries its amount instead.
		item := protocol.OrdinaryItem(slot, a.Template, a.Amount)
		if s.Equipment != nil && !s.stackable(a.Template) {
			durability, err := s.Equipment.Reward(a.Template)
			if err != nil { return nil, err }
			item = inventory.EquipmentRow(inventory.BagEquipment{Slot: slot, Template: a.Template, Durability: durability})
		}
		rows = append(rows, protocol.SceneDrop{Object: object, Item: item, Sentinel: 65535, Owner: s.Actor})
	}
	s.seeds[d.Room.Map] = result.NextSeed
	s.deaths[entity] = rows
	s.Skipped[entity] = result.SkippedKinds
	return append([]protocol.SceneDrop(nil), rows...), nil
}
// stackable reports whether this identity belongs to the ordinary stackable
// pool. The two source lists have independent identity spaces, so a stackable
// always wins and gear is only assumed for identities it does not claim.
func (s *Session) stackable(id uint32) bool {
	return id == 0 || s.Catalog.Items[id].Kind == "stackable"
}

// applyHellPartyDrop bursts Hell Party epic/mythic drops when the last combat
// monster in the NOTI 28 Random Hell room is defeated.
func (s *Session) applyHellPartyDrop(d *dungeon.Session, monster *protocol.DungeonMonster, result *Outcome) {
	if s.hellRoomDropped || s.HellParty == nil || !s.HellParty.Enabled || !d.IsHellRoom() || s.Equipment == nil || monster == nil || monster.NonCombat {
		return
	}
	for _, m := range d.Monsters {
		if !m.NonCombat && !d.Dead[m.Entity] {
			return
		}
	}
	s.hellRoomDropped = true
	count := s.HellParty.DropCount
	if count < 1 {
		count = 1
	}
	if s.UnstableRiftActive {
		count++
	}
	if count > 6 {
		count = 6
	}
	pool := s.Equipment.RewardPool()
	if len(pool) == 0 {
		return
	}
	rng := RNG{Seed: result.NextSeed}
	helper := &GlobalDropPolicy{
		Lv100Abyss: Lv100AbyssPolicy{
			Enabled:      true,
			EpicWeight:   s.HellParty.EpicWeight,
			MythicWeight: s.HellParty.MythicWeight,
			PoolMode:     "lv100_all",
		},
	}
	for i := 0; i < count; i++ {
		if id := helper.pickLv100AbyssItem(&rng, pool, s.Catalog); id != 0 {
			result.Awards = append(result.Awards, Award{id, 1})
		}
	}
	result.NextSeed = rng.Seed
}

func (s *Session) Owned(d *dungeon.Session, account, character int64, actor uint16, object uint32) (Drop, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Objects[object]
	if !ok || d == nil || !d.Loaded || d.RunID != s.Run || v.Map != d.Room.Map || account != s.Account || character != s.Character || actor != s.Actor || actor != v.Owner {
		return Drop{}, fmt.Errorf("pickup object is not owned in current room")
	}
	return v, nil
}
