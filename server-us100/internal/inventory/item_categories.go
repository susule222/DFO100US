package inventory

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
