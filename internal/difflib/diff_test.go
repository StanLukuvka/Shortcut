package difflib

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type diffRow struct {
	name, old, new string
	want           []string
}

var hdr = []string{"--- a/f", "+++ b/f"}

func rows() []diffRow {
	many := func(change bool) string {
		var b strings.Builder
		for i := 0; i < 300; i++ {
			switch {
			case change && i == 150:
				b.WriteString("changed\n")
			case i%2 == 0:
				b.WriteString("same\n")
			default:
				b.WriteString("line " + strconv.Itoa(i%7) + "\n")
			}
		}
		return b.String()
	}
	return []diffRow{
		{"identical", "a\nb\n", "a\nb\n", nil},
		{"add", "a\nb\n", "a\nb\nc\n", []string{"@@ -1,2 +1,3 @@", " a", " b", "+c"}},
		{"delete", "a\nb\nc\n", "a\nc\n", []string{"@@ -1,3 +1,2 @@", " a", "-b", " c"}},
		{"change", "a\nb\nc\n", "a\nX\nc\n", []string{"@@ -1,3 +1,3 @@", " a", "-b", "+X", " c"}},
		{"empty to text", "", "a\nb\n", []string{"@@ -0,0 +1,2 @@", "+a", "+b"}},
		{"text to empty", "a\nb\n", "", []string{"@@ -1,2 +0,0 @@", "-a", "-b"}},
		{"no trailing newline", "a\nb", "a\nc", []string{"@@ -1,2 +1,2 @@", " a", "-b", "+c"}},
		{"two hunks", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n", "1\nX\n3\n4\n5\n6\n7\n8\n9\n10\n11\nY\n",
			[]string{"@@ -1,5 +1,5 @@", " 1", "-2", "+X", " 3", " 4", " 5", "@@ -9,4 +9,4 @@", " 9", " 10", " 11", "-12", "+Y"}},
		{"form feed stays in its line", "a\fb\nc\n", "a\fX\nc\n", []string{"@@ -1,2 +1,2 @@", "-a\fb", "+a\fX", " c"}},
		{"carriage return stays in its line", "a\rb\nc\n", "a\rX\nc\n", []string{"@@ -1,2 +1,2 @@", "-a\rb", "+a\rX", " c"}},
		{"U+2028 stays in its line", "a\u2028b\n", "a\u2028X\n", []string{"@@ -1 +1 @@", "-a\u2028b", "+a\u2028X"}},
		{"300 lines, one changed, no autojunk", many(false), many(true), nil}, // want checked by the apply test only
	}
}

func TestDifflib_Table(t *testing.T) {
	for _, r := range rows() {
		if r.want == nil && r.name != "identical" {
			continue
		}
		t.Run(r.name, func(t *testing.T) {
			var got []string
			for _, l := range Unified(SplitLinesKeepEnds(r.old), SplitLinesKeepEnds(r.new), "a/f", "b/f") {
				got = append(got, strings.TrimRight(l, "\r\n"))
			}
			want := r.want
			if want != nil {
				want = append(append([]string{}, hdr...), want...)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %q\nwant %q", got, want)
			}
		})
	}
}

func TestDifflib_SplitLinesKeepEnds(t *testing.T) {
	cases := map[string][]string{
		"":               nil,
		"a":              {"a"},
		"a\n":            {"a\n"},
		"a\r\nb":         {"a\r\n", "b"},
		"a\r\rb\r\n\n":   {"a\r\rb\r\n", "\n"},
		"a\fb\vc\u2028d": {"a\fb\vc\u2028d"},
		"\n\n":           {"\n", "\n"},
	}
	for in, want := range cases {
		if got := SplitLinesKeepEnds(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// apply replays a Unified diff on old and returns the new lines, or an error
// message when the diff does not fit old.
func apply(old, diff []string) ([]string, string) {
	var out []string
	pos := 0 // next unread index of old
	for i := 0; i < len(diff); {
		if !strings.HasPrefix(diff[i], "--- ") || i+2 >= len(diff) || !strings.HasPrefix(diff[i+1], "+++ ") {
			return nil, "bad header at " + strconv.Itoa(i)
		}
		i += 2
		for i < len(diff) && strings.HasPrefix(diff[i], "@@ ") {
			f := strings.Fields(diff[i]) // @@ -a[,b] +c[,d] @@
			i++
			start, n := rng(f[1][1:])
			_, m := rng(f[2][1:])
			if n > 0 {
				start--
			}
			if start < pos || start > len(old) {
				return nil, "hunk start out of order"
			}
			out = append(out, old[pos:start]...)
			pos = start
			for ; n > 0 || m > 0; i++ {
				if i >= len(diff) || diff[i] == "" {
					return nil, "hunk too short"
				}
				switch l := diff[i][1:]; diff[i][0] {
				case ' ', '-':
					if pos >= len(old) || old[pos] != l {
						return nil, "context/removal does not match old"
					}
					pos++
					n--
					if diff[i][0] == ' ' {
						out = append(out, l)
						m--
					}
				case '+':
					out = append(out, l)
					m--
				default:
					return nil, "bad line prefix"
				}
			}
		}
	}
	return append(out, old[pos:]...), ""
}

func rng(s string) (start, length int) {
	a, b, ok := strings.Cut(s, ",")
	start, _ = strconv.Atoi(a)
	length = 1
	if ok {
		length, _ = strconv.Atoi(b)
	}
	return
}

func TestDifflib_DiffAppliedToOldGivesNew(t *testing.T) {
	for _, r := range rows() {
		t.Run(r.name, func(t *testing.T) {
			a, b := SplitLinesKeepEnds(r.old), SplitLinesKeepEnds(r.new)
			got, msg := apply(a, Unified(a, b, "a", "b"))
			if msg != "" {
				t.Fatal(msg)
			}
			if len(got) != len(b) || (len(b) > 0 && !reflect.DeepEqual(got, b)) {
				t.Errorf("applying the diff gave %q, want %q", got, b)
			}
		})
	}
}
