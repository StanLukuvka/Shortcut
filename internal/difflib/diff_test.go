package difflib

import (
	"encoding/json"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type diffCase struct {
	Name  string            `json:"name"`
	Old   map[string]string `json:"old"`
	New   map[string]string `json:"new"`
	Lines []string          `json:"lines"`
}

func loadCases(t *testing.T) []diffCase {
	t.Helper()
	raw := testkit.Golden(t, "diff_cases.json")
	var f struct {
		Cases []diffCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	return f.Cases
}

const binaryMark = "\x00binary:"

// diffFiles ports helpers.py diff_files so the corpus can be checked end to end.
func diffFiles(old, new map[string]string) []string {
	paths := map[string]bool{}
	for p := range old {
		paths[p] = true
	}
	for p := range new {
		paths[p] = true
	}
	var sorted []string
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted) // lazy: byte order equals Python's code point order for UTF-8; fine for the corpus
	lines := []string{}
	for _, path := range sorted {
		before, inOld := old[path]
		after, inNew := new[path]
		if inOld && inNew && before == after {
			continue
		}
		if strings.HasPrefix(after, binaryMark) {
			lines = append(lines, "Binary file "+path+" changed")
			continue
		}
		if strings.HasPrefix(before, binaryMark) {
			before = ""
		}
		shown := strings.TrimPrefix(path, "./")
		changed := Unified(SplitLinesKeepEnds(before), SplitLinesKeepEnds(after), "a/"+shown, "b/"+shown)
		for _, l := range changed {
			lines = append(lines, strings.TrimRight(l, "\r\n"))
		}
		if len(changed) == 0 {
			lines = append(lines, "--- a/"+shown, "+++ b/"+shown)
		}
	}
	return lines
}

func TestDifflib_GoldenCases(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			got := diffFiles(c.Old, c.New)
			if !reflect.DeepEqual(got, c.Lines) && !(len(got) == 0 && len(c.Lines) == 0) {
				t.Errorf("got  %q\nwant %q", got, c.Lines)
			}
		})
	}
}

func TestDifflib_SplitLinesKeepEnds(t *testing.T) {
	cases := map[string][]string{
		"":                       nil,
		"a":                      {"a"},
		"a\n":                    {"a\n"},
		"a\r\nb":                 {"a\r\n", "b"},
		"a\r\rb\r\n\n":           {"a\r", "\r", "b\r\n", "\n"},
		"a\vb\fc\x1cd\x1de\x1ef": {"a\v", "b\f", "c\x1c", "d\x1d", "e\x1e", "f"},
		"a\u0085b\u2028c\u2029d": {"a\u0085", "b\u2028", "c\u2029", "d"},
		"\u00e9\u0085x":          {"\u00e9\u0085", "x"},
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
	for _, c := range loadCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			for path := range c.Old {
				if _, ok := c.New[path]; !ok {
					c.New[path] = ""
				}
			}
			for path, after := range c.New {
				before := c.Old[path]
				a, b := SplitLinesKeepEnds(before), SplitLinesKeepEnds(after)
				d := Unified(a, b, "a", "b")
				got, msg := apply(a, d)
				if msg != "" {
					t.Fatalf("%s: %s", path, msg)
				}
				if len(got) != len(b) || (len(b) > 0 && !reflect.DeepEqual(got, b)) {
					t.Errorf("%s: applying the diff gave %q, want %q", path, got, b)
				}
			}
		})
	}
}
