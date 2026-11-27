package pvf

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

type inlineTranslation struct {
	Field  string `json:"field"`
	Source string `json:"source"`
	Target string `json:"target"`
	Count  int    `json:"count"`
}

func (a *Archive) stageInlineLocalization(plan string, entries map[int][]byte, groups map[int][]int) ([]byte, error) {
	blob, err := os.ReadFile(plan)
	if err != nil {
		return nil, err
	}
	data := map[string][]inlineTranslation{}
	if err := json.Unmarshal(blob, &data); err != nil || len(data) == 0 {
		return nil, fmt.Errorf("invalid inline localization plan: %v", err)
	}
	wide := bytes.Clone(a.strW)
	if len(wide)%2 != 0 {
		return nil, fmt.Errorf("unaligned wide string pool")
	}
	paths := make([]string, 0, len(data))
	for path := range data {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	offsets := map[string]uint32{}
	for _, path := range paths {
		cinema := false
		shop := false
		switch path {
		case "event/universal/guideofwisdomsimulator.evt", "event/universal/guideofwisdomsimulatoritem.evt":
		case "etc/(r)cerashop.etc":
			shop = true
		case "passiveobject/cinematicobject/threekingdomsdungeonmovie/3kingdoms_01.obj",
			"passiveobject/cinematicobject/threekingdomsdungeonmovie/3kingdoms_02.obj",
			"passiveobject/cinematicobject/threekingdomsdungeonmovie/3kingdoms_npc1.obj",
			"passiveobject/cinematicobject/threekingdomsdungeonmovie/3kingdoms_npc2.obj",
			"passiveobject/cinematicobject/threekingdomsdungeonmovie/3kingdoms_npc3.obj",
			"passiveobject/cinematicobject/threekingdomsdungeonmovie/3kingdoms_npc4.obj",
			"passiveobject/actionobject/c_event/chn_knight/lancelot_cinema_01.obj",
			"passiveobject/actionobject/c_event/chn_knight/lancelot_cinema_02.obj",
			"passiveobject/actionobject/c_event/chn_knight/lancelot_cinema_06.obj",
			"passiveobject/actionobject/c_event/chn_knight/modred_cinema_04.obj",
			"passiveobject/cinematicobject/westerndungeon/larguslakecinema.obj":
			cinema = true
		default:
			return nil, fmt.Errorf("unreviewed inline localization script %s", path)
		}
		i, ok := a.findIndexExact(path)
		if !ok || a.items[i].dataType != 1 || entries[i] != nil {
			return nil, fmt.Errorf("expected an existing unmodified compiled script: %s", path)
		}
		raw, err := a.readRawIndex(i)
		if err != nil {
			return nil, err
		}
		if len(data[path]) == 0 {
			return nil, fmt.Errorf("empty inline localization plan: %s", path)
		}
		for _, row := range data[path] {
			if shop {
				break
			}
			if (!cinema && row.Field != "[name]" && row.Field != "[desc]") ||
				(cinema && row.Field != "[notice]" && row.Field != "[speech]") {
				return nil, fmt.Errorf("unreviewed inline field %s:%s", path, row.Field)
			}
		}
		add := func(target string) uint32 {
			if offset, ok := offsets[target]; ok {
				return offset
			}
			// Wide offsets address 16-bit code units and use the low bit as the pool selector.
			offset := uint32(len(wide)) | 1
			for _, unit := range utf16.Encode([]rune(target)) {
				wide = binary.LittleEndian.AppendUint16(wide, unit)
			}
			wide = append(wide, 0, 0)
			offsets[target] = offset
			return offset
		}
		var patched []byte
		if shop {
			patched, err = a.translateShopCheckoutDisplay(raw, data[path], add)
		} else {
			patched, err = a.translateInlineDisplay(raw, data[path], add)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		entries[i] = patched
		groups[a.items[i].chunkIndex] = nil
	}
	if len(wide) >= 1<<30 {
		return nil, fmt.Errorf("expanded wide pool too large")
	}
	return wide, nil
}

// Only the display string for the vetted 100US offer is writable; validate the
// typed record and old English value before changing its four-byte reference.
func (a *Archive) translateShopCheckoutDisplay(raw []byte, rows []inlineTranslation, add func(string) uint32) ([]byte, error) {
	const label = "The Neo: Awakened Ones Package"
	const target = "《新：觉醒者》礼包"
	if len(raw) != 601380 || len(rows) != 1 || rows[0] != (inlineTranslation{"[package display name]", label, target, 1}) {
		return nil, fmt.Errorf("unreviewed checkout display plan")
	}
	const offer = 52837 * 5
	for i, expect := range []uint32{3400149, 590005784, 0, 0, 3980, 0, 0} {
		o := offer + 5*i
		if raw[o] != 0 || binary.LittleEndian.Uint32(raw[o+1:o+5]) != expect {
			return nil, fmt.Errorf("checkout offer numeric prefix changed")
		}
	}
	o := offer + 7*5
	if raw[o] != 6 || a.usInlineString(readInt32(raw[o+1:o+5])) != label {
		return nil, fmt.Errorf("checkout offer original display string changed")
	}
	patched := bytes.Clone(raw)
	binary.LittleEndian.PutUint32(patched[o+1:o+5], add(target))
	return patched, nil
}

func (a *Archive) translateInlineDisplay(raw []byte, translations []inlineTranslation, add func(string) uint32) ([]byte, error) {
	if len(raw)%5 != 0 {
		return nil, fmt.Errorf("misaligned compiled script cells")
	}
	type key struct{ field, source string }
	targets := make(map[key]inlineTranslation, len(translations))
	for _, row := range translations {
		k := key{row.Field, row.Source}
		if !inlineDisplayField(row.Field) ||
			row.Source == "" || row.Target == row.Source || row.Count <= 0 || len(row.Target) > 512 ||
			strings.ContainsAny(row.Target, "\x00\r\n") || strings.IndexFunc(row.Target, func(r rune) bool {
			return unicode.Is(unicode.Han, r)
		}) < 0 || targets[k].Count > 0 {
			return nil, fmt.Errorf("invalid or duplicate inline display value %q", row.Source)
		}
		targets[k] = row
	}
	seen := map[key]int{}
	patched := bytes.Clone(raw)
	for j := 0; j+10 <= len(raw); j += 5 {
		if raw[j] != 3 || raw[j+5] != 6 {
			continue
		}
		field := a.resolveString(readInt32(raw[j+1 : j+5]))
		if !inlineDisplayField(field) {
			continue
		}
		k := key{field, a.usInlineString(readInt32(raw[j+6 : j+10]))}
		if row, ok := targets[k]; ok {
			binary.LittleEndian.PutUint32(patched[j+6:j+10], add(row.Target))
			seen[k]++
		}
	}
	for k, row := range targets {
		if seen[k] != row.Count {
			return nil, fmt.Errorf("inline display %s %q: expected %d, observed %d", k.field, k.source, row.Count, seen[k])
		}
	}
	return patched, nil
}

func inlineDisplayField(field string) bool {
	return field == "[name]" || field == "[desc]" || field == "[notice]" || field == "[speech]"
}

func (a *Archive) replaceWideLocalizationPool(names, wide []byte) ([]byte, error) {
	if len(wide) >= 1<<30 || !bytes.HasPrefix(wide, a.strW) {
		return nil, fmt.Errorf("invalid expanded wide pool")
	}
	pos := 8
	if _, err := decryptPStringBuffer(names, &pos, "stAs", 0xe7adf7ea); err != nil {
		return nil, err
	}
	oldWideAt := pos
	oldWide, err := decryptPStringBuffer(names, &pos, "stWs", 0xb8dea7ac)
	if err != nil || oldWideAt == pos || pos > len(names) || !bytes.Equal(oldWide, a.strW) {
		return nil, fmt.Errorf("original wide pool not verified: %v", err)
	}
	encoded, err := compressZlib(wide)
	if err != nil {
		return nil, err
	}
	decryptPString("stWs", encoded)
	result := make([]byte, 0, oldWideAt+8+len(encoded)+len(names)-pos)
	result = append(result, names[:oldWideAt]...)
	result = binary.LittleEndian.AppendUint32(result, uint32(len(encoded))^0xb8dea7ac)
	result = binary.LittleEndian.AppendUint32(result, uint32(len(wide)^len(encoded)))
	result = append(result, encoded...)
	return append(result, names[pos:]...), nil
}
