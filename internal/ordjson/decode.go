package ordjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ErrNotUTF8 is the refusal for text that is not valid UTF-8. encoding/json would turn it into U+FFFD, so two
// different file names could become one; nothing that is not valid UTF-8 passes through JSON.
var ErrNotUTF8 = errors.New("contains text that is not valid UTF-8 (invalid bytes or a lone surrogate escape)")

// hex4At parses four hex digits (either case) at raw[i:i+4].
func hex4At(raw []byte, i int) (rune, bool) {
	if i+4 > len(raw) {
		return 0, false
	}
	var r rune
	for _, c := range raw[i : i+4] {
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			c = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | rune(c)
	}
	return r, true
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

// Decode reads JSON into nil | bool | string | json.Number | []any | *Object. Numbers keep the text they were
// written with (2 stays 2, 2.0 stays 2.0). Object keys keep their order; a duplicate key keeps its first position
// and takes the last value. Invalid UTF-8 and lone surrogate escapes are refused (ErrNotUTF8); NaN, Infinity and a
// leading byte order mark are syntax errors.
func Decode(raw []byte) (Value, error) {
	if err := checkText(raw); err != nil {
		return nil, err
	}
	if !json.Valid(raw) { // syntax, trailing data, nesting limit
		var v any
		err := json.Unmarshal(raw, &v)
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return nil, fmt.Errorf("%w (at byte %d)", se, se.Offset)
		}
		if err != nil {
			return nil, err
		}
		return nil, errors.New("invalid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return readValue(dec)
}

// Parse is Decode of a string.
func Parse(s string) (Value, error) { return Decode([]byte(s)) }

func readValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil // string, json.Number, bool, nil
	}
	if d == '{' {
		o := &Object{Vals: map[string]Value{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			o.Set(kt.(string), v)
		}
		_, err := dec.Token()
		return o, err
	}
	arr := []Value{}
	for dec.More() {
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	_, err = dec.Token()
	return arr, err
}
