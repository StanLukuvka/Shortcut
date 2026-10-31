#!/usr/bin/env python3

from __future__ import annotations

import argparse
import contextlib
import dataclasses
from datetime import date, datetime, timezone
import fcntl
import hashlib
import json
import os
import pathlib
import re
import select
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import textwrap
import termios
import tty
from typing import Any, Dict, Iterator, List, NoReturn, Optional, Set, Tuple

# Version and Constants
VERSION: str = "1.0.0"
CWD_PREFIX: str = "g_"  # the one marker for "runs in the caller's current directory"
RESERVED_PREFIXES: Set[str] = {CWD_PREFIX}
ALLOWED_FILE: str = "allowed.json"  # in the global store: project root -> fingerprint the user allowed
BUILTIN_COMMANDS: Set[str] = {
    "run", "new", "add", "link", "list", "show", "nano", "edit",
    "location", "rm", "log", "fix", "desktop", "prefix", "init", "allow",
    "migrate", "completion", "help", "--help", "-h", "version", "--version"
}


@dataclasses.dataclass(frozen=True)
class ShortcutEntry:
    name: str
    description: str
    inline: Optional[str] = None
    script: Optional[str] = None
    dir: Optional[str] = None
    icon: Optional[str] = None
    created_by: Optional[str] = None
    created: Optional[str] = None
    log: bool = True

    @classmethod
    def from_dict(cls, name: str, data: Any) -> ShortcutEntry:
        if not isinstance(data, dict):
            raise ValueError(f"'{name}' is not a valid entry: expected a dictionary")
        desc = data.get("description", "")
        if not isinstance(desc, str):
            raise ValueError(f"'{name}' is not a valid entry: 'description' must be a string")
        for field in ("inline", "script", "dir", "icon", "created_by", "created"):
            val = data.get(field)
            if val is not None and not isinstance(val, str):
                raise ValueError(f"'{name}' is not a valid entry: '{field}' must be a string")
        log = data.get("log", True)
        if not isinstance(log, bool):
            raise ValueError(f"'{name}' is not a valid entry: 'log' must be a boolean")
        return cls(
            name=name,
            description=desc,
            inline=data.get("inline"),
            script=data.get("script"),
            dir=data.get("dir"),
            icon=data.get("icon"),
            created_by=data.get("created_by"),
            created=data.get("created"),
            log=log,
        )

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"description": self.description}
        if self.inline is not None:
            d["inline"] = self.inline
        if self.script is not None:
            d["script"] = self.script
        if self.dir is not None:
            d["dir"] = self.dir
        if self.icon is not None:
            d["icon"] = self.icon
        if self.created_by is not None:
            d["created_by"] = self.created_by
        if self.created is not None:
            d["created"] = self.created
        if not self.log:
            d["log"] = False
        return d


@dataclasses.dataclass(frozen=True)
class ResolvedShortcut:
    entry: ShortcutEntry
    store_dir: str
    scope: str            # 'cwd' (g_) | 'prefix' | 'static' | 'project'
    run_cwd: str          # computed execution directory
    hides_global: bool = False  # local entry with the same name as a global static one

    @property
    def base_dir(self) -> str:
        """Where relative script paths resolve: the global store itself, or a project's root."""
        return script_base(self.store_dir)


def die(msg: str, exit_code: int = 1) -> NoReturn:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(exit_code)


def get_global_store_dir() -> str:
    env_dir = os.environ.get("SHORTCUTS_HOME")
    if env_dir:
        return os.path.abspath(os.path.expanduser(env_dir))
    return os.path.abspath(os.path.expanduser("~/.shortcuts"))


def registry_exists(store_dir: str) -> bool:
    return os.path.isfile(os.path.join(store_dir, "shortcuts.json"))


def find_local_store_dir(start_dir: Optional[str] = None) -> Optional[str]:
    curr = pathlib.Path(start_dir or os.getcwd()).resolve()
    global_root = pathlib.Path(get_global_store_dir()).resolve()
    for p in [curr, *curr.parents]:
        candidate = p / ".shortcuts"
        if candidate.resolve() == global_root:
            continue
        if (candidate / "shortcuts.json").is_file():
            return str(candidate)
    return None


@contextlib.contextmanager
def registry_lock(store_dir: str) -> Iterator[None]:
    """Serialize read-modify-write of a store's registry across processes (e.g. two agents at once).
    Not reentrant: never nest it, and never call init_store while holding it."""
    os.makedirs(store_dir, exist_ok=True)
    with open(os.path.join(store_dir, ".lock"), "a") as lock_file:
        fcntl.flock(lock_file, fcntl.LOCK_EX)
        yield


def init_store(store_dir: str, is_global: bool = False) -> None:
    with registry_lock(store_dir):
        if not registry_exists(store_dir):
            data: Dict[str, Any] = {"version": 1, "shortcuts": {}}
            if is_global:
                data["prefixes"] = {}
            else:
                with open(os.path.join(store_dir, ".gitignore"), "w", encoding="utf-8") as f:
                    f.write("logs/\n.lock\n")
            save_registry_atomic(store_dir, data)


def is_global_store(store_dir: str) -> bool:
    return pathlib.Path(store_dir).resolve() == pathlib.Path(get_global_store_dir()).resolve()


def script_base(store_dir: str) -> str:
    """Relative paths in a project's .shortcuts/shortcuts.json resolve against the project root."""
    return store_dir if is_global_store(store_dir) else os.path.dirname(store_dir)


@dataclasses.dataclass(frozen=True)
class ProjectSnapshot:
    """A project store exactly as read once from disk: what `allow` shows is what it hashes."""
    root: str
    registry: Optional[Dict[str, Any]]  # None when shortcuts.json is not a readable JSON object
    scripts: Dict[str, Tuple[str, Optional[bytes]]]  # name -> (resolved path or error, content)
    fingerprint: str


def snapshot_project(store_dir: str) -> ProjectSnapshot:
    root = os.path.realpath(os.path.dirname(store_dir))
    with open(os.path.join(store_dir, "shortcuts.json"), "rb") as f:
        raw = f.read()
    digest = hashlib.sha256(raw)
    try:
        registry = json.loads(raw)
    except ValueError:
        registry = None
    if not isinstance(registry, dict):
        registry = None
    scripts: Dict[str, Tuple[str, Optional[bytes]]] = {}
    shortcuts = registry.get("shortcuts") if registry else None
    for name in sorted(shortcuts) if isinstance(shortcuts, dict) else []:
        script = shortcuts[name].get("script") if isinstance(shortcuts[name], dict) else None
        if not isinstance(script, str):
            continue
        try:
            path = resolve_script_path(root, script)
            with open(path, "rb") as f:
                scripts[name] = (path, f.read())
        except (ValueError, OSError) as e:
            scripts[name] = (str(e), None)
        content_tag = (b"R" + scripts[name][1]) if scripts[name][1] is not None else b"U"
        digest.update(b"\0" + name.encode() + b"\0" + content_tag)
    return ProjectSnapshot(root=root, registry=registry, scripts=scripts, fingerprint=digest.hexdigest())


def load_allowed() -> Dict[str, str]:
    path = os.path.join(get_global_store_dir(), ALLOWED_FILE)
    if not os.path.isfile(path):
        return {}
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except json.JSONDecodeError as e:
        raise ValueError(f"Corrupt {path}: {e}") from e
    return data if isinstance(data, dict) else {}


def set_allowed(store_dir: str, fingerprint: Optional[str]) -> None:
    """Record (or with None, forget) the user's consent for a project store. Takes the global lock."""
    global_dir = get_global_store_dir()
    root = os.path.realpath(os.path.dirname(store_dir))
    with registry_lock(global_dir):
        allowed = load_allowed()
        if fingerprint is None:
            allowed.pop(root, None)
        else:
            allowed[root] = fingerprint
        write_json_atomic(os.path.join(global_dir, ALLOWED_FILE), allowed)


def allowed_registry(store_dir: str) -> Optional[Dict[str, Any]]:
    """The project's registry if the user allowed this exact content, else None."""
    snap = snapshot_project(store_dir)
    if snap.registry is not None and load_allowed().get(snap.root) == snap.fingerprint:
        return snap.registry
    return None


def _only_changed(before: ProjectSnapshot, after: ProjectSnapshot, name: str) -> bool:
    if before.registry is None or after.registry is None:
        return False
    reg_before = dict(before.registry)
    reg_after = dict(after.registry)
    if isinstance(reg_before.get("shortcuts"), dict):
        reg_before["shortcuts"] = {k: v for k, v in reg_before["shortcuts"].items() if k != name}
    if isinstance(reg_after.get("shortcuts"), dict):
        reg_after["shortcuts"] = {k: v for k, v in reg_after["shortcuts"].items() if k != name}
    if reg_before != reg_after:
        return False

    scripts_before = {k: v for k, v in before.scripts.items() if k != name}
    scripts_after = {k: v for k, v in after.scripts.items() if k != name}
    return scripts_before == scripts_after


class AllowKeeper:
    def __init__(self) -> None:
        self.success = True


@contextlib.contextmanager
def keep_allowed(store_dir: str, name: str) -> Iterator[AllowKeeper]:
    """A change made through shortcut itself keeps an allowed project store allowed."""
    is_project = not is_global_store(store_dir)
    was_allowed = is_project and allowed_registry(store_dir) is not None
    before = snapshot_project(store_dir) if was_allowed else None
    keeper = AllowKeeper()
    try:
        yield keeper
    except Exception:
        raise
    else:
        if was_allowed and keeper.success and before is not None:
            after = snapshot_project(store_dir)
            if _only_changed(before, after, name):
                set_allowed(store_dir, after.fingerprint)


def new_local_store_dir(cwd: str) -> str:
    """Where a new local store for cwd goes; refuses when that would be the global store (cwd is ~)."""
    store_dir = os.path.join(cwd, ".shortcuts")
    if pathlib.Path(store_dir).resolve() == pathlib.Path(get_global_store_dir()).resolve():
        die(
            f"{store_dir} is the global store, so it can't hold local shortcuts. "
            "Use a g_ name, pass --static DIR, or run this inside a project folder."
        )
    return store_dir


def load_registry(store_dir: str) -> Dict[str, Any]:
    reg_path = os.path.join(store_dir, "shortcuts.json")
    if not os.path.isfile(reg_path):
        raise FileNotFoundError(f"Registry not found: {reg_path}")
    try:
        with open(reg_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except json.JSONDecodeError as e:
        raise ValueError(f"Corrupt registry in {store_dir}: {e}") from e
    if not isinstance(data, dict):
        raise ValueError(f"Corrupt registry in {store_dir}: root must be a JSON object")
    return data


def save_registry_atomic(store_dir: str, registry_data: Dict[str, Any]) -> None:
    os.makedirs(store_dir, exist_ok=True)
    reg_path = os.path.join(store_dir, "shortcuts.json")
    if os.path.isfile(reg_path):
        try:
            with open(reg_path, "r", encoding="utf-8") as f:
                json.load(f)
        except Exception as e:
            raise ValueError(f"Cannot overwrite corrupt registry in {store_dir}: {e}") from e

    write_json_atomic(reg_path, registry_data)


def write_json_atomic(path: str, data: Any) -> None:
    temp_file = tempfile.NamedTemporaryFile("w", dir=os.path.dirname(path), delete=False, encoding="utf-8")
    try:
        json.dump(data, temp_file, indent=2)
        temp_file.write("\n")
        temp_file.flush()
        os.fsync(temp_file.fileno())
        temp_file.close()
        os.replace(temp_file.name, path)
    except Exception:
        if os.path.exists(temp_file.name):
            try:
                os.unlink(temp_file.name)
            except OSError:
                pass
        raise


def validate_name(name: str) -> None:
    if name in BUILTIN_COMMANDS:
        raise ValueError(f"'{name}' is a built-in command name and cannot be used as a shortcut name.")
    if not re.fullmatch(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", name):
        raise ValueError(
            f"Invalid shortcut name '{name}'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{{0,63}}$ "
            "(alphanumeric start, up to 64 characters of letters, digits, underscores, and dashes)."
        )


def validate_prefix(prefix: str) -> None:
    if prefix in RESERVED_PREFIXES:
        raise ValueError(f"Prefix '{prefix}' is reserved and cannot be registered.")
    if not re.fullmatch(r"^[A-Za-z][A-Za-z0-9]*_$", prefix):
        raise ValueError(
            f"Invalid prefix '{prefix}'. Must match ^[A-Za-z][A-Za-z0-9]*_$ "
            "(letter start, alphanumeric, ending in underscore)."
        )


def resolve_script_path(base_dir: str, script_field: str) -> str:
    """
    Resolves a script (or project dir) path. Absolute paths are used as given.
    Relative paths resolve against base_dir and cannot escape it.
    """
    if os.path.isabs(script_field):
        return script_field

    base = pathlib.Path(base_dir).resolve()
    target = (base / script_field).resolve()

    if not target.is_relative_to(base):
        raise ValueError(
            f"Path traversal detected: '{script_field}' resolves to '{target}', outside '{base}'."
        )
    return str(target)


def project_dir_display(root: str, d: object) -> str:
    """Where a project entry runs, as list and allow show it."""
    if not isinstance(d, str) or not d:
        return root
    try:
        return resolve_script_path(root, d)
    except ValueError:
        return f"{os.path.normpath(os.path.join(root, d))} (outside the project: won't run)"


def prefix_owner(name: str, prefixes: Dict[str, str]) -> Optional[str]:
    """The registered prefix that owns name: the longest one it starts with."""
    matches = [p for p in prefixes if name.startswith(p)]
    return max(matches, key=len) if matches else None


def resolve_shortcut(name: str, caller_cwd: Optional[str] = None, skip_local: bool = False) -> ResolvedShortcut:
    """Find what `shortcut <name>` runs, and where. The name always tells where it runs:
    g_ names run in the caller's directory and nothing else does."""
    caller_cwd = caller_cwd or os.getcwd()
    global_dir = get_global_store_dir()
    global_reg = load_registry(global_dir) if registry_exists(global_dir) else {"shortcuts": {}}
    global_shortcuts = global_reg.get("shortcuts", {})
    global_prefixes: Dict[str, str] = global_reg.get("prefixes", {})

    # 1. g_: runs in the caller's current directory
    if name.startswith(CWD_PREFIX):
        if name not in global_shortcuts:
            raise KeyError(f"Shortcut '{name}' not found in global store.")
        entry = ShortcutEntry.from_dict(name, global_shortcuts[name])
        if entry.dir:
            raise ValueError(
                f"'{name}' has a 'dir', but g_ names always run in your current directory. "
                f"Remove its 'dir', or rename it without the g_ prefix."
            )
        return ResolvedShortcut(entry=entry, store_dir=global_dir, scope="cwd", run_cwd=caller_cwd)

    # 2. Registered prefix: longest matching prefix wins, runs in the prefix's directory
    prefix = prefix_owner(name, global_prefixes)
    if prefix:
        if name not in global_shortcuts:
            raise KeyError(f"Shortcut '{name}' with prefix '{prefix}' not found in global store.")
        entry = ShortcutEntry.from_dict(name, global_shortcuts[name])
        if entry.dir:
            raise ValueError(
                f"'{name}' has a 'dir', but {prefix} names always run in {global_prefixes[prefix]}. "
                f"Remove its 'dir', or rename it."
            )
        return ResolvedShortcut(entry=entry, store_dir=global_dir, scope="prefix", run_cwd=global_prefixes[prefix])

    # 3. Unprefixed: the project's store, read in place, once the user has allowed it
    local_dir = None if skip_local else find_local_store_dir(caller_cwd)
    not_allowed_root: Optional[str] = None
    if local_dir:
        root = os.path.dirname(local_dir)
        local_reg = allowed_registry(local_dir)
        local_shortcuts = local_reg.get("shortcuts", {}) if local_reg else {}
        if local_reg is None:
            not_allowed_root = root
        elif name in local_shortcuts:
            entry = ShortcutEntry.from_dict(name, local_shortcuts[name])
            run_cwd = resolve_script_path(root, entry.dir) if entry.dir else root
            return ResolvedShortcut(
                entry=entry, store_dir=local_dir, scope="project", run_cwd=run_cwd,
                hides_global=name in global_shortcuts,
            )

    # 4. Unprefixed: global static, runs in its fixed 'dir'
    if name in global_shortcuts:
        entry = ShortcutEntry.from_dict(name, global_shortcuts[name])
        if entry.dir:
            return ResolvedShortcut(entry=entry, store_dir=global_dir, scope="static", run_cwd=entry.dir)
        msg = (
            f"'{name}' has no directory to run in: it has no 'dir' and no registered prefix, "
            f"and only g_ names run in your current directory (rename it to {CWD_PREFIX}{name} for that)."
        )
        if m := re.match(r"[A-Za-z][A-Za-z0-9]*_", name):
            msg += f" If '{m.group(0)}' was a prefix you removed, add it back with: shortcut prefix add {m.group(0)} DIR"
        raise ValueError(msg)

    # 5. Not found: hints
    if f"{CWD_PREFIX}{name}" in global_shortcuts:
        raise KeyError(f"Shortcut '{name}' not found. Did you mean '{CWD_PREFIX}{name}'?")
    if not_allowed_root:
        raise KeyError(
            f"Shortcut '{name}' not found. {not_allowed_root} has project shortcuts you haven't allowed; "
            "review them with: shortcut allow"
        )
    raise KeyError(f"Shortcut '{name}' not found.")


def _exit_code(rc: int) -> int:
    return 128 - rc if rc < 0 else rc  # killed by signal N -> shell convention 128+N


def _stop_child(proc: subprocess.Popen, in_group: bool = False) -> int:
    """After Ctrl+C: pass SIGINT on, give the child 5 s to exit, never leave it orphaned."""
    if proc.poll() is None:
        try:
            if in_group:
                os.killpg(proc.pid, signal.SIGINT)
            else:
                proc.send_signal(signal.SIGINT)
        except (ProcessLookupError, OSError):
            pass
    try:
        rc = proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            if in_group:
                os.killpg(proc.pid, signal.SIGKILL)
            else:
                proc.kill()
        except (ProcessLookupError, OSError):
            pass
        rc = proc.wait()
    return rc if rc < 0 else 130


def _tee(
    proc: subprocess.Popen,
    out_fd: int,
    lf: Any,
    stdin_fd: Optional[int] = None,
    in_group: bool = False,
) -> Tuple[int, bytes]:
    """Copy the child's output to the terminal and the log until it exits; with stdin_fd, forward keystrokes
    to out_fd (the pty master). Returns (exit code, last byte written)."""
    last = b"\n"
    readers = [out_fd] if stdin_fd is None else [out_fd, stdin_fd]
    # Pass SIGTERM/SIGHUP on and keep draining, so the child exits first and our cleanup
    # (log footer, restoring the terminal) still runs instead of dying mid-copy.
    def _forward(sig: int, _frame: Any) -> None:
        if proc.poll() is None:
            try:
                if in_group:
                    os.killpg(proc.pid, sig)
                else:
                    proc.send_signal(sig)
            except (ProcessLookupError, OSError):
                pass

    old_handlers = {sig: signal.signal(sig, _forward) for sig in (signal.SIGTERM, signal.SIGHUP)}
    try:
        while True:
            ready, _, _ = select.select(readers, [], [], 0.05)
            if not ready:
                if proc.poll() is not None:
                    break  # exited and drained; a background job may still hold the output open
                continue
            if out_fd in ready:
                try:
                    chunk = os.read(out_fd, 65536)
                except OSError:  # EIO: the pty's last writer closed it
                    chunk = b""
                if not chunk:
                    break
                broken_pipe = False
                try:
                    sys.stdout.buffer.write(chunk)
                    sys.stdout.buffer.flush()
                except BrokenPipeError:
                    broken_pipe = True
                    with open(os.devnull, "wb") as devnull:
                        os.dup2(devnull.fileno(), 1)
                    if out_fd in readers:
                        readers.remove(out_fd)
                    if proc.stdout is not None and not proc.stdout.closed:
                        proc.stdout.close()
                    else:
                        with contextlib.suppress(OSError):
                            os.close(out_fd)
                lf.write(chunk.replace(b"\r\n", b"\n"))
                lf.flush()
                last = chunk[-1:]
                if broken_pipe:
                    break
            if stdin_fd is not None and stdin_fd in ready:
                data = os.read(stdin_fd, 65536)
                if data:
                    os.write(out_fd, data)
                else:
                    readers.remove(stdin_fd)
        rc = proc.wait()
    except KeyboardInterrupt:
        rc = _stop_child(proc, in_group=in_group)
    except OSError:  # our terminal went away (SIGHUP); still reap the child and write the footer
        rc = _stop_child(proc, in_group=in_group)
    finally:
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)
    return _exit_code(rc), last


def _terminal_in_foreground() -> bool:
    """True when stdin, stdout and stderr are one interactive terminal and we own its foreground."""
    try:
        return (
            sys.stdin.isatty() and sys.stdout.isatty() and sys.stderr.isatty()
            and os.tcgetpgrp(sys.stdin.fileno()) == os.getpgrp()
        )
    except OSError:
        return False


def _copy_winsize(from_fd: int, to_fd: int) -> None:
    try:
        fcntl.ioctl(to_fd, termios.TIOCSWINSZ, fcntl.ioctl(from_fd, termios.TIOCGWINSZ, b"\0" * 8))
    except OSError:
        pass


def _run_piped(cmd: List[str], cwd: str, env: Dict[str, str], lf: Any) -> Tuple[int, bytes]:
    in_group = not sys.stdin.isatty()
    # lazy: when stdin is a tty, keep the child in our process group so terminal reads don't get SIGTTIN; SIGTERM reaches only bash, would need job-control forwarding to signal the whole pipeline
    proc = subprocess.Popen(
        cmd,
        cwd=cwd,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        bufsize=0,
        start_new_session=in_group,
    )
    assert proc.stdout is not None
    with proc.stdout:
        return _tee(proc, proc.stdout.fileno(), lf, in_group=in_group)


def _run_in_pty(cmd: List[str], cwd: str, env: Dict[str, str], lf: Any) -> Tuple[int, bytes]:
    """Run the child on its own pseudo-terminal, the way `script` does, so colours, progress bars and
    full-screen programs behave as if it had the real terminal while its output is still logged."""
    stdin_fd, stdout_fd = sys.stdin.fileno(), sys.stdout.fileno()
    saved = termios.tcgetattr(stdin_fd)
    child_attrs = termios.tcgetattr(stdin_fd)
    # Ctrl+Z would stop the child where nothing can resume it; leave suspending to `log: false` shortcuts.
    child_attrs[6][termios.VSUSP] = bytes([os.fpathconf(stdin_fd, "PC_VDISABLE") % 256])
    master, slave = os.openpty()
    try:
        termios.tcsetattr(slave, termios.TCSANOW, child_attrs)
        _copy_winsize(stdout_fd, master)
        try:
            proc = subprocess.Popen(
                cmd, cwd=cwd, env=env, stdin=slave, stdout=slave, stderr=slave,
                start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0),
            )
        finally:
            os.close(slave)
        old_winch = signal.signal(signal.SIGWINCH, lambda *_: _copy_winsize(stdout_fd, master))
        try:
            tty.setraw(stdin_fd, termios.TCSANOW)  # keystrokes, Ctrl+C included, go to the child's pty
            return _tee(proc, master, lf, stdin_fd, in_group=True)
        finally:
            with contextlib.suppress(termios.error, OSError):  # the terminal may be gone after SIGHUP
                termios.tcsetattr(stdin_fd, termios.TCSADRAIN, saved)
            signal.signal(signal.SIGWINCH, old_winch)
    finally:
        with contextlib.suppress(OSError):
            os.close(master)


def run_shortcut(name: str, args: List[str], caller_cwd: Optional[str] = None, skip_local: bool = False) -> int:
    caller_cwd = caller_cwd or os.getcwd()
    resolved = resolve_shortcut(name, caller_cwd, skip_local=skip_local)
    entry = resolved.entry
    if os.environ.get("SHORTCUT_SHOW_DIR", "1") != "0":
        line = f"\u2192 {resolved.run_cwd}"
        if resolved.hides_global:
            line += f"  (project shortcut; your global '{name}' is hidden here: shortcut run --global {name})"
        print(f"\033[2m{line}\033[0m" if sys.stderr.isatty() else line, file=sys.stderr, flush=True)

    # Build command argv
    if entry.inline is not None:
        cmd = ["bash", "-c", entry.inline, name, *args]
    elif entry.script is not None:
        script_path = resolve_script_path(resolved.base_dir, entry.script)
        if not os.path.exists(script_path):
            raise FileNotFoundError(f"Script file not found: {script_path}")
        is_exec = os.access(script_path, os.X_OK)
        header = b""
        try:
            with open(script_path, "rb") as f:
                header = f.read(1024)
        except OSError:
            pass
        is_elf = header.startswith(b"\x7fELF")
        has_shebang = header.startswith(b"#!")
        if is_exec and (has_shebang or is_elf):
            cmd = [script_path, *args]
        elif has_shebang:
            first_line = header.split(b"\n", 1)[0][2:].decode(errors="replace").strip()
            parts = first_line.split(None, 1)
            cmd = [*parts, script_path, *args] if parts else ["bash", script_path, *args]
        else:
            cmd = ["bash", script_path, *args]
    else:
        raise ValueError(f"Entry '{name}' has neither 'inline' nor 'script'.")

    env = os.environ.copy()
    env["SHORTCUT_NAME"] = name
    env["SHORTCUT_CALLER_CWD"] = caller_cwd

    if not entry.log:
        proc = subprocess.Popen(cmd, cwd=resolved.run_cwd, env=env)
        try:
            rc = proc.wait()
        except KeyboardInterrupt:
            rc = _stop_child(proc)
        return _exit_code(rc)

    log_dir = os.path.join(resolved.store_dir, "logs")
    os.makedirs(log_dir, exist_ok=True)
    log_file = os.path.join(log_dir, f"{name}.log")

    now_iso = datetime.now(timezone.utc).isoformat()
    header = f"=== SHORTCUT RUN: {name} | {now_iso} | cwd: {resolved.run_cwd} | args: {shlex.join(args)} ===\n"

    run = _run_in_pty if _terminal_in_foreground() else _run_piped
    with open(log_file, "wb") as lf:  # binary: no decode split across chunk boundaries
        lf.write(header.encode())
        rc, last = run(cmd, resolved.run_cwd, env, lf)
        if last != b"\n":
            lf.write(b"\n")
        lf.write(f"=== exit: {rc} ===\n".encode())

    return rc


def choose_target_store(name: str, static_dir: Optional[str], local: bool, cwd: str) -> Tuple[str, Optional[str]]:
    """Return (store_dir, absolute static dir or None). Creates nothing; _register does."""
    validate_name(name)  # grammar + not a BUILTIN_COMMANDS member
    global_dir = get_global_store_dir()
    prefixes = load_registry(global_dir).get("prefixes", {}) if registry_exists(global_dir) else {}
    prefix = prefix_owner(name, prefixes)
    if name.startswith(CWD_PREFIX):
        if static_dir or local:
            die(f"'{name}' runs in your current directory (g_ name); --static/--local don't apply")
        return global_dir, None
    if prefix:
        if static_dir or local:
            die(f"'{name}' runs in {prefixes[prefix]} ({prefix} name); --static/--local don't apply")
        return global_dir, None
    if static_dir and local:
        die("--static and --local are mutually exclusive")
    if static_dir:
        d = os.path.abspath(os.path.expanduser(static_dir))
        if not os.path.isdir(d):
            die(f"--static dir does not exist: {d}")
        return global_dir, d
    return find_local_store_dir(cwd) or new_local_store_dir(cwd), None


def get_editor_cmd(nano_only: bool = False) -> List[str]:
    """SHORTCUT_EDITOR_CMD (test hook) wins; then nano for `shortcut nano`, else $VISUAL, $EDITOR, nano."""
    if env_cmd := os.environ.get("SHORTCUT_EDITOR_CMD"):
        cmd = shlex.split(env_cmd)
    elif nano_only:
        cmd = ["nano"]
    else:
        cmd = shlex.split(os.environ.get("VISUAL") or os.environ.get("EDITOR") or "nano")
    if not cmd or shutil.which(cmd[0]) is None:
        die(f"editor not found: {cmd[0] if cmd else '(empty)'} (install nano or set $EDITOR)")
    return cmd


def edit_file(editor_cmd: List[str], path: str) -> int:
    if not os.path.isfile(path):
        die(f"Script file not found: {path}")
    return subprocess.run([*editor_cmd, path]).returncode


def edit_inline_roundtrip(name: str, store_dir: str, editor_cmd: List[str]) -> int:
    reg = load_registry(store_dir)
    shortcuts = reg.get("shortcuts", {})
    if name not in shortcuts or "inline" not in shortcuts[name]:
        raise ValueError(f"'{name}' is not an inline shortcut.")

    original_cmd = shortcuts[name]["inline"]
    with tempfile.NamedTemporaryFile("w+", suffix=f"-{name}.sh", delete=False, encoding="utf-8") as tf:
        temp_path = tf.name
        tf.write(original_cmd)
        if not original_cmd.endswith("\n"):
            tf.write("\n")
        tf.flush()

    try:
        res = subprocess.run([*editor_cmd, temp_path])
        if res.returncode != 0:
            raise RuntimeError(f"Editor exited with code {res.returncode}")
        with open(temp_path, "r", encoding="utf-8") as tf_read:
            updated_cmd = tf_read.read().rstrip("\r\n")

        # Save only if modified; re-read under the lock so changes made while editing aren't lost
        if updated_cmd != original_cmd.rstrip("\r\n"):
            with registry_lock(store_dir), keep_allowed(store_dir, name):
                reg = load_registry(store_dir)
                shortcuts = reg.get("shortcuts", {})
                if name not in shortcuts:
                    raise ValueError(f"'{name}' was removed while you were editing it.")
                shortcuts[name]["inline"] = updated_cmd
                save_registry_atomic(store_dir, reg)
            print(f"Updated inline shortcut '{name}'.")
        else:
            print(f"No changes made to '{name}'.")
        return 0
    finally:
        if os.path.exists(temp_path):
            os.unlink(temp_path)


def cmd_run(args: List[str]) -> int:
    if args and args[0] in ("--help", "-h"):
        print("Usage: shortcut run [--global] <name> [args...]")
        return 0
    skip_local = bool(args) and args[0] == "--global"
    if skip_local:
        args = args[1:]
    if not args:
        print("shortcut run: missing shortcut name\nUsage: shortcut run [--global] <name> [args...]", file=sys.stderr)
        return 2
    return run_shortcut(args[0], args[1:], skip_local=skip_local)


def _register(store_dir: str, entry: ShortcutEntry, write_script: Optional[Any] = None) -> None:
    """Add entry to store_dir under the registry lock; write_script() runs first, once the name is known free.
    Creates a missing store. A project store the user created or allowed stays allowed."""
    is_project = not is_global_store(store_dir)
    created = not registry_exists(store_dir)
    if created:
        init_store(store_dir, is_global=not is_project)
        if is_project:
            root = os.path.dirname(store_dir)
            print(f"created project store {store_dir} (its shortcuts work inside {root})", file=sys.stderr)
    with registry_lock(store_dir):
        was_allowed = is_project and (created or allowed_registry(store_dir) is not None)
        before = snapshot_project(store_dir) if was_allowed else None
        reg = load_registry(store_dir)
        shortcuts = reg.setdefault("shortcuts", {})
        if entry.name in shortcuts:
            die(f"Shortcut '{entry.name}' already exists in store {store_dir}")
        if write_script is not None:
            os.makedirs(os.path.join(store_dir, "scripts"), exist_ok=True)
            write_script()
        shortcuts[entry.name] = entry.to_dict()
        save_registry_atomic(store_dir, reg)
        if was_allowed and before is not None:
            after = snapshot_project(store_dir)
            if _only_changed(before, after, entry.name):
                set_allowed(store_dir, after.fingerprint)
        elif is_project:
            print(
                f"note: {os.path.dirname(store_dir)} has project shortcuts you haven't allowed yet; "
                "review them with: shortcut allow",
                file=sys.stderr,
            )


def _stored_script_field(store_dir: str, filename: str) -> Tuple[str, str]:
    """(registry field, absolute path) for a copy kept in <store>/scripts/."""
    abs_path = os.path.join(store_dir, "scripts", filename)
    return os.path.relpath(abs_path, script_base(store_dir)), abs_path


def cmd_new(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut new", description="Create a new shortcut")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("--desc", default=None, help="Shortcut description")
    parser.add_argument("--static", default=None, help="Static directory for global scope")
    parser.add_argument("--local", action="store_true", help="Store locally in nearest project")
    parser.add_argument("--by", default=None, help="Creator identifier")

    # Split args at '--' if present
    inline_cmd_tokens: Optional[List[str]] = None
    if "--" in args:
        dash_idx = args.index("--")
        pre_args = args[:dash_idx]
        inline_cmd_tokens = args[dash_idx + 1:]
    else:
        pre_args = args

    parsed = parser.parse_args(pre_args)
    if inline_cmd_tokens is not None and not inline_cmd_tokens:
        die("Command expected after '--'")

    # Pick the editor before creating anything, so a missing editor leaves nothing half-made.
    # Without a terminal (an agent running this), skip the editor and say how to open it later.
    editor_cmd: Optional[List[str]] = None
    if inline_cmd_tokens is None and (
        "SHORTCUT_EDITOR_CMD" in os.environ or (sys.stdin.isatty() and sys.stdout.isatty())
    ):
        editor_cmd = get_editor_cmd()

    store_dir, static_target = choose_target_store(parsed.name, parsed.static, parsed.local, os.getcwd())
    today = date.today().isoformat()

    if inline_cmd_tokens is not None:
        inline_str = inline_cmd_tokens[0] if len(inline_cmd_tokens) == 1 else shlex.join(inline_cmd_tokens)
        entry = ShortcutEntry(
            name=parsed.name,
            description=parsed.desc if parsed.desc is not None else inline_str,
            inline=inline_str,
            dir=static_target,
            created_by=parsed.by,
            created=today,
        )
        _register(store_dir, entry)
        print(f"Created inline shortcut '{parsed.name}'.")
        return 0

    if registry_exists(store_dir) and parsed.name in load_registry(store_dir).get("shortcuts", {}):
        die(f"Shortcut '{parsed.name}' already exists in store {store_dir}")
    rel_script_path, abs_script_path = _stored_script_field(store_dir, f"{parsed.name}.sh")

    # Edit a temporary copy; the shortcut exists only once the editor exits successfully.
    fd, draft = tempfile.mkstemp(prefix=f"shortcut-{parsed.name}-", suffix=".sh")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as sf:
            sf.write("#!/usr/bin/env bash\nset -euo pipefail\n\n")
        if editor_cmd is not None:
            rc = subprocess.run([*editor_cmd, draft]).returncode
            if rc != 0:
                die(f"editor exited with code {rc}; shortcut '{parsed.name}' was not created")

        def write_script() -> None:
            shutil.copyfile(draft, abs_script_path)
            os.chmod(abs_script_path, 0o755)

        entry = ShortcutEntry(
            name=parsed.name,
            description=parsed.desc if parsed.desc is not None else "(no description)",
            script=rel_script_path,
            dir=static_target,
            created_by=parsed.by,
            created=today,
        )
        _register(store_dir, entry, write_script)
    finally:
        os.unlink(draft)
    print(f"Created script shortcut '{parsed.name}'.")
    if editor_cmd is None:
        print(f"Edit it with: shortcut edit {parsed.name}  (file: {abs_script_path})")
    return 0


def cmd_add(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut add", description="Add an existing script to shortcut store")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("file", help="Path to script file, or '-' for stdin")
    parser.add_argument("--desc", required=True, help="Shortcut description")
    parser.add_argument("--static", default=None, help="Static directory for global scope")
    parser.add_argument("--local", action="store_true", help="Store locally in nearest project")
    parser.add_argument("--by", default=None, help="Creator identifier")

    parsed = parser.parse_args(args)
    if parsed.file == "-":
        content: Optional[str] = sys.stdin.read()
        ext = ".sh"
    else:
        content = None
        src_path = os.path.abspath(os.path.expanduser(parsed.file))
        if not os.path.isfile(src_path):
            die(f"Source file not found: {src_path}")
        ext = pathlib.Path(src_path).suffix or ".sh"

    store_dir, static_target = choose_target_store(parsed.name, parsed.static, parsed.local, os.getcwd())
    rel_script_path, abs_script_path = _stored_script_field(store_dir, f"{parsed.name}{ext}")

    def write_script() -> None:
        if content is not None:
            with open(abs_script_path, "w", encoding="utf-8") as f:
                f.write(content)
            os.chmod(abs_script_path, 0o755)
        else:
            shutil.copy2(src_path, abs_script_path)
            os.chmod(abs_script_path, os.stat(abs_script_path).st_mode | 0o111)

    entry = ShortcutEntry(
        name=parsed.name,
        description=parsed.desc,
        script=rel_script_path,
        dir=static_target,
        created_by=parsed.by,
        created=date.today().isoformat(),
    )
    _register(store_dir, entry, write_script)
    print(f"Added shortcut '{parsed.name}'.")
    return 0


def cmd_link(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut link", description="Link an external script to shortcut store")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("path", help="Path to existing script")
    parser.add_argument("--desc", required=True, help="Shortcut description")
    parser.add_argument("--static", default=None, help="Static directory for global scope")
    parser.add_argument("--local", action="store_true", help="Store locally in nearest project")
    parser.add_argument("--by", default=None, help="Creator identifier")

    parsed = parser.parse_args(args)
    abs_path = os.path.abspath(os.path.expanduser(parsed.path))
    if not os.path.isfile(abs_path):
        die(f"Target script does not exist: {abs_path}")

    store_dir, static_target = choose_target_store(parsed.name, parsed.static, parsed.local, os.getcwd())
    entry = ShortcutEntry(
        name=parsed.name,
        description=parsed.desc,
        script=abs_path,
        dir=static_target,
        created_by=parsed.by,
        created=date.today().isoformat(),
    )
    _register(store_dir, entry)
    print(f"Linked shortcut '{parsed.name}'.")
    return 0


def cmd_list(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut list", description="List available shortcuts")
    parser.add_argument("--names", action="store_true", help="Print bare visible names only")
    parsed = parser.parse_args(args)

    global_dir = get_global_store_dir()
    global_items: List[Tuple[str, str, str]] = []
    global_prefixes: Dict[str, str] = {}
    if registry_exists(global_dir):
        global_reg = load_registry(global_dir)
        global_prefixes = global_reg.get("prefixes", {})
        for name, data in sorted(global_reg.get("shortcuts", {}).items()):
            prefix = prefix_owner(name, global_prefixes)
            if name.startswith(CWD_PREFIX):
                runs_in = "(current dir)"
            elif prefix:
                runs_in = global_prefixes[prefix]
            else:
                runs_in = data.get("dir") or "(no dir: won't run)"
            global_items.append((name, runs_in, data.get("description", "")))

    local_dir = find_local_store_dir(os.getcwd())
    project_root = os.path.dirname(local_dir) if local_dir else None
    local_reg = allowed_registry(local_dir) if local_dir else None
    local_items: List[Tuple[str, str, str]] = []  # (name, runs in, description)
    for name, data in sorted((local_reg or {}).get("shortcuts", {}).items()):
        if name.startswith(CWD_PREFIX) or prefix_owner(name, global_prefixes) or not isinstance(data, dict):
            continue  # g_ and prefix names only come from the global store
        local_items.append((name, project_dir_display(project_root, data.get("dir")), data.get("description", "")))

    if parsed.names:
        for name in dict.fromkeys(n for n, _, _ in local_items + global_items):
            print(name)
        return 0

    local_names = {name for name, _, _ in local_items}
    global_names = {name for name, _, _ in global_items}
    local_items = [(n, r, d + ("  [hides global]" if n in global_names else "")) for n, r, d in local_items]
    global_items = [(n, r, d + ("  [hidden here by project]" if n in local_names else "")) for n, r, d in global_items]

    def table(items: List[Tuple[str, str, str]]) -> None:
        width = max(len(r) for _, r, _ in items)
        for name, runs_in, desc in items:
            print(f"  {name:<20} {runs_in:<{width}}  {desc}")

    sections = 0
    if local_dir and local_reg is None:
        print(f"Project shortcuts in {project_root} are not allowed yet. Review them with: shortcut allow")
        sections += 1
    if local_items:
        print(f"Project shortcuts ({project_root}):")
        table(local_items)
        sections += 1
    if global_items:
        if sections:
            print()
        print("Global shortcuts:")
        table(global_items)
        sections += 1
    if global_prefixes:
        if sections:
            print()
        print("Prefixes:")
        for p, d in sorted(global_prefixes.items()):
            print(f"  {p:<20} -> {d}")

    return 0


def cmd_show(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut show", description="Show shortcut details and content")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)

    resolved = resolve_shortcut(parsed.name)
    entry = resolved.entry

    print(f"Name:        {entry.name}")
    print(f"Scope:       {resolved.scope}" + ("  (hides a global shortcut of the same name)" if resolved.hides_global else ""))
    print(f"Store:       {resolved.store_dir}")
    print(f"Run CWD:     {resolved.run_cwd}")
    print(f"Description: {entry.description}")
    if entry.dir:
        print(f"Dir:         {entry.dir}")
    if entry.created:
        print(f"Created:     {entry.created}")
    if entry.created_by:
        print(f"Created by:  {entry.created_by}")
    if entry.icon:
        print(f"Icon:        {entry.icon}")
    if not entry.log:
        print(f"Log:         False")

    if entry.inline is not None:
        print(f"\nInline command:\n  {entry.inline}")
    elif entry.script is not None:
        script_path = resolve_script_path(resolved.base_dir, entry.script)
        print(f"\nScript ({script_path}):")
        if os.path.isfile(script_path):
            with open(script_path, "r", encoding="utf-8", errors="replace") as sf:
                print(sf.read().rstrip())
        else:
            print(f"  [file missing: {script_path}]")
    return 0


def cmd_location(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut location", description="Show script location")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)

    resolved = resolve_shortcut(parsed.name)
    entry = resolved.entry
    if entry.inline is not None:
        reg_path = os.path.abspath(os.path.join(resolved.store_dir, "shortcuts.json"))
        print(f"{reg_path} (inline)")
    elif entry.script is not None:
        script_path = resolve_script_path(resolved.base_dir, entry.script)
        print(os.path.abspath(script_path))
    else:
        die(f"Shortcut '{entry.name}' has neither inline nor script.")
    return 0


def cmd_rm(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut rm", description="Remove a shortcut")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("-y", "--yes", action="store_true", help="Do not prompt for confirmation")
    parsed = parser.parse_args(args)

    name = parsed.name
    global_dir = get_global_store_dir()
    global_reg = load_registry(global_dir) if registry_exists(global_dir) else {"shortcuts": {}}
    global_shortcuts = global_reg.get("shortcuts", {})
    global_prefixes: Dict[str, str] = global_reg.get("prefixes", {})

    store_dir: Optional[str] = None
    raw_entry: Any = None

    local_dir = find_local_store_dir()
    if local_dir:
        local_reg = allowed_registry(local_dir)
        if local_reg is not None:
            local_shortcuts = local_reg.get("shortcuts", {})
            if (
                name in local_shortcuts
                and not name.startswith(CWD_PREFIX)
                and prefix_owner(name, global_prefixes) is None
            ):
                store_dir = local_dir
                raw_entry = local_shortcuts[name]

    if store_dir is None:
        if name in global_shortcuts:
            store_dir = global_dir
            raw_entry = global_shortcuts[name]

    if store_dir is None:
        die(f"Shortcut '{name}' not found.")

    if not parsed.yes:
        if not sys.stdin.isatty():
            die(f"Cannot remove shortcut '{parsed.name}' without -y in non-interactive environment")
        ans = input(f"Remove shortcut '{parsed.name}'? [y/N]: ")
        if ans.strip().lower() not in ("y", "yes"):
            print("Aborted.")
            return 0

    # 1. Remove from registry
    with registry_lock(store_dir), keep_allowed(store_dir, parsed.name):
        reg = load_registry(store_dir)
        shortcuts = reg.get("shortcuts", {})
        if parsed.name in shortcuts:
            del shortcuts[parsed.name]
            save_registry_atomic(store_dir, reg)

    # 2. Remove the stored copy: only files shortcut keeps in <store>/scripts/, never a linked
    #    script or a project's own file
    script = raw_entry.get("script") if isinstance(raw_entry, dict) else None
    if isinstance(script, str) and script:
        base_dir = script_base(store_dir)
        try:
            stored_path = resolve_script_path(base_dir, script)
            scripts_dir = pathlib.Path(store_dir, "scripts").resolve()
            if pathlib.Path(stored_path).resolve().is_relative_to(scripts_dir) and os.path.isfile(stored_path):
                try:
                    os.unlink(stored_path)
                except OSError:
                    pass
        except ValueError:
            pass

    # 3. Remove log file
    log_path = os.path.join(store_dir, "logs", f"{parsed.name}.log")
    if os.path.isfile(log_path):
        try:
            os.unlink(log_path)
        except OSError:
            pass

    # 4. Remove desktop file if present (global entries only)
    if is_global_store(store_dir):
        xdg_data = os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share")
        desktop_file = os.path.join(xdg_data, "applications", f"shortcut-{parsed.name}.desktop")
        if os.path.isfile(desktop_file):
            try:
                os.unlink(desktop_file)
            except OSError:
                pass

    print(f"Removed shortcut '{parsed.name}'.")
    return 0


def _edit_shortcut(name: str, editor_cmd: List[str]) -> int:
    resolved = resolve_shortcut(name)
    entry = resolved.entry
    if entry.inline is not None:
        return edit_inline_roundtrip(entry.name, resolved.store_dir, editor_cmd)
    if entry.script is not None:
        with keep_allowed(resolved.store_dir, entry.name) as keeper:
            rc = edit_file(editor_cmd, resolve_script_path(resolved.base_dir, entry.script))
            if rc != 0:
                keeper.success = False
            return rc
    die(f"Entry '{name}' has neither inline nor script.")


def cmd_nano(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut nano", description="Edit shortcut script in nano")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)
    return _edit_shortcut(parsed.name, get_editor_cmd(nano_only=True))


def cmd_edit(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut edit", description="Edit shortcut script in editor")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)
    return _edit_shortcut(parsed.name, get_editor_cmd())


def cmd_log(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut log", description="Show last log for shortcut")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)

    resolved = resolve_shortcut(parsed.name)
    log_path = os.path.join(resolved.store_dir, "logs", f"{parsed.name}.log")
    if not os.path.isfile(log_path):
        die(f"No log file found for shortcut '{parsed.name}' at {log_path}")

    with open(log_path, "rb") as f:
        sys.stdout.buffer.write(f.read())
        sys.stdout.buffer.flush()
    return 0


ANSI_ESCAPE = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]")


def plain_text(text: str) -> str:
    """Terminal output as an agent should read it: no colour codes, and each line as it was last drawn."""
    text = ANSI_ESCAPE.sub("", text)
    return "\n".join(line.rstrip("\r").rsplit("\r", 1)[-1] for line in text.split("\n"))


def cmd_fix(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut fix", description="Print diagnostic block for an agent")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)

    resolved = resolve_shortcut(parsed.name)
    entry_dict = resolved.entry.to_dict()
    entry_json = json.dumps(entry_dict, indent=2)

    if resolved.entry.inline is not None:
        content = resolved.entry.inline
    elif resolved.entry.script is not None:
        script_path = resolve_script_path(resolved.base_dir, resolved.entry.script)
        if os.path.isfile(script_path):
            with open(script_path, "r", encoding="utf-8", errors="replace") as f:
                content = f.read()
        else:
            content = f"(script file missing: {script_path})"
    else:
        content = "(no inline command or script)"

    log_path = os.path.join(resolved.store_dir, "logs", f"{parsed.name}.log")
    last_exit = "none"
    if os.path.isfile(log_path):
        try:
            with open(log_path, "r", encoding="utf-8", errors="replace", newline="") as f:
                log_text = f.read()
            m = re.findall(r"=== exit: (-?\d+) ===", log_text)
            if m:
                last_exit = m[-1]
        except OSError:
            log_text = "no log yet"
    else:
        log_text = "no log yet"

    log_text = plain_text(log_text)
    lines = [
        f"=== SHORTCUT FIX: {parsed.name} ===",
        "--- Entry JSON ---",
        entry_json,
        "--- Script / Inline Command ---",
        content.rstrip("\r\n"),
        "--- Last Log ---",
        log_text.rstrip("\r\n"),
        f"Last exit code: {last_exit}",
    ]
    print("\n".join(lines))
    return 0


def format_desktop_file(name: str, description: str, icon: Optional[str]) -> str:
    """
    Freedesktop Desktop Entry Specification escaping:
    Inside double-quoted Exec args, backslash, double quote, backtick, and dollar sign
    must be escaped with a backslash; then every literal '%' must be written '%%'
    (field codes). Name is already validated to [A-Za-z0-9_-].
    The tool is invoked by ABSOLUTE path: desktop sessions often lack ~/.local/bin in PATH.
    That is the command that was run (e.g. ~/.local/bin/shortcut, also for pip/uv installs),
    else Python plus this file.
    """
    launcher = os.path.abspath(sys.argv[0]) if sys.argv and sys.argv[0] else ""
    if os.path.isfile(launcher) and os.access(launcher, os.X_OK):
        tool = shlex.quote(launcher)
    else:
        tool = f"{shlex.quote(sys.executable)} {shlex.quote(os.path.realpath(__file__))}"
    icon_val = icon if icon else "utilities-terminal"
    inner_script = (
        f'{tool} run --global {name}; rc=$?; '
        f'if command -v notify-send >/dev/null 2>&1; then '
        f'if [ $rc -eq 0 ]; then notify-send "shortcut: {name}" "Finished"; '
        f'else notify-send -u critical "shortcut: {name}" "Failed (exit $rc)"; fi; '
        f'fi; '
        f'echo; read -r -p "Press Enter to close..."'
    )
    escaped_inner = "".join(f"\\{c}" if c in ('"', '\\', '`', '$') else c for c in inner_script)
    exec_line = f'bash -c "{escaped_inner}"'.replace("%", "%%")
    description = description.replace("\n", " ")       # Name= is single-line

    lines = [
        "[Desktop Entry]",
        "Version=1.0",
        "Type=Application",
        f"Name={description}",
        f"Exec={exec_line}",
        f"Icon={icon_val}",
        "Terminal=true",
        "Categories=Utility;",
    ]
    return "\n".join(lines) + "\n"


def cmd_desktop(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut desktop", description="Create a desktop launcher entry")
    parser.add_argument("name", help="Shortcut name")
    parsed = parser.parse_args(args)

    resolved = resolve_shortcut(parsed.name)
    if resolved.scope == "project":
        die(f"Cannot create a desktop entry for project shortcut '{parsed.name}': it only works inside its project.")

    content = format_desktop_file(resolved.entry.name, resolved.entry.description or resolved.entry.name, resolved.entry.icon)
    xdg_data = os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share")
    apps_dir = os.path.join(xdg_data, "applications")
    os.makedirs(apps_dir, exist_ok=True)
    desktop_file = os.path.join(apps_dir, f"shortcut-{resolved.entry.name}.desktop")
    with open(desktop_file, "w", encoding="utf-8") as f:
        f.write(content)

    print(f"Created desktop entry: {desktop_file}")
    return 0


def cmd_prefix(args: List[str]) -> int:
    if not args or args[0] in ("-h", "--help", "help"):
        print("Usage:\n  shortcut prefix add <P_> <dir>\n  shortcut prefix rm <P_>\n  shortcut prefix list")
        return 0 if args and args[0] in ("-h", "--help", "help") else 2

    subcmd = args[0]
    global_dir = get_global_store_dir()

    if subcmd == "add":
        if len(args) != 3:
            die("Usage: shortcut prefix add <P_> <dir>")
        prefix = args[1]
        target_dir = args[2]

        validate_prefix(prefix)
        abs_target_dir = os.path.abspath(os.path.expanduser(target_dir))
        if not os.path.isdir(abs_target_dir):
            die(f"Directory does not exist: {abs_target_dir}")

        init_store(global_dir, is_global=True)
        with registry_lock(global_dir):
            reg = load_registry(global_dir)
            prefixes = {**reg.get("prefixes", {}), prefix: abs_target_dir}
            # A name never lies about where it runs: shortcuts with their own 'dir' can't become prefix names.
            clash = sorted(
                n for n, d in reg.get("shortcuts", {}).items()
                if prefix_owner(n, prefixes) == prefix and isinstance(d, dict) and d.get("dir")
            )
            if clash:
                die(
                    f"{', '.join(clash)} run in their own 'dir', but as {prefix} names they would run in "
                    f"{abs_target_dir}. Rename or remove them first."
                )
            reg["prefixes"] = prefixes
            save_registry_atomic(global_dir, reg)
        print(f"Registered prefix '{prefix}' -> {abs_target_dir}")
        return 0

    elif subcmd == "rm":
        force = "--force" in args[1:]
        rest = [a for a in args[1:] if a != "--force"]
        if len(rest) != 1:
            die("Usage: shortcut prefix rm <P_> [--force]")
        prefix = rest[0]
        if not registry_exists(global_dir):
            die(f"Prefix '{prefix}' not found.")
        with registry_lock(global_dir):
            reg = load_registry(global_dir)
            prefixes = reg.get("prefixes", {})
            if prefix not in prefixes:
                die(f"Prefix '{prefix}' not found.")
            users = sorted(n for n in reg.get("shortcuts", {}) if prefix_owner(n, prefixes) == prefix)
            if users and not force:
                die(
                    f"prefix '{prefix}' is still used by: {', '.join(users)}. "
                    "Remove those first (shortcut rm NAME), or pass --force to leave them without a directory."
                )
            del prefixes[prefix]
            save_registry_atomic(global_dir, reg)
        print(f"Removed prefix '{prefix}'.")
        if users:
            print(f"These shortcuts no longer run until the prefix is added back: {', '.join(users)}")
        return 0

    elif subcmd == "list":
        if registry_exists(global_dir):
            reg = load_registry(global_dir)
            prefixes = reg.get("prefixes", {})
            for p, d in sorted(prefixes.items()):
                print(f"  {p:<20} -> {d}")
        return 0

    else:
        die(f"Unknown prefix subcommand '{subcmd}'. Usage: shortcut prefix [add|rm|list]")


def is_git_source(source: str) -> bool:
    return (
        source.startswith(("http://", "https://", "git://", "git@", "ssh://", "file://"))
        or source.endswith(".git")
    )


def merge_init_entries(
    source_base: str,
    dest_store_dir: str,
    dest_registry: Dict[str, Any],
    source_registry: Dict[str, Any],
    global_prefixes: Set[str],
) -> Tuple[List[str], List[str]]:
    """Copy a collection's entries into the global store. Returns (imported, skipped)."""
    imported: List[str] = []
    skipped: List[str] = []
    dest_shortcuts = dest_registry.setdefault("shortcuts", {})
    source_shortcuts = source_registry.get("shortcuts", {})
    if not isinstance(source_shortcuts, dict):
        return imported, skipped

    for name, entry_data in source_shortcuts.items():
        # Validate entry structure
        try:
            ShortcutEntry.from_dict(name, entry_data)
        except ValueError:
            skipped.append(f"{name} (invalid entry)")
            continue
        has_inline = isinstance(entry_data.get("inline"), str)
        has_script = isinstance(entry_data.get("script"), str)
        if has_inline == has_script:
            skipped.append(f"{name} (invalid entry)")
            continue

        if name in dest_shortcuts:
            skipped.append(f"{name} (already exists)")
            continue

        script_field = entry_data.get("script")
        if script_field and os.path.isabs(script_field):
            skipped.append(f"{name} (linked external script)")
            continue

        try:
            validate_name(name)  # untrusted source
        except ValueError as e:
            skipped.append(f"{name!r} (invalid name: {e})")
            continue

        is_cwd_named = name.startswith(CWD_PREFIX)
        has_reg_prefix = any(name.startswith(p) for p in global_prefixes)
        d = entry_data.get("dir")
        if (is_cwd_named or has_reg_prefix) and d:
            skipped.append(f"{name} (has a 'dir', but its name says it runs {'in your current directory' if is_cwd_named else 'in its prefix directory'})")
            continue
        # Prefix mappings are never imported: a prefixed name is imported only when that
        # prefix is already registered here.
        looks_prefixed = re.match(r"^[A-Za-z][A-Za-z0-9]*_", name) and not is_cwd_named
        if looks_prefixed and not has_reg_prefix and not d:
            skipped.append(f"{name} (prefix not registered here; run `shortcut prefix add` first)")
            continue
        # Any other unprefixed entry runs in its fixed dir, which must exist here
        if not is_cwd_named and not has_reg_prefix and (not d or not os.path.isabs(d) or not os.path.isdir(d)):
            skipped.append(f"{name} (static dir missing or not found here: {d})")
            continue

        new_entry = dict(entry_data)
        if script_field:
            curr = pathlib.Path(source_base)
            symlink_found = False
            for part in pathlib.Path(script_field).parts:
                if part in ("", "."):
                    continue
                curr = curr / part
                if os.path.islink(curr):
                    symlink_found = True
                    break
            if symlink_found:
                skipped.append(f"{name} (symlink in source)")
                continue
            try:
                src_path = resolve_script_path(source_base, script_field)
            except ValueError:
                skipped.append(f"{name} (script path escapes the source: {script_field})")
                continue
            if not os.path.isfile(src_path):
                skipped.append(f"{name} (missing source script: {script_field})")
                continue
            dest_rel_script, dest_abs_script = _stored_script_field(
                dest_store_dir, f"{name}{pathlib.Path(script_field).suffix or '.sh'}"
            )
            os.makedirs(os.path.dirname(dest_abs_script), exist_ok=True)
            shutil.copyfile(src_path, dest_abs_script, follow_symlinks=False)
            os.chmod(dest_abs_script, os.stat(dest_abs_script, follow_symlinks=False).st_mode | 0o111)
            new_entry["script"] = dest_rel_script

        dest_shortcuts[name] = new_entry
        imported.append(name)

    return imported, skipped


def cmd_init(args: List[str]) -> int:
    parser = argparse.ArgumentParser(
        prog="shortcut init",
        description="Create an empty project store here, or with --global import a collection into the global store",
    )
    parser.add_argument("source", nargs="?", default=None, help="Local path or git URL to import from (needs --global)")
    parser.add_argument("--global", "-g", dest="is_global", action="store_true", help="Import into global store")
    parsed = parser.parse_args(args)

    if not parsed.is_global:
        if parsed.source:
            die(
                "a project's .shortcuts/ is used in place, never copied: clone the project and run "
                "`shortcut allow` there. To copy a collection into your global store, add --global."
            )
        store_dir = new_local_store_dir(os.getcwd())
        if registry_exists(store_dir):
            print(f"Project store already exists at {store_dir}")
            return 0
        init_store(store_dir)
        set_allowed(store_dir, snapshot_project(store_dir).fingerprint)  # the user just made it
        print(f"Initialized empty project store at {store_dir}")
        return 0

    dest_store_dir = get_global_store_dir()
    init_store(dest_store_dir, is_global=True)
    if not parsed.source:
        print(f"Initialized empty global store at {dest_store_dir}")
        return 0

    source_str = parsed.source
    temp_dir_obj: Optional[tempfile.TemporaryDirectory] = None

    try:
        if is_git_source(source_str):
            temp_dir_obj = tempfile.TemporaryDirectory()
            clone_dir = temp_dir_obj.name
            try:
                subprocess.run(
                    ["git", "clone", "--depth", "1", source_str, clone_dir],
                    capture_output=True,
                    text=True,
                    check=True,
                )
            except (subprocess.CalledProcessError, FileNotFoundError) as e:
                err_detail = e.stderr.strip() if hasattr(e, "stderr") and e.stderr else str(e)
                die(f"Failed to clone git repository from {source_str}: {err_detail}")
            source_root = clone_dir
        else:
            source_root = os.path.abspath(os.path.expanduser(source_str))
            if not os.path.isdir(source_root):
                die(f"Source directory does not exist: {source_root}")

        # A collection's shortcuts.json sits at its root or in .shortcuts/; either way its
        # relative script paths resolve against the source root.
        root_json = os.path.join(source_root, "shortcuts.json")
        dot_json = os.path.join(source_root, ".shortcuts", "shortcuts.json")
        if os.path.isfile(root_json):
            source_store_dir = source_root
        elif os.path.isfile(dot_json):
            source_store_dir = os.path.join(source_root, ".shortcuts")
        else:
            die(f"No shortcuts.json found in {source_root} (checked root and .shortcuts/)")

        source_registry = load_registry(source_store_dir)
        with registry_lock(dest_store_dir):
            dest_registry = load_registry(dest_store_dir)
            imported, skipped = merge_init_entries(
                source_base=source_root,
                dest_store_dir=dest_store_dir,
                dest_registry=dest_registry,
                source_registry=source_registry,
                global_prefixes=set(dest_registry.get("prefixes", {})),
            )
            save_registry_atomic(dest_store_dir, dest_registry)

        if imported:
            print(f"Imported ({len(imported)}):")
            for name in imported:
                print(f"  {name}")
        if skipped:
            print(f"Skipped ({len(skipped)}):")
            for s in skipped:
                print(f"  {s}")
        if imported:
            print("review with: shortcut show <name>")

        return 0

    finally:
        if temp_dir_obj is not None:
            temp_dir_obj.cleanup()


def cmd_allow(args: List[str]) -> int:
    parser = argparse.ArgumentParser(
        prog="shortcut allow",
        description="Show every shortcut in this project's .shortcuts/ and let them run (consent, not a check)",
    )
    parser.add_argument("-y", "--yes", action="store_true", help="Allow without asking")
    parser.add_argument("--revoke", action="store_true", help="Stop allowing this project's shortcuts")
    parsed = parser.parse_args(args)

    store_dir = find_local_store_dir(os.getcwd())
    if store_dir is None:
        die("no project shortcuts here: no .shortcuts/shortcuts.json in this folder or its parents")
    if parsed.revoke:
        set_allowed(store_dir, None)
        print(f"Revoked: shortcuts in {os.path.dirname(store_dir)} are ignored until you allow them again.")
        return 0

    snap = snapshot_project(store_dir)
    if snap.registry is None:
        die(f"cannot read {os.path.join(store_dir, 'shortcuts.json')}: not a JSON object")
    global_dir = get_global_store_dir()
    global_reg = load_registry(global_dir) if registry_exists(global_dir) else {}
    global_names = set(global_reg.get("shortcuts", {}))
    prefixes = global_reg.get("prefixes", {})
    shortcuts = snap.registry.get("shortcuts", {})
    if not isinstance(shortcuts, dict):
        shortcuts = {}

    print(f"Project shortcuts in {snap.root} ({len(shortcuts)}):")
    for name in sorted(shortcuts):
        data = shortcuts[name]
        print(f"\n{name}")
        owner_pref = prefix_owner(name, prefixes)
        if name.startswith(CWD_PREFIX):
            print("  ignored: g_ and prefix names only come from your global store")
        elif owner_pref:
            print(f"  ignored while {owner_pref} is a registered prefix (prefix names come from your global store)")
        if not isinstance(data, dict):
            print("  ignored: not a valid entry")
            continue
        if data.get("description"):
            print(f"  description: {data['description']}")
        if not (name.startswith(CWD_PREFIX) or owner_pref):
            print(f"  runs in: {project_dir_display(snap.root, data.get('dir'))}")
            if name in global_names:
                print(f"  inside this project it hides your global '{name}'")
        if isinstance(data.get("inline"), str):
            print("  command:")
            print(textwrap.indent(data["inline"], "    "))
        elif name in snap.scripts:
            path, content = snap.scripts[name]
            if content is None:
                print(f"  script: cannot read ({path})")
            else:
                print(f"  script: {path}")
                print(textwrap.indent(content.decode("utf-8", errors="replace").rstrip("\n"), "    "))
        else:
            print("  ignored: no command or script")
    print()

    if load_allowed().get(snap.root) == snap.fingerprint:
        print("Already allowed; nothing has changed since.")
        return 0
    if not parsed.yes:
        if not sys.stdin.isatty():
            die("confirm in a terminal (run `shortcut allow` there), or pass -y")
        ans = input(f"Allow these shortcuts to run from {snap.root}? [y/N]: ")
        if ans.strip().lower() not in ("y", "yes"):
            print("Not allowed.")
            return 1
    set_allowed(store_dir, snap.fingerprint)
    print("Allowed. Any change to shortcuts.json or a script it uses needs `shortcut allow` again.")
    return 0


def cmd_migrate(args: List[str]) -> int:
    parser = argparse.ArgumentParser(
        prog="shortcut migrate",
        description="Convert legacy flat ~/.shortcuts/*.sh scripts to global g_<stem> entries",
    )
    parser.parse_args(args)
    global_dir = get_global_store_dir()
    init_store(global_dir, is_global=True)
    with registry_lock(global_dir):
        return _migrate_locked(global_dir)


def _migrate_locked(global_dir: str) -> int:
    reg = load_registry(global_dir)
    shortcuts = reg.setdefault("shortcuts", {})
    os.makedirs(os.path.join(global_dir, "scripts"), exist_ok=True)

    # Legacy flat files: <store>/*.sh (only files directly in global_dir)
    flat_files = [
        f for f in sorted(os.listdir(global_dir))
        if f.endswith(".sh") and (os.path.isfile(os.path.join(global_dir, f)) or os.path.islink(os.path.join(global_dir, f)))
    ]

    if not flat_files:
        print("Nothing to migrate.")
        return 0

    candidates: List[Tuple[str, str, str, str, str, str]] = []
    planned_names: Set[str] = set()

    # Pass 1: validate everything before moving anything
    for filename in flat_files:
        src_path = os.path.join(global_dir, filename)

        if os.path.islink(src_path):
            print(f"skipped {filename} (symlink)")
            continue

        stem = pathlib.Path(filename).stem
        name = stem if stem.startswith("g_") else f"g_{stem}"

        try:
            validate_name(name)
        except ValueError:
            print(f"skipped {filename} (invalid name: {name})")
            continue

        dest_rel_script = f"scripts/{name}.sh"
        dest_abs_script = os.path.join(global_dir, dest_rel_script)

        if os.path.exists(dest_abs_script) or name in shortcuts or name in planned_names:
            print(f"skipped {filename} ({name} already exists)")
            continue

        # Extract description from the first non-shebang # comment
        desc = "(migrated)"
        try:
            with open(src_path, "r", encoding="utf-8", errors="replace") as f:
                for idx, line in enumerate(f):
                    stripped = line.strip()
                    if idx == 0 and stripped.startswith("#!"):
                        continue
                    if stripped.startswith("#"):
                        comment_text = stripped.lstrip("#").strip()
                        if comment_text:
                            desc = comment_text
                            break
        except OSError:
            pass

        planned_names.add(name)
        candidates.append((filename, src_path, name, dest_abs_script, dest_rel_script, desc))

    # Pass 2: move and register validated candidates
    migrated: List[str] = []
    today = date.today().isoformat()

    for filename, src_path, name, dest_abs_script, dest_rel_script, desc in candidates:
        shutil.move(src_path, dest_abs_script)
        os.chmod(dest_abs_script, os.stat(dest_abs_script).st_mode | 0o111)

        entry = ShortcutEntry(
            name=name,
            description=desc,
            script=dest_rel_script,
            created=today,
        )
        shortcuts[name] = entry.to_dict()
        migrated.append(f"{filename} -> {name} ({desc})")

    if migrated:
        save_registry_atomic(global_dir, reg)
        print(f"Migrated ({len(migrated)}):")
        for item in migrated:
            print(f"  {item}")

    return 0


BASH_COMPLETION_SCRIPT: str = """# bash completion for shortcut

_shortcut() {
    local cur prev words cword
    if type _init_completion &>/dev/null; then
        _init_completion || return
    else
        COMPREPLY=()
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    fi

    local subcommands="run new add link list show nano edit location rm log fix desktop prefix init allow migrate completion help"
    local name_commands="run show nano edit location rm log fix desktop"

    if [ "${COMP_CWORD:-1}" -eq 1 ]; then
        local names
        names=$(shortcut list --names 2>/dev/null)
        COMPREPLY=( $(compgen -W "${subcommands} ${names}" -- "$cur") )
        return 0
    fi

    case " ${name_commands} " in
        *" ${prev} "*)
            local names
            names=$(shortcut list --names 2>/dev/null)
            COMPREPLY=( $(compgen -W "${names}" -- "$cur") )
            return 0
            ;;
    esac

    case "$prev" in
        prefix)
            COMPREPLY=( $(compgen -W "add rm list" -- "$cur") )
            return 0
            ;;
        init)
            COMPREPLY=( $(compgen -W "--global" -- "$cur") )
            return 0
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
            return 0
            ;;
    esac
}

complete -F _shortcut shortcut
"""

ZSH_COMPLETION_SCRIPT: str = """# zsh completion for shortcut (load after compinit)

_shortcut() {
    local -a subcommands
    subcommands=(run new add link list show nano edit location rm log fix desktop prefix init allow migrate completion help)

    if (( CURRENT == 2 )); then
        compadd -- $subcommands ${(f)"$(shortcut list --names 2>/dev/null)"}
        return
    fi

    case $words[2] in
        run|show|nano|edit|location|rm|log|fix|desktop)
            (( CURRENT == 3 )) && compadd -- ${(f)"$(shortcut list --names 2>/dev/null)"}
            ;;
        prefix)
            (( CURRENT == 3 )) && compadd -- add rm list
            ;;
        init)
            compadd -- --global
            _files -/
            ;;
        completion)
            (( CURRENT == 3 )) && compadd -- bash zsh fish
            ;;
    esac
}

compdef _shortcut shortcut
"""

FISH_COMPLETION_SCRIPT: str = """# fish completion for shortcut

function __shortcut_names
    shortcut list --names 2>/dev/null
end

complete -c shortcut -f
complete -c shortcut -n __fish_use_subcommand -a "run new add link list show nano edit location rm log fix desktop prefix init allow migrate completion help"
complete -c shortcut -n __fish_use_subcommand -a "(__shortcut_names)"
complete -c shortcut -n "__fish_seen_subcommand_from run show nano edit location rm log fix desktop" -a "(__shortcut_names)"
complete -c shortcut -n "__fish_seen_subcommand_from prefix" -a "add rm list"
complete -c shortcut -n "__fish_seen_subcommand_from init" -F -l global -s g
complete -c shortcut -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
"""

COMPLETION_SCRIPTS: Dict[str, str] = {
    "bash": BASH_COMPLETION_SCRIPT,
    "zsh": ZSH_COMPLETION_SCRIPT,
    "fish": FISH_COMPLETION_SCRIPT,
}


def cmd_completion(args: List[str]) -> int:
    parser = argparse.ArgumentParser(prog="shortcut completion", description="Generate shell completion script")
    parser.add_argument("shell", choices=sorted(COMPLETION_SCRIPTS), nargs="?", default="bash", help="Shell type")
    parsed = parser.parse_args(args)
    sys.stdout.write(COMPLETION_SCRIPTS[parsed.shell])
    return 0


def cmd_help(args: List[str]) -> int:
    print_usage()
    return 0


def print_usage() -> None:
    usage_text = """shortcut - run and manage quick scripts and commands

Usage:
  shortcut <name> [args...]                 Run shortcut <name> (prints its run directory to stderr)
  shortcut run [--global] <name> [args...]  Run shortcut <name> (--global skips project shortcuts)
  shortcut new <name> [--desc D] [--static DIR | --local] [--by C] [-- CMD...]
                                            Create an inline shortcut, or a script opened in $EDITOR
  shortcut add <name> <file|-> --desc D [--static DIR | --local] [--by C]
                                            Add a script copy to store
  shortcut link <name> <path> --desc D [--static DIR | --local] [--by C]
                                            Link an external script by path
  shortcut list [--names]                   List visible shortcuts and prefixes
  shortcut show <name>                      Show shortcut metadata and script contents
  shortcut nano <name>                      Edit shortcut script or inline command in nano
  shortcut edit <name>                      Edit in $VISUAL, $EDITOR, or nano
  shortcut location <name>                  Print path of script or registry (inline)
  shortcut rm <name> [-y]                   Remove shortcut and associated files
  shortcut log <name>                       Print the most recent execution log
  shortcut fix <name>                       Print agent diagnostic block
  shortcut desktop <name>                   Create Freedesktop .desktop launcher in applications
  shortcut prefix add <P_> <dir>            Register a directory prefix in global store
  shortcut prefix rm <P_> [--force]         Remove a directory prefix
  shortcut prefix list                      List registered prefixes
  shortcut init                             Create an empty project store (.shortcuts/) here
  shortcut init --global [SOURCE]           Import a collection (folder or git URL) into the global store
  shortcut allow [-y] [--revoke]            Review this project's shortcuts and let them run
  shortcut migrate                          Migrate legacy flat ~/.shortcuts/*.sh to global store
  shortcut completion bash|zsh|fish         Print a shell completion script
  shortcut help, -h, --help                 Show this help message
  shortcut --version                        Show version

Names: g_<name> runs in your current directory; <P_><name> runs in that prefix's directory;
any other name runs in its fixed dir (--static) or at its project's root (.shortcuts/).
A project's shortcuts are ignored until you run `shortcut allow` there.
Set SHORTCUT_SHOW_DIR=0 to hide the "→ <run directory>" line.
"""
    print(usage_text, end="")


def main(argv: Optional[List[str]] = None) -> NoReturn:
    args = sys.argv[1:] if argv is None else argv

    if not args:
        print_usage()
        sys.exit(0)

    first = args[0]

    if first in ("--help", "-h", "help"):
        print_usage()
        sys.exit(0)

    if first in ("--version", "version"):
        print(f"shortcut {VERSION}")
        sys.exit(0)

    if first == "run":
        try:
            rc = cmd_run(args[1:])
            sys.exit(rc)
        except (KeyError, ValueError, OSError, RuntimeError) as e:
            err_msg = e.args[0] if (isinstance(e, KeyError) and e.args) else str(e)
            die(err_msg)

    handlers = {
        "new": cmd_new,
        "add": cmd_add,
        "link": cmd_link,
        "list": cmd_list,
        "show": cmd_show,
        "nano": cmd_nano,
        "edit": cmd_edit,
        "location": cmd_location,
        "rm": cmd_rm,
        "log": cmd_log,
        "fix": cmd_fix,
        "desktop": cmd_desktop,
        "prefix": cmd_prefix,
        "init": cmd_init,
        "migrate": cmd_migrate,
        "allow": cmd_allow,
        "completion": cmd_completion,
    }

    if first in handlers:
        try:
            rc = handlers[first](args[1:])
            sys.exit(rc)
        except (KeyError, ValueError, OSError, RuntimeError) as e:
            err_msg = e.args[0] if (isinstance(e, KeyError) and e.args) else str(e)
            die(err_msg)

    if first.startswith("-"):
        print(f"shortcut: unrecognized option '{first}'\nTry 'shortcut --help' for more information.", file=sys.stderr)
        sys.exit(2)

    # Direct shortcut execution: shortcut <name> [args...]
    try:
        rc = run_shortcut(first, args[1:])
        sys.exit(rc)
    except (KeyError, ValueError, OSError, RuntimeError) as e:
        err_msg = e.args[0] if (isinstance(e, KeyError) and e.args) else str(e)
        die(err_msg)


if __name__ == "__main__":
    main()
