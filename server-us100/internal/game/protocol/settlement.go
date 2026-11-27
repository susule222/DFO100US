package protocol

import (
	"encoding/binary"
	"fmt"
)

type PlayResultRequest struct {
	Actor     uint16
	RankPoint byte
}

func DecodePlayResult(p []byte) (PlayResultRequest, error) {
	var r PlayResultRequest
	// Current solo native writer emits113B. Send flush appends the folded
	// MD5, then cipher padding rounds117B to128B.
	if len(p) != 113 && len(p) != 128 {
		return r, fmt.Errorf("unsupported current play-result size")
	}
	if p[0] != 0 || p[3] != 1 {
		return r, fmt.Errorf("play-result requires ordinary solo row")
	}
	if e := digestRequestTail(p, 113, 16); e != nil {
		return r, e
	}
	r.Actor = binary.LittleEndian.Uint16(p[4:])
	r.RankPoint = p[10]
	if r.Actor == 0 || r.Actor == 65535 || r.RankPoint != p[75] {
		return r, fmt.Errorf("invalid solo result identity or rank")
	}
	return r, nil
}

func PlayResultNotice(actor uint16, grade, rank byte, elapsed uint32, allClear bool) ([]byte, error) {
	return PlayResultNoticeRecord(actor, grade, rank, elapsed, elapsed, allClear, true)
}

func PlayResultNoticeRecord(actor uint16, grade, rank byte, elapsed, best uint32, allClear, improved bool) ([]byte, error) {
	if actor == 0 || actor == 65535 {
		return nil, fmt.Errorf("invalid result actor")
	}
	p := add32([]byte{grade}, elapsed)
	flag := byte(0)
	if allClear {
		flag = 1
	}
	p = append(p, 0, rank, flag, 1)
	// Native1452b1caf..1cdc uses the first time and its result flag for the
	// personal-record announcement. -1 suppresses a record-update notice.
	flagRecord := uint32(0xffffffff)
	if improved {
		flagRecord = 0
	}
	p = add32(add16(p, actor), best)
	p = add32(p, flagRecord)
	p = add32(p, 0) // no party record
	p = add32(p, 0)
	return add32(p, 0xffffffff), nil
}

type CardReward struct {
	Template, Amount uint32
	Metadata         [21]byte
}
type ClearRewardState struct {
	BaseExperience, ScoreExperience, MonsterExperience uint32
	Cards                                              [8][]CardReward
}

func ClearReward(p ClearRewardState) ([]byte, error) {
	if p.BaseExperience == 0 || uint64(p.BaseExperience)+uint64(p.ScoreExperience) > 0xffffffff {
		return nil, fmt.Errorf("invalid committed clear reward")
	}
	// Current NOTI35 full empty-group path is281B. Kept separate from the
	// reference90 210B grammar. This encoder covers EXP and empty card groups.
	out := make([]byte, 281)
	binary.LittleEndian.PutUint32(out[0:], p.BaseExperience)
	binary.LittleEndian.PutUint32(out[4:], p.ScoreExperience)
	out[167] = 1 // current result-window flag read at1452a7b73
	binary.LittleEndian.PutUint32(out[169:], p.MonsterExperience)
	// Native tail uses -1 for the absence of two optional event results.
	binary.LittleEndian.PutUint32(out[265:], 0xffffffff)
	binary.LittleEndian.PutUint32(out[277:], 0xffffffff)
	// First card group at1452a74c8: eight counts, each followed by
	// (template32, amount32, metadata21). 90's two-u32 row is too short.
	group := make([]byte, 0, 8)
	for _, rows := range p.Cards {
		if len(rows) > 127 {
			return nil, fmt.Errorf("too many card rewards")
		}
		group = append(group, byte(len(rows)))
		for _, row := range rows {
			if row.Amount == 0 {
				return nil, fmt.Errorf("empty card reward")
			}
			group = add32(add32(group, row.Template), row.Amount)
			group = append(group, row.Metadata[:]...)
		}
	}
	return append(append(out[:131:131], group...), out[139:]...), nil
}

// ClearRewardUS224State encodes US100 v2.24 NOTI35 (handler 0x1434d3cc0):
// 131B EXP prefix, Group 0 (8B rows), u32 gold card cost, Group 1 (8B rows),
// Group 2 Neo/PC-Bang extra card (8B rows + u8 style when non-empty), 26B
// result flags/monster EXP, 8B card mode flags, and Groups 5..9 (8B rows).
// Empty-group total is 233B.
type ClearRewardUS224State struct {
	BaseExperience    uint32
	ScoreExperience   uint32
	NeoExperience     uint32
	GrowthExperience  uint32
	MonsterExperience uint32
	GoldCardCost      uint32
	NeoCardStyle      byte
	Cards             [8][]CardReward
	GoldCards         [8][]CardReward
	NeoCards          [8][]CardReward
}

func appendUS224CardGroup(dst []byte, groups [8][]CardReward, withStyle bool, style byte) ([]byte, error) {
	for _, rows := range groups {
		if len(rows) > 127 {
			return nil, fmt.Errorf("too many card rewards")
		}
		dst = append(dst, byte(len(rows)))
		for _, row := range rows {
			if row.Amount == 0 {
				return nil, fmt.Errorf("empty card reward")
			}
			dst = add32(add32(dst, row.Template), row.Amount)
		}
		if withStyle && len(rows) > 0 {
			dst = append(dst, style)
		}
	}
	return dst, nil
}

func ClearRewardUS224(p ClearRewardUS224State) ([]byte, error) {
	total := uint64(p.BaseExperience) + uint64(p.ScoreExperience) + uint64(p.NeoExperience) + uint64(p.GrowthExperience)
	if p.BaseExperience == 0 || total > 0xffffffff {
		return nil, fmt.Errorf("invalid committed clear reward")
	}
	// Native 0x1434d3f85 subtracts offset 8 (Neo/PC-Bang EXP) from offset 0
	// before displaying BaseClearExp, then adds offset 8 back into the total.
	wireBase := uint64(p.BaseExperience) + uint64(p.NeoExperience)
	if wireBase > 0xffffffff {
		return nil, fmt.Errorf("clear base experience overflow")
	}
	out := make([]byte, 131, 260)
	binary.LittleEndian.PutUint32(out[0:], uint32(wireBase))
	binary.LittleEndian.PutUint32(out[4:], p.ScoreExperience)
	binary.LittleEndian.PutUint32(out[8:], p.NeoExperience)
	binary.LittleEndian.PutUint32(out[33:], p.GrowthExperience)

	var e error
	if out, e = appendUS224CardGroup(out, p.Cards, false, 0); e != nil {
		return nil, e
	}
	out = add32(out, p.GoldCardCost)
	if out, e = appendUS224CardGroup(out, p.GoldCards, false, 0); e != nil {
		return nil, e
	}
	if out, e = appendUS224CardGroup(out, p.NeoCards, true, p.NeoCardStyle); e != nil {
		return nil, e
	}
	// 26B middle metadata: u32(0), u32(0), u8(resultWindowFlag=1), u8(0),
	// u32(MonsterExperience), u32(0), u32(0), u32(0).
	out = add32(add32(out, 0), 0)
	out = append(out, 1, 0)
	out = add32(out, p.MonsterExperience)
	out = add32(add32(add32(out, 0), 0), 0)
	// 8B card mode flags (0x1434d4978) + 40B empty Groups 5..9 (0x1434d4a32..c55).
	out = append(out, make([]byte, 8+40)...)
	return out, nil
}
