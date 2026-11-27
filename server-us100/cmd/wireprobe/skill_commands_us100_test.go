package main

import (
	"bytes"
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUS100CustomizeCommandRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, err := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	l, err := character.LoadLearningCatalog("../../configs/us100/skills.generated.json", c.Source.Checksum)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "isolated.db")
	input := os.Getenv("US100_SKILL_COMMAND_COPY")
	if input != "" {
		b, e := os.ReadFile(input)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	open := func() *storage.Store {
		s, e := storage.Open(ctx, storage.Config{DBPath: path})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	store := open()
	if input == "" {
		p := c.Professions[0]
		e := store.Database().Mutate(func(tx *db100.Tx) error {
			return tx.Put("characters", "48", map[string]any{"id": 48, "account_id": 41, "wire_id": 1, "profession": 0, "config_version": c.Source.Checksum, "state": map[string]any{"source_sha256": p.RawSHA256, "initial_skill_cells": p.InitialSkills, "level": 10, "learned_skills": []any{map[string]int{"175": 1}, nil}, "test_sentinel": "unchanged"}})
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	get := func() storage.Character {
		r, e := store.LiveCharacter(41, 48)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	cs := character.Service{Store: store, Learning: l, Catalog: c}
	role := get()
	w := worldSession{role: role}
	var session skillSession
	check := func(body []byte, want []byte, seq byte) {
		t.Helper()
		p := make([]byte, 16)
		copy(p, body)
		response, e := session.handle(&cs, &w, 331, p, append([]byte{seq}, p...))
		if e != nil || len(response) != 2 || response[0].ID != 331 || !bytes.Equal(response[0].Payload, []byte{1}) {
			t.Fatalf("ack: %+v %v", response, e)
		}
		restore, err := cs.EntrySkills(get())
		if err != nil || response[1].ID != 19 || response[1].Kind != 0 || !bytes.Equal(response[1].Payload, restore) {
			t.Fatalf("command palette not refreshed: %v", err)
		}
		var s character.State
		if e = json.Unmarshal(get().State, &s); e != nil {
			t.Fatal(e)
		}
		if bytes.Equal(want, []byte{0, 8}) && bytes.Contains(response[1].Payload, []byte{16, 175, 1, 24, 1, 32, 0, 32, 8}) {
			t.Fatal("runtime refresh re-registered Leap's native UP, BUFF command")
		}
		if bytes.Equal(want, []byte{0, 3, 8}) && !bytes.Contains(response[1].Payload, []byte{16, 175, 1, 24, 1, 32, 0, 32, 3, 32, 8}) {
			t.Fatal("non-default command lost the leading UP direction")
		}
		if !bytes.Equal(s.SkillCommands[0][175], want) {
			t.Fatalf("persisted %v expected %v", s.SkillCommands[0][175], want)
		}
	}
	check([]byte{1, 175, 0, 1, 8}, []byte{8}, 1)       // exact captured request (Space)
	check([]byte{1, 175, 0, 2, 0, 8}, []byte{0, 8}, 4) // live 15:25 edit: first direction token is zero, not padding
	check([]byte{1, 175, 0, 3, 0, 3, 8}, []byte{0, 3, 8}, 5)
	check([]byte{1, 175, 0, 3, 1, 3, 8}, []byte{1, 3, 8}, 2)
	store.Close()
	store = open()
	defer store.Close()
	cs.Store = store
	payload, e := cs.EntrySkills(get())
	if e != nil || len(payload) < 4 || int(binary.LittleEndian.Uint32(payload)) != len(payload)-4 || !bytes.Contains(payload, []byte{16, 175, 1, 24, 1, 32, 1, 32, 3, 32, 8}) {
		t.Fatalf("missing restored skill entry: %v", e)
	}
	before := get().State
	for _, bad := range [][]byte{{1, 175, 0, 1, 3}, {1, 175, 0, 1, 8, 9}, {2, 175, 0, 1, 8}, {2, 175, 0, 1, 8, 175, 0, 1, 4}, {2, 175, 0, 1, 8, 0x2e, 0xfb, 1, 8}} {
		if _, e := session.handle(&cs, &w, 331, bad, bad); e == nil {
			t.Fatalf("accepted invalid edit %x", bad)
		}
		if !bytes.Equal(before, get().State) {
			t.Fatal("invalid batch changed character")
		}
	}
	check([]byte{1, 175, 0, 0}, nil, 3)
	var orig, after map[string]json.RawMessage
	if e = json.Unmarshal(role.State, &orig); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(get().State, &after); e != nil {
		t.Fatal(e)
	}
	delete(orig, "custom_skill_commands")
	delete(after, "custom_skill_commands")
	if !reflect.DeepEqual(orig, after) {
		t.Fatal("unrelated character fields changed")
	}
	if !observedGameRequest(331) {
		t.Fatal("custom command updates lost after sampling")
	}
}
