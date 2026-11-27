// Repair an explicitly selected development character's legacy zero progress.
package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strconv"
	"time"
)

func main() {
	config := flag.String("storage", "runtime/storage/local.json", "storage configuration")
	source := flag.String("catalog", "configs/quests.generated.json", "quest source")
	id := flag.Int64("character", 0, "exact development character ID")
	apply := flag.Bool("apply", false, "apply audited repair; default previews character changes after schema migration")
	flag.Parse()
	if *id <= 0 {
		log.Fatal("an exact character ID is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conf, e := storage.LoadConfig(*config)
	if e != nil {
		log.Fatal(e)
	}
	s, e := storage.Open(ctx, conf)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	if e = s.MigrateQuests(ctx); e != nil {
		log.Fatal(e)
	}
	// Ownership: the character must exist and belong to a development account.
	var account int64
	{
		var cRow struct {
			AccountID int64 `json:"account_id"`
		}
		ok, e := s.Database().Get("characters", strconv.FormatInt(*id, 10), &cRow)
		if e != nil || !ok {
			log.Fatal("development character not found")
		}
		var aRow struct {
			DevelopmentOnly bool `json:"development_only"`
		}
		ok, e = s.Database().Get("accounts", strconv.FormatInt(cRow.AccountID, 10), &aRow)
		if e != nil || !ok || !aRow.DevelopmentOnly {
			log.Fatal("development character not found")
		}
		account = cRow.AccountID
	}
	quests, e := s.Quests(ctx, account, *id)
	if e != nil {
		log.Fatal(e)
	}
	cat, e := catalog.LoadQuests(*source)
	if e != nil {
		log.Fatal(e)
	}
	changes := []map[string]any{}
	for _, q := range quests {
		if q.ProgressModel != "legacy-zero" {
			continue
		}
		d, ok := cat.Quests[uint32(q.ID)]
		if !ok || q.ConfigVersion != cat.Source.Checksum {
			log.Fatal("quest source mismatch")
		}
		initial, model, e := quest.InitialProgress(d)
		if e != nil {
			log.Fatal(e)
		}
		if q.Status != "accepted" || q.Progress != 0 {
			log.Fatal("unexpected legacy state; no repair applied")
		}
		changes = append(changes, map[string]any{"character": *id, "quest": q.ID, "before": q, "remaining": initial, "progress_model": model, "objective": d.ObjectiveCells})
	}
	// Finish validation before applying the explicitly selected records.
	if *apply {
		for _, q := range quests {
			if q.ProgressModel == "legacy-zero" {
				initial, model, _ := quest.InitialProgress(cat.Quests[uint32(q.ID)])
				if e = s.RepairLegacyQuest(ctx, account, *id, q, initial, model); e != nil {
					log.Fatal(e)
				}
			}
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"applied": *apply, "changes": changes}, "", "  ")
	fmt.Println(string(b))
}
