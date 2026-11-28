package inventory

import "sync"

// Verified US100 (v2.24.0.124) specialized inventory space layouts from DFO_unpacked.exe:
//
// Space 7 — Creature Inventory (宠物栏, 0x143a88674: cap 0x110 = 272 slots;
// tab bounds verified at 0x14398d830..0x14398d89a & 0x143b1dcf0..0x143b1dde5):
//   0..159   (0x00..0x9f): 160 Creatures & Creature Eggs ([equipment type] `[creature]`)
//   160..215 (0xa0..0xd7):  56 Creature Artifacts (`[artifact red/blue/green]`)
//   216..271 (0xd8..0x10f): 56 Creature Consumables (`[creature]`, `[creature expitem]`, `[feed]`)
//
// Space 0x25 — Insignia & Gem Inventory (勋章/守护珠栏, 0x143a886b1: cap 0x70 = 112 slots;
// tab bounds verified at 0x143b1dcfc & 0x143b1dde7):
//   0..55    (0x00..0x37):  56 Insignias ([equipment type] `[flag]`)
//   56..111  (0x38..0x6f):  56 Gems ([stackable type] `[flag gem]`)
//
// Space 3 — Worn Equipment Slots (0..35, 0x143b1c36f):
//   0..11    : Avatars
//   12..25   : Ordinary Equipment (Weapon..Earring)
//   26 (0x1a): Worn Creature (0x14391dab7)
//   27..29   : Worn Creature Artifacts (Red=27, Blue=28, Green=29)
//   31 (0x1f): Worn Insignia (0x143510aee)
//   33..35   : Worn Talismans (0x21..0x23)
const (
	CreatureBagCapacity   uint16 = 272 // 0x110
	CreatureSlotStart     uint16 = 0   // 0x00..0x9f (160 slots)
	CreatureSlotEnd       uint16 = 159
	ArtifactSlotStart     uint16 = 160 // 0xa0..0xd7 (56 slots)
	ArtifactSlotEnd       uint16 = 215
	CreatureItemSlotStart uint16 = 216 // 0xd8..0x10f (56 slots)
	CreatureItemSlotEnd   uint16 = 271

	InsigniaBagCapacity uint16 = 112 // 0x70
	InsigniaSlotStart   uint16 = 0   // 0x00..0x37 (56 slots)
	InsigniaSlotEnd     uint16 = 55
	GemSlotStart        uint16 = 56 // 0x38..0x6f (56 slots)
	GemSlotEnd          uint16 = 111

	WornCreatureSlot    uint16 = 26 // 0x1a
	WornArtifactSlotMin uint16 = 27 // 0x1b (red)
	WornArtifactSlotMax uint16 = 29 // 0x1d (green)
	WornInsigniaSlot    uint16 = 31 // 0x1f
)

// Zero-overlap ID bands verified across all 300k+ items in US100 Script.pvf.
var (
	// 2,171 [equipment type] `[creature]` (creatures & creature eggs).
	us100CreatureBands = [][2]uint32{
		{63000, 63495},
		{100050077, 100050077},
		{100050771, 100050774},
		{100344914, 100344915},
		{100980000, 100990809},
		{400990552, 400990616},
		{500980000, 500990107},
		{500990112, 500990172},
		{500990176, 500990719},
	}

	// 80 [equipment type] `[artifact red]`, `[artifact blue]`, `[artifact green]`.
	us100CreatureArtifactBands = [][2]uint32{
		{63500, 64506},
		{500950000, 500970007},
		{500990108, 500990111},
		{500990173, 500990175},
	}

	// 9 [stackable type] `[creature]`, `[creature expitem]`, `[feed]`.
	us100CreatureStackableBands = [][2]uint32{
		{24, 26},
		{10000002, 10000003},
		{50005523, 50005523},
		{50051600, 50051602},
	}

	// 48 [equipment type] `[flag]` (Guild Insignias / 勋章).
	us100InsigniaBands = [][2]uint32{
		{100380017, 100380063},
		{500380000, 500380000},
	}

	// 2,916 [stackable type] `[flag gem]` (Guild Gems / 守护珠).
	us100FlagGemBands = [][2]uint32{
		{90000, 94209},
	}

	// 16,540 [stackable type] `[rune]` (Runes / 符文 -> Space 0x2b slots 80..319).
	us100RuneBands = [][2]uint32{
		{10321814, 10321833},
		{10324860, 10324899},
		{10329445, 10329484},
		{10337378, 10337417},
		{10600191, 10617695},
		{490022310, 490022329},
	}

	// 200,661 [equipment type] `*avatar*` (Hair, Hat, Face, Breast, Coat, Pants,
	// Waist, Shoes, Skin, Aura `[aurora avatar]`, Weapon Avatar -> Space 1, 0..279)
	// with zero overlap against any non-avatar item across all 397,957 US100 PVF items.
	us100AvatarBands = [][2]uint32{
		{29201, 29205},
		{39000, 39307},
		{39400, 58706},
		{1000000, 2190081},
		{2290036, 2290036},
		{2291036, 2291036},
		{2292031, 2292031},
		{2293038, 2293038},
		{2294050, 2294050},
		{2295032, 2297026},
		{5310000, 5340015},
		{101500005, 101501191},
		{101501193, 101600288},
		{102500005, 103600269},
		{104500005, 105600271},
		{106500005, 107600268},
		{108500005, 108600272},
		{109500005, 112600339},
		{113500000, 113600069},
		{114500000, 115600063},
		{116500000, 116602286},
		{116602314, 116602362},
		{401500221, 401600044},
		{402500193, 403600035},
		{404500188, 405600035},
		{406500202, 407600036},
		{408500190, 408600036},
		{409500193, 413600066},
		{414500283, 415600060},
		{416500218, 416600049},
		{501500000, 501600020},
		{501600653, 501600822},
		{502500000, 503600177},
		{504500000, 505600176},
		{506500000, 507600178},
		{508500000, 508600180},
		{509500000, 513600172},
		{514500000, 515600177},
		{516500000, 516600170},
	}

	// 83,155 ordinary wearable equipment templates (weapons, armors, accessories,
	// special gear, titles, support weapons -> Space 0 slots 9..64, Space 3 slots 12..25)
	// with zero overlap against any avatar, talisman, creature, artifact, insignia, or stackable.
	us100OrdinaryEquipmentBands = [][2]uint32{
		{10000, 10981},
		{11200, 29175},
		{29300, 37713},
		{39350, 39399},
		{100000, 835003},
		{2200000, 2290002},
		{2290102, 2291002},
		{2291102, 2292002},
		{2292102, 2293002},
		{2293102, 2294002},
		{2294102, 2294113},
		{2300000, 2312900},
		{2676113, 2676120},
		{100050017, 100050074},
		{100050097, 100050770},
		{100050784, 100344913},
		{100344916, 100353869},
		{100390001, 100390781},
		{101000001, 101040765},
		{101501192, 101501192},
		{102000001, 102040808},
		{104000001, 104040833},
		{106000001, 106040768},
		{108000001, 108040748},
		{109000001, 109040517},
		{113370002, 113370040},
		{114000000, 114030314},
		{116000001, 116030344},
		{116602287, 116602287},
		{400050187, 400390433},
		{401000059, 401040102},
		{402000054, 402040082},
		{404000051, 404040097},
		{406000047, 406040094},
		{408000051, 408040097},
		{409000052, 409040110},
		{414000059, 414030072},
		{416000021, 416030063},
		{500050000, 500350306},
		{500390001, 500390288},
		{501000000, 501330035},
		{502000000, 502040048},
		{504000000, 504040053},
		{506000000, 506040067},
		{508000000, 508040056},
		{509000000, 509040052},
		{514000000, 514030053},
		{516000005, 516030052},
	}
)

func matchIDBands(id uint32, bands [][2]uint32) bool {
	lo, hi := 0, len(bands)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		b := bands[mid]
		if id < b[0] {
			hi = mid - 1
		} else if id > b[1] {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

// IsCreatureTemplate reports whether template id is a US100 Creature or Creature Egg (Space 7, 0..159).
func IsCreatureTemplate(id uint32) bool {
	return matchIDBands(id, us100CreatureBands)
}

// IsCreatureArtifactTemplate reports whether template id is a US100 Creature Artifact (Space 7, 160..215).
func IsCreatureArtifactTemplate(id uint32) bool {
	return matchIDBands(id, us100CreatureArtifactBands)
}

// IsCreatureStackableTemplate reports whether template id is a US100 Creature Consumable (Space 7, 216..271).
func IsCreatureStackableTemplate(id uint32) bool {
	return matchIDBands(id, us100CreatureStackableBands)
}

// IsInsigniaTemplate reports whether template id is a US100 Guild Insignia (Space 0x25, 0..55).
func IsInsigniaTemplate(id uint32) bool {
	return matchIDBands(id, us100InsigniaBands)
}

// IsFlagGemTemplate reports whether template id is a US100 Guild Gem (Space 0x25, 56..111).
func IsFlagGemTemplate(id uint32) bool {
	return matchIDBands(id, us100FlagGemBands)
}

// IsRuneTemplate reports whether template id is a US100 Rune (Space 0x2b, 80..319).
func IsRuneTemplate(id uint32) bool {
	return matchIDBands(id, us100RuneBands)
}

// IsAvatarTemplate reports whether template id is a US100 Avatar / Aura / Skin /
// Weapon Avatar (Space 1, 0..279).
func IsAvatarTemplate(id uint32) bool {
	return matchIDBands(id, us100AvatarBands)
}

// IsOrdinaryEquipmentTemplate reports whether template id is a US100 ordinary
// wearable equipment template (weapons, armors, accessories, special gear, titles).
func IsOrdinaryEquipmentTemplate(id uint32) bool {
	return matchIDBands(id, us100OrdinaryEquipmentBands)
}

var (
	catalogLookupMu        sync.RWMutex
	creatureEggLookupFn    func(uint32) uint32
	ordinaryEquipLookupFn  func(uint32) bool
	ordinaryEquipDurFn     func(uint32) (uint16, bool)
)

// RegisterCreatureEggLookup registers the PVF creature-egg -> hatched-creature
// template resolver (loaded from boosters.generated.json.gz) so ReadBag and
// WearService can auto-hatch sealed eggs when equipped to Space 3 slot 26.
func RegisterCreatureEggLookup(fn func(uint32) uint32) {
	catalogLookupMu.Lock()
	creatureEggLookupFn = fn
	catalogLookupMu.Unlock()
}

// CreatureEggOutput returns the hatched creature template ID for a creature egg,
// or 0 if template id is not a known hatchable egg.
func CreatureEggOutput(id uint32) uint32 {
	if id == 0 {
		return 0
	}
	catalogLookupMu.RLock()
	fn := creatureEggLookupFn
	catalogLookupMu.RUnlock()
	if fn == nil {
		return 0
	}
	return fn(id)
}

// RegisterOrdinaryEquipmentLookup registers a PVF-derived predicate identifying
// ordinary equipment templates (weapons, armors, accessories, titles) so ReadBag
// can auto-migrate misplaced equipment from stackable tabs into Equipment (9..64)
// and WearService can equip booster-only titles/weapons absent from drop tables.
func RegisterOrdinaryEquipmentLookup(fn func(uint32) bool) {
	catalogLookupMu.Lock()
	ordinaryEquipLookupFn = fn
	catalogLookupMu.Unlock()
}

// RegisterOrdinaryEquipmentDurabilityLookup registers an optional PVF durability
// lookup for ordinary equipment templates (0 for titles/accessories/special gear).
func RegisterOrdinaryEquipmentDurabilityLookup(fn func(uint32) (uint16, bool)) {
	catalogLookupMu.Lock()
	ordinaryEquipDurFn = fn
	catalogLookupMu.Unlock()
}

// OrdinaryEquipmentDurability returns the PVF durability for ordinary equipment
// template id when registered, defaulting to 0 for titles/accessories and 35 otherwise.
func OrdinaryEquipmentDurability(id uint32) uint16 {
	catalogLookupMu.RLock()
	fn := ordinaryEquipDurFn
	catalogLookupMu.RUnlock()
	if fn != nil {
		if dur, ok := fn(id); ok {
			return dur
		}
	}
	return 35
}

// IsKnownOrdinaryEquipment reports whether id is a known ordinary equipment
// template (excluding avatars, creatures, artifacts, insignias, and talismans).
func IsKnownOrdinaryEquipment(id uint32) bool {
	if id == 0 || IsAvatarTemplate(id) || IsCreatureTemplate(id) || IsCreatureArtifactTemplate(id) || IsInsigniaTemplate(id) || IsTalismanTemplate(id) {
		return false
	}
	catalogLookupMu.RLock()
	fn := ordinaryEquipLookupFn
	catalogLookupMu.RUnlock()
	if fn == nil {
		return false
	}
	return fn(id)
}
