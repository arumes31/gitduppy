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
// NOTE: CodeQL's go/weak-sensitive-data-hashing flags this SHA-256 use. It is a
// reviewed false positive (the query targets password hashing; these inputs are
// high-entropy tokens) and is dismissed in the Security tab — GitHub code
// scanning does not honor in-source suppression comments, so this is only
// documentation, not a suppression.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
