package main

import "fmt"

// On the 7115/53130 source action the client emits an empty CMD475 when the
// monster-team count reaches zero, also on a revisit. It is not a boss death.
func (w *worldSession) lorienFinale(p []byte) ([]outboundPacket, error) {
	if len(p) != 0 || !w.inTutorial || w.role.ID == 0 {
		return nil, fmt.Errorf("not an owned tutorial end event")
	}
	if e := w.activeDungeon.CompleteLorienFinale(); e != nil {
		return nil, e
	}
	return w.completeDungeon()
}
