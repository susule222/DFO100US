package character

import (
	"bytes"
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Rules struct {
	MaxCharacters int  `json:"max_characters"`
	InitialLevel  byte `json:"initial_level"`
}
type State struct {
	Level           byte               `json:"level"`
	Experience      uint64             `json:"experience,omitempty"`
	SkillPoints     [2]uint16          `json:"skill_points,omitempty"`
	TechniquePoints [2]uint16          `json:"technique_points,omitempty"`
	CurrencySlot2   uint32             `json:"currency_slot2,omitempty"`
	Advancement     byte               `json:"advancement"`
	Attributes      map[string]float32 `json:"attributes"`
	InitialSkills   []int32            `json:"initial_skill_cells"`
	// Only ranks bought above the natural level using an active Tactician contract.
	TacticianRanks map[uint16]map[int]int `json:"tactician_paid_ranks"`
	LearnedSkills  [2]map[uint16]byte     `json:"learned_skills,omitempty"`
	SkillCommands  [2]map[uint16][]byte   `json:"custom_skill_commands,omitempty"`
	SkillSlots     [2]map[uint16]uint16   `json:"skill_slots,omitempty"`
	SourcePath     string                 `json:"source_path"`
	SourceSHA256   string                 `json:"source_sha256"`
	// Create equipment cells are intentionally unresolved until the native
	// grow-type/slot selection semantics are verified. Never substitute IDs.
	EquipmentPending bool `json:"equipment_pending"`
}
type Service struct {
	Store     *storage.Store
	Catalog   catalog.Characters
	Rules     Rules
	Learning  *LearningCatalog
	Equipment *inventory.EquipmentCatalog
	// V224 selects the US100 (2.24.0.124) roster row layout. The 2.38 build
	// reads a different row, so the layout is per-client, not per-archive.
	V224 bool
	// RosterMarkers fills every unknown roster field with distinct markers
	// for semantic recovery; never enabled in gameplay.
	RosterMarkers bool
	// RosterLayoutProbe sends explicit post-name bytes so one client launch
	// can bisect which byte drives the character-select portrait. Semantic
	// recovery only; never enabled in gameplay.
	RosterLayoutProbe bool
}

func New(s *storage.Store, c catalog.Characters, r Rules) (*Service, error) {
	if s == nil || len(c.Professions) == 0 || r.MaxCharacters < 1 || r.MaxCharacters > 65534 || r.InitialLevel < 1 {
		return nil, errors.New("incomplete character service configuration")
	}
	return &Service{Store: s, Catalog: c, Rules: r}, nil
}
func (s *Service) CheckName(ctx context.Context, p []byte) ([]byte, error) {
	name, e := protocol.DecodeNameRequest(p)
	if e != nil {
		return nil, e
	}
	exists, e := s.Store.NameExists(ctx, name)
	if e != nil {
		return nil, e
	}
	if exists {
		return nil, errors.New("character name already exists; native refusal code pending")
	}
	return []byte{1}, nil
}
func (s *Service) Create(ctx context.Context, account int64, p []byte) (storage.Character, error) {
	req, e := protocol.DecodeCreateRequest(p)
	if e != nil {
		return storage.Character{}, e
	}
	prof, ok := s.Catalog.Professions[req.Profession]
	if !ok {
		return storage.Character{}, fmt.Errorf("profession %d is absent from source data", req.Profession)
	}
	// A repeated confirmation for the same account/request returns its saved
	// role. Never reset an existing character's state on a client retry.
	existing, e := s.Store.Characters(ctx, account)
	if e != nil {
		return storage.Character{}, e
	}
	for _, c := range existing {
		if strings.EqualFold(c.Name, req.Name) {
			old, parseErr := protocol.DecodeCreateRequest(c.Request)
			if c.Name == req.Name && c.Profession == req.Profession && parseErr == nil && bytes.Equal(old.Options, req.Options) {
				return c, nil
			}
			return storage.Character{}, errors.New("character name already exists with another creation request")
		}
	}
	state, e := json.Marshal(State{Level: s.Rules.InitialLevel, Attributes: prof.InitialAttributes, InitialSkills: prof.InitialSkills, SourcePath: prof.Path, SourceSHA256: prof.RawSHA256, EquipmentPending: true})
	if e != nil {
		return storage.Character{}, e
	}
	return s.Store.CreateCharacter(ctx, storage.Character{AccountID: account, Name: req.Name, Profession: req.Profession, Request: append([]byte(nil), p...), ConfigVersion: s.Catalog.Source.Checksum, State: state}, s.Rules.MaxCharacters)
}
func (s *Service) List(ctx context.Context, account int64) ([]byte, error) {
	return s.ListWithFatigue(ctx, account, nil, time.Time{})
}

// RosterEquipment, when non-empty, is attached to every roster row (development
// knob driven by -roster-equip).
var RosterEquipment []protocol.Equipment

func (s *Service) ListWithFatigue(ctx context.Context, account int64, fatigue *FatigueService, now time.Time) ([]byte, error) {
	chars, e := s.Store.Characters(ctx, account)
	if e != nil {
		return nil, e
	}
	rows := make([]protocol.CharacterRow, 0, len(chars))
	for slot, c := range chars {
		var state State
		if e = json.Unmarshal(c.State, &state); e != nil {
			return nil, e
		}
		row := protocol.CharacterRow{Slot: uint16(slot), Name: c.Name, Profession: c.Profession, Advancement: state.Advancement, Level: state.Level}
		if fatigue != nil {
			fp, err := fatigue.State(ctx, account, c.ID, now)
			if err != nil {
				return nil, err
			}
			if fp.Limit > fp.Used {
				row.FatigueRemaining = fp.Limit - fp.Used
			}
		}
		// Development knob: put an appearance entry on every row. The KR client
		// never loads character body sprites with an empty appearance block, so
		// this tests whether the block is what makes a character drawable.
		if len(RosterEquipment) > 0 {
			row.Equipment = RosterEquipment
		}
		rows = append(rows, row)
	}
	if s.RosterLayoutProbe {
		return protocol.CharacterListV224LayoutProbe(uint16(s.Rules.MaxCharacters), probeRows(rows))
	}
	if s.RosterMarkers {
		return protocol.CharacterListV224Markers(uint16(s.Rules.MaxCharacters), rows)
	}
	if s.V224 {
		return protocol.CharacterListV224(uint16(s.Rules.MaxCharacters), rows)
	}
	return protocol.CharacterList(uint16(s.Rules.MaxCharacters), rows)
}

// probeRows maps the first four roster slots onto the profession-byte
// candidates of the layout bisect. Every candidate carries the value 3 (mage)
// so a single character-select screen answers which byte the client renders:
// the control row keeps zeros, then f1, then f2's low nibble, then f2's bits
// 4-6. Slots beyond the fourth repeat the control row.
func probeRows(rows []protocol.CharacterRow) []protocol.V224ProbeRow {
	candidates := [4][3]byte{{0, 0, 0}, {3, 0, 0}, {0, 0x03, 0}, {0, 0x30, 0}}
	out := make([]protocol.V224ProbeRow, 0, len(rows))
	for i, r := range rows {
		c := candidates[len(candidates)-1]
		if i < len(candidates) {
			c = candidates[i]
		}
		probe := protocol.V224ProbeRow{Slot: r.Slot, Name: r.Name, FatigueRemaining: r.FatigueRemaining, FatigueBonus: r.FatigueBonus}
		probe.Bytes[0], probe.Bytes[1], probe.Bytes[2] = c[0], c[1], c[2]
		probe.Bytes[3] = r.Level
		out = append(out, probe)
	}
	return out
}

// RosterSlot deliberately resolves within the account's ordered roster. The
// database identity and legacy wire_id column remain durable storage keys.
func (s *Service) RosterSlot(ctx context.Context, account, characterID int64) (uint16, error) {
	roles, err := s.Store.Characters(ctx, account)
	if err != nil {
		return 0, err
	}
	for slot, role := range roles {
		if role.ID == characterID {
			return uint16(slot), nil
		}
	}
	return 0, errors.New("created character is absent from account roster")
}

func (s *Service) Select(ctx context.Context, account int64, p []byte) (storage.Character, error) {
	slot, e := protocol.DecodeSelectRequest(p)
	if e != nil {
		return storage.Character{}, e
	}
	roles, e := s.Store.Characters(ctx, account)
	if e != nil {
		return storage.Character{}, e
	}
	if uint64(slot) >= uint64(len(roles)) {
		return storage.Character{}, errors.New("selected slot is absent from account roster")
	}
	return s.settleTactician(ctx, roles[slot])
}

// EntryBasicProbe uses the persisted character identity, worn town appearance
// (avatars + weapon/title), and computed Adventurer Fame.
func (s *Service) EntryBasicProbe(role storage.Character, channelContext [2]byte) ([]byte, error) {
	var state State
	if err := json.Unmarshal(role.State, &state); err != nil {
		return nil, err
	}
	bag, err := inventory.ReadBag(role.State)
	if err != nil {
		return nil, err
	}
	appearance := mode0Appearance(bag)
	var fame uint32
	var neoPremiumKind byte
	if s != nil {
		fame = s.Equipment.BagAdventurerFame(bag)
		if s.Store != nil && role.AccountID > 0 {
			if active, perr := s.Store.ActivePremiumKinds(role.AccountID, time.Now()); perr == nil {
				switch {
				case active[storage.NeoPremiumPlusKind]:
					neoPremiumKind = storage.NeoPremiumPlusKind
				case active[120]:
					neoPremiumKind = 120
				case active[117]:
					neoPremiumKind = 117
				}
			}
		}
	} else {
		fame = (*inventory.EquipmentCatalog)(nil).BagAdventurerFame(bag)
	}
	return protocol.UserInfoBasicProbe(protocol.EntryBasicProbe{
		ActorServerID:  role.WireID,
		Context:        channelContext,
		Fame:           fame,
		NeoPremiumKind: neoPremiumKind,
		Character: protocol.CharacterRow{
			Name:        role.Name,
			Profession:  role.Profession,
			Advancement: state.Advancement,
			Level:       state.Level,
			Equipment:   appearance,
		},
	})
}

func mode0Appearance(bag inventory.Bag) []protocol.Equipment {
	bySlot := map[byte]uint32{}
	for _, item := range bag.WornAvatars {
		if (item.Slot <= 11 || item.Slot == 26 || item.Slot == 32) && item.Template != 0 {
			bySlot[byte(item.Slot)] = item.Template
		}
	}
	for _, item := range bag.Worn {
		if (item.Slot == 12 || item.Slot == 13 || item.Slot == 26 || item.Slot == 32) && item.Template != 0 {
			bySlot[byte(item.Slot)] = item.Template
		}
	}
	if len(bySlot) == 0 {
		return nil
	}
	slots := make([]int, 0, len(bySlot))
	for slot := range bySlot {
		slots = append(slots, int(slot))
	}
	sort.Ints(slots)
	out := make([]protocol.Equipment, 0, len(slots))
	for _, slot := range slots {
		out = append(out, protocol.Equipment{Slot: byte(slot), Item: bySlot[byte(slot)]})
	}
	return out
}
