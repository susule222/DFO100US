package pluginhost

import (
	"fmt"
	"strings"
)

const (
	ABIVersion uint32 = 1
)

// Unified status codes matching sdk/plugin/us100_plugin_common.h.
const (
	StatusOK             int32 = 0
	ErrInvalidArg        int32 = -1
	ErrNotFound          int32 = -2
	ErrBufferTooSmall    int32 = -3
	ErrNotReady          int32 = -4
	ErrFailed            int32 = -5
	ErrUnsupported       int32 = -6
	ErrLimitReached      int32 = -7
	ErrMismatch          int32 = -8
)

// Log severity levels matching sdk/plugin/us100_plugin_common.h.
const (
	LogDebug int32 = 0
	LogInfo  int32 = 1
	LogWarn  int32 = 2
	LogError int32 = 3
)

// Packet hook verdicts matching sdk/plugin/us100_plugin_common.h.
const (
	ActionContinue int32 = 0
	ActionHandled  int32 = 1
	ActionBlock    int32 = 2
)

// LogLevelName formats an integer log level for structured events and logs.
func LogLevelName(level int32) string {
	switch level {
	case LogDebug:
		return "debug"
	case LogInfo:
		return "info"
	case LogWarn:
		return "warn"
	case LogError:
		return "error"
	default:
		return fmt.Sprintf("level_%d", level)
	}
}

// PluginMeta describes a loaded external server DLL plugin.
type PluginMeta struct {
	File        string `json:"file"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Hooks       []string `json:"hooks"`
}

// SessionSnapshot captures the current state of an active game connection.
type SessionSnapshot struct {
	SessionID    uint64
	ServerID     uint32
	ChannelID    uint32
	AccountID    int64
	CharacterID  int64
	WireID       uint16
	TownID       uint16
	AreaID       uint16
	PosX         uint16
	PosY         uint16
	Level        byte
	Bootstrapped bool
	InDungeon    bool
	PeerAddr     string
	CharName     string
	ChannelName  string
}

// PacketView describes a decrypted packet frame passed to plugins.
type PacketView struct {
	PacketID uint16
	Kind     byte // 0 = NOTI, 1 = CMD/ACK
	Verified bool
	Payload  []byte
	RawLen   uint32
}

// Backend bridges host API calls from loaded DLL plugins into wireprobe services.
type Backend interface {
	Log(level int32, pluginName, message string)
	SendPacket(sessionID uint64, kind byte, packetID uint16, payload []byte) error
	BroadcastPacket(kind byte, packetID uint16, payload []byte) error
	GetSessionInfo(sessionID uint64) (SessionSnapshot, bool)
	ListSessions() []uint64
	GrantItem(accountID, characterID int64, templateID, amount uint32, reason string) error
	ExecAdminOpJSON(opJSON []byte) ([]byte, error)
	GetCharacterJSON(accountID, characterID int64) ([]byte, error)
	KVGet(namespace, key string) ([]byte, bool, error)
	KVPut(namespace, key string, valueJSON []byte) error
	KVDelete(namespace, key string) error
	NotifyCharacterSync(characterID int64) error
}

// SanitizeNamespace validates a plugin KV namespace to prevent table collisions.
func SanitizeNamespace(ns string) (string, bool) {
	ns = strings.TrimSpace(ns)
	if ns == "" || len(ns) > 48 {
		return "", false
	}
	for i := 0; i < len(ns); i++ {
		c := ns[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return "", false
		}
	}
	return "plugin_" + ns, true
}
