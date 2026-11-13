// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-1). Don't hand-edit without checking the plan.

package pyjson

import "unicode/utf8"

// Less orders two path strings as Python orders the str values os.fsdecode makes of them:
// by code point, with each invalid UTF-8 byte 0xNN read as U+DC00+NN (surrogateescape).
func Less(a, b string) bool {
	for a != "" && b != "" {
		ra, na := pyRune(a)
		rb, nb := pyRune(b)
		if ra != rb {
			return ra < rb
		}
		a, b = a[na:], b[nb:]
	}
	return a == "" && b != ""
}

// pyRune reads one code point as a Python str would hold it: UTF-8, or an invalid byte 0xNN as U+DC00+NN. A WTF-8
// triple for a lone surrogate other than U+DC80..U+DCFF (ED A0 80 .. ED B1 BF and ED B4 80 .. ED BF BF; what Decode
// stores for a lone \udXXX escape) is read as that surrogate. U+DC80..U+DCFF never appear as triples: Decode turns
// them into the raw byte, so ED B2 xx and ED B3 xx are just invalid bytes.
// lazy: a raw path holding bytes ED A0..B1 xx or ED B4..BF xx is read as a surrogate where Python would escape three
// bytes; such a path is not valid UTF-8 and never a real file name in practice. Upgrade: a distinct string type for
// decoded text.
func pyRune(s string) (rune, int) {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && n == 1 {
		if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] != 0xB2 && s[1] != 0xB3 && s[2]&0xC0 == 0x80 {
			return 0xD000 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), 3
		}
		return 0xDC00 + rune(s[0]), 1
	}
	return r, n
}
