#!/usr/bin/env python3

from __future__ import annotations

import argparse
import contextlib
import dataclasses
from datetime import date, datetime, timezone
import difflib
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
import termios
import tty
from typing import Any, Dict, Iterator, List, NoReturn, Optional, Set, Tuple

# Version and Constants
VERSION: str = "2.0.0"
REGISTRY_VERSION: int = 2  # 1 had g_/prefix names and a 'dir' field; `shortcut migrate` converts it
CALLER: str = "caller"  # 'cwd' value, and the default: run in the user's current directory
LEGACY_PREFIXES: Tuple[str, ...] = ("g_", "glob_", "global_")
IMPORT_SUFFIX: str = ".shortcut"  # an imported namespace, in the same JSON format as shortcuts.json
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


@dataclasses.dataclass(frozen=True)
class ShortcutEntry:
    name: str
    description: str = ""
    inline: Optional[str] = None
    script: Optional[str] = None
    cwd: Optional[str] = None
    icon: Optional[str] = None
    created_by: Optional[str] = None
    created: Optional[str] = None
    log: bool = True
    confirm: bool = False
    args: Optional[List[Dict[str, Any]]] = None

    TEXT_FIELDS = ("inline", "script", "cwd", "icon", "created_by", "created")

    @classmethod
    def from_dict(cls, name: str, data: Any) -> ShortcutEntry:
        def bad(problem: str) -> ValueError:
            return ValueError(f"'{name}' is not a valid entry: {problem}")

        if not isinstance(data, dict):
            raise bad("expected a dictionary")
        if "dir" in data:
            raise ValueError(f"'{name}' still uses the old 'dir' field; convert it with: shortcut migrate")
        # An ignored field could be a safety setting from a newer version, so never ignore one
        unknown = sorted(set(data) - ({f.name for f in dataclasses.fields(cls)} - {"name"}))
        if unknown:
            raise bad(f"unknown field: {', '.join(unknown)}")
        if not isinstance(data.get("description", ""), str):
            raise bad("'description' must be a string")
        for field in cls.TEXT_FIELDS:
            val = data.get(field)
            if val is not None and not isinstance(val, str):
                raise bad(f"'{field}' must be a string")
        for field, default in (("log", True), ("confirm", False)):
            if not isinstance(data.get(field, default), bool):
                raise bad(f"'{field}' must be a boolean")
        if (data.get("inline") is None) == (data.get("script") is None):
            raise bad("it needs exactly one of 'inline' and 'script'")
        if data.get("args") is not None:
            validate_args(name, data["args"])
        return cls(name=name, **data)

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"description": self.description}
        for field in self.TEXT_FIELDS:
            if getattr(self, field) is not None:
                d[field] = getattr(self, field)
        if not self.log:
            d["log"] = False
        if self.confirm:
            d["confirm"] = True
        if self.args is not None:
            d["args"] = self.args
        return d


def safe(text: str) -> str:
    """Text as it can be shown: control characters are printed, never obeyed. Keeps newlines and tabs."""
    def visible(match: re.Match) -> str:
        code = ord(match.group())
        return f"^{chr(code ^ 0x40)}" if code < 0x80 else f"\\u{code:04x}"
    return CONTROL_CHARS.sub(visible, text)


class SafeOutput:
    """Wraps sys.stdout and sys.stderr so nothing printed from a file can rewrite the screen a person
    approves on. A child's output goes to .buffer and isn't touched; write_styled adds our own colour."""

    def __init__(self, stream: Any) -> None:
        self.stream = stream

    def write(self, text: str) -> int:
        return self.stream.write(safe(text))

    def __getattr__(self, name: str) -> Any:
        return getattr(self.stream, name)


def write_styled(file: Any, text: str) -> None:
    """Write past SafeOutput, for colour codes of our own. Everything from a file must already be safe()."""
    stream = getattr(file, "stream", file)
    if stream:  # None when the stream was closed
        stream.write(text)


class Parser(argparse.ArgumentParser):
    """Plain output: on 3.14 argparse adds colour, and SafeOutput would print it as ^[ text."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.color = False


def die(msg: str, exit_code: int = 1) -> NoReturn:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(exit_code)


def get_global_store_dir() -> str:
    return os.path.abspath(os.path.expanduser(os.environ.get("SHORTCUTS_HOME") or "~/.shortcuts"))


def registry_exists(store_dir: str) -> bool:
    return os.path.isfile(os.path.join(store_dir, "shortcuts.json"))


def find_local_store_dir(start_dir: Optional[str] = None) -> Optional[str]:
    curr = pathlib.Path(start_dir or os.getcwd()).resolve()
    # ~/.shortcuts is never a project, also when SHORTCUTS_HOME points the global store elsewhere
    never = {pathlib.Path(get_global_store_dir()).resolve(), (pathlib.Path.home() / ".shortcuts").resolve()}
    for p in [curr, *curr.parents]:
        candidate = p / ".shortcuts"
        if candidate.resolve() in never:
            continue
        if (candidate / "shortcuts.json").is_file():
            return str(candidate)
    return None


def no_follow(path: str, flags: int) -> int:
    """open() opener: a repo can commit any file in a project store as a symlink to one of yours."""
    return os.open(path, flags | os.O_NOFOLLOW, 0o644)


@contextlib.contextmanager
def registry_lock(store_dir: str) -> Iterator[None]:
    """Serialize read-modify-write of a store's registry across processes (e.g. two agents at once).
    Not reentrant: never nest it, and never call init_store while holding it."""
    os.makedirs(store_dir, exist_ok=True)
    with open(os.path.join(store_dir, ".lock"), "a", opener=no_follow) as lock_file:
        fcntl.flock(lock_file, fcntl.LOCK_EX)
        yield


def init_store(store_dir: str) -> None:
    with registry_lock(store_dir):
        if not registry_exists(store_dir):
            if not is_global_store(store_dir):
                with open(os.path.join(store_dir, ".gitignore"), "w", encoding="utf-8", opener=no_follow) as f:
                    f.write(".lock\n")
            save_registry_atomic(store_dir, {"version": REGISTRY_VERSION, "shortcuts": {}})


def is_global_store(store_dir: str) -> bool:
    return pathlib.Path(store_dir).resolve() == pathlib.Path(get_global_store_dir()).resolve()


def new_local_store_dir(cwd: str) -> str:
    """Where a new local store for cwd goes; refuses when that would be the global store (cwd is ~)."""
    store_dir = os.path.join(cwd, ".shortcuts")
    if is_global_store(store_dir):
        die(f"{store_dir} is the global store, so it can't hold local shortcuts. Run this inside a project folder.")
    if os.path.islink(store_dir):
        die(f"{store_dir} is a symlink, so a store there would be written somewhere else.")
    return store_dir


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


@dataclasses.dataclass(frozen=True)
class Group:
    """One JSON file of shortcuts: a store's own shortcuts.json (name ""), or an imported namespace."""
    name: str
    file: str
    store_dir: str
    data: Dict[str, Any]
    error: Optional[str] = None  # why the file can't be used

    @property
    def shortcuts(self) -> Dict[str, Any]:
        shortcuts = self.data.get("shortcuts", {})
        return shortcuts if isinstance(shortcuts, dict) else {}

    @property
    def folder(self) -> str:
        """Where relative 'cwd' values resolve: the folder of the defining file."""
        return os.path.dirname(os.path.abspath(self.file))

    @property
    def script_base(self) -> str:
        """Relative paths resolve against a namespace's folder, a project's root, or the global store."""
        if self.name:
            return self.folder
        return self.store_dir if is_global_store(self.store_dir) else os.path.dirname(self.store_dir)

    def display(self, name: str) -> str:
        return f"{self.name} {name}" if self.name else name

    def log_path(self, name: str) -> str:
        """Always in the global store: a repo could commit a project's logs/ as a symlink and have a run
        write through it, and nothing approved would show that."""
        stem = f"{self.name}.{name}" if self.name else name
        logs = os.path.join(get_global_store_dir(), "logs")
        if not is_global_store(self.store_dir):
            logs = os.path.join(logs, hashlib.sha256(os.path.realpath(self.store_dir).encode()).hexdigest())
        return os.path.join(logs, f"{stem}.log")

    @property
    def needs_approval(self) -> bool:
        return bool(self.name) or not is_global_store(self.store_dir)

    @property
    def key(self) -> str:
        """What the approval is recorded against: the import's path, or the project's."""
        return os.path.realpath(self.file if self.name else os.path.dirname(self.store_dir))

    @property
    def label(self) -> str:
        return self.name or self.key

    @property
    def update_cmd(self) -> str:
        return f"shortcut update {self.name}" if self.name else "shortcut update"


@dataclasses.dataclass(frozen=True)
class Store:
    dir: str
    main: Group
    namespaces: Dict[str, Group]
    problems: List[str]  # load errors: a name that is both a shortcut and a namespace

    @property
    def groups(self) -> List[Group]:
        return [self.main, *self.namespaces.values()]


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
        if version != REGISTRY_VERSION:
            known.add("prefixes")  # 1.x; `shortcut migrate` removes it
    if not isinstance(data.get("shortcuts", {}), dict):
        return f"corrupt registry {path}: 'shortcuts' must be an object"
    if not isinstance(data.get("description", ""), str):
        return f"corrupt registry {path}: 'description' must be a string"
    unknown = sorted(set(data) - known)
    return f"{path} has an unknown field: {', '.join(unknown)}" if unknown else None


def load_group(path: str, store_dir: str, name: str = "") -> Group:
    data: Dict[str, Any] = {}
    error: Optional[str] = None
    try:
        data = load_registry_file(path)
    except FileNotFoundError as e:
        error = str(e) if name else None  # a store without a registry is simply empty
    except (ValueError, OSError) as e:
        error = str(e)
    else:
        error = file_error(path, data, name)
    return Group(name=name, file=path, store_dir=store_dir, data=data, error=error)


def entry_errors(group: Group) -> List[str]:
    """Every entry that couldn't run, found without running anything."""
    errors: List[str] = []
    for name, data in group.shortcuts.items():
        try:
            validate_name(name)
            entry = ShortcutEntry.from_dict(name, data)
            if entry.script is not None and not os.path.isfile(resolve_script_path(group.script_base, entry.script)):
                raise ValueError(f"'{name}' is not a valid entry: script not found: {entry.script}")
        except ValueError as e:
            errors.append(str(e))
    return errors


def load_store(store_dir: str) -> Store:
    """Reads a store and its namespaces. Never raises on bad content: that is reported on the
    Group or Store, so the commands that repair a broken store keep working."""
    main = load_group(os.path.join(store_dir, "shortcuts.json"), store_dir)
    problems: List[str] = []
    sources: Dict[str, str] = {}
    imports_dir = os.path.join(store_dir, "imports")
    if os.path.isdir(imports_dir):
        for filename in sorted(os.listdir(imports_dir)):
            if filename.endswith(IMPORT_SUFFIX):
                sources[filename[:-len(IMPORT_SUFFIX)]] = os.path.join(imports_dir, filename)
    imports = main.data.get("imports", {})
    for name, path in (imports.items() if isinstance(imports, dict) else ()):
        if not isinstance(path, str):
            problems.append(f"import '{name}' in {main.file} must be a path")
        elif not path.endswith(IMPORT_SUFFIX):
            problems.append(f"import '{name}' in {main.file} must be a {IMPORT_SUFFIX} file")
        elif name in sources:
            problems.append(f"namespace '{name}' is imported twice in {store_dir}")
        else:
            sources[name] = os.path.normpath(os.path.join(main.folder, os.path.expanduser(path)))

    namespaces: Dict[str, Group] = {}
    for name, path in sorted(sources.items()):
        group = load_group(path, store_dir, name)
        try:
            validate_name(name)
        except ValueError as e:
            group = dataclasses.replace(group, error=str(e))
        namespaces[name] = group
        if name in main.shortcuts:
            problems.append(f"'{name}' is both a shortcut and a namespace in {store_dir}; rename one")
    return Store(dir=store_dir, main=main, namespaces=namespaces, problems=problems)


def visible_stores(caller_cwd: Optional[str] = None, skip_local: bool = False) -> List[Store]:
    """The stores a name is looked up in, nearest first."""
    local_dir = None if skip_local else find_local_store_dir(caller_cwd)
    return [load_store(d) for d in ([local_dir] if local_dir else []) + [get_global_store_dir()]]


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


def group_files(group: Group) -> Dict[str, str]:
    """The group as {path: content}, the JSON normalised: exactly what is hashed, snapshotted and diffed.
    A relative cwd also adds where it really leads, so a symlink moved under it needs approving; the
    leading ./ keeps that key apart from any script, whose path is normalised."""
    files = {display_path(group, group.file): json.dumps(group.data, indent=2, sort_keys=True) + "\n"}
    for name, data in group.shortcuts.items():
        cwd = data.get("cwd") if isinstance(data, dict) else None
        if isinstance(cwd, str) and cwd and cwd != CALLER and not os.path.isabs(os.path.expanduser(cwd)):
            with contextlib.suppress(ValueError):  # a NUL: it can't run, and the JSON already shows it
                files[f"./cwd of {name}"] = os.path.realpath(resolve_run_dir(cwd, group, "")) + "\n"
        script = data.get("script") if isinstance(data, dict) else None
        if not isinstance(script, str):
            continue
        try:
            path = resolve_script_path(group.script_base, script)
            with open(path, "rb") as f:
                raw = f.read()
        except (ValueError, OSError):
            continue  # shows up as a change the moment it becomes readable
        files[display_path(group, path)] = reviewable(raw)
    return files


def files_hash(files: Dict[str, str]) -> str:
    return hashlib.sha256(json.dumps(sorted(files.items())).encode()).hexdigest()


def approval_path(group: Group) -> str:
    name = hashlib.sha256(group.key.encode()).hexdigest()
    return os.path.join(get_global_store_dir(), APPROVALS_DIR, f"{name}.json")


def load_approval(group: Group) -> Optional[Dict[str, Any]]:
    """The hash + snapshot last approved. They live in the global store, never next to what they approve."""
    try:
        with open(approval_path(group), "r", encoding="utf-8") as f:
            record = json.load(f)
    except (OSError, ValueError):
        return None
    return record if isinstance(record, dict) and isinstance(record.get("snapshot"), dict) else None


def approved_names(group: Group) -> List[str]:
    """The shortcuts and namespaces a group's own file had when last approved, for when it can't be read now."""
    record = load_approval(group)
    try:
        data = json.loads(record["snapshot"][display_path(group, group.file)]) if record else {}
        return [name for field in ("shortcuts", "imports") for name in data.get(field, {})]
    except (KeyError, TypeError, ValueError, AttributeError):
        return []


def save_approval(group: Group, files: Dict[str, str]) -> None:
    global_dir = get_global_store_dir()
    path = approval_path(group)
    with registry_lock(global_dir):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        write_json_atomic(path, {"key": group.key, "hash": files_hash(files), "snapshot": files})


def approval_state(group: Group) -> str:
    """'ok', 'new' (never approved) or 'changed'."""
    if not group.needs_approval:
        return "ok"
    record = load_approval(group)
    if record is None:
        return "new"
    return "ok" if record.get("hash") == files_hash(group_files(group)) else "changed"


def require_approved(group: Group) -> None:
    state = approval_state(group)
    if state == "new":
        raise ValueError(f"{group.label} isn't approved yet; review it with: {group.update_cmd}")
    if state == "changed":
        raise ValueError(f"{group.label} changed since you approved it; run: {group.update_cmd}")


def note_if_unapproved(group: Group) -> None:
    """After a change made through shortcut: say so if it needs approving before it runs."""
    if group.needs_approval and approval_state(load_group(group.file, group.store_dir, group.name)) != "ok":
        print(f"note: {group.label} needs approving before it runs: {group.update_cmd}", file=sys.stderr)


@dataclasses.dataclass(frozen=True)
class ResolvedShortcut:
    entry: ShortcutEntry
    group: Group
    run_cwd: str          # computed execution directory
    hides_global: bool = False  # project entry with the same name as a global one

    @property
    def display(self) -> str:
        return self.group.display(self.entry.name)


def find_entry(
    words: List[str],
    caller_cwd: Optional[str] = None,
    skip_local: bool = False,
    strict: bool = False,
    writes: bool = False,
) -> Tuple[Group, str, Any, List[str], bool]:
    """Find `name` or `namespace name` without judging the entry: (group, name, raw entry, leftover
    words, hides a global one). strict (for run) makes a store's load errors stop lookups in that store; the
    commands that repair a store leave it False. A name found past a broken store gets a warning, or with
    writes (for edit and rm) an error: it may be the wrong shortcut, and --global says it isn't."""
    stores = visible_stores(caller_cwd, skip_local)
    first, rest = words[0], words[1:]
    broken: List[str] = []

    def warn_broken() -> None:
        if writes and broken:
            raise ValueError(
                f"{broken[0]}; this would change the global '{first}'. Repair the file, or add --global"
            )
        for error in broken:
            print(f"warning: {error}; its shortcuts are skipped, so this is the global '{first}'", file=sys.stderr)

    for store in stores:
        problems = ([store.main.error] if store.main.error else []) + store.problems
        if strict and problems and (first in store.main.shortcuts or first in store.namespaces):
            raise ValueError("; ".join(problems))
        if strict and store.main.error and first in approved_names(store.main):
            home = stores[-1]
            if first not in home.main.shortcuts and first not in home.namespaces:
                raise ValueError(f"{store.main.error}; '{first}' was approved as this project's. Repair the file.")
            raise ValueError(
                f"{store.main.error}; '{first}' was approved as this project's, so it won't run the global one "
                f"instead. Repair the file, or run: shortcut run --global {first}"
            )
        if first in store.main.shortcuts and (not rest or first not in store.namespaces):
            hides = store is not stores[-1] and first in stores[-1].main.shortcuts
            warn_broken()
            return store.main, first, store.main.shortcuts[first], rest, hides
        if first in store.namespaces:
            group = store.namespaces[first]
            if group.error:
                raise ValueError(group.error)
            members = ", ".join(sorted(group.shortcuts)) or "(none)"
            if not rest:
                raise ValueError(f"'{first}' is a namespace; run one of its shortcuts: {members}")
            if rest[0] not in group.shortcuts:
                raise KeyError(f"'{rest[0]}' not found in namespace '{first}'. It has: {members}")
            warn_broken()
            return group, rest[0], group.shortcuts[rest[0]], rest[1:], False
        if store.main.error:
            broken.append(store.main.error)
    raise KeyError(" ".join([f"shortcut '{first}' not found.", *broken]))


def resolve_run_dir(cwd: Optional[str], group: Group, caller_cwd: str) -> str:
    if not cwd or cwd == CALLER:
        return caller_cwd
    return os.path.normpath(os.path.join(group.folder, os.path.expanduser(cwd)))


def resolve_shortcut(
    words: List[str],
    caller_cwd: Optional[str] = None,
    skip_local: bool = False,
    strict: bool = False,
    writes: bool = False,
) -> Tuple[ResolvedShortcut, List[str]]:
    """Find what `shortcut run <words>` runs, and where; also returns the words left over as arguments."""
    caller_cwd = caller_cwd or os.getcwd()
    group, name, raw, rest, hides_global = find_entry(words, caller_cwd, skip_local, strict, writes)
    entry = ShortcutEntry.from_dict(name, raw)
    run_cwd = resolve_run_dir(entry.cwd, group, caller_cwd)
    return ResolvedShortcut(entry=entry, group=group, run_cwd=run_cwd, hides_global=hides_global), rest


def resolve_name(words: List[str], skip_local: bool = False, writes: bool = False) -> ResolvedShortcut:
    """For commands that name a shortcut: `name` or `namespace name`, and nothing else."""
    resolved, rest = resolve_shortcut(words, skip_local=skip_local, writes=writes)
    if rest:
        die(f"too many names: {shlex.join(words)}")
    return resolved


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
        except OSError:
            pass
    try:
        rc = proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            if in_group:
                os.killpg(proc.pid, signal.SIGKILL)
            else:
                proc.kill()
        except OSError:
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
            except OSError:
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


def ask(prompt: str, accept: Tuple[str, ...] = ("y", "yes")) -> bool:
    try:
        return input(safe(prompt)).strip().lower() in accept
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


def bind_args(display: str, spec: List[Dict[str, Any]], argv: List[str]) -> Tuple[List[str], Dict[str, str]]:
    """Check presence and count against declared args, fill defaults. Returns (argv, SC_ARG_* env).
    --flags are never parsed: they don't count, and stay where they are in argv."""
    def fail(msg: str) -> NoReturn:
        print(f"error: {msg}\nusage: {usage_line(display, spec)}", file=sys.stderr)
        sys.exit(2)

    positional = [t for t in argv if not is_flag(t)]
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
    last = max((i for i, t in enumerate(argv) if not is_flag(t)), default=-1)
    return argv[:last + 1] + fill + argv[last + 1:], env


def run_shortcut(words: List[str], skip_local: bool = False, assume_yes: bool = False) -> int:
    caller_cwd = os.getcwd()
    resolved, args = resolve_shortcut(words, caller_cwd, skip_local=skip_local, strict=True)
    require_approved(resolved.group)
    entry, name = resolved.entry, resolved.display
    arg_env: Dict[str, str] = {}
    if entry.args is not None:
        args, arg_env = bind_args(name, entry.args, args)
    if not os.path.isdir(resolved.run_cwd):
        raise FileNotFoundError(f"run directory does not exist: {resolved.run_cwd}")
    if entry.confirm and not assume_yes:
        if not sys.stdin.isatty():
            die(f"'{name}' asks for confirmation: run it in a terminal, or pass -y (shortcut r -y {name})")
        if not ask(f"run '{name}' in {os.path.realpath(resolved.run_cwd)}? [y/N] "):
            print("Aborted.", file=sys.stderr)
            return 1
    if sys.stderr and os.environ.get("SHORTCUT_SHOW_DIR", "1") != "0":
        line = f"→ {os.path.realpath(resolved.run_cwd)}"
        if resolved.hides_global:
            line += f"  (project shortcut; your global '{name}' is hidden here: shortcut run --global {name})"
        line = safe(line)
        write_styled(sys.stderr, f"\033[2m{line}\033[0m\n" if sys.stderr.isatty() else f"{line}\n")
        sys.stderr.flush()

    # Build command argv
    if entry.inline is not None:
        cmd = ["bash", "-c", entry.inline, name, *args]
    else:
        script_path = resolve_script_path(resolved.group.script_base, entry.script)
        if not os.path.exists(script_path):
            raise FileNotFoundError(f"script file not found: {script_path}")
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

    env = os.environ.copy()
    env["SHORTCUT_NAME"] = name
    env["SHORTCUT_CALLER_CWD"] = caller_cwd
    env.update(arg_env)

    if not entry.log:
        proc = subprocess.Popen(cmd, cwd=resolved.run_cwd, env=env)
        try:
            rc = proc.wait()
        except KeyboardInterrupt:
            rc = _stop_child(proc)
        return _exit_code(rc)

    log_file = resolved.group.log_path(resolved.entry.name)
    os.makedirs(os.path.dirname(log_file), exist_ok=True)

    now_iso = datetime.now(timezone.utc).isoformat()
    log_header = f"=== SHORTCUT RUN: {name} | {now_iso} | cwd: {resolved.run_cwd} | args: {shlex.join(args)} ===\n"

    run = _run_in_pty if _terminal_in_foreground() else _run_piped
    with open(log_file, "wb") as lf:  # binary: no decode split across chunk boundaries
        lf.write(log_header.encode())
        rc, last = run(cmd, resolved.run_cwd, env, lf)
        if last != b"\n":
            lf.write(b"\n")
        lf.write(f"=== exit: {rc} ===\n".encode())

    return rc


def stored_cwd(value: Optional[str], group: Group) -> Optional[str]:
    """--cwd as typed (relative to the user's shell) -> the 'cwd' field (relative to the defining file)."""
    if value is None or value == CALLER:
        return value
    path = os.path.realpath(os.path.expanduser(value))
    if not os.path.isdir(path):
        die(f"--cwd directory does not exist: {path}")
    if is_global_store(group.store_dir):
        return path
    project = pathlib.Path(os.path.realpath(os.path.dirname(group.store_dir)))
    if pathlib.Path(path).is_relative_to(project):
        return os.path.relpath(path, os.path.realpath(group.folder))
    return path


def target_group(parsed: argparse.Namespace, cwd: str) -> Group:
    """The file a new shortcut goes in. Creates nothing; _register does."""
    validate_name(parsed.name)
    store_dir = (find_local_store_dir(cwd) or new_local_store_dir(cwd)) if parsed.local else get_global_store_dir()
    store = load_store(store_dir)
    group = store.main
    if parsed.namespace:
        validate_name(parsed.namespace)
        if parsed.namespace in store.main.shortcuts:
            die(f"'{parsed.namespace}' is a shortcut in store {store_dir}, so it can't be a namespace")
        new_file = os.path.join(store_dir, "imports", parsed.namespace + IMPORT_SUFFIX)
        group = store.namespaces.get(parsed.namespace) or Group(parsed.namespace, new_file, store_dir, {})
    if group.error:
        die(group.error)
    return group


def get_editor_cmd() -> List[str]:
    """SHORTCUT_EDITOR_CMD (test hook) wins; then $VISUAL, $EDITOR, nano."""
    if env_cmd := os.environ.get("SHORTCUT_EDITOR_CMD"):
        cmd = shlex.split(env_cmd)
    else:
        cmd = shlex.split(os.environ.get("VISUAL") or os.environ.get("EDITOR") or "nano")
    if not cmd or shutil.which(cmd[0]) is None:
        die(f"editor not found: {cmd[0] if cmd else '(empty)'} (install nano or set $EDITOR)")
    return cmd


def edit_file(editor_cmd: List[str], path: str) -> int:
    if not os.path.isfile(path):
        die(f"script file not found: {path}")
    return subprocess.run([*editor_cmd, path]).returncode


def edit_inline_roundtrip(name: str, group: Group, editor_cmd: List[str]) -> int:
    original_cmd = group.shortcuts[name]["inline"]
    with tempfile.NamedTemporaryFile("w+", suffix=f"-{name}.sh", delete=False, encoding="utf-8") as tf:
        temp_path = tf.name
        tf.write(original_cmd)
        if not original_cmd.endswith("\n"):
            tf.write("\n")
        tf.flush()

    try:
        res = subprocess.run([*editor_cmd, temp_path])
        if res.returncode != 0:
            raise RuntimeError(f"editor exited with code {res.returncode}")
        with open(temp_path, "r", encoding="utf-8") as tf_read:
            updated_cmd = tf_read.read().rstrip("\r\n")

        # Save only if modified; re-read under the lock so changes made while editing aren't lost
        if updated_cmd != original_cmd.rstrip("\r\n"):
            with registry_lock(group.store_dir):
                reg = load_registry_file(group.file)
                shortcuts = reg.get("shortcuts", {})
                if name not in shortcuts:
                    raise ValueError(f"'{name}' was removed while you were editing it.")
                shortcuts[name]["inline"] = updated_cmd
                save_registry_file(group.file, reg)
            print(f"Updated inline shortcut '{name}'.")
        else:
            print(f"No changes made to '{name}'.")
        return 0
    finally:
        if os.path.exists(temp_path):
            os.unlink(temp_path)


RUN_USAGE: str = "usage: shortcut run|r [--global] [-y] <name> [args...]\n       shortcut run|r [--global] [-y] <namespace> <name> [args...]"


def cmd_run(args: List[str]) -> int:
    if args and args[0] in ("--help", "-h"):
        print(RUN_USAGE)
        return 0
    skip_local = assume_yes = False
    while args and args[0] in ("--global", "-y", "--yes"):
        skip_local = skip_local or args[0] == "--global"
        assume_yes = assume_yes or args[0] != "--global"
        args = args[1:]
    if not args:
        print(f"shortcut run: missing shortcut name\n{RUN_USAGE}", file=sys.stderr)
        return 2
    return run_shortcut(args, skip_local=skip_local, assume_yes=assume_yes)


def _register(group: Group, entry: ShortcutEntry, write_script: Optional[Any] = None) -> None:
    """Add entry to group's file under the registry lock; write_script() runs first, once the name is known free.
    Creates a missing store or namespace file."""
    store_dir = group.store_dir
    is_project = not is_global_store(store_dir)
    if not registry_exists(store_dir):
        init_store(store_dir)
        if is_project:
            print(f"created project store {store_dir}", file=sys.stderr)
    with registry_lock(store_dir):
        is_new_file = bool(group.name) and not os.path.exists(group.file)
        reg = {"version": REGISTRY_VERSION, "shortcuts": {}} if is_new_file else load_registry_file(group.file)
        shortcuts = reg.setdefault("shortcuts", {})
        if entry.name in shortcuts:
            die(f"shortcut '{entry.name}' already exists in {group.file}")
        if not group.name and entry.name in load_store(store_dir).namespaces:
            die(f"'{entry.name}' is a namespace in store {store_dir}")
        if write_script is not None:
            os.makedirs(scripts_dir(group), exist_ok=True)
            write_script()
        shortcuts[entry.name] = entry.to_dict()
        os.makedirs(os.path.dirname(group.file), exist_ok=True)
        save_registry_file(group.file, reg)
    note_if_unapproved(group)


def scripts_dir(group: Group) -> str:
    """Where shortcut keeps its copies of a group's scripts, one folder per namespace."""
    return os.path.join(group.folder, "scripts", group.name) if group.name else os.path.join(group.store_dir, "scripts")


def _stored_script_field(group: Group, filename: str) -> Tuple[str, str]:
    """(registry field, absolute path) for a copy kept in the group's scripts folder."""
    abs_path = os.path.join(scripts_dir(group), filename)
    return os.path.relpath(abs_path, group.script_base), abs_path


def refuse_symlinks(group: Group, path: str) -> None:
    """A repo can commit scripts/, or one script, as a symlink to a file of yours. Your own stores can do what they like."""
    if not group.needs_approval:
        return
    base = group.folder if group.name else group.store_dir
    parts = pathlib.Path(os.path.relpath(path, base)).parts
    for depth in range(1, len(parts) + 1):
        if os.path.islink(os.path.join(base, *parts[:depth])):
            raise ValueError(f"{os.path.join(base, *parts[:depth])} is a symlink, so this would write somewhere else")


def add_global_flag(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--global", dest="skip_local", action="store_true",
                        help="Skip this project's shortcuts, to reach a global one it hides")


def add_entry_flags(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--cwd", default=None, help="Where it runs: caller (default) or a directory")
    parser.add_argument("--local", action="store_true", help="Store it in this project's .shortcuts/, not globally")
    parser.add_argument("--namespace", default=None, metavar="NAME",
                        help="Put it in this namespace, creating imports/NAME.shortcut if needed")
    parser.add_argument("--by", default=None, help="Creator identifier")
    parser.add_argument("--arg", action="append", default=[], metavar="NAME[=DEFAULT]",
                        help="Declare an argument: required, or optional with a default. Repeatable")
    parser.add_argument("--confirm", action="store_true", help="Ask before running it")


def entry_fields(name: str, parsed: argparse.Namespace, group: Group) -> Dict[str, Any]:
    args: Optional[List[Dict[str, Any]]] = None
    if parsed.arg:
        args = []
        for flag in parsed.arg:
            arg_name, has_default, default = flag.partition("=")
            args.append({"name": arg_name, "default": default} if has_default else {"name": arg_name, "required": True})
        validate_args(name, args)
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


def cmd_new(args: List[str]) -> int:
    parser = Parser(prog="shortcut new", description="Create a new shortcut")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("--desc", default=None, help="Shortcut description")
    add_entry_flags(parser)

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
        die("command expected after '--'")

    # Pick the editor before creating anything, so a missing editor leaves nothing half-made.
    # Without a terminal (an agent running this), skip the editor and say how to open it later.
    # SHORTCUT_EDITOR_CMD is the test hook from get_editor_cmd.
    editor_cmd: Optional[List[str]] = None
    if inline_cmd_tokens is None and (
        "SHORTCUT_EDITOR_CMD" in os.environ or (sys.stdin.isatty() and sys.stdout.isatty())
    ):
        editor_cmd = get_editor_cmd()

    group = target_group(parsed, os.getcwd())
    fields = entry_fields(parsed.name, parsed, group)

    if inline_cmd_tokens is not None:
        inline_str = join_command(inline_cmd_tokens)
        entry = ShortcutEntry(
            name=parsed.name,
            description=parsed.desc if parsed.desc is not None else inline_str,
            inline=inline_str,
            **fields,
        )
        _register(group, entry)
        if not re.search(POSITIONAL, inline_str) and "SC_ARG_" not in inline_str:
            print(f"note: '{parsed.name}' won't see any arguments you give it; add \"$@\" to pass them on", file=sys.stderr)
        print(f"Created inline shortcut '{parsed.name}'.")
        return 0

    if parsed.name in group.shortcuts:
        die(f"shortcut '{parsed.name}' already exists in {group.file}")
    rel_script_path, abs_script_path = _stored_script_field(group, f"{parsed.name}.sh")

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
            refuse_symlinks(group, abs_script_path)
            shutil.copyfile(draft, abs_script_path)
            os.chmod(abs_script_path, 0o755)

        entry = ShortcutEntry(
            name=parsed.name,
            description=parsed.desc if parsed.desc is not None else "(no description)",
            script=rel_script_path,
            **fields,
        )
        _register(group, entry, write_script)
    finally:
        os.unlink(draft)
    print(f"Created script shortcut '{parsed.name}'.")
    if editor_cmd is None:
        print(f"Edit it with: shortcut edit {group.display(parsed.name)}  (file: {abs_script_path})")
    return 0


def cmd_add(args: List[str]) -> int:
    parser = Parser(prog="shortcut add", description="Add an existing script to shortcut store")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("file", help="Path to script file, or '-' for stdin")
    parser.add_argument("--desc", required=True, help="Shortcut description")
    add_entry_flags(parser)

    parsed = parser.parse_args(args)
    if parsed.file == "-":
        content: Optional[str] = sys.stdin.read()
        ext = ".sh"
    else:
        content = None
        src_path = os.path.abspath(os.path.expanduser(parsed.file))
        if not os.path.isfile(src_path):
            die(f"script file not found: {src_path}")
        ext = pathlib.Path(src_path).suffix or ".sh"

    group = target_group(parsed, os.getcwd())
    fields = entry_fields(parsed.name, parsed, group)
    rel_script_path, abs_script_path = _stored_script_field(group, f"{parsed.name}{ext}")

    def write_script() -> None:
        refuse_symlinks(group, abs_script_path)
        if content is not None:
            with open(abs_script_path, "w", encoding="utf-8") as f:
                f.write(content)
            os.chmod(abs_script_path, 0o755)
        else:
            shutil.copy2(src_path, abs_script_path)
            os.chmod(abs_script_path, os.stat(abs_script_path).st_mode | 0o111)

    entry = ShortcutEntry(name=parsed.name, description=parsed.desc, script=rel_script_path, **fields)
    _register(group, entry, write_script)
    print(f"Added shortcut '{parsed.name}'.")
    return 0


def cmd_link(args: List[str]) -> int:
    parser = Parser(prog="shortcut link", description="Link an external script to shortcut store")
    parser.add_argument("name", help="Shortcut name")
    parser.add_argument("path", help="Path to existing script")
    parser.add_argument("--desc", required=True, help="Shortcut description")
    add_entry_flags(parser)

    parsed = parser.parse_args(args)
    abs_path = os.path.abspath(os.path.expanduser(parsed.path))
    if not os.path.isfile(abs_path):
        die(f"script file not found: {abs_path}")

    group = target_group(parsed, os.getcwd())
    entry = ShortcutEntry(name=parsed.name, description=parsed.desc, script=abs_path, **entry_fields(parsed.name, parsed, group))
    _register(group, entry)
    print(f"Linked shortcut '{parsed.name}'.")
    return 0


def cmd_list(args: List[str]) -> int:
    parser = Parser(prog="shortcut list", description="List available shortcuts")
    parser.add_argument("--names", nargs="?", const="", metavar="NAMESPACE",
                        help="Print bare visible names only, or the names in one namespace")
    parsed = parser.parse_args(args)

    stores = visible_stores(os.getcwd())
    global_names = set(stores[-1].main.shortcuts)
    local_names = set(stores[0].main.shortcuts) if len(stores) > 1 and approval_state(stores[0].main) == "ok" else set()
    for store in stores:
        for problem in [*store.problems, *(g.error for g in store.groups if g.error)]:
            print(f"warning: {problem}", file=sys.stderr)

    if parsed.names:
        for store in stores:
            group = store.namespaces.get(parsed.names)
            if group is not None and not group.error and approval_state(group) == "ok":
                print("\n".join(sorted(group.shortcuts)))
            if group is not None or parsed.names in store.main.shortcuts:
                break
        return 0

    if parsed.names is not None:
        names: List[str] = []
        for store in stores:
            names.extend(sorted(store.main.shortcuts) if approval_state(store.main) == "ok" else [])
            names.extend(n for n, g in store.namespaces.items() if not g.error and approval_state(g) == "ok")
        for name in dict.fromkeys(names):
            print(name)
        return 0

    sections = 0

    def section(title: str, group: Group, prefix: str = "") -> None:
        nonlocal sections
        state = approval_state(group)
        if sections:
            print()
        sections += 1
        if state != "ok":
            verb = "aren't approved yet" if state == "new" else "changed since you approved them"
            print(f"{title} {verb}. Review them with: {group.update_cmd}")
            return
        print(f"{title}:")
        if group.data.get("description"):
            print(f"  {group.data['description']}")
        rows = []
        for name, data in sorted(group.shortcuts.items()):
            data = data if isinstance(data, dict) else {}
            desc = str(data.get("description", ""))
            if name in global_names and group.needs_approval and not group.name:
                desc += "  [hides global]"
            elif name in local_names and not group.needs_approval:
                desc += "  [hidden here by project]"
            rows.append((prefix + name, str(data.get("cwd") or CALLER), desc))
        width = max((len(r[1]) for r in rows), default=0)
        for name, mode, desc in rows:
            print(f"  {name:<20} {mode:<{width}}  {desc}")

    for store in stores:
        if store.main.needs_approval:
            if store.main.shortcuts:
                section(f"Project shortcuts ({store.main.key})", store.main)
        elif store.main.shortcuts:
            section("Global shortcuts", store.main)
        for name, group in store.namespaces.items():
            if not group.error:
                section(f"Namespace {name} ({group.file})", group, f"{name} ")
    return 0


def cmd_show(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut show",
        usage="%(prog)s [-h] [--global] [namespace] name",
        description="Show shortcut details and content",
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    resolved = resolve_name(parsed.name, parsed.skip_local)
    entry, group = resolved.entry, resolved.group

    scope = "namespace" if group.name else ("project" if group.needs_approval else "global")
    print(f"Name:        {resolved.display}")
    print(f"Scope:       {scope}" + ("  (hides a global shortcut of the same name)" if resolved.hides_global else ""))
    print(f"Defined in:  {group.file}")
    print(f"CWD:         {entry.cwd or CALLER}")
    print(f"Run CWD:     {resolved.run_cwd}")
    print(f"Description: {entry.description}")
    if entry.args is not None:
        print(f"Usage:       {usage_line(resolved.display, entry.args)}")
    if entry.confirm:
        print("Confirm:     True")
    if entry.created:
        print(f"Created:     {entry.created}")
    if entry.created_by:
        print(f"Created by:  {entry.created_by}")
    if entry.icon:
        print(f"Icon:        {entry.icon}")
    if not entry.log:
        print("Log:         False")
    state = approval_state(group)
    if state != "ok":
        print(f"Approval:    {'not approved' if state == 'new' else 'changed'} (run: {group.update_cmd})")

    if entry.inline is not None:
        print(f"\nInline command:\n  {entry.inline}")
    else:
        script_path = resolve_script_path(resolved.group.script_base, entry.script)
        print(f"\nScript ({script_path}):")
        if os.path.isfile(script_path):
            with open(script_path, "r", encoding="utf-8", errors="replace") as sf:
                print(sf.read().rstrip())
        else:
            print(f"  [file missing: {script_path}]")
    return 0


def cmd_location(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut location",
        usage="%(prog)s [-h] [--log] [--global] [namespace] name",
        description="Print the path of a shortcut's script",
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    parser.add_argument("--log", action="store_true", help="Print the path of its log instead")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    resolved = resolve_name(parsed.name, parsed.skip_local)
    entry = resolved.entry
    if parsed.log:
        print(resolved.group.log_path(resolved.entry.name))
    elif entry.inline is not None:
        die(f"'{resolved.display}' is an inline shortcut, so it has no file. Change it with: shortcut edit {resolved.display}")
    else:
        print(os.path.abspath(resolve_script_path(resolved.group.script_base, entry.script)))
    return 0


def cmd_rm(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut rm", usage="%(prog)s [-h] [-y] [--global] [namespace] name", description="Remove a shortcut"
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    parser.add_argument("-y", "--yes", action="store_true", help="Do not prompt for confirmation")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    # Raw lookup: an entry that can't run must still be removable
    group, name, raw_entry, rest, _ = find_entry(parsed.name, skip_local=parsed.skip_local, writes=True)
    if rest:
        die(f"too many names: {shlex.join(parsed.name)}")
    display = group.display(name)

    if not parsed.yes:
        if not sys.stdin.isatty():
            die(f"remove '{display}' in a terminal, or pass -y")
        if not ask(f"remove '{display}' from {group.file}? [y/N] "):
            print("Aborted.", file=sys.stderr)
            return 1

    # 1. Remove from registry
    with registry_lock(group.store_dir):
        reg = load_registry_file(group.file)
        shortcuts = reg.get("shortcuts", {})
        if name in shortcuts:
            del shortcuts[name]
            save_registry_file(group.file, reg)

    # 2. Remove the stored copy: only files shortcut keeps in its scripts folder, never a linked
    #    script or a project's own file
    script = raw_entry.get("script") if isinstance(raw_entry, dict) else None
    if isinstance(script, str) and script:
        with contextlib.suppress(OSError, ValueError):
            stored_path = resolve_script_path(group.script_base, script)
            if pathlib.Path(stored_path).resolve().is_relative_to(pathlib.Path(scripts_dir(group)).resolve()):
                os.unlink(stored_path)

    # 3. Remove log file
    with contextlib.suppress(OSError):
        os.unlink(group.log_path(name))

    # 4. Remove desktop file if present (global entries only)
    if not group.needs_approval:
        xdg_data = os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share")
        with contextlib.suppress(OSError):
            os.unlink(os.path.join(xdg_data, "applications", f"shortcut-{name}.desktop"))

    print(f"Removed shortcut '{display}'.")
    note_if_unapproved(group)
    return 0


def cmd_edit(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut edit",
        usage="%(prog)s [-h] [--global] [namespace] name",
        description="Edit shortcut script in editor",
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    add_global_flag(parser)
    parsed = parser.parse_args(args)
    resolved = resolve_name(parsed.name, parsed.skip_local, writes=True)
    editor_cmd = get_editor_cmd()
    entry = resolved.entry
    if entry.inline is not None:
        rc = edit_inline_roundtrip(entry.name, resolved.group, editor_cmd)
    else:
        rc = edit_file(editor_cmd, resolve_script_path(resolved.group.script_base, entry.script))
    note_if_unapproved(resolved.group)
    return rc


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


def confirm_review(verb: str, old: Dict[str, str], new: Dict[str, str]) -> bool:
    """Ask after a diff. A binary file can't be shown, so taking one unseen has to be typed out."""
    unseen = sorted(path for path, text in new.items() if text.startswith(BINARY_MARK) and old.get(path) != text)
    if unseen:
        return ask(f"can't show {', '.join(unseen)}. Type binary to {verb} it unseen: ", ("binary",))
    return ask(f"{verb}? [y/N] ")


def approve_group(group: Group, assume_yes: bool, global_names: Set[str]) -> int:
    """Show what changed since the last approval and ask. What is shown is what gets hashed."""
    if group.error:
        print(f"error: cannot approve {group.label}: {group.error}", file=sys.stderr)
        return 1
    errors = entry_errors(group)
    if errors:
        for error in errors:
            print(f"error: cannot approve {group.label}: {error}", file=sys.stderr)
        return 1
    files = group_files(group)
    record = load_approval(group)
    if record is not None and record.get("hash") == files_hash(files):
        print(f"{group.label}: already approved, nothing has changed.")
        return 0
    old = record["snapshot"] if record else {}
    print(f"{'Changes to' if record else 'New to you:'} {group.label}")
    show_diff(diff_files(old, files))
    if not group.name:
        for name in sorted(set(group.shortcuts) & global_names):
            print(f"Inside this project, '{name}' hides your global '{name}'.")
    if not assume_yes:
        if not sys.stdin.isatty():
            die(f"approve {group.label} in a terminal, or pass -y")
        if not confirm_review("approve", old, files):
            print("Aborted.", file=sys.stderr)
            return 1
    save_approval(group, files)
    print(f"Approved {group.label}. This covers its definitions, not what they call. "
          f"Any change to them needs `{group.update_cmd}` again.")
    return 0


def cmd_update(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut update",
        description="Review changes to project shortcuts and namespaces, and approve them",
    )
    parser.add_argument("group", nargs="?", help="A namespace, or . for this project's own shortcuts (default: all here)")
    parser.add_argument("-y", "--yes", action="store_true", help="Approve without asking")
    parsed = parser.parse_args(args)

    stores = visible_stores(os.getcwd())
    groups = [g for store in stores for g in store.groups if g.needs_approval]
    if parsed.group == ".":
        groups = [g for g in groups if not g.name]
        if not groups:
            die("no project shortcuts here: no .shortcuts/shortcuts.json in this folder or its parents")
    elif parsed.group:
        groups = [g for g in groups if g.name == parsed.group][:1]
        if not groups:
            die(f"no namespace named '{parsed.group}' here")
    else:
        groups = [g for g in groups if g.error or g.shortcuts or load_approval(g)]
        groups = [g for g in groups if g.error or approval_state(g) != "ok"]
        if not groups:
            print("Nothing to approve here.")
            return 0
    global_names = set(stores[-1].main.shortcuts)
    return max(approve_group(g, parsed.yes, global_names) for g in groups)


ANSI_ESCAPE = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]")


def plain_text(text: str) -> str:
    """Terminal output as an agent should read it: no colour codes, and each line as it was last drawn."""
    text = ANSI_ESCAPE.sub("", text)
    return "\n".join(line.rstrip("\r").rsplit("\r", 1)[-1] for line in text.split("\n"))


def store_report(stores: List[Store]) -> List[str]:
    """One line per store and namespace, so a broken file can be found with nothing else working."""
    lines: List[str] = []
    for store in stores:
        for group in store.groups:
            errors = [group.error] if group.error else entry_errors(group)
            if errors:
                status = f"BROKEN: {'; '.join(errors)}"
            else:
                state = approval_state(group)
                ok = "definitions unchanged since approved" if group.needs_approval else "ok"
                status = {"ok": ok, "new": "not approved", "changed": "changed since approved"}[state]
            lines.append(f"{group.file}: {status}")
        lines.extend(f"{store.dir}: BROKEN: {problem}" for problem in store.problems)
    return lines


def cmd_fix(args: List[str]) -> int:
    parser = Parser(prog="shortcut fix", description="Print diagnostic block for an agent")
    parser.add_argument("name", nargs="*", help="Shortcut name, or namespace and name (none: check the stores)")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    report = store_report(visible_stores(skip_local=parsed.skip_local))
    if not parsed.name:
        print("\n".join(["=== SHORTCUT FIX ===", *report]))
        return 0

    display = " ".join(parsed.name)
    try:
        group, name, raw, _, _ = find_entry(parsed.name, skip_local=parsed.skip_local)
    except (KeyError, ValueError) as e:
        message = e.args[0] if e.args else str(e)
        print("\n".join([f"=== SHORTCUT FIX: {display} ===", str(message), "--- Stores ---", *report]))
        return 1
    log_path = group.log_path(name)
    entry_json = json.dumps(raw, indent=2)

    if isinstance(raw, dict) and isinstance(raw.get("inline"), str):
        content = raw["inline"]
    elif isinstance(raw, dict) and isinstance(raw.get("script"), str):
        try:
            script_path = resolve_script_path(group.script_base, raw["script"])
            if os.path.isfile(script_path):
                with open(script_path, "r", encoding="utf-8", errors="replace") as f:
                    content = f.read()
            else:
                content = f"(script file missing: {script_path})"
        except ValueError as e:
            content = f"(bad script path: {e})"
    else:
        content = "(no inline command or script)"

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
        f"=== SHORTCUT FIX: {display} ===",
        "--- Entry JSON ---",
        entry_json,
        "--- Script / Inline Command ---",
        content.rstrip("\r\n"),
        "--- Last Log ---",
        log_text.rstrip("\r\n"),
        f"Last exit code: {last_exit}",
    ]
    bad = [line for line in report if "BROKEN" in line or ": changed since" in line or "not approved" in line]
    if bad:
        lines.extend(["--- Stores ---", *bad])
    print("\n".join(lines))
    return 0


def is_git_source(source: str) -> bool:
    return (
        source.startswith(("http://", "https://", "git://", "git@", "ssh://", "file://"))
        or source.endswith(".git")
    )


def plan_import(
    source_base: str,
    dest_store_dir: str,
    dest_registry: Dict[str, Any],
    source_registry: Dict[str, Any],
) -> Tuple[Dict[str, Any], Dict[str, bytes], List[str]]:
    """What a collection would add to the global store, changing nothing: (entries, {stored script path:
    its bytes}, skipped). The bytes are read once here, so what gets copied is what was reviewed."""
    entries: Dict[str, Any] = {}
    scripts: Dict[str, bytes] = {}
    skipped: List[str] = []
    dest_shortcuts = dest_registry.get("shortcuts", {})
    dest_namespaces = load_store(dest_store_dir).namespaces
    source_shortcuts = source_registry.get("shortcuts", {})
    if not isinstance(source_shortcuts, dict):
        return entries, scripts, skipped

    for name, entry_data in source_shortcuts.items():
        # Validate entry structure
        try:
            ShortcutEntry.from_dict(name, entry_data)
        except ValueError as e:
            skipped.append(f"{name} (invalid entry: {e})")
            continue

        if name in dest_shortcuts:
            skipped.append(f"{name} (already exists)")
            continue
        if name in dest_namespaces:
            skipped.append(f"{name} (already a namespace)")
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

        # A relative cwd means nothing once the entry leaves the file that defined it
        d = entry_data.get("cwd")
        if d and d != CALLER and (not os.path.isabs(d) or not os.path.isdir(d)):
            skipped.append(f"{name} (cwd is relative or not found here: {d})")
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
                load_store(dest_store_dir).main, f"{name}{pathlib.Path(script_field).suffix or '.sh'}"
            )
            if os.path.lexists(dest_abs_script) or dest_abs_script in scripts:
                skipped.append(f"{name} (would replace {dest_rel_script})")
                continue
            with open(src_path, "rb") as f:
                scripts[dest_abs_script] = f.read()
            new_entry["script"] = dest_rel_script

        entries[name] = new_entry

    return entries, scripts, skipped


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




def cmd_init(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut init",
        description="Create an empty project store here, or with --global review a collection and copy it "
                    "into the global store",
    )
    parser.add_argument("source", nargs="?", default=None, help="Local path or git URL to import from (needs --global)")
    parser.add_argument("--global", dest="is_global", action="store_true", help="Import into global store")
    parser.add_argument("-y", "--yes", action="store_true", help="Import without asking")
    parsed = parser.parse_args(args)

    if not parsed.is_global:
        if parsed.source:
            die(
                "a project's .shortcuts/ is used in place, never copied: clone the project and run "
                "`shortcut update` there. To copy a collection into your global store, add --global."
            )
        store_dir = new_local_store_dir(os.getcwd())
        if registry_exists(store_dir):
            print(f"Project store already exists at {store_dir}")
            return 0
        init_store(store_dir)
        print(f"Initialized empty project store at {store_dir}")
        return 0

    dest_store_dir = get_global_store_dir()
    if not parsed.source:
        if registry_exists(dest_store_dir):
            print(f"Global store already exists at {dest_store_dir}")
            return 0
        init_store(dest_store_dir)
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
                die(f"failed to clone git repository from {source_str}: {err_detail}")
            source_root = clone_dir
        else:
            source_root = os.path.abspath(os.path.expanduser(source_str))
            if not os.path.isdir(source_root):
                die(f"source directory does not exist: {source_root}")

        # A collection's shortcuts.json sits at its root or in .shortcuts/; either way its
        # relative script paths resolve against the source root.
        root_json = os.path.join(source_root, "shortcuts.json")
        dot_json = os.path.join(source_root, ".shortcuts", "shortcuts.json")
        if os.path.isfile(root_json):
            source_store_dir = source_root
        elif os.path.isfile(dot_json):
            source_store_dir = os.path.join(source_root, ".shortcuts")
        else:
            die(f"no shortcuts.json found in {source_root} (checked root and .shortcuts/)")

        entries, scripts, skipped = plan_import(
            source_base=source_root,
            dest_store_dir=dest_store_dir,
            dest_registry=load_registry(dest_store_dir) if registry_exists(dest_store_dir) else {},
            source_registry=load_registry(source_store_dir),
        )
        if skipped:
            print(f"Skipped ({len(skipped)}):")
            for s in skipped:
                print(f"  {s}")
        if not entries:
            return 0

        # Global shortcuts have no approval step, so an import is reviewed here or never
        files = {"shortcuts.json": json.dumps(entries, indent=2, sort_keys=True) + "\n"}
        files.update({os.path.relpath(path, dest_store_dir): reviewable(raw) for path, raw in scripts.items()})
        print(f"New from {source_str}:")
        show_diff(diff_files({}, files))
        if not parsed.yes:
            if not sys.stdin.isatty():
                die(f"import {source_str} in a terminal, or pass -y")
            if not confirm_review("import", {}, files):
                print("Aborted.", file=sys.stderr)
                return 1
        apply_import(dest_store_dir, entries, scripts)

        print(f"Imported ({len(entries)}):")
        for name in entries:
            print(f"  {name}")
        return 0

    finally:
        if temp_dir_obj is not None:
            temp_dir_obj.cleanup()


def legacy_stem(name: str) -> str:
    for prefix in LEGACY_PREFIXES:
        if name.startswith(prefix):
            return name[len(prefix):]
    return name


def cmd_migrate(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut migrate",
        description="Convert 1.x shortcuts (g_ names, prefixes, 'dir') and flat ~/.shortcuts/*.sh scripts",
    )
    parser.parse_args(args)
    global_dir = get_global_store_dir()
    init_store(global_dir)
    with registry_lock(global_dir):
        changed = _migrate_registry(global_dir)
        changed = _migrate_flat_scripts(global_dir) or changed
    local_dir = find_local_store_dir()
    if local_dir:
        with registry_lock(local_dir):
            changed = _migrate_registry(local_dir) or changed
        note_if_unapproved(load_store(local_dir).main)
    if not changed:
        print("Nothing to migrate.")
    return 0


def _migrate_registry(store_dir: str) -> bool:
    """1.x decided where a shortcut runs from its name and store; write that down as 'cwd'."""
    reg = load_registry(store_dir)
    shortcuts = reg.get("shortcuts", {})
    if not isinstance(shortcuts, dict):
        raise ValueError(f"corrupt registry {store_dir}: 'shortcuts' must be an object")
    is_global = is_global_store(store_dir)
    prefixes = reg.get("prefixes", {}) if isinstance(reg.get("prefixes", {}), dict) else {}
    taken = set(shortcuts) | set(load_store(store_dir).namespaces)
    migrated: Dict[str, Any] = {}
    done: List[str] = []
    skipped: List[str] = []

    for name, data in shortcuts.items():
        if not isinstance(data, dict):
            migrated[name] = data
            continue
        new_name, new_data = name, dict(data)
        old_dir = new_data.pop("dir", None)
        stem = legacy_stem(name)
        owner = max((p for p in prefixes if name.startswith(p)), key=len, default=None)
        if stem != name:
            try:
                validate_name(stem)
            except ValueError:
                skipped.append(f"{name} ('{stem}' is not a valid name)")
                migrated[name] = data
                continue
            if stem in taken:
                skipped.append(f"{name} ({stem} already exists)")
                migrated[name] = data
                continue
            new_name = stem
            taken.add(stem)
            new_data["cwd"] = CALLER
        elif owner is not None and is_global:
            new_data["cwd"] = prefixes[owner]
        elif isinstance(old_dir, str) and old_dir:
            is_rel = not is_global and not os.path.isabs(old_dir)
            new_data["cwd"] = os.path.normpath(os.path.join("..", old_dir)) if is_rel else old_dir
        elif not is_global and reg.get("version") == 1 and "cwd" not in new_data:
            new_data["cwd"] = ".."  # a project's shortcuts ran at its root; this is relative to .shortcuts/
        migrated[new_name] = new_data
        if new_name != name:
            done.append(f"{name} -> {new_name} (cwd: {new_data['cwd']})")
            xdg_data = os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share")
            launcher = os.path.join(xdg_data, "applications", f"shortcut-{name}.desktop")
            if is_global and os.path.isfile(launcher):
                done.append(f"  {launcher} still runs {name}; recreate it with: shortcut r desktop {new_name}")
        elif new_data != data:
            done.append(f"{name} (cwd: {new_data.get('cwd', CALLER)})")

    if "prefixes" in reg:
        done.extend(f"prefix {p} removed; its shortcuts now have cwd: {d}" for p, d in prefixes.items())
    changed = migrated != shortcuts or list(migrated) != list(shortcuts) or "prefixes" in reg
    if changed or reg.get("version") != REGISTRY_VERSION:
        reg["shortcuts"] = migrated
        reg["version"] = REGISTRY_VERSION
        reg.pop("prefixes", None)
        save_registry_atomic(store_dir, reg)
    if done:
        print(f"Migrated in {store_dir}:")
        for item in done:
            print(f"  {item}")
    for item in skipped:
        print(f"skipped {item}")
    return bool(done or skipped)


def _migrate_flat_scripts(global_dir: str) -> bool:
    reg = load_registry(global_dir)
    shortcuts = reg.setdefault("shortcuts", {})
    os.makedirs(os.path.join(global_dir, "scripts"), exist_ok=True)

    # Legacy flat files: <store>/*.sh (only files directly in global_dir)
    flat_files = [
        f for f in sorted(os.listdir(global_dir))
        if f.endswith(".sh") and (os.path.isfile(os.path.join(global_dir, f)) or os.path.islink(os.path.join(global_dir, f)))
    ]

    if not flat_files:
        return False

    candidates: List[Tuple[str, str, str, str, str, str]] = []
    planned_names: Set[str] = set()

    # Pass 1: validate everything before moving anything
    for filename in flat_files:
        src_path = os.path.join(global_dir, filename)

        if os.path.islink(src_path):
            print(f"skipped {filename} (symlink)")
            continue

        name = legacy_stem(pathlib.Path(filename).stem)

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
            cwd=CALLER,
            created=today,
        )
        shortcuts[name] = entry.to_dict()
        migrated.append(f"{filename} -> {name} ({desc})")

    if migrated:
        save_registry_atomic(global_dir, reg)
        print(f"Migrated ({len(migrated)}):")
        for item in migrated:
            print(f"  {item}")

    return True


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

    local subcommands="run r new add link list show edit location rm fix update init migrate completion help"
    local name_commands="run r show edit location rm fix"

    if [ "${COMP_CWORD:-1}" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "${subcommands}" -- "$cur") )
        return 0
    fi

    # What's before the cursor, leaving out flags: nothing yet, or a namespace
    local cmd="${COMP_WORDS[1]}" i
    local -a typed=()
    for (( i = 2; i < COMP_CWORD; i++ )); do
        [[ "${COMP_WORDS[i]}" == -* ]] || typed+=("${COMP_WORDS[i]}")
    done

    case " ${name_commands} " in
        *" ${cmd} "*)
            if [ "${#typed[@]}" -eq 0 ] && [[ "$cur" == -* ]]; then
                local flags="--global"
                case "$cmd" in
                    run|r|rm) flags="--global -y" ;;
                    location) flags="--global --log" ;;
                esac
                COMPREPLY=( $(compgen -W "${flags}" -- "$cur") )
            elif [ "${#typed[@]}" -le 1 ]; then
                local names
                names=$(shortcut list --names ${typed[0]} 2>/dev/null)
                COMPREPLY=( $(compgen -W "${names}" -- "$cur") )
            fi
            return 0
            ;;
    esac

    case "$prev" in
        init)
            COMPREPLY=( $(compgen -W "--global -y" -- "$cur") )
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
    subcommands=(run r new add link list show edit location rm fix update init migrate completion help)

    if (( CURRENT == 2 )); then
        compadd -- $subcommands
        return
    fi

    case $words[2] in
        run|r|show|edit|location|rm|fix)
            local -a typed
            typed=(${words[3,CURRENT-1]:#-*})
            if (( $#typed == 0 )) && [[ $PREFIX == -* ]]; then
                compadd -- --global
                case $words[2] in
                    run|r|rm) compadd -- -y ;;
                    location) compadd -- --log ;;
                esac
            elif (( $#typed <= 1 )); then
                compadd -- ${(f)"$(shortcut list --names $typed[1] 2>/dev/null)"}
            fi
            ;;
        init)
            compadd -- --global -y
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
    set -l typed (string match -v -- '-*' (commandline -opc)[3..-1])
    if test (count $typed) -le 1
        shortcut list --names $typed 2>/dev/null
    end
end

complete -c shortcut -f
complete -c shortcut -n __fish_use_subcommand -a "run r new add link list show edit location rm fix update init migrate completion help"
complete -c shortcut -n "__fish_seen_subcommand_from run r show edit location rm fix" -a "(__shortcut_names)"
complete -c shortcut -n "__fish_seen_subcommand_from run r show edit location rm fix" -l global
complete -c shortcut -n "__fish_seen_subcommand_from run r rm" -s y
complete -c shortcut -n "__fish_seen_subcommand_from location" -l log
complete -c shortcut -n "__fish_seen_subcommand_from init" -F -l global
complete -c shortcut -n "__fish_seen_subcommand_from init" -s y
complete -c shortcut -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
"""

COMPLETION_SCRIPTS: Dict[str, str] = {
    "bash": BASH_COMPLETION_SCRIPT,
    "zsh": ZSH_COMPLETION_SCRIPT,
    "fish": FISH_COMPLETION_SCRIPT,
}


def cmd_completion(args: List[str]) -> int:
    parser = Parser(prog="shortcut completion", description="Generate shell completion script")
    parser.add_argument("shell", choices=sorted(COMPLETION_SCRIPTS), nargs="?", default="bash", help="Shell type")
    parsed = parser.parse_args(args)
    sys.stdout.write(COMPLETION_SCRIPTS[parsed.shell])
    return 0


def print_usage() -> None:
    usage_text = """shortcut - run and manage quick scripts and commands

Usage:
  shortcut run [--global] [-y] <name> [args...]
                                            Run a shortcut, or `<namespace> <name>`; r is short for run.
                                            --global skips project shortcuts, -y answers a confirm prompt
  shortcut new <name> [FLAGS] [-- CMD...]   Create an inline shortcut, or a script opened in $EDITOR
  shortcut add <name> <file|-> --desc D [FLAGS]
                                            Add a script copy to store
  shortcut link <name> <path> --desc D [FLAGS]
                                            Link an external script by path
  shortcut list [--names [NAMESPACE]]       List visible shortcuts, with where each runs
  shortcut show [--global] <name>           Show shortcut metadata and script contents
  shortcut edit [--global] <name>           Edit a script or inline command in $VISUAL, $EDITOR, or nano
  shortcut location [--global] <name> [--log]
                                            Print the path of its script (fails for inline), or its log
  shortcut rm [--global] <name> [-y]        Remove shortcut and associated files
  shortcut fix [--global] [name]            Print agent diagnostic block; with no name, check the stores
  shortcut update [group] [-y]              Review changes to project shortcuts and namespaces, and approve them
  shortcut init                             Create an empty project store (.shortcuts/) here
  shortcut init --global [SOURCE] [-y]      Review a collection (folder or git URL) and copy it into the
                                            global store. Later changes to the source don't reach it
  shortcut migrate                          Convert 1.x shortcuts and flat ~/.shortcuts/*.sh scripts
  shortcut completion bash|zsh|fish         Print a shell completion script
  shortcut help, -h, --help                 Show this help message
  shortcut --version                        Show version

FLAGS for new, add and link:
  --desc D                  Description
  --cwd caller|DIR          Where it runs (default: caller, your current directory)
  --arg NAME[=DEFAULT]      Declare a required argument, or an optional one with a default
  --confirm                 Ask before running it
  --local                   Store it in this project's .shortcuts/ (default: the global store)
  --namespace NAME          Put it in namespace NAME, creating imports/NAME.shortcut if needed
  --by C                    Creator identifier

Project shortcuts and namespaces don't run until approved with `shortcut update`,
and must be approved again after any change to their definitions. Approval covers
shortcuts.json, imports and the scripts they name, not the programs those call.
Global shortcuts have no approval step, so a global link target can change freely.
Set SHORTCUT_SHOW_DIR=0 to hide the "→ <run directory>" line.
"""
    print(usage_text, end="")


def main(argv: Optional[List[str]] = None) -> NoReturn:
    args = sys.argv[1:] if argv is None else argv
    sys.stdout, sys.stderr = (SafeOutput(stream) if stream else None for stream in (sys.stdout, sys.stderr))

    if not args:
        print_usage()
        sys.exit(0)

    first = args[0]

    if first in ("--help", "-h", "help"):
        print_usage()
        sys.exit(0)

    if first == "--version":
        print(f"shortcut {VERSION}")
        sys.exit(0)

    handlers = {
        "run": cmd_run,
        "r": cmd_run,
        "new": cmd_new,
        "add": cmd_add,
        "link": cmd_link,
        "list": cmd_list,
        "show": cmd_show,
        "edit": cmd_edit,
        "location": cmd_location,
        "rm": cmd_rm,
        "fix": cmd_fix,
        "update": cmd_update,
        "init": cmd_init,
        "migrate": cmd_migrate,
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

    print(f"unknown command '{first}' (to run a shortcut: shortcut run {first})", file=sys.stderr)
    sys.exit(2)


if __name__ == "__main__":
    main()
