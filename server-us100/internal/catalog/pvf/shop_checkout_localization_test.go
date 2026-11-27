package pvf

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestCheckoutDisplayChangesOnlyTitleReference(t *testing.T) {
	a := &Archive{strA: []byte("The Neo: Awakened Ones Package\x00")}
	b := make([]byte, 601380)
	const start = 52837 * 5
	for i, n := range []uint32{3400149, 590005784, 0, 0, 3980, 0, 0} {
		binary.LittleEndian.PutUint32(b[start+i*5+1:], n)
	}
	const cell = start + 7*5
	b[cell] = 6
	r := []inlineTranslation{{"[package display name]", "The Neo: Awakened Ones Package", "《新：觉醒者》礼包", 1}}
	patch, err := a.translateShopCheckoutDisplay(b, r, func(s string) uint32 { return 12345 })
	if err != nil || !bytes.Equal(b[:cell+1], patch[:cell+1]) || !bytes.Equal(b[cell+5:], patch[cell+5:]) || binary.LittleEndian.Uint32(patch[cell+1:]) != 12345 {
		t.Fatalf("unexpected checkout delta: %v", err)
	}
	binary.LittleEndian.PutUint32(b[start+4*5+1:], 3981)
	if _, err := a.translateShopCheckoutDisplay(b, r, func(s string) uint32 { return 12345 }); err == nil {
		t.Fatal("accepted a different offer")
	}
}
