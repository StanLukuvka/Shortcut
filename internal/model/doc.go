package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

// ErrNotUTF8 is the refusal for text that is not valid UTF-8. encoding/json would turn it into U+FFFD, so two
// different file names could become one; nothing that is not valid UTF-8 passes through JSON.
var ErrNotUTF8 = errors.New("contains text that is not valid UTF-8 (invalid bytes or a lone surrogate escape)")

var errNotObject = errors.New("root must be a JSON object")

// Doc is one JSON object as written: keys in first-seen order, each value as compact text with strings as
// characters, numbers as written and every nested object's keys in first-seen order. The zero value is the empty
// object. Object returns a copy; to change a nested object, change the copy and write it back with Set.
type Doc struct {
	Keys []string
	Raw  map[string][]byte
}

// ParseDoc reads a JSON object. Text that is not valid UTF-8 is refused (ErrNotUTF8). A repeated key keeps its
// first place and takes the last value.
func ParseDoc(text []byte) (*Doc, error) {
	if err := checkJSON(text); err != nil {
		return nil, err
	}
	d, ok := objectOf(bytes.TrimLeft(text, " \t\r\n"))
	if !ok {
		return nil, errNotObject
	}
	return d, nil
}

// objectOf reads text that is already known to be valid JSON, if it is an object.
func objectOf(text []byte) (*Doc, bool) {
	if len(text) == 0 || text[0] != '{' {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	dec.Token() // '{'
	d, err := readMembers(dec)
	return d, err == nil
}

// readMembers reads the rest of an object whose '{' is consumed.
func readMembers(dec *json.Decoder) (*Doc, error) {
	d := &Doc{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		d.Set(k.(string), v)
	}
	_, err := dec.Token() // '}'
	return d, err
}

// readValue returns the next value as compact text. The text is valid UTF-8 (checkJSON), so nothing here refuses it.
func readValue(dec *json.Decoder) ([]byte, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			d, err := readMembers(dec)
			if err != nil {
				return nil, err
			}
			return d.Bytes()
		}
		out := []byte{'['}
		for dec.More() {
			v, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			if len(out) > 1 {
				out = append(out, ',')
			}
			out = append(out, v...)
		}
		_, err = dec.Token() // ']'
		return append(out, ']'), err
	case json.Number:
		return []byte(t), nil
	case string:
		return quote(t)
	}
	return json.Marshal(tok) // true, false, null
}

func (d *Doc) Has(k string) bool {
	_, ok := d.Raw[k]
	return ok
}

// Set appends a new key; a known key keeps its place.
func (d *Doc) Set(k string, raw []byte) {
	if d.Raw == nil {
		d.Raw = map[string][]byte{}
	}
	if _, ok := d.Raw[k]; !ok {
		d.Keys = append(d.Keys, k)
	}
	d.Raw[k] = raw
}

func (d *Doc) SetString(k, s string) error {
	raw, err := quote(s)
	if err != nil {
		return err
	}
	d.Set(k, raw)
	return nil
}

func (d *Doc) Delete(k string) {
	if i := slices.Index(d.Keys, k); i >= 0 {
		delete(d.Raw, k)
		d.Keys = slices.Delete(d.Keys, i, i+1)
	}
}

// Object is the value of k when it is an object; otherwise an empty Doc and false.
func (d *Doc) Object(k string) (*Doc, bool) {
	if o, ok := objectOf(d.Raw[k]); ok {
		return o, true
	}
	return &Doc{}, false
}

// String is the value of k when it is a string.
func (d *Doc) String(k string) (string, bool) {
	raw := d.Raw[k]
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// Bytes is the object on one line. A key or value that is not valid UTF-8 is an error (ErrNotUTF8).
func (d *Doc) Bytes() ([]byte, error) {
	out := []byte{'{'}
	for i, k := range d.Keys {
		if !utf8.ValidString(k) {
			return nil, fmt.Errorf("%w: key %q", ErrNotUTF8, k)
		}
		if !utf8.Valid(d.Raw[k]) {
			return nil, fmt.Errorf("%w: %q", ErrNotUTF8, d.Raw[k])
		}
		if i > 0 {
			out = append(out, ',')
		}
		kb, _ := quote(k)
		out = append(append(append(out, kb...), ':'), d.Raw[k]...)
	}
	return append(out, '}'), nil
}

// Pretty is Bytes with two-space indentation, as a store file is written (without the final newline).
func (d *Doc) Pretty() ([]byte, error) {
	raw, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	return indent(raw)
}

// Normal is what approval hashes: the object with its keys sorted at every depth, two-space indentation and a
// final newline. Formatting and key order in the file do not change it.
func (d *Doc) Normal() ([]byte, error) {
	raw, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	text, err := encode(v) // maps write their keys sorted
	if err != nil {
		return nil, err
	}
	text, err = indent(text)
	return append(text, '\n'), err
}

// indent is json.Indent with two spaces. Unlike json.MarshalIndent it does not escape < > & again.
func indent(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	err := json.Indent(&out, raw, "", "  ")
	return out.Bytes(), err
}

// encode is json.Marshal without the escaping of < > & that encoding/json does by default.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// quote is s as a JSON string; text that is not valid UTF-8 is an error (ErrNotUTF8).
func quote(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, fmt.Errorf("%w: %q", ErrNotUTF8, s)
	}
	return encode(s)
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
