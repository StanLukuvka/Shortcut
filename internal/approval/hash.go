// Package approval decides what a project or namespace has been approved for: the files that are compared and shown,
// the record kept in the global store, and the diff between two states.
package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	binaryMark  = "\x00binary:"            // snapshot value for a binary file, followed by its SHA-256
	invalidMark = "\x00not UTF-8, sha256 " // last line of a text file with bad bytes
)

// Reviewable is a file as it's compared and shown. Only a compiled program is hidden: a NUL or a bad byte must not
// hide a script bash will still run. The digest keeps two files that display the same from comparing equal.
func Reviewable(raw []byte) string {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if bytes.HasPrefix(raw, []byte("\x7fELF")) || bytes.HasPrefix(raw, []byte(binaryMark)) {
		return binaryMark + digest
	}
	var out strings.Builder
	valid := true
	for i := 0; i < len(raw); {
		r, n := utf8.DecodeRune(raw[i:])
		if r == utf8.RuneError && n == 1 { // an invalid byte; a real U+FFFD has n == 3
			fmt.Fprintf(&out, "\\x%02x", raw[i])
			valid = false
		} else {
			out.Write(raw[i : i+n])
		}
		i += n
	}
	text := out.String()
	if valid && !strings.Contains(text, invalidMark) {
		return text
	}
	return text + "\n" + invalidMark + digest + "\n"
}
