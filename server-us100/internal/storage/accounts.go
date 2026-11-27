package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"dfolan/internal/db100"
)

const (
	pbkdf2Scheme     = "pbkdf2_sha256"
	pbkdf2Iterations = 100000
	pbkdf2SaltBytes  = 16
	pbkdf2KeyBytes   = 32
)

var (
	ErrAccountExists      = errors.New("account already exists")
	ErrAccountNotFound    = errors.New("account not found")
	ErrInvalidCredentials = errors.New("invalid username or password")

	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)
)

// ValidateAccountCredentials checks username and password format rules shared
// with the desktop launcher.
func ValidateAccountCredentials(username, password string) error {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return errors.New("username must be 3-32 characters of letters, digits, '_', '.', or '-'")
	}
	if len(password) < 4 || len(password) > 64 {
		return errors.New("password must be 4-64 characters")
	}
	return nil
}

// pbkdf2SHA256 implements RFC 2898 PBKDF2-HMAC-SHA256 using only the Go standard
// library, matching Python's hashlib.pbkdf2_hmac('sha256', ...).
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	dk := make([]byte, 0, numBlocks*hashLen)
	var blockIdx [4]byte
	for block := 1; block <= numBlocks; block++ {
		binary.BigEndian.PutUint32(blockIdx[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(blockIdx[:])
		u := mac.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// HashPassword generates a salted PBKDF2-HMAC-SHA256 password hash string:
// pbkdf2_sha256$<iterations>$<salt_hex>$<dk_hex>
func HashPassword(password string) (string, error) {
	if len(password) < 4 || len(password) > 64 {
		return "", errors.New("password must be 4-64 characters")
	}
	salt := make([]byte, pbkdf2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyBytes)
	return fmt.Sprintf("%s$%d$%s$%s", pbkdf2Scheme, pbkdf2Iterations, hex.EncodeToString(salt), hex.EncodeToString(dk)), nil
}

// VerifyPassword verifies a plaintext password against a stored PBKDF2 hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != pbkdf2Scheme {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 || iter > 2000000 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := hex.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual := pbkdf2SHA256([]byte(password), salt, iter, len(expected))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// RegisterAccount creates a new password-protected player account.
func (s *Store) RegisterAccount(ctx context.Context, username, password string) (int64, error) {
	username = strings.TrimSpace(username)
	if err := ValidateAccountCredentials(username, password); err != nil {
		return 0, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return 0, err
	}
	want := strings.ToLower(username)
	var id int64
	err = s.db.Mutate(func(tx *db100.Tx) error {
		for _, key := range tx.Keys("accounts") {
			var a accountRow
			if ok, e := tx.Get("accounts", key, &a); e != nil {
				return e
			} else if ok && strings.ToLower(a.Username) == want {
				return ErrAccountExists
			}
		}
		next, e := nextID(tx, "accounts")
		if e != nil {
			return e
		}
		h := hash
		row := accountRow{
			ID:              next,
			Username:        username,
			PasswordHash:    &h,
			DevelopmentOnly: false,
			CreatedAt:       nowStamp(),
		}
		if e = tx.Put("accounts", idKey(next), row); e != nil {
			return e
		}
		id = next
		return nil
	})
	return id, err
}

// AuthenticateAccount verifies username and password against a registered account.
func (s *Store) AuthenticateAccount(ctx context.Context, username, password string) (int64, error) {
	want := strings.ToLower(strings.TrimSpace(username))
	if want == "" || password == "" {
		return 0, ErrInvalidCredentials
	}
	var matched accountRow
	var found bool
	s.db.Each("accounts", func(_ string, raw json.RawMessage) bool {
		var a accountRow
		if json.Unmarshal(raw, &a) == nil && strings.ToLower(a.Username) == want {
			matched = a
			found = true
			return false
		}
		return true
	})
	if !found || matched.PasswordHash == nil || !VerifyPassword(*matched.PasswordHash, password) {
		return 0, ErrInvalidCredentials
	}
	return matched.ID, nil
}

// FindAccount looks up an existing account (registered or development) by
// username without auto-creating it.
func (s *Store) FindAccount(ctx context.Context, username string) (int64, bool, error) {
	want := strings.ToLower(strings.TrimSpace(username))
	if want == "" {
		return 0, false, nil
	}
	var id int64
	var found bool
	s.db.Each("accounts", func(_ string, raw json.RawMessage) bool {
		var a accountRow
		if json.Unmarshal(raw, &a) == nil && strings.ToLower(a.Username) == want {
			id = a.ID
			found = true
			return false
		}
		return true
	})
	return id, found, nil
}

// RegisteredAccountCount returns how many password-protected accounts exist.
func (s *Store) RegisteredAccountCount() int {
	count := 0
	s.db.Each("accounts", func(_ string, raw json.RawMessage) bool {
		var a accountRow
		if json.Unmarshal(raw, &a) == nil && a.PasswordHash != nil && *a.PasswordHash != "" {
			count++
		}
		return true
	})
	return count
}
