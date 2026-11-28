package main

import (
	"dfolan/internal/db100"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"strconv"
)

// Option 82 (0x52) in the 219-entry v2.24 general options table controls
// CinematicManager+0x9d90 ("自动跳过动画", OptionSystem_0 / control 350):
//   - 0: do not auto-skip
//   - 1: skip all cutscenes ("跳过所有过场动画")
//   - 2: skip dungeon cutscenes (type == 0)
//   - 3: skip scenario/story cutscenes (type == 1)
//
// DFO_unpacked.exe ShouldAutoSkip (0x141f980a0..0x141f98175) still gates
// skipping behind CinematicManager+0x150[cinematicID] != 0 (populated by
// NOTI 1352 / 0x548). Sending all-1s when Option 82 == 1 lets "跳过所有过场动画"
// skip every skippable cutscene immediately (even on first viewing), while
// other modes use the account's persisted CMD 1417 (0x589) seen bitmask.
const (
	cinematicSkipOptionIndex = 82
	cinematicSkipModeAll     = 1
	seenCinematicsTable      = "account_seen_cinematics_v224"
	storyDigestLevelTable    = "character_story_digest_level_v224"
)

func generalOptionValue(store *storage.Store, account int64, idx int) (uint16, bool, error) {
	if store == nil || account <= 0 || idx < 0 || idx*2+2 > generalOptionBytes {
		return 0, false, nil
	}
	var table []byte
	ok, err := store.Database().Get(generalOptionsTable, strconv.FormatInt(account, 10), &table)
	if err != nil || !ok || len(table) != generalOptionBytes {
		return 0, false, err
	}
	return binary.LittleEndian.Uint16(table[idx*2 : idx*2+2]), true, nil
}

func recordSeenCinematic(store *storage.Store, account int64, id uint16) error {
	if store == nil || account <= 0 || int(id) >= protocol.SeenCinematicMaxID {
		return nil
	}
	key := strconv.FormatInt(account, 10)
	byteIdx := int(id) >> 3
	bitMask := byte(1 << (id & 7))
	return store.Database().Mutate(func(tx *db100.Tx) error {
		var mask []byte
		ok, err := tx.Get(seenCinematicsTable, key, &mask)
		if err != nil {
			return err
		}
		if !ok || len(mask) != protocol.SeenCinematicBytes {
			next := make([]byte, protocol.SeenCinematicBytes)
			copy(next, mask)
			mask = next
		}
		if (mask[byteIdx] & bitMask) != 0 {
			return nil
		}
		mask[byteIdx] |= bitMask
		return tx.Put(seenCinematicsTable, key, mask)
	})
}

func seenCinematicsOnEntry(store *storage.Store, account int64) ([]byte, error) {
	if mode, ok, err := generalOptionValue(store, account, cinematicSkipOptionIndex); err != nil {
		return nil, err
	} else if ok && mode == cinematicSkipModeAll {
		return protocol.SeenCinematicsAllPayload(), nil
	}
	if store == nil || account <= 0 {
		return protocol.SeenCinematicsPayload(nil), nil
	}
	var mask []byte
	ok, err := store.Database().Get(seenCinematicsTable, strconv.FormatInt(account, 10), &mask)
	if err != nil {
		return nil, err
	}
	if !ok {
		return protocol.SeenCinematicsPayload(nil), nil
	}
	return protocol.SeenCinematicsPayload(mask), nil
}

func characterLevelFromState(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	var st struct {
		Level int `json:"level"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return 0
	}
	return st.Level
}

func recordStoryDigestLevel(store *storage.Store, characterID int64, level uint32) error {
	if store == nil || characterID <= 0 || level == 0 {
		return nil
	}
	key := strconv.FormatInt(characterID, 10)
	return store.Database().Mutate(func(tx *db100.Tx) error {
		var saved uint32
		ok, err := tx.Get(storyDigestLevelTable, key, &saved)
		if err != nil {
			return err
		}
		if ok && saved >= level {
			return nil
		}
		return tx.Put(storyDigestLevelTable, key, level)
	})
}

// storyDigestLevelOnEntry builds NOTI 1370 (0x55a) for StoryDigestManager+0x8c.
// DigestTable::FindGroupForLevel (0x1458cc5b0) matches [minLevel, maxLevel], so
// when Option 82 == 1 ("跳过所有过场动画") we push the character's exact current
// level (making the seen digest group identical to the current level's group so
// StoryDigest + MoviePlayer never open). Otherwise we push the persisted level
// recorded via CMD 1438 (ENUM_CMDPACKET_STORY_DIGEST_UPDATE).
func storyDigestLevelOnEntry(store *storage.Store, account, characterID int64, charLevel int) ([]byte, error) {
	var current uint32
	if charLevel > 0 {
		current = uint32(charLevel)
	}
	if mode, ok, err := generalOptionValue(store, account, cinematicSkipOptionIndex); err != nil {
		return nil, err
	} else if ok && mode == cinematicSkipModeAll && current > 0 {
		return protocol.StoryDigestLevelPayload(current), nil
	}
	if store == nil || characterID <= 0 {
		return protocol.StoryDigestLevelPayload(0), nil
	}
	var saved uint32
	ok, err := store.Database().Get(storyDigestLevelTable, strconv.FormatInt(characterID, 10), &saved)
	if err != nil {
		return nil, err
	}
	if !ok {
		return protocol.StoryDigestLevelPayload(0), nil
	}
	return protocol.StoryDigestLevelPayload(saved), nil
}
