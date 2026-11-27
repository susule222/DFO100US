// equipmentbuild projects the source equipment list into a base catalog,
// keeping exactly the pieces the reward and drop rules accept: one attach-type
// cell, a readable rarity, a known equipment kind and a usable durability.
// The previous catalogs were built for the 2.38 archive and carry that
// archive's checksum; the loader refuses them against a different source, so
// each archive needs its own projection. Read-only against the archive.
package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/inventory"
	"encoding/json"
	"flag"
	"log"
	"os"
	"runtime/debug"
	"strings"
)

// keptFields are the tags the bag, reward and drop paths read. Unknown cells
// are left out of the projection, but every listed tag keeps its own cells.
var keptFields = map[string]bool{
	"[attach type]":                true,
	"[rarity]":                     true,
	"[equipment type]":             true,
	"[durability]":                 true,
	"[grade]":                      true,
	"[name]":                       true,
	"[minimum level]":              true,
	"[maximum level]":              true,
	"[physical attack]":            true,
	"[physical defense]":           true,
	"[magical attack]":             true,
	"[magical defense]":            true,
	"[equipment physical attack]":  true,
	"[equipment physical defense]": true,
	"[equipment magical attack]":   true,
	"[equipment magical defense]":  true,
	"[skill bonus]":                true,
	"[item price]":                 true,
	"[need material]":              true,
	"[usable job]":                 true,
	"[grow type]":                  true,
	"[part set index]":             true,
	"[set name]":                   true,
	"[anti evil]":                  true,
	"[customize type]":             true,
}

type row struct {
	ID     uint32
	Path   string
	SHA256 string
	Fields map[string][]pvf.Token
}

func main() {
	source := flag.String("source", "runtime/pvf_source/Script.inner.pvf", "read-only source archive")
	out := flag.String("output", "configs/equipment.generated.json", "projected catalog")
	maxRarity := flag.Int("max-rarity", 1, "highest rarity kept as pool gear")
	flag.Parse()
	// This importer allocates the archive's tokens once per template and never
	// reuses them. The default GC target lets the heap triple before a cycle;
	// a low target trades CPU for a bounded working set, which is the whole
	// point of the memory cap.
	debug.SetGCPercent(10)

	a, e := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if e != nil {
		log.Fatal(e)
	}
	idx, e := catalog.ResolveScript(a, "list/equipment.lst")
	if e != nil {
		log.Fatal(e)
	}
	refs, e := catalog.ParseIndex(idx.Cells)
	if e != nil {
		log.Fatal(e)
	}
	log.Printf("source list carries %d templates", len(refs))

	c := inventory.EquipmentCatalog{Source: a.Snapshot()}
	skipped := map[string]int{}
	for _, ref := range refs {
		p := ref.Path
		if !strings.HasPrefix(p, "equipment/") {
			p = "equipment/" + p
		}
		s, err := catalog.ResolveScript(a, p)
		if err != nil {
			skipped["unreadable"]++
			continue
		}
		fields := map[string][]pvf.Token{}
		tag := ""
		seen := map[string]bool{}
		enabled := false
		for _, t := range s.Cells {
			if t.Type == 3 {
				tag = t.Text
				// Repeated markers restart their own block; a later duplicate
				// does not reopen a block already read.
				enabled = keptFields[tag] && !seen[tag]
				seen[tag] = true
				continue
			}
			if enabled {
				fields[tag] = append(fields[tag], t)
			}
		}
		kind, ok := accept(fields, *maxRarity)
		if !ok {
			skipped[kind]++
			continue
		}
		c.Rows = append(c.Rows, inventory.EquipmentDefinition{
			ID: ref.ID, Path: s.Path, SHA256: s.SHA256, Fields: fields,
		})
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		log.Fatal(e)
	}
	if e = os.WriteFile(*out, b, 0600); e != nil {
		log.Fatal(e)
	}
	log.Printf("wrote %s: %d rows; skipped %v", *out, len(c.Rows), skipped)
}

// accept mirrors inventory.EquipmentCatalog.Reward, plus the drop-pool rule
// when maxRarity limits the projection.
func accept(fields map[string][]pvf.Token, maxRarity int) (string, bool) {
	attach, rarity, kind := fields["[attach type]"], fields["[rarity]"], fields["[equipment type]"]
	if len(attach) != 1 {
		return "attach type missing or repeated", false
	}
	if attach[0].Text != "[free]" {
		return "bound gear", false
	}
	if len(rarity) != 1 || rarity[0].Type != 0 || rarity[0].Value < 0 {
		return "unreadable rarity", false
	}
	if int(rarity[0].Value) > maxRarity {
		return "rarity above pool limit", false
	}
	if len(kind) == 0 {
		return "unknown equipment kind", false
	}
	d := fields["[durability]"]
	if len(d) == 0 {
		if kind[0].Text == "[amulet]" || kind[0].Text == "[wrist]" || kind[0].Text == "[ring]" {
			return "", true
		}
		return "missing durability", false
	}
	if len(d) != 1 || d[0].Type != 0 || d[0].Value < 0 || d[0].Value > 65535 {
		return "invalid durability", false
	}
	return "", true
}
