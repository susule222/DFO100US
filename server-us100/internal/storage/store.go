// Package storage persists accounts and characters in the project's own
// single-file database (internal/db100). It replaced the borrowed
// PostgreSQL/Redis backend wholesale: same exported API, no external process,
// no network. Row JSON keeps the old column names so the seed exported from
// the last PostgreSQL snapshot imports unchanged.
package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dfolan/internal/db100"
)

// ErrNoRows mirrors the sentinel the old PostgreSQL backend surfaced for a
// missing row; callers match on it with errors.Is.
var ErrNoRows = errors.New("no rows in result set")

type Config struct {
	DBPath          string `json:"db_path"`
	SeedPath        string `json:"seed_path"`
	RequireExisting bool   `json:"-"`
	SkipSeed        bool   `json:"-"`
}

// LoadConfig reads the JSON config. Empty db_path/seed_path default next to
// the config file itself (runtime/storage/local.json -> runtime/us100.db and
// runtime/us100_seed.json); legacy PostgreSQL/Redis keys in the same file are
// ignored.
func LoadConfig(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	dir := filepath.Dir(path)
	if c.DBPath == "" {
		c.DBPath = filepath.Join(dir, "..", "us100.db")
	}
	if c.SeedPath == "" {
		c.SeedPath = filepath.Join(dir, "..", "us100_seed.json")
	}
	return c, nil
}

type Store struct {
	db *db100.DB
}

// Database exposes the raw db100 handle for the validation harness, which
// injects corrupt legacy rows directly instead of going through the API.
func (s *Store) Database() *db100.DB { return s.db }

func Open(ctx context.Context, c Config) (*Store, error) {
	if c.DBPath == "" {
		return nil, errors.New("storage configuration incomplete")
	}
	var (
		db *db100.DB
		e  error
	)
	if c.RequireExisting {
		db, e = db100.OpenExisting(c.DBPath)
	} else {
		db, e = db100.Open(c.DBPath)
	}
	if e != nil {
		return nil, e
	}
	if !c.SkipSeed && c.SeedPath != "" {
		if e = db.ImportSeed(c.SeedPath); e != nil {
			return nil, e
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() { s.db.Close() }

// Migrate initialises the id sequences. db100 is schemaless, so the old
// CREATE TABLE migrations collapse into sequence bookkeeping.
func (s *Store) Migrate(ctx context.Context) error {
	return s.db.Mutate(func(tx *db100.Tx) error {
		if e := initSeq(tx, "accounts"); e != nil {
			return e
		}
		return initSeq(tx, "characters")
	})
}

const seqTable = "meta"

func seqKey(table string) string { return "seq:" + table }

func readSeq(tx *db100.Tx, table string) int64 {
	var v int64
	raw, ok := tx.Raw(seqTable, seqKey(table))
	if ok {
		json.Unmarshal(raw, &v)
	}
	return v
}

// initSeq makes sure the sequence is at least the highest id already present
// (seeded rows carry their PostgreSQL ids).
func initSeq(tx *db100.Tx, table string) error {
	cur := readSeq(tx, table)
	var max int64
	for _, key := range tx.Keys(table) {
		if id, e := strconv.ParseInt(key, 10, 64); e == nil && id > max {
			max = id
		}
	}
	if max > cur {
		return tx.Put(seqTable, seqKey(table), max)
	}
	return nil
}

func nextID(tx *db100.Tx, table string) (int64, error) {
	if e := initSeq(tx, table); e != nil {
		return 0, e
	}
	next := readSeq(tx, table) + 1
	if e := tx.Put(seqTable, seqKey(table), next); e != nil {
		return 0, e
	}
	return next, nil
}

func idKey(id int64) string { return strconv.FormatInt(id, 10) }

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// parseStamp accepts RFC 3339 (what this package writes) and the PostgreSQL
// text format (what the seed snapshot may still carry).
func parseStamp(v string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05.999999Z07"} {
		if t, e := time.Parse(layout, v); e == nil {
			return t
		}
	}
	return time.Time{}
}

// ---- row types: JSON names match the old PostgreSQL columns ----

type accountRow struct {
	ID              int64   `json:"id"`
	Username        string  `json:"username"`
	PasswordHash    *string `json:"password_hash,omitempty"`
	DevelopmentOnly bool    `json:"development_only"`
	CreatedAt       string  `json:"created_at"`
}

type characterRow struct {
	ID            int64           `json:"id"`
	AccountID     int64           `json:"account_id"`
	WireID        int             `json:"wire_id"`
	Name          string          `json:"name"`
	Profession    int             `json:"profession"`
	CreateRequest string          `json:"create_request"` // base64
	ConfigVersion string          `json:"config_version"`
	State         json.RawMessage `json:"state"`
	CreatedAt     string          `json:"created_at"`
	DeletedAt     *string         `json:"deleted_at,omitempty"`
}

func (r *characterRow) character() (Character, error) {
	c := Character{
		ID:            r.ID,
		AccountID:     r.AccountID,
		WireID:        uint16(r.WireID),
		Name:          r.Name,
		Profession:    byte(r.Profession),
		ConfigVersion: r.ConfigVersion,
		State:         r.State,
		CreatedAt:     parseStamp(r.CreatedAt),
	}
	if r.CreateRequest != "" {
		b, e := base64.StdEncoding.DecodeString(r.CreateRequest)
		if e != nil {
			return c, fmt.Errorf("character %d request: %w", r.ID, e)
		}
		c.Request = b
	}
	return c, nil
}

func newCharacterRow(c Character) characterRow {
	return characterRow{
		ID:            c.ID,
		AccountID:     c.AccountID,
		WireID:        int(c.WireID),
		Name:          c.Name,
		Profession:    int(c.Profession),
		CreateRequest: base64.StdEncoding.EncodeToString(c.Request),
		ConfigVersion: c.ConfigVersion,
		State:         c.State,
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// charRow loads a character row, optionally requiring the owning account and
// a live (not archived) row. The bool reports presence.
func charRowTx(tx *db100.Tx, account, id int64, liveOnly bool) (characterRow, bool, error) {
	var r characterRow
	ok, e := tx.Get("characters", idKey(id), &r)
	if e != nil || !ok {
		return r, false, e
	}
	if r.AccountID != account {
		return r, false, nil
	}
	if liveOnly && r.DeletedAt != nil {
		return r, false, nil
	}
	return r, true, nil
}

func (s *Store) charRow(account, id int64, liveOnly bool) (characterRow, bool, error) {
	var r characterRow
	var found bool
	e := s.db.Mutate(func(tx *db100.Tx) error {
		var e error
		r, found, e = charRowTx(tx, account, id, liveOnly)
		return e
	})
	return r, found, e
}

// ---- public API ----

func (s *Store) NameExists(ctx context.Context, name string) (bool, error) {
	want := strings.ToLower(name)
	var exists bool
	s.db.Each("characters", func(_ string, raw json.RawMessage) bool {
		var r characterRow
		if json.Unmarshal(raw, &r) == nil && strings.ToLower(r.Name) == want {
			exists = true
			return false
		}
		return true
	})
	return exists, nil
}

func (s *Store) DevelopmentAccount(ctx context.Context, name string) (int64, error) {
	var id int64
	e := s.db.Mutate(func(tx *db100.Tx) error {
		for _, key := range tx.Keys("accounts") {
			var a accountRow
			if ok, e := tx.Get("accounts", key, &a); e != nil {
				return e
			} else if ok && a.Username == name {
				if !a.DevelopmentOnly {
					return ErrNoRows
				}
				id = a.ID
				return nil
			}
		}
		next, e := nextID(tx, "accounts")
		if e != nil {
			return e
		}
		row := accountRow{ID: next, Username: name, DevelopmentOnly: true, CreatedAt: nowStamp()}
		if e = tx.Put("accounts", idKey(next), row); e != nil {
			return e
		}
		id = next
		return nil
	})
	return id, e
}

type Character struct {
	ID            int64
	AccountID     int64
	WireID        uint16
	Name          string
	Profession    byte
	Request       []byte
	ConfigVersion string
	State         json.RawMessage
	CreatedAt     time.Time
}

func (s *Store) CreateCharacter(ctx context.Context, c Character, maxCharacters int) (Character, error) {
	if maxCharacters < 1 || maxCharacters > 65534 || c.Name == "" || c.ConfigVersion == "" || !json.Valid(c.State) {
		return c, errors.New("invalid character or missing initial configuration")
	}
	e := s.db.Mutate(func(tx *db100.Tx) error {
		if ok, _ := tx.Get("accounts", idKey(c.AccountID), nil); !ok {
			return ErrNoRows
		}
		// The old schema enforced lower(name) uniqueness across ALL accounts
		// with a unique index; the single pass below replaces it.
		want := strings.ToLower(c.Name)
		n, next := 0, 0
		for _, key := range tx.Keys("characters") {
			var r characterRow
			if ok, e := tx.Get("characters", key, &r); e != nil {
				return e
			} else if !ok {
				continue
			}
			if strings.ToLower(r.Name) == want {
				return errors.New("character name already exists")
			}
			if r.AccountID != c.AccountID {
				continue
			}
			if r.DeletedAt == nil {
				n++
			}
			if r.WireID > next {
				next = r.WireID
			}
		}
		next++
		if n >= maxCharacters || next > 65534 {
			return errors.New("character slots full")
		}
		id, e := nextID(tx, "characters")
		if e != nil {
			return e
		}
		c.ID, c.WireID, c.CreatedAt = id, uint16(next), time.Now().UTC()
		return tx.Put("characters", idKey(id), newCharacterRow(c))
	})
	return c, e
}

func (s *Store) LiveCharacter(account, id int64) (Character, error) {
	r, ok, err := s.charRow(account, id, true)
	if err != nil {
		return Character{}, err
	}
	if !ok {
		return Character{}, ErrNoRows
	}
	return r.character()
}

func (s *Store) Characters(ctx context.Context, account int64) ([]Character, error) {
	var rows []characterRow
	s.db.Each("characters", func(_ string, raw json.RawMessage) bool {
		var r characterRow
		if json.Unmarshal(raw, &r) == nil && r.AccountID == account && r.DeletedAt == nil {
			rows = append(rows, r)
		}
		return true
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].WireID < rows[j].WireID })
	out := make([]Character, 0, len(rows))
	for i := range rows {
		c, e := rows[i].character()
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}
