package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"dfolan/internal/dungeon"
	"encoding/json"
	"flag"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	source := flag.String("source", "", "original PVF (read-only)")
	ids := flag.String("ids", "", "source dungeon IDs to import")
	refresh := flag.String("refresh", "", "existing dungeon catalog to refresh missing maps and validated routes")
	dest := flag.String("output", "configs/dungeons.generated.json", "output catalog")
	flag.Parse()
	a, e := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if e != nil {
		log.Fatal(e)
	}
	if *refresh != "" {
		c, e := catalog.LoadDungeons(*refresh)
		if e != nil {
			log.Fatal(e)
		}
		if a.Snapshot().Checksum != c.Source.Checksum {
			log.Fatalf("source checksum mismatch: archive=%s catalog=%s", a.Snapshot().Checksum, c.Source.Checksum)
		}
		idx, e := catalog.ReadScript(a, "list/map.lst")
		if e != nil {
			log.Fatal(e)
		}
		rows, e := catalog.ParseIndex(idx.Cells)
		if e != nil {
			log.Fatal(e)
		}
		mapPaths := map[uint32]string{}
		for _, r := range rows {
			mapPaths[r.ID] = r.Path
		}
		// Re-parse every dungeon from its stored script without LoadDungeons'
		// Expanded filter so newly supported layered/multi-candidate routes can
		// be imported and verified via CheckEntryRoute.
		dIDs := make([]uint32, 0, len(c.Dungeons))
		for id := range c.Dungeons {
			dIDs = append(dIDs, id)
		}
		sort.Slice(dIDs, func(i, j int) bool { return dIDs[i] < dIDs[j] })
		addedMaps := 0
		addedRoutes := 0
		for _, id := range dIDs {
			d := c.Dungeons[id]
			parsed, err := catalog.ParseDungeon(id, d.Script)
			if err != nil {
				log.Fatalf("reparse dungeon %d: %v", id, err)
			}
			for _, m := range parsed.Mazes {
				if len(m.Pending) > 0 {
					continue
				}
				for _, r := range m.Rooms {
					if _, ok := c.Maps[r.Map]; ok {
						continue
					}
					p := mapPaths[r.Map]
					if p == "" {
						continue
					}
					rec, err := catalog.ResolveScript(a, p)
					if err != nil {
						continue
					}
					c.Maps[r.Map] = rec
					addedMaps++
				}
			}
			c.Dungeons[id] = parsed
			if oldRoutes, expanded := c.Expanded[id]; expanded {
				oldSet := map[byte]bool{}
				for _, n := range oldRoutes {
					oldSet[n] = true
				}
				var valid []byte
				for _, m := range parsed.Mazes {
					if err := dungeon.CheckEntryRoute(c, parsed, m); err == nil {
						valid = append(valid, m.Index)
						if !oldSet[m.Index] {
							addedRoutes++
						}
					}
				}
				if len(valid) > 0 {
					c.Expanded[id] = valid
				}
			}
		}
		b, e := json.Marshal(c)
		if e != nil {
			log.Fatal(e)
		}
		if e = os.WriteFile(*dest, b, 0600); e != nil {
			log.Fatal(e)
		}
		log.Printf("refreshed source=%s dungeons=%d maps=%d (+%d maps, +%d expanded routes)", c.Source.Checksum, len(c.Dungeons), len(c.Maps), addedMaps, addedRoutes)
		return
	}
	var list []uint32
	for _, s := range strings.Split(*ids, ",") {
		v, e := strconv.ParseUint(s, 10, 32)
		if e != nil {
			log.Fatal(e)
		}
		list = append(list, uint32(v))
	}
	c, e := catalog.ImportDungeons(a, list)
	if e != nil {
		log.Fatal(e)
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		log.Fatal(e)
	}
	if e = os.WriteFile(*dest, b, 0600); e != nil {
		log.Fatal(e)
	}
	log.Printf("source=%s dungeons=%d maps=%d", c.Source.Checksum, len(c.Dungeons), len(c.Maps))
}
