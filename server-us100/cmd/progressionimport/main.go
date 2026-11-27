package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"log"
	"os"
)

func main() {
	source := flag.String("source", "runtime/pvf_source/Script.inner.pvf", "read-only current source")
	output := flag.String("output", "configs/progression.next25.json", "generated progression catalog")
	maxLevel := flag.Int("max-level", 0, "keep only the first N cumulative thresholds (0 = whole source table)")
	flag.Parse()
	a, e := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if e != nil {
		log.Fatal(e)
	}
	c, e := catalog.ImportProgressionUpTo(a, *maxLevel)
	if e != nil {
		log.Fatal(e)
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		log.Fatal(e)
	}
	if e = os.WriteFile(*output, b, 0600); e != nil {
		log.Fatal(e)
	}
	log.Printf("progression thresholds=%d max_level=%d notes=%d SP=%d monster_exp=%d source=%s",
		len(c.Thresholds), c.MaxLevel, len(c.SourceNotes), len(c.SkillPoints), len(c.MonsterExperience), c.Source.Checksum)
	for _, note := range c.SourceNotes {
		log.Printf("source note: %s", note)
	}
}
