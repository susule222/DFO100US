// ateliericons exports inventory icon fields without rebuilding or changing the gateway.
package main

import (
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	source := flag.String("source", "../runtime/pvf_source/Script.inner.pvf", "read-only source archive")
	catalog := flag.String("catalog", "configs/us100/equipment.generated.json", "equipment catalog")
	out := flag.String("out", "../tools/atelier/equipment_icons.json", "launcher icon index")
	flag.Parse()
	b, err := os.ReadFile(*catalog)
	if err != nil {
		log.Fatal(err)
	}
	var c struct {
		Source pvf.ArchiveSnapshot
		Rows   []struct {
			Path   string
			Fields map[string][]pvf.Token
		}
	}
	if err = json.Unmarshal(b, &c); err != nil {
		log.Fatal(err)
	}
	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		log.Fatal(err)
	}
	if a.Snapshot().Checksum != c.Source.Checksum {
		log.Fatal("source archive differs from catalog")
	}
	type ref struct {
		Image string `json:"img"`
		Frame int32  `json:"frame"`
	}
	refs := make(map[string]ref)
	for _, row := range c.Rows {
		if len(row.Fields["[icon]"]) > 0 {
			continue
		}
		ts, err := a.Tokens(row.Path)
		if err != nil {
			log.Fatalf("%s: %s", row.Path, err)
		}
		for i, t := range ts {
			if t.Type == 3 && t.Text == "[icon]" && i+2 < len(ts) {
				file, frame := ts[i+1], ts[i+2]
				if file.Type == 6 && strings.HasSuffix(strings.ToLower(file.Text), ".img") && frame.Type == 0 && frame.Value >= 0 {
					refs[row.Path] = ref{file.Text, frame.Value}
				}
				break
			}
		}
	}
	b, err = json.Marshal(map[string]any{"source": c.Source.Checksum, "rows": refs})
	if err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(*out+".tmp", b, 0644); err != nil {
		log.Fatal(err)
	}
	if err = os.Rename(*out+".tmp", *out); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("indexed %d omitted equipment icon fields\n", len(refs))
}
