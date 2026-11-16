package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/ordjson"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// golden reads ../testdata/approval.json.
func golden(t *testing.T, set string) []*ordjson.Object {
	t.Helper()
	raw := testkit.Golden(t, "approval.json")
	v, err := ordjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	cases, ok := v.(*ordjson.Object).Get(set)
	if !ok {
		t.Fatalf("approval.json has no %s", set)
	}
	var out []*ordjson.Object
	for _, c := range cases.([]ordjson.Value) {
		out = append(out, c.(*ordjson.Object))
	}
	return out
}

func str(o *ordjson.Object, k string) string {
	s, _ := o.Vals[k].(string)
	return s
}

// text is the field k, or the bytes of k+"_hex" when the name is not valid UTF-8 and so is given as hex.
func text(t *testing.T, o *ordjson.Object, k string) string {
	t.Helper()
	if h, ok := o.Get(k + "_hex"); ok {
		b, err := hex.DecodeString(h.(string))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return str(o, k)
}

func strMap(t *testing.T, v ordjson.Value) map[string]string {
	t.Helper()
	m, ok := stringMap(v)
	if !ok {
		t.Fatalf("not a map of strings: %v", v)
	}
	return m
}

func sameJSON(a, b ordjson.Value) bool {
	switch x := a.(type) {
	case *ordjson.Object:
		y, ok := b.(*ordjson.Object)
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
	case []ordjson.Value:
		y, ok := b.([]ordjson.Value)
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
	x, err1 := ordjson.Marshal(a)
	y, err2 := ordjson.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

func TestApproval_KeysAndRecordNames(t *testing.T) {
	base := testkit.New(t).Tmp
	names := map[string]string{}
	seen := func(g model.Group, what string) {
		t.Helper()
		sum := sha256.Sum256([]byte(g.Key()))
		if want := hex.EncodeToString(sum[:]) + ".json"; model.RecordName(g.Key()) != want {
			t.Errorf("%s: record name %s, want %s", what, model.RecordName(g.Key()), want)
		}
		if prev, dup := names[model.RecordName(g.Key())]; dup {
			t.Errorf("%s and %s share a record", prev, what)
		}
		names[model.RecordName(g.Key())] = what
	}
	for _, d := range []string{"plain", "café", "😀", "a b/c", "p\xffroj"} {
		dir := filepath.Join(base, d)
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Logf("this filesystem refuses %q: %v", d, err)
			continue
		}
		g := model.Group{Kind: model.KindProject, InProject: true, StoreDir: dir, File: dir + "/.shortcuts", LocalFile: dir + "/.shortcuts.local"}
		if g.Key() != osutil.Realpath(dir) {
			t.Errorf("project key %q, want %q", g.Key(), osutil.Realpath(dir))
		}
		seen(g, "project "+d)
	}
	store := osutil.Realpath(base)
	for _, c := range []struct{ name, file string }{{"ns", "a.sh"}, {"ns", "b.sh"}, {"other", "a.sh"}, {"ns", "café.sh"}, {"ns", "caf\xe9.sh"}, {"ns", `caf\xe9.sh`}, {"n\"s", "a\\b"}} {
		g := model.Group{Kind: model.KindNamespace, Name: c.name, StoreDir: base, File: filepath.Join(base, c.file)}
		want := "[" + strconv.QuoteToASCII(store) + ", " + strconv.QuoteToASCII(c.name) + ", " + strconv.QuoteToASCII(osutil.Realpath(g.File)) + "]"
		if g.Key() != want {
			t.Errorf("namespace key %s, want %s", g.Key(), want)
		}
		seen(g, "namespace "+c.name+" "+c.file)
	}
	if k := (model.Group{Kind: model.KindNamespace, Name: "n", StoreDir: "/s", File: "/s/é"}).Key(); k != `["/s", "n", "/s/\u00e9"]` {
		t.Errorf("example key %s", k)
	}
}

func TestApproval_ApprovalPathIsInTheGlobalStore(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	g := model.Group{Kind: model.KindProject, StoreDir: env.Home, File: env.Home + "/.shortcuts", LocalFile: env.Home + "/.shortcuts.local"}
	if got, want := ctx.ApprovalPath(g), filepath.Join(env.Store, "approvals", model.RecordName(env.Home)); got != want {
		t.Errorf("%s, want %s", got, want)
	}
}

// buildTree writes a golden tree under root. It skips the test on a filesystem that refuses non-UTF-8 names.
func buildTree(t *testing.T, env *testkit.Env, root string, tree []ordjson.Value) {
	t.Helper()
	for _, item := range tree {
		e := item.(*ordjson.Object)
		if p := text(t, e, "path"); !utf8.ValidString(p) {
			probe := filepath.Join(env.Tmp, "probe\xff")
			if err := os.WriteFile(probe, nil, 0o644); err != nil {
				t.Skipf("this filesystem refuses non-UTF-8 file names: %v", err)
			}
			os.Remove(probe)
		}
		path := filepath.Join(root, text(t, e, "path"))
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

// afterShortcut cuts a file error back to the part after the file's name, since the golden root belongs to the frozen data (recorded once from the first version).
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
			ctx := model.NewContext(env.Home, nil)
			root := env.Folder("tree")
			buildTree(t, env, root, c.Vals["tree"].([]ordjson.Value))
			store := model.NewContext(env.Home, nil).Store(root)
			want := c.Vals["groups"].([]ordjson.Value)
			got := store.Groups()
			if len(got) != len(want) {
				t.Fatalf("%d groups, want %d", len(got), len(want))
			}
			if wp := c.Vals["problems"].([]ordjson.Value); len(store.Problems) != len(wp) {
				t.Errorf("problems %q, want %v", store.Problems, wp)
			}
			for i, g := range got {
				w := want[i].(*ordjson.Object)
				groups++
				if g.Name != str(w, "name") {
					t.Fatalf("group %d is %q, want %q", i, g.Name, str(w, "name"))
				}
				if contains, ok := w.Get("error_contains"); ok {
					msg := ""
					if g.Error != nil {
						msg = *g.Error
					} else if err := Check(ctx, g); err != nil {
						msg = err.Error()
						if State(ctx, g) != "invalid" || Require(ctx, g) == nil {
							t.Errorf("state %s, require %v", State(ctx, g), Require(ctx, g))
						}
						for _, e := range c.Vals["tree"].([]ordjson.Value) { // the message shows the offending path
							if _, bad := e.(*ordjson.Object).Get("path_hex"); bad && !strings.Contains(msg, strconv.Quote(text(t, e.(*ordjson.Object), "path"))) {
								t.Errorf("%q does not name the path", msg)
							}
						}
					}
					if want := strings.ReplaceAll(contains.(string), "@ROOT@", root); !strings.Contains(msg, want) {
						t.Errorf("error %q, want it to hold %q", msg, want)
					}
					continue
				}
				if wantErr := w.Vals["error"]; (g.Error == nil) != (wantErr == nil) ||
					(g.Error != nil && afterShortcut(*g.Error) != afterShortcut(wantErr.(string))) {
					t.Errorf("error %v, want %v", g.Error, wantErr)
				}
				shared, local := must(SharedFiles(g)), must(LocalFiles(g))
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

// checkRecord: a record in the stored form reads as ok; one written now parses to the same object.
func checkRecord(t *testing.T, env *testkit.Env, g model.Group, root, template string, shared, local map[string]string) {
	t.Helper()
	ctx := model.NewContext(env.Home, nil)
	text := strings.ReplaceAll(template, "@ROOT@", root)
	want, err := ordjson.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	path := ctx.ApprovalPath(g)
	if State(ctx, g) != "new" {
		t.Errorf("before any record: %s", State(ctx, g))
	}
	env.WriteFile(path, text, 0)
	if got := State(model.NewContext(env.Home, nil), g); got != "ok" {
		t.Errorf("a stored record: state %s, want ok", got)
	}
	if r := Load(ctx, g); r == nil || !equalMaps(r.Snapshot, shared) || !equalMaps(r.Local, local) {
		t.Errorf("Load: %+v", r)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Save(ctx, g, shared, local); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ordjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(got, want) {
		t.Errorf("written record\n%s\ndiffers from the template\n%s", raw, text)
	}
	if State(ctx, g) != "ok" {
		t.Errorf("a Go-written record: state %s", State(ctx, g))
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("record mode %v", st.Mode().Perm())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestApproval_ProjectInANonUTF8FolderCannotBeApproved(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	probe := filepath.Join(env.Tmp, "caf\xe9")
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	env.WriteStore(filepath.Join(probe, ".shortcuts"), map[string]any{"a": map[string]any{"inline": "x"}})
	g := model.NewContext(env.Home, nil).Store(probe).Main
	if g.Key() != probe {
		t.Fatalf("key %q", g.Key())
	}
	// the record is still named after the key's raw bytes, so two such folders cannot share one
	sum := sha256.Sum256([]byte(probe))
	if got := filepath.Base(ctx.ApprovalPath(g)); got != hex.EncodeToString(sum[:])+".json" {
		t.Errorf("record %s", got)
	}
	// but the record would have to hold the key as JSON text, which cannot hold it: refused, nothing written
	err := Save(ctx, g, must(SharedFiles(g)), must(LocalFiles(g)))
	if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(ctx.ApprovalPath(g)); err == nil {
		t.Error("a record was written")
	}
	if State(ctx, g) != "invalid" || Check(ctx, g) == nil || Require(ctx, g) == nil { // refused up front, before any diff or prompt
		t.Errorf("state %s", State(ctx, g))
	}
}

func TestApproval_StateAndRequire(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	load := func() model.Group { return model.NewContext(env.Home, nil).Store(proj).Main }
	g := load()
	if State(ctx, g) != "new" {
		t.Fatal(State(ctx, g))
	}
	if err := Require(ctx, g); err == nil || err.Error() != g.Key()+" isn't approved yet; review it with: shortcut update" {
		t.Error(err)
	}
	if err := Save(ctx, g, must(SharedFiles(g)), must(LocalFiles(g))); err != nil {
		t.Fatal(err)
	}
	if State(ctx, g) != "ok" || Require(ctx, g) != nil {
		t.Fatal("not ok after Save")
	}
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo two\n", 0)
	ctx, g = model.NewContext(env.Home, nil), load()
	if State(ctx, g) != "changed" {
		t.Errorf("after a script change: %s", State(ctx, g))
	}
	if err := Require(ctx, g); err == nil || err.Error() != g.Key()+" changed since you approved it; run: shortcut update" {
		t.Error(err)
	}
	// a .shortcuts.local appearing changes the local half
	if err := Save(ctx, g, must(SharedFiles(g)), must(LocalFiles(g))); err != nil {
		t.Fatal(err)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts.local"), `{"version": 1, "shortcuts": {"m": {"inline": "x"}}}`, 0)
	ctx, g = model.NewContext(env.Home, nil), load()
	if State(ctx, g) != "changed" || LocalIsApproved(ctx, g, Load(ctx, g)) {
		t.Errorf("after adding .shortcuts.local: %s", State(ctx, g))
	}
	// a record with no snapshot (only CLI writes to .local were recorded) reads as new, not changed
	if err := Save(ctx, g, map[string]string{}, must(LocalFiles(g))); err != nil {
		t.Fatal(err)
	}
	if State(ctx, g) != "new" {
		t.Errorf("empty snapshot: %s", State(ctx, g))
	}
	// the global store never needs approval
	if gl := model.NewContext(env.Home, nil).Store(env.Store).Main; State(ctx, gl) != "ok" || Require(ctx, gl) != nil {
		t.Error("global store")
	}
}

func TestApproval_LoadRefusesWhatIsNotARecord(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{})
	g := model.NewContext(env.Home, nil).Store(proj).Main
	path := ctx.ApprovalPath(g)
	for _, bad := range []string{`{not json`, `[]`, `{"hash": "x"}`, `{"snapshot": []}`, `{"snapshot": {"a": 1}}`, `{"snapshot": {}, "local": []}`, `{"snapshot": {}}`} {
		env.WriteFile(path, bad, 0)
		if r := Load(ctx, g); r != nil {
			t.Errorf("%s loaded as %+v", bad, r)
		}
		if got := State(model.NewContext(env.Home, nil), g); got != "new" {
			t.Errorf("%s: state %s", bad, got)
		}
	}
	env.WriteFile(path, `{"snapshot": {}, "local": {}}`, 0)
	if r := Load(ctx, g); r == nil || len(r.Local) != 0 {
		t.Errorf("%+v", r)
	}
	env.WriteFile(path, "{\r\n\"snapshot\": {},\r\n\"local\": {}\r\n}", 0)
	if Load(ctx, g) == nil {
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
	g := model.NewContext(env.Home, nil).Store(proj).Main
	files := must(SharedFiles(g))
	if files[elsewhere] != sum("echo out\n") { // an absolute script is used as given, so its key is the absolute path
		t.Errorf("absolute script: %q", files)
	}
	if got := files["./cwd of rel"]; got != sum(filepath.Join(proj, "work")+"\n") {
		t.Errorf("rel cwd: %q", got)
	}
	// a NUL in a cwd does not panic and is listed like any relative cwd; the run itself fails (see the cli test)
	if _, ok := files["./cwd of nul"]; !ok {
		t.Errorf("no cwd row for the NUL entry: %q", files)
	}
	for k := range files {
		if k != ".shortcuts" && k != elsewhere && k != "./cwd of rel" && k != "./cwd of nul" {
			t.Errorf("unexpected key %q", k) // not home, caller or the FIFO-less "pipe"
		}
	}
	if len(files) != 4 { // .shortcuts, the absolute script, the two cwds
		t.Errorf("%d files: %q", len(files), files)
	}
}

func TestApproval_DisplayPathTreatsADotDotPrefixAsOutside(t *testing.T) {
	g := model.Group{Kind: model.KindProject, StoreDir: "/p", File: "/p/.shortcuts"}
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
	store := model.NewContext(env.Home, nil).Store(proj)
	files := must(SharedFiles(store.Main))
	if files["cwd of go"] != sum("x") {
		t.Errorf("script: %q", files["cwd of go"])
	}
	if want := sum(osutil.Realpath(filepath.Join(proj, "safe")) + "\n"); files["./cwd of go"] != want {
		t.Errorf("cwd record %q, want %q", files["./cwd of go"], want)
	}
}

func TestApproval_InvalidUTF8AndItsEscapedTextAreDifferentFiles(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{"a": map[string]any{"inline": "x"}}, testkit.Dirs("scripts"))
	// a file named with the ASCII text caf\udce9.sh is an ordinary file, distinct from caf<0xe9>.sh
	env.WriteFile(filepath.Join(proj, "scripts", `caf\udce9.sh`), "echo ascii\n", 0o644)
	ns := func() model.Group {
		env.WriteFile(filepath.Join(proj, "ns.shortcut"), `{"version": 1, "shortcuts": {"go": {"inline": "x"}}}`, 0)
		env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {}, "imports": {"ns": "ns.shortcut"}}`, 0)
		g, ok := model.NewContext(env.Home, nil).Store(proj).Namespace("ns")
		if !ok {
			t.Fatal("no namespace")
		}
		return g
	}
	g := ns()
	if g.ScriptsDir() != filepath.Join(proj, "scripts", "ns") {
		t.Skipf("scripts folder %s", g.ScriptsDir())
	}
	env.WriteFile(filepath.Join(g.ScriptsDir(), `caf\udce9.sh`), "echo ascii\n", 0o644)
	if err := Check(ctx, g); err != nil {
		t.Fatalf("ASCII name: %v", err)
	}
	shared, local, err := snapshot(ctx, g)
	if err != nil || shared[`scripts/ns/caf\udce9.sh`] != sum("echo ascii\n") {
		t.Fatalf("%q %v", shared, err)
	}
	if err := Save(ctx, g, shared, local); err != nil || State(ctx, g) != "ok" {
		t.Fatalf("Save %v, state %s", err, State(ctx, g))
	}
	// a file named caf<0xe9>.sh is refused, and the message names it; the approval no longer holds
	bad := filepath.Join(g.ScriptsDir(), "caf\xe9.sh")
	if err := os.WriteFile(bad, []byte("echo bad\n"), 0o644); err != nil {
		t.Skipf("this filesystem refuses non-UTF-8 names: %v", err)
	}
	ctx = model.NewContext(env.Home, nil)
	err = Check(ctx, g)
	if err == nil || !strings.Contains(err.Error(), `"scripts/ns/caf\xe9.sh"`) || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("Check: %v", err)
	}
	if State(ctx, g) != "invalid" || Require(ctx, g) == nil || Save(ctx, g, must(SharedFiles(g)), must(LocalFiles(g))) == nil {
		t.Errorf("state %s", State(ctx, g))
	}
}

func TestApproval_ARecordWithNonUTF8TextIsNoRecord(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	g := model.NewContext(env.Home, nil).Store(proj).Main
	// a record whose file name is not valid UTF-8 is no record
	env.WriteFile(ctx.ApprovalPath(g), "{\"snapshot\": {\"caf\xe9.sh\": \""+sum("x")+"\"}, \"local\": {}}", 0)
	if Load(ctx, g) != nil || State(ctx, g) != "new" {
		t.Errorf("loaded as a record: %+v, state %s", Load(ctx, g), State(ctx, g))
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
	g, ok := model.NewContext(env.Home, nil).Store(proj).Namespace("ns")
	if !ok {
		t.Fatal("no namespace")
	}
	got := folderFiles(g)
	want := map[string]string{
		"scripts/ns/go.sh": sum("echo go\n"), "scripts/ns/sub/deep.sh": sum("echo deep\n"), "scripts/ns/dirlink": sum("symlink to sub\n"),
	}
	if !equalMaps(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// a main group has no scripts folder in this sense
	if len(folderFiles(model.NewContext(env.Home, nil).Store(proj).Main)) != 0 {
		t.Error("main group")
	}
	// the namespace's script reaches SharedFiles once, under its real path
	if f := must(SharedFiles(g)); f["scripts/ns/go.sh"] != sum("echo go\n") || len(f) != 4 {
		t.Errorf("%q", f)
	}
}

// An approval record with an unknown field, "extra", which is ignored. Hand-written.
const recordWithExtraField = `{
  "key": "@ROOT@",
  "extra": 1,
  "snapshot": {
    ".shortcuts": "@SHORTCUTS@",
    "bin/t": "@BINT@"
  },
  "local": {}
}
`

func extraFieldRecord(root string) string {
	r := strings.NewReplacer("@ROOT@", root, "@SHORTCUTS@", sum("{\n  \"shortcuts\": {\n    \"t\": {\n      \"script\": \"bin/t\"\n    }\n  },\n  \"version\": 1\n}\n"), "@BINT@", sum("echo one\n"))
	return r.Replace(recordWithExtraField)
}

func TestApproval_RecordWithAnUnknownFieldStillApproves(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	g := model.NewContext(env.Home, nil).Store(proj).Main
	rec := ctx.ApprovalPath(g)
	env.WriteFile(rec, extraFieldRecord(proj), 0)
	if got := State(ctx, g); got != "ok" {
		t.Fatalf("unchanged files: %s", got)
	}
	env.WriteFile(rec, extraFieldRecord(proj), 0)
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo two\n", 0)
	if got := stateOnDisk(env, proj); got != "changed" {
		t.Errorf("text changed: %s", got)
	}
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo one\n", 0)
	env.WriteFile(filepath.Join(proj, "bin", "u"), "echo u\n", 0)
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {"t": {"script": "bin/t"}, "u": {"script": "bin/u"}}}`, 0)
	if got := stateOnDisk(env, proj); got != "changed" {
		t.Errorf("file added: %s", got)
	}
	env.WriteFile(filepath.Join(proj, ".shortcuts"), `{"version": 1, "shortcuts": {"t": {"script": "bin/t"}}}`, 0)
	if err := os.Remove(filepath.Join(proj, "bin", "t")); err != nil {
		t.Fatal(err)
	}
	if got := stateOnDisk(env, proj); got != "changed" {
		t.Errorf("file removed: %s", got)
	}
}

func TestApproval_RecordWithoutSnapshotOrLocal(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	g := model.NewContext(env.Home, nil).Store(proj).Main
	rec := ctx.ApprovalPath(g)
	env.WriteFile(rec, `{"key": "x", "hash": "abc", "local": {}}`, 0) // no snapshot: not a record, never approved
	if got := State(ctx, g); got != "new" {
		t.Errorf("no snapshot: %s", got)
	}
	// no "local": not a record either (fail closed)
	env.WriteFile(rec, `{"snapshot": {".shortcuts": "{\n  \"shortcuts\": {\n    \"t\": {\n      \"script\": \"bin/t\"\n    }\n  },\n  \"version\": 1\n}\n", "bin/t": "echo one\n"}}`, 0)
	if got := State(model.NewContext(env.Home, nil), g); got != "new" {
		t.Errorf("no local: %s", got)
	}
}

// sum is what the record keeps for a file with this content.
func sum(s string) string { return digest([]byte(s)) }

// must is the value of a call that cannot fail in these tests.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestApproval_ANamespaceFileWithBadBytesInItsPathIsRefusedBeforeAnythingIsSaved(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	g := model.Group{Kind: model.KindNamespace, Name: "ns", StoreDir: env.Store, File: filepath.Join(env.Store, "imports", "caf\xe9.shortcut")}
	for name, err := range map[string]error{"Check": Check(ctx, g), "Save": Save(ctx, g, map[string]string{}, map[string]string{})} {
		if err == nil || !strings.Contains(err.Error(), `caf\xe9.shortcut"`) || !strings.Contains(err.Error(), "not valid UTF-8") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if State(ctx, g) != "invalid" {
		t.Errorf("state %s", State(ctx, g))
	}
	if _, err := os.Stat(ctx.ApprovalPath(g)); err == nil {
		t.Error("a record was written")
	}
}

func TestApproval_ARecordInTheTextFormatIsNotApprovedYet(t *testing.T) {
	env := testkit.New(t)
	ctx := model.NewContext(env.Home, nil)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	g := model.NewContext(env.Home, nil).Store(proj).Main
	env.WriteFile(ctx.ApprovalPath(g), `{"key": "x", "snapshot": {"bin/t": "echo one\n"}, "local": {}}`, 0)
	if Load(ctx, g) != nil || State(ctx, g) != "new" {
		t.Errorf("text record: %+v, state %s", Load(ctx, g), State(ctx, g))
	}
	for _, v := range []string{"", sum("a")[1:], strings.ToUpper(sum("a")), sum("a") + "0", strings.Repeat("g", 64)} {
		env.WriteFile(ctx.ApprovalPath(g), `{"snapshot": {"bin/t": "`+v+`"}, "local": {}}`, 0)
		if Load(ctx, g) != nil {
			t.Errorf("%q loaded as a digest", v)
		}
	}
}

// stateOnDisk is the state of proj's group as a new run would read it.
func stateOnDisk(env *testkit.Env, proj string) model.ApprovalState {
	c := model.NewContext(env.Home, nil)
	return State(c, c.Store(proj).Main)
}

func TestApproval_StateIsComputedOncePerRun(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	ctx := model.NewContext(env.Home, nil)
	g := ctx.Store(proj).Main
	if err := Save(ctx, g, must(SharedFiles(g)), must(LocalFiles(g))); err != nil {
		t.Fatal(err)
	}
	if got := State(ctx, g); got != model.StateOK {
		t.Fatal(got)
	}
	env.WriteFile(filepath.Join(proj, "bin", "t"), "echo two\n", 0)
	if got := State(ctx, g); got != model.StateOK {
		t.Errorf("the state was computed again: %s", got)
	}
	if got := Refresh(ctx, g); got != model.StateChanged {
		t.Errorf("Refresh: %s", got)
	}
	if got := stateOnDisk(env, proj); got != model.StateChanged {
		t.Errorf("a new run: %s", got)
	}
}

func TestApproval_SaveForgetsTheCachedState(t *testing.T) {
	env := testkit.New(t)
	proj := env.Project(map[string]any{"t": map[string]any{"script": "bin/t"}}, testkit.Files(map[string]any{"bin/t": "echo one\n"}))
	ctx := model.NewContext(env.Home, nil)
	g := ctx.Store(proj).Main
	if got := State(ctx, g); got != model.StateNew {
		t.Fatal(got)
	}
	if err := Save(ctx, g, must(SharedFiles(g)), must(LocalFiles(g))); err != nil {
		t.Fatal(err)
	}
	if got := State(ctx, g); got != model.StateOK {
		t.Errorf("after Save: %s", got)
	}
}
