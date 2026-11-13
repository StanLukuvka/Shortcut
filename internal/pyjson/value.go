// Package pyjson is an ordered, number-preserving JSON value model on encoding/json: the decoder keeps key order
// and number text, and refuses text that is not valid UTF-8, on the way in and on the way out.
//
// Strings are Go strings holding valid UTF-8. A string from the old Python-written store that held a lone surrogate
// escape (a non-UTF-8 file name) is refused when it is read.
package pyjson

import "slices"

// Value is nil | bool | string | json.Number | []any | *Object.
type Value = any

// Object keeps keys in first-insertion order; a duplicate key keeps its first position and takes the last value.
type Object struct {
	Keys []string
	Vals map[string]Value
}

func (o *Object) Get(k string) (Value, bool) {
	v, ok := o.Vals[k]
	return v, ok
}

// Set appends the key if it is new.
func (o *Object) Set(k string, v Value) {
	if o.Vals == nil {
		o.Vals = map[string]Value{}
	}
	if _, ok := o.Vals[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Vals[k] = v
}

func (o *Object) Delete(k string) {
	if _, ok := o.Vals[k]; !ok {
		return
	}
	delete(o.Vals, k)
	for i, key := range o.Keys {
		if key == k {
			o.Keys = append(o.Keys[:i], o.Keys[i+1:]...)
			return
		}
	}
}

// Sorted returns a copy with the keys in order, at every depth, through objects and arrays.
func (o *Object) Sorted() *Object {
	out := &Object{Vals: make(map[string]Value, len(o.Keys))}
	keys := slices.Clone(o.Keys)
	slices.Sort(keys)
	for _, k := range keys {
		out.Set(k, sortedValue(o.Vals[k]))
	}
	return out
}

func sortedValue(v Value) Value {
	switch t := v.(type) {
	case *Object:
		return t.Sorted()
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = sortedValue(e)
		}
		return out
	}
	return v
}
