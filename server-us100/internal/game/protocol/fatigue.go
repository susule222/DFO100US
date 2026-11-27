package protocol

import "fmt"

// NOTI_FATIGUE 36 / US100 0x1434f7020 reads five u16 values (used, limit,
// usedMax, bonus, burn) followed by a trailing u8 flag at 0x1434f721f (11B).
func Fatigue(used, limit, usedMax uint16) ([]byte, error) {
	if limit == 0 || used > limit {
		return nil, fmt.Errorf("invalid fatigue state")
	}
	p := add16(add16(add16(nil, used), limit), usedMax)
	p = add16(add16(p, 0), 0)
	return append(p, 0), nil
}
