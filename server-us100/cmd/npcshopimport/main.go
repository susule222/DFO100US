package main

import (
	"compress/gzip"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type materialCost struct {
	ID    uint32 `json:"id"`
	Count uint32 `json:"c"`
}

type purchaseLimit struct {
	Scope  string `json:"s"` // "account" or "character"
	Period string `json:"p"` // "accumulate", "weekly", "daily"
	Max    uint32 `json:"m"`
}

type shopItemEntry struct {
	Kind          string         `json:"k,omitempty"` // "s"=stackable, "e"=equipment
	StackType     string         `json:"t,omitempty"`
	StackLimit    uint32         `json:"sl,omitempty"`
	Gold          uint32         `json:"g,omitempty"`
	Materials     []materialCost `json:"mat,omitempty"`
	VarNeedCount  uint32         `json:"vc,omitempty"`
	VarMaterials  []materialCost `json:"vmat,omitempty"`
	Limit         *purchaseLimit `json:"lim,omitempty"`
}

type shopEntry struct {
	NPC   uint32   `json:"npc"`
	Items []uint32 `json:"items"`
}

type npcShopCatalogFile struct {
	Source string                    `json:"source"`
	Shops  map[uint32]shopEntry      `json:"shops"`
	Items  map[uint32]shopItemEntry  `json:"items"`
}

func loadListMap(a *pvf.Archive, lstPath, prefix string) map[uint32]string {
	toks, err := a.Tokens(lstPath)
	if err != nil {
		log.Fatalf("load %s: %v", lstPath, err)
	}
	out := make(map[uint32]string, len(toks)/2)
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].Type == 0 && toks[i+1].Type == 6 && toks[i].Value > 0 && toks[i+1].Text != "" {
			rel := strings.ReplaceAll(strings.TrimSpace(toks[i+1].Text), "\\", "/")
			if prefix != "" && !strings.HasPrefix(strings.ToLower(rel), strings.ToLower(prefix)) {
				rel = prefix + rel
			}
			out[uint32(toks[i].Value)] = rel
			i++
		}
	}
	return out
}

func openTokensWithRegional(a *pvf.Archive, p string) ([]pvf.Token, error) {
	if toks, err := a.Tokens(p); err == nil {
		return toks, nil
	}
	dir, base := filepath.Split(strings.ReplaceAll(p, "\\", "/"))
	return a.Tokens(dir + "(r)" + base)
}

func parseShop(a *pvf.Archive, path string) (shopEntry, bool) {
	toks, err := openTokensWithRegional(a, path)
	if err != nil {
		return shopEntry{}, false
	}
	var e shopEntry
	seen := make(map[uint32]bool)
	for i := 0; i < len(toks); i++ {
		if toks[i].Type != 3 {
			continue
		}
		switch strings.ToLower(toks[i].Text) {
		case "[npc]":
			if i+1 < len(toks) && toks[i+1].Type == 0 && toks[i+1].Value > 0 {
				e.NPC = uint32(toks[i+1].Value)
			}
		case "[item list]":
			for j := i + 1; j < len(toks); j++ {
				if toks[j].Type == 3 {
					i = j
					break
				}
				if toks[j].Type == 0 && toks[j].Value > 0 {
					id := uint32(toks[j].Value)
					if !seen[id] {
						seen[id] = true
						e.Items = append(e.Items, id)
					}
				}
			}
		}
	}
	return e, len(e.Items) > 0
}

func parseItem(a *pvf.Archive, path, kind string) (shopItemEntry, bool) {
	toks, err := openTokensWithRegional(a, path)
	if err != nil {
		return shopItemEntry{}, false
	}
	entry := shopItemEntry{Kind: kind}
	for i := 0; i < len(toks); i++ {
		if toks[i].Type != 3 {
			continue
		}
		switch strings.ToLower(toks[i].Text) {
		case "[price]":
			if i+1 < len(toks) && toks[i+1].Type == 0 && toks[i+1].Value > 0 {
				entry.Gold = uint32(toks[i+1].Value)
			}
		case "[stack limit]":
			if i+1 < len(toks) && toks[i+1].Type == 0 && toks[i+1].Value > 0 {
				entry.StackLimit = uint32(toks[i+1].Value)
			}
		case "[stackable type]":
			if i+1 < len(toks) && toks[i+1].Type == 6 {
				entry.StackType = toks[i+1].Text
			}
		case "[need material]":
			for j := i + 1; j+1 < len(toks); j += 2 {
				if toks[j].Type == 3 {
					i = j
					break
				}
				if toks[j].Type == 0 && toks[j+1].Type == 0 && toks[j].Value > 0 && toks[j+1].Value > 0 {
					entry.Materials = append(entry.Materials, materialCost{
						ID:    uint32(toks[j].Value),
						Count: uint32(toks[j+1].Value),
					})
				} else {
					break
				}
			}
		case "[variable need material]":
			for j := i + 1; j < len(toks); j++ {
				if toks[j].Type == 3 && strings.EqualFold(toks[j].Text, "[/variable need material]") {
					i = j
					break
				}
				if toks[j].Type == 3 && strings.EqualFold(toks[j].Text, "[material count]") {
					if j+1 < len(toks) && toks[j+1].Type == 0 && toks[j+1].Value > 0 {
						entry.VarNeedCount = uint32(toks[j+1].Value)
					}
				}
				if toks[j].Type == 3 && strings.EqualFold(toks[j].Text, "[materials]") {
					for k := j + 1; k+1 < len(toks); k += 2 {
						if toks[k].Type == 3 {
							j = k
							break
						}
						if toks[k].Type == 0 && toks[k+1].Type == 0 && toks[k].Value > 0 && toks[k+1].Value > 0 {
							entry.VarMaterials = append(entry.VarMaterials, materialCost{
								ID:    uint32(toks[k].Value),
								Count: uint32(toks[k+1].Value),
							})
						} else {
							break
						}
					}
				}
			}
		case "[purchase limit]":
			if i+3 < len(toks) && toks[i+1].Type == 6 && toks[i+2].Type == 6 && toks[i+3].Type == 0 && toks[i+3].Value > 0 {
				entry.Limit = &purchaseLimit{
					Scope:  strings.ToLower(toks[i+1].Text),
					Period: strings.ToLower(toks[i+2].Text),
					Max:    uint32(toks[i+3].Value),
				}
			}
		}
	}
	return entry, true
}

func main() {
	source := flag.String("source", "", "read-only US100 inner PVF")
	output := flag.String("output", "cmd/wireprobe/npcshops_us100.json.gz", "output gzipped NPC shop catalog")
	flag.Parse()
	if *source == "" {
		log.Fatal("-source is required")
	}
	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		log.Fatal(err)
	}
	shopList := loadListMap(a, "list/itemshop.lst", "")
	stkList := loadListMap(a, "list/stackable.lst", "stackable/")
	equList := loadListMap(a, "list/equipment.lst", "equipment/")

	cat := npcShopCatalogFile{
		Source: a.Snapshot().Checksum,
		Shops:  make(map[uint32]shopEntry, len(shopList)),
		Items:  make(map[uint32]shopItemEntry, 4096),
	}

	neededItems := make(map[uint32]bool, 4096)
	for sid, spath := range shopList {
		se, ok := parseShop(a, spath)
		if !ok {
			continue
		}
		cat.Shops[sid] = se
		for _, id := range se.Items {
			neededItems[id] = true
		}
	}

	for id := range neededItems {
		if sp, ok := stkList[id]; ok {
			if it, ok := parseItem(a, sp, "s"); ok {
				cat.Items[id] = it
				continue
			}
		}
		if ep, ok := equList[id]; ok {
			if it, ok := parseItem(a, ep, "e"); ok {
				cat.Items[id] = it
				continue
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*output)
	if err != nil {
		log.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(cat); err != nil {
		log.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	info, _ := os.Stat(*output)
	log.Printf("npcshops source=%s shops=%d items=%d size=%d bytes", cat.Source, len(cat.Shops), len(cat.Items), info.Size())
}
