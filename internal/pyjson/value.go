// Package pyjson reproduces what Python's json module does with the files shortcut reads and writes: an ordered,
// number-preserving value model, a decoder with Python's grammar and error text, and an encoder with Python's
// formatting. It does not import encoding/json.
//
// Strings are Go strings holding WTF-8: valid UTF-8, except that a lone surrogate U+D800..U+DFFF is kept as its
// three-byte form ED A0..BF xx, as Python's str can hold one.
package pyjson

// Value is nil | bool | string | *big.Int | float64 | []Value | *Object.
type Value any

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
