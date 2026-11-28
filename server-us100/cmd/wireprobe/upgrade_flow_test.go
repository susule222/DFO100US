package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/binary"
	"path/filepath"
	"testing"
)

func buildCmd80Payload(mode, space byte, eqSlot uint16, eqTpl uint32, ticketSlot, protectSlot uint16, name string) []byte {
	var p []byte
	p = append(p, mode, space)
	p = append(p, byte(eqSlot), byte(eqSlot>>8))
	p = binary.LittleEndian.AppendUint32(p, eqTpl)
	p = append(p, byte(ticketSlot), byte(ticketSlot>>8))
	p = append(p, byte(protectSlot), byte(protectSlot>>8))
	p = binary.LittleEndian.AppendUint32(p, uint32(len(name)))
	p = append(p, []byte(name)...)
	p = append(p, 0xff, 0xff, 0xff, 0xff, 0)
	for len(p)%8 != 0 {
		p = append(p, 0)
	}
	return p
}

func TestUpgradeFlowReinforceAmplifyAndGrimoire(t *testing.T) {
	tickets, err := catalog.LoadTickets("../../configs/us100/tickets.generated.json")
	if err != nil {
		t.Fatalf("load tickets catalog: %v", err)
	}
	if tk, ok := tickets.Tickets[590006248]; !ok || tk.Kind != "amplify" || tk.Level != 10 || tk.Rate != 50 {
		t.Fatalf("unexpected 590006248 entry: %+v ok=%v", tk, ok)
	}
	if tk, ok := tickets.Tickets[590006279]; !ok || tk.Kind != "reinforce" || tk.Level != 10 || tk.Rate != 1 {
		t.Fatalf("unexpected 590006279 entry: %+v ok=%v", tk, ok)
	}

	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{DBPath: filepath.Join(t.TempDir(), "upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	acctID, err := store.DevelopmentAccount(ctx, "upgrade-tester")
	if err != nil {
		t.Fatal(err)
	}
	initBag := inventory.Bag{
		Version: "ordinary-bag-v1",
		Gold:    100000,
		Items: []inventory.BagItem{
			{Slot: 73, Template: 590006252, Amount: 1}, // +11 50% Amplification Ticket
			{Slot: 74, Template: 590006279, Amount: 2}, // +10 1% Reinforcement Ticket
			{Slot: 75, Template: 590006248, Amount: 1}, // +10 50% Amplification Ticket
			{Slot: 76, Template: 10000902, Amount: 1},  // Golden Grimoire (+3..+6)
		},
		Equipment: []inventory.BagEquipment{
			{Slot: 9, Template: 100331477, Durability: 45},
		},
		Worn: []inventory.BagEquipment{
			{Slot: 14, Template: 400090164, Durability: 60},
		},
	}
	stateJSON, err := inventory.SaveBag([]byte(`{"level":100}`), initBag)
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateCharacter(ctx, storage.Character{
		AccountID:     acctID,
		Name:          "AmpTester",
		ConfigVersion: tickets.Source.Checksum,
		State:         stateJSON,
	}, 12)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Reinforcement Ticket (590006279) on worn weapon (space=3, slot=14) with deterministic success (rng->0).
	reqReinforce := buildCmd80Payload(0, 3, 14, 400090164, 74, 0xffff, "Weapon")
	role, plan, res, err := handleUpgradeItem(ctx, store, nil, tickets, acctID, role, []byte("frame1"), reqReinforce, func(int) int { return 0 })
	if err != nil {
		t.Fatalf("reinforce ticket failed: %v", err)
	}
	if res.Outcome != 0 || res.NewLevel != 10 || res.TicketRemaining != 1 || res.UseTicketType != 1 {
		t.Fatalf("unexpected reinforce result: %+v", res)
	}
	if len(plan) < 3 {
		t.Fatalf("expected ACK80 + NOTI14(space0) + NOTI14(space3), got %d packets", len(plan))
	}
	b, _ := inventory.ReadBag(role.State)
	if b.Worn[0].Upgrade != 10 {
		t.Fatalf("worn weapon should be +10, got +%d", b.Worn[0].Upgrade)
	}

	// 2. Golden Grimoire (CMD 205 mode=2, slot 76 -> bag equip slot 9) grants Dimensional Strength (3) + initial amplify level.
	p205 := []byte{2, 9, 0}
	p205 = binary.LittleEndian.AppendUint32(p205, 100331477)
	p205 = append(p205, 76, 0)
	p205 = binary.LittleEndian.AppendUint32(p205, 10000902)
	p205 = append(p205, 3) // Strength
	p205 = binary.LittleEndian.AppendUint32(p205, 4)
	p205 = append(p205, []byte("Gear")...)
	for len(p205)%8 != 0 {
		p205 = append(p205, 0)
	}
	role, plan205, err := handleInvestAmplifyOption(ctx, store, nil, tickets, acctID, role, []byte("frame2"), p205, func(int) int { return 0 })
	if err != nil {
		t.Fatalf("golden grimoire failed: %v", err)
	}
	if len(plan205) != 2 || plan205[0].ID != protocol.CmdInvestAmplifyOption {
		t.Fatalf("unexpected grimoire plan: %d", len(plan205))
	}
	b, _ = inventory.ReadBag(role.State)
	if b.Equipment[0].AmplifyType != 3 || b.Equipment[0].Upgrade != 3 || b.Equipment[0].AmplifyValue == 0 {
		t.Fatalf("expected +3 Dimensional Strength after grimoire, got %+v", b.Equipment[0])
	}

	// 3. Amplification Ticket 590006248 (+10 50%) on bag equip slot 9 -> success -> +10 with scaled AmplifyValue!
	reqAmp10 := buildCmd80Payload(1, 0, 9, 100331477, 75, 0xffff, "Gear")
	role, _, resAmp, err := handleUpgradeItem(ctx, store, nil, tickets, acctID, role, []byte("frame3"), reqAmp10, func(int) int { return 10 })
	if err != nil {
		t.Fatalf("amplify ticket +10 failed: %v", err)
	}
	if resAmp.Outcome != 0 || resAmp.OldLevel != 3 || resAmp.NewLevel != 10 || resAmp.TicketRemaining != 0 {
		t.Fatalf("unexpected amplify +10 result: %+v", resAmp)
	}
	b, _ = inventory.ReadBag(role.State)
	if b.Equipment[0].Upgrade != 10 || b.Equipment[0].AmplifyType != 3 || b.Equipment[0].AmplifyValue < 50 {
		t.Fatalf("expected +10 amplified equipment with scaled stat, got %+v", b.Equipment[0])
	}
	row := inventory.EquipmentRow(b.Equipment[0])
	if row[10] != 10 || row[19] != 3 || binary.LittleEndian.Uint16(row[20:22]) != b.Equipment[0].AmplifyValue {
		t.Fatalf("EquipmentRow mismatch: upgrade=%d ampType=%d ampVal=%d", row[10], row[19], binary.LittleEndian.Uint16(row[20:22]))
	}

	// 4. Amplification Ticket 590006252 (+11 50%) failure (rng->99) -> keeps +10, consumes ticket!
	reqAmp11 := buildCmd80Payload(1, 0, 9, 100331477, 73, 0xffff, "Gear")
	role, _, resFail, err := handleUpgradeItem(ctx, store, nil, tickets, acctID, role, []byte("frame4"), reqAmp11, func(int) int { return 99 })
	if err != nil {
		t.Fatalf("amplify ticket +11 fail case errored: %v", err)
	}
	if resFail.Outcome != 1 || resFail.OldLevel != 10 || resFail.NewLevel != 10 || resFail.TicketRemaining != 0 {
		t.Fatalf("unexpected amplify +11 fail result: %+v", resFail)
	}
}
