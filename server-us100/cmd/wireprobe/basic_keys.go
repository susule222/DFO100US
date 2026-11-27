package main

import (
	"bytes"
	"dfolan/internal/db100"
	"dfolan/internal/storage"
	"encoding/binary"
	"fmt"
	"strconv"
)

// Basic keyboard editor, distinct from the per-skill CMD331 command.
// CMD198 at 143793ab6 writes a string then two u8s (flag, effective preset).
// NOTI440 at 1434fb140 reads preset then a single byte-length-prefixed string.
// recvHotkeyOption2 has a strict n < 0x578 bound.
const keymapTable = "account_basic_keys_v224"

func validateBasicKeys(raw []byte) error {
	if len(raw) == 0 || len(raw) >= 0x578 || raw[len(raw)-1] != '\n' {
		return fmt.Errorf("invalid keyboard table length or terminator")
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		tag, value, ok := bytes.Cut(line, []byte{'\t'})
		if !ok || len(tag) != 3 || (tag[0] != 'I' && tag[0] != 'K') || tag[1] < '0' || tag[1] > '9' || tag[2] < '0' || tag[2] > '9' || seen[string(tag)] || len(value) < 1 || len(value) > 3 {
			return fmt.Errorf("invalid or repeated keyboard binding")
		}
		seen[string(tag)] = true
		for _, c := range value {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid key code")
			}
		}
		if _, e := strconv.ParseUint(string(value), 10, 8); e != nil {
			return e
		}
	}
	if !seen["I08"] {
		return fmt.Errorf("not a full basic keyboard map")
	}
	return nil
}

func basicKeysOnEntry(store *storage.Store, account int64) ([]byte, error) {
	var raw []byte
	ok, e := store.Database().Get(keymapTable, strconv.FormatInt(account, 10), &raw)
	if e != nil || !ok || len(raw) == 0 {
		return nil, e
	}
	if e = validateBasicKeys(raw); e != nil {
		return nil, e
	}
	return append(binary.LittleEndian.AppendUint32([]byte{0}, uint32(len(raw))), raw...), nil
}

func saveBasicKeys(store *storage.Store, account int64, p []byte) ([]byte, error) {
	if len(p) < 6 {
		return nil, fmt.Errorf("short keyboard request")
	}
	n := int64(binary.LittleEndian.Uint32(p))
	if n < 1 || n >= 0x578 || n+6 > int64(len(p)) {
		return nil, fmt.Errorf("invalid keyboard request size")
	}
	raw := p[4 : 4+n]
	flags := p[4+n : 6+n]
	tail := p[6+n:]
	if len(tail) >= 16 || len(bytes.Trim(tail, "\x00")) != 0 {
		return nil, fmt.Errorf("invalid keyboard padding")
	}
	// Only the verified ordinary keyboard upload may be echoed. Do not apply
	// it to a different preset on next login if the user switched profiles.
	standard := flags[0] == 0 && flags[1] == 0
	if e := validateBasicKeys(raw); e != nil {
		return nil, e
	}
	if !standard {
		raw = nil
	}
	key := strconv.FormatInt(account, 10)
	e := store.Database().Mutate(func(tx *db100.Tx) error {
		var old []byte
		_, err := tx.Get(keymapTable, key, &old)
		if err != nil || bytes.Equal(raw, old) {
			return err
		}
		return tx.Put(keymapTable, key, raw)
	})
	if e != nil || !standard {
		return nil, e
	}
	return append(binary.LittleEndian.AppendUint32([]byte{0}, uint32(len(raw))), raw...), nil
}
