// Compile only independently checkable solo routes. Report every worldmap ID,
// including missing entries and special systems, without substituting a map.
package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/dungeon"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

type result struct {
	Path   string
	Status string
	Routes map[int]string `json:",omitempty"`
}

// A new dungeon with an unknown source section gets audited, not opened.
// Do not accept admission tickets, party/raid modes or time-limited events
// just because a compatible first room can be constructed for them.
const ordinary = `background pos|cutscene image|minimap image|worldmap pattern info|minimum required level|basis level|champion|pathgate object|story mode|difficulty size|increase exp rate|warlike|all|normal|named|boss|sight|stuck|max_hp|attackspeed|movespeed|activestatustoler|attack|defense|hitrecovery|quest list|maze info|quest connection|size|greed|map specification|start map|boss map|escape hell|special passive object item|name|explain|designate dungeon difficulty|recommend equipment|recommend party|monsterapc diff table|event monster random map|event monster|difficulty level|experience increasing point`

func check(e error) {
	if e != nil {
		log.Fatal(e)
	}
}
func write(name string, v any) {
	f, e := os.Create(name)
	check(e)
	check(json.NewEncoder(f).Encode(v))
	check(f.Close())
}
func main() {
	source := flag.String("source", "", "matching read-only source PVF")
	base := flag.String("base", "", "existing dungeon catalog")
	world := flag.String("world", "", "world catalog")
	out := flag.String("out", "", "new candidate, never overwrite the base")
	audit := flag.String("audit", "", "per-ID audit JSON")
	flag.Parse()
	if *out == "" || *audit == "" || *base == *out {
		log.Fatal("separate base, out and audit required")
	}
	c, e := catalog.LoadDungeons(*base)
	check(e)
	if c.Gates != nil || c.Expanded != nil {
		log.Fatal("use the original base catalog, not a previously expanded candidate")
	}
	w, e := catalog.LoadWorld(*world)
	check(e)
	a, e := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1 << 30})
	check(e)
	if a.Snapshot().Checksum != c.Source.Checksum || w.Source.Checksum != c.Source.Checksum {
		log.Fatal("different source revisions")
	}
	idx, e := catalog.ReadScript(a, "list/map.lst")
	check(e)
	rows, e := catalog.ParseIndex(idx.Cells)
	check(e)
	maps := map[uint32]string{}
	for _, r := range rows {
		maps[r.ID] = r.Path
	}
	paths := map[uint32]string{}
	for _, r := range w.Dungeons {
		paths[r.ID] = r.Path
	}
	gates, pendingGates, e := catalog.SourceGateNodes(a)
	check(e)
	c.Gates = gates
	keys := map[uint32]bool{}
	for _, v := range gates {
		for _, n := range v {
			keys[n.Dungeon] = true
		}
	}
	ids := make([]uint32, 0, len(keys))
	for k := range keys {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	const soloPresentation = "named monster|named monster map pos|cutscene random image|show boss hp percent gauge|adjust hp gauge|preload passive|recommended level|dungeon minimap off|boss mark disable|hidden secret shop|prohibit practice|no campfire|no fatigue"
	allowed := map[string]bool{}
	for _, s := range strings.Split(ordinary+"|"+soloPresentation, "|") {
		allowed[s] = true
	}
	report := map[uint32]*result{}
	tags := map[string]int{}
	c.Expanded = map[uint32][]byte{}
	added := 0
	for _, id := range ids {
		r := &result{Path: paths[id]}
		report[id] = r
		_, old := c.Dungeons[id]
		if r.Path == "" {
			r.Status = "not in dungeon.lst"
			continue
		}
		s, e := catalog.ResolveScript(a, r.Path)
		if e != nil {
			r.Status = e.Error()
			continue
		}
		d, e := catalog.ParseDungeon(id, s)
		if e != nil {
			r.Status = e.Error()
			continue
		}
		unknown := map[string]bool{}
		variant := -1
		specialClear := map[int]bool{}
		inClear := false
		for _, v := range s.Cells {
			if v.Type != 3 {
				continue
			}
			switch v.Text {
			case "[maze info]":
				variant++
			case "[clear condition]":
				if variant >= 0 {
					inClear = true
					specialClear[variant] = true
					continue
				}
			case "[/clear condition]":
				if inClear {
					inClear = false
					continue
				}
			}
			if inClear {
				continue
			}
			k := strings.ToLower(strings.Trim(v.Text, "[]/"))
			if !allowed[k] {
				unknown[k] = true
			}
		}
		if inClear {
			unknown["unterminated clear condition"] = true
		}
		for k := range unknown {
			tags[k]++
		}
		if !old && (d.Tutorial || len(unknown) > 0) {
			ks := []string{}
			for k := range unknown {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			r.Status = "special or unclassified source sections: " + strings.Join(ks, ", ")
			continue
		}
		r.Routes = map[int]string{}
		var valid []byte
		for _, m := range d.Mazes {
			if !old && specialClear[int(m.Index)] {
				r.Routes[int(m.Index)] = "custom clear condition for this maze"
				continue
			}
			if len(m.Pending) == 0 {
				for _, room := range m.Rooms {
					if _, ok := c.Maps[room.Map]; !ok && maps[room.Map] != "" {
						rec, err := catalog.ResolveScript(a, maps[room.Map])
						if err == nil {
							c.Maps[room.Map] = rec
						}
					}
				}
			}
			if e := dungeon.CheckEntryRoute(c, d, m); e != nil {
				r.Routes[int(m.Index)] = e.Error()
				continue
			}
			r.Routes[int(m.Index)] = fmt.Sprintf("room packets verified; quest=%d", m.Quest)
			valid = append(valid, m.Index)
		}
		switch {
		case old:
			r.Status = "base catalog"
		case len(valid) > 0:
			c.Dungeons[id] = d
			c.Expanded[id] = valid
			r.Status = "added"
			added++
		default:
			r.Status = "no verified route"
		}
	}
	keep := map[uint32]bool{}
	for _, d := range c.Dungeons {
		for _, m := range d.Mazes {
			for _, r := range m.Rooms {
				keep[r.Map] = true
			}
		}
	}
	for k := range c.Maps {
		if !keep[k] {
			delete(c.Maps, k)
		}
	}
	outside := map[uint32]string{}
	for id, path := range paths {
		if !keys[id] {
			outside[id] = path
		}
	}
	write(*audit, map[string]any{"source": c.Source.Checksum, "dungeon_index": len(paths), "not_on_parsed_primary_page": outside, "worldmap_ids": len(ids), "parsed_gates": len(gates), "unresolved_gate_files": pendingGates, "added": added, "by_id": report, "unclassified_sections": tags})
	write(*out, c)
	log.Printf("audited=%d added=%d catalog=%d maps=%d", len(ids), added, len(c.Dungeons), len(c.Maps))
}
