package quest

import (
	"fmt"
	"math"
)

// The thresholds are cumulative; a percentage applies to one level's span.
func percentExperience(thresholds []uint64, level, cap byte, percent uint64) (uint32, error) {
	if percent == 0 || percent > 100 || level == 0 || level > cap {
		return 0, fmt.Errorf("invalid percent experience reward")
	}
	if level == cap {
		return 0, nil
	}
	if int(level) > len(thresholds) {
		return 0, fmt.Errorf("missing level threshold")
	}
	var lo uint64
	if level > 1 {
		lo = thresholds[level-2]
	}
	hi := thresholds[level-1]
	if hi <= lo {
		return 0, fmt.Errorf("invalid level threshold")
	}
	span := hi - lo
	gain := span/100*percent + span%100*percent/100
	if gain > math.MaxUint32 {
		return 0, fmt.Errorf("quest experience exceeds packet range")
	}
	return uint32(gain), nil
}
