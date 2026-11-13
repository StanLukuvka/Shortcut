package pyos

import "testing"

func TestSuffix(t *testing.T) { // pathlib.PurePath.suffix, Python 3.14
	for in, want := range map[string]string{
		"a.sh": ".sh", "dir/a.tar.gz": ".gz", ".bashrc": "", "a": "", "": "", "..": "", "x/..": "",
		"a.": ".", "dir/a.": ".", "a..": ".", "dir.d/a": "", "dir.d/": ".d", "bin/tool": "", "a/b.sh/.": ".sh",
	} {
		if got := Suffix(in); got != want {
			t.Errorf("Suffix(%q) = %q, want %q", in, got, want)
		}
	}
}
