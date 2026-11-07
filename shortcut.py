#!/usr/bin/env python3

from __future__ import annotations

import argparse
import contextlib
import dataclasses
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
from typing import Any, Dict, Iterator, List, NoReturn, Optional, Set, Tuple

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
    confirm: bool = False
    export: bool = False  # made to work outside its project; only a project's own files can say so
    args: Optional[List[Dict[str, Any]]] = None

    TEXT_FIELDS = ("inline", "script", "cwd", "icon", "created_by", "created")

    @classmethod
    def from_dict(cls, name: str, data: Any) -> ShortcutEntry:
        def bad(problem: str) -> ValueError:
            return ValueError(f"'{name}' is not a valid entry: {problem}")

        if not isinstance(data, dict):
            raise bad("expected a dictionary")
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
        for field in ("confirm", "export"):
            if not isinstance(data.get(field, False), bool):
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
        if self.confirm:
            d["confirm"] = True
        if self.export:
            d["export"] = True
        if self.args is not None:
            d["args"] = self.args
        return d


def is_relative_cwd(cwd: Optional[str]) -> bool:
    """A cwd that means a folder next to the file defining it, so it's only right in that checkout."""
    return bool(cwd) and cwd != CALLER and not os.path.isabs(os.path.expanduser(cwd))


def export_error(entry: ShortcutEntry, in_project: bool) -> Optional[str]:
    if not entry.export:
        return None
    if not in_project:
        return f"'{entry.name}' is not a valid entry: 'export' is only for {PROJECT_FILE} and {LOCAL_FILE}"
    if is_relative_cwd(entry.cwd):
        return f"'{entry.name}' is exported, so its cwd must be caller or an absolute path, not '{entry.cwd}'"
    return None


def load_entry(group: "Group", name: str, data: Any) -> ShortcutEntry:
    """An entry as its group allows it: ShortcutEntry.from_dict, plus the rules for 'export'."""
    entry = ShortcutEntry.from_dict(name, data)
    error = export_error(entry, bool(group.local_file))
    if error:
        raise ValueError(error)
    return entry


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


@dataclasses.dataclass(frozen=True)
class Group:
    """One set of shortcuts: the global store's shortcuts.json (name ""), a project's .shortcuts with
    .shortcuts.local over it, or an imported namespace. store_dir is the global store or the project's root."""
    name: str
    file: str
    store_dir: str
    data: Dict[str, Any]
    error: Optional[str] = None  # why the file can't be used
    local_file: str = ""  # only a project's own group has one
    local_data: Dict[str, Any] = dataclasses.field(default_factory=dict)

    @property
    def shared_shortcuts(self) -> Dict[str, Any]:
        shortcuts = self.data.get("shortcuts", {})
        return shortcuts if isinstance(shortcuts, dict) else {}

    @property
    def local_shortcuts(self) -> Dict[str, Any]:
        """Raw, so a null that hides a shared entry is still here."""
        shortcuts = self.local_data.get("shortcuts", {})
        return shortcuts if isinstance(shortcuts, dict) else {}

    @property
    def shortcuts(self) -> Dict[str, Any]:
        """What can run: the shared entries, each replaced or hidden by .shortcuts.local."""
        merged = {**self.shared_shortcuts, **self.local_shortcuts}
        return {name: entry for name, entry in merged.items() if name not in self.local_shortcuts or entry is not None}

    def source(self, name: str) -> str:
        """Where a project entry comes from: shared, local, overridden or hidden."""
        if name not in self.local_shortcuts:
            return "shared"
        if self.local_shortcuts[name] is None:
            return "hidden"
        return "overridden" if name in self.shared_shortcuts else "local"

    def defining_file(self, name: str) -> str:
        return self.local_file if name in self.local_shortcuts else self.file

    @property
    def is_project(self) -> bool:
        return not is_global_store(self.store_dir)

    @property
    def folder(self) -> str:
        """Where relative 'cwd' values resolve: the folder of the defining file."""
        return os.path.dirname(os.path.abspath(self.file))

    @property
    def script_base(self) -> str:
        """Relative paths resolve against a namespace's folder, a project's root, or the global store."""
        return self.folder if self.name else self.store_dir

    def display(self, name: str) -> str:
        return f"{self.name} {name}" if self.name else name

    @property
    def needs_approval(self) -> bool:
        return bool(self.name) or not is_global_store(self.store_dir)

    @property
    def key(self) -> str:
        """What the approval is recorded against: the import's path, or the project's."""
        return os.path.realpath(self.file if self.name else self.store_dir)

    @property
    def label(self) -> str:
        return self.name or self.key

    @property
    def update_cmd(self) -> str:
        return f"shortcut update {self.name}" if self.name else "shortcut update"


@dataclasses.dataclass(frozen=True)
class Store:
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
            entry = load_entry(group, name, data)
            if entry.script is not None and not os.path.isfile(resolve_script_path(group.script_base, entry.script)):
                raise ValueError(f"'{name}' is not a valid entry: script not found: {entry.script}")
        except ValueError as e:
            errors.append(str(e))
    return errors


def load_project_group(root: str) -> Group:
    shared = load_group(os.path.join(root, PROJECT_FILE), root)
    local_path = os.path.join(root, LOCAL_FILE)
    local = load_group(local_path, root)
    error = shared.error or local.error
    if not error and "imports" in local.data:
        error = f"{local_path} can't have 'imports': namespaces belong in {PROJECT_FILE}"
    return dataclasses.replace(shared, local_file=local_path, local_data=local.data, error=error)


def load_store(store_dir: str) -> Store:
    """Reads a store and its namespaces. Never raises on bad content: that is reported on the
    Group or Store, so list, show and rm still work on a broken store."""
    is_global = is_global_store(store_dir)
    main = load_group(os.path.join(store_dir, "shortcuts.json"), store_dir) if is_global else load_project_group(store_dir)
    problems: List[str] = []
    sources: Dict[str, str] = {}
    imports_dir = os.path.join(store_dir, "imports")
    if is_global and os.path.isdir(imports_dir):
        for filename in sorted(os.listdir(imports_dir)):
            if filename.endswith(IMPORT_SUFFIX):
                sources[filename[:-len(IMPORT_SUFFIX)]] = os.path.join(imports_dir, filename)
    imports = main.data.get("imports", {})
    for name, path in (imports.items() if isinstance(imports, dict) else ()):
        if not isinstance(path, str):
            problems.append(f"import '{name}' in {main.file} must be a path")
        elif not path.endswith(IMPORT_SUFFIX):
            problems.append(f"import '{name}' in {main.file} must be a {IMPORT_SUFFIX} file")
        elif os.path.basename(path) == IMPORT_SUFFIX:
            problems.append(f"import '{name}' in {main.file}: {BARE_IMPORT}")
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
            group = dataclasses.replace(group, error=str(e) if name else f"{path}: {BARE_IMPORT}")
        namespaces[name] = group
        if name in main.shortcuts:
            problems.append(f"'{name}' is both a shortcut and a namespace in {store_dir}; rename one")
    return Store(main=main, namespaces=namespaces, problems=problems)


def visible_stores(caller_cwd: Optional[str] = None, skip_local: bool = False) -> List[Store]:
    """The stores a name is looked up in, nearest first."""
    root = None if skip_local else find_project_root(caller_cwd)
    return [load_store(d) for d in ([root] if root else []) + [get_global_store_dir()]]


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


def json_file(group: Group, path: str, data: Dict[str, Any]) -> Dict[str, str]:
    return {display_path(group, path): json.dumps(data, indent=2, sort_keys=True) + "\n"}


def shared_files(group: Group) -> Dict[str, str]:
    """The group's own file and what it names, the JSON normalised: exactly what is hashed, snapshotted
    and diffed. A project may have no .shortcuts, and then this is empty."""
    if group.local_file and not os.path.exists(group.file):
        return {}
    return {**json_file(group, group.file, group.data), **entry_files(group, group.shared_shortcuts)}


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


def note_if_unapproved(group: Group) -> None:
    """After a change made through shortcut: say so if it needs approving before it runs."""
    fresh = load_project_group(group.store_dir) if group.local_file else load_group(group.file, group.store_dir, group.name)
    if group.needs_approval and approval_state(fresh) != "ok":
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
) -> Tuple[Group, str, Any, List[str], bool]:
    """Find `name` or `namespace name` without judging the entry: (group, name, raw entry, leftover
    words, hides a global one). A project whose file can't be read stops every lookup inside it.
    strict (for run) also stops on a global store's load errors for the name it has."""
    stores = visible_stores(caller_cwd, skip_local)
    first, rest = words[0], words[1:]
    home = stores[-1]

    for store in stores:
        if store is not home and store.main.error:
            raise ValueError(
                f"{store.main.error}. Nothing runs from this project until it's repaired; "
                f"shortcut r --global {first} reaches your own shortcuts"
            )
        problems = ([store.main.error] if store.main.error else []) + store.problems
        imports = store.main.data.get("imports", {})
        named = first in store.main.shortcuts or first in store.namespaces or (isinstance(imports, dict) and first in imports)
        if strict and problems and named:
            raise ValueError("; ".join(problems))
        if first in store.main.shortcuts and (not rest or first not in store.namespaces):
            hides = store is not home and first in home.main.shortcuts
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
            return group, rest[0], group.shortcuts[rest[0]], rest[1:], False
    raise KeyError(" ".join([f"shortcut '{first}' not found.", *([home.main.error] if home.main.error else [])]))


def resolve_run_dir(cwd: Optional[str], group: Group, caller_cwd: str) -> str:
    if not cwd or cwd == CALLER:
        return caller_cwd
    return os.path.normpath(os.path.join(group.folder, os.path.expanduser(cwd)))


def resolve_shortcut(
    words: List[str],
    caller_cwd: Optional[str] = None,
    skip_local: bool = False,
    strict: bool = False,
) -> Tuple[ResolvedShortcut, List[str]]:
    """Find what `shortcut run <words>` runs, and where; also returns the words left over as arguments."""
    caller_cwd = caller_cwd or os.getcwd()
    group, name, raw, rest, hides_global = find_entry(words, caller_cwd, skip_local, strict)
    entry = load_entry(group, name, raw)
    run_cwd = resolve_run_dir(entry.cwd, group, caller_cwd)
    return ResolvedShortcut(entry=entry, group=group, run_cwd=run_cwd, hides_global=hides_global), rest


def resolve_name(words: List[str], skip_local: bool = False) -> ResolvedShortcut:
    """For commands that name a shortcut: `name` or `namespace name`, and nothing else."""
    resolved, rest = resolve_shortcut(words, skip_local=skip_local)
    if rest:
        die(f"too many names: {shlex.join(words)}")
    return resolved


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
    --flags are never parsed: they don't count, and stay where they are in argv. A first -- is dropped,
    and every word after it counts, so a value can start with a dash."""
    def fail(msg: str) -> NoReturn:
        print(f"error: {msg}\nusage: {usage_line(display, spec)}", file=sys.stderr)
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

    return exec_child(cmd, resolved.run_cwd, env)


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


def target_group(parsed: argparse.Namespace, cwd: str) -> Group:
    """The file a new shortcut goes in. Creates nothing; _register does."""
    validate_name(parsed.name)
    if parsed.local and parsed.namespace:
        die(f"--namespace can't be used with --local: {LOCAL_FILE} is one file, and {PROJECT_FILE} is yours to edit")
    store_dir = local_project_root(cwd) if parsed.local else get_global_store_dir()
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
    Creates a missing store or namespace file. A project's group means .shortcuts.local."""
    store_dir = group.store_dir
    if group.local_file:
        def change(shortcuts: Dict[str, Any]) -> None:
            if shortcuts.get(entry.name) is not None:
                die(f"shortcut '{entry.name}' already exists in {group.local_file}")
            if entry.name in load_store(store_dir).namespaces:
                die(f"'{entry.name}' is a namespace in {store_dir}")
            shortcuts[entry.name] = entry.to_dict()
        note_if_unapproved(write_local(store_dir, change))
        if entry.name in group.shared_shortcuts:
            print(f"note: this overrides '{entry.name}' from {PROJECT_FILE} for you", file=sys.stderr)
        return
    if not registry_exists(store_dir):
        init_store(store_dir)
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


def write_local(root: str, change: Any) -> Group:
    """Change .shortcuts.local through change(shortcuts), the only way shortcut writes to a project. The
    approval record follows, but only if .local matched it first: a hand edit, or anything in .shortcuts,
    must not get approved by the way."""
    with registry_lock(root):
        before = load_project_group(root)
        record = load_approval(before)
        approved = local_is_approved(before, record)
        path = before.local_file
        is_new_file = not os.path.exists(path)
        reg = {"version": REGISTRY_VERSION, "shortcuts": {}} if is_new_file else load_registry_file(path)
        change(reg.setdefault("shortcuts", {}))
        save_registry_file(path, reg)
        if is_new_file:
            exclude_from_git(root)
        after = load_project_group(root)
        if approved:
            save_approval(after, record["snapshot"] if record else {}, local_files(after))
    return after


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
    source = parser.add_mutually_exclusive_group()
    source.add_argument("--script", metavar="PATH", help="Run this file where it is")
    source.add_argument("--copy", metavar="FILE", help="Copy this file, or - for stdin, into the store")
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
    if parsed.script == "" or parsed.copy == "":
        die("--script and --copy need a file name")
    if inline_cmd_tokens is not None and (parsed.script or parsed.copy):
        die("use one of: '-- CMD', --script, --copy")
    if parsed.local and parsed.copy:
        die(f"--copy can't be used with --local: {LOCAL_FILE} is one file. Use --script")
    if parsed.local and inline_cmd_tokens is None and not parsed.script:
        die("a local shortcut needs a command after '--', or --script PATH")

    # Pick the editor before creating anything, so a missing editor leaves nothing half-made.
    # Without a terminal (an agent running this), skip the editor and say how to open it later.
    # SHORTCUT_EDITOR_CMD is the test hook from get_editor_cmd.
    editor_cmd: Optional[List[str]] = None
    if inline_cmd_tokens is None and not (parsed.script or parsed.copy) and (
        "SHORTCUT_EDITOR_CMD" in os.environ or (sys.stdin.isatty() and sys.stdout.isatty())
    ):
        editor_cmd = get_editor_cmd()

    group = target_group(parsed, os.getcwd())
    fields = entry_fields(parsed.name, parsed, group)
    if parsed.script:
        return new_from_script(parsed, group, fields)
    if parsed.copy:
        return new_from_copy(parsed, group, fields)

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
        print(f"Edit it at: {abs_script_path}")
    return 0


def new_from_script(parsed: argparse.Namespace, group: Group, fields: Dict[str, Any]) -> int:
    """new --script: the entry names the file where it is. In .shortcuts.local a file inside the project is kept relative."""
    path = parsed.script
    abs_path = os.path.abspath(os.path.expanduser(path))
    if path == "-" or not os.path.isfile(abs_path):
        die(f"script file not found: {path}")
    script = abs_path
    if parsed.local:
        relative = os.path.relpath(os.path.realpath(abs_path), group.store_dir)
        script = abs_path if relative.startswith("..") else relative
    entry = ShortcutEntry(name=parsed.name, description=parsed.desc or "(no description)", script=script, **fields)
    _register(group, entry)
    print(f"Created shortcut '{parsed.name}' using {abs_path}.")
    return 0


def new_from_copy(parsed: argparse.Namespace, group: Group, fields: Dict[str, Any]) -> int:
    """new --copy: the store keeps its own copy, so later changes to the source don't reach it."""
    if parsed.copy == "-":
        content: Optional[str] = sys.stdin.read()
        ext = ".sh"
    else:
        content = None
        src_path = os.path.abspath(os.path.expanduser(parsed.copy))
        if not os.path.isfile(src_path):
            die(f"script file not found: {src_path}")
        ext = pathlib.Path(src_path).suffix or ".sh"

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

    entry = ShortcutEntry(name=parsed.name, description=parsed.desc or "(no description)", script=rel_script_path, **fields)
    _register(group, entry, write_script)
    print(f"Created shortcut '{parsed.name}' with a copy at {abs_script_path}.")
    return 0


def cmd_list(args: List[str]) -> int:
    parser = Parser(prog="shortcut list", description="List available shortcuts")
    parser.add_argument("--names", nargs="?", const="", metavar="NAMESPACE",
                        help="Print bare visible names only, or the names in one namespace")
    parsed = parser.parse_args(args)

    stores = visible_stores(os.getcwd())
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

    global_names = set(stores[-1].main.shortcuts)
    local_names = set(stores[0].main.shortcuts) if len(stores) > 1 and approval_state(stores[0].main) == "ok" else set()

    for store in stores:
        for group in store.groups:
            for error in [] if group.error else entry_errors(group):
                print(f"warning: {group.label}: {error}", file=sys.stderr)

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
        for name, data in sorted({**group.shared_shortcuts, **group.shortcuts}.items()):
            data = data if isinstance(data, dict) else {}
            desc = str(data.get("description", ""))
            if name in global_names and group.needs_approval and not group.name:
                desc += "  [hides global]"
            elif name in local_names and not group.needs_approval:
                desc += "  [hidden here by project]"
            rows.append((prefix + name, str(data.get("cwd") or CALLER), group.source(name) if group.local_file else "", desc))
        width = max((len(r[1]) for r in rows), default=0)
        source_width = max((len(r[2]) for r in rows), default=0)
        for name, mode, source, desc in rows:
            print(f"  {name:<20} {mode:<{width}}  " + (f"{source:<{source_width}}  " if source else "") + desc)

    for store in stores:
        if store.main.needs_approval:
            if store.main.shared_shortcuts or store.main.shortcuts:
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
        usage="%(prog)s [-h] [--global] [--json] [namespace] name",
        description="Show shortcut details and content",
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    parser.add_argument("--json", action="store_true", help="Print the entry and where it resolves, as one JSON object")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    if parsed.json:
        return show_json(parsed.name, parsed.skip_local)

    resolved = resolve_name(parsed.name, parsed.skip_local)
    entry, group = resolved.entry, resolved.group

    scope = "namespace" if group.name else ("project" if group.needs_approval else "global")
    print(f"Name:        {resolved.display}")
    print(f"Scope:       {scope}" + ("  (hides a global shortcut of the same name)" if resolved.hides_global else ""))
    print(f"Defined in:  {group.defining_file(entry.name)}")
    if group.local_file:
        print(f"Source:      {group.source(entry.name)}")
    print(f"CWD:         {entry.cwd or CALLER}")
    print(f"Run CWD:     {resolved.run_cwd}")
    print(f"Description: {entry.description}")
    if entry.args is not None:
        print(f"Usage:       {usage_line(resolved.display, entry.args)}")
    if entry.confirm:
        print("Confirm:     True")
    if entry.export:
        print("Export:      True")
    if entry.created:
        print(f"Created:     {entry.created}")
    if entry.created_by:
        print(f"Created by:  {entry.created_by}")
    if entry.icon:
        print(f"Icon:        {entry.icon}")
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


def show_json(words: List[str], skip_local: bool) -> int:
    """One JSON object: the entry as written, plus where it resolves. A broken entry still prints, with
    its error and null for what can't be worked out."""
    group, name, raw, rest, hides_global = find_entry(words, skip_local=skip_local)
    if rest:
        die(f"too many names: {shlex.join(words)}")
    out: Dict[str, Any] = {
        "name": group.display(name),
        "scope": "namespace" if group.name else ("project" if group.needs_approval else "global"),
        "defined_in": group.defining_file(name),
        "source": group.source(name) if group.local_file else None,
        "hides_global": hides_global,
        "approval": approval_state(group),
        "run_cwd": None,
        "script_path": None,
        "error": None,
        "entry": raw,
    }
    try:
        entry = load_entry(group, name, raw)
        out["run_cwd"] = resolve_run_dir(entry.cwd, group, os.getcwd())
        if entry.script is not None:
            out["script_path"] = resolve_script_path(group.script_base, entry.script)
    except ValueError as e:
        out["error"] = str(e)
    print(json.dumps(out, indent=2))
    return 0


def cmd_location(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut location",
        usage="%(prog)s [-h] [--global] [namespace] name",
        description="Print the path of a shortcut's script",
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    resolved = resolve_name(parsed.name, parsed.skip_local)
    entry = resolved.entry
    if entry.inline is not None:
        die(f"'{resolved.display}' is an inline shortcut, so it has no file. Change it in the file `shortcut show` names.")
    print(os.path.abspath(resolve_script_path(resolved.group.script_base, entry.script)))
    return 0


def rm_local(parsed: argparse.Namespace) -> int:
    """Take a name out of .shortcuts.local, which also undoes an override or a hide. A name only in
    .shortcuts is hidden instead."""
    if parsed.skip_local:
        die("--local and --global can't be used together")
    if len(parsed.name) > 1:
        die("--local takes one name: namespaces aren't in .shortcuts.local")
    name = parsed.name[0]
    group = load_store(local_project_root(os.getcwd())).main
    if name not in group.local_shortcuts and name not in group.shared_shortcuts:
        die(f"'{name}' isn't in {PROJECT_FILE} or {LOCAL_FILE} here")
    verb = "remove" if name in group.local_shortcuts else "hide"
    if not parsed.yes:
        if not sys.stdin.isatty():
            die(f"{verb} '{name}' in a terminal, or pass -y")
        if not ask(f"{verb} '{name}' in {group.local_file}? [y/N] "):
            print("Aborted.", file=sys.stderr)
            return 1

    def change(shortcuts: Dict[str, Any]) -> None:
        if name in shortcuts:
            del shortcuts[name]
        else:
            shortcuts[name] = None

    note_if_unapproved(write_local(group.store_dir, change))
    print(f"Hid shortcut '{name}' from you." if verb == "hide" else f"Removed '{name}' from {LOCAL_FILE}.")
    return 0


def cmd_rm(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut rm", usage="%(prog)s [-h] [-y] [--global | --local] [namespace] name", description="Remove a shortcut"
    )
    parser.add_argument("name", nargs="+", help="Shortcut name, or namespace and name")
    parser.add_argument("-y", "--yes", action="store_true", help="Do not prompt for confirmation")
    parser.add_argument("--local", action="store_true",
                        help="Remove it from .shortcuts.local, or hide a shared shortcut from you there")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    if parsed.local:
        return rm_local(parsed)

    # Raw lookup: an entry that can't run must still be removable
    group, name, raw_entry, rest, _ = find_entry(parsed.name, skip_local=parsed.skip_local)
    if rest:
        die(f"too many names: {shlex.join(parsed.name)}")
    display = group.display(name)
    if group.is_project:
        die(f"'{display}' is a project shortcut, and shortcut doesn't change {PROJECT_FILE}. "
            f"Edit it by hand, or use: shortcut rm --local {display}")

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

    # 2. Remove the stored copy: only files shortcut keeps in its scripts folder, never a --script
    #    file or a project's own file. A copy is always recorded as a relative path; --script never is outside a project.
    script = raw_entry.get("script") if isinstance(raw_entry, dict) else None
    if isinstance(script, str) and script and not os.path.isabs(script):
        with contextlib.suppress(OSError, ValueError):
            stored_path = resolve_script_path(group.script_base, script)
            if pathlib.Path(stored_path).resolve().is_relative_to(pathlib.Path(scripts_dir(group)).resolve()):
                os.unlink(stored_path)

    print(f"Removed shortcut '{display}'.")
    note_if_unapproved(group)
    return 0


def cmd_promote(args: List[str]) -> int:
    parser = Parser(prog="shortcut promote", description="Copy a project shortcut into your global shortcuts")
    parser.add_argument("name", help="Project shortcut name")
    parser.add_argument("--as", dest="new_name", metavar="NEW", help="Global name to give it")
    parser.add_argument("-y", "--yes", action="store_true", help="Do not prompt for confirmation")
    parser.add_argument("--force", action="store_true", help="Promote an entry that isn't exported")
    parsed = parser.parse_args(args)

    caller_cwd = os.getcwd()
    if find_project_root(caller_cwd) is None:
        die(f"promote runs inside a project: there's no {PROJECT_FILE} or {LOCAL_FILE} here or above")
    name, new_name = parsed.name, parsed.new_name or parsed.name
    validate_name(new_name)
    resolved, _ = resolve_shortcut([name], caller_cwd, strict=True)
    group, entry = resolved.group, resolved.entry
    if not group.is_project or group.name:
        die(f"'{name}' isn't a project shortcut here")
    require_approved(group)
    if not entry.export and not parsed.force:
        die(f"'{name}' isn't exported, so it's written for this project. Copy it anyway with: "
            f"shortcut promote {name} --force")

    home = load_store(get_global_store_dir())
    if home.main.error:
        die(home.main.error)
    if new_name in home.main.shortcuts or new_name in home.namespaces:
        if parsed.new_name is None:
            die(f"you already have a global '{name}'; give this one another name: shortcut promote {name} --as NEW")
        die(f"you already have a global '{new_name}'")

    fields = entry.to_dict()
    fields.pop("export", None)  # a global entry can't export
    old_cwd = entry.cwd
    if is_relative_cwd(old_cwd):
        fields["cwd"] = os.path.realpath(resolve_run_dir(old_cwd, group, caller_cwd))  # what was approved

    raw: Optional[bytes] = None
    if entry.script is not None:
        src = resolve_script_path(group.script_base, entry.script)
        if not os.path.isfile(src):
            die(f"script file not found: {src}")
        with open(src, "rb") as f:
            raw = f.read()
        # Copy what was approved: the file may have changed since require_approved hashed it
        record = load_approval(group) or {}
        key = display_path(group, src)
        if reviewable(raw) not in (record.get("snapshot", {}).get(key), record.get("local", {}).get(key)):
            die(f"{src} changed since you approved it; run: {group.update_cmd}")
        fields["script"], dest = _stored_script_field(home.main, new_name + (pathlib.Path(src).suffix or ".sh"))
    new_entry = ShortcutEntry.from_dict(new_name, fields)

    print(f"From {group.defining_file(name)}, as your global '{new_name}':")
    print(json.dumps({new_name: fields}, indent=2))
    if raw is not None:
        print(f"\nScript {src}, copied to {dest}:")
        print(reviewable(raw).rstrip())
    if fields.get("cwd") != old_cwd:
        print(f"\ncwd: '{old_cwd}' becomes {fields['cwd']}. It will always run there, in this checkout.")
    if not parsed.yes:
        if not sys.stdin.isatty():
            die(f"promote '{name}' in a terminal, or pass -y")
        if not ask(f"add '{new_name}' to {home.main.file}? [y/N] "):
            print("Aborted.", file=sys.stderr)
            return 1

    def write_script() -> None:
        with open(dest, "wb", opener=lambda path, flags: os.open(path, flags | getattr(os, "O_NOFOLLOW", 0), 0o755)) as f:
            f.write(raw)
        os.chmod(dest, 0o755)

    _register(home.main, new_entry, write_script if raw is not None else None)
    print(f"Promoted '{name}' to your global shortcuts as '{new_name}'.")
    if fields.get("cwd") != old_cwd:
        print(f"It will always run in {fields['cwd']}, this checkout's '{old_cwd}'.")
    if new_name == name:
        print(f"note: inside this project '{name}' is still the project's; shortcut r --global {name} runs yours",
              file=sys.stderr)
    return 0


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
    shared, local = shared_files(group), local_files(group)
    files = {**shared, **local}
    if approval_state(group) == "ok":
        print(f"{group.label}: already approved, nothing has changed.")
        return 0
    record = load_approval(group) or {"snapshot": {}}
    old = {**record["snapshot"], **record.get("local", {})}
    print(f"{'Changes to' if old else 'New to you:'} {group.label}")
    show_diff(diff_files(old, files))
    for name in sorted(set(group.shared_shortcuts) & set(group.local_shortcuts)):
        print(f"'{name}' in {PROJECT_FILE} is {group.source(name)} by {LOCAL_FILE} for you.")
    if not group.name:
        for name in sorted(set(group.shortcuts) & global_names):
            print(f"Inside this project, '{name}' hides your global '{name}'.")
    if not assume_yes:
        if not sys.stdin.isatty():
            die(f"approve {group.label} in a terminal, or pass -y")
        if not confirm_review("approve", old, files):
            print("Aborted.", file=sys.stderr)
            return 1
    save_approval(group, shared, local)
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
            die(f"no project shortcuts here: no {PROJECT_FILE} or {LOCAL_FILE} in this folder or its parents")
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
    from_project: bool = False,
) -> Tuple[Dict[str, Any], Dict[str, bytes], List[str]]:
    """What a collection would add to the global store, changing nothing: (entries, {stored script path:
    its bytes}, skipped). The bytes are read once here, so what gets copied is what was reviewed.
    From a project's .shortcuts, only exported entries come; a collection offers every entry."""
    entries: Dict[str, Any] = {}
    scripts: Dict[str, bytes] = {}
    skipped: List[str] = []
    dest_shortcuts = dest_registry.get("shortcuts", {})
    dest_namespaces = load_store(dest_store_dir).namespaces
    source_shortcuts = source_registry.get("shortcuts", {})
    if not isinstance(source_shortcuts, dict):
        return entries, scripts, skipped

    for name, entry_data in source_shortcuts.items():
        try:
            entry = ShortcutEntry.from_dict(name, entry_data)
            error = export_error(entry, from_project)
            if error:
                raise ValueError(error)
        except ValueError as e:
            skipped.append(f"{name} (invalid entry: {e})")
            continue
        if from_project and not entry.export:
            skipped.append(f"{name} (project-only)")
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

        # A collection's relative cwd means nothing once the entry leaves the file that defined it.
        # An exported entry can't have one, and says it works elsewhere.
        d = entry_data.get("cwd")
        if not from_project and d and d != CALLER and (not os.path.isabs(d) or not os.path.isdir(d)):
            skipped.append(f"{name} (cwd is relative or not found here: {d})")
            continue

        new_entry = dict(entry_data)
        new_entry.pop("export", None)  # a global entry can't export
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
        description="With --global, create the global store, or review a collection and copy it there",
    )
    parser.add_argument("source", nargs="?", default=None, help="Local path or git URL to import from (needs --global)")
    parser.add_argument("--global", dest="is_global", action="store_true", help="Import into global store")
    parser.add_argument("-y", "--yes", action="store_true", help="Import without asking")
    parsed = parser.parse_args(args)

    if not parsed.is_global:
        if parsed.source:
            die(
                f"a project's {PROJECT_FILE} is used in place, never copied: clone the project and run "
                "`shortcut update` there. To copy a collection into your global store, add --global."
            )
        die(f"a project's {PROJECT_FILE} is a file you write by hand, starting from: "
            f'{{"version": {REGISTRY_VERSION}, "shortcuts": {{}}}}. For your own shortcuts, use new --local.')

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

        # A collection is a shortcuts.json at its root or a project's .shortcuts; either way its
        # relative script paths resolve against the source root.
        candidates = [os.path.join(source_root, "shortcuts.json"), os.path.join(source_root, PROJECT_FILE)]
        source_file = next((path for path in candidates if os.path.isfile(path)), None)
        if source_file is None:
            die(f"no shortcuts.json or {PROJECT_FILE} found in {source_root}")

        entries, scripts, skipped = plan_import(
            source_base=source_root,
            dest_store_dir=dest_store_dir,
            dest_registry=load_registry(dest_store_dir) if registry_exists(dest_store_dir) else {},
            source_registry=load_registry_file(source_file),
            from_project=os.path.basename(source_file) == PROJECT_FILE,
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

    local subcommands="run r new list show location rm promote update init completion help"
    local name_commands="run r show location rm promote"

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
                    run|r) flags="--global -y" ;;
                    rm) flags="--global --local -y" ;;
                    promote) flags="--as -y --force" ;;
                    show) flags="--global --json" ;;
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
    subcommands=(run r new list show location rm promote update init completion help)

    if (( CURRENT == 2 )); then
        compadd -- $subcommands
        return
    fi

    case $words[2] in
        run|r|show|location|rm|promote)
            local -a typed
            typed=(${words[3,CURRENT-1]:#-*})
            if (( $#typed == 0 )) && [[ $PREFIX == -* ]]; then
                case $words[2] in
                    run|r) compadd -- --global -y ;;
                    rm) compadd -- --global --local -y ;;
                    show) compadd -- --global --json ;;
                    promote) compadd -- --as -y --force ;;
                    *) compadd -- --global ;;
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
complete -c shortcut -n __fish_use_subcommand -a "run r new list show location rm promote update init completion help"
complete -c shortcut -n "__fish_seen_subcommand_from run r show location rm promote" -a "(__shortcut_names)"
complete -c shortcut -n "__fish_seen_subcommand_from run r show location rm" -l global
complete -c shortcut -n "__fish_seen_subcommand_from run r rm promote" -s y
complete -c shortcut -n "__fish_seen_subcommand_from promote" -l as -r
complete -c shortcut -n "__fish_seen_subcommand_from promote" -l force
complete -c shortcut -n "__fish_seen_subcommand_from rm" -l local
complete -c shortcut -n "__fish_seen_subcommand_from show" -l json
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
  shortcut new <name> [FLAGS] [-- CMD...]   Create a shortcut: an inline command after --, a script
                                            opened in $EDITOR, or a file with --script or --copy
  shortcut list [--names [NAMESPACE]]       List visible shortcuts, with where each runs
  shortcut show [--global] [--json] <name>  Show shortcut metadata and script contents, or the entry as JSON
  shortcut location [--global] <name>       Print the path of its script (fails for inline)
  shortcut rm [--global|--local] <name> [-y]
                                            Remove a shortcut. With --local, take it out of .shortcuts.local,
                                            or hide a shared one from you
  shortcut promote <name> [--as NEW] [-y] [--force]
                                            Copy an approved, exported project shortcut, and its script,
                                            into your global shortcuts. --force takes one that isn't
                                            exported. The project entry stays as it is
  shortcut update [group] [-y]              Review changes to project shortcuts and namespaces, and approve them
  shortcut init --global [SOURCE] [-y]      Review a collection (folder or git URL) and copy it into the
                                            global store. Later changes to the source don't reach it
  shortcut completion bash|zsh|fish         Print a shell completion script
  shortcut help, -h, --help                 Show this help message
  shortcut --version                        Show version

FLAGS for new:
  --script PATH             Run this file where it is
  --copy FILE|-             Copy this file, or stdin, into the store
  --desc D                  Description
  --cwd caller|DIR          Where it runs (default: caller, your current directory)
  --arg NAME[=DEFAULT]      Declare a required argument, or an optional one with a default
  --confirm                 Ask before running it
  --local                   Store it in .shortcuts.local in this directory (default: the global store)
  --namespace NAME          Put it in namespace NAME in the global store, creating it if needed
  --by C                    Creator identifier

Project shortcuts and namespaces don't run until approved with `shortcut update`,
and must be approved again after any change to their definitions. Approval covers
.shortcuts, .shortcuts.local, imports and the scripts they name, not the programs those
call. A change shortcut makes to .shortcuts.local keeps its approval.
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
        "list": cmd_list,
        "show": cmd_show,
        "location": cmd_location,
        "rm": cmd_rm,
        "promote": cmd_promote,
        "update": cmd_update,
        "init": cmd_init,
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
