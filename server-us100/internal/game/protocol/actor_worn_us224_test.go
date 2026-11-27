package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestActorWornUS224EntryEquipmentAndSuffix(t *testing.T) {
	var stats [EntryStatBlobUS100Size]byte
	skills := [2][]EntrySkill{{{ID: 179, Level: 2}}, {{ID: 55, Level: 1}}}
	known, e := UserInfoAdditionStatsWithSkillsUS100(10, EntryAdditionBlock{Level: 7}, stats[:], skills)
	if e != nil {
		t.Fatal(e)
	}
	w := []ActorWornUS224{{Slot: 8, Template: 42215, Avatar: true}, {Slot: 19, Template: 20002, Durability: 42, Upgrade: 7}, {Slot: 0, Template: 39165, Avatar: true}}
	p, e := UserInfoAdditionWithWornUS224(10, EntryAdditionBlock{Level: 7}, stats[:], skills, w)
	if e != nil {
		t.Fatal(e)
	}
	const count = 42 + 89 + 1
	if !bytes.Equal(p[:count], known[:count]) || p[count] != 3 {
		t.Fatal("actor/stats header changed")
	}
	i := count + 1
	// Offsets independently counted from the US224 compact actor row reader.
	for _, v := range w {
		if p[i] != byte(v.Slot) || binary.LittleEndian.Uint32(p[i+1:]) != v.Template || p[i+9] != v.Upgrade || binary.LittleEndian.Uint16(p[i+10:]) != v.Durability {
			t.Fatal("identity or item data", v)
		}
		if v.Template == 20002 {
			i += 99
		} else {
			i += 107
		}
	}
	if !bytes.Equal(p[i:], known[count+1:]) {
		t.Fatal("worn row consumed bytes from switches/skills or flags")
	}
	nilRows, e := UserInfoAdditionWithWornUS224(10, EntryAdditionBlock{Level: 7}, stats[:], skills, nil)
	if e != nil || !bytes.Equal(nilRows, known) {
		t.Fatal("empty character changed")
	}
	for _, bad := range [][]ActorWornUS224{{{Slot: 26, Template: 123}}, {{Slot: 0, Template: 1}}, {{Slot: 1, Template: 0, Avatar: true}}, {{Slot: 0, Template: 2, Avatar: true}, {Slot: 0, Template: 2, Avatar: true}}} {
		if _, e := appendActorWornUS224(nil, bad); e == nil {
			t.Fatal("unsupported persisted slots", bad)
		}
	}
}
