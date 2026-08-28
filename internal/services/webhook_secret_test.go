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
	stored, err := s.encryptSecret(secret)
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
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

func TestWebhookSecretLegacyPlaintextRejected(t *testing.T) {
	enc, _ := crypto.NewEncryptionService(strings.Repeat("k", 32))
	s := &WebhookService{encryption: enc}

	const legacy = "legacy-plaintext-secret"
	if got, err := s.decryptSecret(legacy); err == nil {
		t.Fatalf("legacy plaintext should be rejected, got %q", got)
	}
}

func TestWebhookSecretEmptyStaysEmpty(t *testing.T) {
	enc, _ := crypto.NewEncryptionService(strings.Repeat("k", 32))
	s := &WebhookService{encryption: enc}
	got, err := s.encryptSecret("")
	if err != nil {
		t.Fatalf("encrypt empty secret: %v", err)
	}
	if got != "" {
		t.Errorf("empty secret should stay empty, got %q", got)
	}
}

func TestWebhookSecretEncryptionRequiresService(t *testing.T) {
	s := &WebhookService{}
	if got, err := s.encryptSecret("secret"); err == nil {
		t.Fatalf("missing encryption service should fail, got %q", got)
	}
	if service, err := NewWebhookService(nil, nil); err == nil {
		t.Fatalf("constructor should reject missing encryption service, got %#v", service)
	}
}
