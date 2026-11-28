package catalog

import (
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Portal struct {
	Bounds [4]int32 `json:"bounds"`
	Town   uint32   `json:"town"`
	Area   uint32   `json:"area"`
}

type WorldArea struct {
	Town             uint32         `json:"town"`
	Area             uint32         `json:"area"`
	MapPath          string         `json:"map_path"`
	MinimumLevel     uint32         `json:"minimum_level"`
	Kind             string         `json:"kind"`
	// Worldmap is the id written after a "[dungeon gate]" marker, which is the
	// map screen this gate opens. Zero for areas that are not gates.
	Worldmap         uint32         `json:"worldmap,omitempty"`
	Definition       []pvf.Token    `json:"definition"`
	Map              ScriptRecord   `json:"map"`
	ImportedScripts  []ScriptRecord `json:"imported_scripts,omitempty"`
	SeriaReturnWarp  bool           `json:"seria_return_warp,omitempty"`
	ReturnWarpBounds [][4]int32     `json:"return_warp_bounds,omitempty"`
	Walkable         [][4]int32     `json:"walkable"`
	Portals          []Portal       `json:"portals"`
	Imports          []string       `json:"imports,omitempty"`
	Pending          []string       `json:"pending,omitempty"`
}

type WorldCatalog struct {
	Source       pvf.ArchiveSnapshot  `json:"source"`
	TownIndex    ScriptRecord         `json:"town_index"`
	Towns        []ScriptRecord       `json:"towns"`
	Areas        map[string]WorldArea `json:"areas"`
	// Worldmaps maps a worldmap id to the dungeon ids its own map screen
	// lists, read from list/worldmap.lst and each worldmap script. The
	// dungeon gate's permission rows must be this set: the reference records
	// that sending ids which do not belong to the gate's worldmap "will leave
	// select UI slots empty" (flow-dungeon_enter._permission_entries).
	Worldmaps    map[string][]uint32 `json:"worldmaps,omitempty"`
	DungeonIndex ScriptRecord        `json:"dungeon_index"`
	Dungeons     []IndexEntry        `json:"dungeons"`
}

func AreaKey(town, area uint32) string { return fmt.Sprintf("%d/%d", town, area) }

func parseWorldAreas(town uint32, cells []pvf.Token) ([]WorldArea, error) {
	var rows []WorldArea
	for i := 0; i < len(cells); i++ {
		if cells[i].Type != 3 || cells[i].Text != "[area]" {
			continue
		}
		start := i + 1
		for i++; i < len(cells) && !(cells[i].Type == 3 && cells[i].Text == "[/area]"); i++ {
		}
		if i == len(cells) || i-start < 2 || cells[start].Type != 0 || cells[start].Value < 0 || cells[start+1].Type != 6 {
			return nil, fmt.Errorf("malformed area in town %d", town)
		}
		def := append([]pvf.Token(nil), cells[start:i]...)
		a := WorldArea{Town: town, Area: uint32(cells[start].Value), MapPath: "map/" + strings.ToLower(strings.ReplaceAll(cells[start+1].Text, "\\", "/")), Definition: def}
		permission := false
		for _, c := range def {
			if c.Type != 3 {
				continue
			}
			if c.Text == "[permission]" {
				permission = true
				continue
			}
			if c.Text == "[/permission]" {
				permission = false
				continue
			}
			if permission && c.Text != "[need level]" && c.Text != "[odyssey enter level]" {
				a.Pending = append(a.Pending, "unsupported permission "+c.Text)
			}
		}
		level := sectionCells(def, "[need level]")
		if len(level) > 0 {
			if len(level) != 1 || level[0].Type != 0 || level[0].Value < 0 {
				a.Pending = append(a.Pending, "conditional level rule requires interpretation")
			} else {
				a.MinimumLevel = uint32(level[0].Value)
			}
		}
		for i, c := range def {
			if c.Type != 6 {
				continue
			}
			switch c.Text {
			case "[normal]", "[gate]", "[dungeon gate]":
				a.Kind = c.Text
			}
			// A dungeon gate names its map screen right after the marker:
			// "`[dungeon gate]` 2" in the town script.
			if c.Text == "[dungeon gate]" && i+1 < len(def) && def[i+1].Type == 0 && def[i+1].Value > 0 {
				a.Worldmap = uint32(def[i+1].Value)
			}
		}
		rows = append(rows, a)
	}
	return rows, nil
}

func sourceRectangles(cells []pvf.Token, stride int) ([][]int32, error) {
	if len(cells)%stride != 0 {
		return nil, fmt.Errorf("incomplete rectangle rows")
	}
	var rows [][]int32
	for i := 0; i < len(cells); i += stride {
		row := make([]int32, stride)
		for j := range row {
			if cells[i+j].Type != 0 {
				return nil, fmt.Errorf("non-numeric rectangle cell")
			}
			row[j] = cells[i+j].Value
		}
		if row[2] < 0 || row[3] < 0 {
			return nil, fmt.Errorf("negative rectangle dimensions")
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// importWorldmaps reads list/worldmap.lst and every worldmap script it names,
// so a dungeon gate can be told which dungeons its own map screen contains.
//
// The list is optional: a source without it leaves gates without permission
// rows rather than failing the whole import.
func importWorldmaps(a *pvf.Archive, w *WorldCatalog) error {
	raw, e := a.ReadText("list/worldmap.lst")
	if e != nil {
		// The list is optional: a source without it leaves gates without
		// permission rows rather than failing the whole import.
		return nil
	}
	w.Worldmaps = map[string][]uint32{}
	for _, entry := range parseNameList(raw) {
		name := strings.ToLower(strings.ReplaceAll(entry.Path, "\\", "/"))
		path := name
		if !strings.Contains(name, "/") {
			path = "worldmap/" + name
		}
		text, e := a.ReadText(path)
		if e != nil {
			continue
		}
		if ids := parseWorldmapDungeons(text); len(ids) > 0 {
			w.Worldmaps[strconv.FormatUint(uint64(entry.ID), 10)] = ids
		}
	}
	return nil
}

type nameEntry struct {
	ID   uint32
	Path string
}

// parseNameList reads the "id `path`" token stream these .lst files use.
func parseNameList(raw string) []nameEntry {
	var out []nameEntry
	var pending *nameEntry
	for _, field := range strings.Fields(raw) {
		token := strings.Trim(field, "`")
		if token == "" {
			continue
		}
		if n, e := strconv.ParseUint(token, 10, 32); e == nil {
			out = append(out, nameEntry{ID: uint32(n)})
			pending = &out[len(out)-1]
			continue
		}
		if pending != nil && pending.Path == "" {
			pending.Path = token
		}
	}
	kept := out[:0]
	for _, entry := range out {
		if entry.Path != "" {
			kept = append(kept, entry)
		}
	}
	return kept
}

// parseWorldmapDungeons reads the [dungeon] block of a worldmap script: pairs
// of (dungeon id, condition), and only the pairs before the first
// [in progress] block, which lists quest-conditional nodes instead.
func parseWorldmapDungeons(text string) []uint32 {
	lower := strings.ToLower(text)
	start := strings.Index(lower, "[dungeon]")
	if start < 0 {
		return nil
	}
	end := strings.Index(lower[start:], "[/dungeon]")
	if end < 0 {
		end = len(lower) - start
	}
	body := text[start : start+end]
	if progress := strings.Index(strings.ToLower(body), "[in progress]"); progress >= 0 {
		body = body[:progress]
	}
	var numbers []int64
	for _, field := range strings.Fields(body) {
		n, e := strconv.ParseInt(strings.Trim(field, "`"), 10, 64)
		if e == nil {
			numbers = append(numbers, n)
		}
	}
	seen := map[uint32]bool{}
	var ids []uint32
	for i := 0; i+1 < len(numbers); i += 2 {
		if numbers[i] <= 0 || numbers[i] > 0xffffffff {
			continue
		}
		id := uint32(numbers[i])
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func ImportWorld(a *pvf.Archive) (WorldCatalog, error) {
	w := WorldCatalog{Source: a.Snapshot(), Areas: map[string]WorldArea{}}
	var e error
	w.TownIndex, e = ReadScript(a, "list/town.lst")
	if e != nil {
		return w, e
	}
	if e = importWorldmaps(a, &w); e != nil {
		return w, e
	}
	list, e := ParseIndex(w.TownIndex.Cells)
	if e != nil {
		return w, e
	}
	for _, t := range list {
		s, e := ReadScript(a, t.Path)
		if e != nil {
			return w, e
		}
		w.Towns = append(w.Towns, s)
		areas, e := parseWorldAreas(t.ID, s.Cells)
		if e != nil {
			return w, e
		}
		for _, area := range areas {
			// Newer source entries already contain a root-qualified Contents/,
			// CommonMap/ or Live/ path; older ones are relative to Map/.
			rootPath := strings.TrimPrefix(area.MapPath, "map/")
			if f, ok := a.FindFile(rootPath); ok {
				area.MapPath = f.ArchivePath
			}
			key := AreaKey(area.Town, area.Area)
			if _, ok := w.Areas[key]; ok {
				return w, fmt.Errorf("duplicate area %s", key)
			}
			area.Map, e = ResolveScript(a, area.MapPath)
			if e != nil {
				area.Pending = append(area.Pending, e.Error())
				w.Areas[key] = area
				continue
			}
			cells := area.Map.Cells
			for i, c := range cells {
				if c.Type == 3 && c.Text == "[is seria room warp]" && i+1 < len(cells) && cells[i+1].Type == 0 && cells[i+1].Value == 1 {
					for j := i - 1; j >= 0; j-- {
						if cells[j].Type == 3 && cells[j].Text == "[movable area]" {
							break
						}
						if cells[j].Type == 3 && cells[j].Text == "[area]" && j+4 < i {
							row, e := sourceRectangles(cells[j+1:j+5], 4)
							if e == nil && len(row) == 1 {
								area.SeriaReturnWarp = true
								area.ReturnWarpBounds = append(area.ReturnWarpBounds, [4]int32{row[0][0], row[0][1], row[0][2], row[0][3]})
							}
							break
						}
					}
				}
			}
			imports, importErr := resolveMapImports(a, area.Map, map[string]bool{}, 0)
			if importErr != nil {
				area.Pending = append(area.Pending, importErr.Error())
			} else {
				area.ImportedScripts = imports
				for _, imported := range imports {
					cells = append(append([]pvf.Token(nil), cells...), imported.Cells...)
				}
			}
			walk, e := sourceRectangles(sectionCells(cells, "[virtual movable area]"), 4)
			if e != nil {
				area.Pending = append(area.Pending, e.Error())
			} else {
				for _, r := range walk {
					area.Walkable = append(area.Walkable, [4]int32{r[0], r[1], r[2], r[3]})
				}
			}
			portals, e := sourceRectangles(sectionCells(cells, "[town movable area]"), 6)
			if e != nil {
				area.Pending = append(area.Pending, e.Error())
			} else {
				for _, r := range portals {
					if r[4] < 0 || r[5] < 0 {
						area.Pending = append(area.Pending, "dynamic portal destination")
						continue
					}
					area.Portals = append(area.Portals, Portal{[4]int32{r[0], r[1], r[2], r[3]}, uint32(r[4]), uint32(r[5])})
				}
			}
			for _, c := range sectionCells(area.Map.Cells, "[import script]") {
				if c.Type == 6 {
					area.Imports = append(area.Imports, c.Text)
				} else {
					area.Pending = append(area.Pending, "unresolved import cell")
				}
			}
			if len(area.Walkable) == 0 {
				area.Pending = append(area.Pending, "no resolved walkable rectangles")
			}
			augmentAreaGeometry(&area)
			w.Areas[key] = area
		}
	}
	w.DungeonIndex, e = ReadScript(a, "list/dungeon.lst")
	if e != nil {
		return w, e
	}
	w.Dungeons, e = ParseIndex(w.DungeonIndex.Cells)
	return w, e
}

func resolveMapImports(a *pvf.Archive, script ScriptRecord, visiting map[string]bool, depth int) ([]ScriptRecord, error) {
	if visiting[script.Path] || depth > 16 {
		return nil, fmt.Errorf("cyclic or excessive map imports at %s", script.Path)
	}
	visiting[script.Path] = true
	defer delete(visiting, script.Path)
	var out []ScriptRecord
	for _, cell := range sectionCells(script.Cells, "[import script]") {
		if cell.Type != 6 {
			return nil, fmt.Errorf("unsupported map import cell")
		}
		name := strings.ToLower(strings.ReplaceAll(cell.Text, "\\", "/"))
		if _, ok := a.FindFile(name); !ok {
			name = "map/" + strings.TrimPrefix(name, "map/")
		}
		child, e := ResolveScript(a, name)
		if e != nil {
			return nil, e
		}
		more, e := resolveMapImports(a, child, visiting, depth+1)
		if e != nil {
			return nil, e
		}
		out = append(out, child)
		out = append(out, more...)
	}
	return out, nil
}

func LoadWorld(file string) (WorldCatalog, error) {
	var w WorldCatalog
	b, e := os.ReadFile(file)
	if e != nil {
		return w, e
	}
	if e = json.Unmarshal(b, &w); e != nil {
		return w, e
	}
	if len(w.Source.Checksum) != 64 || len(w.Areas) == 0 {
		return w, fmt.Errorf("invalid world catalog")
	}
	for key, area := range w.Areas {
		if key != AreaKey(area.Town, area.Area) {
			return w, fmt.Errorf("world area key mismatch")
		}
		augmentAreaGeometry(&area)
		w.Areas[key] = area
	}
	return w, nil
}

// augmentAreaGeometry recovers walkable rectangles and town portals from the
// map's raw script tokens that sectionCells previously truncated at nested
// [quest condition] / [check condition] / [in progress] tags, plus animated
// portals from [town movable area with animation] (such as Hendon Myre and
// West Coast Seria's Room doors).
func augmentAreaGeometry(area *WorldArea) {
	if area == nil || len(area.Map.Cells) == 0 {
		return
	}
	cells := area.Map.Cells
	if len(area.ImportedScripts) > 0 {
		combined := make([]pvf.Token, 0, len(cells))
		combined = append(combined, cells...)
		for _, imp := range area.ImportedScripts {
			combined = append(combined, imp.Cells...)
		}
		cells = combined
	}
	seenWalk := make(map[[4]int32]bool, len(area.Walkable))
	for _, r := range area.Walkable {
		seenWalk[r] = true
	}
	for _, r := range parseSectionRectsSkippingNested(cells, "[virtual movable area]", "[/virtual movable area]", 4) {
		if r[2] < 0 || r[3] < 0 {
			continue
		}
		rect := [4]int32{r[0], r[1], r[2], r[3]}
		if !seenWalk[rect] {
			seenWalk[rect] = true
			area.Walkable = append(area.Walkable, rect)
		}
	}
	seenPortal := make(map[Portal]bool, len(area.Portals))
	for _, p := range area.Portals {
		seenPortal[p] = true
	}
	for _, r := range parseSectionRectsSkippingNested(cells, "[town movable area]", "[/town movable area]", 6) {
		if r[2] < 0 || r[3] < 0 || r[4] < 0 || r[5] < 0 {
			continue
		}
		p := Portal{Bounds: [4]int32{r[0], r[1], r[2], r[3]}, Town: uint32(r[4]), Area: uint32(r[5])}
		if !seenPortal[p] {
			seenPortal[p] = true
			area.Portals = append(area.Portals, p)
		}
	}
	for _, p := range parseAnimatedPortals(cells) {
		if !seenPortal[p] {
			seenPortal[p] = true
			area.Portals = append(area.Portals, p)
		}
	}
}

func parseSectionRectsSkippingNested(cells []pvf.Token, startTag, endTag string, width int) [][]int32 {
	var out [][]int32
	on := false
	nestedEnd := ""
	nums := make([]int32, 0, width)
	for _, c := range cells {
		if c.Type == 3 {
			switch {
			case c.Text == startTag:
				on = true
				nestedEnd = ""
				nums = nums[:0]
			case c.Text == endTag:
				on = false
				nestedEnd = ""
				nums = nums[:0]
			case on && nestedEnd != "" && c.Text == nestedEnd:
				nestedEnd = ""
			case on && nestedEnd == "" && strings.HasPrefix(c.Text, "[") && !strings.HasPrefix(c.Text, "[/"):
				nestedEnd = "[/" + c.Text[1:]
				nums = nums[:0]
			}
			continue
		}
		if on && nestedEnd == "" && c.Type == 0 {
			nums = append(nums, c.Value)
			if len(nums) == width {
				row := make([]int32, width)
				copy(row, nums)
				out = append(out, row)
				nums = nums[:0]
			}
		}
	}
	return out
}

func parseAnimatedPortals(cells []pvf.Token) []Portal {
	var out []Portal
	inAnim := false
	inMove := false
	hasRect := false
	hasDest := false
	var rect [4]int32
	var destTown, destArea uint32
	for i := 0; i < len(cells); i++ {
		c := cells[i]
		if c.Type != 3 {
			continue
		}
		switch c.Text {
		case "[town movable area with animation]":
			inAnim = true
		case "[/town movable area with animation]":
			inAnim = false
			inMove = false
		case "[movable area]":
			if inAnim {
				inMove = true
				hasRect = false
				hasDest = false
			}
		case "[/movable area]":
			if inMove && hasRect && hasDest {
				out = append(out, Portal{Bounds: rect, Town: destTown, Area: destArea})
			}
			inMove = false
		case "[area]":
			if inMove && i+4 < len(cells) &&
				cells[i+1].Type == 0 && cells[i+2].Type == 0 && cells[i+3].Type == 0 && cells[i+4].Type == 0 &&
				cells[i+3].Value >= 0 && cells[i+4].Value >= 0 {
				rect = [4]int32{cells[i+1].Value, cells[i+2].Value, cells[i+3].Value, cells[i+4].Value}
				hasRect = true
			}
		case "[move town]":
			if inMove && i+2 < len(cells) &&
				cells[i+1].Type == 0 && cells[i+2].Type == 0 &&
				cells[i+1].Value >= 0 && cells[i+2].Value >= 0 {
				destTown, destArea = uint32(cells[i+1].Value), uint32(cells[i+2].Value)
				hasDest = true
			}
		}
	}
	return out
}
