package main

import "dfolan/internal/storage"

// US100 (r)cerashop.etc, item period or contract, shown states 0/4.
// Stackable identities verified against list/stackable.lst, not the global equipment/creature namespace.
var premiumListings = map[uint32]storage.CartLine{
	3400021: {Product: 3400021, Template: 590004900, Cera: 380, Gold: 0},   // Gabriel's Contract: 7  Days
	3400022: {Product: 3400022, Template: 590004901, Cera: 710, Gold: 0},   // Gabriel's Contract: 15 Days
	3400023: {Product: 3400023, Template: 590004902, Cera: 990, Gold: 0},   // Gabriel's Contract: 30 Days
	3400004: {Product: 3400004, Template: 590004687, Cera: 380, Gold: 0},   // Unstable Rift Contract: 7  Days
	3400005: {Product: 3400005, Template: 590004689, Cera: 710, Gold: 0},   // Unstable Rift Contract: 15 Days
	3400006: {Product: 3400006, Template: 590004691, Cera: 990, Gold: 0},   // Unstable Rift Contract: 30 Days
	3500033: {Product: 3500033, Template: 50042664, Cera: 400, Gold: 0},    // VIP Premium Contract: 7 Days
	3500034: {Product: 3500034, Template: 50042665, Cera: 770, Gold: 0},    // VIP Premium Contract: 14 Days
	3500026: {Product: 3500026, Template: 50002533, Cera: 200, Gold: 0},    // Neo Premium Contract Plus: 3 Days
	3500027: {Product: 3500027, Template: 50002534, Cera: 380, Gold: 0},    // Neo Premium Contract Plus: 7 Days
	3500028: {Product: 3500028, Template: 50002535, Cera: 710, Gold: 0},    // Neo Premium Contract Plus: 15 Days
	3500032: {Product: 3500032, Template: 50002918, Cera: 990, Gold: 0},    // Neo Premium Contract Plus: 30 Days
	3500005: {Product: 3500005, Template: 16, Cera: 0, Gold: 180000},       // Arad Trading License
	3500006: {Product: 3500006, Template: 193, Cera: 290, Gold: 0},         // Arad Auction License
	3500000: {Product: 3500000, Template: 44, Cera: 150, Gold: 0},          // Tactician's Contract 3D
	3500001: {Product: 3500001, Template: 45, Cera: 280, Gold: 0},          // Tactician's Contract 7D
	3500002: {Product: 3500002, Template: 46, Cera: 525, Gold: 0},          // Tactician's Contract 15D
	3500007: {Product: 3500007, Template: 31, Cera: 120, Gold: 0},          // Conqueror's Contract 3D
	3500008: {Product: 3500008, Template: 33, Cera: 225, Gold: 0},          // Conqueror's Contract 7D
	3500009: {Product: 3500009, Template: 34, Cera: 420, Gold: 0},          // Conqueror's Contract 15D
	3500011: {Product: 3500011, Template: 2660409, Cera: 180, Gold: 0},     // Growth's Contract 3D
	3500012: {Product: 3500012, Template: 2660410, Cera: 335, Gold: 0},     // Growth's Contract 7D
	3500013: {Product: 3500013, Template: 2660411, Cera: 630, Gold: 0},     // Growth's Contract 15D
	3500017: {Product: 3500017, Template: 10000388, Cera: 45, Gold: 0},     // Cube's Contract 3D
	3500016: {Product: 3500016, Template: 10000389, Cera: 85, Gold: 0},     // Cube's Contract 7D
	3000678: {Product: 3000678, Template: 10000389, Cera: 0, Gold: 500000}, // Cube's Contract 7D
	3500015: {Product: 3500015, Template: 10000390, Cera: 160, Gold: 0},    // Cube's Contract 15D
	3000150: {Product: 3000150, Template: 821, Cera: 390, Gold: 0},         // Dual Skill Build License
	3000152: {Product: 3000152, Template: 2660239, Cera: 290, Gold: 0},     // Character Slot Extension Kit
	3000153: {Product: 3000153, Template: 2660698, Cera: 470, Gold: 0},     // Character Renaming Ticket
}
