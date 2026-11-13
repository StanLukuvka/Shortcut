// Package approval decides what a project or namespace has been approved for: the files that are hashed and shown,
// the record kept in the global store, and the diff between two states.
package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
)

const (
	binaryMark  = "\x00binary:"            // snapshot value for a binary file, followed by its SHA-256
	invalidMark = "\x00not UTF-8, sha256 " // last line of a text file with bad bytes
)

// hashInput is json.dumps(sorted(files.items())): an array of [path, text] pairs, default separators, ensure_ascii.
func hashInput(files map[string]string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, k := range slices.SortedFunc(maps.Keys(files), pyjson.Compare) {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('[')
		pyjson.WriteString(&b, k)
		b.WriteString(", ")
		pyjson.WriteString(&b, files[k])
		b.WriteByte(']')
	}
	b.WriteByte(']') // an empty map is "[]"
	return b.String()
}

// filesHash returns the SHA-256 (lowercase hex) of hashInput's UTF-8 bytes.
func filesHash(files map[string]string) string {
	sum := sha256.Sum256([]byte(hashInput(files)))
	return hex.EncodeToString(sum[:])
}

// Reviewable is a file as it's hashed and shown. Only a compiled program is hidden: a NUL or a bad byte must not
// hide a script bash will still run. The digest keeps two files that display the same from hashing the same.
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
