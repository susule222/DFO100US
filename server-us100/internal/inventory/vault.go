package inventory

import (
	"context"
	"dfolan/internal/game/protocol"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"os"
)

type VaultRules struct {
	SourceSHA256  string   `json:"source_sha256"`
	InitialSlots  uint16   `json:"initial_slots"`
	VerifiedSlots []uint16 `json:"verified_slots"`
}

func LoadVaultRules(path string) (VaultRules, error) {
	var r VaultRules
	b, e := os.ReadFile(path)
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if len(r.SourceSHA256) != 64 || !r.allows(r.InitialSlots) {
		return r, fmt.Errorf("invalid source vault configuration")
	}
	return r, nil
}
func (r VaultRules) allows(n uint16) bool {
	for _, v := range r.VerifiedSlots {
		if n != 0 && n == v {
			return true
		}
	}
	return false
}

// PersonalVaultTiers is the 17-tier native US100 Personal Safe I / Safe II
// capacity table verified at DFO_unpacked.exe 0x140057760..0x140057e8a (8..264).
var PersonalVaultTiers = []uint16{
	8, 24, 40, 56, 72, 88, 104, 120, 136, 152, 168, 184, 200, 216, 232, 248, 264,
}

const (
	MaxPersonalVaultSlots uint16 = 264
	MaxAccountVaultSlots  uint16 = 240
)

// NextPersonalVaultSlots computes the next valid Personal Safe capacity from
// current capacity cur and optional targetSlots (0 means advance by one tier,
// +16 slots; non-zero jumps to at least targetSlots snapped to a valid tier).
func NextPersonalVaultSlots(cur, target uint16) (uint16, error) {
	if cur >= MaxPersonalVaultSlots {
		return cur, fmt.Errorf("personal vault is already at maximum capacity (%d)", MaxPersonalVaultSlots)
	}
	if target > 0 {
		for _, capSlots := range PersonalVaultTiers {
			if capSlots >= target && capSlots > cur {
				return capSlots, nil
			}
		}
		// If target is already <= cur (e.g. player bought a lower-tier kit),
		// still advance by one tier so the purchase/kit takes effect cleanly.
	}
	for _, capSlots := range PersonalVaultTiers {
		if capSlots > cur {
			return capSlots, nil
		}
	}
	return cur, fmt.Errorf("personal vault is already at maximum capacity (%d)", MaxPersonalVaultSlots)
}

// NextAccountVaultSlots computes the next valid Account Vault capacity (8..240,
// step +8, verified from US100 PVF etc/accountcargo.etc).
func NextAccountVaultSlots(cur, target uint16) (uint16, error) {
	if cur >= MaxAccountVaultSlots {
		return cur, fmt.Errorf("account vault is already at maximum capacity (%d)", MaxAccountVaultSlots)
	}
	if target > cur {
		// Snap up to nearest multiple of 8, capped at 240.
		snapped := ((target + 7) / 8) * 8
		if snapped > MaxAccountVaultSlots {
			snapped = MaxAccountVaultSlots
		}
		return snapped, nil
	}
	next := cur + 8
	if next > MaxAccountVaultSlots {
		next = MaxAccountVaultSlots
	}
	return next, nil
}

type VaultService struct {
	Store *storage.Store
	Rules VaultRules
}

func (s *VaultService) Bootstrap(ctx context.Context, role storage.Character) ([]byte, error) {
	v1, _, _, err := s.BootstrapAll(ctx, role)
	return v1, err
}

// BootstrapAll builds NOTI 13 restore payloads for Personal Safe I (space 2),
// Personal Safe II (space 0x2d, omitted when 0), and Account Vault (space 0x0c,
// omitted when 0).
func (s *VaultService) BootstrapAll(ctx context.Context, role storage.Character) (vault1, vault2, accountVault []byte, err error) {
	v, e := s.Store.LoadVault(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, s.Rules.SourceSHA256)
	if e != nil {
		return nil, nil, nil, e
	}
	if v.ConfigVersion != s.Rules.SourceSHA256 || !s.Rules.allows(v.Slots) {
		return nil, nil, nil, fmt.Errorf("vault requires configuration migration")
	}
	var items []json.RawMessage
	if e = json.Unmarshal(v.Items, &items); e != nil {
		return nil, nil, nil, e
	}
	if len(items) != 0 {
		return nil, nil, nil, fmt.Errorf("nonempty vault item encoding is pending; stored items preserved")
	}
	vault1, err = protocol.EmptyPersonalVault(v.Slots)
	if err != nil {
		return nil, nil, nil, err
	}
	if v.Slots2 > 0 {
		vault2, err = protocol.EmptyPersonalVault2(v.Slots2)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if av, aerr := s.Store.LoadAccountVault(ctx, role.AccountID); aerr == nil && av.Slots > 0 {
		accountVault, err = protocol.EmptyAccountVault(av.Slots, av.Gold)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return vault1, vault2, accountVault, nil
}

// UpgradePersonal advances Personal Safe I (tab=0) or Personal Safe II (tab=1)
// to targetSlots (or next tier when targetSlots==0) and persists it in us100.db.
func (s *VaultService) UpgradePersonal(ctx context.Context, role storage.Character, tab byte, targetSlots uint16) (uint16, error) {
	if s == nil || s.Store == nil {
		return 0, fmt.Errorf("vault service unavailable")
	}
	v, err := s.Store.LoadVault(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, s.Rules.SourceSHA256)
	if err != nil {
		return 0, err
	}
	cur := v.Slots
	if tab == 1 {
		cur = v.Slots2
	}
	next, err := NextPersonalVaultSlots(cur, targetSlots)
	if err != nil {
		return cur, err
	}
	saved, err := s.Store.SavePersonalVaultSlots(ctx, role.AccountID, role.ID, s.Rules.InitialSlots, next, tab, s.Rules.SourceSHA256)
	if err != nil {
		return cur, err
	}
	if tab == 1 {
		return saved.Slots2, nil
	}
	return saved.Slots, nil
}

// UpgradeAccount advances the account's shared Account Vault (space 0x0c) to
// targetSlots (or next +8 tier when targetSlots==0) and persists it.
func (s *VaultService) UpgradeAccount(ctx context.Context, account int64, targetSlots uint16) (storage.AccountVaultState, error) {
	if s == nil || s.Store == nil {
		return storage.AccountVaultState{}, fmt.Errorf("vault service unavailable")
	}
	cur, err := s.Store.LoadAccountVault(ctx, account)
	if err != nil {
		return cur, err
	}
	next, err := NextAccountVaultSlots(cur.Slots, targetSlots)
	if err != nil {
		return cur, err
	}
	return s.Store.SaveAccountVaultSlots(ctx, account, next)
}
