package character

import (
	"dfolan/internal/catalog"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestTownEntryEncodesEquippedAvatarAndAdventurerFame(t *testing.T) {
	cfg := filepath.Join("..", "..", "configs", "us100")
	loot, err := catalog.LoadLoot(filepath.Join(cfg, "loot.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	gear, err := inventory.LoadEquipmentCatalog(filepath.Join(cfg, "equipment.generated.json"), loot.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	st := json.RawMessage(`{
		"level":15,
		"advancement":0,
		"attributes":{"[hp max]":347,"[mp max]":166},
		"inventory":{
			"version":"ordinary-bag-v1",
			"worn_avatars":[{"slot":0,"template":39165},{"slot":8,"template":42215}],
			"worn":[{"slot":12,"template":100001},{"slot":20,"template":22006}]
		}
	}`)
	role := storage.Character{WireID: 10, Name: "2", Profession: 0, State: st}
	svc := &Service{Equipment: gear}
	p, err := svc.EntryBasicProbe(role, [2]byte{})
	if err != nil {
		t.Fatal(err)
	}
	// 261 base + 1 name byte + 3 visual appearance rows (slots 0, 8, 12; slot 20 is non-visual armor).
	if want := 261 + 1 + 3*31; len(p) != want {
		t.Fatalf("mode-0 length=%d want=%d", len(p), want)
	}
	wantFame := gear.AdventurerFame(st)
	if wantFame <= 88+69 {
		t.Fatalf("expected equipment+avatar fame > 157, got %d", wantFame)
	}
	if got := binary.LittleEndian.Uint32(p[124:128]); got != wantFame {
		t.Fatalf("mode-0 fame=%d want=%d", got, wantFame)
	}
	eqStart := 138 + 1 + 5
	if p[eqStart] != 3 {
		t.Fatalf("appearance count=%d want=3", p[eqStart])
	}
	wantVisual := [][2]uint32{{0, 39165}, {8, 42215}, {12, 100001}}
	for i, w := range wantVisual {
		row := p[eqStart+1+i*31 : eqStart+1+(i+1)*31]
		if uint32(row[0]) != w[0] || binary.LittleEndian.Uint32(row[1:5]) != w[1] {
			t.Fatalf("visual row %d = (%d,%d), want (%d,%d)", i, row[0], binary.LittleEndian.Uint32(row[1:5]), w[0], w[1])
		}
	}

	mode1, err := svc.EntryAdditionStatsUS100(role)
	if err != nil {
		t.Fatal(err)
	}
	// Mode-1 prefix is u8(1) + u16(1) + u16(actorID), followed by 23-byte EntryAdditionBlock
	// whose Value19 (fame) starts at offset 5+19 = 24.
	if got := binary.LittleEndian.Uint32(mode1[24:28]); got != wantFame {
		t.Fatalf("mode-1 Value19 fame=%d want=%d", got, wantFame)
	}

	restore, err := inventory.WornPayload(st)
	if err != nil || len(restore) < 3 || restore[0] != 3 || restore[1] != 4 || restore[2] != 0 {
		t.Fatalf("equipped items not restored: %v", err)
	}
}

func TestTownEntryProjectsEquippedTitleSlot13(t *testing.T) {
	st := json.RawMessage(`{
		"level":15,
		"advancement":0,
		"inventory":{
			"version":"ordinary-bag-v1",
			"worn_avatars":[{"slot":0,"template":39165}],
			"worn":[{"slot":13,"template":100331498},{"slot":20,"template":22006}]
		}
	}`)
	role := storage.Character{WireID: 10, Name: "2", Profession: 0, State: st}
	p, err := new(Service).EntryBasicProbe(role, [2]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if want := 261 + 1 + 2*31; len(p) != want {
		t.Fatalf("mode-0 length=%d want=%d", len(p), want)
	}
	eqStart := 138 + 1 + 5
	if p[eqStart] != 2 {
		t.Fatalf("appearance count=%d want=2", p[eqStart])
	}
	row0 := p[eqStart+1 : eqStart+1+31]
	row1 := p[eqStart+1+31 : eqStart+1+62]
	if row0[0] != 0 || binary.LittleEndian.Uint32(row0[1:5]) != 39165 {
		t.Fatalf("row0 = (%d,%d), want (0,39165)", row0[0], binary.LittleEndian.Uint32(row0[1:5]))
	}
	if row1[0] != 13 || binary.LittleEndian.Uint32(row1[1:5]) != 100331498 {
		t.Fatalf("row1 = (%d,%d), want (13,100331498)", row1[0], binary.LittleEndian.Uint32(row1[1:5]))
	}
}
