// pvfloadprobe loads an archive and reports its in-process footprint.
package main

import (
	"dfolan/internal/catalog/pvf"
	"flag"
	"log"
	"runtime"
)

var a *pvf.Archive

func report(tag string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	ans, wide := a.StringPoolBytes()
	log.Printf("%s: heap_alloc=%.0fMB heap_inuse=%.0fMB sys=%.0fMB numgc=%d strA=%.1fMB strW=%.1fMB",
		tag, float64(m.HeapAlloc)/1048576, float64(m.HeapInuse)/1048576, float64(m.Sys)/1048576, m.NumGC,
		float64(ans)/1048576, float64(wide)/1048576)
}

func main() {
	src := flag.String("source", "", "archive")
	preload := flag.Bool("preload", false, "run PreloadAll")
	flag.Parse()
	var e error
	a, e = pvf.LoadArchive(pvf.Options{Path: *src, MaxBytes: 1024 * 1024 * 1024})
	if e != nil {
		log.Fatal(e)
	}
	report("after_load")
	if *preload {
		if _, e = a.PreloadAll(); e != nil {
			log.Fatal(e)
		}
		report("after_preload_all")
	}
	runtime.GC()
	report("after_gc")
	log.Printf("files=%d groups=%d", a.FileCount(), a.Snapshot().GroupCount)
}
