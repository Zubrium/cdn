package identity

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewSecretHas256BitsOfRandomEntropy(t *testing.T) {
	first, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("independent credentials collided")
	}
	if !strings.HasPrefix(first, "ct_live_") {
		t.Fatalf("missing credential prefix: %q", first)
	}
	encoded := strings.TrimPrefix(first, "ct_live_")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("credential entropy is not 256 bits: %d bytes, %v", len(decoded), err)
	}
}

func TestHashSecretIsStableOneWaySizedDigest(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	first := HashSecret(secret)
	second := HashSecret(secret)
	if len(first) != 32 || !bytes.Equal(first, second) {
		t.Fatal("secret hash must be a stable SHA-256 digest")
	}
	if bytes.Equal(first, []byte(secret)) || bytes.Contains(first, []byte(secret)) {
		t.Fatal("hash contains plaintext secret")
	}
	other, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, HashSecret(other)) {
		t.Fatal("different credentials produced the same hash")
	}
}
