// Export compact display metadata for every source list entry, independently of the drop pool.
package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

type cell = pvf.Token
type layer struct {
	Job         string
	V, C, Z     int32
	Layer, Path string
}
type pose struct {
	Image       string
	Frame, X, Y int32
	Front       bool
	Delay       int32 `json:",omitempty"`
}
type auraTrack struct {
	Front  bool
	Frames []pose
}
type timedPose struct {
	p            pose
	start, delay int32
}
type row struct {
	ID                                        uint32
	Group, Path, Name, DisplayName, Kind, Img string
	Rarity, Frame                             int32
	Jobs                                      []string          `json:",omitempty"`
	SuitableJobs                              []string          `json:",omitempty"`
	Layers                                    []layer           `json:",omitempty"`
	Aura                                      []pose            `json:",omitempty"`
	AuraAnim                                  []auraTrack       `json:",omitempty"`
	Set                                       []int32           `json:",omitempty"`
	Package                                   []uint32          `json:",omitempty"`
	Display                                   []uint32          `json:",omitempty"`
	Booster                                   []uint32          `json:",omitempty"`
	Error                                     string            `json:",omitempty"`
	Fields                                    map[string][]cell `json:",omitempty"`
	SHA                                       string            `json:",omitempty"`
	Limit                                     int32             `json:",omitempty"`
}

var avatarKinds = map[string]bool{
	"[hat avatar]":    true,
	"[coat avatar]":   true,
	"[pants avatar]":  true,
	"[shoes avatar]":  true,
	"[hair avatar]":   true,
	"[breast avatar]": true,
	"[face avatar]":   true,
	"[waist avatar]":  true,
	"[skin avatar]":   true,
	"[weapon avatar]": true,
	"[aurora avatar]": true,
}

func decodeUTF16LE(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		raw = raw[2:]
	}
	n := len(raw) / 2
	u := make([]uint16, n)
	for i := 0; i < n; i++ {
		u[i] = binary.LittleEndian.Uint16(raw[i*2 : i*2+2])
	}
	return string(utf16.Decode(u))
}

func loadStringTable(a *pvf.Archive, tablePath string) map[string]string {
	out := map[string]string{}
	raw, err := a.ReadRaw(tablePath)
	if err != nil || len(raw) < 2 {
		return out
	}
	text := decodeUTF16LE(raw)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		k, v, ok := strings.Cut(line, ">")
		if ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func extractRefKey(ref string) string {
	if idx := strings.LastIndex(ref, "::"); idx >= 0 {
		ref = ref[idx+2:]
	}
	return strings.TrimSuffix(ref, ">")
}

func blocks(ts []cell, f func(string, []cell)) {
	for i := 0; i < len(ts); {
		if ts[i].Type != 3 {
			i++
			continue
		}
		j := i + 1
		for j < len(ts) && ts[j].Type != 3 {
			j++
		}
		f(ts[i].Text, ts[i+1:j])
		i = j
	}
}
func timeline(ts []cell) []timedPose {
	var paths []string
	var frames []timedPose
	blocks(ts, func(k string, v []cell) {
		if strings.HasPrefix(k, "[FRAME") && strings.HasSuffix(k, "]") {
			if _, err := strconv.Atoi(k[6 : len(k)-1]); err == nil {
				var start int32
				if len(frames) > 0 {
					last := frames[len(frames)-1]
					start = last.start + last.delay
				}
				frames = append(frames, timedPose{start: start})
			}
		}
		if k == "[IMAGE PATH]" {
			for _, t := range v {
				if t.Type == 6 {
					paths = append(paths, t.Text)
				}
			}
		}
		if len(frames) == 0 {
			return
		}
		cur := &frames[len(frames)-1]
		p := &cur.p
		if k == "[IMAGE EX]" && len(v) >= 2 && int(v[0].Value) >= 0 && int(v[0].Value) < len(paths) {
			p.Image = paths[v[0].Value]
			p.Frame = v[1].Value
		}
		if k == "[IMAGE]" && len(v) >= 2 && v[0].Type == 6 {
			p.Image = v[0].Text
			p.Frame = v[1].Value
		}
		if k == "[DELAY]" && len(v) > 0 && v[0].Value >= 0 {
			cur.delay = v[0].Value
		}
		if k == "[IMAGE POS]" && len(v) >= 2 {
			p.X, p.Y = v[0].Value, v[1].Value
		}
	})
	return frames
}
func stand(ts []cell) (pose, bool) {
	for _, frame := range timeline(ts) {
		if frame.p.Image != "" {
			return frame.p, true
		}
	}
	return pose{}, false
}
func auraStill(tracks [][]timedPose, front []bool) []pose {
	// Use a shared instant, not a different first visible frame for each effect.
	var at int32 = -1
	for _, track := range tracks {
		for _, f := range track {
			if f.p.Image != "" && (at < 0 || f.start < at) {
				at = f.start
				break
			}
		}
	}
	var out []pose
	for i, track := range tracks {
		for _, f := range track {
			if f.start <= at && (at < f.start+f.delay || (at == f.start && f.delay == 0)) {
				if f.p.Image != "" {
					p := f.p
					p.Front = front[i]
					out = append(out, p)
				}
				break
			}
		}
	}
	return out
}
func auraTracks(tracks [][]timedPose, front []bool) []auraTrack {
	var out []auraTrack
	for i, track := range tracks {
		var frames []pose
		for _, f := range track {
			if f.p.Image == "" {
				continue
			}
			p := f.p
			p.Front = front[i]
			delay := f.delay
			if delay <= 0 {
				delay = 100
			}
			p.Delay = delay
			frames = append(frames, p)
			if len(frames) >= 24 {
				break
			}
		}
		if len(frames) > 0 {
			out = append(out, auraTrack{Front: front[i], Frames: frames})
		}
	}
	return out
}
func main() {
	source := flag.String("source", "../runtime/pvf_source/Script.inner.pvf", "inner PVF")
	dest := flag.String("out", "../runtime/atelier-library.jsonl.gz", "projection")
	flag.Parse()
	a, e := pvf.LoadArchive(pvf.Options{Path: *source, MaxBytes: 1 << 30})
	if e != nil {
		log.Fatal(e)
	}
	f, e := os.Create(*dest + ".tmp")
	if e != nil {
		log.Fatal(e)
	}
	buf := bufio.NewWriterSize(f, 1<<20)
	gz, _ := gzip.NewWriterLevel(buf, gzip.BestSpeed)
	enc := json.NewEncoder(gz)
	write := func(v any) {
		if e := enc.Encode(v); e != nil {
			log.Fatal(e)
		}
	}
	normalize := func(p string) string { return strings.ToLower(strings.ReplaceAll(p, "\\", "/")) }
	resolve := func(p string) string {
		p = normalize(p)
		if a.FindFileIndex(p) < 0 {
			p = path.Join(path.Dir(p), "(r)"+path.Base(p))
		}
		return p
	}
	chr, e := catalog.ImportCharacters(a)
	if e != nil {
		log.Fatal(e)
	}
	poses := map[string]pose{}
	for _, prof := range chr.Professions {
		ts, _ := a.Tokens(prof.Path)
		blocks(ts, func(k string, v []cell) {
			if k == "[waiting motion]" && len(v) > 0 {
				n := resolve(path.Join(path.Dir(prof.Path), v[0].Text))
				ani, _ := a.Tokens(n)
				if p, ok := stand(ani); ok {
					poses[prof.Job] = p
				}
			}
		})
	}
	write(map[string]any{"source": a.Snapshot().Checksum, "poses": poses, "version": 1})
	auraCache := map[string][]timedPose{}
	for _, group := range []string{"equipment", "stackable"} {
		strTable := loadStringTable(a, "string/"+group+".uv.str")
		idx, e := a.Tokens("list/" + group + ".lst")
		if e != nil {
			log.Fatal(e)
		}
		refs, e := catalog.ParseIndex(idx)
		if e != nil {
			log.Fatal(e)
		}
		type rec struct {
			id uint32
			p  string
			i  int
		}
		rs := make([]rec, 0, len(refs))
		for _, r := range refs {
			p := r.Path
			if !strings.HasPrefix(p, group+"/") {
				p = group + "/" + p
			}
			p = resolve(p)
			rs = append(rs, rec{r.ID, p, a.FindFileIndex(p)})
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i].i < rs[j].i })
		bad := 0
		for _, r := range rs {
			rr := row{ID: r.id, Group: group, Path: r.p, Rarity: -1}
			ts, err := a.Tokens(r.p)
			if err != nil {
				rr.Error = "source unreadable"
				bad++
				write(rr)
				continue
			}
			var l layer
			var variants []layer
			begin := 0
			isEquip := group == "equipment"
			blocks(ts, func(k string, v []cell) {
				if len(v) == 0 {
					return
				}
				switch k {
				case "[name]":
					if rr.Name == "" {
						rr.Name = v[0].Reference
						if rr.Name == "" {
							rr.Name = v[0].Text
						}
					}
				case "[equipment type]", "[stackable type]":
					if rr.Kind == "" {
						rr.Kind = v[0].Text
					}
				case "[rarity]":
					if rr.Rarity < 0 {
						rr.Rarity = v[0].Value
					}
				case "[icon]":
					if rr.Img == "" && len(v) >= 2 {
						rr.Img = v[0].Text
						rr.Frame = v[1].Value
					}
				case "[usable job]":
					for _, t := range v {
						if t.Type == 6 {
							rr.Jobs = append(rr.Jobs, t.Text)
						}
					}
				case "[part set index]":
					if isEquip {
						for _, t := range v {
							if t.Type == 0 && t.Value > 0 {
								rr.Set = append(rr.Set, t.Value)
							}
						}
					}
				case "[animation job]":
					l = layer{Job: v[0].Text}
					begin = len(rr.Layers)
				case "[variation]":
					if len(v) >= 2 {
						l.V, l.C = v[0].Value, v[1].Value
						variants = append(variants, l)
					}
				case "[layer variation]":
					if isEquip {
						for i := 0; i+1 < len(v); i++ {
							if v[i].Type == 0 && v[i+1].Type == 6 {
								l.Z, l.Layer = v[i].Value, v[i+1].Text
								rr.Layers = append(rr.Layers, l)
							}
						}
					}
				case "[expand path]":
					l.Path = v[0].Text
					for i := begin; i < len(rr.Layers); i++ {
						rr.Layers[i].Path = l.Path
					}
				case "[aurora graphic effects]":
					var tracks [][]timedPose
					var front []bool
					for i := 0; i+1 < len(v); i++ {
						if v[i].Type != 0 || v[i+1].Type != 6 {
							continue
						}
						name := normalize(v[i+1].Text)
						frames, ok := auraCache[name]
						if !ok {
							t, _ := a.Tokens(resolve(name))
							frames = timeline(t)
							auraCache[name] = frames
						}
						tracks = append(tracks, frames)
						front = append(front, v[i].Value != 0)
					}
					rr.Aura = append(rr.Aura, auraStill(tracks, front)...)
					rr.AuraAnim = append(rr.AuraAnim, auraTracks(tracks, front)...)
				case "[package data]":
					if len(v)%2 == 0 {
						for i := 0; i < len(v); i += 2 {
							if v[i].Type != 0 || v[i].Value <= 0 || v[i+1].Type != 0 || v[i+1].Value <= 0 {
								rr.Package = nil
								break
							}
							rr.Package = append(rr.Package, uint32(v[i].Value))
						}
					}
				case "[cs display only]":
					if len(v)%2 == 0 {
						for i := 0; i < len(v); i += 2 {
							if v[i].Type == 0 && v[i].Value > 0 && v[i+1].Type == 0 && v[i+1].Value > 0 {
								rr.Display = append(rr.Display, uint32(v[i].Value))
							}
						}
					}
				case "[avatar]":
					if group == "stackable" && len(v) >= 2 && v[0].Type == 0 && v[1].Type == 0 && v[1].Value > 0 {
						rr.Booster = append(rr.Booster, uint32(v[1].Value))
					}
				case "[suitable job]":
					for _, t := range v {
						if t.Type == 6 {
							rr.SuitableJobs = append(rr.SuitableJobs, t.Text)
						}
					}
				case "[stack limit]":
					if len(v) == 1 && v[0].Type == 0 {
						rr.Limit = v[0].Value
					}
				}
			})
			if rr.Name != "" {
				if disp, ok := strTable[extractRefKey(rr.Name)]; ok && disp != "" {
					rr.DisplayName = disp
				}
			}
			avatar := isEquip && (strings.Contains(r.p, "avatar/") || avatarKinds[rr.Kind])
			if !avatar {
				rr.Layers = nil
				rr.Set = nil
			}
			if rr.Kind == "[skin avatar]" {
				for _, v := range variants {
					v.Z = 1000
					v.Layer = "body"
					rr.Layers = append(rr.Layers, v)
				}
			}
			if !avatar {
				b, err := a.ReadRaw(r.p)
				if err != nil {
					log.Fatal(err)
				}
				rr.SHA = fmt.Sprintf("%x", sha256.Sum256(b))
				if group == "equipment" {
					rr.Fields = map[string][]cell{}
					blocks(ts, func(k string, v []cell) {
						switch k {
						case "[equipment type]", "[attach type]", "[durability]", "[rarity]", "[usable job]", "[grow type]", "[minimum level]", "[maximum level]", "[grade]", "[physical attack]", "[physical defense]", "[magical attack]", "[magical defense]", "[equipment physical attack]", "[equipment physical defense]", "[equipment magical attack]", "[equipment magical defense]", "[anti evil]":
							if _, ok := rr.Fields[k]; !ok {
								rr.Fields[k] = v
							}
						}
					})
				}
			}
			write(rr)
		}
		log.Printf("%s: %d rows, %d unreadable", group, len(rs), bad)
	}
	if e = gz.Close(); e != nil {
		log.Fatal(e)
	}
	if e = buf.Flush(); e != nil {
		log.Fatal(e)
	}
	if e = f.Close(); e != nil {
		log.Fatal(e)
	}
	if e = os.Rename(*dest+".tmp", *dest); e != nil {
		log.Fatal(e)
	}
	fmt.Println("written", *dest)
}
