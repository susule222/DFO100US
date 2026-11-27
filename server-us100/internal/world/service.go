package world

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"errors"
	"fmt"
	"path"
	"strings"
)

type Rules struct {
	RequirePortalProximity bool   `json:"require_portal_proximity"`
	PortalMargin           uint16 `json:"portal_margin"`
}
type Service struct {
	Store   *storage.Store
	Catalog catalog.WorldCatalog
	Rules   Rules
}

var ErrLevel = errors.New("destination level requirement not met")

func Contains(r [4]int32, x, y uint16, margin uint16) bool {
	px, py, m := int64(x), int64(y), int64(margin)
	return r[2] >= 0 && r[3] >= 0 && px >= int64(r[0])-m && py >= int64(r[1])-m && px <= int64(r[0])+int64(r[2])+m && py <= int64(r[1])+int64(r[3])+m
}
func Walkable(a catalog.WorldArea, x, y uint16) bool {
	for _, r := range a.Walkable {
		if Contains(r, x, y, 0) {
			return true
		}
	}
	return false
}

type QuestAccess struct{ Account, Character int64 }

func (s *Service) ValidatePosition(level byte, p storage.WorldPosition, owner ...QuestAccess) error {
	a, ok := s.Catalog.Areas[catalog.AreaKey(p.Town, p.Area)]
	if !ok {
		return errors.New("unknown area")
	}
	if uint32(level) < a.MinimumLevel {
		return ErrLevel
	}
	checkQuests := false
	for _, pending := range a.Pending {
		switch pending {
		case "unsupported permission [need quest]", "unsupported permission [/need quest]", "unsupported permission [need quest or]", "unsupported permission [/need quest or]", "unsupported permission [in progress quest]", "unsupported permission [/in progress quest]":
			checkQuests = true
			continue
		}
		// An unresolved outgoing dynamic portal is never selectable in the
		// catalog's authorized edge set; it does not invalidate known geometry.
		if pending != "dynamic portal destination" {
			return fmt.Errorf("area configuration unresolved: %s", pending)
		}
	}
	if !Walkable(a, p.X, p.Y) {
		return errors.New("position outside source walkable rectangles")
	}
	if checkQuests {
		if len(owner) != 1 || s.Store == nil {
			return errors.New("area requires owned quest ledger")
		}
		rows, e := s.Store.Quests(context.Background(), owner[0].Account, owner[0].Character)
		if e != nil {
			return e
		}
		status := map[uint16]string{}
		for _, q := range rows {
			if q.ConfigVersion == s.Catalog.Source.Checksum {
				status[q.ID] = q.Status
			}
		}
		if e = checkSourceAreaQuests(a, status); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) Transition(level byte, old storage.WorldPosition, r protocol.AreaChangeRequest, owner ...QuestAccess) (storage.WorldPosition, error) {
	next := old
	if r.PreviousTown != old.Town || uint32(r.PreviousArea) != old.Area {
		return next, errors.New("stale source area")
	}
	next.Town, next.Area, next.X, next.Y = r.Town, r.Area, r.X, r.Y
	dest, exists := s.Catalog.Areas[catalog.AreaKey(r.Town, r.Area)]
	if !exists {
		return old, errors.New("unknown destination area")
	}
	if uint32(level) < dest.MinimumLevel {
		return old, ErrLevel
	}
	src, ok := s.Catalog.Areas[catalog.AreaKey(old.Town, old.Area)]
	if !ok {
		return old, errors.New("unknown source area")
	}
	adjacent := false
	for _, p := range src.Portals {
		if p.Town == r.Town && p.Area == r.Area && (!s.Rules.RequirePortalProximity || Contains(p.Bounds, old.X, old.Y, s.Rules.PortalMargin)) {
			adjacent = true
		}
	}
	// A source Seria return warp targets the saved origin, never an arbitrary
	// client-selected map. Its animated portal condition remains explicit.
	if old.Return != nil && src.SeriaReturnWarp && old.Return.Town == r.Town && old.Return.Area == r.Area {
		if !s.Rules.RequirePortalProximity {
			adjacent = true
		} else {
			for _, rect := range src.ReturnWarpBounds {
				if Contains(rect, old.X, old.Y, s.Rules.PortalMargin) {
					adjacent = true
				}
			}
		}
		if adjacent {
			// The Seria map selector may supply its generic animation landing
			// point (live17: 746,157), which is outside this source town's
			// walkable geometry. Restore this character's validated saved
			// origin instead. Destination/portal ownership is still checked.
			next.X, next.Y = old.Return.X, old.Return.Y
			next.Return = nil
		}
	}
	if !adjacent {
		return old, errors.New("no authorized source portal to destination")
	}
	if e := s.ValidatePosition(level, next, owner...); e != nil {
		return old, e
	}
	if dest.SeriaReturnWarp {
		next.Return = &storage.WorldReturn{Town: old.Town, Area: old.Area, X: old.X, Y: old.Y}
	}
	return next, nil
}
func (s *Service) Enter(ctx context.Context, account, id int64, level byte, spawn storage.WorldPosition) (storage.WorldState, error) {
	if e := s.ValidatePosition(level, spawn, QuestAccess{account, id}); e != nil {
		return storage.WorldState{}, e
	}
	state, e := s.Store.LoadWorld(ctx, account, id, spawn, s.Catalog.Source.Checksum)
	if e != nil {
		return state, e
	}
	if state.ConfigVersion != s.Catalog.Source.Checksum {
		// The row was created against an older archive. Saved coordinates are
		// only meaningful under the geometry that validated them, so they are
		// not reinterpreted: the validated spawn becomes the migrated position
		// and the row is rebound to the current source.
		if e = s.Store.SaveWorldWithVersion(ctx, account, id, spawn, s.Catalog.Source.Checksum); e != nil {
			return state, e
		}
		state.Position = spawn
		state.ConfigVersion = s.Catalog.Source.Checksum
		state.Revision++
	}
	// Every town entry places the character in the Seria's Room belonging to
	// their current town/map (or the shared Seria's Room 38/1 with Return
	// pointing back to their saved map when that town has no dedicated gate map).
	if target, changed := s.SeriaRoomEntry(level, state.Position, spawn, QuestAccess{account, id}); changed {
		saved, saveErr := s.Store.SaveWorld(ctx, account, id, state, target)
		if saveErr != nil {
			return state, saveErr
		}
		state = saved
	}
	return state, s.ValidatePosition(level, state.Position, QuestAccess{account, id})
}

// SeriaRoomEntry resolves the Seria's Room spawn position corresponding to cur's
// map/town. Towns with their own dedicated Seria/gate room (e.g. 38/1, 1/0, 4/0,
// 31/0, 47/0, 60/0, 83/0, 98/0) land directly in that town's Seria room; towns
// without a separate Seria room map (e.g. Hendon Myre 39, West Coast 40,
// Alfhlyra 41, Underfoot 42, Storm Pass 43) land in the shared Seria's Room
// (38/1) with Return set to cur so exiting the door returns to that exact map.
func (s *Service) SeriaRoomEntry(level byte, cur, fallbackSpawn storage.WorldPosition, owner ...QuestAccess) (storage.WorldPosition, bool) {
	if curArea, ok := s.Catalog.Areas[catalog.AreaKey(cur.Town, cur.Area)]; ok && isSeriaRoomArea(curArea) {
		next := cur
		if !Walkable(curArea, next.X, next.Y) || inReturnWarpBounds(curArea, next.X, next.Y) {
			if sx, sy, ok := seriaSpawnPoint(curArea); ok {
				next.X, next.Y = sx, sy
			}
		}
		if curArea.SeriaReturnWarp && !s.validReturnTarget(level, next.Return, owner...) {
			next.Return = s.seriaReturnOrigin(cur, curArea, fallbackSpawn, level, owner...)
		}
		if s.ValidatePosition(level, next, owner...) == nil {
			return next, !sameWorldPosition(cur, next)
		}
		return cur, false
	}

	// 1. Prefer a dedicated Seria's Room inside the character's own town.
	if own, ok := s.findTownSeriaRoom(cur.Town); ok {
		if cand, ok := s.buildSeriaPosition(level, cur, own, fallbackSpawn, owner...); ok {
			return cand, !sameWorldPosition(cur, cand)
		}
	}

	// 2. Otherwise use the shared SeriaReturnWarp room (e.g. Elvengard 38/1)
	//    with Return bound to the character's current map/town.
	if shared, ok := s.findSharedSeriaRoom(); ok {
		if cand, ok := s.buildSeriaPosition(level, cur, shared, fallbackSpawn, owner...); ok {
			return cand, !sameWorldPosition(cur, cand)
		}
	}
	return cur, false
}

func (s *Service) buildSeriaPosition(level byte, cur storage.WorldPosition, seria catalog.WorldArea, fallbackSpawn storage.WorldPosition, owner ...QuestAccess) (storage.WorldPosition, bool) {
	sx, sy, ok := seriaSpawnPoint(seria)
	if !ok {
		return storage.WorldPosition{}, false
	}
	cand := storage.WorldPosition{Town: seria.Town, Area: seria.Area, X: sx, Y: sy}
	if seria.SeriaReturnWarp {
		cand.Return = s.seriaReturnOrigin(cur, seria, fallbackSpawn, level, owner...)
	}
	if s.ValidatePosition(level, cand, owner...) != nil {
		return storage.WorldPosition{}, false
	}
	return cand, true
}

func (s *Service) validReturnTarget(level byte, ret *storage.WorldReturn, owner ...QuestAccess) bool {
	if ret == nil {
		return false
	}
	return s.ValidatePosition(level, storage.WorldPosition{Town: ret.Town, Area: ret.Area, X: ret.X, Y: ret.Y}, owner...) == nil
}

func (s *Service) seriaReturnOrigin(cur storage.WorldPosition, seria catalog.WorldArea, fallbackSpawn storage.WorldPosition, level byte, owner ...QuestAccess) *storage.WorldReturn {
	// If cur is outside the Seria room:
	// - For another town (e.g. Hendon Myre 39/0, West Coast 40/0), return directly to cur.
	// - For the same town (e.g. Elvengard 38/2 dungeon gate), prefer the area in that
	//   town that has a portal into seria (38/0) so exiting Seria's door lands right
	//   outside Seria's gate instead of inside a dungeon gate.
	if cur.Town != seria.Town || cur.Area != seria.Area {
		if cur.Town == seria.Town {
			if curArea, ok := s.Catalog.Areas[catalog.AreaKey(cur.Town, cur.Area)]; ok && areaHasPortalTo(curArea, seria.Town, seria.Area) {
				if s.ValidatePosition(level, cur, owner...) == nil {
					return &storage.WorldReturn{Town: cur.Town, Area: cur.Area, X: cur.X, Y: cur.Y}
				}
			}
			if gateRet := s.townSeriaGateOrigin(seria, fallbackSpawn, level, owner...); gateRet != nil {
				return gateRet
			}
		}
		if s.ValidatePosition(level, cur, owner...) == nil {
			return &storage.WorldReturn{Town: cur.Town, Area: cur.Area, X: cur.X, Y: cur.Y}
		}
	}
	if gateRet := s.townSeriaGateOrigin(seria, fallbackSpawn, level, owner...); gateRet != nil {
		return gateRet
	}
	if fallbackSpawn.Town != seria.Town || fallbackSpawn.Area != seria.Area {
		if s.ValidatePosition(level, fallbackSpawn, owner...) == nil {
			return &storage.WorldReturn{Town: fallbackSpawn.Town, Area: fallbackSpawn.Area, X: fallbackSpawn.X, Y: fallbackSpawn.Y}
		}
	}
	return nil
}

func (s *Service) townSeriaGateOrigin(seria catalog.WorldArea, fallbackSpawn storage.WorldPosition, level byte, owner ...QuestAccess) *storage.WorldReturn {
	if fallbackSpawn.Town == seria.Town && fallbackSpawn.Area != seria.Area {
		if a, ok := s.Catalog.Areas[catalog.AreaKey(fallbackSpawn.Town, fallbackSpawn.Area)]; ok && areaHasPortalTo(a, seria.Town, seria.Area) {
			if s.ValidatePosition(level, fallbackSpawn, owner...) == nil {
				return &storage.WorldReturn{Town: fallbackSpawn.Town, Area: fallbackSpawn.Area, X: fallbackSpawn.X, Y: fallbackSpawn.Y}
			}
		}
	}
	var best *catalog.WorldArea
	for _, a := range s.Catalog.Areas {
		if a.Town != seria.Town || a.Area == seria.Area || !areaHasPortalTo(a, seria.Town, seria.Area) {
			continue
		}
		aCopy := a
		if best == nil || aCopy.Area < best.Area {
			best = &aCopy
		}
	}
	if best != nil {
		for _, p := range best.Portals {
			if p.Town == seria.Town && p.Area == seria.Area {
				if x, y, ok := nearestWalkable(*best, p.Bounds[0]+p.Bounds[2]/2, p.Bounds[1]+p.Bounds[3]/2); ok {
					pos := storage.WorldPosition{Town: best.Town, Area: best.Area, X: x, Y: y}
					if s.ValidatePosition(level, pos, owner...) == nil {
						return &storage.WorldReturn{Town: pos.Town, Area: pos.Area, X: pos.X, Y: pos.Y}
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) findTownSeriaRoom(town uint32) (catalog.WorldArea, bool) {
	var best catalog.WorldArea
	found := false
	for _, a := range s.Catalog.Areas {
		if a.Town != town || !isSeriaRoomArea(a) {
			continue
		}
		if !found || preferSeriaArea(a, best) {
			best = a
			found = true
		}
	}
	return best, found
}

func (s *Service) findSharedSeriaRoom() (catalog.WorldArea, bool) {
	// Prefer Elvengard 38/1 (universal Seria's Room) when present.
	if a, ok := s.Catalog.Areas[catalog.AreaKey(38, 1)]; ok && a.SeriaReturnWarp && len(a.Walkable) > 0 {
		return a, true
	}
	var best catalog.WorldArea
	found := false
	for _, a := range s.Catalog.Areas {
		if !a.SeriaReturnWarp || len(a.Walkable) == 0 {
			continue
		}
		if !found || a.MinimumLevel < best.MinimumLevel || (a.MinimumLevel == best.MinimumLevel && (a.Town < best.Town || (a.Town == best.Town && a.Area < best.Area))) {
			best = a
			found = true
		}
	}
	return best, found
}

func preferSeriaArea(cand, cur catalog.WorldArea) bool {
	if cand.SeriaReturnWarp != cur.SeriaReturnWarp {
		return cand.SeriaReturnWarp
	}
	if (cand.Kind == "[gate]") != (cur.Kind == "[gate]") {
		return cand.Kind == "[gate]"
	}
	return cand.Area < cur.Area
}

func isSeriaRoomArea(a catalog.WorldArea) bool {
	if len(a.Walkable) == 0 {
		return false
	}
	if a.SeriaReturnWarp {
		return true
	}
	mp := strings.ToLower(strings.ReplaceAll(a.MapPath, "\\", "/"))
	base := path.Base(mp)
	if strings.Contains(mp, "seria") {
		return true
	}
	if strings.HasPrefix(base, "gate_") || strings.HasPrefix(base, "(f)gate_") || base == "gate.map" {
		return true
	}
	if a.Kind == "[gate]" {
		r := a.Walkable[0]
		if r[0] >= 350 && r[0] <= 520 && r[1] >= 110 && r[1] <= 210 && r[2] >= 140 && r[2] <= 200 && r[3] >= 150 && r[3] <= 240 {
			return true
		}
	}
	return false
}

func areaHasPortalTo(a catalog.WorldArea, town, area uint32) bool {
	for _, p := range a.Portals {
		if p.Town == town && p.Area == area {
			return true
		}
	}
	return false
}

func inReturnWarpBounds(a catalog.WorldArea, x, y uint16) bool {
	for _, rw := range a.ReturnWarpBounds {
		if Contains(rw, x, y, 0) {
			return true
		}
	}
	return false
}

func seriaSpawnPoint(a catalog.WorldArea) (uint16, uint16, bool) {
	if len(a.Walkable) == 0 {
		return 0, 0, false
	}
	r := a.Walkable[0]
	if r[2] < 0 || r[3] < 0 {
		return 0, 0, false
	}
	x := r[0] + r[2]/2
	y := r[1] + (r[3]*2)/3
	// Keep spawn cleanly above any bottom return-warp trigger rectangle.
	for _, rw := range a.ReturnWarpBounds {
		if y >= rw[1] && rw[1]-12 >= r[1] {
			y = rw[1] - 12
		}
	}
	if x >= 0 && y >= 0 && x <= 65535 && y <= 65535 && Walkable(a, uint16(x), uint16(y)) {
		return uint16(x), uint16(y), true
	}
	cx, cy := r[0]+r[2]/2, r[1]+r[3]/2
	if cx >= 0 && cy >= 0 && cx <= 65535 && cy <= 65535 && Walkable(a, uint16(cx), uint16(cy)) {
		return uint16(cx), uint16(cy), true
	}
	return 0, 0, false
}

func nearestWalkable(a catalog.WorldArea, tx, ty int32) (uint16, uint16, bool) {
	if tx >= 0 && ty >= 0 && tx <= 65535 && ty <= 65535 && Walkable(a, uint16(tx), uint16(ty)) {
		return uint16(tx), uint16(ty), true
	}
	for _, r := range a.Walkable {
		if r[2] < 0 || r[3] < 0 {
			continue
		}
		x := tx
		if x < r[0] {
			x = r[0]
		} else if x > r[0]+r[2] {
			x = r[0] + r[2]
		}
		y := ty
		if y < r[1] {
			y = r[1]
		} else if y > r[1]+r[3] {
			y = r[1] + r[3]
		}
		if x >= 0 && y >= 0 && x <= 65535 && y <= 65535 && Walkable(a, uint16(x), uint16(y)) {
			return uint16(x), uint16(y), true
		}
	}
	return 0, 0, false
}

func sameWorldPosition(a, b storage.WorldPosition) bool {
	if a.Town != b.Town || a.Area != b.Area || a.X != b.X || a.Y != b.Y {
		return false
	}
	if (a.Return == nil) != (b.Return == nil) {
		return false
	}
	if a.Return != nil && *a.Return != *b.Return {
		return false
	}
	return true
}

func checkSourceAreaQuests(a catalog.WorldArea, status map[uint16]string) error {
	found := false
	for i := 0; i < len(a.Definition); i++ {
		t := a.Definition[i]
		if t.Type != 3 || (t.Text != "[need quest]" && t.Text != "[need quest or]" && t.Text != "[in progress quest]") {
			continue
		}
		found = true
		matched, count := false, 0
		end := "[/" + t.Text[1:]
		for i++; i < len(a.Definition) && a.Definition[i].Type != 3; i++ {
			v := a.Definition[i]
			if v.Type != 0 || v.Value <= 0 || v.Value >= 40000 {
				return fmt.Errorf("unsupported %s identity", t.Text)
			}
			want := "completed"
			if t.Text == "[in progress quest]" {
				want = "accepted"
			}
			ok := status[uint16(v.Value)] == want
			if !ok && t.Text != "[need quest or]" {
				return fmt.Errorf("area requires quest %d %s", v.Value, want)
			}
			matched = matched || ok
			count++
		}
		if i >= len(a.Definition) || a.Definition[i].Text != end || count == 0 || !matched {
			return fmt.Errorf("area quest condition not met: %s", t.Text)
		}
	}
	if !found {
		return fmt.Errorf("area lacks resolvable quest conditions")
	}
	return nil
}
