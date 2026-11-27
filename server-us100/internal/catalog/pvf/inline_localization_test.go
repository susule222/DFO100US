package pvf

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestTranslateInlineDisplayKeepsOtherCells(t *testing.T) {
	a := &Archive{strA: []byte("[name]\x00[rarity]\x00English\x00")}
	var raw []byte
	for _, pair := range []struct {
		kind  byte
		value uint32
	}{{3, 0}, {6, 32}, {3, 14}, {6, 32}, {0, 17}} {
		raw = binary.LittleEndian.AppendUint32(append(raw, pair.kind), pair.value)
	}
	rows := []inlineTranslation{{"[name]", "English", "\u4e2d\u6587", 1}}
	got, err := a.translateInlineDisplay(raw, rows, func(string) uint32 { return 19 })
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(got[6:10]) != 19 || !bytes.Equal(got[10:], raw[10:]) ||
		binary.LittleEndian.Uint32(raw[6:10]) != 32 {
		t.Fatal("inline replacement modified a sibling token or loaded source")
	}
	rows[0].Count = 2
	if _, err := a.translateInlineDisplay(raw, rows, func(string) uint32 { return 19 }); err == nil {
		t.Fatal("mismatching occurrence count was accepted")
	}
}
