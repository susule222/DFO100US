package main

import (
	"bytes"
	"context"
	"dfolan/internal/db100"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestLoadCharacterOptionsEnablesAuraByDefaultAndMigratesLegacyZero(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aura_opts.db")
	store, err := storage.Open(context.Background(), storage.Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Seed a live character via storage API.
	ctx := context.Background()
	accountID, err := store.DevelopmentAccount(ctx, "aura_tester")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     accountID,
		Name:          "AuraTester",
		ConfigVersion: "v1",
		State:         json.RawMessage(`{"level":100}`),
	}, 16)
	if err != nil {
		t.Fatal(err)
	}
	charID := ch.ID

	// 1. Brand-new character (no saved row) -> NOTI 376 still sent with opt[98]=1 and others 0xFFFF.
	body, err := loadCharacterOptions(store, accountID, charID)
	if err != nil {
		t.Fatalf("load default options: %v", err)
	}
	if len(body) != 4+characterOptionSize {
		t.Fatalf("default NOTI 376 len = %d, want %d", len(body), 4+characterOptionSize)
	}
	if got := binary.LittleEndian.Uint32(body[:4]); got != characterOptionSize {
		t.Fatalf("header size = %d, want %d", got, characterOptionSize)
	}
	if got := binary.LittleEndian.Uint16(body[4+characterOptionShowAuraIndex*2:]); got != 1 {
		t.Fatalf("new char opt[98] (ShowAura) = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(body[4:]); got != 0xffff {
		t.Fatalf("unrelated opt[0] = %#x, want 0xffff sentinel", got)
	}

	// 2. Legacy pre-fix saved row with opt[98]=0 (saved while checkbox 121 loaded unchecked from 0xFFFF)
	// and no migration marker -> repaired once to opt[98]=1.
	legacyZero := bytes.Repeat([]byte{0xff}, characterOptionSize)
	binary.LittleEndian.PutUint16(legacyZero[characterOptionShowAuraIndex*2:], 0)
	if err := store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put(characterOptionsTable, characterOptionKey(accountID, charID), legacyZero)
	}); err != nil {
		t.Fatal(err)
	}
	body, err = loadCharacterOptions(store, accountID, charID)
	if err != nil {
		t.Fatalf("load legacy zero options: %v", err)
	}
	if got := binary.LittleEndian.Uint16(body[4+characterOptionShowAuraIndex*2:]); got != 1 {
		t.Fatalf("migrated legacy char opt[98] = %d, want 1", got)
	}

	// 3. Explicit user save setting opt[98]=0 AFTER migration must be respected and NOT overwritten.
	explicitOff := make([]byte, 520)
	binary.LittleEndian.PutUint32(explicitOff[:4], characterOptionSize)
	copy(explicitOff[4:516], bytes.Repeat([]byte{0xff}, characterOptionSize))
	binary.LittleEndian.PutUint16(explicitOff[4+characterOptionShowAuraIndex*2:], 0)
	if err := saveCharacterOptions(store, accountID, charID, explicitOff); err != nil {
		t.Fatalf("save explicit aura off: %v", err)
	}
	body, err = loadCharacterOptions(store, accountID, charID)
	if err != nil {
		t.Fatalf("reload after explicit off: %v", err)
	}
	if got := binary.LittleEndian.Uint16(body[4+characterOptionShowAuraIndex*2:]); got != 0 {
		t.Fatalf("explicitly disabled opt[98] = %d, want 0", got)
	}

	// 4. Unset sentinel 0xFFFF in opt[98] is always normalized to 1.
	unsetRow := make([]byte, 520)
	binary.LittleEndian.PutUint32(unsetRow[:4], characterOptionSize)
	copy(unsetRow[4:516], bytes.Repeat([]byte{0xff}, characterOptionSize))
	if err := saveCharacterOptions(store, accountID, charID, unsetRow); err != nil {
		t.Fatalf("save unset row: %v", err)
	}
	body, err = loadCharacterOptions(store, accountID, charID)
	if err != nil {
		t.Fatalf("reload unset row: %v", err)
	}
	if got := binary.LittleEndian.Uint16(body[4+characterOptionShowAuraIndex*2:]); got != 1 {
		t.Fatalf("unset sentinel opt[98] = %d, want 1", got)
	}
}

func TestEntryPlanSendsCharacterOptionsBeforeTownSpawn(t *testing.T) {
	p := entryPayloads{
		Basic:            []byte{0, 1},
		Skills:           []byte{1},
		GeneralOptions:   []byte{1},
		CharacterOptions: []byte{1},
		UserArea:         []byte{1},
		Area:             []byte{1},
	}
	pkts := p.packets()
	pos := map[string]int{}
	for i, pkt := range pkts {
		if len(pkt.Payload) > 0 {
			pos[pkt.Name] = i
		}
	}
	if pos["character_options_restored"] <= pos["entry_skills_sent"] {
		t.Fatalf("NOTI 376 (%d) must follow Skills (%d)", pos["character_options_restored"], pos["entry_skills_sent"])
	}
	if pos["character_options_restored"] >= pos["user_area_sent"] {
		t.Fatalf("NOTI 376 (%d) must precede UserArea/town spawn (%d)", pos["character_options_restored"], pos["user_area_sent"])
	}
}
