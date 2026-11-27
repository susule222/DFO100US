// pvflocalize exports or replaces existing UTF-16 localization tables only.
package main

import (
	"dfolan/internal/catalog/pvf"
	"flag"
	"log"
)

func main() {
	source := flag.String("source", "", "input inner PVF")
	dir := flag.String("dir", "", "new export directory, or directory of replacement tables")
	out := flag.String("output", "", "new inner PVF; omit to export tables")
	refs := flag.String("refs", "", "new JSON report of script localization references")
	inline := flag.String("inline", "", "new JSON inventory of script display-field inline strings")
	inlinePlan := flag.String("inline-plan", "", "verified JSON inline names to replace when writing a candidate")
	xui := flag.String("xui", "", "new directory of raw UTF-16 XUI entries")
	inventory := flag.String("inventory", "", "new JSON inventory of PVF entry extensions and types")
	auxiliary := flag.String("aux-text", "", "new directory of raw Lua/COS/JSON resources for audit")
	flag.Parse()
	modes := 0
	for _, option := range []string{*dir, *refs, *inline, *xui, *inventory, *auxiliary} {
		if option != "" {
			modes++
		}
	}
	if *source == "" || modes != 1 || (*out != "" && *dir == "") || (*inlinePlan != "" && *out == "") {
		log.Fatal("provide a source and exactly one of -dir (optionally -output), -refs, -inline, -xui, -inventory, or -aux-text")
	}
	a, err := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1 << 30})
	if err == nil {
		if *auxiliary != "" {
			err = a.ExportAuxiliaryText(*auxiliary)
		} else if *inventory != "" {
			err = a.ExportTypeInventory(*inventory)
		} else if *xui != "" {
			err = a.ExportXUI(*xui)
		} else if *inline != "" {
			err = a.ExportInlineDisplayInventory(*inline)
		} else if *refs != "" {
			err = a.ExportLocalizationReferences(*refs)
		} else if *out == "" {
			err = a.ExportLocalization(*dir)
		} else {
			err = a.WriteLocalizationCopyWithInline(*dir, *inlinePlan, *out)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
}
