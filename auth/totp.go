package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTPConfig is the time-based one-time password policy (RFC 6238).
//
// The defaults are the ones every authenticator app assumes: six digits, a thirty second
// step and SHA-1. Changing them makes the otpauth:// URL carry the change, so an app that
// scans the QR code stays in sync.
type TOTPConfig struct {
	Digits int
	Period time.Duration
	// Skew is how many steps either side are accepted, to tolerate clock drift. One step
	// is the usual compromise between usability and replay window.
	Skew   int
	Issuer string
}

// DefaultTOTPConfig returns the authenticator-app defaults.
func DefaultTOTPConfig(issuer string) TOTPConfig {
	return TOTPConfig{Digits: 6, Period: 30 * time.Second, Skew: 1, Issuer: issuer}
}

func (c TOTPConfig) normalize() TOTPConfig {
	if c.Digits <= 0 {
		c.Digits = 6
	}
	if c.Period <= 0 {
		c.Period = 30 * time.Second
	}
	if c.Skew < 0 {
		c.Skew = 0
	}
	return c
}

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh 20-byte secret in the base32 form authenticator apps
// expect.
func NewTOTPSecret() (string, error) {
	buffer := make([]byte, 20)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("auth: generate mfa secret: %w", err)
	}
	return base32NoPadding.EncodeToString(buffer), nil
}

// TOTPCode returns the code for a secret at a moment in time.
func TOTPCode(secret string, at time.Time, cfg TOTPConfig) (string, error) {
	cfg = cfg.normalize()
	key, err := base32NoPadding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("auth: invalid mfa secret: %w", err)
	}
	counter := uint64(at.Unix() / int64(cfg.Period.Seconds()))

	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(message)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	modulus := uint32(1)
	for i := 0; i < cfg.Digits; i++ {
		modulus *= 10
	}
	return fmt.Sprintf("%0*d", cfg.Digits, truncated%modulus), nil
}

// VerifyTOTP reports whether a code is valid at a moment in time, accepting one step of
// drift either way by default.
func VerifyTOTP(secret, code string, at time.Time, cfg TOTPConfig) bool {
	cfg = cfg.normalize()
	code = strings.TrimSpace(code)
	if len(code) != cfg.Digits {
		return false
	}
	for offset := -cfg.Skew; offset <= cfg.Skew; offset++ {
		candidate, err := TOTPCode(secret, at.Add(time.Duration(offset)*cfg.Period), cfg)
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// OTPAuthURL is what a QR code encodes.
func OTPAuthURL(issuer, account, secret string, cfg TOTPConfig) string {
	cfg = cfg.normalize()
	if issuer == "" {
		issuer = cfg.Issuer
	}
	label := account
	if issuer != "" {
		label = issuer + ":" + account
	}
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprintf("%d", cfg.Digits))
	query.Set("period", fmt.Sprintf("%d", int(cfg.Period.Seconds())))
	return "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode()
}
