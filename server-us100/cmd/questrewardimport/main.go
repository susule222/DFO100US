// questrewardimport adds exact-source quest templates to the existing item
// catalogs. Added rows are eligible for grants without entering drop pools.
package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/inventory"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func must[T any](v T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return v
}

func sections(c []pvf.Token) map[string][]pvf.Token {
	out := map[string][]pvf.Token{}
	var tag string
	for _, t := range c {
		if t.Type == 3 {
			tag = t.Text
			continue
		}
		out[tag] = append(out[tag], t)
	}
	return out
}

func templates(c []pvf.Token, dst map[uint32]bool) error {
	for len(c) > 0 {
		if c[0].Type != 0 || c[0].Value < 0 {
			return fmt.Errorf("invalid template")
		}
		id := uint32(c[0].Value)
		c = c[1:]
		if len(c) > 0 && c[0].Type == 6 && c[0].Text == "[job]" {
			if len(c) < 4 || c[1].Type != 0 || c[2].Type != 0 {
				return fmt.Errorf("invalid job tuple")
			}
			c = c[3:]
		}
		if len(c) == 0 || c[0].Type != 0 || c[0].Value < 0 {
			return fmt.Errorf("invalid count")
		}
		if id != 0 && c[0].Value > 0 {
			dst[id] = true
		}
		c = c[1:]
	}
	return nil
}

func main() {
	dir := flag.String("configs", "configs/us100", "source catalogs")
	source := flag.String("source", "../runtime/pvf_source/Script.inner.pvf", "source PVF")
	output := flag.String("out", "", "existing staging directory")
	flag.Parse()
	if *output == "" {
		log.Fatal("-out is required")
	}
	a := must(pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1 << 30}))
	q := must(catalog.LoadQuests(filepath.Join(*dir, "quests.generated.json")))
	l := must(catalog.LoadLoot(filepath.Join(*dir, "loot.generated.json")))
	e := must(inventory.LoadEquipmentCatalog(filepath.Join(*dir, "equipment.generated.json"), q.Source.Checksum))
	if a.Snapshot().Checksum != q.Source.Checksum || l.Source.Checksum != q.Source.Checksum {
		log.Fatal("source mismatch")
	}
	want := map[uint32]bool{}
	for id, d := range q.Quests {
		tags := sections(d.Script.Cells)
		rt := tags["[reward type]"]
		if len(rt) > 0 && rt[0].Text != "[item]" {
			continue
		}
		for _, tag := range []string{"[reward int data]", "[reward selection int data]"} {
			if err := templates(tags[tag], want); err != nil {
				log.Printf("quest %d %s: %v", id, tag, err)
			}
		}
	}
	for id := range l.Items {
		delete(want, id)
	}
	for _, r := range e.Rows {
		delete(want, r.ID)
	}
	types := map[string]int{}
	for _, kind := range []string{"stackable", "equipment"} {
		idx := must(catalog.ResolveScript(a, "list/"+kind+".lst"))
		for _, ref := range must(catalog.ParseIndex(idx.Cells)) {
			if !want[ref.ID] {
				continue
			}
			path := strings.ToLower(strings.ReplaceAll(ref.Path, "\\", "/"))
			if !strings.HasPrefix(path, kind+"/") {
				path = kind + "/" + path
			}
			s := must(catalog.ResolveScript(a, path))
			fields := sections(s.Cells)
			if kind == "equipment" {
				e.Rows = append(e.Rows, inventory.EquipmentDefinition{ID: ref.ID, Path: s.Path, SHA256: s.SHA256, RewardOnly: true, Fields: fields})
			} else {
				t := fields["[stackable type]"]
				if len(t) == 0 || t[0].Text == "" {
					continue
				}
				item := catalog.LootItem{ID: ref.ID, Kind: kind, StackableType: t[0].Text, RewardOnly: true, Script: s}
				if v := fields["[stack limit]"]; len(v) == 1 && v[0].Type == 0 && v[0].Value > 0 {
					item.StackLimit = uint32(v[0].Value)
				}
				l.Items[ref.ID] = item
				types[item.StackableType]++
			}
			delete(want, ref.ID)
		}
	}
	sort.Slice(e.Rows, func(i, j int) bool { return e.Rows[i].ID < e.Rows[j].ID })
	for name, obj := range map[string]any{"equipment.generated.json": e, "loot.generated.json": l} {
		if err := os.WriteFile(filepath.Join(*output, name), must(json.MarshalIndent(obj, "", "  ")), 0600); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("remaining unindexed IDs=%v; added stack types=%v", want, types)
}
