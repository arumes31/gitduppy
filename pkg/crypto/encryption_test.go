package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

const validKey = "0123456789abcdef0123456789abcdef" // exactly 32 bytes

func TestNewEncryptionService(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid 32-byte key", validKey, false},
		{"valid 64-char hex key", strings.Repeat("ab", 32), false},
		{"invalid 64-char non-hex", strings.Repeat("zz", 32), true},
		{"too short", "short", true},
		{"empty", "", true},
		{"31 bytes", strings.Repeat("a", 31), true},
		{"33 bytes", strings.Repeat("a", 33), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewEncryptionService(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if svc == nil {
				t.Fatalf("expected service, got nil")
			}
		})
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	svc, err := NewEncryptionService(validKey)
	if err != nil {
		t.Fatal(err)
	}
	payload := CredentialsPayload{Username: "alice", Password: "s3cr3t", Token: "tok", SSHKey: "ssh-key"}
	enc, err := svc.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == "" {
		t.Fatal("expected non-empty ciphertext")
	}
	// Two encryptions of the same payload must differ (random nonce).
	enc2, _ := svc.Encrypt(payload)
	if enc == enc2 {
		t.Fatal("ciphertext should be non-deterministic")
	}
	got, err := svc.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != payload {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", got, payload)
	}
}

func TestEncryptStringRoundtrip(t *testing.T) {
	svc, _ := NewEncryptionService(validKey)
	plain := "hello-world-secret"
	enc, err := svc.EncryptString(plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.DecryptString(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("got %q want %q", got, plain)
	}
}

func TestDecryptErrors(t *testing.T) {
	svc, _ := NewEncryptionService(validKey)

	if _, err := svc.Decrypt("!!!not-base64!!!"); err == nil {
		t.Error("expected error on invalid base64")
	}
	if _, err := svc.Decrypt(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("expected error on short ciphertext")
	}
	// Tampered ciphertext should fail authentication.
	enc, _ := svc.EncryptString("data")
	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xFF
	if _, err := svc.DecryptString(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Error("expected error on tampered ciphertext")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	svc1, _ := NewEncryptionService(validKey)
	svc2, _ := NewEncryptionService("fedcba9876543210fedcba9876543210")
	enc, _ := svc1.EncryptString("secret")
	if _, err := svc2.DecryptString(enc); err == nil {
		t.Error("expected decryption with wrong key to fail")
	}
}

func TestDecryptStringInvalidBase64(t *testing.T) {
	svc, _ := NewEncryptionService(validKey)
	if _, err := svc.DecryptString("@@@"); err == nil {
		t.Error("expected error")
	}
	if _, err := svc.DecryptString(base64.StdEncoding.EncodeToString([]byte("x"))); err == nil {
		t.Error("expected error on short ciphertext")
	}
}
