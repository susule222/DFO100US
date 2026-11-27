package quest

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/inventory"
	"testing"
)

func TestHandInConsumesOnlyAvailableInputs(t *testing.T) {
	b := inventory.Bag{Gold: 4, Items: []inventory.BagItem{{Template: 10, Amount: 1}, {Template: 10, Amount: 2}}}
	for _, needs := range [][]ItemNeed{{{0, 2}, {10, 4}}, {{10, 3}, {0, 5}}, {{10, 2}, {10, 2}}} {
		if _, ok := takeItems(b, needs); ok || b.Gold != 4 || b.Items[0].Amount != 1 || b.Items[1].Amount != 2 {
			t.Fatalf("partial or invalid debit for %v", needs)
		}
	}
	out, ok := takeItems(b, []ItemNeed{{0, 3}, {10, 2}})
	if !ok || out.Gold != 1 || len(out.Items) != 1 || out.Items[0].Amount != 1 || b.Items[0].Amount != 1 {
		t.Fatalf("invalid debit: %+v", out)
	}
}

func TestConditionalClearRequiresKnownShape(t *testing.T) {
	d := catalog.QuestDefinition{Kind: "[condition under clear]", ObjectiveCells: []pvf.Token{{Value: 3}, {Value: 1}, {Value: 5}}}
	d.Script.Cells = []pvf.Token{{Type: 3, Text: "[sub type]"}, {Value: 6}}
	if n, model, err := InitialProgress(d); n != 5 || model != ClearDungeon || err != nil {
		t.Fatalf("counted dungeon: %d %s %v", n, model, err)
	}
	d.Script.Cells[1].Value = 11
	if _, _, e := InitialProgress(d); e == nil {
		t.Fatal("unknown clear condition accepted")
	}
	d.Script.Cells = []pvf.Token{{Type: 3, Text: "[creature kind]"}, {Value: 13}}
	if unverifiedSection(d) != "[creature kind]" {
		t.Fatal("pet restriction lost")
	}
}
