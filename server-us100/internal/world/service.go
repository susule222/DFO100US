package world

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"errors"
	"fmt"
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
	return state, s.ValidatePosition(level, state.Position, QuestAccess{account, id})
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
