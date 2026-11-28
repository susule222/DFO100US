package catalog

import (
	"compress/gzip"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// BoosterAward describes one reward entry inside a PVF box ([booster],
// [booster selection], or [package data]).
type BoosterAward struct {
	Template      uint32 `json:"t"`
	Quantity      uint32 `json:"q"`
	Weight        uint32 `json:"w,omitempty"`
	Kind          string `json:"k,omitempty"`  // ""|"s"=stackable, "a"=avatar, "e"=equipment, "c"=creature
	StackableType string `json:"st,omitempty"` // PVF [stackable type] for non-loot-catalog stackables
	Durability    uint16 `json:"d,omitempty"`  // PVF [durability] for equipment rewards
}

// BoosterBox is a source-backed definition of one openable box stackable.
type BoosterBox struct {
	Mode       string                    `json:"m"`           // "sel"=booster selection, "bst"=booster groups, "pkg"=package data
	SelectNum  int                       `json:"n,omitempty"` // for "sel": 0=all in category, >=1=pick N
	Categories map[uint32][]BoosterAward `json:"c,omitempty"` // for "sel"
	Groups     [][]BoosterAward          `json:"g,omitempty"` // for "bst"/"pkg": one weighted pick per group (len=1 => guaranteed)
}

// SkinStorageItem maps a stackable item with [action type] `[add skin storage]`
// to its Skin Storage category (0=DamageFont, 1=SkillCutscene, 2=PartyFrame/PartyRequestFrame,
// 3=InstantEmoticon, 4=WeaponSkin, 6=PartyRequestFrame equip-only) and skin indices in list/skin.lst.
type SkinStorageItem struct {
	Category     byte     `json:"c"`
	Index        uint32   `json:"i"`
	Indices      []uint32 `json:"is,omitempty"`
	AccountBound bool     `json:"a,omitempty"`
}

// SkinIndices returns all skin IDs granted by this item (always at least 1 when valid).
func (s SkinStorageItem) SkinIndices() []uint32 {
	if len(s.Indices) > 0 {
		return s.Indices
	}
	if s.Index != 0 {
		return []uint32{s.Index}
	}
	return nil
}

// BoosterCatalog holds all openable box definitions extracted from a US100 PVF.
type BoosterCatalog struct {
	Source       pvf.ArchiveSnapshot        `json:"source"`
	Boxes        map[uint32]BoosterBox      `json:"boxes"`
	SkinItems    map[uint32]SkinStorageItem `json:"skin_items,omitempty"`
	CreatureEggs map[uint32]uint32          `json:"creature_eggs,omitempty"`
	TypeCounts   map[string]int             `json:"type_counts,omitempty"`
	BoxTagStats  map[string]int             `json:"box_tag_stats,omitempty"`
}

func LoadBoosters(filePath string) (*BoosterCatalog, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(filePath), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	var c BoosterCatalog
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return nil, err
	}
	if c.Boxes == nil {
		c.Boxes = map[uint32]BoosterBox{}
	}
	if c.SkinItems == nil {
		c.SkinItems = map[uint32]SkinStorageItem{}
	}
	if c.CreatureEggs == nil {
		c.CreatureEggs = map[uint32]uint32{}
	}
	return &c, nil
}

func SaveBoosters(filePath string, c *BoosterCatalog) error {
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if strings.HasSuffix(strings.ToLower(filePath), ".gz") {
		gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(gz).Encode(c); err != nil {
			gz.Close()
			return err
		}
		return gz.Close()
	}
	return json.NewEncoder(f).Encode(c)
}

// ImportBoosters scans every entry in list/stackable.lst from the US100 PVF and
// extracts all [booster selection], [booster], and [package data] box definitions,
// classifying every awarded template as avatar / equipment / stackable / creature.
func ImportBoosters(a *pvf.Archive) (*BoosterCatalog, error) {
	cat := &BoosterCatalog{
		Source:       a.Snapshot(),
		Boxes:        map[uint32]BoosterBox{},
		SkinItems:    map[uint32]SkinStorageItem{},
		CreatureEggs: map[uint32]uint32{},
		TypeCounts:   map[string]int{},
		BoxTagStats:  map[string]int{},
	}

	skinCatByID := map[uint32]byte{}
	if skinIdx, err := ResolveScript(a, "list/skin.lst"); err == nil {
		if skinRows, err := ParseIndex(skinIdx.Cells); err == nil {
			for _, r := range skinRows {
				low := strings.ToLower(r.Path)
				switch {
				case strings.Contains(low, "damagefont"):
					skinCatByID[r.ID] = 0
				case strings.Contains(low, "skillcutscene"):
					skinCatByID[r.ID] = 1
				case strings.Contains(low, "partyrequestframe"):
					skinCatByID[r.ID] = 6
				case strings.Contains(low, "partyframe"):
					skinCatByID[r.ID] = 2
				case strings.Contains(low, "instantemoticon"):
					skinCatByID[r.ID] = 3
				case strings.Contains(low, "weaponskin"):
					skinCatByID[r.ID] = 4
				}
			}
		}
	}

	stkIdx, err := ResolveScript(a, "list/stackable.lst")
	if err != nil {
		return nil, err
	}
	stkRows, err := ParseIndex(stkIdx.Cells)
	if err != nil {
		return nil, err
	}
	eqIdx, err := ResolveScript(a, "list/equipment.lst")
	if err != nil {
		return nil, err
	}
	eqRows, err := ParseIndex(eqIdx.Cells)
	if err != nil {
		return nil, err
	}

	stkPaths := make(map[uint32]string, len(stkRows))
	for _, r := range stkRows {
		p := r.Path
		if !strings.HasPrefix(p, "stackable/") {
			p = path.Join("stackable", p)
		}
		stkPaths[r.ID] = p
	}
	eqPaths := make(map[uint32]string, len(eqRows))
	for _, r := range eqRows {
		p := r.Path
		if !strings.HasPrefix(p, "equipment/") {
			p = path.Join("equipment", p)
		}
		eqPaths[r.ID] = p
	}

	// Extract all Creature Egg -> Hatched Creature mappings ([output index]) from list/equipment.lst.
	for id, p := range eqPaths {
		low := strings.ToLower(p)
		if !strings.Contains(low, "creature/") && !strings.Contains(low, "/creature") {
			continue
		}
		if s, err := ResolveScript(a, p); err == nil {
			if outCells := sectionCells(s.Cells, "[output index]"); len(outCells) > 0 && outCells[0].Type == 0 && outCells[0].Value > 0 {
				cat.CreatureEggs[id] = uint32(outCells[0].Value)
			}
		}
	}

	// Cache equipment metadata (avatar / talisman / creature / artifact / flag / ordinary equipment + durability) on demand.
	eqCache := map[uint32]boosterEquipMeta{}
	classifyEquip := func(id uint32) boosterEquipMeta {
		if m, ok := eqCache[id]; ok {
			return m
		}
		p, ok := eqPaths[id]
		if !ok {
			m := boosterEquipMeta{isAvatar: id >= 500000000 && id < 520000000}
			eqCache[id] = m
			return m
		}
		lower := strings.ToLower(p)
		isAv := strings.Contains(lower, "/avatar/") || strings.Contains(lower, "avatar/")
		isTal := strings.Contains(lower, "/talisman/") || strings.Contains(lower, "talisman/")
		var isCreature, isArtifact, isFlag, noDurability bool
		var dur uint16 = 35
		hasDur := false
		if s, err := ResolveScript(a, p); err == nil {
			if tc := sectionCells(s.Cells, "[equipment type]"); len(tc) > 0 {
				eqType := strings.ToLower(tc[0].Text)
				switch {
				case strings.Contains(eqType, "avatar"):
					isAv = true
				case eqType == "[talisman]":
					isTal = true
				case eqType == "[creature]":
					isCreature = true
				case eqType == "[artifact red]" || eqType == "[artifact blue]" || eqType == "[artifact green]":
					isArtifact = true
				case eqType == "[flag]":
					isFlag = true
				case eqType == "[title name]" || eqType == "[amulet]" || eqType == "[wrist]" || eqType == "[ring]" || eqType == "[support]" || eqType == "[magic stone]" || eqType == "[earring]" || eqType == "[support weapon]":
					noDurability = true
				}
			}
			if dc := sectionCells(s.Cells, "[durability]"); len(dc) > 0 && dc[0].Type == 0 && dc[0].Value >= 0 && dc[0].Value <= 65535 {
				dur = uint16(dc[0].Value)
				hasDur = true
			}
		}
		if isAv || isTal || isCreature || isArtifact || isFlag || (noDurability && !hasDur) {
			dur = 0
		}
		m := boosterEquipMeta{
			isAvatar:   isAv,
			isTalisman: isTal,
			isCreature: isCreature,
			isArtifact: isArtifact,
			isFlag:     isFlag,
			durability: dur,
		}
		eqCache[id] = m
		return m
	}

	// Cache stackable tab types from pass 1 so rewarded stackables know their exact tab.
	stkTypes := make(map[uint32]string, len(stkRows))

	ids := make([]uint32, 0, len(stkPaths))
	for id := range stkPaths {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		s, err := ResolveScript(a, stkPaths[id])
		if err != nil {
			continue
		}
		stType := ""
		if tc := sectionCells(s.Cells, "[stackable type]"); len(tc) > 0 && tc[0].Type == 6 {
			stType = tc[0].Text
		}
		if stType != "" {
			stkTypes[id] = stType
		}

		// Record [action type] `[add skin storage]` <skinID>... items (Damage Fonts, Cut-Scenes, Party Frames, Emoticons, Weapon Skins).
		if actCells := sectionCells(s.Cells, "[action type]"); len(actCells) >= 2 {
			for i := 0; i+1 < len(actCells); i++ {
				if actCells[i].Type == 6 && strings.EqualFold(actCells[i].Text, "[add skin storage]") {
					var skinIDs []uint32
					for j := i + 1; j < len(actCells); j++ {
						if actCells[j].Type != 0 {
							break
						}
						if actCells[j].Value > 0 {
							skinIDs = append(skinIDs, uint32(actCells[j].Value))
						}
					}
					if len(skinIDs) > 0 {
						firstID := skinIDs[0]
						skinCat, ok := skinCatByID[firstID]
						if !ok && hasSection(s.Cells, "[damage font info]") {
							skinCat = 0
							ok = true
						}
						if ok {
							acctBound := false
							if tgtCells := sectionCells(s.Cells, "[action target]"); len(tgtCells) > 0 && tgtCells[0].Type == 6 && strings.EqualFold(tgtCells[0].Text, "[account]") {
								acctBound = true
							}
							item := SkinStorageItem{
								Category:     skinCat,
								Index:        firstID,
								AccountBound: acctBound,
							}
							if len(skinIDs) > 1 {
								item.Indices = skinIDs
							}
							cat.SkinItems[id] = item
						}
					}
					break
				}
			}
		}

		// Detect box sections regardless of [stackable type] spelling.
		hasSel := hasSection(s.Cells, "[booster select category]")
		hasBst := hasSection(s.Cells, "[booster info]")
		hasPkg := hasSection(s.Cells, "[package data]")
		hasRand := hasSection(s.Cells, "[random item]") || hasSection(s.Cells, "[random]")

		if !hasSel && !hasBst && !hasPkg {
			if hasRand && (stType == "[booster]" || stType == "[random]" || stType == "[multi booster]") {
				cat.BoxTagStats["other:"+stType]++
			}
			continue
		}
		cat.TypeCounts[stType]++

		if hasSel {
			box := parseBoosterSelection(s.Cells, eqPaths, stkPaths, classifyEquip)
			if len(box.Categories) > 0 {
				cat.Boxes[id] = box
				cat.BoxTagStats["sel"]++
			}
			continue
		}
		if hasBst {
			box := parseBoosterInfo(s.Cells, eqPaths, stkPaths, classifyEquip)
			if len(box.Groups) > 0 {
				cat.Boxes[id] = box
				cat.BoxTagStats["bst"]++
			}
			continue
		}
		if hasPkg {
			box := parsePackageData(s.Cells, eqPaths, stkPaths, classifyEquip)
			if len(box.Groups) > 0 {
				cat.Boxes[id] = box
				cat.BoxTagStats["pkg"]++
			}
		}
	}

	// Backfill exact PVF [stackable type] onto every stackable reward entry.
	for id, box := range cat.Boxes {
		for cid, list := range box.Categories {
			for i := range list {
				if list[i].Kind == "" {
					if st := stkTypes[list[i].Template]; st != "" && st != "[throw]" && st != "[waste]" {
						list[i].StackableType = st
					}
				}
			}
			box.Categories[cid] = list
		}
		for gi, grp := range box.Groups {
			for i := range grp {
				if grp[i].Kind == "" {
					if st := stkTypes[grp[i].Template]; st != "" && st != "[throw]" && st != "[waste]" {
						grp[i].StackableType = st
					}
				}
			}
			box.Groups[gi] = grp
		}
		cat.Boxes[id] = box
	}

	return cat, nil
}

func hasSection(cells []pvf.Token, tag string) bool {
	for _, c := range cells {
		if c.Type == 3 && c.Text == tag {
			return true
		}
	}
	return false
}

// extractSubBlocks returns the token slice inside each <openTag>...</closeTag> pair.
func extractSubBlocks(cells []pvf.Token, openTag string) [][]pvf.Token {
	closeTag := "[/" + strings.TrimPrefix(openTag, "[")
	var out [][]pvf.Token
	for i := 0; i < len(cells); i++ {
		if cells[i].Type == 3 && cells[i].Text == openTag {
			j := i + 1
			for j < len(cells) && !(cells[j].Type == 3 && cells[j].Text == closeTag) {
				j++
			}
			out = append(out, cells[i+1:j])
			i = j
		}
	}
	return out
}

func intValues(cells []pvf.Token) []int32 {
	out := make([]int32, 0, len(cells))
	for _, c := range cells {
		if c.Type == 0 {
			out = append(out, c.Value)
		}
	}
	return out
}

type boosterEquipMeta struct {
	isAvatar   bool
	isTalisman bool
	isCreature bool
	isArtifact bool
	isFlag     bool
	durability uint16
}

func (m boosterEquipMeta) applyToAward(aw *BoosterAward) {
	switch {
	case m.isAvatar:
		aw.Kind = "a"
	case m.isTalisman:
		aw.Kind = "t"
	case m.isCreature:
		aw.Kind = "c"
	case m.isArtifact:
		aw.Kind = "ca"
	case m.isFlag:
		aw.Kind = "f"
	default:
		aw.Kind = "e"
		aw.Durability = m.durability
	}
}

// classifyAward resolves the authoritative PVF kind of a reward template.
// US100 list/equipment.lst and list/stackable.lst have zero ID overlap, whereas
// box tags ([avatar], [equipment], [creature], [etc], [cera], [stackable]) often
// mix categories (e.g., dyes/sub-boxes under [equipment]/[avatar], or auras/titles
// under [etc]). Always trust the PVF index over the box sub-tag when present.
func classifyAward(aw *BoosterAward, fallbackKind string, eqPaths, stkPaths map[uint32]string, classifyEquip func(uint32) boosterEquipMeta) {
	if _, isEq := eqPaths[aw.Template]; isEq {
		classifyEquip(aw.Template).applyToAward(aw)
		return
	}
	if _, isStk := stkPaths[aw.Template]; isStk {
		aw.Kind = ""
		aw.Durability = 0
		return
	}
	switch fallbackKind {
	case "a", "c":
		aw.Kind = fallbackKind
	case "e":
		classifyEquip(aw.Template).applyToAward(aw)
	}
}

func parseBoosterSelection(cells []pvf.Token, eqPaths, stkPaths map[uint32]string, classifyEquip func(uint32) boosterEquipMeta) BoosterBox {
	selNum := 1
	if sn := sectionCells(cells, "[booster selection num]"); len(sn) > 0 && sn[0].Type == 0 {
		selNum = int(sn[0].Value)
	}
	cats := map[uint32][]BoosterAward{}
	for _, blk := range extractSubBlocks(cells, "[booster select category]") {
		if len(blk) < 2 || blk[0].Type != 0 {
			continue
		}
		// Collect all leading category coordinate ints before the first section tag.
		// 1D boxes: [dim0] -> cid = dim0
		// 2D boxes (e.g. [booster category num] 2 16 5): [dim0, dim1] -> cid = dim0 | (dim1<<8)
		// Directly matches US100 CMD 160 wire bytes [6]=dim0, [7]=dim1 (LittleEndian uint16).
		var dims []uint32
		for _, tok := range blk {
			if tok.Type != 0 {
				break
			}
			dims = append(dims, uint32(tok.Value))
		}
		if len(dims) == 0 {
			continue
		}
		cid := dims[0]
		if len(dims) >= 2 {
			cid = (dims[0] & 0xff) | ((dims[1] & 0xff) << 8)
		}
		if len(dims) >= 3 {
			cid |= (dims[2] & 0xff) << 16
		}
		var items []BoosterAward
		// [avatar]: 4-tuples (template, qty, flag, param)
		for _, sub := range extractSubBlocks(blk, "[avatar]") {
			nums := intValues(sub)
			for i := 0; i+3 < len(nums); i += 4 {
				if nums[i] > 0 {
					q := uint32(1)
					if nums[i+1] > 0 {
						q = uint32(nums[i+1])
					}
					aw := BoosterAward{Template: uint32(nums[i]), Quantity: q}
					classifyAward(&aw, "a", eqPaths, stkPaths, classifyEquip)
					items = append(items, aw)
				}
			}
		}
		// [equipment]: 4-tuples (template, qty, upgrade/flag, param) or 2-tuples (template, qty).
		// Note: even-item 2-tuple lists have len(nums)%4 == 0, so also inspect nums[2]:
		// a template ID (>= 1000) at index 2 proves 2-tuple layout.
		for _, sub := range extractSubBlocks(blk, "[equipment]") {
			nums := intValues(sub)
			stride := 4
			if len(nums)%2 == 0 && (len(nums)%4 != 0 || (len(nums) >= 4 && nums[2] >= 1000)) {
				stride = 2
			}
			for i := 0; i+stride-1 < len(nums); i += stride {
				if nums[i] > 0 {
					tpl := uint32(nums[i])
					q := uint32(1)
					if nums[i+1] > 0 {
						q = uint32(nums[i+1])
					}
					aw := BoosterAward{Template: tpl, Quantity: q}
					classifyAward(&aw, "e", eqPaths, stkPaths, classifyEquip)
					items = append(items, aw)
				}
			}
		}
		// [etc], [stackable], [emblem], [cera]: 2-tuples (template, qty)
		// Many US100 boxes list titles, weapons, armors, avatars, creatures, talismans,
		// or insignias inside [etc]/[cera]; check eqPaths so they are never misrouted as stackables.
		for _, tag := range []string{"[etc]", "[stackable]", "[emblem]", "[cera]"} {
			for _, sub := range extractSubBlocks(blk, tag) {
				nums := intValues(sub)
				for i := 0; i+1 < len(nums); i += 2 {
					if nums[i] > 0 {
						tpl := uint32(nums[i])
						q := uint32(1)
						if nums[i+1] > 0 {
							q = uint32(nums[i+1])
						}
						aw := BoosterAward{Template: tpl, Quantity: q}
						classifyAward(&aw, "", eqPaths, stkPaths, classifyEquip)
						items = append(items, aw)
					}
				}
			}
		}
		// [creature]: 4-tuples (template, qty, ...) or 2-tuples
		for _, sub := range extractSubBlocks(blk, "[creature]") {
			nums := intValues(sub)
			stride := 4
			if len(nums)%2 == 0 && (len(nums)%4 != 0 || (len(nums) >= 4 && nums[2] >= 1000)) {
				stride = 2
			}
			for i := 0; i+stride-1 < len(nums); i += stride {
				if nums[i] > 0 {
					q := uint32(1)
					if nums[i+1] > 0 {
						q = uint32(nums[i+1])
					}
					aw := BoosterAward{Template: uint32(nums[i]), Quantity: q}
					classifyAward(&aw, "c", eqPaths, stkPaths, classifyEquip)
					items = append(items, aw)
				}
			}
		}
		if len(items) > 0 {
			cats[cid] = append(cats[cid], items...)
		}
	}
	return BoosterBox{Mode: "sel", SelectNum: selNum, Categories: cats}
}

func parseBoosterInfo(cells []pvf.Token, eqPaths, stkPaths map[uint32]string, classifyEquip func(uint32) boosterEquipMeta) BoosterBox {
	blocks := extractSubBlocks(cells, "[booster info]")
	if len(blocks) == 0 {
		return BoosterBox{}
	}
	var groups [][]BoosterAward
	for _, blk := range blocks {
		for _, tag := range []string{"[avatar]", "[equipment]", "[etc]", "[stackable]", "[emblem]", "[cera]", "[creature]"} {
			for _, sub := range extractSubBlocks(blk, tag) {
				nums := intValues(sub)
				if len(nums) < 4 {
					continue
				}
				// PVF [booster info] sub-block format:
				//   pickCount, then N tuples of (template, weight, quantity)
				//   ([avatar] may occasionally carry 5-tuples: template, weight, qty, period, flag)
				pickCount := int(nums[0])
				if pickCount <= 0 {
					pickCount = 1
				}
				if pickCount > 20 {
					pickCount = 20
				}
				rem := nums[1:]
				stride := 3
				if tag == "[avatar]" && len(rem) >= 5 && len(rem)%5 == 0 && len(rem)%3 != 0 {
					stride = 5
				}
				var entries []BoosterAward
				for i := 0; i+2 < len(rem); i += stride {
					tpl := rem[i]
					weight := rem[i+1]
					qty := rem[i+2]
					if tpl <= 0 || weight <= 0 {
						continue
					}
					if qty <= 0 {
						qty = 1
					}
					aw := BoosterAward{
						Template: uint32(tpl),
						Quantity: uint32(qty),
						Weight:   uint32(weight),
					}
					fallback := ""
					switch tag {
					case "[avatar]":
						fallback = "a"
					case "[equipment]":
						fallback = "e"
					case "[creature]":
						fallback = "c"
					}
					classifyAward(&aw, fallback, eqPaths, stkPaths, classifyEquip)
					entries = append(entries, aw)
				}
				if len(entries) > 0 {
					for p := 0; p < pickCount; p++ {
						groups = append(groups, entries)
					}
				}
			}
		}
	}
	return BoosterBox{Mode: "bst", Groups: groups}
}

func parsePackageData(cells []pvf.Token, eqPaths, stkPaths map[uint32]string, classifyEquip func(uint32) boosterEquipMeta) BoosterBox {
	blocks := extractSubBlocks(cells, "[package data]")
	if len(blocks) == 0 {
		return BoosterBox{}
	}
	var groups [][]BoosterAward
	for _, blk := range blocks {
		nums := intValues(blk)
		for i := 0; i+1 < len(nums); i += 2 {
			tpl := nums[i]
			qty := nums[i+1]
			if tpl <= 0 {
				continue
			}
			if qty <= 0 {
				qty = 1
			}
			id := uint32(tpl)
			aw := BoosterAward{Template: id, Quantity: uint32(qty), Weight: 1000}
			classifyAward(&aw, "", eqPaths, stkPaths, classifyEquip)
			groups = append(groups, []BoosterAward{aw})
		}
	}
	return BoosterBox{Mode: "pkg", Groups: groups}
}
