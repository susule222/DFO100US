//go:build windows

package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Exact C ABI struct mirrors matching sdk/plugin/us100_server_plugin.h (windows/amd64).

type cServerPluginInfo struct {
	StructSize  uint32
	ABIVersion  uint32
	Name        [64]byte
	Version     [32]byte
	Author      [64]byte
	Description [256]byte
}

type cSessionInfo struct {
	StructSize   uint32
	ServerID     uint32
	SessionID    uint64
	AccountID    int64
	CharacterID  int64
	ChannelID    uint32
	WireID       uint16
	TownID       uint16
	AreaID       uint16
	PosX         uint16
	PosY         uint16
	Level        byte
	Bootstrapped byte
	InDungeon    byte
	Pad          [7]byte
	PeerAddr     [64]byte
	CharName     [64]byte
	ChannelName  [64]byte
}

type cPacketView struct {
	StructSize uint32
	PacketID   uint16
	Kind       byte
	Verified   byte
	Payload    uintptr
	PayloadLen uint32
	RawLen     uint32
}

type cServerHostAPI struct {
	StructSize          uint32
	ABIVersion          uint32
	PluginsDir          uintptr
	ConfigDir           uintptr
	Log                 uintptr
	SendPacket          uintptr
	BroadcastPacket     uintptr
	GetSessionInfo      uintptr
	ListSessions        uintptr
	GrantItem           uintptr
	ExecAdminOpJSON     uintptr
	GetCharacterJSON    uintptr
	KVGet               uintptr
	KVPut               uintptr
	KVDelete            uintptr
	NotifyCharacterSync uintptr
}

var (
	kernel32DLL      = syscall.NewLazyDLL("kernel32.dll")
	procVirtualAlloc = kernel32DLL.NewProc("VirtualAlloc")
	procVirtualFree  = kernel32DLL.NewProc("VirtualFree")
)

const (
	memCommit  = 0x1000
	memReserve = 0x2000
	memRelease = 0x8000
	pageRW     = 0x04
)

func allocUnmanaged(size uintptr) (uintptr, error) {
	if size == 0 {
		size = 16
	}
	addr, _, err := procVirtualAlloc.Call(0, size, memCommit|memReserve, pageRW)
	if addr == 0 {
		return 0, fmt.Errorf("VirtualAlloc(%d): %w", size, err)
	}
	return addr, nil
}

func freeUnmanaged(addr uintptr) {
	if addr != 0 {
		_, _, _ = procVirtualFree.Call(addr, 0, memRelease)
	}
}

func allocUnmanagedCString(s string) (uintptr, error) {
	b := append([]byte(s), 0)
	addr, err := allocUnmanaged(uintptr(len(b)))
	if err != nil {
		return 0, err
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(addr)), len(b))
	copy(dst, b)
	return addr, nil
}

func cStringToGo(ptr uintptr, maxLen int) string {
	if ptr == 0 || maxLen <= 0 {
		return ""
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), maxLen)
	n := 0
	for n < len(raw) && raw[n] != 0 {
		n++
	}
	if n == 0 {
		return ""
	}
	out := make([]byte, n)
	copy(out, raw[:n])
	return string(out)
}

func fixedBytesToGo(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func copyGoStringToFixed(dst []byte, s string) {
	if len(dst) == 0 {
		return
	}
	n := len(s)
	if n >= len(dst) {
		n = len(dst) - 1
	}
	copy(dst[:n], s[:n])
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
}

func rcPtr(code int32) uintptr {
	return uintptr(uint32(code))
}

func writeCStringBuffer(dst uintptr, capLen uint32, outLenPtr uintptr, data []byte) int32 {
	if outLenPtr != 0 {
		*(*uint32)(unsafe.Pointer(outLenPtr)) = uint32(len(data))
	}
	if dst == 0 || capLen == 0 || uint32(len(data))+1 > capLen {
		return ErrBufferTooSmall
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(dst)), int(capLen))
	copy(buf, data)
	buf[len(data)] = 0
	return StatusOK
}

func fillCSessionInfo(out *cSessionInfo, s SessionSnapshot) {
	*out = cSessionInfo{
		StructSize:  uint32(unsafe.Sizeof(cSessionInfo{})),
		ServerID:    s.ServerID,
		SessionID:   s.SessionID,
		AccountID:   s.AccountID,
		CharacterID: s.CharacterID,
		ChannelID:   s.ChannelID,
		WireID:      s.WireID,
		TownID:      s.TownID,
		AreaID:      s.AreaID,
		PosX:        s.PosX,
		PosY:        s.PosY,
		Level:       s.Level,
	}
	if s.Bootstrapped {
		out.Bootstrapped = 1
	}
	if s.InDungeon {
		out.InDungeon = 1
	}
	copyGoStringToFixed(out.PeerAddr[:], s.PeerAddr)
	copyGoStringToFixed(out.CharName[:], s.CharName)
	copyGoStringToFixed(out.ChannelName[:], s.ChannelName)
}

type loadedPlugin struct {
	meta             PluginMeta
	dll              *syscall.DLL
	fnShutdown       *syscall.Proc
	fnSessionOpen    *syscall.Proc
	fnSessionClose   *syscall.Proc
	fnCharacterEnter *syscall.Proc
	fnClientPacket   *syscall.Proc
	fnServerPacket   *syscall.Proc
	fnTick           *syscall.Proc
}

// Host manages the lifecycle and invocation of external Windows x64 server DLL plugins.
type Host struct {
	mu           sync.RWMutex
	backend      Backend
	pluginsDir   string
	configDir    string
	plugins      []*loadedPlugin
	closed       bool
	apiAddr      uintptr
	pluginsDirCS uintptr
	configDirCS  uintptr
	// Retain callback closures/refs for the lifetime of Host.
	callbacks []uintptr
	stopTick  chan struct{}
	tickDone  chan struct{}
}

// New creates a server DLL plugin host backed by the given Backend.
func New(pluginsDir, configDir string, backend Backend) (*Host, error) {
	if backend == nil {
		return nil, fmt.Errorf("pluginhost: nil backend")
	}
	absPlugins := pluginsDir
	if absPlugins != "" {
		if a, err := filepath.Abs(absPlugins); err == nil {
			absPlugins = a
		}
	}
	absConfig := configDir
	if absConfig != "" {
		if a, err := filepath.Abs(absConfig); err == nil {
			absConfig = a
		}
	}

	h := &Host{
		backend:    backend,
		pluginsDir: absPlugins,
		configDir:  absConfig,
	}

	var err error
	h.pluginsDirCS, err = allocUnmanagedCString(absPlugins)
	if err != nil {
		return nil, err
	}
	h.configDirCS, err = allocUnmanagedCString(absConfig)
	if err != nil {
		freeUnmanaged(h.pluginsDirCS)
		return nil, err
	}
	h.apiAddr, err = allocUnmanaged(unsafe.Sizeof(cServerHostAPI{}))
	if err != nil {
		freeUnmanaged(h.pluginsDirCS)
		freeUnmanaged(h.configDirCS)
		return nil, err
	}

	cbLog := syscall.NewCallback(func(level uintptr, namePtr uintptr, msgPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		h.backend.Log(int32(level), cStringToGo(namePtr, 128), cStringToGo(msgPtr, 4096))
		return 0
	})
	cbSendPacket := syscall.NewCallback(func(sessionID uintptr, kind uintptr, packetID uintptr, payloadPtr uintptr, payloadLen uintptr) uintptr {
		defer func() { _ = recover() }()
		if payloadLen > 4*1024*1024 {
			return rcPtr(ErrInvalidArg)
		}
		var payload []byte
		if payloadLen > 0 {
			if payloadPtr == 0 {
				return rcPtr(ErrInvalidArg)
			}
			payload = make([]byte, int(payloadLen))
			copy(payload, unsafe.Slice((*byte)(unsafe.Pointer(payloadPtr)), int(payloadLen)))
		}
		if err := h.backend.SendPacket(uint64(sessionID), byte(kind), uint16(packetID), payload); err != nil {
			return rcPtr(ErrFailed)
		}
		return rcPtr(StatusOK)
	})
	cbBroadcastPacket := syscall.NewCallback(func(kind uintptr, packetID uintptr, payloadPtr uintptr, payloadLen uintptr) uintptr {
		defer func() { _ = recover() }()
		if payloadLen > 4*1024*1024 {
			return rcPtr(ErrInvalidArg)
		}
		var payload []byte
		if payloadLen > 0 {
			if payloadPtr == 0 {
				return rcPtr(ErrInvalidArg)
			}
			payload = make([]byte, int(payloadLen))
			copy(payload, unsafe.Slice((*byte)(unsafe.Pointer(payloadPtr)), int(payloadLen)))
		}
		if err := h.backend.BroadcastPacket(byte(kind), uint16(packetID), payload); err != nil {
			return rcPtr(ErrFailed)
		}
		return rcPtr(StatusOK)
	})
	cbGetSessionInfo := syscall.NewCallback(func(sessionID uintptr, outPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		if outPtr == 0 {
			return rcPtr(ErrInvalidArg)
		}
		snap, ok := h.backend.GetSessionInfo(uint64(sessionID))
		if !ok {
			return rcPtr(ErrNotFound)
		}
		out := (*cSessionInfo)(unsafe.Pointer(outPtr))
		fillCSessionInfo(out, snap)
		return rcPtr(StatusOK)
	})
	cbListSessions := syscall.NewCallback(func(outIDsPtr uintptr, maxCount uintptr, outCountPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		ids := h.backend.ListSessions()
		if outCountPtr != 0 {
			*(*uint32)(unsafe.Pointer(outCountPtr)) = uint32(len(ids))
		}
		if len(ids) == 0 {
			return rcPtr(StatusOK)
		}
		if outIDsPtr == 0 || maxCount == 0 {
			return rcPtr(ErrBufferTooSmall)
		}
		dst := unsafe.Slice((*uint64)(unsafe.Pointer(outIDsPtr)), int(maxCount))
		n := copy(dst, ids)
		if n < len(ids) {
			return rcPtr(ErrBufferTooSmall)
		}
		return rcPtr(StatusOK)
	})
	cbGrantItem := syscall.NewCallback(func(accountID uintptr, characterID uintptr, templateID uintptr, amount uintptr, reasonPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		reason := cStringToGo(reasonPtr, 256)
		if err := h.backend.GrantItem(int64(accountID), int64(characterID), uint32(templateID), uint32(amount), reason); err != nil {
			h.backend.Log(LogWarn, "pluginhost", "grant_item failed: "+err.Error())
			return rcPtr(ErrFailed)
		}
		return rcPtr(StatusOK)
	})
	cbExecAdminOpJSON := syscall.NewCallback(func(opPtr uintptr, outBuf uintptr, outCap uintptr, outLenPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		if opPtr == 0 {
			return rcPtr(ErrInvalidArg)
		}
		opJSON := cStringToGo(opPtr, 64*1024)
		if opJSON == "" {
			return rcPtr(ErrInvalidArg)
		}
		res, err := h.backend.ExecAdminOpJSON([]byte(opJSON))
		if err != nil {
			h.backend.Log(LogWarn, "pluginhost", "exec_admin_op_json failed: "+err.Error())
			return rcPtr(ErrFailed)
		}
		return rcPtr(writeCStringBuffer(outBuf, uint32(outCap), outLenPtr, res))
	})
	cbGetCharacterJSON := syscall.NewCallback(func(accountID uintptr, characterID uintptr, outBuf uintptr, outCap uintptr, outLenPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		res, err := h.backend.GetCharacterJSON(int64(accountID), int64(characterID))
		if err != nil {
			return rcPtr(ErrNotFound)
		}
		return rcPtr(writeCStringBuffer(outBuf, uint32(outCap), outLenPtr, res))
	})
	cbKVGet := syscall.NewCallback(func(nsPtr uintptr, keyPtr uintptr, outBuf uintptr, outCap uintptr, outLenPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		ns := cStringToGo(nsPtr, 64)
		key := cStringToGo(keyPtr, 256)
		if ns == "" || key == "" {
			return rcPtr(ErrInvalidArg)
		}
		val, found, err := h.backend.KVGet(ns, key)
		if err != nil {
			return rcPtr(ErrFailed)
		}
		if !found {
			return rcPtr(ErrNotFound)
		}
		return rcPtr(writeCStringBuffer(outBuf, uint32(outCap), outLenPtr, val))
	})
	cbKVPut := syscall.NewCallback(func(nsPtr uintptr, keyPtr uintptr, valPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		ns := cStringToGo(nsPtr, 64)
		key := cStringToGo(keyPtr, 256)
		if ns == "" || key == "" || valPtr == 0 {
			return rcPtr(ErrInvalidArg)
		}
		val := cStringToGo(valPtr, 256*1024)
		if err := h.backend.KVPut(ns, key, []byte(val)); err != nil {
			return rcPtr(ErrFailed)
		}
		return rcPtr(StatusOK)
	})
	cbKVDelete := syscall.NewCallback(func(nsPtr uintptr, keyPtr uintptr) uintptr {
		defer func() { _ = recover() }()
		ns := cStringToGo(nsPtr, 64)
		key := cStringToGo(keyPtr, 256)
		if ns == "" || key == "" {
			return rcPtr(ErrInvalidArg)
		}
		if err := h.backend.KVDelete(ns, key); err != nil {
			return rcPtr(ErrFailed)
		}
		return rcPtr(StatusOK)
	})
	cbNotifyCharSync := syscall.NewCallback(func(characterID uintptr) uintptr {
		defer func() { _ = recover() }()
		if err := h.backend.NotifyCharacterSync(int64(characterID)); err != nil {
			return rcPtr(ErrFailed)
		}
		return rcPtr(StatusOK)
	})

	h.callbacks = []uintptr{
		cbLog, cbSendPacket, cbBroadcastPacket, cbGetSessionInfo, cbListSessions,
		cbGrantItem, cbExecAdminOpJSON, cbGetCharacterJSON,
		cbKVGet, cbKVPut, cbKVDelete, cbNotifyCharSync,
	}

	api := (*cServerHostAPI)(unsafe.Pointer(h.apiAddr))
	*api = cServerHostAPI{
		StructSize:          uint32(unsafe.Sizeof(cServerHostAPI{})),
		ABIVersion:          ABIVersion,
		PluginsDir:          h.pluginsDirCS,
		ConfigDir:           h.configDirCS,
		Log:                 cbLog,
		SendPacket:          cbSendPacket,
		BroadcastPacket:     cbBroadcastPacket,
		GetSessionInfo:      cbGetSessionInfo,
		ListSessions:        cbListSessions,
		GrantItem:           cbGrantItem,
		ExecAdminOpJSON:     cbExecAdminOpJSON,
		GetCharacterJSON:    cbGetCharacterJSON,
		KVGet:               cbKVGet,
		KVPut:               cbKVPut,
		KVDelete:            cbKVDelete,
		NotifyCharacterSync: cbNotifyCharSync,
	}
	return h, nil
}

// LoadDir scans pluginsDir for *.dll files (skipping names starting with '_')
// in lexical order and initializes each one.
func (h *Host) LoadDir() ([]PluginMeta, []error) {
	if h == nil || h.pluginsDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(h.pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "_") || !strings.HasSuffix(strings.ToLower(name), ".dll") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var loaded []PluginMeta
	var errs []error
	for _, name := range names {
		fullPath := filepath.Join(h.pluginsDir, name)
		meta, lerr := h.LoadPlugin(fullPath)
		if lerr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, lerr))
			h.backend.Log(LogError, "pluginhost", fmt.Sprintf("failed to load %s: %v", name, lerr))
			continue
		}
		loaded = append(loaded, meta)
	}
	return loaded, errs
}

// LoadPlugin loads and initializes a single external server DLL plugin.
func (h *Host) LoadPlugin(path string) (PluginMeta, error) {
	var zero PluginMeta
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return zero, fmt.Errorf("LoadDLL: %w", err)
	}
	initProc, err := dll.FindProc("us100_server_plugin_init")
	if err != nil {
		_ = dll.Release()
		return zero, fmt.Errorf("missing export us100_server_plugin_init: %w", err)
	}

	var info cServerPluginInfo
	info.StructSize = uint32(unsafe.Sizeof(info))
	info.ABIVersion = ABIVersion
	rc, _, _ := initProc.Call(h.apiAddr, uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(&info)
	if int32(rc) != StatusOK {
		_ = dll.Release()
		return zero, fmt.Errorf("us100_server_plugin_init returned %d", int32(rc))
	}

	p := &loadedPlugin{
		dll: dll,
		meta: PluginMeta{
			File:        filepath.Base(path),
			Name:        fixedBytesToGo(info.Name[:]),
			Version:     fixedBytesToGo(info.Version[:]),
			Author:      fixedBytesToGo(info.Author[:]),
			Description: fixedBytesToGo(info.Description[:]),
		},
	}
	if p.meta.Name == "" {
		p.meta.Name = strings.TrimSuffix(p.meta.File, filepath.Ext(p.meta.File))
	}

	bindOpt := func(sym, label string, target **syscall.Proc) {
		if proc, ferr := dll.FindProc(sym); ferr == nil {
			*target = proc
			p.meta.Hooks = append(p.meta.Hooks, label)
		}
	}
	bindOpt("us100_server_plugin_shutdown", "shutdown", &p.fnShutdown)
	bindOpt("us100_server_plugin_on_session_open", "session_open", &p.fnSessionOpen)
	bindOpt("us100_server_plugin_on_session_close", "session_close", &p.fnSessionClose)
	bindOpt("us100_server_plugin_on_character_enter", "character_enter", &p.fnCharacterEnter)
	bindOpt("us100_server_plugin_on_client_packet", "client_packet", &p.fnClientPacket)
	bindOpt("us100_server_plugin_on_server_packet", "server_packet", &p.fnServerPacket)
	bindOpt("us100_server_plugin_on_tick", "tick", &p.fnTick)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		if p.fnShutdown != nil {
			_, _, _ = p.fnShutdown.Call()
		}
		_ = dll.Release()
		return zero, fmt.Errorf("host already closed")
	}
	h.plugins = append(h.plugins, p)
	h.mu.Unlock()

	h.backend.Log(LogInfo, "pluginhost", fmt.Sprintf("loaded server plugin %s v%s (%s) hooks=%v",
		p.meta.Name, p.meta.Version, p.meta.File, p.meta.Hooks))
	return p.meta, nil
}

// Loaded returns metadata for all active plugins.
func (h *Host) Loaded() []PluginMeta {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PluginMeta, len(h.plugins))
	for i, p := range h.plugins {
		out[i] = p.meta
	}
	return out
}

// HasClientPacketHooks returns true if at least one loaded plugin listens to client packets.
func (h *Host) HasClientPacketHooks() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.plugins {
		if p.fnClientPacket != nil {
			return true
		}
	}
	return false
}

// HasServerPacketHooks returns true if at least one loaded plugin listens to server packets.
func (h *Host) HasServerPacketHooks() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.plugins {
		if p.fnServerPacket != nil {
			return true
		}
	}
	return false
}

func (h *Host) snapshotPlugins() []*loadedPlugin {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed || len(h.plugins) == 0 {
		return nil
	}
	list := make([]*loadedPlugin, len(h.plugins))
	copy(list, h.plugins)
	return list
}

// StartTickLoop starts a background ticker that invokes on_tick on all loaded plugins.
func (h *Host) StartTickLoop(interval time.Duration) {
	if h == nil || interval <= 0 {
		return
	}
	h.mu.Lock()
	if h.stopTick != nil || h.closed {
		h.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	h.stopTick = stop
	h.tickDone = done
	h.mu.Unlock()

	go func() {
		defer close(done)
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case now := <-tk.C:
				h.OnTick(uint64(now.UnixMilli()))
			case <-stop:
				return
			}
		}
	}()
}

// OnSessionOpen dispatches session open notifications to plugins.
func (h *Host) OnSessionOpen(s SessionSnapshot) {
	list := h.snapshotPlugins()
	if len(list) == 0 {
		return
	}
	var cs cSessionInfo
	fillCSessionInfo(&cs, s)
	for _, p := range list {
		if p.fnSessionOpen != nil {
			_, _, _ = p.fnSessionOpen.Call(uintptr(unsafe.Pointer(&cs)))
		}
	}
	runtime.KeepAlive(&cs)
}

// OnSessionClose dispatches session close notifications to plugins.
func (h *Host) OnSessionClose(s SessionSnapshot) {
	list := h.snapshotPlugins()
	if len(list) == 0 {
		return
	}
	var cs cSessionInfo
	fillCSessionInfo(&cs, s)
	for _, p := range list {
		if p.fnSessionClose != nil {
			_, _, _ = p.fnSessionClose.Call(uintptr(unsafe.Pointer(&cs)))
		}
	}
	runtime.KeepAlive(&cs)
}

// OnCharacterEnter dispatches character entry notifications to plugins.
func (h *Host) OnCharacterEnter(s SessionSnapshot) {
	list := h.snapshotPlugins()
	if len(list) == 0 {
		return
	}
	var cs cSessionInfo
	fillCSessionInfo(&cs, s)
	for _, p := range list {
		if p.fnCharacterEnter != nil {
			_, _, _ = p.fnCharacterEnter.Call(uintptr(unsafe.Pointer(&cs)))
		}
	}
	runtime.KeepAlive(&cs)
}

// OnClientPacket dispatches an inbound client packet to plugins in load order.
// Stops immediately if a plugin returns ActionHandled or ActionBlock.
func (h *Host) OnClientPacket(s SessionSnapshot, pkt PacketView) int32 {
	list := h.snapshotPlugins()
	if len(list) == 0 {
		return ActionContinue
	}
	var cs cSessionInfo
	fillCSessionInfo(&cs, s)
	var cp cPacketView
	cp.StructSize = uint32(unsafe.Sizeof(cp))
	cp.PacketID = pkt.PacketID
	cp.Kind = pkt.Kind
	if pkt.Verified {
		cp.Verified = 1
	}
	if len(pkt.Payload) > 0 {
		cp.Payload = uintptr(unsafe.Pointer(&pkt.Payload[0]))
		cp.PayloadLen = uint32(len(pkt.Payload))
	}
	cp.RawLen = pkt.RawLen

	verdict := ActionContinue
	for _, p := range list {
		if p.fnClientPacket == nil {
			continue
		}
		rc, _, _ := p.fnClientPacket.Call(uintptr(unsafe.Pointer(&cs)), uintptr(unsafe.Pointer(&cp)))
		if a := int32(rc); a == ActionHandled || a == ActionBlock {
			verdict = a
			break
		}
	}
	runtime.KeepAlive(&cs)
	runtime.KeepAlive(&cp)
	runtime.KeepAlive(pkt.Payload)
	return verdict
}

// OnServerPacket dispatches an outbound server packet to plugins before encryption/send.
// Returning ActionBlock suppresses sending the packet.
func (h *Host) OnServerPacket(s SessionSnapshot, pkt PacketView) int32 {
	list := h.snapshotPlugins()
	if len(list) == 0 {
		return ActionContinue
	}
	var cs cSessionInfo
	fillCSessionInfo(&cs, s)
	var cp cPacketView
	cp.StructSize = uint32(unsafe.Sizeof(cp))
	cp.PacketID = pkt.PacketID
	cp.Kind = pkt.Kind
	cp.Verified = 1
	if len(pkt.Payload) > 0 {
		cp.Payload = uintptr(unsafe.Pointer(&pkt.Payload[0]))
		cp.PayloadLen = uint32(len(pkt.Payload))
	}
	cp.RawLen = pkt.RawLen

	verdict := ActionContinue
	for _, p := range list {
		if p.fnServerPacket == nil {
			continue
		}
		rc, _, _ := p.fnServerPacket.Call(uintptr(unsafe.Pointer(&cs)), uintptr(unsafe.Pointer(&cp)))
		if a := int32(rc); a == ActionBlock {
			verdict = ActionBlock
			break
		}
	}
	runtime.KeepAlive(&cs)
	runtime.KeepAlive(&cp)
	runtime.KeepAlive(pkt.Payload)
	return verdict
}

// OnTick dispatches a periodic server tick (unix ms) to plugins.
func (h *Host) OnTick(nowUnixMS uint64) {
	list := h.snapshotPlugins()
	for _, p := range list {
		if p.fnTick != nil {
			_, _, _ = p.fnTick.Call(uintptr(nowUnixMS))
		}
	}
}

// Close stops the tick loop, calls shutdown on each plugin in reverse load order,
// unloads all DLLs, and frees unmanaged host ABI memory.
func (h *Host) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	stop := h.stopTick
	done := h.tickDone
	plugins := h.plugins
	h.plugins = nil
	h.mu.Unlock()

	if stop != nil {
		close(stop)
		<-done
	}
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		if p.fnShutdown != nil {
			_, _, _ = p.fnShutdown.Call()
		}
		if p.dll != nil {
			_ = p.dll.Release()
		}
	}
	freeUnmanaged(h.apiAddr)
	freeUnmanaged(h.pluginsDirCS)
	freeUnmanaged(h.configDirCS)
	h.apiAddr = 0
	h.pluginsDirCS = 0
	h.configDirCS = 0
}
