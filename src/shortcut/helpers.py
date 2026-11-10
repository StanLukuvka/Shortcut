"""Constants and the functions that need none of the classes: stores and registries, approvals, arguments,
running a child."""

from __future__ import annotations

import argparse
import contextlib
from datetime import date
import difflib
import hashlib
import json
import os
import pathlib
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
from typing import TYPE_CHECKING, Any, Dict, Iterator, List, NoReturn, Optional, Set, Tuple

if TYPE_CHECKING:
    from .shortcut_entry import ShortcutEntry
    from .group import Group
    from .store import Store


try:
    import fcntl
except ImportError:  # Windows
    fcntl = None  # type: ignore[assignment]

# Version and Constants
VERSION: str = "2.0.0"
WINDOWS: bool = os.name == "nt"
REGISTRY_VERSION: int = 2
CALLER: str = "caller"  # 'cwd' value, and the default: run in the user's current directory
IMPORT_SUFFIX: str = ".shortcut"  # an imported namespace, in the same JSON format as shortcuts.json
BARE_IMPORT: str = "a namespace file needs a name, e.g. docker.shortcut"  # a bare .shortcut is one letter from .shortcuts
PROJECT_FILE: str = ".shortcuts"  # shared and committed; shortcut never writes it
LOCAL_FILE: str = ".shortcuts.local"  # personal; overlays PROJECT_FILE entry by entry
APPROVALS_DIR: str = "approvals"  # in the global store: one file per approved group, hash + snapshot
BINARY_MARK: str = "\0binary:"  # snapshot value for a binary file, followed by its SHA-256
INVALID_MARK: str = "\0not UTF-8, sha256 "  # last line of a text file with bad bytes; shown text alone is ambiguous
CONTROL_CHARS = re.compile("[\x00-\x08\x0b-\x1f\x7f-\x9f\u2028\u2029\u202a-\u202e\u2066-\u2069]")
POSITIONAL = r"\$(?:[1-9@*]|\{[1-9@*])"  # $1, $@, $*, ${1}: how a shell command reads its arguments


def validate_args(name: str, args: Any) -> None:
    def bad(msg: str) -> NoReturn:
        raise ValueError(f"'{name}' is not a valid entry: {msg}")

    if not isinstance(args, list):
        bad("'args' must be a list")
    for i, arg in enumerate(args):
        if not isinstance(arg, dict) or not isinstance(arg.get("name"), str) \
                or not re.fullmatch(r"[A-Za-z][A-Za-z0-9_-]*", arg["name"]):
            bad("every entry in 'args' needs a 'name' (letters, digits, _ and -)")
        unknown = sorted(set(arg) - {"name", "required", "default", "desc", "rest"})
        if unknown:
            bad(f"unknown args field: {', '.join(unknown)}")
        for field in ("default", "desc"):
            if arg.get(field) is not None and not isinstance(arg[field], str):
                bad(f"args '{field}' must be a string")
        for field in ("required", "rest"):
            if not isinstance(arg.get(field, False), bool):
                bad(f"args '{field}' must be a boolean")
        if arg.get("rest") and i != len(args) - 1:
            bad("only the last entry in 'args' can be 'rest'")
        if arg.get("required") and arg.get("default") is not None:
            bad(f"arg '{arg['name']}' can't be both 'required' and have a 'default'")
        if any(other.get("name") == arg["name"] for other in args[:i]):
            bad(f"arg '{arg['name']}' is declared twice")


def is_relative_cwd(cwd: Optional[str]) -> bool:
    """A cwd that means a folder next to the file defining it, so it's only right in that checkout."""
    return bool(cwd) and cwd != CALLER and not os.path.isabs(os.path.expanduser(cwd))


def placement_error(entry: ShortcutEntry, in_project: bool, in_global: bool) -> Optional[str]:
    """Fields only some files can have: 'export' a project's own, 'from' the global store's shortcuts.json."""
    if entry.export and not in_project:
        return f"'{entry.name}' is not a valid entry: 'export' is only for {PROJECT_FILE} and {LOCAL_FILE}"
    if entry.origin is not None and not in_global:
        return f"'{entry.name}' is not a valid entry: 'from' is only for the global store's shortcuts.json"
    return None


def safe(text: str) -> str:
    """Text as it can be shown: control characters are printed, never obeyed. Keeps newlines and tabs."""
    def visible(match: re.Match) -> str:
        code = ord(match.group())
        return f"^{chr(code ^ 0x40)}" if code < 0x80 else f"\\u{code:04x}"
    return CONTROL_CHARS.sub(visible, text)


def write_styled(file: Any, text: str) -> None:
    """Write past SafeOutput, for colour codes of our own. Everything from a file must already be safe()."""
    stream = getattr(file, "stream", file)
    if stream:  # None when the stream was closed
        stream.write(text)


def die(msg: str, exit_code: int = 1) -> NoReturn:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(exit_code)


def get_global_store_dir() -> str:
    return os.path.abspath(os.path.expanduser(os.environ.get("SHORTCUTS_HOME") or "~/.shortcuts"))


def registry_exists(store_dir: str) -> bool:
    return os.path.isfile(os.path.join(store_dir, "shortcuts.json"))


def never_a_project() -> Set[pathlib.Path]:
    """~/.shortcuts is never a project, also when SHORTCUTS_HOME points the global store elsewhere."""
    return {pathlib.Path(get_global_store_dir()).resolve(), (pathlib.Path.home() / PROJECT_FILE).resolve()}


def is_project_root(folder: pathlib.Path) -> bool:
    if (folder / PROJECT_FILE).resolve() in never_a_project():
        return False
    return (folder / PROJECT_FILE).is_file() or (folder / LOCAL_FILE).is_file()


def find_project_root(start_dir: Optional[str] = None) -> Optional[str]:
    curr = pathlib.Path(start_dir or os.getcwd()).resolve()
    return next((str(p) for p in [curr, *curr.parents] if is_project_root(p)), None)


def local_project_root(cwd: str) -> str:
    """Where --local writes: this folder, never a parent. .shortcuts.local is created here if it's missing,
    but not in a subfolder of a project, where it would start a second project."""
    here = os.path.realpath(cwd)
    root = find_project_root(here)
    if root and root != here:
        die(f"{here} is inside the project at {root}. Run this from there.")
    if (pathlib.Path(here) / PROJECT_FILE).resolve() in never_a_project():
        die(f"{here} holds the global store, so it can't hold local shortcuts. Run this inside a project folder.")
    if pathlib.Path(here).is_relative_to(pathlib.Path(get_global_store_dir()).resolve()):
        die(f"{here} is inside the global store, so it can't hold local shortcuts. Run this inside a project folder.")
    if os.path.islink(os.path.join(here, LOCAL_FILE)):
        die(f"{os.path.join(here, LOCAL_FILE)} is a symlink, so writing it would go somewhere else.")
    return here


def no_follow(path: str, flags: int) -> int:
    """open() opener: a repo can commit any file in a project store as a symlink to one of yours."""
    return os.open(path, flags | getattr(os, "O_NOFOLLOW", 0), 0o644)


@contextlib.contextmanager
def registry_lock(store_dir: str) -> Iterator[None]:
    """Serialize read-modify-write of a store's registry across processes (e.g. two agents at once).
    Not reentrant: never nest it, and never call init_store while holding it."""
    lock_path = os.path.join(store_dir, ".lock")
    if not is_global_store(store_dir):  # a project is a repo's root, so its lock stays out of it
        name = hashlib.sha256(os.path.realpath(store_dir).encode()).hexdigest()
        lock_path = os.path.join(get_global_store_dir(), "locks", f"{name}.lock")
    os.makedirs(os.path.dirname(lock_path), exist_ok=True)
    with open(lock_path, "a", opener=no_follow) as lock_file:
        if fcntl:
            fcntl.flock(lock_file, fcntl.LOCK_EX)
        yield


def init_store(store_dir: str) -> None:
    with registry_lock(store_dir):
        if not registry_exists(store_dir):
            save_registry_atomic(store_dir, {"version": REGISTRY_VERSION, "shortcuts": {}})


def is_global_store(store_dir: str) -> bool:
    return pathlib.Path(store_dir).resolve() == pathlib.Path(get_global_store_dir()).resolve()


def load_registry_file(path: str) -> Dict[str, Any]:
    if not os.path.isfile(path):
        raise FileNotFoundError(f"registry not found: {path}")
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (json.JSONDecodeError, UnicodeDecodeError) as e:
        raise ValueError(f"corrupt registry {path}: {e}") from e
    if not isinstance(data, dict):
        raise ValueError(f"corrupt registry {path}: root must be a JSON object")
    version = data.get("version")
    if isinstance(version, int) and version > REGISTRY_VERSION:
        raise ValueError(f"{path} is format version {version}, newer than this shortcut reads; upgrade shortcut")
    return data


def load_registry(store_dir: str) -> Dict[str, Any]:
    return load_registry_file(os.path.join(store_dir, "shortcuts.json"))


def save_registry_file(path: str, registry_data: Dict[str, Any]) -> None:
    if os.path.isfile(path):
        try:
            with open(path, "r", encoding="utf-8") as f:
                json.load(f)
        except Exception as e:
            raise ValueError(f"cannot overwrite corrupt registry {path}: {e}") from e

    write_json_atomic(path, registry_data)


def save_registry_atomic(store_dir: str, registry_data: Dict[str, Any]) -> None:
    os.makedirs(store_dir, exist_ok=True)
    save_registry_file(os.path.join(store_dir, "shortcuts.json"), registry_data)


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
    if not re.fullmatch(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", name):
        raise ValueError(
            f"invalid shortcut name '{name}'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{{0,63}}$ "
            "(alphanumeric start, up to 64 characters of letters, digits, underscores, and dashes)."
        )


def resolve_script_path(base_dir: str, script_field: str) -> str:
    """
    Resolves a script path. Absolute paths are used as given.
    Relative paths resolve against base_dir and cannot escape it.
    """
    if os.path.isabs(script_field):
        return script_field

    base = pathlib.Path(base_dir).resolve()
    target = (base / script_field).resolve()

    if not target.is_relative_to(base):
        raise ValueError(
            f"path traversal detected: '{script_field}' resolves to '{target}', outside '{base}'."
        )
    return str(target)


def file_error(path: str, data: Dict[str, Any], name: str) -> Optional[str]:
    """What's wrong with a file as a whole; entry_errors checks what's inside 'shortcuts'."""
    version = data.get("version")
    known = {"version", "description", "shortcuts"}
    if name:
        if "imports" in data:
            return f"{path} has its own 'imports', but namespaces don't nest"
        if version != REGISTRY_VERSION:
            return f"{path} needs \"version\": {REGISTRY_VERSION}"
        if "shortcuts" not in data:
            return f"{path} needs 'shortcuts'"
    else:
        known.add("imports")
    if not isinstance(data.get("shortcuts", {}), dict):
        return f"corrupt registry {path}: 'shortcuts' must be an object"
    if not isinstance(data.get("description", ""), str):
        return f"corrupt registry {path}: 'description' must be a string"
    unknown = sorted(set(data) - known)
    return f"{path} has an unknown field: {', '.join(unknown)}" if unknown else None


def display_path(group: Group, path: str) -> str:
    rel = os.path.relpath(path, group.script_base)
    return path if rel.startswith("..") else rel


def reviewable(raw: bytes) -> str:
    """A file as it's hashed and shown. Only a compiled program is hidden: a NUL or a bad byte must not
    hide a script bash will still run. The digest keeps two files that display the same from hashing
    the same, so a text file can't copy the mark, or pass for one with a bad byte."""
    digest = hashlib.sha256(raw).hexdigest()
    if raw.startswith((b"\x7fELF", BINARY_MARK.encode())):
        return BINARY_MARK + digest
    text = raw.decode("utf-8", "backslashreplace")
    if text.encode("utf-8") == raw and INVALID_MARK not in text:
        return text
    return f"{text}\n{INVALID_MARK}{digest}\n"


def entry_files(group: Group, shortcuts: Dict[str, Any], origin: str = "") -> Dict[str, str]:
    """What entries point at, as {path: content}. A relative cwd adds where it really leads, so a symlink
    moved under it needs approving; the leading ./ keeps that key apart from any script, whose path is
    normalised. origin keeps two entries of the same name apart."""
    files: Dict[str, str] = {}
    for name, data in shortcuts.items():
        cwd = data.get("cwd") if isinstance(data, dict) else None
        if isinstance(cwd, str) and is_relative_cwd(cwd):
            with contextlib.suppress(ValueError):  # a NUL: it can't run, and the JSON already shows it
                files[f"./cwd of {name}{origin}"] = os.path.realpath(resolve_run_dir(cwd, group, "")) + "\n"
        script = data.get("script") if isinstance(data, dict) else None
        if not isinstance(script, str):
            continue
        try:
            path = resolve_script_path(group.script_base, script)
            if not os.path.isfile(path):
                continue  # a FIFO or device would block or never end; entry_errors reports it as missing
            with open(path, "rb") as f:
                raw = f.read()
        except (ValueError, OSError):
            continue  # shows up as a change the moment it becomes readable
        files[display_path(group, path)] = reviewable(raw)
    return files


def folder_files(group: Group) -> Dict[str, str]:
    """Everything in a namespace's scripts folder, as {path: content}: a script it names can run or source
    a file beside it, so a change to any of them needs approving. A symlinked folder in it is recorded as
    where it leads."""
    files: Dict[str, str] = {}
    if not group.name:
        return files
    for dirpath, dirs, names in os.walk(scripts_dir(group)):
        real = os.path.realpath(dirpath)  # as entry_files resolves a script's path
        for name in dirs:
            link = os.path.join(dirpath, name)
            if os.path.islink(link):
                with contextlib.suppress(OSError):
                    files[display_path(group, os.path.join(real, name))] = f"symlink to {os.readlink(link)}\n"
        for name in names:
            path = os.path.join(real, name)
            with contextlib.suppress(OSError):
                if os.path.isfile(path):  # a FIFO or device would block or never end
                    files[display_path(group, path)] = reviewable(pathlib.Path(path).read_bytes())
    return files


def json_file(group: Group, path: str, data: Dict[str, Any]) -> Dict[str, str]:
    return {display_path(group, path): json.dumps(data, indent=2, sort_keys=True) + "\n"}


def shared_files(group: Group) -> Dict[str, str]:
    """The group's own file and what it names, the JSON normalised, and for a namespace its scripts folder:
    exactly what is hashed, snapshotted and diffed. A project may have no .shortcuts, and then this is empty."""
    if group.local_file and not os.path.exists(group.file):
        return {}
    return {**json_file(group, group.file, group.data), **folder_files(group), **entry_files(group, group.shared_shortcuts)}


def local_files(group: Group) -> Dict[str, str]:
    """The same for .shortcuts.local."""
    if not group.local_file or not os.path.exists(group.local_file):
        return {}
    return {**json_file(group, group.local_file, group.local_data),
            **entry_files(group, group.local_shortcuts, f" in {LOCAL_FILE}")}


def files_hash(files: Dict[str, str]) -> str:
    return hashlib.sha256(json.dumps(sorted(files.items())).encode()).hexdigest()


def approval_path(group: Group) -> str:
    name = hashlib.sha256(group.key.encode()).hexdigest()
    return os.path.join(get_global_store_dir(), APPROVALS_DIR, f"{name}.json")


def load_approval(group: Group) -> Optional[Dict[str, Any]]:
    """The hash + snapshot last approved, and 'local' for .shortcuts.local on its own: a change the CLI
    makes there updates that half alone. They live in the global store, never next to what they approve."""
    try:
        with open(approval_path(group), "r", encoding="utf-8") as f:
            record = json.load(f)
    except (OSError, ValueError):
        return None
    return record if isinstance(record, dict) and isinstance(record.get("snapshot"), dict) else None


def save_approval(group: Group, shared: Dict[str, str], local: Dict[str, str]) -> None:
    global_dir = get_global_store_dir()
    path = approval_path(group)
    with registry_lock(global_dir):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        write_json_atomic(path, {"key": group.key, "hash": files_hash(shared), "snapshot": shared, "local": local})


def local_is_approved(group: Group, record: Optional[Dict[str, Any]]) -> bool:
    return files_hash((record or {}).get("local", {})) == files_hash(local_files(group))


def approval_state(group: Group) -> str:
    """'ok', 'new' (never approved) or 'changed'."""
    if not group.needs_approval:
        return "ok"
    record = load_approval(group)
    if record is None:
        return "new"
    if record.get("hash") == files_hash(shared_files(group)) and local_is_approved(group, record):
        return "ok"
    return "changed" if record["snapshot"] else "new"  # with no snapshot, only CLI writes to .local were recorded


def require_approved(group: Group) -> None:
    state = approval_state(group)
    if state == "new":
        raise ValueError(f"{group.label} isn't approved yet; review it with: {group.update_cmd}")
    if state == "changed":
        raise ValueError(f"{group.label} changed since you approved it; run: {group.update_cmd}")


def resolve_run_dir(cwd: Optional[str], group: Group, caller_cwd: str) -> str:
    if not cwd or cwd == CALLER:
        return caller_cwd
    return os.path.normpath(os.path.join(group.folder, os.path.expanduser(cwd)))


def exec_child(cmd: List[str], cwd: str, env: Dict[str, str]) -> int:
    """The shortcut replaces this process, so its exit code, signals and terminal are its own.
    Windows has no such exec, so there it's a child with our stdio, and only the exit code passes through."""
    if WINDOWS:
        try:
            return subprocess.run(cmd, cwd=cwd, env=env).returncode
        except KeyboardInterrupt:
            return 130
    if sys.stdout:
        sys.stdout.flush()
    os.chdir(cwd)
    for sig in (signal.SIGPIPE, signal.SIGXFSZ):  # Python ignores these, and exec would keep them ignored
        signal.signal(sig, signal.SIG_DFL)
    os.execvpe(cmd[0], cmd, env)


def ask(prompt: str, accept: Tuple[str, ...] = ("y", "yes")) -> bool:
    """The prompt goes to stderr, so it reaches the terminal when stdout is redirected."""
    try:
        sys.stderr.write(safe(prompt))
        sys.stderr.flush()
        return input().strip().lower() in accept
    except (EOFError, KeyboardInterrupt):
        print()
        return False


def is_flag(token: str) -> bool:
    return len(token) > 1 and token.startswith("-")


def usage_line(display: str, spec: List[Dict[str, Any]]) -> str:
    parts = []
    for arg in spec:
        label = arg["name"] + ("..." if arg.get("rest") else "")
        parts.append(f"<{label}>" if arg.get("required") else f"[{label}]")
    return " ".join(["shortcut r", display, *parts])


def one_line(text: str) -> str:
    return text.replace("\n", "\\n")


def arg_lines(spec: List[Dict[str, Any]]) -> List[str]:
    """One line per declared argument: its name, whether it's needed or its default, and its desc."""
    rows = []
    for arg in spec:
        label = arg["name"] + ("..." if arg.get("rest") else "")
        if arg.get("required"):
            need = "required"
        elif arg.get("default") is not None:
            need = f"default: {one_line(shlex.quote(arg['default']))}"
        else:
            need = "optional"
        rows.append((label, need, one_line(arg.get("desc") or "")))
    width = max(len(label) for label, _, _ in rows)
    need_width = max(len(need) for _, need, _ in rows)
    return [f"  {label:<{width}}  {need:<{need_width}}  {desc}".rstrip() for label, need, desc in rows]


def bind_args(display: str, spec: List[Dict[str, Any]], argv: List[str]) -> Tuple[List[str], Dict[str, str]]:
    """Check presence and count against declared args, fill defaults. Returns (argv, SC_ARG_* env).
    --flags are never parsed: they don't count, and stay where they are in argv. A first -- is dropped,
    and every word after it counts, so a value can start with a dash."""
    def fail(msg: str) -> NoReturn:
        print(f"error: {msg}\nusage: {usage_line(display, spec)}", file=sys.stderr)
        if any(arg.get("desc") for arg in spec):
            print("\n".join(arg_lines(spec)), file=sys.stderr)
        sys.exit(2)

    end = argv.index("--") if "--" in argv else len(argv)
    argv = argv[:end] + argv[end + 1:]
    counts = [i >= end or not is_flag(t) for i, t in enumerate(argv)]
    positional = [t for t, c in zip(argv, counts) if c]
    has_rest = bool(spec) and bool(spec[-1].get("rest"))
    if len(positional) > len(spec) and not has_rest:
        fail("too many arguments")
    env: Dict[str, str] = {}
    fill: List[str] = []
    for i, arg in enumerate(spec):
        is_rest = has_rest and i == len(spec) - 1
        values = positional[i:] if is_rest else positional[i:i + 1]
        if not values:
            if arg.get("default") is not None:
                values = [arg["default"]]
                fill.extend(values)
            elif arg.get("required"):
                fail(f"missing <{arg['name']}>")
            else:
                fill.append("")  # keeps a later default at its own position
        var = "SC_ARG_" + arg["name"].upper().replace("-", "_")
        env[var] = shlex.join(values) if is_rest else "".join(values)
    while fill and not fill[-1]:
        fill.pop()
    last = max((i for i, c in enumerate(counts) if c), default=-1)
    return argv[:last + 1] + fill + argv[last + 1:], env


def stored_cwd(value: Optional[str], group: Group) -> Optional[str]:
    """--cwd as typed (relative to the user's shell) -> the 'cwd' field (relative to the defining file)."""
    if value is None or value == CALLER:
        return value
    path = os.path.realpath(os.path.expanduser(value))
    if not os.path.isdir(path):
        die(f"--cwd directory does not exist: {path}")
    if is_global_store(group.store_dir):
        return path
    if pathlib.Path(path).is_relative_to(os.path.realpath(group.store_dir)):
        return os.path.relpath(path, os.path.realpath(group.folder))
    return path


def exclude_from_git(root: str) -> None:
    """Keep .shortcuts.local out of `git add`, without touching a file the project commits."""
    with contextlib.suppress(OSError):
        res = subprocess.run(["git", "rev-parse", "--git-path", "info/exclude"], cwd=root, capture_output=True, text=True)
        if res.returncode != 0:
            return
        path = os.path.join(root, res.stdout.strip())
        os.makedirs(os.path.dirname(path), exist_ok=True)
        text = pathlib.Path(path).read_text(encoding="utf-8") if os.path.exists(path) else ""
        if LOCAL_FILE not in text.splitlines():
            with open(path, "a", encoding="utf-8") as f:
                f.write(("" if not text or text.endswith("\n") else "\n") + LOCAL_FILE + "\n")


def scripts_dir(group: Group) -> str:
    """Where shortcut keeps its copies of a group's scripts, one folder per namespace."""
    return os.path.join(group.script_base, "scripts", *([group.name] if group.name else []))


def _stored_script_field(group: Group, filename: str) -> Tuple[str, str]:
    """(registry field, absolute path) for a copy kept in the group's scripts folder."""
    abs_path = os.path.join(scripts_dir(group), filename)
    return os.path.relpath(abs_path, group.script_base), abs_path


def refuse_symlinks(group: Group, path: str) -> None:
    """A repo can commit scripts/, or one script, as a symlink to a file of yours. Your own stores can do what they like."""
    if not group.needs_approval:
        return
    base = group.script_base
    parts = pathlib.Path(os.path.relpath(path, base)).parts
    for depth in range(1, len(parts) + 1):
        if os.path.islink(os.path.join(base, *parts[:depth])):
            raise ValueError(f"{os.path.join(base, *parts[:depth])} is a symlink, so this would write somewhere else")


def add_global_flag(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--global", dest="skip_local", action="store_true",
                        help="Skip this project's shortcuts, to reach a global one it hides")


def add_entry_flags(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--cwd", default=None, help="Where it runs: caller (default) or a directory")
    parser.add_argument("--local", action="store_true", help="Store it in .shortcuts.local in this directory, not globally")
    parser.add_argument("--namespace", default=None, metavar="NAME",
                        help="Put it in this namespace, creating imports/NAME.shortcut if needed")
    parser.add_argument("--by", default=None, help="Creator identifier")
    parser.add_argument("--arg", action="append", default=[], metavar="NAME[=DEFAULT]",
                        help="Declare an argument: required, or optional with a default. NAME... takes the "
                             "rest of the arguments; NAME...= makes that optional. Repeatable")
    parser.add_argument("--arg-desc", action="append", default=[], metavar="NAME=TEXT",
                        help="Describe a declared argument, for show and usage errors. Repeatable")
    parser.add_argument("--confirm", action="store_true", help="Ask before running it")


def entry_fields(name: str, parsed: argparse.Namespace, group: Group) -> Dict[str, Any]:
    args: Optional[List[Dict[str, Any]]] = None
    if parsed.arg:
        args = []
        for flag in parsed.arg:
            arg_name, has_default, default = flag.partition("=")
            arg: Dict[str, Any] = {"name": arg_name.removesuffix("...")}
            if arg_name.endswith("..."):
                arg["rest"] = True
            if not has_default:
                arg["required"] = True
            elif default or not arg.get("rest"):  # NAME...= is optional with nothing in its place
                arg["default"] = default
            args.append(arg)
        validate_args(name, args)
    for flag in parsed.arg_desc:
        arg_name, has_text, text = flag.partition("=")
        match = [arg for arg in args or [] if arg["name"] == arg_name.removesuffix("...")]
        if not has_text or not match:
            die(f"--arg-desc {flag}: needs NAME=TEXT, with NAME declared by --arg")
        if "desc" in match[0]:
            die(f"--arg-desc {flag}: '{match[0]['name']}' already has a desc")
        match[0]["desc"] = text
    return {
        "cwd": stored_cwd(parsed.cwd, group),
        "created_by": parsed.by,
        "created": date.today().isoformat(),
        "confirm": parsed.confirm,
        "args": args,
    }


def join_command(words: List[str]) -> str:
    """`-- cmd word word` as one inline command: each word stays one word, as typed, and only
    the shortcut's own arguments stay live, because $1 is why someone quoted it past their shell."""
    def quote(word: str) -> str:
        if not re.search(POSITIONAL, word):
            return shlex.quote(word)
        return '"' + re.sub(rf'["\\`]|(?!{POSITIONAL})\$', lambda m: "\\" + m.group(), word) + '"'

    return words[0] if len(words) == 1 else " ".join(quote(word) for word in words)


def stored_copy(group: Group, raw_entry: Any) -> Optional[str]:
    """The copy shortcut keeps of an entry's script, which rm deletes with it: a relative path inside the
    group's scripts folder. new --script never records one there as relative, so it's never one."""
    script = raw_entry.get("script") if isinstance(raw_entry, dict) else None
    if not isinstance(script, str) or not script or os.path.isabs(script):
        return None
    with contextlib.suppress(OSError, ValueError):
        path = resolve_script_path(group.script_base, script)
        if pathlib.Path(path).is_relative_to(pathlib.Path(scripts_dir(group)).resolve()):
            return path
    return None


def kept_script(group: Group, raw_entry: Any) -> Optional[str]:
    """The script an entry names, as a path, for saying it stays."""
    script = raw_entry.get("script") if isinstance(raw_entry, dict) else None
    if not isinstance(script, str) or not script:
        return None
    try:
        return resolve_script_path(group.script_base, script)
    except ValueError:
        return script


def rm_message(group: Group, display: str, raw_entry: Any, copy: Optional[str]) -> str:
    """What rm is about to change, shown above its question."""
    where = f"namespace {group.name}" if group.name else "your global shortcuts"
    lines = [f"Removes '{display}' from {where}: {group.file}"]
    if copy:
        lines.append(f"Deletes its script copy: {copy}")
    elif kept := kept_script(group, raw_entry):
        lines.append(f"Keeps its script: {kept}")
    return "\n".join(lines)


def rm_local_message(group: Group, name: str, unreadable: Optional[str]) -> str:
    """What rm --local is about to change in .shortcuts.local, shown above its question."""
    local = group.local_file
    lines = [f"{unreadable}. {PROJECT_FILE} stays as it is."] if unreadable else []
    if name not in group.local_shortcuts:
        lines.append(f"Hides '{name}' from you: writes \"{name}\": null to {local}.")
    elif group.local_shortcuts[name] is None:
        lines.append(f"Stops hiding '{name}': takes its null out of {local}. The one in {PROJECT_FILE} applies again.")
    else:
        lines.append(f"Removes your '{name}' from {local}.")
        if name in group.shared_shortcuts:
            lines.append(f"The '{name}' in {PROJECT_FILE} applies again.")
        if kept := kept_script(group, group.local_shortcuts[name]):
            lines.append(f"Keeps its script: {kept}")
    return "\n".join(lines)


def is_listed_import(store: Store, ns: str) -> bool:
    """Whether a namespace comes from the store file's "imports" rather than its imports/ folder."""
    imports = store.main.data.get("imports")
    return isinstance(imports, dict) and ns in imports


def remove_namespace_files(group: Group) -> None:
    """A namespace's file in imports/ and its scripts folder. A symlinked folder loses only the link."""
    folder = scripts_dir(group)
    with contextlib.suppress(FileNotFoundError):
        os.unlink(group.file)
    if os.path.islink(folder):
        os.unlink(folder)
    elif os.path.isdir(folder):
        shutil.rmtree(folder)


def diff_files(old: Dict[str, str], new: Dict[str, str]) -> List[str]:
    """Unified diff of two {path: content} maps. No rename detection: a path on one side only
    diffs against "", so any difference in the maps gives at least one line."""
    lines: List[str] = []
    for path in sorted(old.keys() | new.keys()):
        before, after = old.get(path, ""), new.get(path, "")
        if path in old and path in new and before == after:
            continue
        if after.startswith(BINARY_MARK):
            lines.append(f"Binary file {path} changed")
            continue
        if before.startswith(BINARY_MARK):
            before = ""
        shown = path.removeprefix("./")
        changed = list(difflib.unified_diff(
            before.splitlines(keepends=True), after.splitlines(keepends=True),
            fromfile=f"a/{shown}", tofile=f"b/{shown}", lineterm="",
        ))
        lines.extend(line.rstrip("\r\n") for line in changed)
        if not changed:  # an empty file added or removed
            lines.extend([f"--- a/{shown}", f"+++ b/{shown}"])
    return lines


def show_diff(lines: List[str]) -> None:
    """A definition must not be able to rewrite the screen you approve it on."""
    if not sys.stdout:
        return
    lines = [safe(line) for line in lines]
    tty_out = sys.stdout.isatty()
    if tty_out:
        def colour(line: str) -> str:
            if line.startswith(("+++", "---")) or not line.startswith(("+", "-")):
                return line
            return f"\033[{32 if line.startswith('+') else 31}m{line}\033[0m"
        lines = [colour(line) for line in lines]
    text = "\n".join(lines) + "\n"
    pager = os.environ.get("PAGER")
    if tty_out and pager and len(lines) >= shutil.get_terminal_size().lines:
        with contextlib.suppress(OSError, ValueError, subprocess.SubprocessError):
            env = {**os.environ, "LESS": os.environ.get("LESS", "FRX")}
            subprocess.run(shlex.split(pager), input=text.encode(), env=env, check=True)
            return
    write_styled(sys.stdout, text)
    sys.stdout.flush()


def is_git_source(source: str) -> bool:
    return (
        source.startswith(("http://", "https://", "git://", "git@", "ssh://", "file://"))
        or source.endswith(".git")
    )


def apply_import(store_dir: str, entries: Dict[str, Any], scripts: Dict[str, bytes]) -> None:
    init_store(store_dir)
    for path, raw in scripts.items():
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(raw)
        os.chmod(path, os.stat(path).st_mode | 0o111)
    with registry_lock(store_dir):
        registry = load_registry(store_dir)
        registry.setdefault("shortcuts", {}).update(entries)
        save_registry_atomic(store_dir, registry)


def namespace_folder_problem(group: Group, source_root: str, folder: str) -> Optional[str]:
    """Why a namespace in a source can't be copied as it is: a script outside its scripts folder or
    missing, or a symlink, which would copy something else."""
    for name, data in group.shared_shortcuts.items():
        script = data.get("script") if isinstance(data, dict) else None
        if not isinstance(script, str):
            continue
        if not os.path.normpath(script).startswith(folder + os.sep):
            return f"'{name}' has its script outside {folder}/: {script}"
        if not os.path.isfile(os.path.join(source_root, script)):
            return f"'{name}' has a missing script: {script}"
    parts = [os.path.join(source_root, "scripts"), os.path.join(source_root, folder)]
    for dirpath, dirs, names in os.walk(os.path.join(source_root, folder)):
        parts += [os.path.join(dirpath, name) for name in dirs + names]
    links = [os.path.relpath(p, source_root) for p in parts if os.path.islink(p)]
    if links:
        return f"symlink in source: {links[0]}"
    return None


def installed_files(group: Group, imports_dir: str) -> Dict[str, str]:
    """A namespace in imports/ as init shows it: its file and its scripts folder, under imports/."""
    paths = [group.file] + [os.path.join(d, n) for d, _, names in os.walk(scripts_dir(group)) for n in names]
    files: Dict[str, str] = {}
    for path in paths:
        with contextlib.suppress(OSError):
            if os.path.isfile(path):
                files[os.path.join("imports", os.path.relpath(path, imports_dir))] = reviewable(pathlib.Path(path).read_bytes())
    return files


def apply_namespaces(store_dir: str, plans: Dict[str, Dict[str, Tuple[bytes, bool]]], replaced: List[Group]) -> None:
    init_store(store_dir)
    imports_dir = os.path.join(store_dir, "imports")
    with registry_lock(store_dir):
        for group in replaced:
            remove_namespace_files(group)
        for files in plans.values():
            for rel, (raw, executable) in files.items():
                path = os.path.join(imports_dir, rel)
                os.makedirs(os.path.dirname(path), exist_ok=True)
                with open(path, "xb") as f:  # never over a file that appeared since the review
                    f.write(raw)
                if executable:
                    os.chmod(path, os.stat(path).st_mode | 0o111)
