package character

import (
	"dfolan/internal/catalog"
	"testing"
)

func TestArcherBaseSkillsRespectBothFitnessAndGrowCap(t *testing.T) {
	c, e := catalog.LoadCharacters("../../configs/characters.next25.json")
	if e != nil {
		t.Fatal(e)
	}
	l, e := LoadLearningCatalog("../../configs/skills.next27.json", c.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []uint16{3, 7, 511} {
		if !l.index[16][id].ForAdvancement(0) {
			t.Fatalf("base Archer skill%d filtered", id)
		}
	}
	// The original latent-ability script lists grow0 in fitness but caps it
	// at zero. Its advancement-only passive must not leak into the base page.
	if l.index[16][501].ForAdvancement(0) {
		t.Fatal("grow0 zero-cap skill leaked")
	}
	if !l.index[16][501].ForAdvancement(1) {
		t.Fatal("legitimate advanced eligibility lost")
	}
	if _, e = l.index[16][7].Cost(4, 0, 1, map[uint16]byte{}); e == nil {
		t.Fatal("level4 learned level10 RisingMoon")
	}
}

func TestUS100GrowCapOverridesFitnessForBaseAndMultiCostSkills(t *testing.T) {
	c, e := catalog.LoadCharacters("../../configs/us100/characters.generated.json")
	if e != nil {
		t.Fatal(e)
	}
	l, e := LoadLearningCatalog("../../configs/us100/skills.generated.json", c.Source.Checksum)
	if e != nil {
		t.Fatal(e)
	}
	// Male Slayer Lv15 SpeedSlash (ID 282) has [growtype maximum level]=[60,0,0,0,0,60]
	// and [skill fitness growtype]=[5]. Base Slayer (adv=0) and Ghostblade (adv=5)
	// must be able to learn it, while adv=1..4 remain blocked.
	speedSlash := l.index[0][282]
	if !speedSlash.ForAdvancement(0) || !speedSlash.ForAdvancement(5) {
		t.Fatalf("SpeedSlash (282) must be eligible for adv 0 and 5")
	}
	for _, blocked := range []int{1, 2, 3, 4} {
		if speedSlash.ForAdvancement(blocked) {
			t.Fatalf("SpeedSlash (282) must remain blocked for adv %d", blocked)
		}
	}
	cost, err := speedSlash.Cost(15, 0, 1, map[uint16]byte{})
	if err != nil || cost != 20 {
		t.Fatalf("SpeedSlash (282) Lv15 base cost=%d err=%v, want 20", cost, err)
	}
	// Multi-tier [purchase cost] (e.g. Knight PerfectGuard ID 8: [30, 15]).
	perfectGuard := l.index[12][8]
	c1, err := perfectGuard.Cost(10, 0, 1, map[uint16]byte{})
	if err != nil || c1 != 30 {
		t.Fatalf("PerfectGuard rank 1 cost=%d err=%v, want 30", c1, err)
	}
	c2, err := perfectGuard.Cost(10, 0, 2, map[uint16]byte{})
	if err != nil || c2 != 15 {
		t.Fatalf("PerfectGuard rank 2 cost=%d err=%v, want 15", c2, err)
	}
}
