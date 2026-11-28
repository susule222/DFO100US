package world

import (
	"dfolan/internal/catalog"
	"dfolan/internal/storage"
)

func (s *Service) HasNPC(p storage.WorldPosition, id uint32) bool {
	_, ok := s.NPCPosition(p, id)
	return ok
}

func (s *Service) HasArea(p storage.WorldPosition) bool {
	if s == nil {
		return false
	}
	_, ok := s.Catalog.Areas[catalog.AreaKey(p.Town, p.Area)]
	return ok
}

// CatalogHasNPC reports whether id appears in any base-phase area's [NPC] list.
// Quest-phased NPCs (introduced only on [phase] alternate maps, such as Lv.82+
// and Lv.95-100 scenario NPCs) return false so CMD 33 handlers can distinguish
// wrong-area spoof checks from phase-only NPCs.
func (s *Service) CatalogHasNPC(id uint32) bool {
	if s == nil || id == 0 {
		return false
	}
	for _, a := range s.Catalog.Areas {
		if _, ok := findNPCInArea(a, id); ok {
			return true
		}
	}
	return false
}

// NPCPosition returns where a source NPC stands in this area. A source [NPC]
// row is five cells: identity, facing tag, x, y and a trailing flag — read
// against town38/area0 NPC1 "[left] 1227 164 0" and town40/area0 NPC358
// "[left] 2249 148 0". Only complete, well-formed rows are accepted.
func (s *Service) NPCPosition(p storage.WorldPosition, id uint32) ([2]uint16, bool) {
	if s == nil {
		return [2]uint16{}, false
	}
	a, ok := s.Catalog.Areas[catalog.AreaKey(p.Town, p.Area)]
	if !ok || id == 0 {
		return [2]uint16{}, false
	}
	return findNPCInArea(a, id)
}

func findNPCInArea(a catalog.WorldArea, id uint32) ([2]uint16, bool) {
	for _, script := range append([]catalog.ScriptRecord{a.Map}, a.ImportedScripts...) {
		c := script.Cells
		for i := 0; i < len(c); i++ {
			if c[i].Type != 3 || c[i].Text != "[NPC]" {
				continue
			}
			for i++; i < len(c) && c[i].Type != 3; {
				if i+4 < len(c) && c[i].Type == 0 && c[i+1].Type == 6 && c[i+2].Type == 0 && c[i+3].Type == 0 && c[i+4].Type == 0 {
					if c[i].Value > 0 && uint32(c[i].Value) == id {
						x, y := c[i+2].Value, c[i+3].Value
						if x < 0 || y < 0 || x > 65535 || y > 65535 {
							return [2]uint16{}, false
						}
						return [2]uint16{uint16(x), uint16(y)}, true
					}
					i += 5
					continue
				}
				i++
			}
		}
	}
	return [2]uint16{}, false
}
