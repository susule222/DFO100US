package main

import (
	"context"
	"crypto/rand"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/dungeon"
	"dfolan/internal/game/protocol"
	"dfolan/internal/loot"
	"dfolan/internal/storage"
	"encoding/binary"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"
)

func randomSeed() (uint32, error) {
	var seed uint32
	e := binary.Read(rand.Reader, binary.LittleEndian, &seed)
	return seed, e
}

func (w *worldSession) dungeonGate(p []byte) ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("dungeon gate without selected character")
	}
	requested, e := protocol.DecodeDungeonGate(p)
	if e != nil {
		return nil, e
	}
	// The town gate sends zero. The client's own tutorial sender (146cce650)
	// supplies a source dungeon ID instead; accept that only when it is this
	// character's own starting route and the route is still owed.
	if requested != 0 {
		if e = w.authorizeTutorial(requested); e != nil {
			return nil, e
		}
		return w.enterSelectPlan(), nil
	}
	if e = w.checkTownGate(); e != nil {
		return nil, e
	}
	return w.enterSelectPlan(), nil
}

func (w *worldSession) checkTownGate() error {
	a, ok := w.service.Catalog.Areas[catalog.AreaKey(w.state.Position.Town, w.state.Position.Area)]
	if !ok || a.Kind != "[dungeon gate]" {
		return fmt.Errorf("source area is not a PVF dungeon gate")
	}
	return w.service.ValidatePosition(w.level, w.state.Position, w.questAccess())
}

// enterSelectPlan is the whole gate answer: the companions, ENTER_SELECT itself,
// and the party preload. Order is a gate-set switch because it is the last
// difference left against the reference whose set is otherwise identical.
//
// The reference's own comment explains why the order is load-bearing:
// "install one complete mode1 snapshot before USER_STATE changes the selector
// context" — i.e. S0/0003 is what switches the client into the select context,
// so the permission rows and the area must already be there when it arrives.
// This server has always sent 0003 first, before the rows.
func (w *worldSession) enterSelectPlan() []outboundPacket {
	parts := w.gatePackets()
	if w.gateSet.ReferenceOrder {
		// 0164 -> license/guild -> 0005 -> 0017 -> 0003 -> 001A -> 001B -> 0009
		plan := make([]outboundPacket, 0, 9)
		plan = append(plan, parts.gateAck...)
		plan = append(plan, parts.finishedQuests...)
		plan = append(plan, parts.license...)
		plan = append(plan, parts.guild...)
		plan = append(plan, parts.permissions...)
		plan = append(plan, parts.area...)
		plan = append(plan, parts.userState)
		plan = append(plan, parts.udpHost)
		plan = append(plan, parts.enterSelect...)
		plan = append(plan, parts.party...)
		return plan
	}
	// Proven order: 0003 -> 001A -> license/guild -> 0164 -> 0005 -> 0017 ->
	// 0009 -> 001B.
	plan := make([]outboundPacket, 0, 9)
	plan = append(plan, parts.gateAck...)
	plan = append(plan, parts.userState, parts.udpHost)
	plan = append(plan, parts.license...)
	plan = append(plan, parts.guild...)
	plan = append(plan, parts.finishedQuests...)
	plan = append(plan, parts.permissions...)
	plan = append(plan, parts.area...)
	plan = append(plan, parts.party...)
	plan = append(plan, parts.enterSelect...)
	return plan
}

// gatePacketParts is every packet the gate can answer with, before ordering.
type gatePacketParts struct {
	gateAck        []outboundPacket
	userState      outboundPacket
	udpHost        outboundPacket
	license        []outboundPacket
	guild          []outboundPacket
	finishedQuests []outboundPacket
	permissions    []outboundPacket
	area           []outboundPacket
	party          []outboundPacket
	enterSelect    []outboundPacket
}

// enterSelectBody encodes the current gate's optional keys for NOTI27.
func (w *worldSession) enterSelectBody() []byte {
	if !w.gateSet.CandidateList {
		return protocol.EnterDungeonSelection()
	}
	ids := w.selectWorldmapDungeons()
	if len(ids) > 8 {
		// The client's reader copies these into an eight-slot stack array.
		ids = ids[:8]
	}
	short := make([]uint16, 0, len(ids))
	for _, id := range ids {
		if id > 0 && id <= 0xffff {
			short = append(short, uint16(id))
		}
	}
	body, e := protocol.EnterDungeonSelectionUS100(short)
	if e != nil {
		log.Printf("dungeon enter-select candidate list unavailable: %v", e)
		return protocol.EnterDungeonSelection()
	}
	log.Printf("dungeon enter-select candidate list: %v", short)
	return body
}

// gatePackets builds the packets that accompany ENTER_SELECT when the dungeon
// gate is used: the actor's USER_STATE, the finished-quest list, the dungeon
// permission rows (what marks dungeons as selectable), and the optional packets
// in gate_set.go.
func (w *worldSession) gatePackets() gatePacketParts {
	// Minimal answer: what the 2026-09-18 06:24 run sent and the only retained
	// session where the selection screen opened. See gate_set.go's MinimalGate.
	if w.gateSet.MinimalGate {
		parts := gatePacketParts{}
		if w.gateSet.GateAck {
			parts.gateAck = []outboundPacket{{"dungeon_gate_ack", 1, 15, []byte{1}}}
		}
		if w.gateSet.EnterSelect {
			parts.enterSelect = []outboundPacket{{"dungeon_selection_sent", 0, 27, w.enterSelectBody()}}
		}
		parts.permissions = w.sourceGatePermissions()
		return parts
	}
	// The two license-dungeon packets (S0/02FD, S0/02FE) used to be sent for
	// every gate. The reference sends them only when the gate's worldmap is the
	// Dark Elf Ruins license worldmap — "select_worldmap_id ==
	// LICENSE_DUNGEON_WORLDMAP_ID and ..." — so for this gate (GranFloris,
	// worldmap 2) they were never part of the set. They are now derived from the
	// area instead of sent unconditionally, and likewise S0/02E6 for the guild
	// dungeon worldmap.
	parts := gatePacketParts{
		userState: outboundPacket{"dungeon_select_user_state", 0, 3, protocol.EnterSelectUserStateUS(w.role.WireID, byte(w.gateSet.EnterSelectState))},
		udpHost:   outboundPacket{"dungeon_udp_host_unavailable", 0, 26, protocol.UdpHostUnavailableUS()},
	}
	if w.gateSet.EnterSelect {
		parts.enterSelect = []outboundPacket{{"dungeon_selection_sent", 0, 27, w.enterSelectBody()}}
	} else {
		log.Printf("dungeon gate: enter_select disabled by the gate set; sending companions only")
	}
	if gate := w.gateWorldmap(); gate == licenseDungeonWorldmap {
		parts.license = []outboundPacket{
			{"dungeon_license_clear_info", 0, 766, protocol.LicenseDungeonClearInfoUS()},
			{"dungeon_license_permissions", 0, 765, protocol.LicenseDungeonPermissionUS()}}
	}
	if gate := w.gateWorldmap(); gate == guildDungeonWorldmap {
		parts.guild = []outboundPacket{{"dungeon_guild_repeat_bonus", 0, 742, protocol.GuildDungeonRepeatBonusUS()}}
	}
	if w.quests != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ids, e := w.quests.Completed(ctx, w.role)
		cancel()
		if e != nil {
			log.Printf("dungeon enter-select finished quests unavailable: %v", e)
		} else if p, e := protocol.ClearQuestListUS(ids); e != nil {
			log.Printf("dungeon enter-select 0164 encode failed: %v", e)
		} else {
			parts.finishedQuests = []outboundPacket{{"dungeon_clear_quests_sent", 0, 356, p}}
		}
	}
	// The permission rows must include the dungeons of THIS gate's map screen.
	// The reference records what happens otherwise — "Stage3 live: SilverCrown
	// town=1 area=2 dungeon-gate -> worldmap 21 Abnova, nodes 144..149 (+2009).
	// Sending only 1..32 yields empty Abnova UI" — and this server used to send
	// the tutorial set, which belongs to a different map screen entirely.
	//
	// The list is the union of the gate's own map screen and the whole source
	// dungeon index. The broad tail is not decoration: the selection screen last
	// opened when this list was the whole 2729-entry source table, and narrowing
	// it to one screen's ids is one of the two things that changed since.
	// Sending both covers the screen that needs its own nodes and the behaviour
	// that was observed working. Order does not matter on the wire — the client
	// keeps the highest state per dungeon — so the rows are sorted before they
	// are encoded, which is also why the sort below is not a bug.
	rows := make([]protocol.DungeonPermissionRow, 0, 64)
	seen := map[uint32]bool{}
	add := func(id uint32) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		rows = append(rows, protocol.DungeonPermissionRow{ID: id, State: 1})
	}
	worldmap := w.selectWorldmapDungeons()
	for _, id := range worldmap {
		add(id)
	}
	if w.service != nil {
		for _, d := range w.service.Catalog.Dungeons {
			add(d.ID)
		}
	}
	if len(rows) == 0 && w.tutorialDungeons != nil {
		ids := make([]uint32, 0, len(w.tutorialDungeons.Dungeons))
		for id := range w.tutorialDungeons.Dungeons {
			if id != 0 {
				ids = append(ids, uint32(id))
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			add(id)
		}
	}
	if len(rows) == 0 && w.dungeons != nil {
		for id := range w.dungeons.Dungeons {
			if id != 0 {
				add(uint32(id))
				break
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if w.dungeons != nil && w.dungeons.Gates != nil {
		parts.permissions = w.sourceGatePermissions()
		rows = nil
	}
	if len(rows) > 0 {
		p, e := protocol.DungeonPermissionUS(rows)
		if e != nil {
			log.Printf("dungeon permission encode failed: %v", e)
		} else {
			parts.permissions = []outboundPacket{{"dungeon_permission_sent", 0, 5, p}}
		}
	}
	// USER_AREA with area = 0xFF is what the reference sends between the
	// permission rows and ENTER_SELECT, and it documents it as the signal that
	// raises the dungeon selection screen: "Force area 0xFF for select UI (doc:
	// critical signal)". The actor keeps its real town position and the ordinary
	// leave path restores the real area.
	p := w.state.Position
	if w.gateSet.UserAreaSelect {
		if ua, e := protocol.UserArea(p.Town, 0xff, protocol.AreaUser{ActorServerID: w.role.WireID, X: p.X, Y: p.Y, Flags: w.flags}); e != nil {
			log.Printf("dungeon enter-select user area 0017 unavailable: %v", e)
		} else {
			parts.area = []outboundPacket{{"dungeon_select_user_area", 0, 23, ua}}
		}
	}
	if w.gateSet.PartyPreload {
		// S0/0009 with dungeon_id 0: the reference sends it while the selection
		// UI is up so the local party slot is already warm. Our packet is the
		// solo PARTY_INFO block; the select path also sends it, which is harmless
		// because the client keeps the newest snapshot.
		if party, e := protocol.SoloPartyInfoContext(w.role.WireID, w.channelCtx); e != nil {
			log.Printf("dungeon enter-select party preload unavailable: %v", e)
		} else {
			parts.party = []outboundPacket{{"dungeon_party_preloaded", 0, 9, party}}
		}
	}
	return parts
}

// gateWorldmap is the map screen this character's current gate opens (zero when
// the area is not a gate or the catalog has no worldmap for it).
func (w *worldSession) gateWorldmap() uint32 {
	if w == nil || w.service == nil {
		return 0
	}
	area, ok := w.service.Catalog.Areas[catalog.AreaKey(w.state.Position.Town, w.state.Position.Area)]
	if !ok {
		return 0
	}
	return area.Worldmap
}

// selectWorldmapDungeons returns the dungeon ids of the map screen this
// character is standing at. The gate area names its worldmap in the town
// script ("`[dungeon gate]` 2"), and each worldmap script lists its own
// dungeons, both imported into the world catalog.
func (w *worldSession) selectWorldmapDungeons() []uint32 {
	if w == nil || w.service == nil {
		return nil
	}
	area, ok := w.service.Catalog.Areas[catalog.AreaKey(w.state.Position.Town, w.state.Position.Area)]
	if !ok || area.Worldmap == 0 {
		return nil
	}
	if w.dungeons != nil && w.dungeons.Gates != nil {
		var ids []uint32
		for _, row := range w.sourceGateRows() {
			ids = append(ids, row.ID)
		}
		return ids
	}
	return w.service.Catalog.Worldmaps[strconv.FormatUint(uint64(area.Worldmap), 10)]
}

func (w *worldSession) sourceGateRows() []protocol.DungeonPermissionRow {
	if w.dungeons == nil || w.dungeons.Gates == nil || w.service == nil || w.service.Store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	quests, e := w.service.Store.Quests(ctx, w.account, w.role.ID)
	if e != nil {
		log.Printf("gate eligibility: %v", e)
		return nil
	}
	status := map[uint16]string{}
	for _, q := range quests {
		if q.ConfigVersion == w.dungeons.Source.Checksum {
			status[q.ID] = q.Status
		}
	}
	return gateRows(w.dungeons, w.gateWorldmap(), w.level, status)
}

func (w *worldSession) sourceGatePermissions() []outboundPacket {
	if w.dungeons == nil || w.dungeons.Gates == nil {
		return nil
	}
	body, e := protocol.DungeonPermissionUS(w.sourceGateRows())
	if e != nil {
		return nil
	}
	return []outboundPacket{{"dungeon_gate_permissions", 0, 5, body}}
}

func gateRows(c *catalog.DungeonCatalog, gate uint32, level byte, status map[uint16]string) []protocol.DungeonPermissionRow {
	var rows []protocol.DungeonPermissionRow
	seen := map[uint32]bool{}
	for _, n := range c.Gates[gate] {
		d, ok := c.Dungeons[n.Dungeon]
		if !ok || seen[n.Dungeon] || d.Tutorial || d.MinimumLevel > uint32(level) || !n.Allows(status) {
			continue
		}
		if _, err := dungeon.EntryMaze(d, func(id uint16) bool { return status[id] == "accepted" }); err != nil {
			continue
		}
		seen[n.Dungeon] = true
		rows = append(rows, protocol.DungeonPermissionRow{ID: n.Dungeon, State: 1})
	}
	return rows
}

// startAllowedDungeon starts the dungeon this character is permitted to enter,
// without waiting for SELECT_DUNGEON. The US client moves the actor into the
// dungeon transition on its own after the selection packet set and then waits
// for the room data, so the select body is synthesised here (17-byte Stage3
// layout with the neutral difficulty/option fields).
func (w *worldSession) startAllowedDungeon() (*dungeon.Session, []outboundPacket, error) {
	id := w.allowedDungeonID()
	if id == 0 {
		return nil, nil, fmt.Errorf("no permitted dungeon for this character")
	}
	body := make([]byte, 17)
	binary.LittleEndian.PutUint32(body[0:], id)
	body[8] = 0xff // sso helper byte, as observed in the client's own select
	return w.selectDungeon(body)
}

// allowedDungeonID is this character's own starting-route dungeon when one is
// still owed, and otherwise the lowest catalog dungeon this character's level
// is actually allowed into.
//
// It used to take the lowest id in the tutorial dungeon set, which is a table
// of every job's route rather than this character's. That returned 7110 for a
// character whose route is a different dungeon, so the automatic start was
// refused by authorizeTutorial and the client was left in the dungeon
// transition with nothing behind it — the archived 20:35 session shows exactly
// that: "dungeon 7110 is not this character's source starting route".
//
// The catalog fallback used to ignore the level gate as well, and the lowest
// catalog dungeon here requires level 55, so a level 1 character got
// "dungeon minimum level not met" from the selection domain instead.
func (w *worldSession) allowedDungeonID() uint32 {
	if w.tutorials != nil && w.tutorialDungeons != nil {
		if route, _, _, _, e := w.tutorialRoute(); e == nil && route != 0 {
			if _, ok := w.tutorialDungeons.Dungeons[route]; ok && w.authorizeTutorial(route) == nil {
				return route
			}
		}
	}
	if w.dungeons != nil {
		best := uint32(0)
		for id, d := range w.dungeons.Dungeons {
			if id <= 0 || uint32(w.level) < d.MinimumLevel {
				continue
			}
			if best == 0 || uint32(id) < best {
				best = uint32(id)
			}
		}
		return best
	}
	return 0
}

func (w *worldSession) selectDungeon(p []byte) (*dungeon.Session, []outboundPacket, error) {
	if w == nil || w.dungeons == nil || w.role.ID == 0 {
		return nil, nil, fmt.Errorf("dungeon catalog or character unavailable")
	}
	if w.activeDungeon != nil {
		return nil, nil, fmt.Errorf("dungeon entry already active")
	}
	r, e := protocol.DecodeDungeonSelection(p)
	if e != nil {
		return nil, nil, e
	}
	// A starting route names its own dungeon and has no town gate to stand
	// at, so it is resolved before the ordinary gate check.
	if w.tutorialDungeons != nil {
		if _, ok := w.tutorialDungeons.Dungeons[r.ID]; ok {
			return w.selectTutorial(r.ID)
		}
	}
	if e := w.checkTownGate(); e != nil {
		return nil, nil, e
	}
	if w.soloPartyReady && r.Party == 1 {
		// This connection owns the single-member bootstrap party. The dungeon
		// domain remains solo; never normalize arbitrary party IDs.
		r.Party = 65535
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	quests, e := w.service.Store.Quests(ctx, w.account, w.role.ID)
	if e != nil {
		return nil, nil, e
	}
	accepted := map[uint16]bool{}
	status := map[uint16]string{}
	for _, q := range quests {
		if q.ConfigVersion == w.dungeons.Source.Checksum {
			status[q.ID] = q.Status
		}
		if q.Status == "accepted" && q.ConfigVersion == w.dungeons.Source.Checksum {
			accepted[q.ID] = true
		}
	}
	if w.dungeons.Gates != nil {
		permitted := false
		for _, n := range w.dungeons.Gates[w.gateWorldmap()] {
			if n.Dungeon == r.ID && n.Allows(status) {
				permitted = true
				break
			}
		}
		if !permitted {
			return nil, nil, fmt.Errorf("dungeon %d not eligible at worldmap %d for current quests", r.ID, w.gateWorldmap())
		}
	}
	s, e := dungeon.Select(*w.dungeons, r, w.level, accepted)
	if e != nil {
		return nil, nil, e
	}
	if w.fatigue != nil && w.fatigue.EffectiveRoomCost(s.Definition.NoFatigue) > 0 {
		fp, err := w.fatigue.State(ctx, w.account, w.role.ID, time.Now())
		if err != nil {
			return nil, nil, err
		}
		if fp.Used >= fp.Limit {
			return nil, nil, storage.ErrFatigueExhausted
		}
	}
	var seed uint32
	if e = binary.Read(rand.Reader, binary.LittleEndian, &seed); e != nil {
		return nil, nil, e
	}
	if ev := w.eventPolicy(); ev.HellParty.Enabled {
		s.AssignRandomHell(seed, ev.HellParty.TriggerChance, ev.HellParty.MinDungeonLevel)
	}
	start, e := protocol.StartMapUS100(protocol.StartMapState{Position: s.Maze.Start, Seed: seed, Map: s.Room.Map, Monsters: s.Monsters})
	if e != nil {
		return nil, nil, e
	}
	plan := []outboundPacket{{"dungeon_select_ack", 1, 16, protocol.DungeonSelectAckUS100()}}
	if w.soloPartyBootstrap {
		party, e := protocol.SoloPartyInfoContext(w.role.WireID, w.channelCtx)
		if e != nil {
			return nil, nil, e
		}
		plan = append(plan, outboundPacket{"solo_party_initialized", 0, 9, party})
	}
	plan = append(plan, []outboundPacket{
		{"dungeon_info_sent", 0, 28, protocol.DungeonInfo(protocol.DungeonInfoState{ID: r.ID, Difficulty: r.Difficulty, Maze: s.Maze.Index, Boss: s.Maze.Boss, HasRandomHell: s.HasHellRoom, RandomHell: s.HellRoom})},
		{"dungeon_start_map_sent", 0, 29, start},
	}...)
	return s, plan, nil
}

func (w *worldSession) finishDungeonLoading(p []byte) ([]outboundPacket, error) {
	// v2.24 writes two zero uint32s in CMD37 (143925210). The transport
	// decrypts the whole 16-byte block, including the eight padding bytes.
	if len(p) != 16 {
		return nil, fmt.Errorf("unexpected loading record length")
	}
	for _, b := range p {
		if b != 0 {
			return nil, fmt.Errorf("unsupported loading option")
		}
	}
	if w == nil || w.activeDungeon == nil {
		return nil, fmt.Errorf("loading without dungeon session")
	}
	state, e := protocol.UserState(w.role.WireID, protocol.UserStateDungeon)
	if e != nil {
		return nil, e
	}
	plan := []outboundPacket{{"dungeon_loading_ack", 1, 37, []byte{1}}, {"dungeon_actor_state", 0, 3, state}, {"dungeon_loading_complete", 0, 30, protocol.DungeonLoaded()}}
	if w.progression != nil {
		p, err := character.ExperiencePayload(w.role)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"dungeon_experience_restored", 0, 37, p})
	}
	if w.fatigue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d := w.activeDungeon
		fp, _, e := w.fatigue.EnterRoom(ctx, w.account, w.role.ID, d.RunID, d.Room.Map, d.Definition.NoFatigue, time.Now())
		if e != nil {
			return nil, e
		}
		p, e := protocol.Fatigue(fp.Used, fp.Limit, fp.UsedMax)
		if e != nil {
			return nil, e
		}
		plan = append(plan, outboundPacket{"dungeon_fatigue_updated", 0, 36, p})
	}
	return plan, nil
}

func (w *worldSession) leaveDungeon() ([]outboundPacket, error) {
	if w == nil || w.role.ID == 0 {
		return nil, fmt.Errorf("leave dungeon without selected character")
	}
	// Leaving the starting route settles it, whichever exit is taken, and
	// lands the character at the town position the source route names.
	if w.inTutorial {
		if e := w.settleTutorialReturn(); e != nil {
			return nil, e
		}
	}
	// Town state remains the last owned, persisted origin throughout the run.
	ua, e := w.userAreaPayload()
	if e != nil {
		return nil, e
	}
	area, e := w.areaPayload()
	if e != nil {
		return nil, e
	}
	state, e := protocol.UserState(w.role.WireID, protocol.UserStateTown)
	if e != nil {
		return nil, e
	}
	return []outboundPacket{{"dungeon_leave_ack", 1, 42, []byte{1}}, {"town_actor_state", 0, 3, state}, {"dungeon_return_area", 0, 23, ua}, {"dungeon_return_users", 0, 24, area}}, nil
}

func (w *worldSession) returnFromDungeonSelection(p []byte) ([]outboundPacket, error) {
	if len(p) != 0 || !w.selectingDungeon || w.activeDungeon != nil {
		return nil, fmt.Errorf("return requires owned dungeon selection")
	}
	plan, e := w.leaveDungeon()
	if e != nil {
		return nil, e
	}
	plan[0] = outboundPacket{"dungeon_selection_return", 0, 132, protocol.DungeonSelectionReturn()}
	return plan, nil
}
func (w *worldSession) monsterDeath(p []byte) ([]outboundPacket, error) {
	if w.activeDungeon == nil {
		return nil, fmt.Errorf("death without active run")
	}
	r, e := protocol.DecodeMonsterDeath(p)
	if e != nil {
		return nil, e
	}
	// v2.24 reports the authored self-destructing passive object by template
	// (13099), killer FFFF. It is not a spawned monster entity and pays no
	// monster XP, drops, or room-clear credit.
	if r.Entity == 13099 && r.Killer == 65535 && w.mirkwoodRoom(76131) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return w.sceneObjectDeath(ctx)
	}
	if _, e = w.activeDungeon.ConfirmDeath(r.Entity, r.Killer, w.role.WireID); e != nil {
		return nil, e
	}
	// A monster the boss took with it is confirmed dead so the room can
	// clear, but it was not killed by this character: it pays no drops and
	// no experience. Both are skipped rather than rolled and discarded, so
	// nothing is charged against the run's drop budget either.
	unowned := w.activeDungeon.Unowned[uint16(r.Entity)]
	plan := []outboundPacket{{"monster_death_ack", 1, 39, []byte{1}}}
	if w.quests != nil && !unowned {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		changed, err := w.quests.MonsterKilled(ctx, w.role, w.activeDungeon, uint16(r.Entity))
		if err != nil {
			return nil, err
		}
		if changed {
			active, err := w.quests.Active(ctx, w.role)
			if err != nil {
				return nil, err
			}
			payload, err := protocol.QuestTriggers(active)
			if err != nil {
				return nil, err
			}
			plan = append(plan, outboundPacket{"hunt_quest_triggers", 0, 291, payload})
		}
	}
	if !w.deathSent[uint16(r.Entity)] {
		body := protocol.MonsterDeathConfirmed(uint16(r.Entity))
		if w.loot != nil && !unowned {
			if w.drops == nil || w.drops.Run != w.activeDungeon.RunID {
				// Drops span every job's gear at every level by design; that
				// breadth is a feature, not a bug, so the pool is not narrowed
				// to what this character can wear.
				w.drops = loot.NewSession(w.loot.Catalog, w.loot.Tables, w.loot.Rules, w.loot.Equipment, w.activeDungeon.RunID, w.account, w.role.ID, w.role.WireID)
				rule, ruleErr := loot.LoadAbyss(w.loot.AbyssPath, w.loot.Equipment)
				if ruleErr == nil && (rule == nil || w.loot.Catalog.Items[rule.Template].Kind != "stackable") {
					w.drops.Abyss = rule
				}
				if gd, gdErr := loot.LoadGlobalDrop(w.loot.GlobalDropPath, w.loot.Equipment, w.loot.Catalog); gdErr == nil {
					w.drops.GlobalDrop = gd
				}
				ev := w.eventPolicy()
				var activeContracts map[byte]bool
				if w.characters != nil {
					activeContracts, _ = w.characters.Store.ActivePremiumKinds(w.account, time.Now())
				}
				w.drops.DropMultiplier = ev.EffectiveDropMultiplier(activeContracts[storage.GrowthContractKind])
				w.drops.GoldMultiplier = ev.EffectiveGoldMultiplier()
				w.drops.UnstableRiftActive = ev.Contracts.UnstableRiftBonus && activeContracts[unstableRiftContractKind]
				if ev.HellParty.Enabled {
					hp := ev.HellParty
					w.drops.HellParty = &hp
				}
			}
			rows, err := w.drops.Death(w.activeDungeon, uint16(r.Entity))
			if err != nil {
				return nil, err
			}
			body, err = protocol.MonsterDeathDrops(uint16(r.Entity), rows)
			if err != nil {
				return nil, err
			}
		}
		plan = append(plan, outboundPacket{"monster_death_confirmed", 0, 38, body})
	}
	if w.progression != nil && !unowned {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		previousLevel := w.level
		saved, _, err := w.progression.Monster(ctx, w.role, w.activeDungeon, uint16(r.Entity))
		cancel()
		if err != nil {
			return nil, err
		}
		// Preserve the current scene identity; storage's wire column is a
		// durable roster identity rather than a new scene allocation.
		saved.WireID = w.role.WireID
		p, err := character.ExperiencePayload(saved)
		if err != nil {
			return nil, err
		}
		w.role = saved
		w.level = p[0]
		plan = append(plan, outboundPacket{"monster_experience_updated", 0, 37, p})
		if w.quests != nil && w.level != previousLevel {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			available, err := w.availableQuestPayload(ctx)
			cancel()
			if err != nil {
				return nil, err
			}
			plan = append(plan, outboundPacket{"level_available_quests", 0, 21, available})
		}
	}
	if !unowned {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		plan = append(plan, w.onActivityKill(ctx)...)
		cancel()
	}
	completed, err := w.completeDungeon()
	if err != nil {
		return nil, err
	}
	return append(plan, completed...), nil
}

func (w *worldSession) bossCheck(p []byte) ([]outboundPacket, error) {
	r, err := protocol.DecodeBossCheck(p)
	if err != nil {
		return nil, err
	}
	if err = w.activeDungeon.BossCheck(r, w.role.WireID); err != nil {
		return nil, err
	}
	return w.completeDungeon()
}

func (w *worldSession) completeDungeon() ([]outboundPacket, error) {
	if !w.activeDungeon.Completed() || w.completionSent {
		return nil, nil
	}
	var plan []outboundPacket
	if w.quests != nil && w.dungeons != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stage := false
		if w.mirkwoodRoom(76136) {
			var err error
			stage, err = w.storyChild(ctx, 3054)
			if err != nil {
				return nil, err
			}
		}
		active, err := w.quests.MapClear(ctx, w.role, w.activeDungeon, w.dungeons.Source.Checksum)
		if err != nil {
			return nil, err
		}
		triggers, err := protocol.QuestTriggers(active)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"map_clear_quest_triggers", 0, 291, triggers})
		if stage {
			more, err := w.settleStoryChild(ctx, 3054)
			if err != nil {
				return nil, err
			}
			plan = append(plan, more...)
		}
	}
	// Normal bosses require a matching target acknowledgment. The scripted
	// Lorien finale has no boss entity; do not invent one for the client.
	if target := w.activeDungeon.CompletionTarget(); target != 0 {
		body, err := protocol.BossCheckConfirmed(target)
		if err != nil {
			return nil, err
		}
		plan = append(plan, outboundPacket{"boss_check_confirmed", 0, 115, body})
	}
	plan = append(plan, outboundPacket{"dungeon_clear_enabled", 0, 31, protocol.DungeonClearEnabled()})
	return plan, nil
}
func (w *worldSession) moveDungeonRoom(p []byte) (*dungeon.Session, []outboundPacket, error) {
	if w.activeDungeon == nil || w.dungeons == nil {
		return nil, nil, fmt.Errorf("room transition without active run")
	}
	xy, e := protocol.DecodeMoveDungeonRoom(p)
	if e != nil {
		return nil, nil, e
	}
	next, e := w.activeDungeon.Move(*w.dungeons, xy)
	if e != nil {
		return nil, nil, e
	}
	var seed uint32
	if e = binary.Read(rand.Reader, binary.LittleEndian, &seed); e != nil {
		return nil, nil, e
	}
	// Every room uses the US100 layout. This call site used the 2.38 encoder,
	// which writes 0xFFFFFFFF at offset 9 and 0 at offset 13 — the exact shape
	// StartMapUS100's own comment documents as "tells the client this is a
	// revisit of an existing room, so it never built the dungeon room". The
	// first room already used StartMapUS100, so a run could only ever load its
	// first room.
	//
	// The reference's separate revisit body (room flag 0, 16 bytes, no map
	// index) is deliberately not used: this client's reader branches heavily
	// after offset 13 (handler 0x1434e26b0) and there is no evidence yet that it
	// takes that branch, and a body shorter than the reader expects aborts the
	// entry. Until the revisit path is measured, every room is sent as a first
	// load, which is the shape the first room already uses.
	body, e := protocol.StartMapUS100(protocol.StartMapState{Position: xy, Seed: seed, Map: next.Room.Map, Monsters: next.LivingMonsters()})
	if e != nil {
		return nil, nil, e
	}
	return next, []outboundPacket{{"dungeon_move_ack", 1, 45, []byte{1}}, {"dungeon_next_map_sent", 0, 29, body}}, nil
}
