package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashToken returns the hex-encoded SHA-256 of a high-entropy opaque secret
// (e.g. an API key or session identifier). These values are long random
// strings, not human-chosen passwords, so a fast one-way hash is the correct
// primitive for at-rest storage and constant-work lookup; a slow KDF like
// bcrypt is neither required nor desirable here.
//
// The output is always 64 lowercase hex characters.
func HashToken(opaqueSecret string) string {
	digest := sha256.Sum256([]byte(opaqueSecret))
	return hex.EncodeToString(digest[:])
}
