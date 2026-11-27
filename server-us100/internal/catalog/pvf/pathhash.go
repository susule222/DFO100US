package pvf

import (
	"sort"
)

// pathHashBytes is FNV-1a 64 over the normalized path bytes. Both the build
// side (parseFiles) and the lookup side (findIndex) hash the same normalized
// representation, so equal paths hash equal.
const (
	fnvOffset64 = 0xcbf29ce484222325
	fnvPrime64  = 0x100000001b3
)

func pathHashBytes(b []byte) uint64 {
	h := uint64(fnvOffset64)
	for _, c := range b {
		h ^= uint64(c)
		h *= fnvPrime64
	}
	return h
}

// sortPathEntries orders the path index by hash for binary search.
func sortPathEntries(entries []pathEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].hash < entries[j].hash })
}
