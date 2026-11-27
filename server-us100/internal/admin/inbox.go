package admin

import (
	"context"
	"dfolan/internal/storage"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var jobName = regexp.MustCompile(`^[a-f0-9]{32}\.json$`)

// Drain keeps the offline caller compatible; both paths use the gateway's own database.
func Drain(dir string, svc *Service, account int64) error {
	return DrainAndNotify(dir, svc, account, nil)
}

// A retry after commit replays the same receipt and may request another absolute
// bag refresh, but it never adds a second copy of the item.
func DrainAndNotify(dir string, svc *Service, account int64, committed func(int64)) error {
	pending := filepath.Join(dir, "pending")
	out := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(out, 0700); err != nil {
		return err
	}
	jobs, err := os.ReadDir(pending)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, job := range jobs {
		name := job.Name()
		if !job.Type().IsRegular() || !jobName.MatchString(name) {
			continue
		}
		path := filepath.Join(pending, name)
		input, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var probe struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(input, &probe)
		result := map[string]any{"id": strings.TrimSuffix(name, ".json")}
		if strings.TrimSpace(probe.Action) != "" {
			var op CharacterOp
			err = json.Unmarshal(input, &op)
			op.ID = "launcher-op-" + strings.TrimSuffix(name, ".json")
			op.AccountID, op.Operator = account, "US100 launcher"
			result["action"] = op.Action
			if err == nil {
				var rec OpReceipt
				rec, _, err = svc.ApplyOp(context.Background(), op)
				if err == nil {
					result["op_receipt"] = rec
					result["character_id"] = op.CharacterID
					if committed != nil && op.CharacterID > 0 {
						committed(op.CharacterID)
					}
				}
			}
		} else {
			var g storage.Grant
			err = json.Unmarshal(input, &g)
			g.ID = "launcher-" + strings.TrimSuffix(name, ".json")
			g.AccountID, g.Operator = account, "US100 launcher"
			if err == nil && (len(g.Items) != 1 || g.Items[0].Amount > 999 || g.Character <= 0 || g.Cera != 0 || g.Gold != 0 || strings.TrimSpace(g.Reason) == "") {
				err = fmt.Errorf("invalid item grant")
			}
			if err == nil {
				var receipt Receipt
				receipt, _, err = svc.Apply(context.Background(), g)
				if err == nil {
					result["receipt"] = receipt
					result["character_id"] = g.Character
					if committed != nil {
						committed(g.Character)
					}
				}
			}
		}
		if err != nil {
			result["error"] = err.Error()
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		final := filepath.Join(out, name)
		if err = os.WriteFile(final+".tmp", data, 0600); err != nil {
			return err
		}
		if err = os.Rename(final+".tmp", final); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
