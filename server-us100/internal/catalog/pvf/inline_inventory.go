package pvf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
)

type inlineText struct {
	Field  string `json:"field"`
	Source string `json:"source"`
	File   string `json:"example_path"`
	Count  int    `json:"occurrences"`
}

type inlineKey struct {
	field  string
	offset int
}

func (a *Archive) ExportInlineDisplayInventory(output string) error {
	groups := make([][]int, len(a.groups))
	fields := map[string]bool{
		"[name]": true, "[basic explain]": true, "[detail explain]": true, "[explain]": true,
		"[description]": true, "[desc]": true, "[flavor text]": true, "[title]": true, "[name2]": true,
		"[depend message]": true, "[condition message]": true, "[solve message]": true,
		"[text]": true, "[message]": true, "[guide text]": true, "[notice]": true,
		"[tooltip]": true, "[dialog]": true, "[speech]": true, "[subtitle]": true,
	}
	for i, it := range a.items {
		if it.dataType != 1 || it.dataSize < 10 || it.dataSize%5 != 0 {
			continue
		}
		if it.chunkIndex < 0 || it.chunkIndex >= len(groups) {
			return fmt.Errorf("invalid group for entry %d", i)
		}
		groups[it.chunkIndex] = append(groups[it.chunkIndex], i)
	}
	tags := map[int]string{}
	values := map[inlineKey]inlineText{}
	scanned := 0
	fieldCounts := map[string]int{}
	extensionCounts := map[string]int{}
	extensions := map[int]string{}
	for group, ids := range groups {
		if len(ids) == 0 {
			continue
		}
		raw, err := a.chunk(group)
		if err != nil {
			return err
		}
		for _, i := range ids {
			it := a.items[i]
			end := it.dataOffset + it.dataSize
			if it.dataOffset < 0 || end > len(raw) {
				return fmt.Errorf("out of bounds item %d", i)
			}
			scanned++
			for pos := it.dataOffset; pos+10 <= end; pos += 5 {
				if raw[pos] != 3 || raw[pos+5] != 6 {
					continue
				}
				tag := readInt32(raw[pos+1 : pos+5])
				field, found := tags[tag]
				if !found {
					field = a.resolveString(tag)
					if !fields[field] {
						field = ""
					}
					tags[tag] = field
				}
				if field == "" {
					continue
				}
				idx := readInt32(raw[pos+6 : pos+10])
				key := inlineKey{field, idx}
				record := values[key]
				if record.Count == 0 {
					record.Field = field
					record.Source = a.usInlineString(idx)
					record.File = a.fileAt(i).ArchivePath
				}
				record.Count++
				values[key] = record
				fieldCounts[field]++
				suffix, present := extensions[it.nameOffset]
				if !present {
					suffix = path.Ext(a.resolveString(it.nameOffset))
					extensions[it.nameOffset] = suffix
				}
				extensionCounts[suffix]++
			}
		}
	}
	out := make([]inlineText, 0, len(values))
	for _, row := range values {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].File < out[j].File
	})
	encoded, err := json.Marshal(map[string]any{
		"scripts_scanned": scanned, "fields": fieldCounts, "extensions": extensionCounts, "values": out,
	})
	if err != nil {
		return err
	}
	return writeExclusive(output, encoded)
}

func (a *Archive) usInlineString(offset int) string {
	if offset < 0 || offset&1 != 0 {
		return a.resolveString(offset)
	}
	start := offset >> 1
	if start >= len(a.strA) {
		return ""
	}
	raw := a.strA[start:]
	n := bytes.IndexByte(raw, 0)
	if n < 0 {
		return ""
	}
	raw = raw[:n]
	if utf8.Valid(raw) {
		return string(raw)
	}
	data, err := korean.EUCKR.NewDecoder().Bytes(raw)
	if err != nil {
		return ""
	}
	return string(data)
}
