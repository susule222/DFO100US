//go:build windows

package pluginhost

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

type sentPacket struct {
	sessionID uint64
	kind      byte
	packetID  uint16
	payload   []byte
	broadcast bool
}

type fakeBackend struct {
	mu           sync.Mutex
	logs         []string
	sent         []sentPacket
	sessions     map[uint64]SessionSnapshot
	kv           map[string][]byte
	grants       []string
	adminOps     []string
	syncNotified []int64
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		sessions: make(map[uint64]SessionSnapshot),
		kv:       make(map[string][]byte),
	}
}

func (b *fakeBackend) Log(level int32, pluginName, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logs = append(b.logs, fmt.Sprintf("[%s][%s] %s", pluginName, LogLevelName(level), message))
}

func (b *fakeBackend) SendPacket(sessionID uint64, kind byte, packetID uint16, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := append([]byte(nil), payload...)
	b.sent = append(b.sent, sentPacket{sessionID: sessionID, kind: kind, packetID: packetID, payload: cp})
	return nil
}

func (b *fakeBackend) BroadcastPacket(kind byte, packetID uint16, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := append([]byte(nil), payload...)
	b.sent = append(b.sent, sentPacket{kind: kind, packetID: packetID, payload: cp, broadcast: true})
	return nil
}

func (b *fakeBackend) GetSessionInfo(sessionID uint64) (SessionSnapshot, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sessions[sessionID]
	return s, ok
}

func (b *fakeBackend) ListSessions() []uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]uint64, 0, len(b.sessions))
	for id := range b.sessions {
		out = append(out, id)
	}
	return out
}

func (b *fakeBackend) GrantItem(accountID, characterID int64, templateID, amount uint32, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.grants = append(b.grants, fmt.Sprintf("%d:%d:%d:%d:%s", accountID, characterID, templateID, amount, reason))
	return nil
}

func (b *fakeBackend) ExecAdminOpJSON(opJSON []byte) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.adminOps = append(b.adminOps, string(opJSON))
	return json.Marshal(map[string]any{"ok": true, "echo_len": len(opJSON)})
}

func (b *fakeBackend) GetCharacterJSON(accountID, characterID int64) ([]byte, error) {
	return json.Marshal(map[string]any{"account_id": accountID, "character_id": characterID, "level": 100})
}

func (b *fakeBackend) KVGet(namespace, key string) ([]byte, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.kv[namespace+"/"+key]
	return v, ok, nil
}

func (b *fakeBackend) KVPut(namespace, key string, valueJSON []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.kv[namespace+"/"+key] = append([]byte(nil), valueJSON...)
	return nil
}

func (b *fakeBackend) KVDelete(namespace, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.kv, namespace+"/"+key)
	return nil
}

func (b *fakeBackend) NotifyCharacterSync(characterID int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.syncNotified = append(b.syncNotified, characterID)
	return nil
}

func TestCABIStructSizes(t *testing.T) {
	if got := unsafe.Sizeof(cServerPluginInfo{}); got != 424 {
		t.Fatalf("cServerPluginInfo size = %d, want 424", got)
	}
	if got := unsafe.Sizeof(cSessionInfo{}); got != 248 {
		t.Fatalf("cSessionInfo size = %d, want 248", got)
	}
	if got := unsafe.Sizeof(cPacketView{}); got != 24 {
		t.Fatalf("cPacketView size = %d, want 24", got)
	}
	if got := unsafe.Sizeof(cServerHostAPI{}); got != 120 {
		t.Fatalf("cServerHostAPI size = %d, want 120", got)
	}
}

func ensureExampleServerDLL(t *testing.T) string {
	t.Helper()
	dllPath, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk", "plugin", "examples", "build", "server_hello_plugin.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dllPath); err == nil {
		return dllPath
	}
	batDir, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk", "plugin", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd", "/c", "build_examples.bat")
	cmd.Dir = batDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build_examples.bat failed: %v\n%s", err, string(out))
	}
	if _, err := os.Stat(dllPath); err != nil {
		t.Fatalf("expected built DLL missing: %s", dllPath)
	}
	return dllPath
}

func TestRealExternalServerPluginDLL(t *testing.T) {
	dllSrc := ensureExampleServerDLL(t)
	tmpPlugins := t.TempDir()
	raw, err := os.ReadFile(dllSrc)
	if err != nil {
		t.Fatal(err)
	}
	// Active DLL
	if err := os.WriteFile(filepath.Join(tmpPlugins, "01_hello.dll"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Disabled DLL starting with '_' must be ignored
	if err := os.WriteFile(filepath.Join(tmpPlugins, "_disabled.dll"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	backend := newFakeBackend()
	host, err := New(tmpPlugins, t.TempDir(), backend)
	if err != nil {
		t.Fatalf("New host failed: %v", err)
	}
	defer host.Close()

	loaded, errs := host.LoadDir()
	if len(errs) != 0 {
		t.Fatalf("unexpected load errors: %v", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded = %d, want 1", len(loaded))
	}
	if loaded[0].Name != "server_hello_plugin" || loaded[0].Version != "1.0.0" {
		t.Fatalf("unexpected meta: %+v", loaded[0])
	}
	if !host.HasClientPacketHooks() || !host.HasServerPacketHooks() {
		t.Fatalf("expected packet hooks to be registered")
	}

	sess := SessionSnapshot{
		SessionID:    42,
		ServerID:     1,
		ChannelID:    11,
		AccountID:    1001,
		CharacterID:  77,
		WireID:       5,
		TownID:       3,
		AreaID:       1,
		PosX:         320,
		PosY:         240,
		Level:        100,
		Bootstrapped: true,
		PeerAddr:     "127.0.0.1:54321",
		CharName:     "Asura100",
		ChannelName:  "Cain-1",
	}
	backend.sessions[sess.SessionID] = sess

	host.OnSessionOpen(sess)
	host.OnCharacterEnter(sess)
	if val, ok := backend.kv["hello/last_enter"]; !ok || !strings.Contains(string(val), `"char_id":77`) {
		t.Fatalf("expected last_enter KV from on_character_enter, got ok=%v val=%q", ok, string(val))
	}

	// Custom CMD 9001 should be intercepted, echoed, and persisted to KV.
	payload := []byte("hello-us100-dll")
	verdict := host.OnClientPacket(sess, PacketView{PacketID: 9001, Kind: 1, Verified: true, Payload: payload})
	if verdict != ActionHandled {
		t.Fatalf("CMD 9001 verdict = %d, want ActionHandled", verdict)
	}
	if len(backend.sent) != 1 || backend.sent[0].packetID != 9001 || string(backend.sent[0].payload) != "hello-us100-dll" {
		t.Fatalf("unexpected echo packet: %+v", backend.sent)
	}
	if val, ok := backend.kv["hello/last_cmd_9001"]; !ok || !strings.Contains(string(val), `"sid":42`) {
		t.Fatalf("expected last_cmd_9001 KV with sid=42, got ok=%v val=%q", ok, string(val))
	}

	// Unrelated packet should continue untouched.
	if v := host.OnClientPacket(sess, PacketView{PacketID: 35, Kind: 1, Verified: true}); v != ActionContinue {
		t.Fatalf("CMD 35 verdict = %d, want ActionContinue", v)
	}

	// Custom CMD 9003 exercises exec_admin_op_json callback.
	opJSON := []byte(`{"action":"currency","character_id":77,"gold_mode":"add","gold":50000,"reason":"test"}`)
	if v := host.OnClientPacket(sess, PacketView{PacketID: 9003, Kind: 1, Verified: true, Payload: opJSON}); v != ActionHandled {
		t.Fatalf("CMD 9003 verdict = %d, want ActionHandled", v)
	}
	if len(backend.adminOps) != 1 || len(backend.sent) != 2 || !strings.Contains(string(backend.sent[1].payload), `"ok":true`) {
		t.Fatalf("unexpected admin op result: ops=%v sent=%+v", backend.adminOps, backend.sent)
	}

	// Custom CMD 9004 exercises grant_item callback.
	var grantPayload [8]byte
	binary.LittleEndian.PutUint32(grantPayload[0:4], 2600001)
	binary.LittleEndian.PutUint32(grantPayload[4:8], 5)
	if v := host.OnClientPacket(sess, PacketView{PacketID: 9004, Kind: 1, Verified: true, Payload: grantPayload[:]}); v != ActionHandled {
		t.Fatalf("CMD 9004 verdict = %d, want ActionHandled", v)
	}
	if len(backend.grants) != 1 || backend.grants[0] != "1001:77:2600001:5:plugin_9004" {
		t.Fatalf("unexpected grants: %v", backend.grants)
	}

	// Outbound NOTI 9002 should be blocked by on_server_packet.
	if v := host.OnServerPacket(sess, PacketView{PacketID: 9002, Kind: 0, Verified: true}); v != ActionBlock {
		t.Fatalf("NOTI 9002 verdict = %d, want ActionBlock", v)
	}
	if v := host.OnServerPacket(sess, PacketView{PacketID: 14, Kind: 0, Verified: true}); v != ActionContinue {
		t.Fatalf("NOTI 14 verdict = %d, want ActionContinue", v)
	}

	// Tick hook should fire and write KV.
	host.OnTick(1764316800000)
	if val, ok := backend.kv["hello/tick_seen"]; !ok || !strings.Contains(string(val), "1764316800000") {
		t.Fatalf("expected tick_seen KV, got ok=%v val=%q", ok, string(val))
	}

	host.OnSessionClose(sess)
	host.Close()

	foundShutdown := false
	for _, line := range backend.logs {
		if strings.Contains(line, "shutting down") {
			foundShutdown = true
		}
	}
	if !foundShutdown {
		t.Fatalf("expected shutdown log from plugin, logs=%v", backend.logs)
	}
}
