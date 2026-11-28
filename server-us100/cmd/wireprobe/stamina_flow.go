package main

import (
	"context"
	"crypto/sha256"
	"dfolan/internal/game/protocol"
	"dfolan/internal/inventory"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
)

func readCharacterStamina(state json.RawMessage) (byte, bool) {
	if len(state) == 0 {
		return 0, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(state, &fields); err != nil || fields == nil {
		return 0, false
	}
	raw, ok := fields["stamina"]
	if !ok || len(raw) == 0 {
		return 0, false
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return byte(v), true
}

func writeCharacterStamina(state json.RawMessage, stamina byte) (json.RawMessage, error) {
	if stamina > 100 {
		stamina = 100
	}
	var fields map[string]json.RawMessage
	if len(state) > 0 {
		if err := json.Unmarshal(state, &fields); err != nil {
			return nil, err
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	enc, err := json.Marshal(int(stamina))
	if err != nil {
		return nil, err
	}
	fields["stamina"] = enc
	return json.Marshal(fields)
}

// handleRecoverStamina processes CMD 9 (0x09, Shusia Stamina/Weakness Recovery),
// deducts the exact ServerParameter.etc fee from character gold, persists
// stamina=100, and returns ACK 9 (u8(1) + u32(newGold)) so the client clears
// weakness, restores HP/MP to 100%, updates gold, and closes the dialog.
func (w *worldSession) handleRecoverStamina(ctx context.Context, rawFrame []byte) ([]outboundPacket, uint16, error) {
	if w == nil || w.characters == nil || w.characters.Store == nil || w.role.ID == 0 {
		return nil, protocol.StaminaRecoveryErrCannotRecover, fmt.Errorf("world session not ready for stamina recovery")
	}
	eventKey := fmt.Sprintf("recover_stamina/%d/%x", w.role.ID, sha256.Sum256(rawFrame))
	var (
		refuseCode uint16
		afterGold  uint32
	)
	saved, _, err := w.characters.Store.CommitCharacterEvent(ctx, w.account, w.role.ID, w.role.ConfigVersion, eventKey, "us100-recover-stamina-v1", func(current storage.Character) (json.RawMessage, json.RawMessage, error) {
		level := characterLevelFromState(current.State)
		if level < 1 {
			level = 1
		}
		curStamina, persisted := readCharacterStamina(current.State)
		var cost uint32
		if !persisted || curStamina < 100 {
			cost = protocol.StaminaRecoveryCost(uint32(level), uint32(curStamina))
		}
		bag, err := inventory.ReadBag(current.State)
		if err != nil {
			refuseCode = protocol.StaminaRecoveryErrCannotRecover
			return nil, nil, err
		}
		if bag.Gold < cost {
			refuseCode = protocol.StaminaRecoveryErrInsufficientGold
			return nil, nil, fmt.Errorf("insufficient gold for stamina recovery: have %d need %d", bag.Gold, cost)
		}
		bag.Gold -= cost
		afterGold = bag.Gold
		updated, err := inventory.SaveBag(current.State, bag)
		if err != nil {
			refuseCode = protocol.StaminaRecoveryErrCannotRecover
			return nil, nil, err
		}
		updated, err = writeCharacterStamina(updated, 100)
		if err != nil {
			refuseCode = protocol.StaminaRecoveryErrCannotRecover
			return nil, nil, err
		}
		receipt, err := json.Marshal(map[string]any{
			"level":      level,
			"from":       curStamina,
			"to":         100,
			"cost":       cost,
			"after_gold": afterGold,
		})
		return updated, receipt, err
	})
	if err != nil {
		if refuseCode == 0 {
			refuseCode = protocol.StaminaRecoveryErrCannotRecover
		}
		return nil, refuseCode, err
	}
	saved.WireID = w.role.WireID
	w.role = saved

	return []outboundPacket{
		{
			Name:    "stamina_recovery_ack",
			Kind:    1,
			ID:      protocol.CmdRecoverStamina,
			Payload: protocol.StaminaRecoverySuccess(afterGold),
		},
	}, 0, nil
}
