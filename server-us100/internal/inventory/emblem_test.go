package inventory

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"testing"
)

func TestIsEmblemTemplateCoversPlatinumAndRejectsNonEmblems(t *testing.T) {
	for _, id := range []uint32{2050, 2500000, 590004703, 590004744, 590004899, 590704677} {
		if !IsEmblemTemplate(id) {
			t.Fatalf("expected emblem template %d to match IsEmblemTemplate", id)
		}
	}
	for _, id := range []uint32{0, 15, 1107, 10327281, 590006265, 100401044} {
		if IsEmblemTemplate(id) {
			t.Fatalf("expected non-emblem template %d not to match IsEmblemTemplate", id)
		}
	}
}

func TestReadBagAutoMigratesMisplacedEmblemsToEmblemTab(t *testing.T) {
	raw := json.RawMessage(`{
		"inventory": {
			"version": "ordinary-bag-v1",
			"gold": 1000,
			"items": [
				{"slot": 65, "Template": 1113, "Amount": 5},
				{"slot": 79, "Template": 590004744, "Amount": 1},
				{"slot": 80, "Template": 590004744, "Amount": 2},
				{"slot": 289, "Template": 590004703, "Amount": 1}
			]
		}
	}`)
	b, err := ReadBag(raw)
	if err != nil {
		t.Fatalf("ReadBag failed: %v", err)
	}
	byTpl := map[uint32]BagItem{}
	for _, it := range b.Items {
		byTpl[it.Template] = it
	}
	if got := byTpl[1113]; got.Slot != 65 || got.Amount != 5 {
		t.Fatalf("ordinary consumable 1113 must remain in slot 65, got %+v", got)
	}
	if got := byTpl[590004703]; got.Slot != 289 || got.Amount != 1 {
		t.Fatalf("existing emblem 590004703 must remain in slot 289, got %+v", got)
	}
	got := byTpl[590004744]
	if got.Slot != 290 || got.Amount != 3 {
		t.Fatalf("expected misplaced 590004744 stacks to migrate & merge into slot 290 x3, got %+v", got)
	}
}

func TestBagAddAndAwarderGrantRouteEmblemsToEmblemTab(t *testing.T) {
	rules, err := LoadBagRules("../../configs/us100/rules.bag.json")
	if err != nil {
		t.Fatalf("LoadBagRules: %v", err)
	}
	if got := rules.Slots[EmblemBagCategory]; got != [2]uint16{EmblemSlotStart, EmblemSlotEnd} {
		t.Fatalf("expected %s slots [%d,%d], got %v", EmblemBagCategory, EmblemSlotStart, EmblemSlotEnd, got)
	}

	// Bag.Add routes emblem templates into 289..360 even if caller passes a [throw] fallback.
	src := catalog.LootCatalog{
		Source: pvf.ArchiveSnapshot{Checksum: rules.Source},
		Items: map[uint32]catalog.LootItem{
			590004744: {ID: 590004744, Kind: "stackable", StackableType: "[throw]", StackLimit: 1000},
		},
	}
	b := Bag{Version: "ordinary-bag-v1"}
	b, slot, err := b.Add(src, rules, 590004744, 1)
	if err != nil {
		t.Fatalf("Bag.Add emblem: %v", err)
	}
	if slot < EmblemSlotStart || slot > EmblemSlotEnd {
		t.Fatalf("expected emblem in %d..%d, got slot %d", EmblemSlotStart, EmblemSlotEnd, slot)
	}

	// Awarder.Grant accepts US100 emblems not listed in loot.generated.json and places them in 289..360.
	awarder := &Awarder{
		Catalog: catalog.LootCatalog{Source: pvf.ArchiveSnapshot{Checksum: rules.Source}, Items: map[uint32]catalog.LootItem{}},
		Rules:   rules,
	}
	nextRaw, receipt, err := awarder.Grant(json.RawMessage(`{}`), 590004744, 2)
	if err != nil {
		t.Fatalf("Awarder.Grant emblem: %v", err)
	}
	if len(receipt.Slots) != 1 || receipt.Slots[0] != EmblemSlotStart {
		t.Fatalf("expected Awarder.Grant slot %d, got %+v", EmblemSlotStart, receipt)
	}
	saved, err := ReadBag(nextRaw)
	if err != nil {
		t.Fatalf("ReadBag after grant: %v", err)
	}
	if len(saved.Items) != 1 || saved.Items[0].Slot != EmblemSlotStart || saved.Items[0].Template != 590004744 || saved.Items[0].Amount != 2 {
		t.Fatalf("unexpected saved emblem bag: %+v", saved.Items)
	}
}
