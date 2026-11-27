// pvfvisualdiff checks the visual and combat resource layer of two local inner archives.
// It does not change either archive or start the client.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path"
	"reflect"
	"strings"

	"dfolan/internal/catalog/pvf"
)

type report struct {
	Before            pvf.ArchiveSnapshot `json:"before"`
	After             pvf.ArchiveSnapshot `json:"after"`
	Checked           map[string]int      `json:"checked"`
	RawChange         []string            `json:"raw_changed"`
	DecodedANIChanges []string            `json:"decoded_changed_ani"`
}

func main() {
	before := flag.String("before", "", "original inner archive")
	after := flag.String("after", "", "current inner archive")
	out := flag.String("out", "", "JSON report to create")
	flag.Parse()
	if *before == "" || *after == "" || *out == "" {
		log.Fatal("supply -before, -after and -out")
	}
	load := func(p string) *pvf.Archive {
		a, err := pvf.Open(p)
		if err != nil {
			log.Fatal(err)
		}
		return a
	}
	a, b := load(*before), load(*after)
	if a.FileCount() != b.FileCount() {
		log.Fatal("file count differs")
	}
	result := report{Before: a.Snapshot(), After: b.Snapshot(), Checked: map[string]int{},
		RawChange: []string{}, DecodedANIChanges: []string{}}
	for i := 0; i < a.FileCount(); i++ {
		f := a.FileAt(i)
		name := strings.ToLower(f.ArchivePath)
		ext := path.Ext(name)
		switch ext {
		case ".ani", ".atk", ".act", ".nut", ".img", ".skl", ".chr", ".obj", ".xui":
		default:
			continue
		}
		g := b.FileAt(i)
		if f.ArchivePath != g.ArchivePath || f.DataType != g.DataType {
			log.Fatalf("file path or type differs at %d: %s / %s", i, f.ArchivePath, g.ArchivePath)
		}
		left, err := a.FileRaw(i)
		if err != nil {
			log.Fatal(err)
		}
		right, err := b.FileRaw(i)
		if err != nil {
			log.Fatal(err)
		}
		result.Checked[ext]++
		if !bytes.Equal(left, right) {
			result.RawChange = append(result.RawChange, f.ArchivePath)
		}
		// Check the resolved names and text of the actual player/skill .ani cells
		// as well as their stored bytes; a pool edit can otherwise evade the diff.
		if ext == ".ani" && f.DataType == 1 && (strings.HasPrefix(name, "character/swordman/") ||
			strings.HasPrefix(name, "passiveobject/character/swordman/")) {
			at, err := a.Tokens(f.ArchivePath)
			if err != nil {
				log.Fatal(err)
			}
			bt, err := b.Tokens(g.ArchivePath)
			if err != nil {
				log.Fatal(err)
			}
			result.Checked["swordman_ani_resolved"]++
			if !reflect.DeepEqual(at, bt) {
				result.DecodedANIChanges = append(result.DecodedANIChanges, f.ArchivePath)
			}
		}
	}
	stream, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(stream)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		log.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("checked %v; raw changes %d; decoded character animation changes %d",
		result.Checked, len(result.RawChange), len(result.DecodedANIChanges))
}
