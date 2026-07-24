package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE (RFC 7636) parameters for a single Authorization Code flow. No client
// secret is ever involved; the code_verifier is the only proof-of-possession.
type PKCE struct {
	Verifier  string // 43-128 chars, [A-Za-z0-9-._~]
	Challenge string // BASE64URL(SHA256(verifier)), no padding
	Method    string // always "S256"
}

// NewPKCE generates a fresh verifier/challenge pair using S256.
func NewPKCE() (PKCE, error) {
	v, err := randomVerifier(64)
	if err != nil {
		return PKCE{}, err
	}
	return PKCE{
		Verifier:  v,
		Challenge: S256Challenge(v),
		Method:    "S256",
	}, nil
}

// S256Challenge computes BASE64URL-NOPAD(SHA256(verifier)) per RFC 7636 §4.2.
func S256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomVerifier returns a URL-safe, unpadded random string. RFC 7636 requires
// 43-128 characters from the unreserved set; base64url of n random bytes yields
// ceil(n*4/3) such characters.
func randomVerifier(nbytes int) (string, error) {
	if nbytes < 32 {
		nbytes = 32
	}
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// randomState returns a random opaque state value for CSRF protection.
func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
