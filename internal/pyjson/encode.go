package pyjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// validate refuses a string, in a key or a value, that is not valid UTF-8: encoding/json would write U+FFFD for the
// bad bytes, and two different names would become one.
func validate(v Value) error {
	switch t := v.(type) {
	case string:
		if !utf8.ValidString(t) {
			return fmt.Errorf("%w: %q", ErrNotUTF8, t)
		}
	case []any:
		for _, e := range t {
			if err := validate(e); err != nil {
				return err
			}
		}
	case *Object:
		for _, k := range t.Keys {
			if !utf8.ValidString(k) {
				return fmt.Errorf("%w: key %q", ErrNotUTF8, k)
			}
			if err := validate(t.Vals[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

// MarshalJSON writes the object in key order. It validates first, so it is also safe through encoding/json.
func (o *Object) MarshalJSON() ([]byte, error) {
	if err := validate(o); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := encodeNoEscape(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := encodeNoEscape(o.Vals[k]) // a nested *Object comes back through MarshalJSON
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// encodeNoEscape is json.Marshal without the HTML escaping of < > &, which encoding/json does by default.
func encodeNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// Marshal is v on one line. A json.Number is written as the text it was read with; non-ASCII is written as the
// character, not \uXXXX; a string that is not valid UTF-8 is an error (ErrNotUTF8).
func Marshal(v Value) ([]byte, error) {
	if err := validate(v); err != nil {
		return nil, err
	}
	return encodeNoEscape(v)
}

// MarshalIndent is Marshal with two-space indentation. It does not use json.MarshalIndent, which would escape < > & again.
func MarshalIndent(v Value) ([]byte, error) {
	raw, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
