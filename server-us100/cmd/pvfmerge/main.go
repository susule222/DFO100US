// pvfmerge merges 90JP-style PVF Mod layers into a new verified FormatDFO20260901 inner PVF.
package main

import (
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	source := flag.String("source", "", "baseline inner PVF path")
	planPath := flag.String("plan", "", "JSON merge plan file")
	output := flag.String("output", "", "output merged inner PVF path")
	flag.Parse()
	if *source == "" || *planPath == "" || *output == "" {
		log.Fatal("usage: pvfmerge -source <inner.pvf> -plan <plan.json> -output <merged.inner.pvf>")
	}
	rawPlan, err := os.ReadFile(*planPath)
	if err != nil {
		log.Fatalf("read plan: %v", err)
	}
	var plan pvf.MergePlan
	if err := json.Unmarshal(rawPlan, &plan); err != nil {
		log.Fatalf("parse plan: %v", err)
	}
	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1 << 30})
	if err != nil {
		log.Fatalf("load source pvf: %v", err)
	}
	report, err := a.MergeModPlan(plan, *output)
	if err != nil {
		log.Fatalf("merge pvf: %v", err)
	}
	out, _ := json.Marshal(report)
	fmt.Println(string(out))
}
