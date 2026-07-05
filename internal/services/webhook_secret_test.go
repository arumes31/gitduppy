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
	got, err := s.decryptSecret(stored)
	if err != nil {
		t.Fatalf("decryptSecret: %v", err)
	}
	if got != secret {
		t.Errorf("decryptSecret round-trip = %q, want %q", got, secret)
	}
}

func TestWebhookSecretUndecryptableFails(t *testing.T) {
	enc, _ := crypto.NewEncryptionService(strings.Repeat("k", 32))
	s := &WebhookService{encryption: enc}

	// A prefixed value that is not valid ciphertext must error rather than return
	// the raw ciphertext as if it were the secret.
	if got, err := s.decryptSecret(encSecretPrefix + "not-real-ciphertext"); err == nil {
		t.Errorf("expected error for undecryptable secret, got %q", got)
	}
}

func TestWebhookSecretLegacyPlaintext(t *testing.T) {
	enc, _ := crypto.NewEncryptionService(strings.Repeat("k", 32))
	s := &WebhookService{encryption: enc}

	// A legacy value without the prefix must be returned unchanged.
	const legacy = "legacy-plaintext-secret"
	got, err := s.decryptSecret(legacy)
	if err != nil {
		t.Fatalf("legacy plaintext should not error: %v", err)
	}
	if got != legacy {
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
