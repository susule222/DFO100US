package inventory

import (
	"dfolan/internal/catalog"
	"path/filepath"
	"testing"
)

func TestOrdinaryEquipmentFame(t *testing.T) {
	if got := OrdinaryEquipmentFame(14, 10, 0, 0); got != 1 {
		t.Fatalf("Lv10 common coat fame=%d want=1", got)
	}
	if got := OrdinaryEquipmentFame(20, 10, 1, 0); got != 1 {
		t.Fatalf("Lv10 uncommon bracelet fame=%d want=1", got)
	}
	if got := OrdinaryEquipmentFame(14, 100, 4, 0); got != 464 {
		t.Fatalf("Lv100 epic coat fame=%d want=464", got)
	}
	if got := OrdinaryEquipmentFame(14, 100, 7, 0); got != 580 {
		t.Fatalf("Lv100 mythic coat fame=%d want=580", got)
	}
	weap7 := OrdinaryEquipmentFame(12, 100, 4, 7)
	weap12 := OrdinaryEquipmentFame(12, 100, 4, 12)
	armor7 := OrdinaryEquipmentFame(14, 100, 4, 7)
	if !(weap7 > 464 && weap12 > weap7 && armor7 > 464 && weap7 > armor7) {
		t.Fatalf("unexpected upgrade fame ordering: weap7=%d weap12=%d armor7=%d", weap7, weap12, armor7)
	}
	for _, nonOrdinary := range []uint16{0, 11, 13, 24, 26} {
		if got := OrdinaryEquipmentFame(nonOrdinary, 100, 4, 0); got != 0 {
			t.Fatalf("slot %d returned %d want 0", nonOrdinary, got)
		}
	}
}

func TestAvatarFame(t *testing.T) {
	cases := []struct {
		slot  uint16
		grade int32
		want  uint32
	}{
		{8, 1, 69},
		{0, 1, 69},
		{0, 2, 88},
		{0, 3, 116},
		{0, 4, 154},
	}
	for _, tc := range cases {
		if got := AvatarFame(tc.slot, tc.grade); got != tc.want {
			t.Fatalf("AvatarFame(%d, %d) = %d, want %d", tc.slot, tc.grade, got, tc.want)
		}
	}
}

func TestBagAdventurerFameCharacter48Loadout(t *testing.T) {
	cfg := filepath.Join("..", "..", "configs", "us100")
	loot, err := catalog.LoadLoot(filepath.Join(cfg, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := LoadEquipmentCatalog(filepath.Join(cfg, "equipment.generated.json"), loot.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	// Character 48 exact worn loadout: 4x Lv.10 ordinary equips (4 fame) +
	// 8 advanced SWAT avatars (8*88 = 704) + 1 skin avatar (69) = 777 fame.
	bag := Bag{
		Worn: []BagEquipment{
			{Slot: 14, Template: 400090164},
			{Slot: 20, Template: 22006},
			{Slot: 21, Template: 20008},
			{Slot: 22, Template: 24007},
		},
		WornAvatars: []BagEquipment{
			{Slot: 0, Template: 39165},
			{Slot: 1, Template: 39267},
			{Slot: 2, Template: 39370},
			{Slot: 3, Template: 40813},
			{Slot: 4, Template: 39472},
			{Slot: 5, Template: 39676},
			{Slot: 6, Template: 39778},
			{Slot: 7, Template: 39574},
			{Slot: 8, Template: 42215},
		},
	}
	if got := gear.BagAdventurerFame(bag); got != 777 {
		t.Fatalf("role 48 BagAdventurerFame=%d want=777", got)
	}
	// Extra special slots (title/creature/aura/weapon avatar) also contribute.
	bag.Worn = append(bag.Worn, BagEquipment{Slot: 13, Template: 9999991}, BagEquipment{Slot: 24, Template: 9999992})
	bag.WornAvatars = append(bag.WornAvatars, BagEquipment{Slot: 9, Template: 9999993}, BagEquipment{Slot: 10, Template: 9999994})
	wantExtra := uint32(777 + 315 + 315 + 140 + 72)
	if got := gear.BagAdventurerFame(bag); got != wantExtra {
		t.Fatalf("with title/creature/aura/weapon-avatar fame=%d want=%d", got, wantExtra)
	}
}
