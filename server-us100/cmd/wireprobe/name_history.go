package main

import (
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"math"
	"time"
	"unicode/utf8"
)

type storedNameHistoryRow struct {
	Name      string `json:"name"`
	Server    byte   `json:"server"`
	Timestamp int64  `json:"timestamp"`
}

func truncateHistoryName(name string) string {
	// Native 0x144e6eec0 rejects lengths >= 0x1f (31 bytes).
	const maxBytes = 30
	if len(name) <= maxBytes {
		return name
	}
	n := 0
	for i, r := range name {
		w := utf8.RuneLen(r)
		if w < 0 || i+w > maxBytes {
			break
		}
		n = i + w
	}
	return name[:n]
}

func clampTime32(ts int64, fallback time.Time) uint32 {
	if ts <= 0 || ts > math.MaxInt32 {
		ts = fallback.Unix()
	}
	if ts <= 0 || ts > math.MaxInt32 {
		return 1
	}
	return uint32(ts)
}

func nameHistoryPayload(role storage.Character, serverID byte, now time.Time) ([]byte, error) {
	if serverID == 0 {
		serverID = 1 // 0x143a76fa3 -> Cain (first server name string)
	}
	var state struct {
		NameHistory []storedNameHistoryRow `json:"name_history"`
	}
	if len(role.State) > 0 {
		_ = json.Unmarshal(role.State, &state)
	}
	entries := make([]protocol.NameHistoryEntry, 0, len(state.NameHistory)+1)
	for _, row := range state.NameHistory {
		name := truncateHistoryName(row.Name)
		if name == "" {
			continue
		}
		srv := row.Server
		if srv == 0 {
			srv = serverID
		}
		entries = append(entries, protocol.NameHistoryEntry{
			Name:        name,
			ServerGroup: srv,
			Timestamp:   clampTime32(row.Timestamp, now),
		})
		if len(entries) >= 255 {
			break
		}
	}
	if len(entries) == 0 {
		if name := truncateHistoryName(role.Name); name != "" {
			created := role.CreatedAt.Unix()
			if role.CreatedAt.IsZero() {
				created = now.Unix()
			}
			entries = append(entries, protocol.NameHistoryEntry{
				Name:        name,
				ServerGroup: serverID,
				Timestamp:   clampTime32(created, now),
			})
		}
	}
	return protocol.UserInfoNameHistory(role.WireID, entries)
}
