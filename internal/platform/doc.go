package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"unicode/utf8"
)

// ErrNotUTF8 is the refusal for text that is not valid UTF-8. encoding/json would turn it into U+FFFD, so two
// different file names could become one; nothing that is not valid UTF-8 passes through JSON.
var ErrNotUTF8 = errors.New("contains text that is not valid UTF-8 (invalid bytes or a lone surrogate escape)")

var errNotObject = errors.New("root must be a JSON object")

// Doc is one JSON object: each value is its raw text, as it was read. The zero value reads as the empty object;
// write to a Doc{}. Files are written with keys sorted. Object returns a copy; to change a nested object, change
// the copy and write it back with SetString or Bytes.
type Doc map[string]json.RawMessage

// ParseDoc reads a JSON object. Text that is not valid UTF-8 is refused (ErrNotUTF8). A repeated key takes the last value.
func ParseDoc(text []byte) (Doc, error) {
	if err := checkJSON(text); err != nil {
		return nil, err
	}
	d, ok := ObjectOf(text)
	if !ok {
		return nil, errNotObject
	}
	return d, nil
}

// ObjectOf reads text that is already known to be valid JSON, if it is an object.
func ObjectOf(text []byte) (Doc, bool) {
	var d Doc
	if json.Unmarshal(text, &d) != nil || d == nil { // null is no object
		return nil, false
	}
	return d, true
}

func (d Doc) Has(k string) bool {
	_, ok := d[k]
	return ok
}

func (d Doc) SetString(k, s string) error {
	raw, err := quote(s)
	if err != nil {
		return err
	}
	d[k] = raw
	return nil
}

// Object is the value of k when it is an object; otherwise an empty Doc and false.
func (d Doc) Object(k string) (Doc, bool) {
	if o, ok := ObjectOf(d[k]); ok {
		return o, true
	}
	return Doc{}, false
}

// String is the value of k when it is a string.
func (d Doc) String(k string) (string, bool) {
	var s string
	if raw := d[k]; len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// Bytes is the object on one line, keys sorted. A key or value that is not valid UTF-8 is an error (ErrNotUTF8).
func (d Doc) Bytes() ([]byte, error) {
	out, err := d.encode("")
	return bytes.TrimSuffix(out, []byte("\n")), err
}

// Pretty is the object as a store file is written: two-space indentation, keys sorted, a final newline.
func (d Doc) Pretty() ([]byte, error) { return d.encode("  ") }

func (d Doc) encode(indent string) ([]byte, error) {
	for _, k := range slices.Sorted(maps.Keys(d)) {
		if !utf8.ValidString(k) {
			return nil, fmt.Errorf("%w: key %q", ErrNotUTF8, k)
		}
		if !utf8.Valid(d[k]) {
			return nil, fmt.Errorf("%w: %q", ErrNotUTF8, d[k])
		}
	}
	return encode(d, indent)
}

// encode is json.Marshal without the escaping of < > & that encoding/json does by default, with a final newline.
func encode(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	err := enc.Encode(v)
	return b.Bytes(), err
}

// quote is s as a JSON string; text that is not valid UTF-8 is an error (ErrNotUTF8).
func quote(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, fmt.Errorf("%w: %q", ErrNotUTF8, s)
	}
	raw, err := encode(s, "")
	return bytes.TrimSuffix(raw, []byte("\n")), err
}

// checkJSON refuses text that is not valid UTF-8, then text that is not valid JSON, with the decoder's message.
func checkJSON(raw []byte) error {
	if err := checkText(raw); err != nil {
		return err
	}
	if json.Valid(raw) { // syntax, trailing data, nesting limit
		return nil
	}
	var v any
	err := json.Unmarshal(raw, &v)
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("%w (at byte %d)", se, se.Offset)
	}
	return err // a depth error; Unmarshal always errors when Valid is false
}

// hex4At parses four hex digits (either case) at raw[i:i+4].
func hex4At(raw []byte, i int) (rune, bool) {
	if i+4 > len(raw) {
		return 0, false
	}
	r, err := strconv.ParseUint(string(raw[i:i+4]), 16, 16)
	return rune(r), err == nil
}

// checkText refuses invalid UTF-8 and every surrogate escape that is not a proper high+low pair. A proper pair
// decodes to the right rune and is allowed; a lone one (such as \udcNN) would decode to U+FFFD and merge distinct names.
func checkText(raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrNotUTF8
	}
	inStr := false
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case !inStr:
			inStr = c == '"'
		case c == '"':
			inStr = false
		case c == '\\' && i+1 < len(raw):
			if raw[i+1] != 'u' {
				i++ // \" \\ \n ...: skip the escaped byte so \" does not end the string
				continue
			}
			r, ok := hex4At(raw, i+2)
			if !ok {
				return nil // json.Valid reports the malformed escape with its own message
			}
			switch {
			case r >= 0xdc00 && r <= 0xdfff:
				return ErrNotUTF8 // low surrogate with no high before it
			case r >= 0xd800 && r <= 0xdbff:
				if i+11 < len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' {
					if lo, ok := hex4At(raw, i+8); ok && lo >= 0xdc00 && lo <= 0xdfff {
						i += 11 // a proper pair: both escapes consumed
						continue
					}
				}
				return ErrNotUTF8 // high surrogate with a bad or missing partner
			}
			i += 5 // \uXXXX consumed (the loop adds the last 1)
		}
	}
	return nil
}
