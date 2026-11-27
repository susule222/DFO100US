// Extract only complete native command sections from the exact learning source.
package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"
)

func command(a *pvf.Archive, cells []pvf.Token) string {
	var v []string
	open, closed, seen := false, false, false
	for _, c := range cells {
		if c.Type == 3 {
			switch c.Text {
			case "[command]":
				if seen {
					return ""
				}
				seen = true
				open = true
			case "[/command]":
				if !open {
					return ""
				}
				open = false
				closed = true
			default:
				if open {
					return ""
				}
			}
			continue
		}
		if !open {
			continue
		}
		s := a.ResolveString(int(c.Value))
		if len(v)%2 == 0 {
			if c.Type != 5 {
				return ""
			}
			switch s {
			case "(UP)", "(DOWN)", "(LEFT)", "(RIGHT)", "(SKILL)", "(BUFF)", "(ATTACK)", "(JUMP)":
			default:
				return ""
			}
		} else if c.Type != 7 || (s != "," && s != "&") {
			return ""
		}
		v = append(v, s)
	}
	if !closed || len(v) == 0 || len(v)%2 != 1 {
		return ""
	}
	switch v[len(v)-1] {
	case "(SKILL)", "(BUFF)", "(ATTACK)", "(JUMP)":
	default:
		return ""
	}
	return strings.Join(v, "")
}
func main() {
	src := flag.String("source", "../runtime/pvf_source/Script.inner.pvf", "PVF input")
	cat := flag.String("catalog", "configs/us100/skills.generated.json", "learning catalog")
	out := flag.String("out", "internal/character/native_commands.generated.json", "embedded native command index")
	flag.Parse()
	a, e := pvf.LoadArchive(pvf.Options{Path: *src, MaxBytes: 1 << 30})
	if e != nil {
		log.Fatal(e)
	}
	var c struct {
		Source pvf.ArchiveSnapshot
		Rows   []struct {
			Job          byte
			ID           uint16
			Path, SHA256 string
			Fields       map[string][]pvf.Token
		}
	}
	b, e := os.ReadFile(*cat)
	if e != nil {
		log.Fatal(e)
	}
	if e = json.Unmarshal(b, &c); e != nil {
		log.Fatal(e)
	}
	if c.Source.Checksum != a.Snapshot().Checksum {
		log.Fatal("PVF checksum differs from catalog")
	}
	table := map[string]string{}
	jobs := map[byte]int{}
	active, eligible := 0, 0
	for _, r := range c.Rows {
		jobs[r.Job] += 0
		f := r.Fields["[type]"]
		if len(f) != 1 || f[0].Text != "[active]" {
			continue
		}
		active++
		rec, e := catalog.ReadScript(a, r.Path)
		if e != nil {
			log.Fatal(e)
		}
		if rec.SHA256 != r.SHA256 {
			log.Fatal("skill hash mismatch: ", r.Path)
		}
		if s := command(a, rec.Cells); s != "" {
			table[r.SHA256] = s
			jobs[r.Job]++
			eligible++
		}
	}
	if eligible == 0 {
		log.Fatal("no native commands found")
	}
	b, e = json.MarshalIndent(map[string]any{"source": c.Source.Checksum, "commands": table}, "", " ")
	if e != nil {
		log.Fatal(e)
	}
	if e = os.WriteFile(*out, append(b, '\n'), 0644); e != nil {
		log.Fatal(e)
	}
	log.Printf("rows=%d active=%d eligible=%d unique scripts=%d by job=%v", len(c.Rows), active, eligible, len(table), jobs)
}
