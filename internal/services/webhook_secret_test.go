package services

import (
	"strings"
	"testing"

	"github.com/gitduppy/gitduppy/pkg/crypto"
)

func TestWebhookSecretEncryptRoundTrip(t *testing.T) {
	enc, err := crypto.NewEncryptionService(strings.Repeat("k", 32))
	if err != nil {
		t.Fatalf("new encryption service: %v", err)
	}
	// db is nil here on purpose: encrypt/decrypt secret helpers must not touch it.
	s := &WebhookService{encryption: enc}

	const secret = "super-secret-hmac-key"
	stored := s.encryptSecret(secret)
	if stored == secret {
		t.Fatal("secret should not be stored in plaintext")
	}
	if !strings.HasPrefix(stored, encSecretPrefix) {
		t.Fatalf("stored secret should carry the %q prefix, got %q", encSecretPrefix, stored)
	}
	if got := s.decryptSecret(stored); got != secret {
		t.Errorf("decryptSecret round-trip = %q, want %q", got, secret)
	}
}

func TestWebhookSecretLegacyPlaintext(t *testing.T) {
	enc, _ := crypto.NewEncryptionService(strings.Repeat("k", 32))
	s := &WebhookService{encryption: enc}

	// A legacy value without the prefix must be returned unchanged.
	const legacy = "legacy-plaintext-secret"
	if got := s.decryptSecret(legacy); got != legacy {
		t.Errorf("legacy plaintext should pass through, got %q", got)
	}
}

func TestWebhookSecretEmptyStaysEmpty(t *testing.T) {
	enc, _ := crypto.NewEncryptionService(strings.Repeat("k", 32))
	s := &WebhookService{encryption: enc}
	if got := s.encryptSecret(""); got != "" {
		t.Errorf("empty secret should stay empty, got %q", got)
	}
}
