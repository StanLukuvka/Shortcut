package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// golden reads ../testdata/approval.json with pyjson (it holds lone-surrogate escapes, which encoding/json garbles).
func golden(t *testing.T, set string) []*pyjson.Object {
	t.Helper()
	raw := testkit.Golden(t, "approval.json")
	v, err := pyjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	cases, ok := v.(*pyjson.Object).Get(set)
	if !ok {
		t.Fatalf("approval.json has no %s", set)
	}
	var out []*pyjson.Object
	for _, c := range cases.([]pyjson.Value) {
		out = append(out, c.(*pyjson.Object))
	}
	return out
}

func str(o *pyjson.Object, k string) string {
	s, _ := o.Vals[k].(string)
	return s
}

func strMap(t *testing.T, v pyjson.Value) map[string]string {
	t.Helper()
	m, ok := stringMap(v)
	if !ok {
		t.Fatalf("not a map of strings: %v", v)
	}
	return m
}

func sameJSON(a, b pyjson.Value) bool {
	switch x := a.(type) {
	case *pyjson.Object:
		y, ok := b.(*pyjson.Object)
		if !ok || len(x.Keys) != len(y.Keys) {
			return false
		}
		for _, k := range x.Keys {
			yv, has := y.Vals[k]
			if !has || !sameJSON(x.Vals[k], yv) {
				return false
			}
		}
		return true
	case []pyjson.Value:
		y, ok := b.([]pyjson.Value)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return pyjson.DumpsCompact(a) == pyjson.DumpsCompact(b)
}

func TestApproval_GoldenReviewable(t *testing.T) {
	cases := golden(t, "reviewable_cases")
	for i, c := range cases {
		raw, err := hex.DecodeString(str(c, "raw_hex"))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := Reviewable(raw), str(c, "expected"); got != want {
			t.Errorf("case %d (%x): got %q want %q", i, raw, got, want)
		}
	}
	if len(cases) < 40 {
		t.Errorf("only %d cases", len(cases))
	}
	t.Logf("%d reviewable cases", len(cases))
}

func TestApproval_GoldenKeysAndRecordNames(t *testing.T) {
	if pyos.Realpath("/tmp") != "/tmp" {
		t.Skip("/tmp is a link here; the golden keys name /tmp paths")
	}
	cases := golden(t, "key_cases")
	for _, c := range cases {
		store, name := str(c, "store"), str(c, "name")
		g := model.Group{Name: name, File: str(c, "file"), StoreDir: store}
		if str(c, "kind") == "project" {
			g = model.Group{StoreDir: store, File: store + "/.shortcuts", LocalFile: store + "/.shortcuts.local"}
		}
		t.Run(str(c, "kind")+" "+store+" "+name, func(t *testing.T) {
			if got := g.Key(); got != str(c, "key") {
				t.Errorf("key %q, want %q", got, str(c, "key"))
			}
			want := str(c, "record_name")
			if _, python := c.Get("python_error"); python {
				// Python's key.encode() raises here; Go names the record after the key's raw bytes (os.fsencode).
				if utf8.ValidString(g.Key()) {
					t.Fatal("python_error on a valid key")
				}
				sum := sha256.Sum256([]byte(str(c, "key")))
				want = hex.EncodeToString(sum[:]) + ".json"
			}
			if want == "" {
				t.Fatal("no record name")
			}
			if got := model.RecordName(g.Key()); got != want {
				t.Errorf("record name %s, want %s", got, want)
			}
		})
	}
	if len(cases) < 12 {
		t.Errorf("only %d cases", len(cases))
	}
	t.Logf("%d key cases", len(cases))
}

func TestApproval_ApprovalPathIsInTheGlobalStore(t *testing.T) {
	env := testkit.New(t)
	g := model.Group{StoreDir: env.Home, File: env.Home + "/.shortcuts", LocalFile: env.Home + "/.shortcuts.local"}
	if got, want := model.ApprovalPath(g), filepath.Join(env.Store, "approvals", model.RecordName(env.Home)); got != want {
		t.Errorf("%s, want %s", got, want)
	}
}

// buildTree writes a golden tree under root. It skips the test on a filesystem that refuses non-UTF-8 names.
func buildTree(t *testing.T, env *testkit.Env, root string, tree []pyjson.Value) {
	t.Helper()
	for _, item := range tree {
		e := item.(*pyjson.Object)
		if p := str(e, "path"); !utf8.ValidString(p) {
			probe := filepath.Join(env.Tmp, "probe\xff")
			if err := os.WriteFile(probe, nil, 0o644); err != nil {
				t.Skipf("this filesystem refuses non-UTF-8 file names: %v", err)
			}
			os.Remove(probe)
		}
		path := filepath.Join(root, str(e, "path"))
		switch typ := str(e, "type"); typ {
		case "file":
			var content any = str(e, "content")
			if h, ok := e.Get("content_hex"); ok {
				b, err := hex.DecodeString(h.(string))
				if err != nil {
					t.Fatal(err)
				}
				content = b
			}
			mode, err := strconv.ParseUint(strings.TrimPrefix(str(e, "mode"), "0o"), 8, 32)
			if err != nil {
				t.Fatal(err)
			}
			env.WriteFile(path, content, os.FileMode(mode))
		case "symlink":
			env.Symlink(str(e, "target"), path)
		case "dir":
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("tree entry type %q", typ)
		}
	}
}

// afterShortcut cuts a file error back to the part after the file's name, since the golden root belongs to the frozen data (generated once from the Python version at the python-final commit).
func afterShortcut(s string) string {
	if i := strings.Index(s, ".short"); i >= 0 {
		return s[i:]
	}
	return s
}

func TestApproval_GoldenTrees(t *testing.T) {
	cases := golden(t, "tree_cases")
	groups, records := 0, 0
	for _, c := range cases {
		t.Run(str(c, "name"), func(t *testing.T) {
			env := testkit.New(t)
			root := env.Folder("tree")
			buildTree(t, env, root, c.Vals["tree"].([]pyjson.Value))
			store := model.LoadStore(root)
			want := c.Vals["groups"].([]pyjson.Value)
			got := store.Groups()
			if len(got) != len(want) {
				t.Fatalf("%d groups, want %d", len(got), len(want))
			}
			if wp := c.Vals["problems"].([]pyjson.Value); len(store.Problems) != len(wp) {
				t.Errorf("problems %q, want %v", store.Problems, wp)
			}
			for i, g := range got {
				w := want[i].(*pyjson.Object)
				groups++
				if g.Name != str(w, "name") {
					t.Fatalf("group %d is %q, want %q", i, g.Name, str(w, "name"))
				}
				if wantErr := w.Vals["error"]; (g.Error == nil) != (wantErr == nil) ||
					(g.Error != nil && afterShortcut(*g.Error) != afterShortcut(wantErr.(string))) {
					t.Errorf("error %v, want %v", g.Error, wantErr)
				}
				shared, local := SharedFiles(g), LocalFiles(g)
				if wantShared := strMap(t, w.Vals["shared_files"]); !equalMaps(shared, wantShared) {
					t.Errorf("shared files\n got %q\nwant %q", shared, wantShared)
				}
				if wantLocal := strMap(t, w.Vals["local_files"]); !equalMaps(local, wantLocal) {
					t.Errorf("local files\n got %q\nwant %q", local, wantLocal)
				}
				if got, want := g.Key(), strings.ReplaceAll(str(w, "key_template"), "@ROOT@", root); got != want {
					t.Errorf("key %q, want %q", got, want)
				}
				records++
				checkRecord(t, env, g, root, str(w, "record_template"), shared, local)
			}
		})
	}
	t.Logf("%d trees, %d groups, %d records", len(cases), groups, records)
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// checkRecord: a record Python wrote reads as ok; one Go writes parses to the same object as Python's.
func checkRecord(t *testing.T, env *testkit.Env, g model.Group, root, template string, shared, local map[string]string) {
	t.Helper()
	text := strings.ReplaceAll(template, "@ROOT@", root)
	want, err := pyjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	path := model.ApprovalPath(g)
	if State(g) != "new" {
		t.Errorf("before any record: %s", State(g))
	}
	env.WriteFile(path, text, 0)
	if got := State(g); got != "ok" {
		t.Errorf("a Python-written record: state %s, want ok", got)
	}
	if r := Load(g); r == nil || !equalMaps(r.Snapshot, shared) || !equalMaps(r.Local, local) || r.Key != g.Key() {
		t.Errorf("Load: %+v", r)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Save(g, shared, local); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pyjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(got, want) {
		t.Errorf("Go-written record\n%s\ndiffers from Python's\n%s", raw, text)
	}
	if State(g) != "ok" {
		t.Errorf("a Go-written record: state %s", State(g))
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("record mode %v", st.Mode().Perm())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestApproval_NonUTF8ProjectPathIsHashedByItsRawBytes(t *testing.T) {
	env := testkit.New(t)
	probe := filepath.Join(env.Tmp, "caf\xe9")
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	env.WriteStore(filepath.Join(probe, ".shortcuts"), map[string]any{"a": map[string]any{"inline": "x"}})
	g := model.LoadStore(probe).Main
	if g.Key() != probe {
		t.Fatalf("key %q", g.Key())
	}
	sum := sha256.Sum256([]byte(probe))
	if got := filepath.Base(model.ApprovalPath(g)); got != hex.EncodeToString(sum[:])+".json" {
		t.Errorf("record %s", got)
	}
	shared, local := SharedFiles(g), LocalFiles(g)
	if err := Save(g, shared, local); err != nil {
		t.Fatal(err)
	}
	if State(g) != "ok" {
		t.Errorf("state %s", State(g))
	}
	// the key is written as Python's json.dumps writes it: the byte as a lone surrogate
	raw, _ := os.ReadFile(model.ApprovalPath(g))
	if !strings.Contains(string(raw), `caf\udce9"`) {
		t.Errorf("record: %s", raw)
	}
}

func TestApproval_StateAndRequire(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	load := func() model.Group { return model.LoadStore(proj).Main }
	g := load()
	if State(g) != "new" {
		t.Fatal(State(g))
	}
	if err := Require(g); err == nil || err.Error() != g.Key()+" isn't approved yet; review it with: shortcut update" {
		t.Error(err)
	}
	if err := Save(g, SharedFiles(g), LocalFiles(g)); err != nil {
		t.Fatal(err)
	}
	if State(g) != "ok" || Require(g) != nil {
		t.Fatal("not ok after Save")
	}
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo two\n", 0)
	g = load()
	if State(g) != "changed" {
		t.Errorf("after a script change: %s", State(g))
	}
	if err := Require(g); err == nil || err.Error() != g.Key()+" changed since you approved it; run: shortcut update" {
		t.Error(err)
	}
	// a .shortcuts.local appearing changes the local half
	if err := Save(g, SharedFiles(g), LocalFiles(g)); err != nil {
		t.Fatal(err)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"m": {"inline": "x"}}}`, 0)
	g = load()
	if State(g) != "changed" || LocalIsApproved(g, Load(g)) {
		t.Errorf("after adding .shortcuts.local: %s", State(g))
	}
	// a record with no snapshot (only CLI writes to .local were recorded) reads as new, not changed
	if err := Save(g, map[string]string{}, LocalFiles(g)); err != nil {
		t.Fatal(err)
	}
	if State(g) != "new" {
		t.Errorf("empty snapshot: %s", State(g))
	}
	// the global store never needs approval
	if gl := model.LoadStore(env.Store).Main; State(gl) != "ok" || Require(gl) != nil {
		t.Error("global store")
	}
}

func TestApproval_LoadRefusesWhatIsNotARecord(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{})
	g := model.LoadStore(proj).Main
	path := model.ApprovalPath(g)
	for _, bad := range []string{`{not json`, `[]`, `{"hash": "x"}`, `{"snapshot": []}`, `{"snapshot": {"a": 1}}`, `{"snapshot": {}, "local": []}`} {
		env.WriteFile(path, bad, 0)
		if r := Load(g); r != nil {
			t.Errorf("%s loaded as %+v", bad, r)
		}
		if State(g) != "new" {
			t.Errorf("%s: state %s", bad, State(g))
		}
	}
	env.WriteFile(path, `{"snapshot": {}}`, 0) // an old record with no "local" and no hash
	if r := Load(g); r == nil || len(r.Local) != 0 {
		t.Errorf("%+v", r)
	}
	env.WriteFile(path, "{\r\n\"snapshot\": {}\r\n}", 0)
	if Load(g) == nil {
		t.Error("CRLF record")
	}
}

func TestApproval_EntryFilesEdges(t *testing.T) {
	env := testkit.New(t)
	elsewhere := env.WriteFile(filepath.Join(env.Tmp, "outside.sh"), "echo out\n", 0o755)
	proj := env.Project(map[string]any{
		"abs":    map[string]any{"script": elsewhere},
		"nul":    map[string]any{"inline": "x", "cwd": "a\x00b"},
		"home":   map[string]any{"inline": "x", "cwd": "~/h"},
		"caller": map[string]any{"inline": "x", "cwd": "caller"},
		"rel":    map[string]any{"inline": "x", "cwd": "sub/../work"},
		"fifo":   map[string]any{"script": "pipe"},
	}, testkit.Dirs("work"))
	g := model.LoadStore(proj).Main
	files := SharedFiles(g)
	if files[elsewhere] != "echo out\n" { // an absolute script is used as given, so its key is the absolute path
		t.Errorf("absolute script: %q", files)
	}
	if got := files["./cwd of rel"]; got != filepath.Join(proj, "work")+"\n" {
		t.Errorf("rel cwd: %q", got)
	}
	for k := range files {
		if k != ".shortcuts" && k != elsewhere && k != "./cwd of rel" {
			t.Errorf("unexpected key %q", k) // not nul, home, caller or the FIFO-less "pipe"
		}
	}
	if len(files) != 3 { // .shortcuts, the absolute script, the cwd
		t.Errorf("%d files: %q", len(files), files)
	}
}

func TestApproval_DisplayPathTreatsADotDotPrefixAsOutside(t *testing.T) {
	g := model.Group{StoreDir: "/p", File: "/p/.shortcuts"}
	for path, want := range map[string]string{"/p/a.sh": "a.sh", "/p/..x/a.sh": "/p/..x/a.sh", "/q/a.sh": "/q/a.sh", "/p/d/../a.sh": "a.sh"} {
		if got := displayPath(g, path); got != want {
			t.Errorf("%s: %s", path, got)
		}
	}
}

func TestApproval_AScriptCannotStandInForACwdRecord(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{
		"go":  map[string]any{"inline": "pwd", "cwd": "work"},
		"pad": map[string]any{"script": "cwd of go"},
	}, testkit.Dirs("safe"), testkit.Files(map[string]any{"cwd of go": "x"}))
	env.Symlink(filepath.Join(proj, "safe"), filepath.Join(proj, "work"))
	store := model.LoadStore(proj)
	files := SharedFiles(store.Main)
	if files["cwd of go"] != "x" {
		t.Errorf("script: %q", files["cwd of go"])
	}
	if want := pyos.Realpath(filepath.Join(proj, "safe")) + "\n"; files["./cwd of go"] != want {
		t.Errorf("cwd record %q, want %q", files["./cwd of go"], want)
	}
}

func TestApproval_DiffDetails(t *testing.T) {
	old := map[string]string{"a.txt": "one\ntwo\n", "gone.txt": "bye\n", "same.txt": "x\n", "bin": binaryMark + "aa"}
	new := map[string]string{"a.txt": "one\nthree\n", "added.txt": "hi\n", "same.txt": "x\n", "bin": binaryMark + "bb"}
	lines := DiffFiles(old, new)
	for _, want := range []string{"--- a/a.txt", "+++ b/a.txt", "-two", "+three", "-bye", "+hi", "Binary file bin changed"} {
		if !slices.Contains(lines, want) {
			t.Errorf("missing %q in %q", want, lines)
		}
	}
	for _, l := range lines {
		if strings.Contains(l, "same.txt") {
			t.Errorf("unchanged file shown: %q", l)
		}
	}
	// A new empty file is still a difference you can see
	if got := DiffFiles(map[string]string{}, map[string]string{"empty": ""}); len(got) == 0 {
		t.Error("an empty file added shows nothing")
	}
	// "./" is dropped from the shown name; lines lose their line ends
	got := DiffFiles(map[string]string{}, map[string]string{"./cwd of x": "/tmp/x\r\n"})
	if strings.Join(got, "|") != "--- a/cwd of x|+++ b/cwd of x|@@ -0,0 +1 @@|+/tmp/x" {
		t.Errorf("%q", got)
	}
}

func TestApproval_ATextFileCannotForgeEitherMark(t *testing.T) {
	bad := []byte("a \xff\n")
	shown := Reviewable(bad)
	if Reviewable([]byte(shown)) == shown {
		t.Error("the shown text of a bad file hashes as the bad file")
	}
	forged := append([]byte(binaryMark), "echo hidden\n"...)
	if got := Reviewable(forged); !strings.HasPrefix(got, binaryMark) || strings.Contains(got, "echo hidden") {
		t.Errorf("forged: %q", got)
	}
	// a plain NUL in a text file is shown, not hidden (the script bash would run stays visible)
	if got := Reviewable([]byte("#!/bin/bash\necho PWNED\n\x00")); !strings.Contains(got, "echo PWNED") || strings.HasPrefix(got, binaryMark) {
		t.Errorf("nul: %q", got)
	}
}

func TestApproval_InvalidUTF8AndItsEscapedTextAreDifferentFiles(t *testing.T) {
	one := Reviewable([]byte("a \xff\n"))
	two := Reviewable([]byte("a \\xff\n"))
	if one == two {
		t.Error("the same text")
	}
	if got := Reviewable([]byte("plain\n")); got != "plain\n" {
		t.Errorf("%q", got)
	}
	if want := "a \\xff\n\n" + invalidMark + "593556958570390666a95c849eaaee0a27455bbe3f9cda2b0a2ea551af93b9e9" + "\n"; one != want {
		t.Errorf("%q", one)
	}
}

func TestApproval_ABinaryReplacingTextSaysSoAndTextReplacingABinaryShows(t *testing.T) {
	lines := DiffFiles(map[string]string{"t": "echo hi\n"}, map[string]string{"t": binaryMark + "aa"})
	if !slices.Contains(lines, "Binary file t changed") {
		t.Errorf("%q", lines)
	}
	lines = DiffFiles(map[string]string{"t": binaryMark + "aa"}, map[string]string{"t": "echo hi\n"})
	if !slices.Contains(lines, "+echo hi") {
		t.Errorf("%q", lines)
	}
}

func TestApproval_FolderFilesNamespace(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{}, testkit.Extra(map[string]any{"imports": map[string]any{"ns": "ns.shortcut"}}))
	env.WriteFile(filepath.Join(proj, "ns.shortcut"), `{"version": 1, "shortcuts": {"go": {"script": "scripts/ns/go.sh"}}}`, 0)
	env.WriteFile(filepath.Join(proj, "scripts", "ns", "go.sh"), "echo go\n", 0o755)
	env.WriteFile(filepath.Join(proj, "scripts", "ns", "sub", "deep.sh"), "echo deep\n", 0)
	env.WriteFile(filepath.Join(proj, "scripts", "other", "x.sh"), "echo other\n", 0)
	env.Symlink("sub", filepath.Join(proj, "scripts", "ns", "dirlink"))
	env.Symlink("nowhere", filepath.Join(proj, "scripts", "ns", "dangling"))
	g, ok := model.LoadStore(proj).Namespace("ns")
	if !ok {
		t.Fatal("no namespace")
	}
	got := folderFiles(g)
	want := map[string]string{
		"scripts/ns/go.sh": "echo go\n", "scripts/ns/sub/deep.sh": "echo deep\n", "scripts/ns/dirlink": "symlink to sub\n",
	}
	if !equalMaps(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// a main group has no scripts folder in this sense
	if len(folderFiles(model.LoadStore(proj).Main)) != 0 {
		t.Error("main group")
	}
	// the namespace's script reaches SharedFiles once, under its real path
	if f := SharedFiles(g); f["scripts/ns/go.sh"] != "echo go\n" || len(f) != 4 {
		t.Errorf("%q", f)
	}
}

// An approval record as an earlier version wrote it, with its "hash" field, hand-written (ASCII only).
const oldRecord = `{
  "key": "@ROOT@",
  "hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "snapshot": {
    ".shortcuts": "{\n  \"shortcuts\": {\n    \"t\": {\n      \"script\": \"bin/t\"\n    }\n  },\n  \"version\": 1\n}\n",
    "bin/t": "echo one\n"
  },
  "local": {}
}
`

func TestApproval_OldRecordWithAHashStillApproves(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	g := model.LoadStore(proj).Main
	rec := model.ApprovalPath(g)
	env.WriteFile(rec, strings.ReplaceAll(oldRecord, "@ROOT@", proj), 0)
	if got := State(g); got != "ok" {
		t.Fatalf("unchanged files: %s", got)
	}
	// the hash is ignored: a record whose hash is not even a string is just as good
	env.WriteFile(rec, strings.Replace(strings.ReplaceAll(oldRecord, "@ROOT@", proj), `"hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`, `"hash": 7`, 1), 0)
	if got := State(g); got != "ok" {
		t.Errorf("non-string hash: %s", got)
	}
	env.WriteFile(rec, strings.ReplaceAll(oldRecord, "@ROOT@", proj), 0)
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo two\n", 0)
	if got := State(model.LoadStore(proj).Main); got != "changed" {
		t.Errorf("text changed: %s", got)
	}
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo one\n", 0)
	env.WriteFile(filepath.Join(proj, "bin", "u"), "echo u\n", 0)
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {"t": {"script": "bin/t"}, "u": {"script": "bin/u"}}}`, 0)
	if got := State(model.LoadStore(proj).Main); got != "changed" {
		t.Errorf("file added: %s", got)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {"t": {"script": "bin/t"}}}`, 0)
	if err := os.Remove(filepath.Join(proj, "bin", "t")); err != nil {
		t.Fatal(err)
	}
	if got := State(model.LoadStore(proj).Main); got != "changed" {
		t.Errorf("file removed: %s", got)
	}
}

func TestApproval_RecordWithoutSnapshotOrLocal(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	g := model.LoadStore(proj).Main
	rec := model.ApprovalPath(g)
	env.WriteFile(rec, `{"key": "x", "hash": "abc", "local": {}}`, 0) // no snapshot: not a record, never approved
	if got := State(g); got != "new" {
		t.Errorf("no snapshot: %s", got)
	}
	// no "local" (written before .shortcuts.local existed): empty, so it approves while no local file exists
	env.WriteFile(rec, `{"snapshot": {".shortcuts": "{\n  \"shortcuts\": {\n    \"t\": {\n      \"script\": \"bin/t\"\n    }\n  },\n  \"version\": 1\n}\n", "bin/t": "echo one\n"}}`, 0)
	if got := State(g); got != "ok" {
		t.Fatalf("no local: %s", got)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"m": {"inline": "x"}}}`, 0)
	if got := State(model.LoadStore(proj).Main); got != "changed" {
		t.Errorf("local file appeared: %s", got)
	}
}
