package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrNotUTF8 is the refusal for a file that is not valid UTF-8, the one check on read. Approval hashes a file's bytes, so
// what encoding/json makes of an escape such as \udcNN (U+FFFD) cannot hide a change.
var ErrNotUTF8 = errors.New("contains text that is not valid UTF-8")

var errNotObject = errors.New("root must be a JSON object")

// Doc is one JSON object: each value is its raw text, as it was read. The zero value reads as the empty object;
// write to a Doc{}. Files are written with keys sorted. Object returns a copy; to change a nested object, change
// the copy and write it back with SetString or Bytes.
type Doc map[string]json.RawMessage

// ParseDoc reads a JSON object. A repeated key takes the last value.
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

func (d Doc) SetString(k, s string) { d[k] = quote(s) }

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

// Bytes is the object on one line, keys sorted.
func (d Doc) Bytes() ([]byte, error) {
	out, err := encode(d, "")
	return bytes.TrimSuffix(out, []byte("\n")), err
}

// Pretty is the object as a store file is written: two-space indentation, keys sorted, a final newline.
func (d Doc) Pretty() ([]byte, error) { return encode(d, "  ") }

// encode is json.Marshal without the escaping of < > & that encoding/json does by default, with a final newline.
func encode(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	err := enc.Encode(v)
	return b.Bytes(), err
}

// quote is s as a JSON string. Bytes that are not valid UTF-8 become U+FFFD.
func quote(s string) []byte {
	raw, _ := encode(s, "") // a string always encodes
	return bytes.TrimSuffix(raw, []byte("\n"))
}

// checkJSON refuses text that is not valid UTF-8, then text that is not valid JSON, with the decoder's message.
func checkJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrNotUTF8
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
