package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type rsaSigner struct {
	key *rsa.PrivateKey
	kid string
}

func newRSASigner(pemText, kid string) (*rsaSigner, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("auth: RSA key PEM is invalid")
	}
	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = parsed
	} else if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("auth: RSA key is not an RSA private key")
		}
		key = rsaKey
	} else {
		return nil, fmt.Errorf("auth: parse RSA private key: %w", err)
	}
	if kid == "" {
		kid = "default"
	}
	return &rsaSigner{key: key, kid: kid}, nil
}

type idTokenClaims struct {
	Username string `json:"preferred_username,omitempty"`
	TenantID int64  `json:"tenant_id,omitempty"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Nonce    string `json:"nonce,omitempty"`
	AuthTime int64  `json:"auth_time,omitempty"`
	jwt.RegisteredClaims
}

// SignIDToken issues an RS256 id_token for the OpenID Connect scope.
func (s *TokenService) SignIDToken(principal Principal, clientID, nonce string) (string, error) {
	if s.oidc == nil {
		return "", fmt.Errorf("auth: RSA signing key is not configured")
	}
	now := time.Now()
	subject := principal.Subject
	if subject == "" {
		subject = principal.Username
	}
	claims := idTokenClaims{
		Username: principal.Username,
		TenantID: principal.TenantID,
		Email:    principal.Email,
		Name:     principal.Name,
		Nonce:    nonce,
		AuthTime: now.Unix(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{clientID},
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.IDTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        randomUUID(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.oidc.kid
	return token.SignedString(s.oidc.key)
}

// HasOIDC reports whether an RSA signing key is configured.
func (s *TokenService) HasOIDC() bool { return s.oidc != nil }

// JWKS returns the public JSON Web Key Set for id_token verification.
func (s *TokenService) JWKS() (map[string]any, error) {
	if s.oidc == nil {
		return nil, fmt.Errorf("auth: RSA signing key is not configured")
	}
	public := s.oidc.key.PublicKey
	return map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"kid": s.oidc.kid,
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}},
	}, nil
}
