// avatartextaudit extracts localization evidence for a previously indexed
// list of .equ entries. It opens the source PVF read-only.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"dfolan/internal/catalog/pvf"
)

type entry struct {
	Path string `json:"archive_path"`
}

var language = regexp.MustCompile(`[A-Za-z]{2,}|[\x{3041}-\x{30fa}\x{ac00}-\x{d7a3}]`)
var displayFields = map[string]bool{
	"[name]": true, "[set name]": true, "[description]": true, "[desc]": true,
	"[explain]": true, "[basic explain]": true, "[detail explain]": true,
	"[flavor]": true, "[flavor text]": true, "[tooltip]": true,
}

func main() {
	src := flag.String("source", "", "read-only inner PVF")
	index := flag.String("index", "", "JSON path inventory")
	out := flag.String("out", "", "new TSV file")
	flag.Parse()
	if *src == "" || *index == "" || *out == "" {
		log.Fatal("source, index, and out are required")
	}
	raw, err := os.ReadFile(*index)
	if err != nil {
		log.Fatal(err)
	}
	var files []entry
	if err := json.Unmarshal(raw, &files); err != nil {
		log.Fatal(err)
	}
	a, err := pvf.LoadArchive(pvf.Options{Path: *src, MaxBytes: 1 << 30})
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	write := func(s string) {
		if _, err := w.WriteString(s); err != nil {
			log.Fatal(err)
		}
	}
	write("path\tfield\tencoding\tvalue_json\n")
	nref, ninl := 0, 0
	scriptHash := sha256.New()
	for _, item := range files {
		if !strings.HasSuffix(item.Path, ".equ") || !strings.Contains(item.Path, "/avatar/") {
			log.Fatalf("non-avatar path: %q", item.Path)
		}
		toks, err := a.Tokens(item.Path)
		if err != nil {
			log.Fatalf("%s: %s", item.Path, err)
		}
		var field string
		var cell [5]byte
		scriptHash.Write([]byte(item.Path))
		scriptHash.Write([]byte{0})
		binary.LittleEndian.PutUint32(cell[:4], uint32(len(toks)))
		scriptHash.Write(cell[:4])
		for _, tok := range toks {
			cell[0] = tok.Type
			binary.LittleEndian.PutUint32(cell[1:], uint32(tok.Value))
			scriptHash.Write(cell[:])
			if tok.Type == 3 {
				field = strings.ToLower(tok.Text)
				continue
			}
			if !displayFields[field] {
				continue
			}
			value, kind := "", ""
			if tok.Type == 8 {
				value, kind = tok.Reference, "ref"
				nref++
			} else if tok.Type == 6 && language.MatchString(tok.Text) {
				value, kind = tok.Text, "inline"
				ninl++
			} else {
				continue
			}
			b, err := json.Marshal(value)
			if err != nil {
				log.Fatal(err)
			}
			write(item.Path + "\t" + field + "\t" + kind + "\t" + string(b) + "\n")
		}
	}
	if err := w.Flush(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("indexed %d avatar equipment scripts; display reference occurrences=%d; locale-like inline occurrences=%d\n",
		len(files), nref, ninl)
	fmt.Printf("path_order_and_raw_script_cells_sha256=%x\n", scriptHash.Sum(nil))
}
