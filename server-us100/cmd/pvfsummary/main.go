// pvfsummary aggregates a PVF archive's top-level directories into a summary.
package main

import (
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type agg struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func main() {
	source := flag.String("source", "", "source PVF, opened read-only")
	out := flag.String("output", "runtime/pvf", "output directory")
	flag.Parse()
	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(*out, 0700); err != nil {
		log.Fatal(err)
	}
	m := map[string]*agg{}
	for i := 0; i < a.FileCount(); i++ {
		f := a.FileAt(i)
		top := "(root)"
		if idx := strings.Index(f.ArchivePath, "/"); idx >= 0 {
			top = f.ArchivePath[:idx]
		}
		e := m[top]
		if e == nil {
			e = &agg{}
			m[top] = e
		}
		e.Files++
		e.Bytes += int64(f.Size)
	}
	type row struct {
		Dir string `json:"dir"`
		agg
	}
	rows := make([]row, 0, len(m))
	for k, v := range m {
		rows = append(rows, row{k, *v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Files != rows[j].Files {
			return rows[i].Files > rows[j].Files
		}
		return rows[i].Dir < rows[j].Dir
	})
	b, _ := json.MarshalIndent(rows, "", "  ")
	if err = os.WriteFile(filepath.Join(*out, "summary.json"), b, 0600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("archive files=%d top-level dirs=%d\n", a.FileCount(), len(rows))
	for i, r := range rows {
		if i >= 40 {
			fmt.Printf("... (%d more, see summary.json)\n", len(rows)-40)
			break
		}
		fmt.Printf("%8d files %14d bytes  %s\n", r.Files, r.Bytes, r.Dir)
	}
}
