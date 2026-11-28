package main

import (
	"context"
	"dfolan/internal/character"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStaminaRecoveryCostFormulaMatchesClientPVF(t *testing.T) {
	// Verified from DFO_unpacked.exe 0x14592ba90 + etc/(f)serverparameter.etc:
	// cost = ((100 - stamina) * table[level-1]) / 90.
	cases := []struct {
		level, stamina, want uint32
	}{
		{1, 0, 0},
		{5, 0, 0},
		{6, 0, 281},    // 100 * 253 / 90 = 281
		{18, 0, 6413},  // 100 * 5772 / 90 = 6413 (exact live screenshot match!)
		{18, 10, 5772}, // 90 * 5772 / 90 = 5772
		{18, 100, 0},
		{100, 0, 72472}, // 100 * 65225 / 90 = 72472
	}
	for _, tc := range cases {
		if got := protocol.StaminaRecoveryCost(tc.level, tc.stamina); got != tc.want {
			t.Fatalf("StaminaRecoveryCost(lv=%d, stam=%d) = %d, want %d", tc.level, tc.stamina, got, tc.want)
		}
	}
}

func TestCmd9RecoverStaminaFlow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(dir, "stamina.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateCharacterEvents(ctx); err != nil {
		t.Fatal(err)
	}
	acctID, err := store.DevelopmentAccount(ctx, "stamina-tester")
	if err != nil {
		t.Fatal(err)
	}
	const checksum = "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69"
	initState, err := inventory.SaveBag(json.RawMessage(`{"level":18,"experience":0}`), inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    5000, // less than Lv.18 fee (6413)
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "WeakTester",
		Profession:    0,
		ConfigVersion: checksum,
		State:         initState,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}

	w := &worldSession{
		account:    acctID,
		role:       role,
		characters: &character.Service{Store: store},
	}

	// 1. Insufficient gold -> refused with code 0x16 (22) and state unchanged.
	if _, code, err := w.handleRecoverStamina(ctx, []byte("cmd9-low-gold")); err == nil || code != protocol.StaminaRecoveryErrInsufficientGold {
		t.Fatalf("expected insufficient gold refusal (0x16), got err=%v code=%d", err, code)
	}
	if _, persisted := readCharacterStamina(w.role.State); persisted {
		t.Fatalf("stamina must not be persisted on refused recovery")
	}

	// 2. Grant 10000 gold and retry CMD 9 -> deducts exact 6413, leaves 3587, persists stamina=100.
	richState, err := inventory.SaveBag(w.role.State, inventory.Bag{Version: "ordinary-bag-v1", Gold: 10000})
	if err != nil {
		t.Fatal(err)
	}
	savedRich, _, err := store.CommitCharacterEvent(ctx, acctID, role.ID, checksum, "seed-gold", "test", func(storage.Character) (json.RawMessage, json.RawMessage, error) {
		return richState, json.RawMessage(`{"ok":true}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w.role = savedRich

	pkts, code, err := w.handleRecoverStamina(ctx, []byte("cmd9-ok"))
	if err != nil || code != 0 {
		t.Fatalf("expected recovery success, got err=%v code=%d", err, code)
	}
	if len(pkts) != 1 || pkts[0].Kind != 1 || pkts[0].ID != protocol.CmdRecoverStamina {
		t.Fatalf("unexpected recovery packets: %+v", pkts)
	}
	if len(pkts[0].Payload) != 5 || pkts[0].Payload[0] != 1 {
		t.Fatalf("expected 5-byte ACK 9 success payload, got %x", pkts[0].Payload)
	}
	if gotGold := binary.LittleEndian.Uint32(pkts[0].Payload[1:5]); gotGold != 10000-6413 {
		t.Fatalf("ACK 9 newGold = %d, want %d", gotGold, 10000-6413)
	}

	bagAfter, err := inventory.ReadBag(w.role.State)
	if err != nil {
		t.Fatal(err)
	}
	if bagAfter.Gold != 10000-6413 {
		t.Fatalf("persisted bag gold = %d, want %d", bagAfter.Gold, 10000-6413)
	}
	stam, persisted := readCharacterStamina(w.role.State)
	if !persisted || stam != 100 {
		t.Fatalf("expected persisted stamina=100, got stam=%d persisted=%v", stam, persisted)
	}

	// 3. Re-check observedGameRequest includes CMD 9 so it never hits BodySampleLimit.
	if !observedGameRequest(protocol.CmdRecoverStamina) {
		t.Fatalf("CMD 9 must be listed in observedGameRequest")
	}
}
