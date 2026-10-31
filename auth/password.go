package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// PasswordHasher verifies encoded passwords.
type PasswordHasher interface {
	Hash(raw string) (string, error)
	Verify(encoded, raw string) error
}

// BcryptHasher implements PasswordHasher with bcrypt and the legacy encodings
// the platform stores: {bcrypt}, {SHA-256HEX}, {noop} and bare bcrypt.
type BcryptHasher struct {
	Cost int
}

// Hash hashes a new password.
func (h BcryptHasher) Hash(raw string) (string, error) {
	cost := h.Cost
	if cost <= 0 {
		cost = bcrypt.DefaultCost
	}
	sum, err := bcrypt.GenerateFromPassword([]byte(raw), cost)
	if err != nil {
		return "", err
	}
	return "{bcrypt}" + string(sum), nil
}

// Verify checks raw against encoded.
func (h BcryptHasher) Verify(encoded, raw string) error {
	id, hash, ok := splitEncoding(encoded)
	if !ok {
		if strings.HasPrefix(encoded, "$2") {
			return compareBcrypt(encoded, raw)
		}
		return errors.New("auth: malformed password hash")
	}
	switch id {
	case "bcrypt":
		return compareBcrypt(hash, raw)
	case "SHA-256HEX":
		sum := sha256.Sum256([]byte(raw))
		want := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(want), []byte(hash)) != 1 {
			return errors.New("auth: password mismatch")
		}
		return nil
	case "noop":
		if subtle.ConstantTimeCompare([]byte(hash), []byte(raw)) != 1 {
			return errors.New("auth: password mismatch")
		}
		return nil
	default:
		return errors.New("auth: unsupported password encoding " + id)
	}
}

func compareBcrypt(hash, raw string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)); err != nil {
		return errors.New("auth: password mismatch")
	}
	return nil
}

func splitEncoding(encoded string) (string, string, bool) {
	if !strings.HasPrefix(encoded, "{") {
		return "", "", false
	}
	end := strings.IndexByte(encoded, '}')
	if end <= 1 || end == len(encoded)-1 {
		return "", "", false
	}
	return encoded[1:end], encoded[end+1:], true
}
