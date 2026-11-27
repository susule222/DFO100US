// entrylayout prints the real byte offsets of the US100 entry actor
// notification, so tests assert measured positions instead of arithmetic that
// silently drifts when the encoder changes.
package main

import (
	"dfolan/internal/game/protocol"
	"encoding/hex"
	"fmt"
	"os"
)

func main() {
	name := "LanTest01"
	p, e := protocol.UserInfoBasicProbe(protocol.EntryBasicProbe{
		ActorServerID: 503,
		Context:       [2]byte{4, 7},
		Character:     protocol.CharacterRow{Name: name, Profession: 3, Level: 1},
	})
	if e != nil {
		fmt.Println("error:", e)
		return
	}
	fmt.Printf("total=%d name_len=%d\n", len(p), len(name))
	fmt.Println("actor id at 132:", uint16(p[132])|uint16(p[133])<<8)
	fmt.Println("name at 138:", string(p[138:138+len(name)]))
	fmt.Println("profession at 147:", p[147], "level at 149:", p[149])
	fmt.Println()
	fmt.Println("== tail from 150 ==")
	for off := 150; off < len(p); off += 16 {
		end := off + 16
		if end > len(p) {
			end = len(p)
		}
		fmt.Printf("%4d  %-32s\n", off, hex.EncodeToString(p[off:end]))
	}
	fmt.Println()
	// Report where the previous 2.38-relative flag offsets land after the
	// 33-byte shorter block.
	for _, old := range []int{232, 263, 277} {
		off := old + len(name) - 33
		if 0 <= off && off < len(p) {
			fmt.Printf("old[%d]+len -> %d = %d\n", old, off, p[off])
		}
	}
	os.Exit(0)
}
