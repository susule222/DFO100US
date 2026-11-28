//go:build !windows

package pluginhost

import (
	"fmt"
	"time"
)

// Host is a no-op stub on non-Windows platforms.
type Host struct{}

func New(pluginsDir, configDir string, backend Backend) (*Host, error) {
	return &Host{}, nil
}

func (h *Host) LoadDir() ([]PluginMeta, []error) { return nil, nil }
func (h *Host) LoadPlugin(path string) (PluginMeta, error) {
	return PluginMeta{}, fmt.Errorf("external DLL plugins are only supported on Windows")
}
func (h *Host) Loaded() []PluginMeta                          { return nil }
func (h *Host) HasClientPacketHooks() bool                    { return false }
func (h *Host) HasServerPacketHooks() bool                    { return false }
func (h *Host) StartTickLoop(interval time.Duration)          {}
func (h *Host) OnSessionOpen(s SessionSnapshot)               {}
func (h *Host) OnSessionClose(s SessionSnapshot)              {}
func (h *Host) OnCharacterEnter(s SessionSnapshot)            {}
func (h *Host) OnClientPacket(s SessionSnapshot, p PacketView) int32 { return ActionContinue }
func (h *Host) OnServerPacket(s SessionSnapshot, p PacketView) int32 { return ActionContinue }
func (h *Host) OnTick(nowUnixMS uint64)                       {}
func (h *Host) Close()                                        {}
