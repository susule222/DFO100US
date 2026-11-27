// Command dumppayload writes protocol payloads to files for inject-watch tests.
package main

import (
	"fmt"
	"os"

	"dfolan/internal/game/protocol"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dumppayload <account-options|account-options-198|roster> [out]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "account-options", "account-options-198":
		overrides := map[uint16]uint16(nil)
		if os.Args[1] == "account-options-198" {
			overrides = map[uint16]uint16{198: 1}
		}
		payload, err := protocol.AccountOptions(overrides)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[2], payload, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s: %d bytes\n", os.Args[2], len(payload))
	case "roster-pad2":
		protocol.RosterRowPad = 2
		one := []protocol.CharacterRow{{Slot: 0, Name: "fdfdfd", Profession: 0, Level: 1}}
		p, err := protocol.CharacterList(8, one)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		fmt.Printf("roster-pad2: 1 char payload=%d (expect 144)\n", len(p))
	case "roster":
		// one character, same shape as the reference session's 144-byte roster
		one := []protocol.CharacterRow{{Slot: 0, Name: "fdfdfd", Profession: 0, Level: 1}}
		p1, err := protocol.CharacterList(8, one)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode 1:", err)
			os.Exit(1)
		}
		two := []protocol.CharacterRow{
			{Slot: 0, Name: "KRDMP001", Profession: 0, Level: 1, FatigueRemaining: 0, FatigueBonus: 0},
			{Slot: 1, Name: "KRDMP002", Profession: 1, Level: 1, FatigueRemaining: 0, FatigueBonus: 0},
		}
		p2, err := protocol.CharacterList(8, two)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode 2:", err)
			os.Exit(1)
		}
		eight := make([]protocol.CharacterRow, 0, 8)
		for i := 0; i < 8; i++ {
			eight = append(eight, protocol.CharacterRow{Slot: uint16(i), Name: fmt.Sprintf("CH%06d", i), Profession: byte(i % 5), Level: byte(10 + i)})
		}
		p8, err := protocol.CharacterList(8, eight)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode 8:", err)
			os.Exit(1)
		}
		fmt.Printf("1 char (fdfdfd): payload=%d bytes\n", len(p1))
		fmt.Printf("  reference session: 144 bytes for one 6-char character\n")
		fmt.Printf("2 chars (KRDMP001/2): payload=%d bytes (observed frame 288 -> payload 272?)\n", len(p2))
		fmt.Printf("8 chars: payload=%d bytes\n", len(p8))
		fmt.Printf("header=15 tail=10 -> row size for 6-char name = %d\n", len(p1)-25)
		fmt.Printf("hex head: % x\n", p1[:24])
	}
}
