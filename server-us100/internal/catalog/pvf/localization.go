package pvf

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func (a *Archive) ExportLocalizationReferences(output string) error {
	groups := make([][]int, len(a.groups))
	for i, it := range a.items {
		if it.dataType == 1 {
			if it.chunkIndex < 0 || it.chunkIndex >= len(groups) {
				return fmt.Errorf("invalid group for entry %d", i)
			}
			groups[it.chunkIndex] = append(groups[it.chunkIndex], i)
		}
	}
	offsets := make(map[int]int)
	var scripts, partial int
	for g, ids := range groups {
		if len(ids) == 0 {
			continue
		}
		raw, err := a.chunk(g)
		if err != nil {
			return err
		}
		for _, i := range ids {
			it := a.items[i]
			end := it.dataOffset + it.dataSize
			if it.dataOffset < 0 || it.dataSize < 0 || end > len(raw) {
				return fmt.Errorf("out of bounds entry %d", i)
			}
			scripts++
			if it.dataSize%5 != 0 {
				partial++
				continue
			}
			for pos := it.dataOffset; pos < end; pos += 5 {
				if raw[pos] == 8 {
					offsets[readInt32(raw[pos+1:pos+5])]++
				}
			}
		}
	}
	refs := make(map[string]int, len(offsets))
	for index, count := range offsets {
		refs[a.resolveString(index)] += count
	}
	data, err := json.Marshal(map[string]any{
		"scripts_scanned": scripts, "nonconforming_scripts_skipped": partial,
		"references": refs,
	})
	if err != nil {
		return err
	}
	return writeExclusive(output, data)
}

func localizationTable(path string) bool {
	return (strings.HasSuffix(path, ".str") || strings.HasSuffix(path, ".xui")) &&
		filepath.IsLocal(filepath.FromSlash(path))
}

func (a *Archive) ExportLocalization(dir string) error {
	return a.exportLocalizationSuffix(dir, map[string]bool{".str": true})
}

func (a *Archive) ExportXUI(dir string) error {
	return a.exportLocalizationSuffix(dir, map[string]bool{".xui": true})
}

func (a *Archive) ExportAuxiliaryText(dir string) error {
	return a.exportLocalizationSuffix(dir, map[string]bool{".lua": true, ".cos": true, ".json": true})
}

func (a *Archive) exportLocalizationSuffix(dir string, suffixes map[string]bool) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	for i, item := range a.items {
		if item.dataType != 3 || !suffixes[strings.ToLower(path.Ext(a.resolveString(item.nameOffset)))] {
			continue
		}
		path := strings.ToLower(a.fileAt(i).ArchivePath)
		if !filepath.IsLocal(filepath.FromSlash(path)) {
			return fmt.Errorf("unsafe table path %q", path)
		}
		data, err := a.readRawIndex(i)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		if err := writeExclusive(dest, data); err != nil {
			return err
		}
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Close()
}

// WriteLocalizationCopy preserves the file IDs, native hash table, string pools,
// and every non-localization member. Only affected groups are recompressed.
func (a *Archive) WriteLocalizationCopy(dir, output string) error {
	return a.WriteLocalizationCopyWithInline(dir, "", output)
}

// WriteLocalizationCopyWithInline additionally replaces vetted display-name cells.
func (a *Archive) WriteLocalizationCopyWithInline(dir, inlinePlan, output string) error {
	if a.format != FormatDFO20260901 {
		return fmt.Errorf("unsupported localization output format %s", a.format)
	}
	replacements := map[int][]byte{}
	byGroup := map[int][]int{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		i, ok := a.findIndexExact(rel)
		if !d.Type().IsRegular() || !localizationTable(rel) || !ok || a.items[i].dataType != 3 {
			return fmt.Errorf("not an existing localization table: %s", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(raw) == 0 || len(raw)%2 != 0 || len(raw) > 128<<20 || replacements[i] != nil {
			return fmt.Errorf("invalid replacement: %s", rel)
		}
		replacements[i] = raw
		byGroup[a.items[i].chunkIndex] = nil
		return nil
	})
	if err != nil {
		return err
	}
	wide := a.strW
	nameStart := a.bodyOff - len(a.groups)*groupItemSize - a.header.nameSize
	oldGroupOffset := a.bodyOff - len(a.groups)*groupItemSize
	names := a.data[nameStart:oldGroupOffset]
	if inlinePlan != "" {
		wide, err = a.stageInlineLocalization(inlinePlan, replacements, byGroup)
		if err != nil {
			return err
		}
		names, err = a.replaceWideLocalizationPool(names, wide)
		if err != nil {
			return err
		}
	}
	if len(replacements) == 0 {
		return fmt.Errorf("no replacements")
	}
	for i, it := range a.items {
		if ids, ok := byGroup[it.chunkIndex]; ok {
			byGroup[it.chunkIndex] = append(ids, i)
		}
	}
	groupOffset := nameStart + len(names)
	prefix := make([]byte, groupOffset+len(a.groups)*groupItemSize)
	copy(prefix, a.data[:nameStart])
	copy(prefix[nameStart:], names)
	groups := make([]byte, len(a.groups)*groupItemSize)
	encoded := map[int][]byte{}
	previous, total := 0, 0
	for g, item := range a.groups {
		compSize, originalSize := item.compressedSize-previous, item.originalSize
		if ids, ok := byGroup[g]; ok {
			old, err := a.chunk(g)
			if err != nil {
				return err
			}
			sort.Slice(ids, func(i, j int) bool {
				x, y := a.items[ids[i]], a.items[ids[j]]
				if x.dataOffset != y.dataOffset {
					return x.dataOffset < y.dataOffset
				}
				return x.dataSize < y.dataSize
			})
			updated, err := a.spliceLocalization(old, prefix, ids, replacements)
			if err != nil {
				return fmt.Errorf("group %d: %w", g, err)
			}
			originalSize = len(updated)
			var dst bytes.Buffer
			z, err := zlib.NewWriterLevel(&dst, zlib.BestCompression)
			if err != nil {
				return err
			}
			if _, err := z.Write(updated); err != nil {
				return err
			}
			if err := z.Close(); err != nil {
				return err
			}
			// Verify the finished stream before committing it to the candidate.
			decoded, err := zlibBytes(dst.Bytes())
			if err != nil || sha256.Sum256(decoded) != sha256.Sum256(updated) {
				return fmt.Errorf("group %d failed compression round-trip", g)
			}
			compSize = dst.Len()
			encoded[g] = dst.Bytes()
			decryptProtected("mAIn", encoded[g])
		}
		previous = item.compressedSize
		total += compSize
		if total > 1<<31-1 {
			return fmt.Errorf("body too large")
		}
		binary.LittleEndian.PutUint32(groups[8*g:], uint32(total))
		binary.LittleEndian.PutUint32(groups[8*g+4:], uint32(originalSize))
	}
	header := a.header.plain
	binary.LittleEndian.PutUint32(header[32:36], uint32(total))
	binary.LittleEndian.PutUint32(header[44:48], uint32(len(names)))
	decryptProtected("iNfO", header[:])
	copy(prefix, header[:])
	decryptProtected("Gidx", groups)
	copy(prefix[groupOffset:], groups)
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.Write(prefix); err != nil {
		return err
	}
	previous = 0
	for g, item := range a.groups {
		p := encoded[g]
		if p == nil {
			p = a.data[a.bodyOff+previous : a.bodyOff+item.compressedSize]
		}
		if _, err := w.Write(p); err != nil {
			return err
		}
		previous = item.compressedSize
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	built, err := LoadArchive(Options{Path: output, MaxBytes: 1 << 30})
	if err != nil {
		return err
	}
	if !bytes.Equal(prefix, built.data[:built.bodyOff]) || built.FileCount() != a.FileCount() {
		return fmt.Errorf("reopened archive layout mismatch")
	}
	if inlinePlan != "" && (!bytes.Equal(a.strA, built.strA) || !bytes.Equal(wide, built.strW)) {
		return fmt.Errorf("reopened localization string pools mismatch")
	}
	for _, ids := range byGroup {
		for _, i := range ids {
			want := replacements[i]
			if want == nil {
				want, err = a.readRawIndex(i)
				if err != nil {
					return err
				}
			}
			got, err := built.readRawIndex(i)
			if err != nil || !bytes.Equal(got, want) {
				return fmt.Errorf("reopened entry %d did not match expected bytes: %v", i, err)
			}
		}
	}
	fmt.Printf("verified archive=%s files=%d replacements=%d changed_groups=%d sha256=%s\n", output, a.FileCount(), len(replacements), len(byGroup), built.Snapshot().Checksum)
	return nil
}

func (a *Archive) spliceLocalization(old, prefix []byte, ids []int, replacements map[int][]byte) ([]byte, error) {
	var updated bytes.Buffer
	cursor, delta := 0, 0
	for _, i := range ids {
		it := a.items[i]
		start, end := it.dataOffset, it.dataOffset+it.dataSize
		if start < cursor || end < start || end > len(old) {
			return nil, fmt.Errorf("invalid or overlapping entry %d", i)
		}
		rec := prefix[headerSize+i*fileItemSize:]
		binary.LittleEndian.PutUint32(rec[12:16], uint32(start+delta))
		if raw := replacements[i]; raw != nil {
			updated.Write(old[cursor:start])
			updated.Write(raw)
			cursor = end
			delta += len(raw) - it.dataSize
			binary.LittleEndian.PutUint32(rec[16:20], uint32(len(raw)))
		}
	}
	updated.Write(old[cursor:])
	if updated.Len() > 256<<20 {
		return nil, fmt.Errorf("expanded group too large")
	}
	return updated.Bytes(), nil
}
