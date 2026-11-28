package character

import (
	"bytes"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestSourceAttributesMatchNativeLoader(t *testing.T) {
	c, e := catalog.LoadCharacters("../../configs/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("testdata/native_source_stats.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Profession byte
		SHA        string `json:"source_sha256"`
		Wire       string `json:"wire_hex"`
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e)
	}
	for _, r := range rows {
		p := c.Professions[r.Profession]
		if p.RawSHA256 != r.SHA {
			t.Fatal("fixture source changed")
		}
		state, _ := json.Marshal(State{Level: 1, Attributes: p.InitialAttributes, InitialSkills: p.InitialSkills, SourceSHA256: p.RawSHA256})
		got, e := (&Service{Catalog: c}).EntryAddition(storage.Character{WireID: 3, Profession: r.Profession, State: state})
		if e != nil {
			t.Fatal(e)
		}
		want, _ := hex.DecodeString(r.Wire)
		if !bytes.Equal(got[269:360], want) {
			t.Fatalf("profession %d differs from native PVF loader\ngot %x\nwant%x", r.Profession, got[269:360], want)
		}
	}
}

func TestEntryAdditionStatsUS100UsesSavedVitalsAndExperience(t *testing.T) {
	role := storage.Character{
		ID: 39, WireID: 1, Profession: 0,
		State: json.RawMessage(`{
			"level":7,"experience":4294967305,"attributes":{"[hp max]":416.5,"[mp max]":293.2},
			"initial_skill_cells":[10,1,1,3,2,1],"learned_skills":[{"3":4,"7":1},{"10":3,"2":5}]
		}`),
	}
	p, err := (&Service{}).EntryAdditionStatsUS100(role)
	if err != nil {
		t.Fatal(err)
	}
	skills := [2][]protocol.EntrySkill{
		{{ID: 10, Level: 1}, {ID: 3, Level: 4}, {ID: 7, Level: 1}},
		{{ID: 10, Level: 3}, {ID: 3, Level: 2}, {ID: 2, Level: 5}},
	}
	want, err := protocol.UserInfoAdditionStatsWithSkillsUS100(1, protocol.EntryAdditionBlock{Level: 7, Experience: 4294967305},
		protocol.EntryStatBlobUS100{HPMax: 4165, MPMax: 2932, BasePercent: 100}.Bytes(), skills)
	if err != nil || !bytes.Equal(p, want) {
		t.Fatalf("town entry did not preserve the saved attributes and experience: %v", err)
	}
}

func TestEntryVitalMaximumUsesProfessionGrowthOnlyForMissingValues(t *testing.T) {
	s := &Service{Rules: Rules{InitialLevel: 1}, Catalog: catalog.Characters{Professions: map[byte]catalog.Profession{
		0: {InitialAttributes: map[string]float32{"[hp max]": 180, "[mp max]": 140},
			BaseGrowth: map[string]float32{"[hp max]": 45, "[mp max]": 25}},
	}}}
	role := storage.Character{ID: 39, Profession: 0}
	state := State{Level: 3, Attributes: map[string]float32{"[mp max]": 145}}
	if hp, err := s.entryVitalMaximum(role, state, "[hp max]"); err != nil || hp != 2700 {
		t.Fatalf("legacy HP: got %d, %v", hp, err)
	}
	if mp, err := s.entryVitalMaximum(role, state, "[mp max]"); err != nil || mp != 1450 {
		t.Fatalf("persisted MP: got %d, %v", mp, err)
	}
	for _, n := range []float32{0, -1, 1e12} {
		state.Attributes["[hp max]"] = n
		if _, err := s.entryVitalMaximum(role, state, "[hp max]"); err == nil {
			t.Errorf("accepted invalid saved HP %v", n)
		}
	}
}

func TestEntryAdditionStatsUS100IncludesAllWornAvatarsAndSpecialSlots(t *testing.T) {
	// Reproduces character 48 wearing all 11 avatars (0..10 including Aura=9
	// and Weapon Avatar=10), ordinary gear (12..21), AND special worn slots
	// >= 26 (creature 26 with creature_id, artifact 27, insignia 31, talisman 33).
	// NOTI 13 ignores Space 3 and NOTI 14 Space 3 skips empty slots 0..11 and
	// 26..35, so NOTI 2 mode-1 must emit all worn avatars + equipment + special
	// slots (with slot 26's native type-0x1a creature block) instead of failing
	// to the 34-byte stub or dropping special worn slots.
	role := storage.Character{
		ID: 48, WireID: 10, Profession: 0,
		State: json.RawMessage(`{
			"level":18,
			"experience":12345,
			"attributes":{"[hp max]":945,"[mp max]":565,"[physical attack]":89.1,"[physical defense]":89.1},
			"inventory":{
				"version":"ordinary-bag-v1",
				"worn_avatars":[
					{"slot":9,"template":501590378},
					{"slot":0,"template":101550957},
					{"slot":1,"template":101561096},
					{"slot":2,"template":101570856},
					{"slot":3,"template":101501116},
					{"slot":4,"template":101511276},
					{"slot":5,"template":101541032},
					{"slot":6,"template":101520917},
					{"slot":7,"template":101530876},
					{"slot":8,"template":42215},
					{"slot":10,"template":501600810}
				],
				"worn":[
					{"slot":12,"template":101010938,"durability":45},
					{"slot":13,"template":500330547,"durability":0},
					{"slot":14,"template":400090164,"durability":60},
					{"slot":26,"template":500990710,"creature_id":1},
					{"slot":27,"template":501000001},
					{"slot":31,"template":100350001},
					{"slot":33,"template":100401044}
				]
			}
		}`),
	}
	got, err := (&Service{}).EntryAdditionStatsUS100(role)
	if err != nil {
		t.Fatalf("EntryAdditionStatsUS100 failed with worn avatars and special slots >= 26: %v", err)
	}
	wantBlob := protocol.EntryStatBlobUS100{
		HPMax:       9450,
		MPMax:       5650,
		Strength:    891,
		Vitality:    891,
		BasePercent: 100,
	}.Bytes()
	wantWorn := []protocol.ActorWornUS224{
		{Slot: 0, Template: 101550957, Avatar: true},
		{Slot: 1, Template: 101561096, Avatar: true},
		{Slot: 2, Template: 101570856, Avatar: true},
		{Slot: 3, Template: 101501116, Avatar: true},
		{Slot: 4, Template: 101511276, Avatar: true},
		{Slot: 5, Template: 101541032, Avatar: true},
		{Slot: 6, Template: 101520917, Avatar: true},
		{Slot: 7, Template: 101530876, Avatar: true},
		{Slot: 8, Template: 42215, Avatar: true},
		{Slot: 9, Template: 501590378, Avatar: true},
		{Slot: 10, Template: 501600810, Avatar: true},
		{Slot: 12, Template: 101010938, Durability: 45, Avatar: false},
		{Slot: 13, Template: 500330547, Durability: 0, Avatar: false},
		{Slot: 14, Template: 400090164, Durability: 60, Avatar: false},
		{Slot: 26, Template: 500990710, Avatar: false, CreatureID: 1},
		{Slot: 27, Template: 501000001, Avatar: false},
		{Slot: 31, Template: 100350001, Avatar: false},
		{Slot: 33, Template: 100401044, Avatar: false},
	}
	wantFame := (*inventory.EquipmentCatalog)(nil).AdventurerFame(role.State)
	want, err := protocol.UserInfoAdditionWithWornUS224(
		10,
		protocol.EntryAdditionBlock{Level: 18, Experience: 12345, Value19: wantFame},
		wantBlob,
		[2][]protocol.EntrySkill{},
		wantWorn,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("mode-1 payload mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}
