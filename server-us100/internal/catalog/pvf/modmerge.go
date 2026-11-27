package pvf

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// MergeLayer describes one source file contributed by an enabled Mod.
// Layers are supplied in priority order (index 0 = highest priority).
type MergeLayer struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	ModID    string `json:"mod_id"`
	Priority int    `json:"priority"`
}

// MergePlan is the JSON input accepted by MergeModPlan.
type MergePlan struct {
	Layers []MergeLayer `json:"layers"`
}

// MergeReport summarizes a verified PVF merge.
type MergeReport struct {
	Output        string `json:"output"`
	SHA256        string `json:"sha256"`
	TotalFiles    int    `json:"total_files"`
	ReplacedFiles int    `json:"replaced_files"`
	AddedFiles    int    `json:"added_files"`
	MergedLists   int    `json:"merged_lists"`
	MergedStrings int    `json:"merged_strings"`
	ChangedGroups int    `json:"changed_groups"`
}

type poolBuilder struct {
	strA  []byte
	strW  []byte
	seenA map[string]int
	seenW map[string]int
}

func newPoolBuilder(origA, origW []byte) *poolBuilder {
	a := bytes.Clone(origA)
	w := bytes.Clone(origW)
	if len(w)%2 != 0 {
		w = append(w, 0)
	}
	return &poolBuilder{
		strA:  a,
		strW:  w,
		seenA: map[string]int{},
		seenW: map[string]int{},
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func (pb *poolBuilder) addASCII(s string) int {
	if off, ok := pb.seenA[s]; ok {
		return off
	}
	off := len(pb.strA) << 1
	pb.strA = append(pb.strA, s...)
	pb.strA = append(pb.strA, 0)
	pb.seenA[s] = off
	return off
}

func (pb *poolBuilder) addWide(s string) int {
	if off, ok := pb.seenW[s]; ok {
		return off
	}
	off := len(pb.strW) | 1
	for _, u := range utf16.Encode([]rune(s)) {
		pb.strW = binary.LittleEndian.AppendUint16(pb.strW, u)
	}
	pb.strW = append(pb.strW, 0, 0)
	pb.seenW[s] = off
	return off
}

func (pb *poolBuilder) addString(s string) int {
	if isASCII(s) {
		return pb.addASCII(s)
	}
	return pb.addWide(s)
}

func (pb *poolBuilder) resolve(magicOffset int) string {
	if magicOffset < 0 {
		return ""
	}
	if magicOffset&1 != 0 {
		return readUTF16String(pb.strW, (magicOffset>>1)*2)
	}
	return readUTF8String(pb.strA, magicOffset>>1)
}

// decodeModText converts UTF-8 (with/without BOM), UTF-16LE/BE BOM, Shift-JIS, or GBK to UTF-8 string.
func decodeModText(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		return decodeUTF16LE(raw[2:])
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		units := make([]uint16, 0, (len(raw)-2)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			units = append(units, binary.BigEndian.Uint16(raw[i:i+2]))
		}
		return string(utf16.Decode(units))
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		raw = raw[3:]
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	if s, err := simplifiedchinese.GB18030.NewDecoder().String(string(raw)); err == nil && utf8.ValidString(s) {
		return s
	}
	if s, err := japanese.ShiftJIS.NewDecoder().String(string(raw)); err == nil && utf8.ValidString(s) {
		return s
	}
	return string(raw)
}

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

// isRawTextDataType returns true for PVF entries stored as DataType 3 (raw text/UTF-16 tables).
func isRawTextDataType(relPath string) bool {
	ext := strings.ToLower(path.Ext(relPath))
	switch ext {
	case ".str", ".xui", ".lua", ".cos", ".json", ".txt", ".xml", ".csv", ".ini", ".nut":
		return true
	}
	return false
}

// looksBinaryScript returns true if raw bytes appear to be pre-compiled 5-byte PVF tokens rather than text.
func looksBinaryScript(raw []byte) bool {
	if len(raw) == 0 || len(raw)%5 != 0 {
		return false
	}
	hasNul := bytes.IndexByte(raw, 0) >= 0
	if !hasNul {
		return false
	}
	// Check if first 20 cells all have valid PVF token type tags (0..10).
	limit := len(raw) / 5
	if limit > 20 {
		limit = 20
	}
	for i := 0; i < limit; i++ {
		t := raw[i*5]
		if t > 10 {
			return false
		}
	}
	return true
}

// compileScriptText compiles a DNF PVF text script (.equ, .stk, .lst, .etc, .ani, .map, etc.)
// into native 5-byte token cells (DataType 1), appending new strings to pb.
func compileScriptText(text string, pb *poolBuilder) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(text))
	emit := func(t byte, v int32) {
		out.WriteByte(t)
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(v))
		out.Write(b[:])
	}

	// Strip optional #PVF_File header line used by pvfUtility/90JP mod authors.
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(trimmed, "#PVF_File") {
		if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
			text = trimmed[nl+1:]
		} else {
			text = ""
		}
	}

	i := 0
	n := len(text)
	for i < n {
		c := text[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		// Line comment (// ...)
		if c == '/' && i+1 < n && text[i+1] == '/' {
			for i < n && text[i] != '\n' {
				i++
			}
			continue
		}
		// Backtick quoted string: `...` -> token type 6
		if c == '`' {
			start := i + 1
			end := strings.IndexByte(text[start:], '`')
			if end < 0 {
				return nil, fmt.Errorf("unterminated backtick string at offset %d", i)
			}
			val := text[start : start+end]
			emit(6, int32(pb.addString(val)))
			i = start + end + 1
			continue
		}
		// Localization reference: <string/equipment.uv.str::key> -> token type 8
		if c == '<' {
			end := strings.IndexByte(text[i+1:], '>')
			if end > 0 && strings.Contains(text[i+1:i+1+end], "::") {
				ref := text[i : i+1+end+1]
				emit(8, int32(pb.addString(ref)))
				i = i + 1 + end + 1
				continue
			}
		}
		// Tag token: [tag name] or [/tag name] -> token type 3
		if c == '[' {
			end := strings.IndexByte(text[i+1:], ']')
			if end >= 0 {
				tag := text[i : i+1+end+1]
				emit(3, int32(pb.addString(tag)))
				i = i + 1 + end + 1
				continue
			}
		}
		// Read bare token up to whitespace
		start := i
		for i < n && text[i] != ' ' && text[i] != '\t' && text[i] != '\r' && text[i] != '\n' {
			i++
		}
		tok := text[start:i]
		if tok == "" {
			continue
		}
		if iv, err := strconv.ParseInt(tok, 10, 32); err == nil {
			emit(0, int32(iv))
			continue
		}
		if fv, err := strconv.ParseFloat(tok, 32); err == nil {
			emit(2, int32(math.Float32bits(float32(fv))))
			continue
		}
		// Fallback bare string
		emit(6, int32(pb.addString(tok)))
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("compiled script produced 0 tokens")
	}
	return out.Bytes(), nil
}

type lstEntry struct {
	id   int32
	path string
}

func parseLstText(text string) ([]lstEntry, bool, error) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(trimmed, "#PVF_File") {
		if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
			text = trimmed[nl+1:]
		} else {
			text = ""
		}
	}
	hasIndexTag := false
	var entries []lstEntry
	i := 0
	n := len(text)
	for i < n {
		c := text[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if c == '/' && i+1 < n && text[i+1] == '/' {
			for i < n && text[i] != '\n' {
				i++
			}
			continue
		}
		if c == '[' {
			end := strings.IndexByte(text[i+1:], ']')
			if end < 0 {
				return nil, false, fmt.Errorf("unterminated tag in .lst")
			}
			tag := strings.ToLower(text[i : i+1+end+1])
			if tag == "[index]" {
				hasIndexTag = true
			}
			i = i + 1 + end + 1
			continue
		}
		// Expect integer ID followed by backtick path
		start := i
		for i < n && text[i] != ' ' && text[i] != '\t' && text[i] != '\r' && text[i] != '\n' && text[i] != '`' {
			i++
		}
		idStr := text[start:i]
		iv, err := strconv.ParseInt(idStr, 10, 32)
		if err != nil {
			return nil, false, fmt.Errorf("invalid .lst numeric id %q", idStr)
		}
		for i < n && (text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n') {
			i++
		}
		if i >= n || text[i] != '`' {
			return nil, false, fmt.Errorf("expected backtick path after .lst id %d", iv)
		}
		pStart := i + 1
		pEnd := strings.IndexByte(text[pStart:], '`')
		if pEnd < 0 {
			return nil, false, fmt.Errorf("unterminated backtick path for .lst id %d", iv)
		}
		pVal := strings.ReplaceAll(text[pStart:pStart+pEnd], "\\", "/")
		entries = append(entries, lstEntry{id: int32(iv), path: pVal})
		i = pStart + pEnd + 1
	}
	return entries, hasIndexTag, nil
}

func (a *Archive) mergeLst(relPath string, layers []MergeLayer, pb *poolBuilder) ([]byte, error) {
	merged := map[int32]string{}
	order := make([]int32, 0, 1024)
	seen := map[int32]bool{}
	hasHeaderTag := false
	headerTag := ""

	addEntry := func(id int32, p string) {
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
		merged[id] = p
	}

	if idx, ok := a.findIndexExact(relPath); ok && a.items[idx].dataType == 1 {
		toks, err := a.Tokens(relPath)
		if err != nil {
			return nil, fmt.Errorf("read base .lst %s: %w", relPath, err)
		}
		for k := 0; k < len(toks); {
			if toks[k].Type == 3 {
				if k == 0 {
					hasHeaderTag = true
					headerTag = toks[k].Text
				}
				k++
				continue
			}
			if k+1 < len(toks) && toks[k].Type == 0 && toks[k+1].Type == 6 {
				addEntry(toks[k].Value, toks[k+1].Text)
				k += 2
				continue
			}
			k++
		}
	}

	// Apply layers from lowest priority to highest priority so highest priority wins on duplicate ID.
	for li := len(layers) - 1; li >= 0; li-- {
		raw, err := os.ReadFile(layers[li].Source)
		if err != nil {
			return nil, err
		}
		text := decodeModText(raw)
		entries, hasIdx, err := parseLstText(text)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", relPath, layers[li].ModID, err)
		}
		if hasIdx && !hasHeaderTag {
			hasHeaderTag = true
			headerTag = "[index]"
		}
		for _, e := range entries {
			addEntry(e.id, e.path)
		}
	}

	var out bytes.Buffer
	out.Grow(len(order)*10 + 16)
	emit := func(t byte, v int32) {
		out.WriteByte(t)
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(v))
		out.Write(b[:])
	}
	if hasHeaderTag && headerTag != "" {
		emit(3, int32(pb.addString(headerTag)))
	}
	for _, id := range order {
		emit(0, id)
		emit(6, int32(pb.addString(merged[id])))
	}
	return out.Bytes(), nil
}

func parseStrLines(text string) ([]string, map[string]string) {
	order := []string{}
	kv := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ">")
		if !ok || k == "" {
			continue
		}
		if _, exists := kv[k]; !exists {
			order = append(order, k)
		}
		kv[k] = v
	}
	return order, kv
}

func (a *Archive) mergeStrTable(relPath string, layers []MergeLayer) ([]byte, error) {
	order := []string{}
	kv := map[string]string{}
	if idx, ok := a.findIndexExact(relPath); ok && a.items[idx].dataType == 3 {
		raw, err := a.readRawIndex(idx)
		if err != nil {
			return nil, err
		}
		baseText := decodeUTF16LE(raw)
		if strings.HasPrefix(baseText, "\uFEFF") {
			baseText = strings.TrimPrefix(baseText, "\uFEFF")
		}
		order, kv = parseStrLines(baseText)
	}
	// Apply from lowest priority to highest priority so higher priority wins.
	for li := len(layers) - 1; li >= 0; li-- {
		raw, err := os.ReadFile(layers[li].Source)
		if err != nil {
			return nil, err
		}
		modOrder, modKV := parseStrLines(decodeModText(raw))
		for _, k := range modOrder {
			if _, exists := kv[k]; !exists {
				order = append(order, k)
			}
			kv[k] = modKV[k]
		}
	}
	var b strings.Builder
	b.Grow(len(order) * 32)
	for _, k := range order {
		b.WriteString(k)
		b.WriteByte('>')
		b.WriteString(kv[k])
		b.WriteString("\r\n")
	}
	return encodeUTF16LE(b.String()), nil
}

func (a *Archive) replaceBothStringPools(names, newStrA, newStrW []byte) ([]byte, error) {
	if len(names) < 24 || len(newStrW)%2 != 0 {
		return nil, fmt.Errorf("invalid string pools")
	}
	pos := 8
	if _, err := decryptPStringBuffer(names, &pos, "stAs", 0xe7adf7ea); err != nil {
		return nil, err
	}
	if _, err := decryptPStringBuffer(names, &pos, "stWs", 0xb8dea7ac); err != nil {
		return nil, err
	}
	encA, err := compressZlib(newStrA)
	if err != nil {
		return nil, err
	}
	decryptPString("stAs", encA)
	encW, err := compressZlib(newStrW)
	if err != nil {
		return nil, err
	}
	decryptPString("stWs", encW)

	out := make([]byte, 0, 8+8+len(encA)+8+len(encW)+len(names)-pos)
	out = append(out, names[:8]...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(encA))^0xe7adf7ea)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(newStrA)^len(encA)))
	out = append(out, encA...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(encW))^0xb8dea7ac)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(newStrW)^len(encW)))
	out = append(out, encW...)
	out = append(out, names[pos:]...)
	return out, nil
}

func (a *Archive) appendHSrm(origHashes []byte, added []fileItem, pb *poolBuilder) ([]byte, error) {
	if len(added) == 0 {
		return origHashes, nil
	}
	hashes := bytes.Clone(origHashes)
	decryptProtected("HSrm", hashes)
	if len(hashes) < 8 {
		return nil, fmt.Errorf("short HSrm section")
	}
	n := readInt32(hashes[:4])
	p := 4 + n*8
	m := readInt32(hashes[p : p+4])
	if n < 0 || m < 0 || p+4+m*4 != len(hashes) {
		return nil, fmt.Errorf("invalid HSrm layout")
	}

	// Copy existing pairs and append new (nameOff, pathOff) pairs.
	pairs := make([]byte, (n+len(added))*8)
	copy(pairs, hashes[4:p])
	for i, it := range added {
		off := (n + i) * 8
		binary.LittleEndian.PutUint32(pairs[off:], uint32(it.nameOffset))
		binary.LittleEndian.PutUint32(pairs[off+4:], uint32(it.pathOffset))
	}

	// Copy existing sorted unique filename offsets and binary-insert any new unique filenames.
	idxs := make([]uint32, m, m+len(added))
	for i := 0; i < m; i++ {
		idxs[i] = binary.LittleEndian.Uint32(hashes[p+4+i*4:])
	}
	for _, it := range added {
		name := pb.resolve(it.nameOffset)
		pos := sort.Search(len(idxs), func(i int) bool {
			return pb.resolve(int(idxs[i])) >= name
		})
		if pos < len(idxs) && pb.resolve(int(idxs[pos])) == name {
			continue
		}
		idxs = append(idxs, 0)
		copy(idxs[pos+1:], idxs[pos:])
		idxs[pos] = uint32(it.nameOffset)
	}

	out := make([]byte, 4+len(pairs)+4+len(idxs)*4)
	binary.LittleEndian.PutUint32(out[0:4], uint32(n+len(added)))
	copy(out[4:4+len(pairs)], pairs)
	offM := 4 + len(pairs)
	binary.LittleEndian.PutUint32(out[offM:offM+4], uint32(len(idxs)))
	for i, v := range idxs {
		binary.LittleEndian.PutUint32(out[offM+4+i*4:], v)
	}
	decryptProtected("HSrm", out)
	return out, nil
}

// MergeModPlan merges one or more 90JP-style PVF mod layers into a new verified inner PVF.
// Supports replacing existing entries, adding brand-new entries, merging .lst by numeric ID,
// and merging .str localization tables by key.
func (a *Archive) MergeModPlan(plan MergePlan, output string) (MergeReport, error) {
	if a.format != FormatDFO20260901 {
		return MergeReport{}, fmt.Errorf("unsupported PVF format for mod merge: %s", a.format)
	}
	if len(plan.Layers) == 0 {
		return MergeReport{}, fmt.Errorf("no mod layers to merge")
	}

	// Group layers by normalized PVF internal path, preserving priority order (0 = highest).
	byPath := map[string][]MergeLayer{}
	paths := []string{}
	for _, l := range plan.Layers {
		norm := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(l.Path), "\\", "/"), "/"))
		if norm == "" || strings.Contains(norm, "..") || strings.HasPrefix(norm, "/") {
			return MergeReport{}, fmt.Errorf("unsafe PVF mod path: %q", l.Path)
		}
		if _, ok := byPath[norm]; !ok {
			paths = append(paths, norm)
		}
		byPath[norm] = append(byPath[norm], l)
	}
	sort.Strings(paths)

	pb := newPoolBuilder(a.strA, a.strW)
	replacements := map[int][]byte{}
	byGroup := map[int][]int{}
	type newFileSpec struct {
		relPath  string
		data     []byte
		dataType int
		item     fileItem
	}
	var addedSpecs []newFileSpec
	expectedCheck := map[string][]byte{}
	var report MergeReport

	for _, rel := range paths {
		layers := byPath[rel]
		sort.SliceStable(layers, func(i, j int) bool { return layers[i].Priority < layers[j].Priority })
		ext := strings.ToLower(path.Ext(rel))

		var data []byte
		var dataType int
		var err error

		if ext == ".lst" {
			data, err = a.mergeLst(rel, layers, pb)
			if err != nil {
				return MergeReport{}, err
			}
			dataType = 1
			report.MergedLists++
		} else if ext == ".str" {
			data, err = a.mergeStrTable(rel, layers)
			if err != nil {
				return MergeReport{}, err
			}
			dataType = 3
			report.MergedStrings++
		} else {
			// Ordinary file: highest priority layer (index 0) wins.
			raw, rerr := os.ReadFile(layers[0].Source)
			if rerr != nil {
				return MergeReport{}, fmt.Errorf("read %s: %w", layers[0].Source, rerr)
			}
			if idx, ok := a.findIndexExact(rel); ok {
				dataType = a.items[idx].dataType
			} else if isRawTextDataType(rel) {
				dataType = 3
			} else {
				dataType = 1
			}
			if dataType == 1 {
				if looksBinaryScript(raw) {
					data = raw
				} else {
					data, err = compileScriptText(decodeModText(raw), pb)
					if err != nil {
						return MergeReport{}, fmt.Errorf("compile %s (%s): %w", rel, layers[0].ModID, err)
					}
				}
			} else if ext == ".xui" {
				data = encodeUTF16LE(decodeModText(raw))
			} else {
				data = raw
			}
		}

		expectedCheck[rel] = data
		if idx, ok := a.findIndexExact(rel); ok {
			replacements[idx] = data
			byGroup[a.items[idx].chunkIndex] = nil
			report.ReplacedFiles++
		} else {
			dir := path.Dir(rel)
			if dir == "." {
				dir = ""
			}
			base := path.Base(rel)
			addedSpecs = append(addedSpecs, newFileSpec{
				relPath:  rel,
				data:     data,
				dataType: dataType,
				item: fileItem{
					nameOffset: pb.addASCII(base),
					pathOffset: pb.addASCII(dir),
					dataSize:   len(data),
					dataType:   dataType,
				},
			})
			report.AddedFiles++
		}
	}

	// Build updated file table (existing items + newly added items).
	oldFileTableEnd := headerSize + len(a.items)*fileItemSize
	oldHashOffset := oldFileTableEnd
	oldNameOffset := oldHashOffset + a.header.hashSize
	oldGroupOffset := oldNameOffset + a.header.nameSize

	for i, it := range a.items {
		if _, ok := byGroup[it.chunkIndex]; ok {
			byGroup[it.chunkIndex] = append(byGroup[it.chunkIndex], i)
		}
	}

	// Pack added files into new chunks (max 16 MiB uncompressed per chunk).
	const maxNewChunk = 16 << 20
	type newChunkData struct {
		compressed []byte
		origSize   int
	}
	var newChunks []newChunkData
	if len(addedSpecs) > 0 {
		var cur bytes.Buffer
		chunkIdx := len(a.groups)
		flushChunk := func() error {
			if cur.Len() == 0 {
				return nil
			}
			var dst bytes.Buffer
			z, err := zlib.NewWriterLevel(&dst, zlib.BestCompression)
			if err != nil {
				return err
			}
			if _, err := z.Write(cur.Bytes()); err != nil {
				return err
			}
			if err := z.Close(); err != nil {
				return err
			}
			comp := dst.Bytes()
			decryptProtected("mAIn", comp)
			newChunks = append(newChunks, newChunkData{compressed: comp, origSize: cur.Len()})
			cur.Reset()
			chunkIdx++
			return nil
		}
		for i := range addedSpecs {
			if cur.Len() > 0 && cur.Len()+len(addedSpecs[i].data) > maxNewChunk {
				if err := flushChunk(); err != nil {
					return MergeReport{}, err
				}
			}
			addedSpecs[i].item.chunkIndex = chunkIdx
			addedSpecs[i].item.dataOffset = cur.Len()
			cur.Write(addedSpecs[i].data)
		}
		if err := flushChunk(); err != nil {
			return MergeReport{}, err
		}
	}

	// Build updated HSrm & string pools.
	addedItems := make([]fileItem, len(addedSpecs))
	for i, s := range addedSpecs {
		addedItems[i] = s.item
	}
	newHashes, err := a.appendHSrm(a.data[oldHashOffset:oldNameOffset], addedItems, pb)
	if err != nil {
		return MergeReport{}, fmt.Errorf("update HSrm: %w", err)
	}
	newNames := a.data[oldNameOffset:oldGroupOffset]
	if len(pb.strA) != len(a.strA) || len(pb.strW) != len(a.strW) {
		newNames, err = a.replaceBothStringPools(newNames, pb.strA, pb.strW)
		if err != nil {
			return MergeReport{}, fmt.Errorf("update string pools: %w", err)
		}
	}

	totalFiles := len(a.items) + len(addedItems)
	totalGroups := len(a.groups) + len(newChunks)
	fileTableBytes := make([]byte, totalFiles*fileItemSize)
	copy(fileTableBytes, a.data[headerSize:oldFileTableEnd])
	for i, it := range addedItems {
		rec := fileTableBytes[(len(a.items)+i)*fileItemSize:]
		binary.LittleEndian.PutUint32(rec[0:4], uint32(it.nameOffset))
		binary.LittleEndian.PutUint32(rec[4:8], uint32(it.pathOffset))
		binary.LittleEndian.PutUint32(rec[8:12], uint32(it.chunkIndex))
		binary.LittleEndian.PutUint32(rec[12:16], uint32(it.dataOffset))
		binary.LittleEndian.PutUint32(rec[16:20], uint32(it.dataSize))
		binary.LittleEndian.PutUint32(rec[20:24], uint32(it.dataType))
	}

	// Recompress modified existing groups and build full groups table.
	groupsBytes := make([]byte, totalGroups*groupItemSize)
	encoded := map[int][]byte{}
	previous, totalBody := 0, 0
	for g, item := range a.groups {
		compSize, origSize := item.compressedSize-previous, item.originalSize
		if ids, ok := byGroup[g]; ok {
			oldChunk, err := a.chunk(g)
			if err != nil {
				return MergeReport{}, err
			}
			sort.Slice(ids, func(i, j int) bool {
				x, y := a.items[ids[i]], a.items[ids[j]]
				if x.dataOffset != y.dataOffset {
					return x.dataOffset < y.dataOffset
				}
				return x.dataSize < y.dataSize
			})
			var updated bytes.Buffer
			cursor, delta := 0, 0
			for _, id := range ids {
				it := a.items[id]
				start, end := it.dataOffset, it.dataOffset+it.dataSize
				if start < cursor || end < start || end > len(oldChunk) {
					return MergeReport{}, fmt.Errorf("overlapping entry %d in group %d", id, g)
				}
				rec := fileTableBytes[id*fileItemSize:]
				binary.LittleEndian.PutUint32(rec[12:16], uint32(start+delta))
				if raw := replacements[id]; raw != nil {
					updated.Write(oldChunk[cursor:start])
					updated.Write(raw)
					cursor = end
					delta += len(raw) - it.dataSize
					binary.LittleEndian.PutUint32(rec[16:20], uint32(len(raw)))
				}
			}
			updated.Write(oldChunk[cursor:])
			if updated.Len() > 256<<20 {
				return MergeReport{}, fmt.Errorf("expanded group %d too large", g)
			}
			origSize = updated.Len()
			var dst bytes.Buffer
			z, err := zlib.NewWriterLevel(&dst, zlib.BestCompression)
			if err != nil {
				return MergeReport{}, err
			}
			if _, err := z.Write(updated.Bytes()); err != nil {
				return MergeReport{}, err
			}
			if err := z.Close(); err != nil {
				return MergeReport{}, err
			}
			decoded, err := zlibBytes(dst.Bytes())
			if err != nil || sha256.Sum256(decoded) != sha256.Sum256(updated.Bytes()) {
				return MergeReport{}, fmt.Errorf("group %d failed compression round-trip", g)
			}
			compSize = dst.Len()
			encoded[g] = dst.Bytes()
			decryptProtected("mAIn", encoded[g])
		}
		previous = item.compressedSize
		totalBody += compSize
		if totalBody > 1<<31-1 {
			return MergeReport{}, fmt.Errorf("merged PVF body exceeds 2 GiB")
		}
		binary.LittleEndian.PutUint32(groupsBytes[8*g:], uint32(totalBody))
		binary.LittleEndian.PutUint32(groupsBytes[8*g+4:], uint32(origSize))
	}
	for i, nc := range newChunks {
		g := len(a.groups) + i
		totalBody += len(nc.compressed)
		if totalBody > 1<<31-1 {
			return MergeReport{}, fmt.Errorf("merged PVF body exceeds 2 GiB")
		}
		binary.LittleEndian.PutUint32(groupsBytes[8*g:], uint32(totalBody))
		binary.LittleEndian.PutUint32(groupsBytes[8*g+4:], uint32(nc.origSize))
	}
	decryptProtected("Gidx", groupsBytes)

	header := a.header.plain
	binary.LittleEndian.PutUint32(header[24:28], uint32(totalFiles))
	binary.LittleEndian.PutUint32(header[32:36], uint32(totalBody))
	binary.LittleEndian.PutUint32(header[36:40], uint32(totalGroups))
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(newHashes)))
	binary.LittleEndian.PutUint32(header[44:48], uint32(len(newNames)))
	decryptProtected("iNfO", header[:])

	tmpOut := output + ".tmp"
	_ = os.Remove(tmpOut)
	f, err := os.OpenFile(tmpOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return MergeReport{}, err
	}
	defer func() {
		f.Close()
		_ = os.Remove(tmpOut)
	}()
	w := bufio.NewWriterSize(f, 1<<20)
	for _, block := range [][]byte{header[:], fileTableBytes, newHashes, newNames, groupsBytes} {
		if _, err := w.Write(block); err != nil {
			return MergeReport{}, err
		}
	}
	previous = 0
	for g, item := range a.groups {
		chunk := encoded[g]
		if chunk == nil {
			chunk = a.data[a.bodyOff+previous : a.bodyOff+item.compressedSize]
		}
		if _, err := w.Write(chunk); err != nil {
			return MergeReport{}, err
		}
		previous = item.compressedSize
	}
	for _, nc := range newChunks {
		if _, err := w.Write(nc.compressed); err != nil {
			return MergeReport{}, err
		}
	}
	if err := w.Flush(); err != nil {
		return MergeReport{}, err
	}
	if err := f.Sync(); err != nil {
		return MergeReport{}, err
	}
	if err := f.Close(); err != nil {
		return MergeReport{}, err
	}
	if err := os.Rename(tmpOut, output); err != nil {
		return MergeReport{}, err
	}

	// Read-back verification: reopen the merged archive and check every merged/added path.
	var built *Archive
	if info, serr := os.Stat(output); serr == nil && info.Size() <= 32<<20 {
		rawOut, rerr := os.ReadFile(output)
		if rerr != nil {
			return MergeReport{}, fmt.Errorf("read-back read failed: %w", rerr)
		}
		built, err = OpenBytes(rawOut)
	} else {
		built, err = LoadArchive(Options{Path: output, MaxBytes: 1 << 30})
	}
	if err != nil {
		return MergeReport{}, fmt.Errorf("read-back open failed: %w", err)
	}
	if built.FileCount() != totalFiles {
		return MergeReport{}, fmt.Errorf("read-back file count mismatch: want %d got %d", totalFiles, built.FileCount())
	}
	for rel, want := range expectedCheck {
		got, err := built.ReadRaw(rel)
		if err != nil || !bytes.Equal(got, want) {
			return MergeReport{}, fmt.Errorf("read-back verify failed for %s: %v", rel, err)
		}
	}

	report.Output = output
	report.SHA256 = built.Snapshot().Checksum
	report.TotalFiles = built.FileCount()
	report.ChangedGroups = len(byGroup) + len(newChunks)
	return report, nil
}
