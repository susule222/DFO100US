package main

import (
	"bytes"
	"context"
	"dfolan/internal/storage"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestBasicKeysApplyAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	open := func() *storage.Store {
		s, e := storage.Open(context.Background(), storage.Config{DBPath: path})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	s := open()
	defer func() { s.Close() }()
	p := func(text string) []byte {
		x := binary.LittleEndian.AppendUint32(nil, uint32(len(text)))
		x = append(x, []byte(text)...)
		return append(x, 0, 0)
	}
	check := func(account int64, want []byte) {
		t.Helper()
		v, e := basicKeysOnEntry(s, account)
		if e != nil || !bytes.Equal(v, want) {
			t.Fatal("entry mismatch", e)
		}
	}
	check(41, nil)
	var expected, src []byte
	for _, v := range []string{"56", "13"} {
		src = p("I07\t80\nI08\t" + v + "\nK12\t109\n")
		b, e := saveBasicKeys(s, 41, src)
		expected = append([]byte{0}, src[:len(src)-2]...)
		if e != nil || !bytes.Equal(expected, b) {
			t.Fatal("noti440", e)
		}
	}
	if path := os.Getenv("US100_BASIC_KEYS_FIXTURE"); path != "" {
		var e error
		src, e = os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		n := binary.LittleEndian.Uint32(src)
		b, e := saveBasicKeys(s, 41, src)
		expected = append([]byte{0}, src[:4+n]...)
		if e != nil || !bytes.Equal(b, expected) {
			t.Fatal("live frame", e)
		}
	}
	s.Close()
	s = open()
	check(41, expected)
	check(42, nil)
	for _, bad := range [][]byte{{255, 255, 255, 255}, p("I08\t999\n"), p("I08\t13\nI08\t56\n"), append(p("I08\t13\n"), 1)} {
		if _, e := saveBasicKeys(s, 41, bad); e == nil {
			t.Fatal("accepted invalid")
		}
		check(41, expected)
	}
	alt := p("I08\t56\n")
	alt[len(alt)-1] = 1
	if b, e := saveBasicKeys(s, 41, alt); e != nil || b != nil {
		t.Fatal("cross preset restore", e)
	}
	check(41, nil)
	if !observedGameRequest(198) {
		t.Fatal("basic keyboard saves subject to sampling")
	}
}
