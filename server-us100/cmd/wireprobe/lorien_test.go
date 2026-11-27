package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/dungeon"
	"dfolan/internal/progression"
	"dfolan/internal/storage"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestLorienFinalRoom475(t *testing.T) {
	c, e := catalog.LoadDungeons("../../runtime/us100_tutorial/dungeons.json")
	if e != nil {
		t.Fatal(e)
	}
	s, e := dungeon.SelectTutorial(c, 7115)
	if e != nil {
		t.Fatal(e)
	}
	w := worldSession{role: storage.Character{ID: 47, WireID: 9}, inTutorial: true, activeDungeon: s}
	if !observedGameRequest(475) {
		t.Fatal("request not retained")
	}
	for x := byte(1); x <= 3; x++ {
		s.Loaded = true
		for _, m := range s.Monsters {
			if _, e = s.ConfirmDeath(uint32(m.Entity), 9, 9); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = w.lorienFinale(nil); e == nil {
			t.Fatal("early completion from another room")
		}
		s, e = s.Move(c, [2]byte{x, 0})
		if e != nil {
			t.Fatal(e)
		}
		w.activeDungeon = s
	}
	if _, e = w.lorienFinale(nil); e == nil {
		t.Fatal("unloaded finale")
	}
	s.Loaded = true
	// Order from the real final encounter, not the source-template order.
	for _, entity := range []uint32{0x100b, 0x100f, 0x100d, 0x100c, 0x100e} {
		if _, e = w.lorienFinale(nil); e == nil {
			t.Fatal("one or more enemies still alive")
		}
		if _, e = s.ConfirmDeath(entity, 9, 9); e != nil {
			t.Fatal(e)
		}
	}
	w.inTutorial = false
	if _, e = w.lorienFinale(nil); e == nil {
		t.Fatal("accepted outside tutorial")
	}
	w.inTutorial = true
	if _, e = w.lorienFinale([]byte{0}); e == nil {
		t.Fatal("unexpected payload")
	}
	plan, e := w.lorienFinale(nil)
	if e != nil || len(plan) != 1 || plan[0].ID != 31 || !s.Completed() || s.CompletionTarget() != 0 {
		t.Fatal(plan, e)
	}
	w.completionSent = true
	if p, e := w.lorienFinale(nil); e != nil || len(p) != 0 {
		t.Fatal("duplicate clear", p, e)
	}
	// Optional reproduction against an isolated copy of the affected role. Only
	// a temp DB is writable. Confirm ordinary result/experience processing too.
	path := os.Getenv("US100_TEST_LORIEN_DB")
	if path == "" {
		return
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	path = filepath.Join(t.TempDir(), "copy.db")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	store, e := storage.Open(ctx, storage.Config{DBPath: path})
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	role, e := store.LiveCharacter(41, 47)
	if e != nil {
		t.Fatal(e)
	}
	role.WireID = 9
	w.role = role
	cat, e := catalog.LoadProgression("../../configs/us100/progression.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	prof, e := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	rules, e := progression.LoadRules("../../configs/us100/rules.experience.json")
	if e != nil {
		t.Fatal(e)
	}
	w.progression = &character.ProgressionService{Store: store, Catalog: cat, Professions: prof, Rules: rules}
	result := make([]byte, 113)
	result[3] = 1
	binary.LittleEndian.PutUint16(result[4:], 9)
	p, e := w.dungeonResult(result)
	if e != nil || len(p) != 3 || p[0].ID != 34 || p[2].ID != 35 {
		t.Fatal(p, e)
	}
	w.resultSent = true
	if p, e = w.dungeonResult(result); e != nil || len(p) != 0 {
		t.Fatal("duplicate result", e, p)
	}
}
