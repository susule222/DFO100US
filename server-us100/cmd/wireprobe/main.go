// wireprobe is a loopback-only protocol experiment, not the LAN account service.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"dfolan/internal/admin"
	"dfolan/internal/catalog"
	"dfolan/internal/channelrefresh"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/dungeon"
	"dfolan/internal/events"
	"dfolan/internal/game/protocol"
	"dfolan/internal/game/wire"
	"dfolan/internal/inventory"
	"dfolan/internal/loot"
	"dfolan/internal/progression"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"dfolan/internal/world"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// selectAck answers specific command ids the client sends while it is still on
// the character select screen (development knob, see -select-ack).
var selectAck = map[uint16]int{}

// v2.24 ENUM_CMDPACKET_SAVE_CHARACTER_OPTION / ENUM_NOTIPACKET_CHARACTER_OPTION.
// The native reader at 1437920d0 requires exactly 512 bytes after the u32.
// This is a character preference table, not skill ownership, levels or SP.
const characterOptionsTable = "character_options_v224"
const characterOptionSize = 512

// US v2.24 CMD197 and NOTI173 (not the 2.38 account-options message).
// Slider control 143 reads/writes option 87. The getter returns zero until
// the client's general-option reader has initialized its manager.
const generalOptionBytes = 219 * 2
const uiSizeIndex = 87
const generalOptionsTable = "account_game_options_v224"

func validGeneralOptions(table []byte) bool {
	if len(table) != generalOptionBytes {
		return false
	}
	size := binary.LittleEndian.Uint16(table[uiSizeIndex*2:])
	return size <= 100 || size == 0x7fff
}

func saveGeneralOptions(store *storage.Store, account int64, body []byte) error {
	if len(body) != 448 || binary.LittleEndian.Uint32(body) != generalOptionBytes ||
		!validGeneralOptions(body[4:442]) || !bytes.Equal(body[442:], []byte{0, 0, 0, 0, 0, 0}) {
		return fmt.Errorf("invalid v2.24 general options")
	}
	key := strconv.FormatInt(account, 10)
	return store.Database().Mutate(func(tx *db100.Tx) error {
		var previous []byte
		ok, err := tx.Get(generalOptionsTable, key, &previous)
		if err != nil || (ok && bytes.Equal(previous, body[4:442])) {
			return err
		}
		return tx.Put(generalOptionsTable, key, body[4:442])
	})
}

func generalOptionsOnEntry(store *storage.Store, account int64) ([]byte, error) {
	var table []byte
	ok, err := store.Database().Get(generalOptionsTable, strconv.FormatInt(account, 10), &table)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Native sentinel: no server override, obtain defaults from this client.
		// Still send NOTI173: otherwise even reopening the panel reads zero.
		table = bytes.Repeat([]byte{0xff, 0x7f}, generalOptionBytes/2)
	}
	if !validGeneralOptions(table) {
		return nil, fmt.Errorf("invalid stored v2.24 general options")
	}
	p := append(binary.LittleEndian.AppendUint32(nil, generalOptionBytes), table...)
	// Two separate length-prefixed auxiliary option blocks. No data supplied:
	// leave their initialized client contents alone; do not populate hotkeys.
	return append(p, 0, 0, 0, 0, 0, 0, 0, 0), nil
}

func characterOptionKey(account, role int64) string {
	return fmt.Sprintf("%d/%d", account, role)
}

func saveCharacterOptions(store *storage.Store, account, role int64, p []byte) error {
	if len(p) != 520 || binary.LittleEndian.Uint32(p) != characterOptionSize ||
		!bytes.Equal(p[516:], []byte{0, 0, 0, 0}) {
		return fmt.Errorf("invalid v2.24 character preference size or padding")
	}
	if _, err := store.LiveCharacter(account, role); err != nil {
		return err
	}
	key := characterOptionKey(account, role)
	return store.Database().Mutate(func(tx *db100.Tx) error {
		var old []byte
		ok, err := tx.Get(characterOptionsTable, key, &old)
		if err != nil || (ok && bytes.Equal(old, p[4:516])) {
			return err
		}
		return tx.Put(characterOptionsTable, key, p[4:516])
	})
}

func loadCharacterOptions(store *storage.Store, account, role int64) ([]byte, error) {
	var saved []byte
	ok, err := store.Database().Get(characterOptionsTable, characterOptionKey(account, role), &saved)
	if err != nil || !ok {
		return nil, err
	}
	if len(saved) != characterOptionSize {
		return nil, fmt.Errorf("invalid stored v2.24 preference size")
	}
	return append(binary.LittleEndian.AppendUint32(nil, characterOptionSize), saved...), nil
}

// Target equipment is a per-character display list, never an item grant or move.
const targetEquipmentTable = "character_target_equipment_v224"

var targetSlots = [...]uint32{12, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 25}

type targetList [12]uint32

func (v targetList) payload() []byte {
	b := make([]byte, 4, 52) // NOTI2304 reads a u32 followed by 12 template IDs.
	for _, id := range v {
		b = binary.LittleEndian.AppendUint32(b, id)
	}
	return b
}
func updateGuideTargets(store *storage.Store, account, role int64, p []byte) (targetList, error) {
	var v targetList
	if len(p) != 112 || p[110] != 0 || p[111] != 0 {
		return v, fmt.Errorf("unexpected guide record size or padding")
	}
	op := p[13] // 1: single, 2: batch, 3: remove. Not a count.
	if op < 1 || op > 3 {
		return v, fmt.Errorf("unknown guide operation %d", op)
	}
	changes := map[int]uint32{}
	for i := 0; i < 12; i++ {
		slot := binary.LittleEndian.Uint32(p[14+4*i:])
		id := binary.LittleEndian.Uint32(p[62+4*i:])
		if slot == 0 && id == 0 {
			continue
		}
		if (op != 2 && i != 0) || (op == 3 && id != 0) || (op != 3 && (id == 0 || id > 0x7fffffff)) {
			return v, fmt.Errorf("invalid guide change")
		}
		index := -1
		for j, k := range targetSlots {
			if k == slot {
				index = j
				break
			}
		}
		if index < 0 {
			return v, fmt.Errorf("unknown guide slot %d", slot)
		}
		if _, dup := changes[index]; dup {
			return v, fmt.Errorf("duplicate guide slot")
		}
		changes[index] = id
	}
	if len(changes) == 0 {
		return v, fmt.Errorf("empty guide change")
	}
	if _, err := store.LiveCharacter(account, role); err != nil {
		return v, err
	}
	key := characterOptionKey(account, role)
	err := store.Database().Mutate(func(tx *db100.Tx) error {
		ok, e := tx.Get(targetEquipmentTable, key, &v)
		if e != nil {
			return e
		}
		before := v
		for j, id := range changes {
			v[j] = id
		}
		if ok && v == before {
			return nil
		}
		return tx.Put(targetEquipmentTable, key, v)
	})
	return v, err
}

// (r)cerashop.etc 014bd4a5f517d8ff45806ab72f8710b62f49503446101cf5c4f27fcb24ea90f1.
// Sealed stackable 590005784, PVF raw SHA a3f6b451b5d4e98103c511dbe7588f6a4e6c972a0ca3406430d1ebd1a9eb6775.
var neoShopSale = storage.CashSale{Product: 3400149, Template: 590005784, Quantity: 1, Cera: 3980}

func neoShopOrder(p []byte) bool {
	return len(p) == 24 && bytes.Equal(p[:6], []byte{0, 0, 1, 0, 255, 255}) && binary.LittleEndian.Uint32(p[6:]) == neoShopSale.Product && bytes.Equal(p[10:], make([]byte, 14))
}
func neoShopAck(product uint32, reason uint16) []byte {
	// v2.24 ACK64 reader consumes distinct success and failure layouts.
	if reason != 0 {
		b := append(protocol.Refusal(reason), 0)
		for _, v := range []uint32{0xffffffff, product, 1, 1, 0} {
			b = binary.LittleEndian.AppendUint32(b, v)
		}
		return b
	}
	b := []byte{1, 0}
	for _, v := range []uint32{0xffffffff, product, 1, 1, 0} {
		b = binary.LittleEndian.AppendUint32(b, v)
	}
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint32(b, 0xffffffff)
	return binary.LittleEndian.AppendUint32(b, 0)
}
func shopBagDelivery(src catalog.LootCatalog, rules inventory.BagRules) storage.CashDelivery {
	return func(role storage.Character, sale storage.CashSale) (json.RawMessage, json.RawMessage, error) {
		if sale != neoShopSale || role.ConfigVersion != "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69" || role.ConfigVersion != src.Source.Checksum || rules.Source != role.ConfigVersion {
			return nil, nil, fmt.Errorf("unsupported sale/source")
		}
		b, e := inventory.ReadBag(role.State)
		if e != nil {
			return nil, nil, e
		}
		// Separate unopened instances, stored in the configured consumable range. Never change world drop tables.
		slots := rules.Slots["[throw]"]
		if slots[0] == 0 || slots[1] < slots[0] {
			return nil, nil, fmt.Errorf("no consumable bag")
		}
		local := catalog.LootCatalog{Source: src.Source, Items: map[uint32]catalog.LootItem{sale.Template: {ID: sale.Template, Kind: "stackable", StackableType: "[usable cera package]", StackLimit: 1}}}
		rules.Slots = map[string][2]uint16{"[usable cera package]": slots}
		rules.SlotAliases = nil
		b, slot, e := b.Add(local, rules, sale.Template, sale.Quantity)
		if e != nil {
			return nil, nil, e
		}
		state, e := inventory.SaveBag(role.State, b)
		if e != nil {
			return nil, nil, e
		}
		receipt, e := json.Marshal(map[string]any{"slot": slot, "template": sale.Template, "quantity": sale.Quantity})
		return state, receipt, e
	}
}

func main() {
	fixture := flag.String("fixture", "", "verified-format server fixture to send after accept")
	dir := flag.String("output", "runtime/wireprobe", "capture directory")
	gameListen := flag.String("game-listen", "0.0.0.0:0", "game TCP listen address (IPv4 host:port)")
	channelListen := flag.String("channel-listen", "", "optional override for channel-refresh listen address (IPv4 host:port)")
	advertiseHost := flag.String("advertise-host", "", "optional IPv4 address advertised in channel directory when listening on 0.0.0.0")
	responseFile := flag.String("responses", "", "JSON mapping command IDs to response fixture paths")
	characterStorage := flag.String("character-storage", "", "enable experimental persisted character handling with this local storage config")
	characterCatalog := flag.String("character-catalog", "configs/characters.generated.json", "PVF-derived profession catalog")
	characterRules := flag.String("character-rules", "configs/character-probe.json", "explicit local bootstrap settings")
	selectProbeConfig := flag.String("select-probe-config", "", "opt in to current-build SELECT parser experiment; does not initialize a town")
	entryBasicProbe := flag.Bool("entry-basic-probe", false, "send experimental current-build minimum actor info after SELECT; does not initialize a town")
	townCatalogFile := flag.String("town-catalog", "", "PVF-derived town-area catalog for the entry experiment")
	townProbeFile := flag.String("town-entry-probe", "", "opt in to experimental town entry using this separate spawn policy")
	worldCatalogFile := flag.String("world-catalog", "", "enable source-backed town transitions and saved positions")
	worldRulesFile := flag.String("world-rules", "configs/world-probe.json", "separate world movement policy")
	entryAdditionProbe := flag.Bool("entry-addition-probe", false, "send current-build source attributes; optional inventory and skills remain pending")
	entryAdditionMarker := flag.Bool("entry-addition-marker", false, "send the mode-1 addition with per-field markers for US100 field mapping (semantic recovery only)")
	entryAppearanceProbe := flag.Bool("entry-appearance-probe", false, "fill the mode-1 23-byte block from the catalog appearance list (semantic recovery only)")
	entryStatProbe := flag.Bool("entry-stat-probe", false, "send the mode-6 45-byte block probe (semantic recovery only; the block target object also carries scene state, so this must stay off for playable runs)")
	entryStatMarker := flag.Bool("entry-stat-marker", false, "send the mode-6 block with distinctive values in the four consumed u32 slots (semantic recovery only)")
	entryStub := flag.Bool("entry-addition-stub", false, "send the mode-1 entry with a key that matches no actor: the client then leaves its loading state without applying any unrecovered field (playable runs use this)")
	injectWatch := flag.String("inject-watch", "", "recovery only: when this file's contents change, send them as a kind-0 id-2 packet to the current session")
	injectWatchDir := flag.String("inject-watch-dir", "", "recovery only: watch this directory for inject-<id>.bin files and send each change as that command id")
	entryRamp := flag.Bool("entry-addition-ramp", false, "recovery only: send the mode-1 entry with a byte ramp in its 23-byte block, inside the entry sequence where the client settles its stats")
	entryLong := flag.Int("entry-addition-long", 0, "recovery only: send the mode-1 entry with this many byte-ramp tail bytes, to measure how far the client reads and whether the loading completes")
	entryNone := flag.Bool("entry-addition-none", false, "recovery only: send the mode-1 entry with every appearance slot marked none, the correct block for a character without equipment")
	entryNoneLong := flag.Int("entry-addition-none-long", 0, "recovery only: none-appearance entry plus this many byte-ramp tail bytes, to expose the sections the client reads after the experience pair")
	questCatalogFile := flag.String("quest-catalog", "", "enable source quest accept/abandon persistence; objectives and rewards are separate")
	vaultRulesFile := flag.String("vault-rules", "", "source vault capacity and empty-state initialization")
	fatigueRulesFile := flag.String("fatigue-rules", "", "separate persisted fatigue and rollover policy")
	dungeonCatalogFile := flag.String("dungeon-catalog", "", "source dungeon layouts and first-room loading experiment")
	progressionCatalogFile := flag.String("progression-catalog", "", "current-source experience and growth catalog")
	progressionRulesFile := flag.String("progression-rules", "configs/experience.compat90.json", "separate reference compatibility formula settings")
	lootCatalogFile := flag.String("loot-catalog", "", "current gold/ordinary stackable source projection; equipment pending")
	lootRulesFile := flag.String("loot-rules", "configs/drop.compat90.json", "explicit reference drop formula policy")
	bagRulesFile := flag.String("bag-rules", "configs/inventory.compat90.json", "separate bag slot and missing stack limit policy")
	cardRulesFile := flag.String("card-rules", "configs/cards.compat90.json", "separate compatible free-card policy")
	learningFile := flag.String("skill-catalog", "", "current PVF learning metadata; enables manual learning and persisted skill slots")
	channelRefreshFile := flag.String("channel-refresh-config", "", "separate local channel directory service for native refresh")
	equipmentRewardFile := flag.String("quest-equipment-catalog", "", "source basic-equipment metadata for atomic quest rewards")
	wearRulesFile := flag.String("equipment-wear-rules", "", "current-client equipment slots and persistent wear handling")
	soloPartyBootstrap := flag.Bool("solo-party-bootstrap", false, "initialize the owned actor in the current solo party roster")
	accountOptionsFile := flag.String("account-options", "", "sparse current-client account option overrides; other defaults remain client-owned")
	tutorialRoutesFile := flag.String("tutorial-routes", "", "source per-job starting route table")
	tutorialDungeonsFile := flag.String("tutorial-dungeons", "", "source starting-route dungeon catalog")
	gateSetFile := flag.String("gate-set", "configs/us100/gate-set.json", "which optional packets the dungeon gate sends (JSON); see cmd/wireprobe/gate_set.go")
	boosterCatalogFile := flag.String("booster-catalog", "", "source PVF booster/selection/package catalog (JSON or .json.gz)")
	accountName := flag.String("account", "probe", "account the gateway serves")
	requireAccount := flag.Bool("require-account", false, "require an existing database and pre-created account instead of auto-creating")
	gmDir := flag.String("launcher-gm", "", "optional local desktop GM inbox and room drop policy")
	rosterV224 := flag.Bool("roster-v224", false, "encode the roster list with the US100 (2.24) row layout")
	rosterPad := flag.Int("roster-pad", 0, "extra bytes to insert after each roster row name (layout experiment)")
	rosterCounter := flag.Int("roster-counter-first", 0, "first u32 of the 848 counter response (development knob)")
	selectAckSpec := flag.String("select-ack", "", "answer these command ids on the select screen with an Ok body, e.g. 217:8,480:8")
	noRoster := flag.Bool("no-roster", false, "do not answer GET_USERINFO with the roster (development knob)")
	rosterDelay := flag.Int("roster-delay", 0, "seconds to wait before answering GET_USERINFO (development knob)")
	userInfoReply := flag.String("userinfo-reply", "", "kind:id to answer GET_USERINFO with, e.g. 1:8 (development knob; default 0:2)")
	rosterEquip := flag.String("roster-equip", "", "attach this appearance entry to every roster row, slot:item, e.g. 12:1000 (development knob)")
	rosterRepeat := flag.Int("roster-repeat", 0, "re-send the GET_USERINFO reply this many times in the same connection (development knob)")
	rosterRepeatDelayMS := flag.Int("roster-repeat-delay", 4000, "milliseconds between repeated replies")
	rosterMarkers := flag.Bool("roster-markers", false, "fill unknown roster fields with distinct markers (semantic recovery only)")
	rosterLayoutProbe := flag.Bool("roster-layout-probe", false, "send explicit post-name roster bytes per row to bisect the portrait field (semantic recovery only)")
	flag.Parse()
	if *rosterEquip != "" {
		var slot, item int
		if _, err := fmt.Sscanf(*rosterEquip, "%d:%d", &slot, &item); err == nil && slot >= 0 && slot < 24 {
			character.RosterEquipment = []protocol.Equipment{{Slot: byte(slot), Item: uint32(item)}}
			log.Printf("roster equip knob: slot=%d item=%d applied to every row", slot, item)
		} else {
			log.Fatalf("invalid -roster-equip %q (want slot:item)", *rosterEquip)
		}
	}
	if *injectWatch != "" {
		go watchInject(*injectWatch)
	}
	if *injectWatchDir != "" {
		go watchInjectDir(*injectWatchDir)
	}
	gateSet, gateSetRead, e := loadGateSet(*gateSetFile)
	if e != nil {
		log.Fatal(e)
	}
	if gateSetRead {
		log.Printf("gate set: read %s -> minimal_gate=%v gate_ack=%v enter_select=%v candidate_list=%v user_area=%v party_preload=%v enter_select_state=%d reference_order=%v",
			*gateSetFile, gateSet.MinimalGate, gateSet.GateAck, gateSet.EnterSelect, gateSet.CandidateList,
			gateSet.UserAreaSelect, gateSet.PartyPreload, gateSet.EnterSelectState, gateSet.ReferenceOrder)
	} else {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			cwd = "?"
		}
		log.Printf("gate set: %s NOT FOUND (working dir %s) -> using defaults minimal_gate=%v candidate_list=%v; pass an absolute path if that is not intended",
			*gateSetFile, cwd, gateSet.MinimalGate, gateSet.CandidateList)
	}
	var accountOptionsPayload []byte
	if *accountOptionsFile != "" {
		data, err := os.ReadFile(*accountOptionsFile)
		if err != nil {
			log.Fatal(err)
		}
		var overrides map[uint16]uint16
		if err = json.Unmarshal(data, &overrides); err != nil {
			log.Fatal(err)
		}
		accountOptionsPayload, err = protocol.AccountOptions(overrides)
		if err != nil {
			log.Fatal(err)
		}
	}
	gameHost, _, gameListenError := net.SplitHostPort(*gameListen)
	if gameListenError != nil || net.ParseIP(gameHost) == nil || net.ParseIP(gameHost).To4() == nil {
		log.Fatal("game-listen must be an explicit IPv4 address")
	}
	if *advertiseHost != "" {
		ip := net.ParseIP(*advertiseHost)
		if ip == nil || ip.To4() == nil || ip.To4().IsUnspecified() || len(ip.To4().String()) > 15 {
			log.Fatal("advertise-host must be a non-unspecified IPv4 address")
		}
	}
	if *channelListen != "" {
		chHost, _, chErr := net.SplitHostPort(*channelListen)
		if chErr != nil || net.ParseIP(chHost) == nil || net.ParseIP(chHost).To4() == nil {
			log.Fatal("channel-listen must be an explicit IPv4 host:port")
		}
	}
	if *entryBasicProbe && (*selectProbeConfig == "" || *characterStorage == "") {
		log.Fatal("entry basic probe requires persisted characters and SELECT probe configuration")
	}
	var townCatalog catalog.TownArea
	var townPolicy struct {
		X     uint16  `json:"x"`
		Y     uint16  `json:"y"`
		Flags [3]byte `json:"flags"`
	}
	if *townProbeFile != "" {
		if !*entryBasicProbe || *townCatalogFile == "" {
			log.Fatal("town probe requires basic actor and town catalog")
		}
		var e error
		townCatalog, e = catalog.LoadTownArea(*townCatalogFile)
		if e != nil {
			log.Fatal(e)
		}
		b, e := os.ReadFile(*townProbeFile)
		if e != nil {
			log.Fatal(e)
		}
		if e = json.Unmarshal(b, &townPolicy); e != nil {
			log.Fatal(e)
		}
		if !townCatalog.Allows(255, townPolicy.X, townPolicy.Y) {
			log.Fatal("spawn policy lies outside source walkable rectangles")
		}
	}
	var selectProbe *protocol.SelectProbeState
	if *selectProbeConfig != "" {
		b, e := os.ReadFile(*selectProbeConfig)
		if e != nil {
			log.Fatal(e)
		}
		selectProbe = new(protocol.SelectProbeState)
		if e = json.Unmarshal(b, selectProbe); e != nil {
			log.Fatal(e)
		}
		if _, e = protocol.SelectProbeSuccess(*selectProbe); e != nil {
			log.Fatal(e)
		}
	}
	var characters *character.Service
	var worldService *world.Service
	var wearService *inventory.WearService
	var questService *quest.Service
	var vaultService *inventory.VaultService
	var fatigueService *character.FatigueService
	var developmentAccount int64
	var dungeonCatalog *catalog.DungeonCatalog
	var progressionService *character.ProgressionService
	var lootService *loot.Service
	if *characterStorage != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cfg, e := storage.LoadConfig(*characterStorage)
		if e != nil {
			log.Fatal(e)
		}
		if *requireAccount {
			cfg.RequireExisting = true
			cfg.SkipSeed = true
		}
		s, e := storage.Open(ctx, cfg)
		if e != nil {
			log.Fatal(e)
		}
		defer s.Close()
		if e = s.Migrate(ctx); e != nil {
			log.Fatal(e)
		}
		if e = s.MigrateTutorial(ctx); e != nil {
			log.Fatal(e)
		}
		// The account cera ledger backs the balance sent in SELECT.
		if e = s.MigrateGrants(ctx); e != nil {
			log.Fatal(e)
		}
		data, e := catalog.LoadCharacters(*characterCatalog)
		if e != nil {
			log.Fatal(e)
		}
		b, e := os.ReadFile(*characterRules)
		if e != nil {
			log.Fatal(e)
		}
		var rules character.Rules
		if e = json.Unmarshal(b, &rules); e != nil {
			log.Fatal(e)
		}
		characters, e = character.New(s, data, rules)
		if e != nil {
			log.Fatal(e)
		}
		protocol.RosterRowPad = *rosterPad
		protocol.RosterCounterFirst = uint32(*rosterCounter)
		for _, part := range strings.Split(*selectAckSpec, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			idText, lenText, _ := strings.Cut(part, ":")
			id, e1 := strconv.Atoi(strings.TrimSpace(idText))
			n, e2 := strconv.Atoi(strings.TrimSpace(lenText))
			if e1 != nil || e2 != nil || id <= 0 {
				continue
			}
			if n < 1 {
				n = 1
			}
			selectAck[uint16(id)] = n
		}
		if len(selectAck) > 0 {
			log.Printf("select ack overrides: %v", selectAck)
		}
		if *rosterV224 {
			// The US100 (2.24.0.124) client parses a different roster row than
			// the 2.38 reference build; the layouts are not interchangeable.
			characters.V224 = true
		}
		if *rosterMarkers {
			// Semantic-recovery mode: every unknown roster field carries a
			// distinct marker. Not for gameplay.
			characters.RosterMarkers = true
		}
		if *rosterLayoutProbe {
			// Semantic-recovery mode: explicit post-name bytes per row to
			// bisect which byte drives the character-select portrait.
			characters.RosterLayoutProbe = true
		}
		if *learningFile != "" {
			characters.Learning, e = character.LoadLearningCatalog(*learningFile, data.Source.Checksum)
			if e != nil {
				log.Fatal(e)
			}
			if e = s.MigrateCharacterEvents(ctx); e != nil {
				log.Fatal(e)
			}
		}
		if *requireAccount {
			var found bool
			developmentAccount, found, e = s.FindAccount(ctx, *accountName)
			if e != nil {
				log.Fatal(e)
			}
			if !found {
				log.Fatalf("account %q does not exist in database (create an account in the launcher first)", *accountName)
			}
		} else {
			var found bool
			developmentAccount, found, e = s.FindAccount(ctx, *accountName)
			if e != nil {
				log.Fatal(e)
			}
			if !found {
				developmentAccount, e = s.DevelopmentAccount(ctx, *accountName)
				if e != nil {
					log.Fatal(e)
				}
			}
		}
	}
	if *entryAdditionProbe && !*entryBasicProbe {
		log.Fatal("addition requires a basic actor")
	}
	if *fatigueRulesFile != "" {
		if characters == nil || selectProbe == nil {
			log.Fatal("fatigue requires persisted characters and SELECT")
		}
		var e error
		fatigueService, e = character.LoadFatigueService(characters.Store, *fatigueRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = characters.Store.MigrateFatigue(ctx)
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	if *worldCatalogFile != "" {
		if characters == nil || *townProbeFile == "" {
			log.Fatal("world requires persisted characters and a spawn policy")
		}
		data, e := catalog.LoadWorld(*worldCatalogFile)
		if e != nil {
			log.Fatal(e)
		}
		b, e := os.ReadFile(*worldRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		var rules world.Rules
		if e = json.Unmarshal(b, &rules); e != nil {
			log.Fatal(e)
		}
		if data.Source.Checksum != characters.Catalog.Source.Checksum {
			log.Fatal("world/character source versions differ")
		}
		worldService = &world.Service{Store: characters.Store, Catalog: data, Rules: rules}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = characters.Store.MigrateWorld(ctx)
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	if *dungeonCatalogFile != "" {
		if worldService == nil {
			log.Fatal("dungeons require world sessions")
		}
		data, e := catalog.LoadDungeons(*dungeonCatalogFile)
		if e != nil {
			log.Fatal(e)
		}
		if data.Source.Checksum != worldService.Catalog.Source.Checksum {
			log.Fatal("dungeon/world source versions differ")
		}
		dungeonCatalog = &data
	}
	if *progressionCatalogFile != "" {
		if characters == nil || dungeonCatalog == nil {
			log.Fatal("progression requires source characters and dungeon sessions")
		}
		data, e := catalog.LoadProgression(*progressionCatalogFile)
		if e != nil {
			log.Fatal(e)
		}
		rules, e := progression.LoadRules(*progressionRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		if data.Source.Checksum != characters.Catalog.Source.Checksum || data.Source.Checksum != dungeonCatalog.Source.Checksum {
			log.Fatal("progression source version mismatch")
		}
		progressionService = &character.ProgressionService{Store: characters.Store, Catalog: data, Professions: characters.Catalog, Rules: rules}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = characters.Store.MigrateCharacterEvents(ctx)
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	var tutorialRoutes *catalog.TutorialCatalog
	var tutorialDungeons *catalog.DungeonCatalog
	if *tutorialRoutesFile != "" {
		if dungeonCatalog == nil || characters == nil {
			log.Fatal("starting routes require source dungeons and persisted characters")
		}
		if *tutorialDungeonsFile == "" {
			log.Fatal("starting routes require their own dungeon catalog")
		}
		routes, e := catalog.LoadTutorialRoutes(*tutorialRoutesFile, characters.Catalog.Source.Checksum)
		if e != nil {
			log.Fatal(e)
		}
		data, e := catalog.LoadDungeons(*tutorialDungeonsFile)
		if e != nil {
			log.Fatal(e)
		}
		if data.Source.Checksum != characters.Catalog.Source.Checksum {
			log.Fatal("starting-route dungeon source version differs")
		}
		tutorialRoutes, tutorialDungeons = routes, &data
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e = characters.Store.MigrateBirth(ctx)
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	if *lootCatalogFile != "" {
		if progressionService == nil {
			log.Fatal("loot requires progression and owned dungeon sessions")
		}
		c, e := catalog.LoadLoot(*lootCatalogFile)
		if e != nil {
			log.Fatal(e)
		}
		r, e := loot.LoadRules(*lootRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		bag, e := inventory.LoadBagRules(*bagRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		tables, e := loot.Parse(c)
		if e != nil {
			log.Fatal(e)
		}
		if c.Source.Checksum != characters.Catalog.Source.Checksum || bag.Source != c.Source.Checksum {
			log.Fatal("loot source mismatch")
		}
		lootService = &loot.Service{Store: characters.Store, Catalog: c, Rules: r, BagRules: bag, Tables: tables}
		boosterPath := *boosterCatalogFile
		if boosterPath == "" {
			for _, cand := range []string{
				filepath.Join(filepath.Dir(*lootCatalogFile), "boosters.generated.json.gz"),
				filepath.Join("configs", "us100", "boosters.generated.json.gz"),
			} {
				if _, statErr := os.Stat(cand); statErr == nil {
					boosterPath = cand
					break
				}
			}
		}
		if boosterPath != "" {
			bc, be := catalog.LoadBoosters(boosterPath)
			if be != nil {
				log.Fatal(be)
			}
			lootService.Boosters = bc
			log.Printf("booster catalog: loaded %d boxes from %s", len(bc.Boxes), boosterPath)
		}
		cards, e := loot.LoadCardRules(*cardRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		lootService.CardPolicy = &cards
	}
	responses := map[uint16][]byte{}
	if *questCatalogFile != "" {
		if worldService == nil {
			log.Fatal("quests require world character sessions")
		}
		data, e := catalog.LoadQuests(*questCatalogFile)
		if e != nil {
			log.Fatal(e)
		}
		if data.Source.Checksum != characters.Catalog.Source.Checksum {
			log.Fatal("quest/character source versions differ")
		}
		questService = &quest.Service{Store: characters.Store, Catalog: data, Professions: characters.Catalog, Progression: progressionService}
		if *equipmentRewardFile != "" {
			if lootService == nil {
				log.Fatal("quest inventory requires the shared bag catalog")
			}
			equipment, e := inventory.LoadEquipmentCatalog(*equipmentRewardFile, data.Source.Checksum)
			if e != nil {
				log.Fatal(e)
			}
			questService.Inventory = &inventory.Awarder{Catalog: lootService.Catalog, Rules: lootService.BagRules, Equipment: equipment}
			if *wearRulesFile != "" {
				rules, err := inventory.LoadWearRules(*wearRulesFile, data.Source.Checksum)
				if err != nil {
					log.Fatal(err)
				}
				wearService = &inventory.WearService{Store: characters.Store, Catalog: equipment, Professions: characters.Catalog, BagRules: lootService.BagRules, Rules: rules}
			}
			// The same source equipment catalog backs quest rewards,
			// monster gear drops, and Adventurer Fame calculations.
			lootService.Equipment = equipment
			characters.Equipment = equipment
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = characters.Store.MigrateQuests(ctx)
		if e == nil {
			e = characters.Store.MigrateQuestObjectives(ctx)
		}
		if e == nil && progressionService != nil {
			e = characters.Store.MigrateQuestRewards(ctx)
		}
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	if *vaultRulesFile != "" {
		if characters == nil {
			log.Fatal("vault initialization requires characters")
		}
		rules, e := inventory.LoadVaultRules(*vaultRulesFile)
		if e != nil {
			log.Fatal(e)
		}
		vaultService = &inventory.VaultService{Store: characters.Store, Rules: rules}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = characters.Store.MigrateVault(ctx)
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	if *responseFile != "" {
		b, err := os.ReadFile(*responseFile)
		if err != nil {
			log.Fatal(err)
		}
		paths := map[uint16]string{}
		if err = json.Unmarshal(b, &paths); err != nil {
			log.Fatal(err)
		}
		for id, path := range paths {
			b, err := os.ReadFile(path)
			if err != nil {
				log.Fatal(err)
			}
			if err = wire.ValidateServer(b); err != nil {
				log.Fatal(err)
			}
			responses[id] = b
		}
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(*fixture)
	if err != nil {
		log.Fatal(err)
	}
	if err = wire.ValidateServer(raw); err != nil {
		log.Fatal(err)
	}
	l, err := net.Listen("tcp4", *gameListen)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	var gm gmSessions
	eventsPath := ""
	if *gmDir != "" {
		if questService == nil || questService.Inventory == nil || lootService == nil {
			log.Fatal("launcher GM requires source inventory")
		}
		svc := &admin.Service{
			Store:       characters.Store,
			Awarder:     questService.Inventory,
			Characters:  characters,
			Progression: progressionService,
			Quests:      questService,
		}
		eventsPath = filepath.Join(*gmDir, "events.json")
		lootService.AbyssPath = filepath.Join(*gmDir, "abyss.json")
		lootService.GlobalDropPath = filepath.Join(*gmDir, "global_drop.json")
		lootService.EventsPath = eventsPath
		if fatigueService != nil {
			fatigueService.EventsPath = eventsPath
		}
		if progressionService != nil {
			progressionService.EventsPath = eventsPath
			progressionService.Fatigue = fatigueService
		}
		drain := func() {
			if err := admin.DrainAndNotify(*gmDir, svc, developmentAccount, gm.committed); err != nil {
				log.Print("launcher GM: ", err)
			}
		}
		drain()
		go func() {
			for range time.Tick(time.Second) {
				drain()
			}
		}()
	}
	f, err := os.OpenFile(filepath.Join(*dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var mu sync.Mutex
	event := func(v map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		v["time"] = time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(f).Encode(v); err != nil {
			log.Print(err)
		}
	}
	type channelListener struct {
		listener net.Listener
		serverID uint32
		id       uint32
		name     string
		banner   []byte
	}
	bannerKeys := make([]byte, channelBannerKeyBytes)
	for i := range bannerKeys {
		bannerKeys[i] = byte(i%127 + 1)
	}
	defaultKeys := bannerKeys[:wire.SessionKeyBytes]
	listeners := []channelListener{{listener: l, serverID: 1, id: 1, name: "LAN Local", banner: raw}}
	channelRefreshAddr := ""
	channelEndpoints := map[string]string{}
	if *channelRefreshFile != "" {
		cfg, e := channelrefresh.Load(*channelRefreshFile)
		if e != nil {
			log.Fatal(e)
		}
		if *channelListen != "" {
			cfg.Listen = *channelListen
		}
		if *advertiseHost != "" {
			cfg.Advertise = *advertiseHost
		}
		endpoints := make(map[uint32]string, len(cfg.Channels))
		listeners = nil
		for i, ch := range cfg.Channels {
			var ln net.Listener
			if i == 0 {
				ln = l
			} else {
				ln, e = net.Listen("tcp4", net.JoinHostPort(gameHost, "0"))
				if e != nil {
					log.Fatal(e)
				}
				defer ln.Close()
			}
			banner, be := buildChannelInfoFrame(cfg.ServerID, ch.ID, ch.Name, bannerKeys)
			if be != nil {
				log.Fatal(be)
			}
			endpoints[ch.ID] = ln.Addr().String()
			channelEndpoints[strconv.FormatUint(uint64(ch.ID), 10)] = ln.Addr().String()
			listeners = append(listeners, channelListener{listener: ln, serverID: cfg.ServerID, id: ch.ID, name: ch.Name, banner: banner})
		}
		cl, e := channelrefresh.ServeWithEndpoints(cfg, endpoints, l.Addr().String(), event)
		if e != nil {
			log.Fatal(e)
		}
		defer cl.Close()
		channelRefreshAddr = cl.Addr().String()
		event(map[string]any{"kind": "channel_refresh_ready", "address": channelRefreshAddr, "game_address": l.Addr().String(), "channels": channelEndpoints})
	}
	readyInfo := map[string]any{
		"address":            l.Addr().String(),
		"pid":                os.Getpid(),
		"account":            *accountName,
		"account_id":         developmentAccount,
		"fixture_bytes":      len(raw),
		"launcher_gm":        *gmDir != "",
		"online_item_grants": *gmDir != "",
		"character_gm":       *gmDir != "",
		"avatar_gm":          *gmDir != "",
	}
	if channelRefreshAddr != "" {
		readyInfo["channel_refresh_address"] = channelRefreshAddr
		readyInfo["channels"] = channelEndpoints
	}
	if *advertiseHost != "" {
		readyInfo["advertise_host"] = *advertiseHost
	}
	ready, _ := json.Marshal(readyInfo)
	if err = os.WriteFile(filepath.Join(*dir, "ready.json"), ready, 0600); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(ready))
	serveConn := func(c net.Conn, serverID uint32, chID uint32, chName string, chRaw []byte) {
		defer c.Close()
		channelCtx := [2]byte{byte(serverID), byte(chID)}
		peer := c.RemoteAddr().String()
		keys := make([]byte, wire.SessionKeyBytes)
		copy(keys, defaultKeys)
		bootstrapped := false
		// Per-command body sample counters for this connection.
		bodySamples := map[uint16]int{}
		// Per-command counters for unimplemented high-frequency frames that
		// are not retained as samples; only a periodic summary is logged.
		throttledCalls := map[uint16]int{}
		var selectedCharacterID int64
		acceptedReplies := map[uint16]bool{}
		var selectedBasic []byte
		var selectedAddition []byte
		var worldState *worldSession
		var skillState skillSession
		var equipmentState equipmentSession
		var disjointState disjointSession
		if worldService != nil {
			worldState = &worldSession{service: worldService, account: developmentAccount, flags: townPolicy.Flags, dungeons: dungeonCatalog, tutorials: tutorialRoutes, tutorialDungeons: tutorialDungeons, professions: characters.Catalog, fatigue: fatigueService, characters: characters, quests: questService, progression: progressionService, loot: lootService, soloPartyBootstrap: *soloPartyBootstrap, channelCtx: channelCtx, gateSet: gateSet, eventsPath: eventsPath}
		}
		sendPayload := func(kind byte, id uint16, payload []byte) error {
			prepared, e := preparePackets(keys, []outboundPacket{{"response", kind, id, payload}})
			if e != nil {
				event(map[string]any{"kind": "response_encode_error", "peer": peer, "error": e.Error()})
				return e
			}
			c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			e = writePackets(c, prepared, nil)
			if e != nil {
				event(map[string]any{"kind": "response_write_error", "peer": peer, "error": e.Error()})
			}
			return e
		}
		event(map[string]any{"kind": "accept", "peer": peer, "server_id": serverID, "channel_id": chID, "channel_name": chName, "local": c.LocalAddr().String()})
		if *injectWatch != "" {
			// Layout recovery: publish this session's sender so a watched
			// file can push candidate packets without a client restart.
			injectSender.Store(sendPayload)
			event(map[string]any{"kind": "inject_watch_ready", "path": *injectWatch})
		}
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.Copy(c, bytes.NewReader(chRaw)); err != nil {
			event(map[string]any{"kind": "write_error", "error": err.Error()})
			return
		}
		event(map[string]any{"kind": "server_frame", "peer": peer, "channel_id": chID, "hex": hex.EncodeToString(chRaw)})
			var notice chan struct{}
			var input <-chan gmInput
			if *gmDir != "" {
				notice = make(chan struct{}, 1)
				stop := make(chan struct{})
				defer close(stop)
				defer gm.bind(notice, 0, 0)
				input = gmReadFrames(c, stop)
			}
			for {
				var frame wire.Frame
				var err error
				if input == nil {
					frame, err = wire.ReadClient(c)
				} else {
					select {
					case <-notice:
						if selectedCharacterID != 0 {
							if err := worldState.syncGrantedBag(developmentAccount, selectedCharacterID, sendPayload, event); err != nil {
								event(map[string]any{"kind": "gm_inventory_update_error", "character_id": selectedCharacterID, "error": err.Error()})
								return
							}
						}
						continue
					case in, ok := <-input:
						if !ok {
							return
						}
						frame, err = in.frame, in.err
					}
				}
				if err != nil {
					event(map[string]any{"kind": "close", "peer": peer, "error": err.Error()})
					return
				}
				entry := map[string]any{"kind": "client_frame", "peer": peer, "type": frame.Type, "id": frame.ID, "bytes": len(frame.Raw)}
				var plaintext []byte
				verified := false
				// An implemented command always retains its body. Everything
				// else is sampled up to a per-command cap, so the wire format
				// of a feature this build does not implement is captured by
				// ordinary play rather than guessed at from another build.
				//
				// A parameterless command is a bare 13-byte header. The client
				// writes no fields, so there is no ciphertext at all; bodies are
				// padded to 16, so any command that carries data arrives as 29
				// bytes or more. command_layouts36.json lists 7 (leave game),
				// 42 and 69 (dungeon exit), 63, 67, 120 and 1301 (village
				// return) as body 0. Demanding 14 bytes before verifying made
				// every one of them fail the checksum gate unread, which is why
				// the escape menu did nothing: the frame is complete, its
				// payload is simply empty, and the checksum then covers only
				// the two header bytes.
				retained := false
				characterPreference := frame.Type == 1 && frame.ID == 439
				targetPreference := frame.Type == 1 && frame.ID == 2065
				openChild := frame.Type == 1 && frame.ID == 160
				openPackage := frame.Type == 1 && frame.ID == 510
				shopBuy := frame.Type == 1 && frame.ID == 64
				adventureInfo := frame.Type == 1 && frame.ID == 1395
				generalPreference := frame.Type == 1 && frame.ID == 197
				openDanji := frame.Type == 1 && frame.ID == 2052
				sortItems := frame.Type == 1 && frame.ID == 20
				if len(frame.Raw) >= wire.ClientHeaderSize && (openChild || openPackage || shopBuy || adventureInfo || targetPreference || characterPreference || generalPreference || openDanji || sortItems || retainRequestBody(frame.ID, bodySamples)) {
					retained = true
					entry["hex"] = hex.EncodeToString(frame.Raw)
					if p, e := wire.DecryptPayload(keys, frame.ID, frame.Raw[13:]); e == nil {
						plaintext = p
						entry["plain_hex"] = hex.EncodeToString(p)
						verified = wire.Checksum(append(append([]byte{}, frame.Raw[11:13]...), p...)) == frame.Raw[7]
						entry["checksum_ok"] = verified
						if !openChild && !openPackage && !shopBuy && !adventureInfo && !targetPreference && !characterPreference && !generalPreference && !openDanji && !sortItems && !observedGameRequest(frame.ID) {
							entry["unimplemented_sample"] = true
						}
					} else {
						entry["decode_error"] = e.Error()
					}
				}
				// 节流：未实现且未被保留正文的高频命令（如 465 NGS 上报）不再逐条
				// 写事件，改为每 5000 次记一条计数汇总，防止 events.jsonl 无限膨胀。
				if retained || observedGameRequest(frame.ID) {
					event(entry)
				} else if !observedGameRequest(frame.ID) {
					throttledCalls[frame.ID]++
					if throttledCalls[frame.ID]%5000 == 0 {
						event(map[string]any{"kind": "throttled_client_frames", "peer": peer, "id": frame.ID, "total": throttledCalls[frame.ID]})
					}
				}
				if frame.Type == 1 && (frame.ID == 1200 || frame.ID == 1201) && characters != nil && bootstrapped && verified {
					plan, err := worldState.claimNeo(frame.ID, plaintext, developmentAccount, selectedCharacterID, characters.Store, lootService, wearService)
					if err != nil {
						event(map[string]any{"kind": "neo_claim_refused", "id": frame.ID, "character_id": selectedCharacterID, "reason": err.Error()})
						if sendPayload(1, frame.ID, protocol.Refusal(4)) != nil {
							return
						}
						continue
					}
					for _, p := range plan {
						if sendPayload(p.Kind, p.ID, p.Payload) != nil {
							return
						}
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID})
					}
					continue
				}
				if openDanji {
					if !verified || !bootstrapped || characters == nil || worldState == nil || lootService == nil || selectedCharacterID == 0 || worldState.role.ID != selectedCharacterID || worldState.role.AccountID != developmentAccount {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					saved, plan, code, err := openDanjiPot(ctx, characters.Store, developmentAccount, worldState.role, lootService.Catalog, lootService.BagRules, frame.Raw, plaintext, nil)
					cancel()
					if err != nil {
						event(map[string]any{"kind": "danji_open_refused", "character_id": selectedCharacterID, "code": code, "reason": err.Error()})
						if sendPayload(1, 2052, protocol.Refusal(code)) != nil {
							return
						}
						continue
					}
					worldState.role = saved
					for _, p := range plan {
						if sendPayload(p.Kind, p.ID, p.Payload) != nil {
							return
						}
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID})
					}
					continue
				}
				if sortItems {
					if !verified || !bootstrapped || characters == nil || worldState == nil || selectedCharacterID == 0 || worldState.role.ID != selectedCharacterID || worldState.role.AccountID != developmentAccount {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					saved, plan, space, err := sortInventory(ctx, characters.Store, developmentAccount, worldState.role, frame.Raw, plaintext)
					cancel()
					if err != nil {
						event(map[string]any{"kind": "sort_items_refused", "character_id": selectedCharacterID, "space": space, "reason": err.Error()})
						if sendPayload(1, 20, protocol.SortItemsRefused(space, 2)) != nil {
							return
						}
						continue
					}
					worldState.role = saved
					for _, p := range plan {
						if sendPayload(p.Kind, p.ID, p.Payload) != nil {
							return
						}
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID})
					}
					continue
				}
				if frame.Type == 1 && (frame.ID == protocol.CmdSortLock || frame.ID == protocol.CmdSortUnlock) {
					if !verified || !bootstrapped || characters == nil || worldState == nil || selectedCharacterID == 0 || worldState.role.ID != selectedCharacterID || worldState.role.AccountID != developmentAccount {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					saved, plan, err := setSortLock(ctx, characters.Store, developmentAccount, worldState.role, frame.Raw, plaintext, frame.ID == protocol.CmdSortLock)
					cancel()
					if err != nil {
						event(map[string]any{"kind": "sort_lock_refused", "id": frame.ID, "character_id": selectedCharacterID, "reason": err.Error()})
						if sendPayload(1, frame.ID, protocol.SortLockRefused(4)) != nil {
							return
						}
						continue
					}
					worldState.role = saved
					for _, p := range plan {
						if sendPayload(p.Kind, p.ID, p.Payload) != nil {
							return
						}
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID})
					}
					continue
				}
				if frame.Type == 1 && (frame.ID == protocol.CmdItemLock || frame.ID == protocol.CmdItemUnlock || frame.ID == protocol.CmdItemCancelUnlock || frame.ID == protocol.CmdItemUnlockNow) {
					if !verified || !bootstrapped || characters == nil || worldState == nil || selectedCharacterID == 0 || worldState.role.ID != selectedCharacterID || worldState.role.AccountID != developmentAccount {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					saved, plan, err := handleItemLockCommand(ctx, characters.Store, developmentAccount, worldState.role, frame.ID, frame.Raw, plaintext)
					cancel()
					if err != nil {
						event(map[string]any{"kind": "item_lock_refused", "id": frame.ID, "character_id": selectedCharacterID, "reason": err.Error()})
						refusal := protocol.Refusal(0x13)
						if frame.ID == protocol.CmdItemUnlock {
							refusal = protocol.ItemUnlockRefused(0x13, 0)
						}
						if sendPayload(1, frame.ID, refusal) != nil {
							return
						}
						continue
					}
					worldState.role = saved
					for _, p := range plan {
						if sendPayload(p.Kind, p.ID, p.Payload) != nil {
							return
						}
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID})
					}
					continue
				}
				if frame.ID == 19 && bootstrapped && verified && wearService != nil {
					plan, e := equipmentState.handle(wearService, worldState, plaintext, frame.Raw)
					if e != nil {
						event(map[string]any{"kind": "equipment_move_refused", "reason": e.Error()})
						r, _ := protocol.DecodeItemMove(plaintext)
						if e = sendPayload(1, 19, protocol.ItemMoveRefused(r)); e != nil {
							return
						}
						continue
					}
					prepared, e := preparePackets(keys, plan)
					if e != nil {
						event(map[string]any{"kind": "equipment_encode_error", "error": e.Error()})
						return
					}
					if e = writePackets(c, prepared, func(p preparedPacket) {
						event(map[string]any{"kind": p.Name, "character_id": worldState.role.ID, "id": p.ID, "plain_hex": hex.EncodeToString(p.Payload)})
					}); e != nil {
						event(map[string]any{"kind": "equipment_write_error", "error": e.Error()})
						return
					}
					continue
				}
				if frame.Type == 1 && frame.ID == 26 && bootstrapped && verified && wearService != nil {
					plan, e := disjointState.handle(wearService, worldState, plaintext, frame.Raw)
					if e != nil {
						event(map[string]any{"kind": "disjoint_refused", "character_id": selectedCharacterID, "reason": e.Error()})
						code := uint16(4)
						if strings.Contains(e.Error(), "bag category is full") {
							code = 22
						}
						if sendPayload(1, 26, protocol.DisjointRefused(code)) != nil {
							return
						}
						continue
					}
					prepared, e := preparePackets(keys, plan)
					if e != nil {
						event(map[string]any{"kind": "disjoint_encode_error", "error": e.Error()})
						return
					}
					if e = writePackets(c, prepared, func(p preparedPacket) {
						event(map[string]any{"kind": p.Name, "character_id": worldState.role.ID, "id": p.ID, "plain_hex": hex.EncodeToString(p.Payload)})
					}); e != nil {
						event(map[string]any{"kind": "disjoint_write_error", "error": e.Error()})
						return
					}
					continue
				}
				if frame.Type != 1 {
					event(map[string]any{"kind": "unsupported_client_type", "type": frame.Type})
					continue
				}
				if openChild {
					if !verified || !bootstrapped || characters == nil || worldState == nil || lootService == nil || selectedCharacterID == 0 || worldState.role.ID != selectedCharacterID || worldState.role.AccountID != developmentAccount {
						continue
					}
					var slot uint16
					if len(plaintext) >= 2 {
						slot = binary.LittleEndian.Uint16(plaintext)
					}
					saved, plan, box, award, quantity, applied, err := openBoosterBox(
						context.Background(),
						characters.Store,
						fatigueService,
						lootService,
						developmentAccount,
						worldState.role,
						frame.Raw,
						plaintext,
						nil,
					)
					if err != nil {
						event(map[string]any{"kind": "child_box_refused", "slot": slot, "reason": err.Error()})
						if sendPayload(1, 160, protocol.Refusal(4)) != nil {
							return
						}
						continue
					}
					saved.WireID = worldState.role.WireID
					worldState.role = saved
					for _, pkt := range plan {
						if sendPayload(pkt.Kind, pkt.ID, pkt.Payload) != nil {
							return
						}
					}
					event(map[string]any{"kind": "child_box_settled", "box": box, "award": award, "quantity": quantity, "slot": slot, "applied": applied})
					continue
				}

				if openPackage {
					if !verified || !bootstrapped || characters == nil || worldState == nil || lootService == nil || selectedCharacterID == 0 || worldState.role.ID != selectedCharacterID || worldState.role.AccountID != developmentAccount {
						continue
					}
					if len(plaintext) != 8 || !bytes.Equal(plaintext[2:], make([]byte, 6)) {
						if sendPayload(1, 510, protocol.Refusal(4)) != nil {
							return
						}
						continue
					}
					slot := binary.LittleEndian.Uint16(plaintext)
					key := fmt.Sprintf("open510/%x", sha256.Sum256(frame.Raw))
					role, applied, err := characters.Store.CommitCharacterEvent(context.Background(), developmentAccount, selectedCharacterID, lootService.Catalog.Source.Checksum, key, "US224-neo-cera-package-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
						if current.ConfigVersion != "53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69" || lootService.BagRules.Source != current.ConfigVersion {
							return nil, nil, fmt.Errorf("package source mismatch")
						}
						b, e := inventory.ReadBag(current.State)
						if e != nil {
							return nil, nil, e
						}
						found := false
						for i, it := range b.Items {
							if it.Slot == slot && it.Template == neoShopSale.Template && it.Amount == 1 {
								b.Items = append(b.Items[:i], b.Items[i+1:]...)
								found = true
								break
							}
						}
						if !found {
							return nil, nil, fmt.Errorf("slot does not hold the sealed Neo package")
						}
						// Exact (template, qty=1) pairs from US 00_pkg.stk [package data].
						items := [...]uint32{590005785, 590005786, 590005787, 590005788, 590005789, 590005790, 590005791, 590005792}
						for _, id := range items {
							b, _, e = b.Add(lootService.Catalog, lootService.BagRules, id, 1)
							if e != nil {
								return nil, nil, e
							}
						}
						if _, e = protocol.US224InventoryRestore(b.Rows()); e != nil {
							return nil, nil, e
						}
						state, e := inventory.SaveBag(current.State, b)
						if e != nil {
							return nil, nil, e
						}
						receipt, e := json.Marshal(map[string]any{"slot": slot, "source": neoShopSale.Template, "contents": items})
						return state, receipt, e
					})
					if err != nil {
						event(map[string]any{"kind": "package_open_refused", "character_id": selectedCharacterID, "slot": slot, "reason": err.Error()})
						code := uint16(4)
						if err.Error() == "bag category is full" {
							code = 22
						}
						if sendPayload(1, 510, protocol.Refusal(code)) != nil {
							return
						}
						continue
					}
					role.WireID = worldState.role.WireID
					worldState.role = role
					b, e := inventory.ReadBag(role.State)
					if e != nil {
						return
					}
					body, e := protocol.US224InventoryRestore(b.Rows())
					if e != nil {
						return
					}
					// ACK510 removes one from the old slot. Do not repeat that
					// client-side decrement when resending the authoritative list.
					if applied && sendPayload(1, 510, binary.LittleEndian.AppendUint16([]byte{1}, slot)) != nil {
						return
					}
					if sendPayload(0, 13, body) != nil {
						return
					}
					event(map[string]any{"kind": "package_open_settled", "character_id": selectedCharacterID, "slot": slot, "applied": applied})
					continue
				}

				if shopBuy {
					if !verified || !bootstrapped || selectedCharacterID == 0 || characters == nil || worldState == nil || worldState.role.ID != selectedCharacterID {
						continue
					}
					var product uint32
					if len(plaintext) >= 10 {
						product = binary.LittleEndian.Uint32(plaintext[6:])
					}
					isNeo := neoShopOrder(plaintext)
					lines, parseErr := premiumOrder(plaintext)
					if (!isNeo && parseErr != nil) || lootService == nil {
						event(map[string]any{"kind": "shop_buy_refused", "product": product, "reason": fmt.Sprint(parseErr)})
						if sendPayload(1, 64, neoShopAck(product, 20)) != nil {
							return
						}
						continue
					}
					order := fmt.Sprintf("buy64/%d/%x", selectedCharacterID, sha256.Sum256(frame.Raw))
					var applied bool
					var e error
					var delivery json.RawMessage
					if isNeo {
						var receipt storage.CashReceipt
						receipt, applied, e = characters.Store.CompleteCashSale(context.Background(), developmentAccount, selectedCharacterID, order, neoShopSale, shopBagDelivery(lootService.Catalog, lootService.BagRules))
						delivery = receipt.Delivery
						lines = []storage.CartLine{{Product: product}}
					} else {
						var receipt storage.CartReceipt
						receipt, applied, e = characters.Store.CompletePremiumCart(context.Background(), developmentAccount, selectedCharacterID, order, lines, premiumDelivery(lootService.Catalog, lootService.BagRules))
						delivery = receipt.Delivery
					}
					if e != nil {
						reason := uint16(20)
						if e.Error() == "insufficient CERA" || e.Error() == "insufficient gold" {
							reason = 11
						} else if e.Error() == "bag category is full" || e.Error() == "avatar bag is full" {
							reason = 4
						}
						event(map[string]any{"kind": "shop_buy_refused", "product": product, "reason": e.Error(), "character_id": selectedCharacterID, "lines": len(lines)})
						for i, l := range lines {
							if sendPayload(1, 64, cartAck(l.Product, reason, i, len(lines))) != nil {
								return
							}
						}
						continue
					}
					latest, e := characters.Store.LiveCharacter(developmentAccount, selectedCharacterID)
					if e != nil {
						return
					}
					bag, e := inventory.ReadBag(latest.State)
					if e != nil {
						return
					}
					inv, e := protocol.US224InventoryUpdate(bag.Rows())
					if e != nil {
						return
					}
					if applied {
						var ceraSpent int64
						if isNeo {
							ceraSpent = neoShopSale.Cera
						} else {
							for _, l := range lines {
								ceraSpent += l.Cera
							}
						}
						if ceraSpent > 0 {
							if ev := worldState.eventPolicy(); ev.Mileage.Enabled && ev.Mileage.ShopRebatePercent > 0 {
								rebate := ceraSpent * int64(ev.Mileage.ShopRebatePercent) / 100
								if rebate < 1 {
									rebate = 1
								}
								_, _, _ = characters.Store.AddAccountMileage(context.Background(), developmentAccount, rebate, "", 0)
							}
						}
					}
					bal, mileage, e := characters.Store.AccountBalances(context.Background(), developmentAccount)
					if e != nil {
						return
					}
					funds := fundsPayload(bal, mileage)
					latest.WireID = worldState.role.WireID
					worldState.role = latest
					event(map[string]any{"kind": "shop_buy_committed", "product": product, "character_id": selectedCharacterID, "delivery": delivery, "applied": applied, "lines": len(lines)})
					if sendPayload(0, 14, inv) != nil || sendPayload(0, 53, funds) != nil {
						return
					}
					for _, l := range lines {
						if l.Template == danjiPotTemplate {
							if djInv, djErr := danjiSpaceUpdatePayload(bag); djErr == nil {
								if sendPayload(0, 14, djInv) != nil {
									return
								}
							}
							break
						}
					}
					hasAvatar := false
					for _, l := range lines {
						if l.Kind == "avatar" {
							hasAvatar = true
							break
						}
					}
					if hasAvatar {
						if avInv, aerr := inventory.AvatarPayload(latest.State); aerr == nil {
							if sendPayload(0, 13, avInv) != nil {
								return
							}
						}
					}
					changed := map[byte]bool{}
					for _, l := range lines {
						if term, ok := l.Contract(); ok {
							changed[term.Kind] = true
						}
					}
					if len(changed) > 0 {
						entries, err := characters.Store.NeoPremiumSeconds(developmentAccount)
						if err != nil {
							event(map[string]any{"kind": "shop_premium_refresh_error", "reason": err.Error()})
						}
						for _, v := range entries {
							if changed[v.Kind] && sendPayload(0, 66, binary.LittleEndian.AppendUint64([]byte{2, 0, v.Kind}, v.Seconds)) != nil {
								return
							}
						}
					}
					for i, l := range lines {
						if sendPayload(1, 64, cartAck(l.Product, 0, i, len(lines))) != nil {
							return
						}
					}
					// Purchase is already committed. Refresh the same authoritative
					// counters as character entry, without granting or consuming FP.
					if changed[storage.NeoPremiumPlusKind] && fatigueService != nil {
						fp, ferr := fatigueService.State(context.Background(), developmentAccount, selectedCharacterID, time.Now())
						if ferr == nil {
							var update []byte
							update, ferr = protocol.Fatigue(fp.Used, fp.Limit, fp.UsedMax)
							if ferr == nil && sendPayload(0, 36, update) != nil {
								return
							}
						}
						if ferr != nil {
							event(map[string]any{"kind": "shop_fatigue_refresh_error", "reason": ferr.Error()})
						}
					}
					continue
				}

				if adventureInfo {
					if !bootstrapped || !verified || characters == nil || selectedCharacterID == 0 || worldState == nil || worldState.role.ID != selectedCharacterID ||
						len(plaintext) != 16 || binary.LittleEndian.Uint16(plaintext) != worldState.role.WireID || !bytes.Equal(plaintext[2:], make([]byte, 14)) {
						event(map[string]any{"kind": "adventure_overview_rejected", "reason": "not current actor or unsupported view"})
						continue
					}
					var account struct {
						Created time.Time `json:"created_at"`
					}
					if ok, e := characters.Store.Database().Get("accounts", strconv.FormatInt(developmentAccount, 10), &account); e != nil || !ok || account.Created.IsZero() {
						event(map[string]any{"kind": "adventure_overview_rejected", "reason": "missing account start date"})
						continue
					}
					// US v2.24 CMD1395 success reader: actor, calendar date,
					// four counters, u32, length-prefixed profile and name,
					// u16 detail count, u32, u16, u8. Empty variable sections
					// retain the native constructor defaults for an unranked club.
					p := make([]byte, 32)
					p[0] = 1
					binary.LittleEndian.PutUint16(p[1:], worldState.role.WireID)
					y, m, day := account.Created.Date()
					binary.LittleEndian.PutUint32(p[3:], uint32(y*10000+int(m)*100+day))
					if e := sendPayload(1, 1395, p); e != nil {
						return
					}
					event(map[string]any{"kind": "adventure_overview_sent", "character_id": selectedCharacterID, "actor": worldState.role.WireID, "bytes": len(p)})
					continue
				}
				if targetPreference {
					if !bootstrapped || !verified || selectedCharacterID == 0 || characters == nil {
						event(map[string]any{"kind": "guide_target_rejected", "reason": "no verified selected character"})
						continue
					}
					v, e := updateGuideTargets(characters.Store, developmentAccount, selectedCharacterID, plaintext)
					if e != nil {
						event(map[string]any{"kind": "guide_target_rejected", "error": e.Error()})
						// Release the UI's pending-click state with the last committed list.
						v = targetList{}
						if _, e = characters.Store.Database().Get(targetEquipmentTable, characterOptionKey(developmentAccount, selectedCharacterID), &v); e != nil {
							continue
						}
					} else {
						event(map[string]any{"kind": "guide_targets_saved", "character_id": selectedCharacterID, "templates": v})
					}
					if e = sendPayload(0, 2304, v.payload()); e != nil {
						return
					}
					continue
				}
				if frame.Type == 1 && frame.ID == 198 {
					if !verified || !bootstrapped || characters == nil || selectedCharacterID == 0 {
						continue
					}
					if _, ownErr := characters.Store.LiveCharacter(developmentAccount, selectedCharacterID); ownErr != nil {
						continue
					}
					body, err := saveBasicKeys(characters.Store, developmentAccount, plaintext)
					if err != nil {
						event(map[string]any{"kind": "basic_keys_refused", "error": err.Error()})
						continue
					}
					if body != nil {
						if err = sendPayload(0, 440, body); err != nil {
							return
						}
						event(map[string]any{"kind": "basic_keys_applied", "character_id": selectedCharacterID, "bytes": len(body) - 5})
					}
					continue
				}
				if generalPreference {
					if !bootstrapped || !verified || selectedCharacterID == 0 || characters == nil {
						event(map[string]any{"kind": "general_options_rejected", "reason": "no verified selected session"})
					} else if e := saveGeneralOptions(characters.Store, developmentAccount, plaintext); e != nil {
						event(map[string]any{"kind": "general_options_rejected", "error": e.Error()})
					} else {
						event(map[string]any{"kind": "general_options_saved", "ui_size": binary.LittleEndian.Uint16(plaintext[4+2*uiSizeIndex:])})
					}
					continue
				}
				if characterPreference {
					if !bootstrapped || !verified || selectedCharacterID == 0 || characters == nil {
						event(map[string]any{"kind": "character_options_rejected", "reason": "not verified and selected"})
					} else if e := saveCharacterOptions(characters.Store, developmentAccount, selectedCharacterID, plaintext); e != nil {
						event(map[string]any{"kind": "character_options_rejected", "error": e.Error()})
					} else {
						event(map[string]any{"kind": "character_options_saved", "character_id": selectedCharacterID})
					}
					continue
				}
				if bootstrapped && (frame.ID == 3 || frame.ID == 7 || frame.ID == 1301) {
					if !verified {
						event(map[string]any{"kind": "menu_rejected", "id": frame.ID, "reason": "checksum failed"})
						continue
					}
					option, e := protocol.DecodeMenuRequest(frame.ID, plaintext)
					if e != nil {
						event(map[string]any{"kind": "menu_rejected", "id": frame.ID, "reason": e.Error()})
						continue
					}
					payload := protocol.MenuLeaveSuccess()
					if frame.ID == 1301 {
						payload, e = worldState.returnDestination()
						if e != nil {
							event(map[string]any{"kind": "village_return_refused", "reason": e.Error()})
							// Native refusal clears the manager wait at143ca6b3f.
							payload = protocol.Refusal(4)
						}
					}
					if e = sendPayload(1, frame.ID, payload); e != nil {
						return
					}
					event(map[string]any{"kind": "menu_response", "id": frame.ID, "character_id": selectedCharacterID, "option": option, "plain_hex": hex.EncodeToString(payload)})
					if frame.ID == 3 || frame.ID == 7 {
						if notice != nil {
							gm.bind(notice, 0, 0)
						}
						selectedCharacterID = 0
						selectedBasic, selectedAddition = nil, nil
						clearSelectedWorld(worldState)
						if frame.ID == 3 {
							bootstrapped = false
						}
					}
					continue
				}
				if characters != nil && bootstrapped && frame.Type == 1 && (frame.ID == 28 || frame.ID == 29 || frame.ID == 331) {
					if !verified {
						continue
					}
					plan, e := skillState.handle(characters, worldState, frame.ID, plaintext, frame.Raw)
					if e != nil {
						event(map[string]any{"kind": "skill_refused", "id": frame.ID, "reason": e.Error()})
						plan = []outboundPacket{{"skill_refused_response", 1, frame.ID, protocol.Refusal(4)}}
					}
					for _, packet := range plan {
						if e = sendPayload(packet.Kind, packet.ID, packet.Payload); e != nil {
							return
						}
						event(map[string]any{"kind": packet.Name, "id": packet.ID, "character_id": selectedCharacterID, "plain_hex": hex.EncodeToString(packet.Payload)})
					}
					continue
				}
				if worldState != nil && bootstrapped && frame.ID == 44 && lootService != nil {
					if !verified {
						event(map[string]any{"kind": "item_use_rejected", "reason": "checksum failed"})
						continue
					}
					plan, e := worldState.useStackable(plaintext, frame.Raw)
					if e != nil {
						event(map[string]any{"kind": "item_use_refused", "character_id": worldState.role.ID, "reason": e.Error()})
						if r, decodeErr := protocol.DecodeUseStackable(plaintext); decodeErr == nil {
							if e = sendPayload(1, 44, protocol.UseStackableRefused(r)); e != nil {
								return
							}
						}
						continue
					}
					for _, packet := range plan {
						if e = sendPayload(packet.Kind, packet.ID, packet.Payload); e != nil {
							return
						}
						event(map[string]any{"kind": packet.Name, "character_id": worldState.role.ID, "id": packet.ID})
					}
					continue
				}
				if characters != nil && bootstrapped && frame.ID == 143 {
					if !verified || selectedCharacterID == 0 {
						event(map[string]any{"kind": "tutorial_rejected", "reason": "invalid checksum or no selected character"})
						continue
					}
					r, e := protocol.DecodeTutorialChange(plaintext)
					if e != nil {
						event(map[string]any{"kind": "tutorial_rejected", "reason": e.Error()})
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					e = characters.Store.SaveTutorialFlag(ctx, developmentAccount, selectedCharacterID, r.Index, r.Completed)
					cancel()
					if e != nil {
						event(map[string]any{"kind": "tutorial_save_error", "error": e.Error()})
						continue
					}
					if e = sendPayload(1, 143, protocol.TutorialChangeSaved()); e != nil {
						return
					}
					event(map[string]any{"kind": "tutorial_flag_saved", "character_id": selectedCharacterID, "index": r.Index, "completed": r.Completed, "rewards_granted": false})
					continue
				}
				if worldState != nil && bootstrapped && frame.ID == 15 {
					if !verified {
						event(map[string]any{"kind": "dungeon_gate_rejected", "reason": "checksum failed"})
						continue
					}
					plan, e := worldState.dungeonGate(plaintext)
					if e != nil {
						event(map[string]any{"kind": "dungeon_gate_rejected", "reason": e.Error()})
						if e = sendPayload(1, 15, protocol.Refusal(4)); e != nil {
							return
						}
						continue
					}
					prepared, e := preparePackets(keys, plan)
					if e != nil {
						event(map[string]any{"kind": "dungeon_encode_error", "error": e.Error()})
						continue
					}
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if e = writePackets(c, prepared, func(p preparedPacket) {
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID, "plain_hex": hex.EncodeToString(p.Payload)})
					}); e != nil {
						event(map[string]any{"kind": "dungeon_write_error", "error": e.Error()})
						return
					}
					worldState.selectingDungeon = true
					continue
				}
				if bootstrapped && selectedCharacterID == 0 && selectAck[frame.ID] > 0 {
					// Keep explicitly configured character-selection responses scoped
					// to the roster, before the selected character's command handlers.
					size := selectAck[frame.ID]
					if size < 1 {
						size = 1
					}
					payload := append([]byte{1}, make([]byte, size-1)...)
					if e := sendPayload(1, frame.ID, payload); e != nil {
						return
					}
					event(map[string]any{"kind": "select_ack_sent", "id": frame.ID, "bytes": size, "verified": verified})
					continue
				}
				if worldState != nil && bootstrapped && frame.ID == 217 {
					// v2.24 CMD217 is OVERFLOW_INFO. It can follow an unreadable
					// server packet while the client is still on the selection screen.
					event(map[string]any{"kind": "client_overflow_info", "id": frame.ID, "verified": verified, "character_id": worldState.role.ID, "plain_hex": hex.EncodeToString(plaintext)})
					continue
				}
				if worldState != nil && bootstrapped && (frame.ID == 16 || frame.ID == 37 || frame.ID == 39 || frame.ID == 42 || frame.ID == 43 || frame.ID == 45 || frame.ID == 46 || frame.ID == 69 || frame.ID == 70 || frame.ID == 71 || frame.ID == 72 || frame.ID == 117 || frame.ID == 132 || frame.ID == 475) {
					if !verified {
						event(map[string]any{"kind": "dungeon_request_rejected", "id": frame.ID, "reason": "checksum failed"})
						continue
					}
					var plan []outboundPacket
					var pending *dungeon.Session
					var e error
					switch frame.ID {
					case 16:
						pending, plan, e = worldState.selectDungeon(plaintext)
					case 37:
						plan, e = worldState.finishDungeonLoading(plaintext)
					case 39:
						plan, e = worldState.monsterDeath(plaintext)
					case 43:
						plan, e = worldState.pickup(plaintext)
					case 117:
						plan, e = worldState.bossCheck(plaintext)
					case 475:
						plan, e = worldState.lorienFinale(plaintext)
					case 45:
						pending, plan, e = worldState.moveDungeonRoom(plaintext)
					case 46:
						plan, e = worldState.dungeonResult(plaintext)
					case 69, 70:
						plan, e = worldState.cardStage(frame.ID, plaintext)
					case 71:
						plan, e = worldState.cardPick(plaintext)
					case 72:
						pending, plan, e = worldState.settlementExit(plaintext)
					case 132:
						plan, e = worldState.returnFromDungeonSelection(plaintext)
					case 42:
						if len(plaintext) != 0 {
							e = fmt.Errorf("unexpected give-up body")
						} else {
							plan, e = worldState.leaveDungeon()
						}
					}
					if e != nil {
						info := map[string]any{"kind": "dungeon_request_refused", "id": frame.ID, "reason": e.Error()}
						if frame.ID == 16 {
							if r, err := protocol.DecodeDungeonSelection(plaintext); err == nil {
								info["dungeon_id"] = r.ID
								info["difficulty"] = r.Difficulty
								info["worldmap"] = worldState.gateWorldmap()
								info["level"] = worldState.level
							}
						}
						event(info)
						if frame.ID == 69 || frame.ID == 70 {
							continue
						}
						if frame.ID == 71 {
							if sendPayload(1, 71, worldState.cardSnapshot()) != nil {
								return
							}
							continue
						}
						if frame.ID == 72 {
							if request, err := protocol.DecodeSettlementExit(plaintext); err == nil {
								if sendPayload(1, 72, protocol.SettlementExitRefused(request.Option)) != nil {
									return
								}
							}
							continue
						}
						if frame.ID == 43 {
							if request, decodeErr := protocol.DecodePickup(plaintext); decodeErr == nil {
								body, encodeErr := protocol.PickupRefused(request.Object)
								if encodeErr != nil || sendPayload(1, 43, body) != nil {
									return
								}
							}
							continue
						}
						// CMD39 failure reads a monster u16; NOTI132 has no generic
						// command refusal. Never send the generic error shape there.
						if frame.ID == 39 || frame.ID == 46 || frame.ID == 117 || frame.ID == 132 || frame.ID == 475 {
							continue
						}
						if e = sendPayload(1, frame.ID, protocol.Refusal(4)); e != nil {
							return
						}
						continue
					}
					prepared, e := preparePackets(keys, plan)
					if e != nil {
						event(map[string]any{"kind": "dungeon_encode_error", "error": e.Error()})
						// Domain state may already be committed. Close this failed
						// transport and restore from storage on reconnect.
						return
					}
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if e = writePackets(c, prepared, func(p preparedPacket) {
						event(map[string]any{"kind": p.Name, "id": p.ID, "character_id": selectedCharacterID, "plain_hex": hex.EncodeToString(p.Payload)})
					}); e != nil {
						return
					}
					if pending != nil {
						if frame.ID == 16 || frame.ID == 72 {
							worldState.deathSent = map[uint16]bool{}
							worldState.drops = nil
							worldState.resetCards()
						}
						worldState.activeDungeon = pending
						worldState.wornScenePending = true // next accepted town position, after leaving this dungeon
						worldState.completionSent = false
						worldState.resultSent = false
						worldState.selectingDungeon = false
						event(map[string]any{"kind": "dungeon_session_started", "dungeon": pending.Definition.ID, "maze": pending.Maze.Index, "map": pending.Room.Map, "monsters": len(pending.Monsters), "quests_changed": false})
					}
					if frame.ID == 37 {
						worldState.activeDungeon.Loaded = true
					}
					for _, p := range plan {
						if p.Name == "solo_party_initialized" {
							worldState.soloPartyReady = true
						}
						if p.Name == "card_scroll_ack" {
							worldState.cardScrolled = true
						}
						if p.Name == "card_layout_ack" {
							worldState.cardLayoutSent = true
						}
						if p.Name == "settlement_exit_ack" && pending == nil {
							worldState.activeDungeon = nil
							worldState.drops = nil
							worldState.deathSent = nil
							worldState.completionSent = false
							worldState.resultSent = false
							worldState.resetCards()
							worldState.selectingDungeon = p.Payload[2] == 1
						}
						if p.Name == "monster_death_confirmed" {
							if worldState.deathSent == nil {
								worldState.deathSent = map[uint16]bool{}
							}
							entity := uint16(p.Payload[0]) | uint16(p.Payload[1])<<8
							worldState.deathSent[entity] = true
							if worldState.drops != nil && len(worldState.drops.Skipped[entity]) > 0 {
								event(map[string]any{"kind": "drop_rules_pending", "entity": entity, "rules": worldState.drops.Skipped[entity]})
							}
						}
						if p.Name == "dungeon_clear_enabled" {
							worldState.completionSent = true
						}
						if p.Name == "dungeon_clear_reward" {
							worldState.resultSent = true
						}
					}
					if frame.ID == 42 {
						worldState.activeDungeon = nil
					}
					if frame.ID == 42 || frame.ID == 132 {
						worldState.selectingDungeon = false
					}
					continue
				}
				if worldState != nil && bootstrapped && frame.ID == 191 && verified {
					r, e := protocol.DecodeStoryPause(plaintext)
					if e != nil || worldState.activeDungeon == nil || worldState.role.ID == 0 {
						event(map[string]any{"kind": "story_pause_refused", "reason": "invalid state or absent owned dungeon"})
						continue
					}
					p, e := protocol.StoryPauseNotice(worldState.role.WireID, r)
					if e != nil {
						continue
					}
					if e = sendPayload(0, 170, p); e != nil {
						return
					}
					event(map[string]any{"kind": "story_pause_restored", "character_id": worldState.role.ID, "state": r.State, "story_kind": r.Kind})
					continue
				}
				if worldState != nil && bootstrapped && (frame.ID == 35 || frame.ID == 36) {
					if worldState.activeDungeon != nil {
						event(map[string]any{"kind": "dungeon_world_request_pending", "id": frame.ID, "origin_preserved": true})
						continue
					}
					if !verified {
						event(map[string]any{"kind": "world_rejected", "id": frame.ID, "error": "checksum rejected"})
						continue
					}
					if e := worldState.handle(frame.ID, plaintext, sendPayload, event); e != nil {
						event(map[string]any{"kind": "world_error", "id": frame.ID, "error": e.Error()})
					}
					continue
				}
				if questService != nil && bootstrapped && frame.ID == 33 && worldState != nil && verified {
					plan, e := worldState.questInteraction(plaintext)
					if e != nil {
						event(map[string]any{"kind": "quest_interaction_refused", "reason": e.Error()})
						continue
					}
					for _, p := range plan {
						if e = sendPayload(p.Kind, p.ID, p.Payload); e != nil {
							return
						}
						event(map[string]any{"kind": p.Name, "character_id": worldState.role.ID})
					}
					continue
				}
				if questService != nil && bootstrapped && frame.ID == 34 {
					if !verified || worldState.role.ID == 0 {
						event(map[string]any{"kind": "quest_rejected", "error": "invalid checksum or no character"})
						continue
					}
					r, e := protocol.DecodeQuestSubmit(plaintext)
					if e != nil {
						event(map[string]any{"kind": "quest_rejected", "error": e.Error()})
						continue
					}
					plan, e := worldState.finishQuest(r)
					if e != nil {
						event(map[string]any{"kind": "quest_submit_refused", "character_id": worldState.role.ID, "quest": r.ID, "reason": e.Error()})
						if e = sendPayload(1, 34, protocol.QuestSubmitRefused()); e != nil {
							return
						}
						continue
					}
					prepared, e := preparePackets(keys, plan)
					if e != nil {
						event(map[string]any{"kind": "quest_submit_encode_error", "error": e.Error()})
						return
					}
					if e = writePackets(c, prepared, func(p preparedPacket) {
						event(map[string]any{"kind": p.Name, "character_id": worldState.role.ID, "quest": r.ID})
					}); e != nil {
						event(map[string]any{"kind": "quest_submit_write_error", "quest": r.ID, "error": e.Error()})
						return
					}
					if worldState.answeredQuests == nil {
						worldState.answeredQuests = map[uint16]bool{}
					}
					worldState.answeredQuests[r.ID] = true
					continue
				}
				if questService != nil && bootstrapped && (frame.ID == 31 || frame.ID == 32) {
					if !verified || worldState.role.ID == 0 {
						event(map[string]any{"kind": "quest_rejected", "error": "invalid checksum or no selected character"})
						continue
					}
					qid, e := protocol.DecodeQuestRequest(frame.ID, plaintext)
					if e != nil {
						event(map[string]any{"kind": "quest_rejected", "error": e.Error()})
						continue
					}
					// Re-ACKing an accepted child on a reliable connection repeatedly reopens its tracker.
					if frame.ID == 31 && acceptedReplies[qid] {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					var response []byte
					if frame.ID == 31 {
						var state storage.QuestState
						state, e = questService.Accept(ctx, worldState.role, qid)
						response = protocol.QuestAccepted(qid, state.Progress)
					} else {
						e = characters.Store.AbandonQuest(ctx, developmentAccount, worldState.role.ID, qid)
						response = protocol.QuestAbandoned(qid)
					}
					cancel()
					if e != nil {
						event(map[string]any{"kind": "quest_rejected", "quest": qid, "error": e.Error()})
						continue
					}
					if e = sendPayload(1, frame.ID, response); e != nil {
						return
					}
					if frame.ID == 31 {
						acceptedReplies[qid] = true
					} else {
						delete(acceptedReplies, qid)
						delete(worldState.answeredQuests, qid)
					}
					event(map[string]any{"kind": "quest_saved_and_sent", "character_id": worldState.role.ID, "quest": qid, "operation": frame.ID, "plain_hex": hex.EncodeToString(response), "client_acceptance": "pending"})
					if frame.ID == 31 {
						// A quest is normally accepted while standing at the
						// very NPC it names, so its objective can already be
						// satisfied the moment it is accepted.
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						e = worldState.settleProximityObjectives(ctx, sendPayload, event)
						cancel()
						if e != nil {
							return
						}
					}
					ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
					list, le := worldState.availableQuestPayload(ctx2)
					cancel2()
					if le != nil {
						event(map[string]any{"kind": "available_quests_refresh_error", "error": le.Error()})
					} else if le = sendPayload(0, 21, list); le != nil {
						return
					}
					// Publish the authoritative parent + child after updating the
					// offered list. Reopening the quest book must not be required.
					if qid == 3157 {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						plan, te := worldState.activeTracker(ctx, "mirkwood_tracker_refresh")
						cancel()
						if te != nil {
							event(map[string]any{"kind": "mirkwood_tracker_error", "error": te.Error()})
							continue
						}
						for _, p := range plan {
							if sendPayload(p.Kind, p.ID, p.Payload) != nil {
								return
							}
							event(map[string]any{"kind": p.Name, "character_id": selectedCharacterID, "quest": qid, "plain_hex": hex.EncodeToString(p.Payload)})
						}
					}

					continue
				}
				if characters != nil && bootstrapped && frame.ID == 4 && selectProbe != nil {
					if !verified {
						event(map[string]any{"kind": "select_rejected", "error": "checksum or cipher rejected"})
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					beforeSelect := gm.beforeSelect()
					role, e := characters.Select(ctx, developmentAccount, plaintext)
					cancel()
					if e != nil {
						event(map[string]any{"kind": "select_rejected", "error": e.Error()})
						continue
					}
					clear(acceptedReplies)
					profile := *selectProbe
					ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
					profile.TutorialCompleted, e = characters.Store.TutorialFlags(ctx, developmentAccount, role.ID)
					cancel()
					if e != nil {
						event(map[string]any{"kind": "tutorial_restore_error", "error": e.Error()})
						continue
					}
					event(map[string]any{"kind": "tutorial_flags_restored", "character_id": role.ID, "completed": profile.TutorialCompleted})
					profile.CreatedTime = uint32(role.CreatedAt.Unix())
					// Cera is an account balance the client reads from this
					// response. Without this it stayed at the configured zero,
					// so an operator top-up could never be seen in game.
					ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
					cera, entryMileage, ce := characters.Store.AccountBalances(ctx, developmentAccount)
					cancel()
					if ce != nil {
						event(map[string]any{"kind": "cera_restore_error", "error": ce.Error()})
						continue
					}
					if cera > 0xffffffff {
						cera = 0xffffffff
					}
					profile.Cash = uint32(cera)
					premiums, pe := characters.Store.NeoPremiumSeconds(developmentAccount)
					if pe != nil {
						event(map[string]any{"kind": "premium_restore_error", "reason": pe.Error()})
						continue
					}
					profile.Premium = nil
					for _, v := range premiums {
						profile.Premium = append(profile.Premium, protocol.US224Premium{Kind: v.Kind, Seconds: v.Seconds})
					}

					event(map[string]any{"kind": "cera_restored", "account": developmentAccount, "cera": profile.Cash})
					// This loopback probe has one development account. A shared
					// multiplayer world will allocate its own unique actor IDs.
					profile.ActorServerID = role.WireID
					var fatiguePayload []byte
					if fatigueService != nil {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						fp, err := fatigueService.State(ctx, developmentAccount, role.ID, time.Now())
						cancel()
						if err != nil {
							event(map[string]any{"kind": "fatigue_restore_error", "error": err.Error()})
							continue
						}
						profile.Fatigue = [3]uint16{fp.Used, fp.Limit, fp.UsedMax}
						fatiguePayload, err = protocol.Fatigue(fp.Used, fp.Limit, fp.UsedMax)
						if err != nil {
							event(map[string]any{"kind": "fatigue_restore_error", "error": err.Error()})
							continue
						}
						event(map[string]any{"kind": "fatigue_restored", "character_id": role.ID, "state": fp})
					}
					if questService != nil {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						profile.ActiveQuests, e = questService.Active(ctx, role)
						cancel()
						if e != nil {
							// A character saved against an older archive must not be
							// shown under the new source's quests, and a restore gap
							// must not block town entry. Record the gap and enter with
							// an empty active list; no legacy state is rewritten.
							event(map[string]any{"kind": "quest_restore_rejected", "error": e.Error(), "character_id": role.ID})
							profile.ActiveQuests = nil
							e = nil
						}
					}
					var basic []byte
					var addition []byte
					var statProbe []byte
					var statResend []byte
					var vaultPayload []byte
					if vaultService != nil {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						vaultPayload, e = vaultService.Bootstrap(ctx, role)
						cancel()
						if e != nil {
							event(map[string]any{"kind": "vault_entry_rejected", "error": e.Error()})
							continue
						}
					}
					var areaPayload []byte
					if *townProbeFile != "" {
						var state character.State
						if e = json.Unmarshal(role.State, &state); e != nil || !townCatalog.Allows(state.Level, townPolicy.X, townPolicy.Y) {
							event(map[string]any{"kind": "town_entry_rejected", "error": "character level or spawn policy incompatible with source area"})
							continue
						}
						areaPayload, e = protocol.AreaUsers(townCatalog.TownID, townCatalog.AreaID, []protocol.AreaUser{{ActorServerID: role.WireID, X: townPolicy.X, Y: townPolicy.Y, Flags: townPolicy.Flags}})
						if e != nil {
							event(map[string]any{"kind": "town_entry_error", "error": e.Error()})
							continue
						}
					}
					if *entryBasicProbe {
						basic, e = characters.EntryBasicProbe(role, channelCtx)
						if e != nil {
							event(map[string]any{"kind": "entry_basic_error", "error": e.Error()})
							continue
						}
					}
					if *entryAdditionProbe && *entryAdditionMarker {
						// Semantic recovery: the US100 build reads a different
						// mode-1 entry than the 2.38 layout; markers plus a
						// frida read trace map the fields.
						addition, e = protocol.UserInfoAdditionMarkerUS100(role.WireID, 0x1122334455667788)
						if e != nil {
							event(map[string]any{"kind": "entry_addition_error", "error": e.Error()})
							continue
						}
					} else if *entryNoneLong > 0 {
						addition, e = protocol.UserInfoAdditionNoneLongUS100(role.WireID, *entryNoneLong)
						if e != nil {
							event(map[string]any{"kind": "entry_addition_error", "error": e.Error()})
							addition = nil
						}
					} else if *entryNone {
						addition = protocol.UserInfoAdditionNoneAppearanceUS100(role.WireID)
					} else if *entryLong > 0 {
						addition, e = protocol.UserInfoAdditionLongUS100(role.WireID, *entryLong)
						if e != nil {
							event(map[string]any{"kind": "entry_addition_error", "error": e.Error()})
							addition = nil
						}
					} else if *entryRamp {
						// Entry-phase probe: the block's semantics cannot be
						// tested mid-session because the client settles stats
						// only while it creates the actor.
						addition = protocol.UserInfoAdditionRampUS100(role.WireID)
					} else if *entryStub {
						// Legacy comparison mode omits local character attributes.
						addition = protocol.UserInfoAdditionStubUS100()
					} else if *entryAdditionProbe {
						// A probe payload must never block town entry: report the
						// error and continue with the rest of the sequence, the
						// same policy the quest/inventory gaps already use.
						switch {
						case *entryAppearanceProbe:
							// Historical flag: the appearance-filled block is
							// now the default, so this sends the zero variant.
							addition, e = characters.EntryAdditionZeroBlockUS100(role)
						case *entryAdditionHP:
							if *entryAdditionFlag13 {
								addition, e = characters.EntryAdditionHPFlag13US100(role)
							} else {
								addition, e = characters.EntryAdditionHPMaxUS100(role)
							}
						case *entryAdditionStats:
							addition, e = characters.EntryAdditionStatsUS100(role)
						default:
							addition, e = characters.EntryAdditionUS100(role)
						}
						if e != nil {
							event(map[string]any{"kind": "entry_addition_error", "error": e.Error()})
							addition = protocol.UserInfoAdditionStubUS100()
						}
					}
					// The stat probe and the mode report sit AFTER the addition
					// chain, not inside it: the chain picks the first matching
					// branch (the marker payload wins over the plain probe), so
					// anything nested in a later branch never runs. That is how
					// the mode-6 packet stopped being sent on marker runs.
					if *entryStatProbe {
						statProbe, e = characters.EntryStatProbeUS100(role)
						if e != nil {
							event(map[string]any{"kind": "entry_stat_error", "error": e.Error()})
							statProbe = nil
						}
					}
					if *entryStatMarker {
						// Distinctive values in the four u32 slots the mode-6 pair
						// consumer reads, so a probe can tell whether the client
						// consumed OUR structure or its own. Small and clean on
						// purpose: the HUD is the readout, and 777/888 cannot be
						// confused with the mode-1 block's hp_max×10 values
						// (1800/1400 by default), so one run names the channel.
						statProbe, e = protocol.UserInfoStatProbeUS100(role.WireID, statMarkerHP, statMarkerMP)
						if e != nil {
							event(map[string]any{"kind": "entry_stat_error", "error": e.Error()})
							statProbe = nil
						}
						// The end-of-sequence resend carries marker+1 so the pair
						// setters see changed values and run the consumption
						// chain again — the re-settle that the entry ordering
						// (settle fires inside mode-0, before any mode-1 apply)
						// can never provide on its own.
						statResend, e = protocol.UserInfoStatProbeUS100(role.WireID, statMarkerHP+1, statMarkerMP+1)
						if e != nil {
							event(map[string]any{"kind": "entry_stat_error", "error": e.Error()})
							statResend = nil
						}
					}
					addition = protocol.WithUserInfoContext(addition, channelCtx)
					statProbe = protocol.WithUserInfoContext(statProbe, channelCtx)
					statResend = protocol.WithUserInfoContext(statResend, channelCtx)
					// Which entry payloads were actually resolved. The flags overlap
					// (stub wins over probe, probe is required for marker), so
					// without this line a run can look like a marker run while
					// sending the stub.
					event(map[string]any{"kind": "entry_addition_mode",
						"probe": *entryAdditionProbe, "marker": *entryAdditionMarker,
						"stub": *entryStub, "ramp": *entryRamp, "long": *entryLong,
						"none": *entryNone, "none_long": *entryNoneLong,
						"stat_probe": *entryStatProbe, "stat_marker": *entryStatMarker,
						"stat_hp": statMarkerHP, "stat_mp": statMarkerMP,
						"addition_hp":    *entryAdditionHP,
						"addition_stats": *entryAdditionStats,
						"addition_bytes": len(addition), "stat_bytes": len(statProbe)})
					if worldState != nil {
						e = worldState.enter(role, storage.WorldPosition{Town: townCatalog.TownID, Area: townCatalog.AreaID, X: townPolicy.X, Y: townPolicy.Y})
						if e != nil {
							event(map[string]any{"kind": "world_entry_error", "error": e.Error()})
							continue
						}
						areaPayload, e = worldState.areaPayload()
						if e != nil {
							event(map[string]any{"kind": "world_entry_error", "error": e.Error()})
							continue
						}
					}
					payload, e := protocol.SelectProbeSuccess(profile)
					if e != nil {
						event(map[string]any{"kind": "entry_prepare_error", "error": e.Error()})
						continue
					}
					var userArea []byte
					if worldState != nil && len(areaPayload) > 0 {
						userArea, e = worldState.userAreaPayload()
						if e != nil {
							event(map[string]any{"kind": "entry_prepare_error", "error": e.Error()})
							continue
						}
					}
					plan := entryPayloads{Select: payload, Basic: basic, Addition: addition, Stat: statProbe, StatResend: statResend, Vault: vaultPayload, UserArea: userArea, Area: areaPayload, Fatigue: fatiguePayload, AccountOptions: accountOptionsPayload}
					if characters != nil {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						migratedRole, accountMats, matErr := inventory.MigrateBagCubes(ctx, characters.Store, role)
						cancel()
						if matErr != nil {
							event(map[string]any{"kind": "entry_account_materials_error", "error": matErr.Error(), "character_id": role.ID})
						} else {
							role = migratedRole
							if worldState != nil {
								worldState.role = role
							}
							if matPayload, encErr := protocol.US224AccountMaterialsFromMap(true, accountMats); encErr != nil {
								event(map[string]any{"kind": "entry_account_materials_error", "error": encErr.Error(), "character_id": role.ID})
							} else {
								plan.AccountMaterials = matPayload
							}
						}
					}
					if wearService != nil {
						plan.Worn, e = inventory.WornPayload(role.State)
						if e == nil {
							plan.WornUpdate, e = inventory.WornSpaceUpdate(role.State)
						}
						if e == nil {
							plan.Avatars, e = inventory.AvatarPayload(role.State)
						}
						if e != nil {
							event(map[string]any{"kind": "entry_worn_error", "error": e.Error()})
							continue
						}
					}
					if lootService != nil {
						plan.Inventory, e = lootService.Bootstrap(role)
						if e != nil {
							// Same policy as quest/world migration gaps: a bag saved
							// under an older archive is not reinterpreted, the
							// character enters with the restore omitted, and the
							// gap is recorded.
							event(map[string]any{"kind": "entry_inventory_error", "error": e.Error(), "character_id": role.ID})
							plan.Inventory = nil
							e = nil
						}
					}
					if progressionService != nil {
						plan.Experience, e = character.ExperiencePayload(role)
						if e != nil {
							event(map[string]any{"kind": "entry_experience_error", "error": e.Error()})
							continue
						}
						if questService != nil {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							ids, qe := questService.Completed(ctx, role)
							if qe == nil {
								plan.CompletedQuests, qe = protocol.CompletedQuests(ids)
							}
							if qe == nil {
								available, ae := questService.Available(ctx, role)
								if ae != nil {
									qe = ae
								} else {
									plan.AvailableQuests, qe = protocol.AvailableQuests(plan.Experience[0], available)
								}
							}
							cancel()
							if qe != nil {
								// Same policy as the active-quest restore above: an
								// older archive's quest history stays out of this
								// entry instead of blocking it. The completed and
								// available lists both go empty.
								event(map[string]any{"kind": "entry_completed_quests_error", "error": qe.Error(), "character_id": role.ID})
								plan.CompletedQuests, plan.AvailableQuests = nil, nil
							}
						}
					}
					if len(addition) > 0 {
						plan.Skills, e = characters.EntrySkills(role)
						if e != nil {
							// Skill state saved under an older archive is not
							// reinterpreted; entry proceeds without the restore and
							// the gap is recorded.
							event(map[string]any{"kind": "entry_skills_error", "error": e.Error(), "character_id": role.ID})
							plan.Skills = nil
							e = nil
						}
					}
					if len(addition) > 0 && len(areaPayload) > 0 {
						plan.Complete = protocol.EnterGameworldComplete()
					}
					if options, readErr := generalOptionsOnEntry(characters.Store, developmentAccount); readErr != nil {
						event(map[string]any{"kind": "general_options_read_error", "error": readErr.Error()})
					} else {
						plan.GeneralOptions = options
					}
					nowEntry := time.Now()
					plan.TimeSync = protocol.UserInfoTimeSyncUS100(uint32(nowEntry.Unix()))
					if eventsBody, evErr := buildActiveEventsPayload(events.LoadBestEffort(eventsPath), nowEntry); evErr != nil {
						event(map[string]any{"kind": "active_events_encode_error", "error": evErr.Error()})
					} else {
						plan.Events = eventsBody
						if worldState != nil {
							worldState.lastSyncedEvents = string(eventsBody)
						}
					}
					if danjiBody, djErr := danjiStatusOnEntry(characters.Store, developmentAccount); djErr != nil {
						event(map[string]any{"kind": "danji_status_read_error", "error": djErr.Error()})
					} else {
						plan.DanjiStatus = danjiBody
					}
					if djInv, djErr := danjiSpaceRestorePayload(role.State); djErr != nil {
						event(map[string]any{"kind": "danji_inventory_read_error", "error": djErr.Error()})
					} else {
						plan.DanjiInventory = djInv
					}
					if locksBody, lockErr := itemLocksOnEntry(role.State); lockErr != nil {
						event(map[string]any{"kind": "item_locks_read_error", "error": lockErr.Error()})
					} else {
						plan.ItemLocks = locksBody
					}
					packets := plan.packets()
					if body, ke := basicKeysOnEntry(characters.Store, developmentAccount); ke != nil {
						event(map[string]any{"kind": "basic_keys_read_error", "error": ke.Error()})
					} else if body != nil {
						packets = append(packets, outboundPacket{"basic_keys_restored", 0, 440, body})
					}

					if len(profile.ActiveQuests) > 0 {
						if body, err := protocol.QuestTriggers(profile.ActiveQuests); err == nil {
							packets = append(packets, outboundPacket{"active_quest_objectives_restored", 0, 291, body})
						}
					}
					var goals targetList
					if ok, err := characters.Store.Database().Get(targetEquipmentTable, characterOptionKey(developmentAccount, role.ID), &goals); err != nil {
						event(map[string]any{"kind": "guide_target_read_error", "character_id": role.ID, "error": err.Error()})
					} else if ok {
						packets = append(packets, outboundPacket{"guide_targets_restored", 0, 2304, goals.payload()})
					}
					// Restore after skill initialization; omit a missing row so new
					// characters retain the client's own defaults.
					if options, readErr := loadCharacterOptions(characters.Store, developmentAccount, role.ID); readErr != nil {
						event(map[string]any{"kind": "character_options_read_error", "character_id": role.ID, "error": readErr.Error()})
					} else if options != nil {
						packets = append(packets, outboundPacket{"character_options_restored", 0, 376, options})
					}
					if entryMileage > 0 {
						packets = append(packets, outboundPacket{"account_funds_restored", 0, 53, fundsPayload(cera, entryMileage)})
					}
					if worldState != nil && eventsPath != "" {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						autoPkts := worldState.onActivityLogin(ctx)
						cancel()
						if len(autoPkts) > 0 {
							packets = append(packets, autoPkts...)
						}
					}
					prepared, e := preparePackets(keys, packets)
					if e != nil {
						event(map[string]any{"kind": "entry_encode_error", "character_id": role.ID, "error": e.Error(), "frames_sent": 0})
						continue
					}
					event(map[string]any{"kind": "entry_preflight_passed", "character_id": role.ID, "frame_count": len(prepared)})
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					e = writePackets(c, prepared, func(p preparedPacket) {
						entry := map[string]any{"kind": p.Name, "character_id": role.ID, "actor_server_id": role.WireID, "type": p.Kind, "id": p.ID, "plain_bytes": len(p.Payload), "client_acceptance": "pending"}
						// Layout recovery replays these payloads through
						// -inject-watch, which needs the exact bytes.
						if p.ID == 2 || p.ID == 4 || p.ID == 19 || p.ID == 23 || p.ID == 24 || p.ID == 36 {
							entry["plain_hex"] = hex.EncodeToString(p.Payload)
						}
						if p.ID == 4 {
							entry["name"] = role.Name
						}
						if p.ID == 23 || p.ID == 24 {
							if worldState != nil {
								entry["position"] = worldState.state.Position
							} else {
								entry["town_id"], entry["area_id"] = townCatalog.TownID, townCatalog.AreaID
							}
						}
						if p.ID == 13 || p.ID == 36 {
							entry["plain_hex"] = hex.EncodeToString(p.Payload)
						}
						event(entry)
					})
					if e != nil {
						event(map[string]any{"kind": "entry_write_error", "character_id": role.ID, "error": e.Error()})
						return
					}
					selectedCharacterID = role.ID
					if notice != nil {
						gm.bind(notice, role.ID, beforeSelect)
					}
					selectedBasic, selectedAddition = basic, addition
					continue
				}
				if characters != nil && bootstrapped && selectedCharacterID == 0 && selectAck[frame.ID] > 0 {
					// Commands the client sends while it is still on the character
					// select screen and this server never answered. The reference
					// session answers every command it receives there (SEASON /
					// MERCENARY / HIDDEN all return "Result : Ok"), so an unanswered
					// command is a candidate for a stalled select screen.
					size := selectAck[frame.ID]
					if size < 1 {
						size = 1
					}
					payload := append([]byte{1}, make([]byte, size-1)...)
					if e := sendPayload(1, frame.ID, payload); e != nil {
						return
					}
					event(map[string]any{"kind": "select_ack_sent", "id": frame.ID, "bytes": size, "verified": verified})
					continue
				}
				if characters != nil && bootstrapped && frame.ID == 637 {
					if verified && len(plaintext) == 0 && selectedCharacterID == 0 {
						// 145250040 reads the count of pending delayed deletions.
						// Local archival is immediate, so no pending timer rows.
						// The reference build received eight bytes for
						// CHARAC_VIEW_HIDDEN_CHARAC_INFO ("(Size : 8) Result : Ok").
						if e := sendPayload(1, 637, append([]byte{1}, make([]byte, 7)...)); e != nil {
							return
						}
					}
					continue
				}
				if characters != nil && bootstrapped && (frame.ID == 433 || frame.ID == 848) {
					if !verified {
						event(map[string]any{"kind": "roster_followup_rejected", "id": frame.ID, "error": "checksum or cipher rejected"})
						continue
					}
					var payload []byte
					var e error
					if frame.ID == 433 {
						payload, e = protocol.EmptyMercenaryInfo(plaintext)
					} else {
						payload, e = protocol.ProbeRosterCounters(plaintext)
					}
					if e != nil {
						event(map[string]any{"kind": "roster_followup_rejected", "id": frame.ID, "error": e.Error()})
						continue
					}
					encrypted, e := wire.EncryptPayload(keys, frame.ID, payload)
					if e != nil {
						event(map[string]any{"kind": "roster_followup_error", "error": e.Error()})
						return
					}
					response, e := wire.ServerFrame(1, frame.ID, encrypted)
					if e != nil {
						return
					}
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if _, e = io.Copy(c, bytes.NewReader(response)); e != nil {
						return
					}
					// Log every reply shape: the KR client's request ids differ from the
					// US reference, so "wrong reply size" is a prime suspect for the
					// disabled select screen.
					event(map[string]any{"kind": "roster_followup_response", "id": frame.ID,
						"bytes": len(response), "payload": len(payload), "hex": hex.EncodeToString(payload)})
					continue
				}
				if characters != nil && bootstrapped && (frame.ID == 5 || frame.ID == 6 || frame.ID == 684 || frame.ID == 8) {
					if !verified {
						event(map[string]any{"kind": "character_rejected", "id": frame.ID, "error": "request checksum or cipher unsupported"})
						return
					}
					var userInfoMode byte
					var userInfoUID uint16
					if frame.ID == 8 {
						uid, mode, e := protocol.DecodeUserInfoRequest(plaintext)
						if e != nil {
							event(map[string]any{"kind": "userinfo_rejected", "error": e.Error()})
							continue
						}
						userInfoMode = mode
						userInfoUID = uid
						nameHistoryOK := mode == 5 && worldState != nil && worldState.role.ID == selectedCharacterID && selectedCharacterID != 0 && (uid == worldState.role.WireID || uid == 0xffff)
						if !((mode == 2 && uid == 0xffff) || (mode == 0 && uid == 0xffff && len(selectedBasic) > 0) || (mode == 1 && uid == 0xffff && len(selectedAddition) > 0) || nameHistoryOK) {
							event(map[string]any{"kind": "userinfo_mode_pending", "mode": mode, "uid": uid, "selected_character_id": selectedCharacterID})
							continue
						}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					var payload []byte
					created := false
					var err error
					kind, id := byte(1), frame.ID
					switch frame.ID {
					case 6:
						var req protocol.DeleteCharacterRequest
						var deletedID int64
						req, err = protocol.DecodeDeleteCharacter(plaintext)
						if err == nil && selectedCharacterID != 0 {
							err = fmt.Errorf("delete requires character selection screen")
						}
						if err == nil {
							deletedID, err = characters.Store.DeleteCharacter(ctx, developmentAccount, req.Slot, req.Name)
						}
						if err == nil {
							created = true // refresh complete roster after the native removal callback
							payload = protocol.DeleteCharacterSuccess(req.Slot)
							event(map[string]any{"kind": "character_archived", "character_id": deletedID, "slot": req.Slot, "name": req.Name})
						}
					case 684:
						payload, err = characters.CheckName(ctx, plaintext)
					case 5:
						var role storage.Character
						role, err = characters.Create(ctx, developmentAccount, plaintext)
						if err == nil {
							var slot uint16
							slot, err = characters.RosterSlot(ctx, developmentAccount, role.ID)
							if err == nil {
								created = true
								payload = protocol.CreateSuccess(slot, role.Name)
								// Only a character created from here on owes a
								// starting route; every earlier character was
								// backfilled as already finished.
								owed := "not tracked"
								if tutorialRoutes != nil {
									if e := characters.Store.StartBirth(ctx, developmentAccount, role.ID); e != nil {
										owed = "record failed: " + e.Error()
									} else {
										owed = "pending"
									}
								}
								event(map[string]any{"kind": "character_committed", "id": role.WireID, "slot": slot, "name": role.Name, "profession": role.Profession, "config_version": role.ConfigVersion, "starting_route": owed})
							}
						}
					case 8:
						kind, id = 0, 2
						// The reply shape is a development knob: the US reference
						// answers NOTI 0/ID2 (its "entry character packet"), but the
						// KR build may expect the character list under another
						// kind/id, including as the command's own response.
						if *userInfoReply != "" {
							var rk, rid int
							if _, serr := fmt.Sscanf(*userInfoReply, "%d:%d", &rk, &rid); serr == nil {
								kind, id = uint8(rk), uint16(rid)
							}
						}
						if *rosterDelay > 0 {
							// The roster normally arrives about three seconds after
							// connecting, while the select screen only appears much
							// later; delaying the reply tests whether the UI builds
							// its list only from a roster it is already waiting for.
							time.Sleep(time.Duration(*rosterDelay) * time.Second)
						}
						if *noRoster {
							// Development knob: leave the GET_USERINFO reply out so a
							// roster injected under another id can be examined alone.
							event(map[string]any{"kind": "roster_suppressed", "id": frame.ID})
							continue
						}
						if userInfoMode == 0 {
							payload = selectedBasic
							if worldState != nil && worldState.role.ID == selectedCharacterID {
								payload, err = characters.EntryBasicProbe(worldState.role, channelCtx)
							}
						} else if userInfoMode == 1 {
							payload = selectedAddition
							if worldState != nil && worldState.role.ID == selectedCharacterID {
								if *rosterV224 && *entryAdditionProbe && *entryAdditionStats {
									payload, err = characters.EntryAdditionStatsUS100(worldState.role)
								} else {
									payload, err = characters.EntryAddition(worldState.role)
								}
							}
						} else if userInfoMode == 5 && worldState != nil && worldState.role.ID == selectedCharacterID {
							roleForHistory := worldState.role
							if userInfoUID != 0 && userInfoUID != 0xffff {
								roleForHistory.WireID = userInfoUID
							}
							payload, err = nameHistoryPayload(roleForHistory, channelCtx[0], time.Now())
						} else {
							payload, err = characters.ListWithFatigue(ctx, developmentAccount, fatigueService, time.Now())
						}
						if err == nil {
							payload = protocol.WithUserInfoContext(payload, channelCtx)
						}
					}
					cancel()
					if err != nil {
						event(map[string]any{"kind": "character_rejected", "id": frame.ID, "error": err.Error()})
						// Both native creation/name handlers explicitly handle code 2
						// and restore input state. Never drop a valid connection for
						// a business refusal; other code meanings remain unverified.
						kind, id = 1, frame.ID
						payload = protocol.Refusal(2)
					}
					ciphertext, err := wire.EncryptPayload(keys, id, payload)
					if err != nil {
						event(map[string]any{"kind": "character_error", "error": err.Error()})
						return
					}
					response, err := wire.ServerFrame(kind, id, ciphertext)
					if err != nil {
						return
					}
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if _, err = io.Copy(c, bytes.NewReader(response)); err != nil {
						return
					}
					event(map[string]any{"kind": "character_response", "id": id, "bytes": len(response), "payload": len(payload), "roster_pad": protocol.RosterRowPad, "hex": hex.EncodeToString(response)})
					// Development knob: re-send the same reply inside the SAME
					// connection. The client parses a repeated roster into the
					// buffers it already holds (the parse allocates nothing), which
					// is what makes its writer catchable with a page watch.
					if *rosterRepeat > 0 {
						go func(resp []byte, rid uint16) {
							for i := 0; i < *rosterRepeat; i++ {
								time.Sleep(time.Duration(*rosterRepeatDelayMS) * time.Millisecond)
								c.SetWriteDeadline(time.Now().Add(5 * time.Second))
								if _, werr := io.Copy(c, bytes.NewReader(resp)); werr != nil {
									return
								}
								event(map[string]any{"kind": "roster_repeat", "n": i + 1, "id": rid, "bytes": len(resp)})
							}
						}(response, id)
					}
					if created {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						list, e := characters.ListWithFatigue(ctx, developmentAccount, fatigueService, time.Now())
						cancel()
						if e != nil {
							event(map[string]any{"kind": "character_list_error", "error": e.Error()})
							continue
						}
						list = protocol.WithUserInfoContext(list, channelCtx)
						encrypted, e := wire.EncryptPayload(keys, 2, list)
						if e != nil {
							return
						}
						notification, e := wire.ServerFrame(0, 2, encrypted)
						if e != nil {
							return
						}
						c.SetWriteDeadline(time.Now().Add(5 * time.Second))
						if _, e = io.Copy(c, bytes.NewReader(notification)); e != nil {
							return
						}
						event(map[string]any{"kind": "character_list_after_mutation", "request": frame.ID, "id": 2, "bytes": len(notification)})
					}
					continue
				}
				if response, ok := responses[frame.ID]; ok {
					c.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if _, err := io.Copy(c, bytes.NewReader(response)); err != nil {
						event(map[string]any{"kind": "write_error", "error": err.Error()})
						return
					}
					event(map[string]any{"kind": "server_response", "peer": peer, "id": frame.ID, "hex": hex.EncodeToString(response)})
					if frame.ID == 1 {
						bootstrapped = true
					}
				}
			}
	}
	var wg sync.WaitGroup
	for _, cl := range listeners {
		cl := cl
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, err := cl.listener.Accept()
				if err != nil {
					log.Print("accept error on channel ", cl.id, ": ", err)
					return
				}
				go serveConn(c, cl.serverID, cl.id, cl.name, cl.banner)
			}
		}()
	}
	wg.Wait()
}
