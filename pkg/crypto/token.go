package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashToken returns the hex-encoded SHA-256 of a high-entropy token (API key or
// session token). These tokens are long random secrets, not human-chosen
// passwords, so a fast one-way hash is the right primitive for at-rest storage
// and constant-work lookup; a slow KDF like bcrypt is neither required nor
// desirable here. The output is always 64 lowercase hex characters.
//
// CodeQL [go/weak-sensitive-data-hashing] - SHA-256 hashes high-entropy random secrets (API/session tokens), not passwords, so a slow hashing algorithm is not required or desirable.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
