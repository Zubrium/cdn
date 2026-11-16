// Package identity handles credentials for Core principals.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewSecret returns a credential with 256 bits of cryptographic entropy.
func NewSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ct_live_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func HashSecret(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}
