// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-5). Don't hand-edit without checking the plan.

// Package approval decides what a project or namespace has been approved for: the files that are hashed and shown
// (plan section 4.1), the record kept in the global store, and the diff between two states.
package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/pyjson"
)

const (
	BinaryMark  = "\x00binary:"            // snapshot value for a binary file, followed by its SHA-256
	InvalidMark = "\x00not UTF-8, sha256 " // last line of a text file with bad bytes
)

// sortedKeys orders paths as Python orders the str values os.fsdecode makes of them, never sort.Strings.
func sortedKeys(files map[string]string) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return pyjson.Less(keys[i], keys[j]) })
	return keys
}

// hashInput is json.dumps(sorted(files.items())): an array of [path, text] pairs, default separators, ensure_ascii.
func hashInput(files map[string]string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, k := range sortedKeys(files) {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('[')
		pyjson.WriteString(&b, k, true)
		b.WriteString(", ")
		pyjson.WriteString(&b, files[k], true)
		b.WriteByte(']')
	}
	b.WriteByte(']') // an empty map is "[]"
	return b.String()
}

// FilesHash is files_hash: SHA-256 (lowercase hex) of hashInput's UTF-8 bytes.
func FilesHash(files map[string]string) string {
	sum := sha256.Sum256([]byte(hashInput(files)))
	return hex.EncodeToString(sum[:])
}

// Reviewable is a file as it's hashed and shown. Only a compiled program is hidden: a NUL or a bad byte must not
// hide a script bash will still run. The digest keeps two files that display the same from hashing the same.
func Reviewable(raw []byte) string {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if bytes.HasPrefix(raw, []byte("\x7fELF")) || bytes.HasPrefix(raw, []byte(BinaryMark)) {
		return BinaryMark + digest
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
	if valid && !strings.Contains(text, InvalidMark) {
		return text
	}
	return text + "\n" + InvalidMark + digest + "\n"
}
