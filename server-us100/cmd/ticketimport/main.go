package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"flag"
	"log"
	"os"
)

func main() {
	source := flag.String("source", "", "read-only US100 inner PVF")
	output := flag.String("output", "configs/us100/tickets.generated.json", "generated upgrade ticket catalog")
	flag.Parse()
	if *source == "" {
		log.Fatal("-source is required")
	}
	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		log.Fatal(err)
	}
	c, err := catalog.ImportTickets(a)
	if err != nil {
		log.Fatal(err)
	}
	if err := catalog.SaveTickets(*output, c); err != nil {
		log.Fatal(err)
	}
	info, _ := os.Stat(*output)
	var size int64
	if info != nil {
		size = info.Size()
	}
	log.Printf("tickets source=%s total=%d size=%d bytes kinds=%v amplify_consts=%d",
		c.Source.Checksum, len(c.Tickets), size, c.KindCounts, len(c.AmplifyConsts))
}
