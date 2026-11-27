package protocol

import "fmt"

// DebugDumpV224 prints the V224 roster layout field offsets.
func DebugDumpV224() {
	roles := []CharacterRow{
		{Slot: 0, Name: "us100a", Profession: 0, Advancement: 0, Level: 1},
		{Slot: 1, Name: "us100b", Profession: 3, Advancement: 0, Level: 1},
	}
	p, err := CharacterListV224(8, roles)
	if err != nil {
		fmt.Println("err", err)
		return
	}
	fmt.Println("total", len(p))
	fmt.Println("header", p[:15])
	row1 := p[15 : 15+93+6]
	fmt.Println("row1 len", len(row1))
	fmt.Printf("row1 slot=%d name=%q packed=%#x level=%d\n",
		uint16(row1[0])|uint16(row1[1])<<8,
		string(row1[6:12]),
		row1[12+2], row1[12+3])
	row2start := 15 + 93 + 6
	row2 := p[row2start : row2start+93+5]
	fmt.Println("row2 len", len(row2))
	fmt.Printf("row2 slot=%d name=%q packed=%#x level=%d\n",
		uint16(row2[0])|uint16(row2[1])<<8,
		string(row2[6:11]),
		row2[12+2], row2[12+3])
	fmt.Println("tail", p[len(p)-10:])
}
