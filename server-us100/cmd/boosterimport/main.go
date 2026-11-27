package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"flag"
	"log"
	"os"
)

func main() {
	source := flag.String("source", "../runtime/atelier-data/pvf-cache/13e3238b276ed223ae1d.inner.pvf", "read-only US100 PVF")
	output := flag.String("output", "configs/us100/boosters.generated.json.gz", "generated gzipped booster catalog")
	flag.Parse()

	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		log.Fatal(err)
	}
	c, err := catalog.ImportBoosters(a)
	if err != nil {
		log.Fatal(err)
	}
	if err := catalog.SaveBoosters(*output, c); err != nil {
		log.Fatal(err)
	}
	info, _ := os.Stat(*output)
	var size int64
	if info != nil {
		size = info.Size()
	}
	log.Printf("boosters source=%s total=%d size=%d bytes tag_stats=%v type_counts=%v",
		c.Source.Checksum, len(c.Boxes), size, c.BoxTagStats, c.TypeCounts)
}
