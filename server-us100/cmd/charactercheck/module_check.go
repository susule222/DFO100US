package main

import (
	"context"
	"dfolan/internal/catalog"
	"dfolan/internal/character"
	"dfolan/internal/inventory"
	"dfolan/internal/quest"
	"dfolan/internal/storage"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Called only after the temporary schema/search_path isolation gate.
func moduleCheck(ctx context.Context, s, reopened *storage.Store, role storage.Character, other int64, prof catalog.Characters) error {
	if e := s.MigrateVault(ctx); e != nil {
		return e
	}
	vr, e := inventory.LoadVaultRules("configs/vault.generated.json")
	if e != nil {
		return e
	}
	v := inventory.VaultService{Store: s, Rules: vr}
	if _, e = v.Bootstrap(ctx, role); e != nil {
		return e
	}
	if _, e = s.LoadVault(ctx, other, role.ID, vr.InitialSlots, vr.SourceSHA256); e == nil {
		return fmt.Errorf("foreign vault read allowed")
	}
	if e = rowUpdate(s, "character_vaults", idKey(role.ID), func(row map[string]any) error {
		row["items"] = json.RawMessage(`[{"retained_test_item":1}]`)
		return nil
	}); e != nil {
		return e
	}
	if _, e = v.Bootstrap(ctx, role); e == nil {
		return fmt.Errorf("unimplemented nonempty vault silently cleared")
	}
	vault, e := reopened.LoadVault(ctx, role.AccountID, role.ID, vr.InitialSlots, vr.SourceSHA256)
	if e != nil || string(vault.Items) == "[]" {
		return fmt.Errorf("vault content did not survive reopen: %v", e)
	}
	if e = s.MigrateFatigue(ctx); e != nil {
		return e
	}
	fp, e := s.LoadFatigue(ctx, role.AccountID, role.ID, "2026-09-10", 156)
	if e != nil || fp.Used != 0 || fp.Limit != 156 {
		return fmt.Errorf("fatigue initialization: %v", e)
	}
	if _, e = s.LoadFatigue(ctx, other, role.ID, "2026-09-11", 156); e == nil {
		return fmt.Errorf("foreign fatigue reset allowed")
	}
	if e = rowUpdate(s, "character_fatigue", idKey(role.ID), func(row map[string]any) error {
		row["used"] = 12
		return nil
	}); e != nil {
		return e
	}
	for _, day := range []string{"2026-09-10", "2026-09-09"} {
		fp, e = reopened.LoadFatigue(ctx, role.AccountID, role.ID, day, 200)
		if e != nil || fp.Used != 12 || fp.Limit != 156 {
			return fmt.Errorf("reconnect/backwards clock reset fatigue: %v", e)
		}
	}
	cs := character.Service{Store: reopened, Catalog: prof, Rules: character.Rules{MaxCharacters: 8, InitialLevel: 1}}
	fs := character.FatigueService{Store: reopened, Rules: character.FatigueRules{DailyLimit: 156}, Location: time.UTC}
	roster, e := cs.ListWithFatigue(ctx, role.AccountID, &fs, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	if e != nil || len(roster) < 18 || binary.LittleEndian.Uint16(roster[len(roster)-18:]) != 144 {
		return fmt.Errorf("roster failed to display persisted remaining fatigue: %v", e)
	}
	fp, e = s.LoadFatigue(ctx, role.AccountID, role.ID, "2026-09-11", 200)
	if e != nil || fp.Used != 0 || fp.Limit != 200 {
		return fmt.Errorf("daily fatigue rollover: %v", e)
	}
	qc, e := catalog.LoadQuests("configs/quests.generated.json")
	if e != nil {
		return e
	}
	qs := quest.Service{Store: s, Catalog: qc, Professions: prof}
	q, e := qs.Accept(ctx, role, 3145)
	if e != nil || q.Progress != 1 {
		return fmt.Errorf("quest initial pending state: %v", e)
	}
	active, e := qs.Active(ctx, role)
	if e != nil || len(active) != 1 || active[0].Progress != 1 {
		return fmt.Errorf("quest relog restore: %v", e)
	}
	if e = qs.Submit(ctx, role, 3145); !errors.Is(e, quest.ErrObjectiveIncomplete) {
		return fmt.Errorf("uncleared map submitted: %v", e)
	}
	before, e := s.Quests(ctx, role.AccountID, role.ID)
	if e != nil || before[0] != q {
		return fmt.Errorf("rejected submit mutated quest: %v", e)
	}
	// Reproduce the old bug only inside this disposable test database.
	zero, legacyModel := 0, "legacy-zero"
	if e = setQuest(s, role.ID, 3145, &zero, &legacyModel); e != nil {
		return e
	}
	legacy, e := s.Quests(ctx, role.AccountID, role.ID)
	if e != nil {
		return e
	}
	if e = s.RepairLegacyQuest(ctx, other, role.ID, legacy[0], 1, quest.SingleClearMap); e == nil {
		return fmt.Errorf("foreign quest repair allowed")
	}
	if e = s.RepairLegacyQuest(ctx, role.AccountID, role.ID, legacy[0], 1, quest.SingleClearMap); e != nil {
		return e
	}
	if e = s.RepairLegacyQuest(ctx, role.AccountID, role.ID, legacy[0], 1, quest.SingleClearMap); e == nil {
		return fmt.Errorf("stale quest repair allowed")
	}
	if repairs := rowCount(s, "character_quest_repairs", idKey(role.ID)+"/"); repairs != 1 {
		return fmt.Errorf("repair audit missing: %d", repairs)
	}
	fmt.Println("MODULE_CHECK_PASS vault_preserved=true fatigue_reconnect_rollover=true quest_pending_restore=true quest_repair_audited=true foreign_access_refused=true")
	return nil
}
