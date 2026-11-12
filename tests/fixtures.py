"""The hub of the tests: one sandbox, one set of fixtures, one way to start a process.

Each test_*.py is a spoke for one behaviour. A spoke subclasses ShortcutTestCase and
builds what it needs from here:

    proj = self.project(GO, approved=True)       a project with an approved store
    src = self.collection(HI)                    a folder for `init --global` to import
    res = self.run_cli(["r", "go"], cwd=str(proj))

Spokes don't make temp dirs, change os.environ or import subprocess; test_fixtures.py
fails if one does. That leaves launch_args() as the only way out to a child process, and
it refuses to start one that could reach anything outside the test's temp dir. Add a
fixture here once a second spoke needs it.

Run everything, one spoke, or one test:

    python3 -m unittest discover -s tests
    python3 tests/test_import.py
    python3 -m unittest discover -s tests -k test_confirm
"""

import fcntl
import json
import os
import pathlib
import select
import shlex
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unittest
import unittest.mock
from typing import Any, Mapping, Optional, Sequence, Union

REPO = pathlib.Path(__file__).resolve().parent.parent
SRC = REPO / "src"
PACKAGE = SRC / "shortcut"
SOURCES = sorted(PACKAGE.glob("*.py"))  # the code, for the tests that read it
TEMPLATES = REPO / "templates"
# Run as `python3 src`: src/ goes first on sys.path, not the child's cwd, which may hold a project's json.py
CLI = [sys.executable, str(SRC)]
CLI_SHELL = shlex.join(CLI)

sys.path.insert(0, str(SRC))
from shortcut import helpers, importing, lookup, stores  # noqa: E402,F401  (for the tests that call it in-process)

# Shortcuts for a store. Writers only read these, so spokes can share them.
HI = {"hi": {"inline": "echo hi"}}
GO = {"go": {"inline": "echo ran"}}
OWN = {"own": {"inline": "echo own"}}
BIN_TOOL = {"tool": {"script": "bin/tool"}}

# Whole .shortcut files, for write_import.
DOCKER_PS = {"version": 2, "shortcuts": {"ps": {"inline": "echo ps"}}}
KUBE_GET = {"version": 2, "shortcuts": {"get": {"inline": "echo get"}}}

BROKEN_JSON = "{not json"

# What a run can write to besides HOME, and where it goes when the variable is unset or empty.
SANDBOXED_PATHS = {
    "SHORTCUTS_HOME": "~/.shortcuts",
    "XDG_DATA_HOME": "~/.local/share",
    "XDG_CONFIG_HOME": "~/.config",
    "BIN_DIR": "~/.local/bin",
}


class SandboxEscape(RuntimeError):
    """A test was about to read or write outside its temp dir."""


def refuse_unless_inside(root: pathlib.Path, what: str, path: pathlib.Path) -> None:
    resolved = path.resolve()
    if not resolved.is_relative_to(root):
        raise SandboxEscape(f"{what} is {resolved}, outside this test's temp dir {root}")


def refuse_unless_sandboxed(root: pathlib.Path, env: Mapping[str, str], cwd: Union[str, pathlib.Path]) -> None:
    """Raise SandboxEscape unless HOME, the cwd and every path derived from them are inside root.

    Each path is worked out as the child will work it out, from this env's own HOME. So
    `export HOME=$(mktemp -d) SHORTCUTS_HOME=$HOME/.shortcuts`, where SHORTCUTS_HOME picks
    up the old HOME, is refused however it was reached. Paths are compared once resolved,
    so a `..`, a symlink or a sibling such as <root>-other doesn't pass.
    """
    home = env.get("HOME")
    if not home:
        raise SandboxEscape(f"HOME is unset or empty, so a child would find the real one (temp dir {root})")
    base = pathlib.Path(cwd)
    refuse_unless_inside(root, "the working directory", base)
    refuse_unless_inside(root, "HOME", base / home)
    for name, default in SANDBOXED_PATHS.items():
        value = env.get(name) or default
        expanded = home + value[1:] if value == "~" or value.startswith("~/") else os.path.expanduser(value)
        refuse_unless_inside(root, name, base / expanded)


class ShortcutTestCase(unittest.TestCase):
    """A fresh temp dir for each test, with HOME and everything under it inside."""

    def setUp(self):
        temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(temp_dir.cleanup)
        self.tmp_path = pathlib.Path(temp_dir.name).resolve()
        self.home_dir = self.tmp_path / "home"
        self.shortcuts_home = self.home_dir / ".shortcuts"
        self.xdg_data_home = self.home_dir / ".local" / "share"

        sandbox = {
            "HOME": str(self.home_dir),
            "SHORTCUTS_HOME": str(self.shortcuts_home),
            "XDG_DATA_HOME": str(self.xdg_data_home),
            "XDG_CONFIG_HOME": str(self.home_dir / ".config"),
            "BIN_DIR": str(self.home_dir / ".local" / "bin"),
        }
        self.env = {**os.environ, **sandbox}
        # Before anything is written: never touch the real ~/.shortcuts
        refuse_unless_sandboxed(self.tmp_path, self.env, self.home_dir)

        self.shortcuts_home.mkdir(parents=True)
        self.xdg_data_home.mkdir(parents=True)

        patcher = unittest.mock.patch.dict(os.environ, sandbox)
        patcher.start()
        self.addCleanup(patcher.stop)

    # Starting processes

    def launch_args(self, cwd: Optional[str], env: Optional[Mapping[str, str]]) -> dict[str, Any]:
        """cwd and env for a child, after refusing any that leave the sandbox. Every child starts here."""
        run_env = {**self.env, **(env or {})}
        run_cwd = cwd or str(self.home_dir)
        # os.environ too: what the spoke calls in-process in the package reads it
        for checked in (run_env, os.environ):
            refuse_unless_sandboxed(self.tmp_path, checked, run_cwd)
        return {"cwd": run_cwd, "env": run_env}

    def run_cmd(
        self,
        argv: list[str],
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        input: Optional[str] = None,
        timeout: float = 60,
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            argv,
            **self.launch_args(cwd, env),
            input=input,
            stdin=subprocess.DEVNULL if input is None else None,
            text=True,
            capture_output=True,
            timeout=timeout,
        )

    def run_cli(
        self,
        args: list[str],
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        input: Optional[str] = None,
        timeout: float = 60,
    ) -> subprocess.CompletedProcess[str]:
        return self.run_cmd([*CLI, *args], cwd=cwd, env=env, input=input, timeout=timeout)

    def popen(
        self,
        argv: list[str],
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        **kwargs: Any,
    ) -> subprocess.Popen:
        """Start a process and leave it running, with its output on pipes unless kwargs say otherwise."""
        kwargs.setdefault("stdin", subprocess.DEVNULL)
        kwargs.setdefault("stdout", subprocess.PIPE)
        kwargs.setdefault("stderr", subprocess.PIPE)
        return subprocess.Popen(argv, **self.launch_args(cwd, env), **kwargs)

    def popen_cli(self, args: list[str], **kwargs: Any) -> subprocess.Popen:
        return self.popen([*CLI, *args], **kwargs)

    def run_in_terminal(
        self,
        args: list[str],
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        keys: Optional[list[tuple[bytes, Any]]] = None,
        timeout: float = 15,
        stdout_file: Optional[pathlib.Path] = None,
    ) -> tuple[int, str]:
        """Run the CLI on a fresh 30x100 pseudo-terminal, as if typed into a real one.
        keys: (wait until this appears in the output, then type these bytes, or send this signal) pairs.
        stdout_file: send stdout to this file instead, as `> file` would."""
        launch = self.launch_args(cwd, env)
        master, slave = os.openpty()
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
        stdout = open(stdout_file, "wb") if stdout_file else slave
        proc = subprocess.Popen(
            [*CLI, *args],
            **launch,
            stdin=slave,
            stdout=stdout,
            stderr=slave,
            start_new_session=True,
            preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0),
        )
        os.close(slave)
        if stdout_file:
            stdout.close()
        pending = list(keys or [])
        out = b""
        deadline = time.time() + timeout
        try:
            while time.time() < deadline:
                if pending and pending[0][0] in out:
                    action = pending.pop(0)[1]
                    if isinstance(action, bytes):
                        os.write(master, action)
                    else:
                        proc.send_signal(action)
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError:
                        break
                    if not chunk:
                        break
                    out += chunk
                elif proc.poll() is not None:
                    break
        finally:
            os.close(master)
        return proc.wait(timeout=timeout), out.decode("utf-8", errors="replace")

    def bash_complete(self, *words: str) -> list[str]:
        """What `shortcut <words><tab>` offers; the last word is the one being completed."""
        line = ["shortcut", *words]
        driver = (
            self.run_cli(["completion", "bash"]).stdout
            + f"COMP_WORDS=({shlex.join(line)}); COMP_CWORD={len(line) - 1}; _shortcut\n"
            + 'printf "%s\\n" "${COMPREPLY[@]}"\n'
        )
        res = self.run_cmd(["bash", "-c", driver])
        self.assertEqual(res.stderr, "")
        return [line for line in res.stdout.splitlines() if line]

    # Files and folders

    def write_file(self, path: pathlib.Path, content: Union[str, bytes] = "", mode: Optional[int] = None) -> pathlib.Path:
        """Write a file, and any folders above it, inside the temp dir."""
        refuse_unless_inside(self.tmp_path, "a fixture file", path)
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(content, bytes):
            path.write_bytes(content)
        else:
            path.write_text(content, encoding="utf-8")
        if mode is not None:
            path.chmod(mode)
        return path

    def write_files(self, root: pathlib.Path, files: Optional[dict[str, Union[str, bytes]]], mode: Optional[int] = None) -> None:
        for relative, content in (files or {}).items():
            self.write_file(root / relative, content, mode)

    def script(self, name: str, content: str, mode: int = 0o755) -> pathlib.Path:
        """An executable in the temp dir, outside any store."""
        return self.write_file(self.tmp_path / name, content, mode)

    def folder(self, name: str) -> pathlib.Path:
        path = self.tmp_path / name
        path.mkdir()
        return path

    # Stores

    def write_registry(self, store_dir: pathlib.Path, data: Any) -> None:
        """Write this exact data as a store's file, valid or not: a project's .shortcuts, or shortcuts.json."""
        self.write_file(self.registry_path(store_dir), json.dumps(data))

    def registry_path(self, store_dir: pathlib.Path) -> pathlib.Path:
        return store_dir if store_dir.name == ".shortcuts" and store_dir != self.shortcuts_home else store_dir / "shortcuts.json"

    def write_store(self, store_dir: pathlib.Path, shortcuts: dict, **extra: Any) -> None:
        self.write_registry(store_dir, {"version": 2, "shortcuts": shortcuts, **extra})

    def write_import(self, store_dir: pathlib.Path, name: str, data: dict) -> pathlib.Path:
        """A namespace file. A project lists it under "imports" in .shortcuts; the global store just finds it."""
        if store_dir == self.shortcuts_home or store_dir.name != ".shortcuts":
            return self.write_file(store_dir / "imports" / f"{name}.shortcut", json.dumps(data))
        path = self.write_file(store_dir.parent / "imports" / f"{name}.shortcut", json.dumps(data))
        registry = json.loads(store_dir.read_text(encoding="utf-8"))
        registry.setdefault("imports", {})[name] = f"imports/{name}.shortcut"
        store_dir.write_text(json.dumps(registry), encoding="utf-8")
        return path

    def corrupt_store(self, store_dir: pathlib.Path) -> None:
        self.write_file(self.registry_path(store_dir), BROKEN_JSON)

    def load_global(self) -> dict:
        return json.loads((self.shortcuts_home / "shortcuts.json").read_text())["shortcuts"]

    def approve(self, store_dir: pathlib.Path) -> None:
        """Approve everything in a project, as `shortcut update -y` there does."""
        res = self.run_cli(["update", "-y"], cwd=str(store_dir.parent))
        self.assertEqual(res.returncode, 0, res.stderr)

    # Projects and collections

    def project(
        self,
        shortcuts: Optional[dict] = None,
        *,
        name: str = "proj",
        dirs: Sequence[str] = (),
        files: Optional[dict[str, Union[str, bytes]]] = None,
        approved: bool = False,
        **extra: Any,
    ) -> pathlib.Path:
        """A project folder in the temp dir, with a .shortcuts store if given shortcuts.
        dirs and files are relative to the project; extra goes in the store's JSON."""
        proj = self.tmp_path / name
        proj.mkdir()
        for relative in dirs:
            (proj / relative).mkdir(parents=True)
        self.write_files(proj, files)
        if shortcuts is not None:
            self.write_store(proj / ".shortcuts", shortcuts, **extra)
        if approved:
            self.approve(proj / ".shortcuts")
        return proj

    def broken_project(self) -> pathlib.Path:
        """A project whose shortcuts.json can't be parsed."""
        proj = self.project()
        self.corrupt_store(proj / ".shortcuts")
        return proj

    def collection(
        self,
        shortcuts: dict,
        *,
        name: str = "source",
        files: Optional[dict[str, Union[str, bytes]]] = None,
        mode: Optional[int] = None,
        **extra: Any,
    ) -> pathlib.Path:
        """A folder to import from: a .shortcuts at its root with every entry exported, unless it says
        otherwise, and files beside it."""
        src = self.tmp_path / name
        self.write_files(src, files, mode)
        exported = {k: {"export": True, **v} if isinstance(v, dict) else v for k, v in shortcuts.items()}
        self.write_store(src / ".shortcuts", exported, **extra)
        return src

    def commit_to_git(self, repo: pathlib.Path) -> None:
        """Make a folder a git repository holding everything in it."""
        for args in (
            ["init"],
            ["config", "user.name", "TestUser"],
            ["config", "user.email", "test@example.com"],
            ["add", "."],
            ["commit", "-m", "initial commit"],
        ):
            res = self.run_cmd(["git", "-C", str(repo), *args])
            self.assertEqual(res.returncode, 0, res.stderr)

    def install_bundled(self) -> None:
        """Import templates/ with a `shortcut` on PATH for them to call, as after a real install."""
        self.tool_bin = self.tmp_path / "bin"
        self.write_file(self.tool_bin / "shortcut", f'#!/bin/sh\nexec "{sys.executable}" "{SRC}" "$@"\n', 0o755)
        self.env["PATH"] = f"{self.tool_bin}{os.pathsep}{self.env['PATH']}"
        res = self.run_cli(["init", "--global", str(TEMPLATES), "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("Skipped", res.stdout)
