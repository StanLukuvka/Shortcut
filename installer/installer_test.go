package installer_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

const zshBlock = "# >>> shortcut completion >>>"

// release is one test's installer run: a flat file:// download dir (the layout in install.sh's header), a stub
// zsh and the env the installer reads.
type release struct {
	e       *testkit.Env
	dir     string // download dir
	stubs   string // first on PATH
	version string
	archive string
}

func newRelease(t *testing.T) *release {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("the installer supports linux and macOS on amd64 and arm64, not %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	testkit.Need(t, "sh", "tar", "curl")
	if !testkit.Have("sha256sum") && !testkit.Have("shasum") {
		t.Skip("neither sha256sum nor shasum is installed")
	}
	e := testkit.New(t)
	r := &release{e: e, dir: filepath.Join(e.Tmp, "download"), stubs: filepath.Join(e.Tmp, "stubs")}
	for _, d := range []string{r.dir, r.stubs, filepath.Join(e.Tmp, "stage")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// the binary's own dev version goes into VERSION, the archive name and checksums.txt
	out := e.Run("--version")
	r.version = strings.TrimPrefix(strings.TrimSpace(out.Stdout), "shortcut ")
	if out.Code != 0 || r.version == "" {
		t.Fatalf("shortcut --version: code %d, %q", out.Code, out.Stdout)
	}
	bin, err := os.ReadFile(testkit.Binary(t))
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(e.Tmp, "stage", "shortcut")
	if err := os.WriteFile(stage, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	r.archive = fmt.Sprintf("shortcut_%s_%s_%s.tar.gz", r.version, runtime.GOOS, runtime.GOARCH)
	if res := e.RunCmd(testkit.Opts{}, "tar", "-czf", filepath.Join(r.dir, r.archive), "-C", filepath.Dir(stage), "shortcut"); res.Code != 0 {
		t.Fatalf("tar: %s", res.Stderr)
	}
	tgz, err := os.ReadFile(filepath.Join(r.dir, r.archive))
	if err != nil {
		t.Fatal(err)
	}
	r.write("VERSION", r.version+"\n", 0o644)
	r.write("checksums.txt", fmt.Sprintf("%x  %s\n", sha256.Sum256(tgz), r.archive), 0o644)
	r.write(filepath.Join("..", "stubs", "zsh"), "#!/bin/sh\nexit 0\n", 0o755)
	return r
}

func (r *release) write(name, content string, mode os.FileMode) {
	r.e.T.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), mode); err != nil {
		r.e.T.Fatal(err)
	}
}

// install runs installer/install.sh with the sandbox env, the stubs on PATH and extra on top.
func (r *release) install(extra map[string]string) testkit.Result {
	r.e.T.Helper()
	env := map[string]string{
		"PATH":                   r.stubs + ":" + r.e.Vars["PATH"],
		"SHELL":                  filepath.Join(r.stubs, "zsh"),
		"SHORTCUT_DOWNLOAD_BASE": "file://" + r.dir,
	}
	for k, v := range extra {
		env[k] = v
	}
	return r.e.RunCmd(testkit.Opts{Env: env}, "sh", script(r.e.T))
}

func script(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("install.sh") // go test runs in the package dir
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *release) bin() string { return r.e.Vars["BIN_DIR"] }

func TestInstallScriptDryRun(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			r := newRelease(t)
			extra := map[string]string{}
			if pinned {
				extra["SHORTCUT_VERSION"] = r.version
			}
			res := r.install(extra)
			if res.Code != 0 {
				t.Fatalf("install.sh failed (%d): %s", res.Code, res.Stderr)
			}
			bin := filepath.Join(r.bin(), "shortcut")
			if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
				t.Fatalf("%s is not an executable file: %v", bin, err)
			}
			if v := r.e.RunCmd(testkit.Opts{}, bin, "--version"); v.Stdout != "shortcut "+r.version+"\n" {
				t.Errorf("--version = %q, want shortcut %s", v.Stdout, r.version)
			}
			// one file in BIN_DIR: no staging file is left behind
			ents, _ := os.ReadDir(r.bin())
			if len(ents) != 1 || ents[0].Name() != "shortcut" {
				t.Errorf("BIN_DIR holds %v, want only shortcut", ents)
			}
			compl := filepath.Join(r.e.Vars["XDG_DATA_HOME"], "bash-completion", "completions", "shortcut")
			if !strings.Contains(testkit.ReadFile(t, compl), "complete -F _shortcut shortcut") {
				t.Errorf("%s lacks the bash completion", compl)
			}
		})
	}
}

func TestSecondRunIsIdempotent(t *testing.T) {
	r := newRelease(t)
	for i := 1; i <= 2; i++ {
		if res := r.install(nil); res.Code != 0 {
			t.Fatalf("run %d failed (%d): %s", i, res.Code, res.Stderr)
		}
	}
	rcPath := filepath.Join(r.e.Home, ".zshrc")
	rc := testkit.ReadFile(t, rcPath)
	if err := os.Chmod(rcPath, 0o444); err != nil { // a read-only .zshrc with the block is left alone
		t.Fatal(err)
	}
	if res := r.install(nil); res.Code != 0 || testkit.ReadFile(t, rcPath) != rc {
		t.Errorf("run 3 on a read-only .zshrc: code %d, stderr %q", res.Code, res.Stderr)
	}
	if n := strings.Count(rc, zshBlock); n != 1 {
		t.Errorf(".zshrc has %d completion blocks, want 1:\n%s", n, rc)
	}
	if _, err := os.Stat(filepath.Join(r.e.Vars["XDG_DATA_HOME"], "shortcut", "completion.zsh")); err != nil {
		t.Error(err)
	}
	ents, _ := os.ReadDir(r.bin())
	if len(ents) != 1 {
		t.Errorf("BIN_DIR holds %v, want only shortcut", ents)
	}
}

func TestNoRCLeavesZshrcAlone(t *testing.T) {
	r := newRelease(t)
	if res := r.install(map[string]string{"SHORTCUT_NO_RC": "1"}); res.Code != 0 {
		t.Fatalf("install failed (%d): %s", res.Code, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(r.e.Home, ".zshrc")); !os.IsNotExist(err) {
		t.Errorf(".zshrc was touched with SHORTCUT_NO_RC set: %v", err)
	}
}

func TestCutOffDownloadRunsNothing(t *testing.T) {
	r := newRelease(t)
	lines := strings.SplitAfter(testkit.ReadFile(t, script(t)), "\n")
	cut := filepath.Join(r.e.Tmp, "cut.sh")
	if err := os.WriteFile(cut, []byte(strings.Join(lines[:len(lines)*2/3], "")), 0o644); err != nil {
		t.Fatal(err)
	}
	res := r.e.RunCmd(testkit.Opts{Env: map[string]string{
		"PATH": r.stubs + ":" + r.e.Vars["PATH"], "SHORTCUT_DOWNLOAD_BASE": "file://" + r.dir,
	}}, "sh", cut)
	if strings.Contains(res.Stdout, "Installed") {
		t.Errorf("a cut-off script installed something: %s", res.Stdout)
	}
	if _, err := os.Stat(r.bin()); !os.IsNotExist(err) {
		t.Errorf("BIN_DIR exists after a cut-off script: %v", err)
	}
}

// a data path zsh would re-evaluate at every startup is an error and gets no block
func TestUnsafeDataPathGetsNoZshBlock(t *testing.T) {
	r := newRelease(t)
	marker := filepath.Join(r.e.Tmp, "marker")
	data := filepath.Join(r.e.Tmp, "da ta\"x$(touch "+marker+")")
	res := r.install(map[string]string{"XDG_DATA_HOME": data})
	if res.Code != 1 || !strings.Contains(res.Stderr, "the data path "+data+" contains a quote") {
		t.Fatalf("code %d, stderr %q; want 1 and the data path error", res.Code, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(r.e.Home, ".zshrc")); !os.IsNotExist(err) {
		t.Errorf(".zshrc exists after an unsafe path: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("the marker command ran: %v", err)
	}
}

// a .zshrc that can't be read is an error and is left as it was
func TestUnreadableZshrcIsAnError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads any file")
	}
	r := newRelease(t)
	rc := filepath.Join(r.e.Home, ".zshrc")
	if err := os.WriteFile(rc, []byte("# mine\n"), 0); err != nil {
		t.Fatal(err)
	}
	res := r.install(nil)
	if res.Code != 1 || !strings.Contains(res.Stderr, "could not read "+rc) {
		t.Fatalf("code %d, stderr %q; want 1 and could not read", res.Code, res.Stderr)
	}
	if err := os.Chmod(rc, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := testkit.ReadFile(t, rc); got != "# mine\n" {
		t.Errorf(".zshrc changed: %q", got)
	}
}

// a data path with a space still gets a block that reads the path literally
func TestDataPathWithSpaceGetsWorkingBlock(t *testing.T) {
	r := newRelease(t)
	data := filepath.Join(r.e.Tmp, "da ta")
	if res := r.install(map[string]string{"XDG_DATA_HOME": data}); res.Code != 0 {
		t.Fatalf("install failed (%d): %s", res.Code, res.Stderr)
	}
	rc := testkit.ReadFile(t, filepath.Join(r.e.Home, ".zshrc"))
	if !strings.Contains(rc, zshBlock) {
		t.Fatalf("no block in .zshrc:\n%s", rc)
	}
	var line string
	for _, l := range strings.Split(rc, "\n") {
		if strings.HasPrefix(l, "[ -r ") {
			line = l
		}
	}
	// run the line in sh with `source` swapped for a printf: the path must come through as one word
	res := r.e.RunCmd(testkit.Opts{}, "sh", "-c", strings.Replace(line, "&& source ", "&& printf %s ", 1))
	if want := filepath.Join(data, "shortcut", "completion.zsh"); res.Stdout != want {
		t.Errorf("block sources %q, want %q (code %d, stderr %q)", res.Stdout, want, res.Code, res.Stderr)
	}
}

// each refusal exits 1 with its message and installs nothing
func TestRefusalsInstallNothing(t *testing.T) {
	for _, c := range []struct {
		name, stderr string
		setup        func(r *release)
		env          map[string]string
	}{
		{name: "unsupported system", stderr: "unsupported system Plan9", setup: func(r *release) {
			if err := os.WriteFile(filepath.Join(r.stubs, "uname"), []byte("#!/bin/sh\necho Plan9\n"), 0o755); err != nil {
				r.e.T.Fatal(err)
			}
		}},
		{name: "bad checksum", stderr: "checksum mismatch", setup: func(r *release) {
			r.write("checksums.txt", strings.Repeat("0", 64)+"  "+r.archive+"\n", 0o644)
		}},
		{name: "version ../x", stderr: "invalid version", env: map[string]string{"SHORTCUT_VERSION": "../x"}},
		{name: "version 1.0/../x", stderr: "invalid version", env: map[string]string{"SHORTCUT_VERSION": "1.0/../x"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRelease(t)
			if c.setup != nil {
				c.setup(r)
			}
			res := r.install(c.env)
			if res.Code != 1 || !strings.Contains(res.Stderr, c.stderr) {
				t.Fatalf("code %d, stderr %q; want 1 and %q", res.Code, res.Stderr, c.stderr)
			}
			if _, err := os.Stat(r.bin()); !os.IsNotExist(err) {
				t.Errorf("BIN_DIR exists after a refusal: %v", err)
			}
			if _, err := os.Stat(filepath.Join(r.e.Vars["XDG_DATA_HOME"], "bash-completion")); !os.IsNotExist(err) {
				t.Errorf("a completion was written after a refusal: %v", err)
			}
		})
	}
}
