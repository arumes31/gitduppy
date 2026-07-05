package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

// CredentialsPayload represents the structure stored in encrypted field.
type CredentialsPayload struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	SSHKey   string `json:"ssh_key,omitempty"`
}

// EncryptionService handles AES-256-GCM encryption/decryption.
type EncryptionService struct {
	masterKey [32]byte // 256-bit key
}

// NewEncryptionService creates a new encryption service.
func NewEncryptionService(masterKey string) (*EncryptionService, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key must be exactly 32 bytes (256 bits)")
	}
	var key [32]byte
	copy(key[:], []byte(masterKey))
	return &EncryptionService{masterKey: key}, nil
}

// Encrypt encrypts a CredentialsPayload and returns base64-encoded ciphertext.
func (s *EncryptionService) Encrypt(payload CredentialsPayload) (string, error) {
	// #nosec G117
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(s.masterKey[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext and returns CredentialsPayload.
func (s *EncryptionService) Decrypt(encrypted string) (CredentialsPayload, error) {
	var empty CredentialsPayload

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return empty, err
	}

	block, err := aes.NewCipher(s.masterKey[:])
	if err != nil {
		return empty, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return empty, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return empty, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, decryptErr := gcm.Open(nil, nonce, ciphertext, nil)
	if decryptErr != nil {
		return empty, decryptErr
	}

	var payload CredentialsPayload
	err = json.Unmarshal(plaintext, &payload)
	return payload, err
}

// EncryptString encrypts a plain string and returns base64-encoded ciphertext.
func (s *EncryptionService) EncryptString(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.masterKey[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptString decrypts a base64-encoded ciphertext and returns the plain string.
func (s *EncryptionService) DecryptString(encrypted string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(s.masterKey[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, decryptErr := gcm.Open(nil, nonce, ciphertext, nil)
	if decryptErr != nil {
		return "", decryptErr
	}

	return string(plaintext), nil
}
