package pvf

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

type typeKey struct {
	typ int
	ext string
}

type typeRow struct {
	Type      int      `json:"data_type"`
	Extension string   `json:"extension"`
	Files     int      `json:"files"`
	Bytes     int64    `json:"bytes"`
	Samples   []string `json:"sample_paths"`
}

func (a *Archive) ExportTypeInventory(output string) error {
	groups := make(map[typeKey]*typeRow)
	ext := make(map[int]string)
	for i, item := range a.items {
		suffix, exists := ext[item.nameOffset]
		if !exists {
			suffix = strings.ToLower(path.Ext(a.resolveString(item.nameOffset)))
			ext[item.nameOffset] = suffix
		}
		key := typeKey{item.dataType, suffix}
		row := groups[key]
		if row == nil {
			row = &typeRow{Type: key.typ, Extension: key.ext}
			groups[key] = row
		}
		row.Files++
		row.Bytes += int64(item.dataSize)
		if len(row.Samples) < 5 && item.dataSize > 0 {
			row.Samples = append(row.Samples, a.fileAt(i).ArchivePath)
		}
	}
	rows := make([]*typeRow, 0, len(groups))
	for _, row := range groups {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type != rows[j].Type {
			return rows[i].Type < rows[j].Type
		}
		if rows[i].Files != rows[j].Files {
			return rows[i].Files > rows[j].Files
		}
		return rows[i].Extension < rows[j].Extension
	})
	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return writeExclusive(output, encoded)
}
