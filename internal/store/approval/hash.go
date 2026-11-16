// Package approval decides what a project or namespace has been approved for: the files that are compared and the
// SHA-256 of each that the record in the global store keeps.
package approval

import (
	"crypto/sha256"
	"encoding/hex"
)

// digest is the lowercase hex SHA-256 of what is compared: it is what a record keeps for a file.
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
