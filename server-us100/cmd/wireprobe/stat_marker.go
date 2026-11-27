package main

import "flag"

// Probes for the mode-1 entry block's two big slots. The block's +0 (u32) and +4
// (u64) are stored verbatim at actor+0x50c / actor+0x510 and mirrored into a
// per-thread singleton, and two readings of those fields are still open: the
// local player's level and 64-bit experience, or the reference servers' hp_max /
// mp_max pair. -entry-addition-hp switches the filling while everything else
// about the packet stays identical, so one run tells the two apart by the HUD.
var entryAdditionHP = flag.Bool("entry-addition-hp", false,
	"fill the mode-1 block's +0/+4 with hp_max×10 and mp_max instead of the character's level and experience")

// Panel-writer trigger experiment (2026-09-22): the entry capture showed the
// settlement runs with flag=block[13]>0 and the panel writers never fire on a
// flag=0 entry, leaving the panel slot array +0x304..+0x334 all zero (the
// panel's 0/0). This flag sets block byte 13 to 1 alongside -entry-addition-hp.
var entryAdditionFlag13 = flag.Bool("entry-addition-flag13", false,
	"set mode-1 block byte 13 to 1 so the settlement receives flag=1 (panel writer trigger test)")

// Normal v2.24 town entry restores the role's persisted base stats.
var entryAdditionStats = flag.Bool("entry-addition-stats", false,
	"send the selected character's v2.24 base stats in its mode-1 entry")

// Values the mode-6 marker probe writes into the four u32 slots that
// 0x143c21110 reads as two (value, maximum) pairs.
//
// They are small and clean because the HUD is the readout: a stat struct whose
// pairs are (777, 777) and (888, 888) puts the character at 100% with a number
// that cannot be confused with anything else on screen. The mode-1 block's own
// values (level 1, or 1800/1400 in the hp variant) stay distinguishable from
// 777/888, so a single run names which channel the HUD reads.
const (
	statMarkerHP = 777
	statMarkerMP = 888
)
