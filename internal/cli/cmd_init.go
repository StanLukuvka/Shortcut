package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	shortcut "github.com/StanLukuvka/Shortcut"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/osutil"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdInit runs init. With --global it creates the global store, or reviews a project's exported shortcuts, or a folder
// of namespaces, and copies them there.
func cmdInit(s *textio.Streams, args []string) error {
	ns, err := parsers["init"].Parse(args)
	if err != nil {
		return err
	}
	source := ns.Str("source")
	if ns.Bool("bundled") {
		if source != "" {
			return Die("--bundled takes no source")
		}
		if !ns.Bool("global") {
			return Die("--bundled copies the bundled shortcuts into your global store: add --global.")
		}
		return initBundled(s, model.GlobalStoreDir(), ns.Bool("yes"), ns.Bool("replace"))
	}
	if !ns.Bool("global") {
		if source != "" {
			return Die("a project's " + model.ProjectFile + " is used in place, never copied: clone the project and run " +
				"`shortcut update` there. To copy its exported shortcuts into your global store, add --global.")
		}
		return Die("a project's " + model.ProjectFile + " is a file you write by hand, starting from: " +
			`{"version": ` + strconv.Itoa(model.RegistryVersion) + `, "shortcuts": {}}. For your own shortcuts, use new --local.`)
	}

	destStoreDir := model.GlobalStoreDir()
	if source == "" {
		if model.RegistryExists(destStoreDir) {
			s.Out.WriteString("Global store already exists at " + destStoreDir + "\n")
			return nil
		}
		if err := model.InitStore(destStoreDir); err != nil {
			return err
		}
		s.Out.WriteString("Initialized empty global store at " + destStoreDir + "\n")
		return nil
	}

	sourceRoot := ""
	if isGitSource(source) {
		tmp, cleanup, err := cloneTemp(source)
		defer cleanup()
		if err != nil {
			return err
		}
		sourceRoot = tmp
	} else {
		sourceRoot = osutil.Abs(osutil.ExpandUser(source))
		if !osutil.IsDir(sourceRoot) {
			return Die("source directory does not exist: " + sourceRoot)
		}
	}
	return importFromRoot(s, source, sourceRoot, destStoreDir, ns.Bool("yes"), ns.Bool("replace"))
}

// initBundled is `init --global --bundled`: the embedded templates/ written to a temporary folder, then imported as
// a folder on disk is, so the review, the "Skipped" list, the question, -y and --replace are the same.
func initBundled(s *textio.Streams, destStoreDir string, assumeYes, replace bool) error {
	dir, cleanup, err := tempDir()
	defer cleanup()
	if err != nil {
		return err
	}
	err = fs.WalkDir(shortcut.Bundled, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, "templates"), "/")))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := shortcut.Bundled.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if bytes.HasPrefix(data, []byte("#!")) {
			mode = 0o755
		}
		return os.WriteFile(dest, data, mode)
	})
	if err != nil {
		return err
	}
	return importFromRoot(s, "the bundled shortcuts", dir, destStoreDir, assumeYes, replace)
}

// isGitSource reports whether source is a git URL.
func isGitSource(source string) bool {
	for _, p := range []string{"http://", "https://", "git://", "git@", "ssh://", "file://"} {
		if strings.HasPrefix(source, p) {
			return true
		}
	}
	return strings.HasSuffix(source, ".git")
}

// cloneTemp runs `git clone --depth 1` into a fresh temporary folder, whose path it returns
// with the function that removes it (see tempDir). cleanup is safe to call whatever the result, and also runs when the process is
// told to stop (Ctrl-C, SIGTERM, SIGHUP): what was cloned has been read into memory by the time anything is asked,
// so a stop at the question only drops the folder and lets Ask answer "no" for SIGINT; any other stop exits 128+signal.
func cloneTemp(source string) (string, func(), error) {
	dir, cleanup, err := tempDir()
	if err != nil {
		return "", cleanup, err
	}
	cmd := exec.Command("git", "clone", "--depth", "1", "--", source, dir) // "--": a source such as --upload-pack=... must not be read as an option
	var errText bytes.Buffer
	cmd.Stderr = &errText
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errText.String())
		var exit *exec.ExitError
		switch {
		case detail != "":
		case errors.Is(err, exec.ErrNotFound):
			detail = "git: not found in PATH"
		case errors.As(err, &exit):
			detail = "git clone exited with status " + strconv.Itoa(exit.ExitCode())
		default:
			detail = err.Error()
		}
		return "", cleanup, Die("failed to clone git repository from " + source + ": " + detail)
	}
	return dir, cleanup, nil
}

// tempDir makes the fresh folder an import is read from, with the function that removes it.
func tempDir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "shortcut-init-")
	if err != nil {
		return "", func() {}, err
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig, ok := <-stop
		if !ok {
			return
		}
		os.RemoveAll(dir)
		if reviewing.Load() && sig == syscall.SIGINT {
			return // Ask is reading: its "no" ends the command, normally
		}
		os.Exit(128 + int(sig.(syscall.Signal)))
	}()
	cleanup := func() {
		signal.Stop(stop)
		close(stop)
		os.RemoveAll(dir)
	}
	return dir, cleanup, nil
}
