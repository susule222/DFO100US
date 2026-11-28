package main

import (
	"context"
	"dfolan/internal/admin"
	"dfolan/internal/db100"
	"dfolan/internal/pluginhost"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type wirePluginSession struct {
	id       uint64
	sendRaw  func(kind byte, id uint16, payload []byte) error
	snapshot func() pluginhost.SessionSnapshot
}

type wirePluginBackend struct {
	mu             sync.RWMutex
	nextID         atomic.Uint64
	sessions       map[uint64]*wirePluginSession
	store          *storage.Store
	adminSvc       *admin.Service
	defaultAccount int64
	gm             *gmSessions
	event          func(map[string]any)
}

func newWirePluginBackend(store *storage.Store, adminSvc *admin.Service, defaultAccount int64, gm *gmSessions, event func(map[string]any)) *wirePluginBackend {
	return &wirePluginBackend{
		sessions:       make(map[uint64]*wirePluginSession),
		store:          store,
		adminSvc:       adminSvc,
		defaultAccount: defaultAccount,
		gm:             gm,
		event:          event,
	}
}

func (b *wirePluginBackend) register(sendRaw func(byte, uint16, []byte) error, snapshot func() pluginhost.SessionSnapshot) uint64 {
	id := b.nextID.Add(1)
	b.mu.Lock()
	b.sessions[id] = &wirePluginSession{id: id, sendRaw: sendRaw, snapshot: snapshot}
	b.mu.Unlock()
	return id
}

func (b *wirePluginBackend) unregister(id uint64) {
	b.mu.Lock()
	delete(b.sessions, id)
	b.mu.Unlock()
}

func (b *wirePluginBackend) Log(level int32, pluginName, message string) {
	if pluginName == "" {
		pluginName = "plugin"
	}
	lvl := pluginhost.LogLevelName(level)
	log.Printf("[plugin:%s][%s] %s", pluginName, lvl, message)
	if b.event != nil {
		b.event(map[string]any{
			"kind":    "plugin_log",
			"level":   lvl,
			"plugin":  pluginName,
			"message": message,
		})
	}
}

func (b *wirePluginBackend) SendPacket(sessionID uint64, kind byte, packetID uint16, payload []byte) error {
	b.mu.RLock()
	sess := b.sessions[sessionID]
	b.mu.RUnlock()
	if sess == nil || sess.sendRaw == nil {
		return fmt.Errorf("session %d not found", sessionID)
	}
	return sess.sendRaw(kind, packetID, payload)
}

func (b *wirePluginBackend) BroadcastPacket(kind byte, packetID uint16, payload []byte) error {
	b.mu.RLock()
	list := make([]*wirePluginSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		list = append(list, s)
	}
	b.mu.RUnlock()
	var firstErr error
	for _, s := range list {
		if s.sendRaw == nil {
			continue
		}
		if err := s.sendRaw(kind, packetID, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (b *wirePluginBackend) GetSessionInfo(sessionID uint64) (pluginhost.SessionSnapshot, bool) {
	b.mu.RLock()
	sess := b.sessions[sessionID]
	b.mu.RUnlock()
	if sess == nil || sess.snapshot == nil {
		return pluginhost.SessionSnapshot{}, false
	}
	snap := sess.snapshot()
	snap.SessionID = sessionID
	return snap, true
}

func (b *wirePluginBackend) ListSessions() []uint64 {
	b.mu.RLock()
	ids := make([]uint64, 0, len(b.sessions))
	for id := range b.sessions {
		ids = append(ids, id)
	}
	b.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (b *wirePluginBackend) GrantItem(accountID, characterID int64, templateID, amount uint32, reason string) error {
	if b.adminSvc == nil {
		return fmt.Errorf("admin service unavailable")
	}
	if accountID <= 0 {
		accountID = b.defaultAccount
	}
	if characterID <= 0 || templateID == 0 || amount == 0 {
		return fmt.Errorf("invalid grant parameters")
	}
	if strings.TrimSpace(reason) == "" {
		reason = "plugin grant"
	}
	grant := storage.Grant{
		ID:        fmt.Sprintf("plugin-grant-%d-%d-%d", characterID, templateID, time.Now().UnixNano()),
		AccountID: accountID,
		Character: characterID,
		Operator:  "US100 plugin",
		Reason:    reason,
		Items:     []storage.GrantItem{{Template: templateID, Amount: amount}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := b.adminSvc.Apply(ctx, grant); err != nil {
		return err
	}
	if b.gm != nil {
		b.gm.committed(characterID)
	}
	return nil
}

func (b *wirePluginBackend) ExecAdminOpJSON(opJSON []byte) ([]byte, error) {
	if b.adminSvc == nil {
		return nil, fmt.Errorf("admin service unavailable")
	}
	var op admin.CharacterOp
	if err := json.Unmarshal(opJSON, &op); err != nil {
		return nil, err
	}
	if op.ID == "" {
		op.ID = fmt.Sprintf("plugin-op-%d", time.Now().UnixNano())
	}
	if op.AccountID <= 0 {
		op.AccountID = b.defaultAccount
	}
	if op.Operator == "" {
		op.Operator = "US100 plugin"
	}
	if strings.TrimSpace(op.Reason) == "" {
		op.Reason = "plugin op"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, _, err := b.adminSvc.ApplyOp(ctx, op)
	if err != nil {
		return nil, err
	}
	if b.gm != nil && op.CharacterID > 0 {
		b.gm.committed(op.CharacterID)
	}
	return json.Marshal(receipt)
}

func (b *wirePluginBackend) GetCharacterJSON(accountID, characterID int64) ([]byte, error) {
	if b.store == nil {
		return nil, fmt.Errorf("storage unavailable")
	}
	if accountID <= 0 {
		accountID = b.defaultAccount
	}
	role, err := b.store.LiveCharacter(accountID, characterID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(role)
}

func (b *wirePluginBackend) KVGet(namespace, key string) ([]byte, bool, error) {
	if b.store == nil || b.store.Database() == nil {
		return nil, false, fmt.Errorf("storage unavailable")
	}
	table, ok := pluginhost.SanitizeNamespace(namespace)
	if !ok {
		return nil, false, fmt.Errorf("invalid plugin namespace %q", namespace)
	}
	var raw json.RawMessage
	found, err := b.store.Database().Get(table, key, &raw)
	if err != nil || !found {
		return nil, found, err
	}
	return raw, true, nil
}

func (b *wirePluginBackend) KVPut(namespace, key string, valueJSON []byte) error {
	if b.store == nil || b.store.Database() == nil {
		return fmt.Errorf("storage unavailable")
	}
	table, ok := pluginhost.SanitizeNamespace(namespace)
	if !ok {
		return fmt.Errorf("invalid plugin namespace %q", namespace)
	}
	trimmed := strings.TrimSpace(string(valueJSON))
	var toStore any
	if json.Valid([]byte(trimmed)) {
		toStore = json.RawMessage(trimmed)
	} else {
		toStore = string(valueJSON)
	}
	return b.store.Database().Mutate(func(tx *db100.Tx) error {
		return tx.Put(table, key, toStore)
	})
}

func (b *wirePluginBackend) KVDelete(namespace, key string) error {
	if b.store == nil || b.store.Database() == nil {
		return fmt.Errorf("storage unavailable")
	}
	table, ok := pluginhost.SanitizeNamespace(namespace)
	if !ok {
		return fmt.Errorf("invalid plugin namespace %q", namespace)
	}
	return b.store.Database().Mutate(func(tx *db100.Tx) error {
		tx.Delete(table, key)
		return nil
	})
}

func (b *wirePluginBackend) NotifyCharacterSync(characterID int64) error {
	if b.gm == nil {
		return fmt.Errorf("gm session notifier unavailable")
	}
	if characterID <= 0 {
		return fmt.Errorf("invalid character_id")
	}
	b.gm.committed(characterID)
	return nil
}
