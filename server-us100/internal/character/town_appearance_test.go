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

func TestTownEntryProjectsEquippedCreatureSlot26(t *testing.T) {
	st := json.RawMessage(`{
		"level":18,
		"advancement":0,
		"inventory":{
			"version":"ordinary-bag-v1",
			"worn":[{"slot":26,"template":500990710,"creature_id":1}]
		}
	}`)
	role := storage.Character{WireID: 10, Name: "2", Profession: 0, State: st}
	p, err := new(Service).EntryBasicProbe(role, [2]byte{1, 20})
	if err != nil {
		t.Fatal(err)
	}
	if want := 261 + 1 + 1*31; len(p) != want {
		t.Fatalf("mode-0 length=%d want=%d", len(p), want)
	}
	eqStart := 138 + 1 + 5
	if p[eqStart] != 1 {
		t.Fatalf("appearance count=%d want=1", p[eqStart])
	}
	// 0x143803320 creature block begins 22 bytes after the appearance rows.
	creOff := eqStart + 1 + 31 + 22
	if got := binary.LittleEndian.Uint32(p[creOff : creOff+4]); got != 500990710 {
		t.Fatalf("mode-0 creature template = %d, want 500990710", got)
	}
	if got := binary.LittleEndian.Uint32(p[creOff+4 : creOff+8]); got != 0 {
		t.Fatalf("mode-0 default creature name len = %d, want 0", got)
	}
	if got := p[creOff+8]; got != 1 {
		t.Fatalf("mode-0 creature active byte = %d, want 1 (fed/visible in town & dungeon)", got)
	}
}

func TestRosterV224ProjectsEquippedAvatarsAndWeapon(t *testing.T) {
	ctx := t.Context()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "roster.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	acc, err := store.DevelopmentAccount(ctx, "probe")
	if err != nil {
		t.Fatal(err)
	}
	st := json.RawMessage(`{
		"level":16,
		"advancement":0,
		"inventory":{
			"version":"ordinary-bag-v1",
			"worn_avatars":[{"slot":0,"template":39165},{"slot":8,"template":42215}],
			"worn":[{"slot":12,"template":100001},{"slot":20,"template":22006}]
		}
	}`)
	if _, err := store.CreateCharacter(ctx, storage.Character{
		AccountID: acc, Name: "2", Profession: 0, ConfigVersion: "test", State: st,
	}, 8); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: store, Rules: Rules{MaxCharacters: 8, InitialLevel: 1}, V224: true}
	p, err := svc.List(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	// Header(15) + row(95 + 1 name byte + 3*31 visual slots) + tail(10)
	if want := 15 + 95 + 1 + 3*31 + 10; len(p) != want {
		t.Fatalf("roster length = %d, want %d", len(p), want)
	}
	// eqCount sits at header(15) + slot(2) + nameLen(4) + name(1) + 6 u8 + 2 u32 = 36
	if p[36] != 3 {
		t.Fatalf("roster appearance count = %d, want 3", p[36])
	}
	wantVisual := [][2]uint32{{0, 39165}, {8, 42215}, {12, 100001}}
	for i, w := range wantVisual {
		entry := p[37+i*31 : 37+(i+1)*31]
		if uint32(entry[0]) != w[0] || binary.LittleEndian.Uint32(entry[1:5]) != w[1] {
			t.Fatalf("roster visual %d = (%d,%d), want (%d,%d)", i, entry[0], binary.LittleEndian.Uint32(entry[1:5]), w[0], w[1])
		}
	}
}
