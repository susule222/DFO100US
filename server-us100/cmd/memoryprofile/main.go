// memoryprofile loads the US100 archive and runs a full equipment projection,
// then writes a heap profile for offline analysis.
package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
)

func main() {
	source := flag.String("source", "", "archive")
	out := flag.String("profile", "", "heap profile output")
	maxRarity := flag.Int("max-rarity", 1, "drop pool rarity cap")
	flag.Parse()
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
	var rows int
	for _, ref := range refs {
		p := ref.Path
		if !strings.HasPrefix(p, "equipment/") {
			p = "equipment/" + p
		}
		s, err := catalog.ResolveScript(a, p)
		if err != nil {
			continue
		}
		// Exercise the same acceptance reads equipmentbuild makes.
		fields := map[string][]pvf.Token{}
		tag := ""
		seen := map[string]bool{}
		enabled := false
		for _, t := range s.Cells {
			if t.Type == 3 {
				tag = t.Text
				enabled = !seen[tag]
				seen[tag] = true
				continue
			}
			if enabled {
				fields[tag] = append(fields[tag], t)
			}
		}
		if len(fields["[rarity]"]) == 1 && int(fields["[rarity]"][0].Value) <= *maxRarity {
			rows++
		}
	}
	runtime.GC()
	if *out != "" {
		f, e := os.Create(*out)
		if e != nil {
			log.Fatal(e)
		}
		defer f.Close()
		if e = pprof.WriteHeapProfile(f); e != nil {
			log.Fatal(e)
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Printf("rows=%d heap_alloc=%.0fMB heap_inuse=%.0fMB sys=%.0fMB numgc=%d",
		rows, float64(m.HeapAlloc)/1048576, float64(m.HeapInuse)/1048576, float64(m.Sys)/1048576, m.NumGC)
	fmt.Printf("rows=%d\n", rows)
}
