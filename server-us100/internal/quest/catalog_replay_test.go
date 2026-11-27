package quest

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/progression"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func loaded[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Opt-in catalog replay. A ready fixture isolates hand-in behavior from
// dungeon and NPC event generation. Never open the player's database.
func TestUS100CatalogHandIns(t *testing.T) {
	outfile := os.Getenv("US100_QUEST_AUDIT")
	if outfile == "" {
		t.Skip("set US100_QUEST_AUDIT to write a per-quest report")
	}
	dir := "../../configs/us100/"
	store := loaded(storage.Open(context.Background(), storage.Config{DBPath: filepath.Join(t.TempDir(), "audit.db")}))
	defer store.Close()
	q := loaded(catalog.LoadQuests(dir + "quests.generated.json"))
	prof := loaded(catalog.LoadCharacters(dir + "characters.generated.json"))
	p := character.ProgressionService{Store: store, Catalog: loaded(catalog.LoadProgression(dir + "progression.generated.json")), Professions: prof, Rules: loaded(progression.LoadRules(dir + "rules.experience.json"))}
	a := inventory.Awarder{Catalog: loaded(catalog.LoadLoot(dir + "loot.generated.json")), Equipment: loaded(inventory.LoadEquipmentCatalog(dir+"equipment.generated.json", q.Source.Checksum)), Rules: loaded(inventory.LoadBagRules(dir + "rules.bag.json"))}
	s := Service{Store: store, Catalog: q, Professions: prof, Progression: &p, Inventory: &a}
	groups := map[string][]uint32{}
	ctx := context.Background()
	for _, id := range s.Index().Ordered {
		d := q.Quests[id]
		en := s.Index().Entries[id]
		reason := ""
		switch {
		case !en.Implemented:
			reason = "objective " + d.Kind
		case en.Unverified != "":
			reason = "source constraint " + en.Unverified
		case !en.RewardUsable:
			reason = fmt.Sprint("reward ", cells(d.Script.Cells, "[reward type]"))
		case en.MinimumLevel > uint32(p.Rules.LevelCap):
			reason = "above configured level cap"
		default:
			var job byte
			for job = 0; job < 255; job++ {
				if pr, ok := prof.Professions[job]; ok && (len(en.Jobs) == 0 || slices.Contains(en.Jobs, "[all]") || slices.Contains(en.Jobs, pr.Job)) {
					break
				}
			}
			if job == 255 {
				reason = "no eligible profession"
				break
			}
			pr := prof.Professions[job]
			state := character.State{Level: byte(max(1, en.MinimumLevel)), SourceSHA256: pr.RawSHA256, Attributes: pr.InitialAttributes, InitialSkills: pr.InitialSkills}
			if len(en.GrowTypes) > 0 && en.GrowTypes[0] > 0 {
				state.Advancement = byte(en.GrowTypes[0])
			}
			if state.Level > 1 {
				state.Experience = p.Catalog.Thresholds[state.Level-2]
			}
			bag := inventory.Bag{Version: "ordinary-bag-v1"}
			needs := collection(d)
			if en.Model == SeekAndMeetNPC {
				needs = en.Seek.Items
			}
			if en.Model == CollectItems || en.Model == SeekAndMeetNPC {
				for i, n := range needs {
					if n.Template == 0 {
						bag.Gold += n.Amount
					} else {
						bag.Items = append(bag.Items, inventory.BagItem{Slot: uint16(121 + i), Template: n.Template, Amount: n.Amount})
					}
				}
			}
			role := storage.Character{ID: 1, WireID: 1, AccountID: 1, Profession: job, ConfigVersion: q.Source.Checksum, State: loaded(inventory.SaveBag(loaded(json.Marshal(state)), bag))}
			key := fmt.Sprintf("1/%d", id)
			err := store.Database().Mutate(func(tx *db100.Tx) error {
				// Keep the fixture small even when thousands of quests are replayed.
				for _, tab := range []string{"character_quests", "character_quest_rewards"} {
					for _, k := range tx.Keys(tab) {
						tx.Delete(tab, k)
					}
				}
				if e := tx.Put("characters", "1", map[string]any{"id": 1, "wire_id": 1, "account_id": 1, "profession": job, "config_version": q.Source.Checksum, "state": role.State}); e != nil {
					return e
				}
				return tx.Put("character_quests", key, map[string]any{"character_id": 1, "quest_id": id, "config_version": q.Source.Checksum, "progress_model": en.Model, "progress": 0, "status": "accepted"})
			})
			if err != nil {
				t.Fatal(err)
			}
			req := protocol.QuestSubmitRequest{ID: uint16(id), Option: 1, RewardSelection: 65535}
			choices, e := progression.ItemRewards(cells(d.Script.Cells, "[reward selection int data]"), job, state.Advancement)
			if e != nil {
				reason = e.Error()
				break
			}
			if len(choices) > 0 {
				req.RewardSelection = 0
			}
			if parent := en.Links.Parent; parent != 0 {
				if _, e := s.Finish(ctx, role, req); e == nil {
					t.Fatalf("quest %d settled without its parent", id)
				}
				err := store.Database().Mutate(func(tx *db100.Tx) error {
					return tx.Put("character_quests", fmt.Sprintf("1/%d", parent), map[string]any{"character_id": 1, "quest_id": parent, "config_version": q.Source.Checksum, "status": "accepted"})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			// Exercise the production event ledger on a counted run: an identical
			// event must never substitute for the remaining distinct clears.
			if id == 12186 && en.Model == ClearDungeon {
				err := store.Database().Mutate(func(tx *db100.Tx) error {
					return tx.Put("character_quests", key, map[string]any{"character_id": 1, "quest_id": id, "config_version": q.Source.Checksum, "progress_model": en.Model, "progress": en.Initial, "status": "accepted"})
				})
				if err != nil {
					t.Fatal(err)
				}
				for i := uint32(0); i < en.Initial; i++ {
					if _, err := s.Finish(ctx, role, req); err == nil {
						t.Fatalf("quest %d settled after only %d clears", id, i)
					}
					run := fmt.Sprintf("%032x", i+1)
					for j := 0; j < 2; j++ {
						recorded, err := store.RecordQuestDungeonClear(ctx, 1, 1, run, uint32(d.ObjectiveCells[0].Value), q.Source.Checksum, ClearDungeon, []uint16{uint16(id)})
						if err != nil || recorded != (j == 0) {
							t.Fatalf("clear %d replay %d: %v %v", i, j, recorded, err)
						}
					}
				}
			}
			first, e := s.Finish(ctx, role, req)
			if e != nil {
				reason = e.Error()
				break
			}
			second, e := s.Finish(ctx, first.Role, req)
			if !first.Applied || e != nil || second.Applied {
				t.Fatalf("quest %d duplicate settlement: %v", id, e)
			}
			reason = "settled_once"
		}
		groups[reason] = append(groups[reason], id)
	}
	for k, ids := range groups {
		t.Logf("%s: %d", k, len(ids))
	}
	if err := os.WriteFile(outfile, loaded(json.MarshalIndent(groups, "", "  ")), 0600); err != nil {
		t.Fatal(err)
	}
}
