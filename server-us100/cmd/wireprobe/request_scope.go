package main

// Bounded game-command evidence. Commands listed here always have their body
// decoded, verified and retained, on every occurrence.
//
// The list is "ids whose bodies are always worth keeping", not "ids with a
// handler". The two sets overlap but are not equal, and the difference matters
// in one direction only: a command that HAS a handler and is missing here stops
// being decodable after BodySampleLimit occurrences, at which point its handler
// sees an unverified empty body and reports a checksum failure. Id 217 (the
// dungeon scene state the US100 client sends when it enters the transition) was
// exactly that trap until it was added below.
func observedGameRequest(id uint16) bool {
	switch id {
	case 21, 22, 80, 198, 204, 205, 267, 268, 269, 270, 305, 306, 309, 331, 507, 643, 680, 681, 705, 706, 707, 1200, 1201, 1565: // NPC shops, upgrade/purify/grimoire, account vault, membership claims, item actions, event gifts/actions/queries, Customize, inventory locks, and Skin Storage: verify every occurrence
		return true
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 15, 16, 19, 26, 28, 29, 31, 32, 33, 34, 35, 36, 37, 39, 42, 43, 44, 45, 46, 69, 70, 71, 72, 117, 132, 143, 191, 217, 433, 475, 623, 627, 637, 684, 848, 1301, 1554:
		return true
	}
	return false
}

// BodySampleLimit is how many bodies are retained per not-yet-implemented
// command, per connection.
//
// Features that are not implemented are exactly the ones with no recovered
// packet layout, and until 36 their bodies were discarded by the whitelist
// above: a capture could show that the client sent command 407 fifty times
// and nothing about what it contained. Sampling a few of every command turns
// the next ordinary play session into the evidence needed to implement mail,
// avatars, the shop and item use from this client's own wire format instead
// of porting another build's opcodes.
//
// The cap is what keeps that affordable: one run has sent a single telemetry
// command over six thousand times, so retaining every body is not an option.
const BodySampleLimit = 8

// retainRequestBody reports whether this occurrence's plaintext should be
// kept. Implemented commands are always retained; everything else is sampled
// up to the cap and then counted as metadata only.
func retainRequestBody(id uint16, seen map[uint16]int) bool {
	if observedGameRequest(id) {
		return true
	}
	if seen == nil || seen[id] >= BodySampleLimit {
		return false
	}
	seen[id]++
	return true
}
