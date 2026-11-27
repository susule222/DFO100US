package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"dfolan/internal/db100"
)

// AdminOpRequest records one idempotent character/account GM mutation in admin_grants.
type AdminOpRequest struct {
	ID          string          `json:"id"`
	AccountID   int64           `json:"account_id"`
	CharacterID int64           `json:"character_id,omitempty"`
	Operator    string          `json:"operator"`
	Reason      string          `json:"reason"`
	Request     json.RawMessage `json:"request"`
}

// AdminOpResult returns the committed or replayed receipt.
type AdminOpResult struct {
	Receipt   json.RawMessage
	Character Character
	Cera      uint64
	Applied   bool
}

// AdminTx gives an operator mutation transactional access to account currency,
// the target character row, and the character's quest rows inside one db100.Mutate.
type AdminTx struct {
	tx      *db100.Tx
	account int64
	charID  int64
}

func (a *AdminTx) AccountCera() (int64, error) {
	return ceraTx(a.tx, a.account)
}

func (a *AdminTx) SetAccountCera(v int64) error {
	if v < 0 || v > math.MaxUint32 {
		return fmt.Errorf("cera balance out of range (0..%d)", uint32(math.MaxUint32))
	}
	return a.tx.Put("account_currency", idKey(a.account), currencyRow{
		AccountID: a.account,
		Cera:      v,
		UpdatedAt: nowStamp(),
	})
}

func (a *AdminTx) Quests() ([]QuestState, error) {
	prefix := idKey(a.charID) + "/"
	var out []QuestState
	for _, key := range a.tx.Keys("character_quests") {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var r questRow
		ok, err := a.tx.Get("character_quests", key, &r)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, QuestState{
				ID:            uint16(r.QuestID),
				Status:        r.Status,
				Progress:      uint32(r.Progress),
				ConfigVersion: r.ConfigVersion,
				ProgressModel: r.ProgressModel,
			})
		}
	}
	sortQuests(out)
	return out, nil
}

func (a *AdminTx) GetQuest(qid uint16) (QuestState, bool, error) {
	var r questRow
	ok, err := a.tx.Get("character_quests", questKey(a.charID, qid), &r)
	if err != nil || !ok {
		return QuestState{}, false, err
	}
	return QuestState{
		ID:            uint16(r.QuestID),
		Status:        r.Status,
		Progress:      uint32(r.Progress),
		ConfigVersion: r.ConfigVersion,
		ProgressModel: r.ProgressModel,
	}, true, nil
}

func (a *AdminTx) PutQuest(q QuestState) error {
	if q.ID == 0 || q.ID == 65535 || len(q.ConfigVersion) != 64 {
		return fmt.Errorf("invalid quest state for %d", q.ID)
	}
	key := questKey(a.charID, q.ID)
	var existing questRow
	found, err := a.tx.Get("character_quests", key, &existing)
	if err != nil {
		return err
	}
	acceptedAt := nowStamp()
	if found && existing.AcceptedAt != "" {
		acceptedAt = existing.AcceptedAt
	}
	rec := questRow{
		CharacterID:   a.charID,
		QuestID:       int(q.ID),
		Status:        q.Status,
		Progress:      int64(q.Progress),
		ConfigVersion: q.ConfigVersion,
		ProgressModel: q.ProgressModel,
		AcceptedAt:    acceptedAt,
	}
	if q.Status == "completed" {
		done := nowStamp()
		rec.CompletedAt = &done
	}
	return a.tx.Put("character_quests", key, rec)
}

func (a *AdminTx) DeleteQuest(qid uint16) {
	key := questKey(a.charID, qid)
	a.tx.Delete("character_quests", key)
	a.tx.Delete("character_quest_rewards", key)
}

// ApplyAdminOp executes an idempotent GM mutation in one atomic db100 transaction.
// The callback returns the updated character state (nil means leave character state unchanged)
// and the JSON receipt to persist in admin_grants.
func (s *Store) ApplyAdminOp(ctx context.Context, req AdminOpRequest, fn func(atx *AdminTx, role *Character) (json.RawMessage, json.RawMessage, error)) (AdminOpResult, error) {
	var out AdminOpResult
	if req.ID == "" || req.AccountID == 0 || req.Operator == "" || strings.TrimSpace(req.Reason) == "" || fn == nil {
		return out, fmt.Errorf("admin op requires id, account, operator, reason and callback")
	}
	err := s.db.Mutate(func(tx *db100.Tx) error {
		if ok, _ := tx.Get("accounts", idKey(req.AccountID), nil); !ok {
			return fmt.Errorf("admin op target account missing: %w", ErrNoRows)
		}
		var prior grantRow
		found, e := tx.Get("admin_grants", req.ID, &prior)
		if e != nil {
			return e
		}
		if found {
			out.Receipt = prior.Receipt
			out.Cera = uint64(ceraMust(tx, req.AccountID))
			return nil
		}
		var charPtr *int64
		var rolePtr *Character
		var crow characterRow
		if req.CharacterID != 0 {
			r, ok, e := charRowTx(tx, req.AccountID, req.CharacterID, true)
			if e != nil {
				return e
			}
			if !ok {
				return fmt.Errorf("admin op target character not found: %w", ErrNoRows)
			}
			role, e := r.character()
			if e != nil {
				return e
			}
			crow = r
			rolePtr = &role
			v := req.CharacterID
			charPtr = &v
		}
		reqBody := req.Request
		if !json.Valid(reqBody) {
			reqBody = json.RawMessage("{}")
		}
		row := grantRow{
			GrantID:     req.ID,
			AccountID:   req.AccountID,
			CharacterID: charPtr,
			Request:     reqBody,
			Receipt:     json.RawMessage("{}"),
			Operator:    req.Operator,
			Reason:      req.Reason,
			CreatedAt:   nowStamp(),
		}
		if e = tx.Put("admin_grants", req.ID, row); e != nil {
			return e
		}
		atx := &AdminTx{tx: tx, account: req.AccountID, charID: req.CharacterID}
		newState, receipt, e := fn(atx, rolePtr)
		if e != nil {
			return e
		}
		if len(newState) > 0 {
			if rolePtr == nil {
				return fmt.Errorf("character state update without character_id")
			}
			if !json.Valid(newState) {
				return fmt.Errorf("invalid updated character state")
			}
			crow.State = newState
			if e = tx.Put("characters", idKey(crow.ID), crow); e != nil {
				return e
			}
			rolePtr.State = newState
			out.Character = *rolePtr
		} else if rolePtr != nil {
			out.Character = *rolePtr
		}
		if !json.Valid(receipt) {
			return fmt.Errorf("invalid admin op receipt")
		}
		out.Cera = uint64(ceraMust(tx, req.AccountID))
		out.Receipt, out.Applied = receipt, true
		row.Receipt = receipt
		return tx.Put("admin_grants", req.ID, row)
	})
	return out, err
}
