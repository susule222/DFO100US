package main

import "sync/atomic"

// dungeonAckKind rotates the frame kind used for the dungeon acknowledgements.
//
// Every packet this client demonstrably dispatches to a handler is kind 0, yet
// the dungeon ids (15/16/27/28/29) never reach their handlers with kind 0 or 1,
// which means the dispatch key for them is not the plain id alone. Rotating the
// kind turns the next few clicks into a self-observing experiment: the minimal
// handler probe reports the first kind that actually arrives.
var dungeonAckKind int32 = 0

func nextDungeonAckKind() byte {
	n := atomic.AddInt32(&dungeonAckKind, 1)
	return byte(n % 5)
}
