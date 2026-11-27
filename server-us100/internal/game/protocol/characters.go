// Package protocol contains build-specific packet layouts, independent of
// persistence and game rules. Layout evidence is recorded under docs/protocol.
package protocol

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type CreateRequest struct {
	Profession byte
	Name       string
	Options    []byte
}

func parseName(p []byte) (string, int, error) {
	if len(p) < 4 {
		return "", 0, fmt.Errorf("missing name length")
	}
	n := int(binary.LittleEndian.Uint32(p))
	if n < 1 || n > 31 || n > len(p)-4 {
		return "", 0, fmt.Errorf("invalid name length")
	}
	name := string(p[4 : 4+n])
	if !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return "", 0, fmt.Errorf("invalid name encoding or surrounding spaces")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", 0, fmt.Errorf("control character in name")
		}
	}
	return name, 4 + n, nil
}
func padding(p []byte, alignment int) error {
	if len(p) >= alignment {
		return fmt.Errorf("excess packet tail")
	}
	for _, b := range p {
		if b != 0 {
			return fmt.Errorf("nonzero packet padding")
		}
	}
	return nil
}

// Native sender 0x1402396f0 writes one length-prefixed string (cipher slot 12).
func DecodeNameRequest(p []byte) (string, error) {
	n, k, e := parseName(p)
	if e != nil {
		return "", e
	}
	return n, padding(p[k:], 16)
}

// Native legacy sender 0x1402394b4 writes ten option bytes. The current
// naming window sender 0x141733fe7 writes twelve, including a selectable
// value at option 8 and two further bytes at 0x1417340ca/0x1417340d9.
func DecodeCreateRequest(p []byte) (CreateRequest, error) {
	var r CreateRequest
	if len(p) < 1 {
		return r, fmt.Errorf("missing profession")
	}
	r.Profession = p[0]
	n, k, e := parseName(p[1:])
	if e != nil {
		return r, e
	}
	r.Name = n
	k++
	if len(p) < k+10 {
		return r, fmt.Errorf("short creation options")
	}
	optionCount := 10
	if len(p)-k >= 12 {
		optionCount = 12
	}
	r.Options = append([]byte(nil), p[k:k+optionCount]...)
	suffix := [5]byte{0, 0, 0, 255, 0}
	for i, b := range suffix {
		if r.Options[i+3] != b {
			return r, fmt.Errorf("unsupported creation option layout")
		}
	}
	return r, padding(p[k+optionCount:], 8)
}
func add16(p []byte, v uint16) []byte   { return binary.LittleEndian.AppendUint16(p, v) }
func add32(p []byte, v uint32) []byte   { return binary.LittleEndian.AppendUint32(p, v) }
func addName(p []byte, s string) []byte { return append(add32(p, uint32(len(s))), []byte(s)...) }

// The native create callback feeds this value into the roster-position lookup
// (0x1401f8a30 -> 0x14021adf0), not into a persistent character-ID lookup.
func CreateSuccess(slot uint16, name string) []byte { return addName(add16([]byte{1}, slot), name) }
func Refusal(code uint16) []byte                    { return add16([]byte{0}, code) }

type Equipment struct {
	Slot byte
	Item uint32
}
type CharacterRow struct {
	Slot             uint16
	Name             string
	Profession       byte
	Advancement      byte
	Level            byte
	Equipment        []Equipment
	FatigueRemaining uint16
	FatigueBonus     uint16
}

// Native list parser 0x145637a20, row parser 0x14563e280. Unknown scalar
// fields are zero in this experimental baseline, recorded as such in evidence.
// RosterRowPad is a development knob: extra bytes written after each roster
// row's name (see the comment inside CharacterList). Zero keeps the layout the
// native 2.38 row parser documents.
var RosterRowPad int

func CharacterList(capacity uint16, roles []CharacterRow) ([]byte, error) {
	if capacity == 0 || len(roles) > int(capacity) {
		return nil, fmt.Errorf("invalid character capacity")
	}
	p := []byte{2, 0, 0}
	p = add16(p, capacity)
	p = add16(p, 0)
	p = add16(p, 0)
	p = add32(p, 0)
	p = add16(p, uint16(len(roles)))
	for index, r := range roles {
		// 0x1401f64d8 builds the native lookup from insertion positions. The
		// row key and create receipt must use that same zero-based position.
		if r.Slot != uint16(index) || r.Level == 0 || len(r.Equipment) > 24 {
			return nil, fmt.Errorf("invalid character row")
		}
		if _, _, e := parseName(addName(nil, r.Name)); e != nil {
			return nil, e
		}
		p = addName(add16(p, r.Slot), r.Name)
		// RosterRowPad inserts extra bytes after the name. The archived 39 build
		// produced a 144-byte one-character roster for the six-character name
		// "fdfdfd" (client_trace.txt), i.e. 113 fixed bytes plus the name, while
		// this encoder writes 111; a uniform shift corrupts every row after the
		// first, so the offset is a runtime experiment.
		for i := 0; i < RosterRowPad; i++ {
			p = append(p, 0)
		}
		p = append(p, 0, r.Profession, r.Advancement, r.Level, 0, 0)
		// Equipment helper 0x145639840: 31 bytes per populated slot.
		p = append(p, byte(len(r.Equipment)))
		for _, e := range r.Equipment {
			if e.Slot >= 24 {
				return nil, fmt.Errorf("equipment slot out of bounds")
			}
			p = append(p, e.Slot)
			p = add32(p, e.Item)
			p = append(p, make([]byte, 13)...)
			p = add32(p, 0)
			p = add32(p, 0)
			p = add32(p, 0)
			p = append(p, 0)
		}
		p = add32(p, 0)
		p = append(p, 0, 0, 0, 0)
		p = append(p, 0) // cosmetic helper 0x145639b70: zero count
		p = add32(p, 0)
		p = add32(p, 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0)
		p = append(p, 0) // premium PC room helper 0x14563be50
		p = add32(p, 0)
		p = append(p, 1) // client default status bit field
		p = append(p, make([]byte, 8+28)...)
		p = add32(p, 0)
		p = add16(p, 0)
		p = append(p, 0, 0, 0)
		for i := 0; i < 4; i++ {
			p = add32(p, 0)
		}
		// 14563ea6c/76 -> info+6d8/+6dc -> 140209be2 ->
		// 14020a04d renders these exact two values as "%d+%d".
		if r.FatigueRemaining > 32767 || r.FatigueBonus > 32767 {
			return nil, fmt.Errorf("roster fatigue exceeds native signed range")
		}
		p = add16(p, r.FatigueRemaining)
		p = add16(p, r.FatigueBonus)
		p = add32(p, 0)
	}
	p = append(p, 1, 0)
	p = add32(p, 0)
	p = add32(p, 0)
	return p, nil
}

// CharacterListV224 encodes the US100 (2.24.0.124) roster list. The exact
// byte layout was recovered from the client's own packet cursor (frida trace
// of the read helpers during the NOTI2 mode-2 parse) and every read in that
// trace lands on a field boundary below. Row-relative offsets after the
// name:
//
//	u8 x6, u32@18, u32@22, u8@26, u32@27, u8 x4@31, u32@35, u32@39, u8@43,
//	u32@44, u8@48, u8@49, u32@50, u8 x26@54, u32@80, u16@84, u8 x3@86,
//	u32@89, u16@93 + u16@95 (fatigue pair), u32@97
//
// With the u16 slot and u32 name length that is 95 fixed bytes plus the name
// (101 bytes for a six-byte name), matching the trace's row span.
//
// An earlier arrangement kept the same total length but placed the lone u8
// that belongs at offset 49 at the row end instead, shifting every field
// after offset 49 by one byte. The shift was visible in the client: the u32
// at 89 read across the fatigue byte 156 (0x9C) and rendered 0x9C000000
// (-1677721600) in the character-select panel, and the fatigue pair itself
// was read two bytes early ("0+0" regardless of the stored value). The
// header (mode 2, two context bytes, three u16, one u32, one u16 count)
// and the four-field tail are shared with the 2.38 build.
func CharacterListV224(capacity uint16, roles []CharacterRow) ([]byte, error) {
	if capacity == 0 || len(roles) > int(capacity) {
		return nil, fmt.Errorf("invalid character capacity")
	}
	p := []byte{2, 0, 0}
	p = add16(p, capacity)
	p = add16(p, 0)
	p = add16(p, 0)
	p = add32(p, 0)
	p = add16(p, uint16(len(roles)))
	for index, r := range roles {
		if r.Slot != uint16(index) || r.Level == 0 {
			return nil, fmt.Errorf("invalid character row")
		}
		if _, _, e := parseName(addName(nil, r.Name)); e != nil {
			return nil, e
		}
		if r.FatigueRemaining > 32767 || r.FatigueBonus > 32767 {
			return nil, fmt.Errorf("roster fatigue exceeds native signed range")
		}
		p = addName(add16(p, r.Slot), r.Name)
		// Six bytes after the name. Field semantics were settled by a one-run
		// bisect (service.RosterLayoutProbe) with four candidate rows whose
		// values were all "3 = mage" and whose labels travelled with the row:
		// the row carrying 3 in the *second* byte's low nibble rendered the
		// Mage portrait and the "Mage" class text, while 3 in the first byte
		// changed nothing and 3 in the second byte's bits 4-6 blanked the row.
		// So byte 1 = zero, byte 2 = job in the low nibble (0-15 holds every
		// base profession) with the advancement in bits 4-6, byte 3 = zero,
		// byte 4 = level. Putting the packed job in byte 3 earlier produced
		// the Slayer portrait with the "berserker" class text.
		p = append(p, 0, r.Profession&0xf|(r.Advancement&7)<<4, 0, r.Level, 0, 0)
		p = add32(add32(p, 0), 0)
		// 0x143807725 calls the exact same 0x143803660 appearance parser as
		// NOTI 2 mode 0: u8 count followed by 31 bytes per worn avatar/weapon slot.
		var err error
		p, err = appendAppearanceRowsV224(p, r.Equipment)
		if err != nil {
			return nil, err
		}
		p = add32(p, 0)
		p = append(p, 0, 0, 0, 0)
		p = add32(add32(p, 0), 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0, 0)
		p = add32(p, 0)
		p = append(p, make([]byte, 26)...)
		p = add32(p, 0)
		p = add16(p, 0)
		p = append(p, 0, 0, 0)
		p = add32(p, 0)
		p = add16(p, r.FatigueRemaining)
		p = add16(p, r.FatigueBonus)
		p = add32(p, 0)
	}
	p = append(p, 1, 0)
	p = add32(p, 0)
	p = add32(p, 0)
	return p, nil
}

// V224ProbeRow carries the six post-name bytes explicitly for the roster
// field-semantics bisect.
type V224ProbeRow struct {
	Slot                           uint16
	Name                           string
	Bytes                          [6]byte
	FatigueRemaining, FatigueBonus uint16
}

// CharacterListV224LayoutProbe encodes the corrected V224 row with the six
// post-name bytes supplied per row, so one client launch can test several
// candidate field assignments at once. Semantic recovery only; not for
// gameplay traffic.
func CharacterListV224LayoutProbe(capacity uint16, rows []V224ProbeRow) ([]byte, error) {
	if capacity == 0 || len(rows) > int(capacity) {
		return nil, fmt.Errorf("invalid character capacity")
	}
	p := []byte{2, 0, 0}
	p = add16(p, capacity)
	p = add16(p, 0)
	p = add16(p, 0)
	p = add32(p, 0)
	p = add16(p, uint16(len(rows)))
	for index, r := range rows {
		if r.Slot != uint16(index) {
			return nil, fmt.Errorf("invalid probe row slot")
		}
		if _, _, e := parseName(addName(nil, r.Name)); e != nil {
			return nil, e
		}
		if r.FatigueRemaining > 32767 || r.FatigueBonus > 32767 {
			return nil, fmt.Errorf("roster fatigue exceeds native signed range")
		}
		p = addName(add16(p, r.Slot), r.Name)
		p = append(p, r.Bytes[:]...)
		p = add32(add32(p, 0), 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0, 0, 0, 0)
		p = add32(add32(p, 0), 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0, 0)
		p = add32(p, 0)
		p = append(p, make([]byte, 26)...)
		p = add32(p, 0)
		p = add16(p, 0)
		p = append(p, 0, 0, 0)
		p = add32(p, 0)
		p = add16(p, r.FatigueRemaining)
		p = add16(p, r.FatigueBonus)
		p = add32(p, 0)
	}
	p = append(p, 1, 0)
	p = add32(p, 0)
	p = add32(p, 0)
	return p, nil
}

// CharacterListV224Markers is the V224 layout with per-row distinct markers in
// the six post-name bytes only: row 0 gets 1..6, row 1 gets 6..1 (all values
// stay in the 1..6 range observed safe). All other fields are zero (some are
// equipment/cosmetic counts or pool indices that must stay in range). Not for
// gameplay use.
func CharacterListV224Markers(capacity uint16, roles []CharacterRow) ([]byte, error) {
	if capacity == 0 || len(roles) > int(capacity) {
		return nil, fmt.Errorf("invalid character capacity")
	}
	p := []byte{2, 0, 0}
	p = add16(p, capacity)
	p = add16(p, 0)
	p = add16(p, 0)
	p = add32(p, 0)
	p = add16(p, uint16(len(roles)))
	for index, r := range roles {
		if r.Slot != uint16(index) || r.Level == 0 || len(r.Equipment) > 0 {
			return nil, fmt.Errorf("invalid character row")
		}
		var six [6]byte
		if index == 0 {
			six = [6]byte{1, 2, 3, 4, 5, 6}
		} else {
			six = [6]byte{6, 5, 4, 3, 2, 1}
		}
		p = addName(add16(p, r.Slot), r.Name)
		p = append(p, six[:]...)
		p = add32(add32(p, 0), 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0, 0, 0, 0)
		p = add32(add32(p, 0), 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0)
		p = add32(p, 0)
		p = append(p, 0, 0)
		p = add32(p, 0)
		p = append(p, make([]byte, 26)...)
		p = add32(p, 0)
		p = add16(p, 0)
		p = append(p, 0, 0, 0)
		p = add32(p, 0)
		p = add16(p, 0)
		p = add16(p, 0)
		p = add32(p, 0)
	}
	p = append(p, 1, 0)
	p = add32(p, 0)
	p = add32(p, 0)
	return p, nil
}
