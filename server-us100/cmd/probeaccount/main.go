// probeaccount creates a clean development account with two characters inside
// the US100 character catalog. Idempotent per name. Never prints secrets.
package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/storage"
	"encoding/json"
	"flag"
	"log"
	"time"
)

func main() {
	cfg := flag.String("config", "runtime/storage/local.json", "storage config")
	accountName := flag.String("account", "probe-us100", "development account name")
	flag.Parse()

	conf, e := storage.LoadConfig(*cfg)
	if e != nil {
		log.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, e := storage.Open(ctx, conf)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(ctx); e != nil {
		log.Fatal(e)
	}
	account, e := s.DevelopmentAccount(ctx, *accountName)
	if e != nil {
		log.Fatal(e)
	}
	log.Printf("account=%s id=%d", *accountName, account)

	chars, e := catalog.LoadCharacters("configs/us100/characters.generated.json")
	if e != nil {
		log.Fatal(e)
	}
	for _, spec := range []struct {
		name string
		job  byte
	}{{"us100a", 0}, {"us100b", 3}, {"us100c", 4}, {"us100d", 2}} {
		prof, ok := chars.Professions[spec.job]
		if !ok {
			log.Fatalf("profession %d not in catalog", spec.job)
		}
		exists, e := s.NameExists(ctx, spec.name)
		if e != nil {
			log.Fatal(e)
		}
		if exists {
			log.Printf("exists %s", spec.name)
			continue
		}
		state, e := json.Marshal(map[string]any{
			"level":               1,
			"attributes":          prof.InitialAttributes,
			"initial_skill_cells": prof.InitialSkills,
			"source_path":         prof.Path,
			"source_sha256":       prof.RawSHA256,
			"advancement":         0,
			"equipment_pending":   true,
		})
		if e != nil {
			log.Fatal(e)
		}
		role, e := s.CreateCharacter(ctx, storage.Character{
			AccountID:     account,
			Name:          spec.name,
			Profession:    spec.job,
			Request:       []byte{},
			ConfigVersion: chars.Source.Checksum,
			State:         state,
		}, 8)
		if e != nil {
			log.Fatalf("create %s: %v", spec.name, e)
		}
		log.Printf("created %s profession=%d wire=%d", role.Name, role.Profession, role.WireID)
	}
}
