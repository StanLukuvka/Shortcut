"""The commands and main, and the code that builds the classes: loading stores and groups, finding a shortcut,
planning an import."""

from __future__ import annotations

import argparse
import contextlib
import dataclasses
import json
import os
import pathlib
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from typing import Any, Dict, List, NoReturn, Optional, Set, Tuple

from .helpers import (
    _stored_script_field, add_entry_flags, add_global_flag, apply_import, apply_namespaces, approval_path,
    approval_state, arg_lines, BARE_IMPORT, BINARY_MARK, bind_args, CALLER, die, diff_files, entry_fields,
    exclude_from_git, exec_child, file_error, find_project_root, get_global_store_dir, IMPORT_SUFFIX, init_store,
    installed_files, is_git_source, is_global_store, is_listed_import, is_relative_cwd, join_command, load_approval,
    load_registry, load_registry_file, LOCAL_FILE, local_files, local_is_approved, local_project_root,
    namespace_folder_problem, one_line, placement_error, POSITIONAL, PROJECT_FILE, refuse_symlinks, registry_exists,
    registry_lock, REGISTRY_VERSION, remove_namespace_files, require_approved, resolve_run_dir, resolve_script_path,
    reviewable, rm_local_message, rm_message, safe, save_approval, save_registry_atomic, save_registry_file,
    scripts_dir, shared_files, show_diff, stored_copy, usage_line, validate_name, VERSION, write_styled,
)
from .shortcut_entry import ShortcutEntry
from .safe_output import SafeOutput
from .parser import Parser
from .group import Group
from .store import Store
from .resolved_shortcut import ResolvedShortcut
from .confirm import Confirm


def load_entry(group: "Group", name: str, data: Any) -> ShortcutEntry:
    """An entry as its group allows it: ShortcutEntry.from_dict, plus where 'export' and 'from' can be."""
    entry = ShortcutEntry.from_dict(name, data)
    error = placement_error(entry, bool(group.local_file), not group.name and not group.is_project)
    if error:
        raise ValueError(error)
    return entry


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


def note_if_unapproved(group: Group) -> None:
    """After a change made through shortcut: say so if it needs approving before it runs."""
    if group.needs_approval and approval_state(reload_group(group)) != "ok":
        print(f"note: {group.label} needs approving before it runs: {group.update_cmd}", file=sys.stderr)


def find_entry(
    words: List[str],
    caller_cwd: Optional[str] = None,
    skip_local: bool = False,
    strict: bool = False,
) -> Tuple[Group, str, Any, List[str], bool]:
    """Find `name` or `namespace name` without judging the entry: (group, name, raw entry, leftover
    words, hides a global one). strict (for run) refuses every name inside a project whose file can't be
    used, and a store's load errors for the name it has. Without it (show, location, rm) a broken file is
    looked in as far as it reads, so you can still see and fix what's there."""
    stores = visible_stores(caller_cwd, skip_local)
    first, rest = words[0], words[1:]
    home = stores[-1]

    for store in stores:
        if strict and store is not home and store.main.error:
            raise ValueError(
                f"{store.main.error}. Nothing runs inside this project until it's fixed; "
                f"for your own: shortcut r --global {first}"
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
    if rest:  # `first` may be meant as a namespace
        missing = (f"shortcut or namespace '{first}' not found. A global namespace is the file "
                   f"{os.path.join(home.main.store_dir, 'imports', first + '.shortcut')}.")
    else:
        missing = f"shortcut '{first}' not found."
    notes = [missing, *([home.main.error] if home.main.error else [])]
    root = find_project_root(caller_cwd) if skip_local else None
    if root:
        project = load_store(root)
        if first in project.main.shortcuts or first in project.namespaces:
            notes.append(f"The project in {root} has its own '{first}': leave out --global to use it.")
    raise KeyError(" ".join(notes))


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


def run_shortcut(words: List[str], skip_local: bool = False, assume_yes: bool = False) -> int:
    caller_cwd = os.getcwd()
    resolved, args = resolve_shortcut(words, caller_cwd, skip_local=skip_local, strict=True)
    require_approved(resolved.group)
    entry, name = resolved.entry, resolved.display
    typed = args  # as given, for the question and the command that answers it
    arg_env: Dict[str, str] = {}
    if entry.args is not None:
        args, arg_env = bind_args(name, entry.args, args)
    if not os.path.isdir(resolved.run_cwd):
        raise FileNotFoundError(f"run directory does not exist: {resolved.run_cwd}")
    with_args = f" with {one_line(shlex.join(typed))}" if typed else ""
    again = shlex.join(["shortcut", "r", "-y", *(["--global"] if skip_local else []), *name.split(), *typed])
    if entry.confirm and not Confirm(
        f"run '{name}'{with_args} in {os.path.realpath(resolved.run_cwd)}?",
        f"'{name}' asks for confirmation: run it in a terminal, or pass -y ({one_line(again)})",
        "" if entry.description in ("(no description)", entry.inline) else one_line(entry.description),
    ).granted(assume_yes):
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

    # A shortcut run from another one gets its own SC_ARG_ variables, not the caller's
    env = {key: value for key, value in os.environ.items() if not key.startswith("SC_ARG_")}
    env["SHORTCUT_NAME"] = name
    env["SHORTCUT_NAMESPACE"] = resolved.group.name
    env["SHORTCUT_CALLER_CWD"] = caller_cwd
    env.update(arg_env)

    return exec_child(cmd, resolved.run_cwd, env)


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
    keep: Optional[Dict[str, str]] = None
    with registry_lock(store_dir):
        is_new_file = bool(group.name) and not os.path.exists(group.file)
        approved = namespace_is_approved(group)
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
        if approved:
            keep = shared_files(reload_group(group))
    if keep is not None:
        save_approval(group, keep, {})  # after the lock: for a namespace, save_approval takes the same one
    note_if_unapproved(group)


def reload_group(group: Group) -> Group:
    return load_project_group(group.store_dir) if group.local_file else load_group(group.file, group.store_dir, group.name)


def namespace_is_approved(group: Group) -> bool:
    """Before shortcut writes to a global namespace, under its lock: whether the file matches its approval.
    Only then does the approval follow the write, as write_local does, so a hand edit still needs update."""
    return bool(group.name) and os.path.exists(group.file) and approval_state(reload_group(group)) == "ok"


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

    def write_script() -> None:
        refuse_symlinks(group, abs_script_path)
        with open(abs_script_path, "w", encoding="utf-8") as f:
            f.write("#!/usr/bin/env bash\nset -euo pipefail\n\n")
        os.chmod(abs_script_path, 0o755)

    entry = ShortcutEntry(
        name=parsed.name,
        description=parsed.desc if parsed.desc is not None else "(no description)",
        script=rel_script_path,
        **fields,
    )
    _register(group, entry, write_script)
    print(f"Created script shortcut '{parsed.name}'.")
    print(f"Edit it at: {abs_script_path}")
    return 0


def new_from_script(parsed: argparse.Namespace, group: Group, fields: Dict[str, Any]) -> int:
    """new --script: the entry names the file where it is. A file inside the folder its paths resolve from
    (the project, the namespace file's folder, the global store) is kept relative, so the folder can move.
    One in the scripts folder stays absolute: a relative path there is a copy, which rm deletes."""
    path = parsed.script
    abs_path = os.path.abspath(os.path.expanduser(path))
    if path == "-" or not os.path.isfile(abs_path):
        die(f"script file not found: {path}")
    base = os.path.realpath(group.script_base)
    real = os.path.realpath(abs_path)
    copies = os.path.realpath(scripts_dir(group))
    inside = pathlib.Path(real).is_relative_to(base) and not pathlib.Path(real).is_relative_to(copies)
    script = os.path.relpath(real, base) if inside else abs_path
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
        if entry.args:
            print("Arguments:\n" + "\n".join(arg_lines(entry.args)))
    if entry.confirm:
        print("Confirm:     True")
    if entry.export:
        print("Export:      True")
    if entry.origin is not None:
        print(f"From:        '{entry.origin['name']}' in {entry.origin['source']}")
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
        "name": name,
        "namespace": group.name or None,
        "scope": "namespace" if group.name else ("project" if group.needs_approval else "global"),
        "defined_in": group.defining_file(name),
        "source": group.source(name) if group.local_file else None,
        "hides_global": hides_global,
        "approval": approval_state(group),
        "cwd": None,
        "run_cwd": None,
        "script_path": None,
        "error": None,
        "entry": raw,
    }
    try:
        entry = load_entry(group, name, raw)
        out["cwd"] = entry.cwd
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
    unreadable = load_group(group.file, group.store_dir).error  # .shortcuts can't be read: hide the name anyway
    if name not in group.local_shortcuts and name not in group.shared_shortcuts and not unreadable:
        die(f"'{name}' isn't in {PROJECT_FILE} or {LOCAL_FILE} here")
    verb = "remove" if name in group.local_shortcuts else "hide"
    if not Confirm(f"{verb} '{name}'?", f"{verb} '{name}' in a terminal, or pass -y",
                   rm_local_message(group, name, unreadable), tell_on_yes=True).granted(parsed.yes):
        return 1

    def change(shortcuts: Dict[str, Any]) -> None:
        if name in shortcuts:
            del shortcuts[name]
        else:
            shortcuts[name] = None

    note_if_unapproved(write_local(group.store_dir, change))
    print(f"Hid shortcut '{name}' from you." if verb == "hide" else f"Removed '{name}' from {LOCAL_FILE}.")
    return 0


def rm_namespace(ns: str, assume_yes: bool) -> int:
    """rm --namespace: a global namespace, whole. One in imports/ goes with its scripts folder. One that
    shortcuts.json imports from elsewhere is only no longer imported: its files are someone's own."""
    store_dir = get_global_store_dir()
    store = load_store(store_dir)
    group = store.namespaces.get(ns)
    if group is None:
        die(f"no namespace named '{ns}' in your global store ({os.path.join(store_dir, 'imports')})")
    listed = is_listed_import(store, ns)
    folder = scripts_dir(group)
    if listed:
        lines = [f"Stops importing '{ns}' in {store.main.file}", f"Keeps its file: {group.file}"]
    else:
        lines = [f"Removes namespace '{ns}' ({len(group.shortcuts)} shortcuts): {group.file}"]
        if os.path.lexists(folder):
            lines.append(f"Deletes its scripts folder: {folder}")
    if not Confirm(f"remove namespace '{ns}'?", f"remove namespace '{ns}' in a terminal, or pass -y",
                   "\n".join(lines), tell_on_yes=True).granted(assume_yes):
        return 1
    approval = approval_path(group)  # its key needs the file, so before it goes
    with registry_lock(store_dir):
        if listed:
            reg = load_registry(store_dir)
            reg["imports"].pop(ns, None)
            if not reg["imports"]:
                del reg["imports"]
            save_registry_atomic(store_dir, reg)
        else:
            remove_namespace_files(group)
        with contextlib.suppress(FileNotFoundError):
            os.unlink(approval)
    print(f"Removed namespace '{ns}'.")
    return 0


def cmd_rm(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut rm", usage="%(prog)s [-h] [-y] [--global | --local] [namespace] name\n       %(prog)s [-y] --namespace NS",
        description="Remove a shortcut, or a whole namespace"
    )
    parser.add_argument("name", nargs="*", help="Shortcut name, or namespace and name")
    parser.add_argument("--namespace", metavar="NS", help="Remove this namespace from the global store, with its scripts folder")
    parser.add_argument("-y", "--yes", action="store_true", help="Do not prompt for confirmation")
    parser.add_argument("--local", action="store_true",
                        help="Remove it from .shortcuts.local, or hide a shared shortcut from you there")
    add_global_flag(parser)
    parsed = parser.parse_args(args)

    if parsed.namespace is not None:
        if parsed.local or parsed.name:
            die(f"--namespace takes the namespace alone: shortcut rm --namespace {parsed.namespace}")
        return rm_namespace(parsed.namespace, parsed.yes)
    if not parsed.name:
        parser.error("the following arguments are required: name")
    if parsed.local:
        return rm_local(parsed)

    # A project that can't be read might hide the name, so rm doesn't reach past it to a global one
    root = None if parsed.skip_local else find_project_root(os.getcwd())
    broken = load_project_group(root).error if root else None
    if broken:
        die(f"{broken}. Inside this project rm only takes --local until it's fixed; "
            f"for your own: shortcut rm --global {shlex.join(parsed.name)}")

    # Raw lookup: an entry that can't run must still be removable
    group, name, raw_entry, rest, _ = find_entry(parsed.name, skip_local=parsed.skip_local)
    if rest:
        die(f"too many names: {shlex.join(parsed.name)}")
    display = group.display(name)
    if group.is_project:
        die(f"'{display}' is a project shortcut, and shortcut doesn't change {PROJECT_FILE}. "
            f"Edit it by hand, or use: shortcut rm --local {display}")

    copy = stored_copy(group, raw_entry)
    if not Confirm(f"remove '{display}'?", f"remove '{display}' in a terminal, or pass -y",
                   rm_message(group, display, raw_entry, copy), tell_on_yes=True).granted(parsed.yes):
        return 1

    keep: Optional[Dict[str, str]] = None
    with registry_lock(group.store_dir):
        approved = namespace_is_approved(group)
        # 1. Remove from registry
        reg = load_registry_file(group.file)
        shortcuts = reg.get("shortcuts", {})
        if name in shortcuts:
            del shortcuts[name]
            save_registry_file(group.file, reg)

        # 2. Remove the stored copy, never a --script file
        if copy:
            with contextlib.suppress(OSError):
                os.unlink(copy)
        if approved:
            keep = shared_files(reload_group(group))
    if keep is not None:
        save_approval(group, keep, {})  # after the lock: for a namespace, save_approval takes the same one

    print(f"Removed shortcut '{display}'.")
    note_if_unapproved(group)
    return 0


def cmd_promote(args: List[str]) -> int:
    parser = Parser(prog="shortcut promote", description="Add a project shortcut to your global shortcuts, run from this checkout")
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
        die(f"'{name}' is marked project-only; promote anyway with --force")

    home = load_store(get_global_store_dir())
    if home.main.error:
        die(home.main.error)
    if new_name in home.main.shortcuts or new_name in home.namespaces:
        if parsed.new_name is None:
            die(f"you already have a global '{name}'; give this one another name: shortcut promote {name} --as NEW")
        die(f"you already have a global '{new_name}'")

    # The entry, pointing at this checkout's files: nothing is copied, so it runs them as they are then
    fields = entry.to_dict()
    fields.pop("export", None)  # a global entry can't export
    if entry.script is not None:
        fields["script"] = resolve_script_path(group.script_base, entry.script)
    if is_relative_cwd(entry.cwd):
        fields["cwd"] = os.path.realpath(resolve_run_dir(entry.cwd, group, caller_cwd))
    fields["from"] = {"source": os.path.realpath(group.store_dir), "name": name}
    new_entry = ShortcutEntry.from_dict(new_name, fields)

    print(f"From {group.defining_file(name)}, as your global '{new_name}':")
    print(json.dumps({new_name: fields}, indent=2))
    print("This runs the project's files as they are now, like typing the command there. "
          "It isn't reviewed again when they change.")
    if not Confirm(f"add '{new_name}' to {home.main.file}?",
                   f"promote '{name}' in a terminal, or pass -y").granted(parsed.yes):
        return 1

    _register(home.main, new_entry)
    print(f"Promoted '{name}' to your global shortcuts as '{new_name}'.")
    if new_name == name:
        print(f"note: inside this project '{name}' is still the project's; shortcut r --global {name} runs yours",
              file=sys.stderr)
    return 0


def confirm_review(verb: str, old: Dict[str, str], new: Dict[str, str], no_terminal: str) -> Confirm:
    """The question after a diff. A binary file can't be shown, so taking one unseen has to be typed out."""
    unseen = sorted(path for path, text in new.items() if text.startswith(BINARY_MARK) and old.get(path) != text)
    if unseen:
        return Confirm(f"can't show {', '.join(unseen)}. Type binary to {verb} it unseen:", no_terminal, accept=("binary",))
    return Confirm(f"{verb}?", no_terminal)


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
    where = f" ({safe(os.path.realpath(group.file))})" if group.name else ""  # the real place, wherever it points
    print(f"{'Changes to' if old else 'New to you:'} {group.label}{where}")
    show_diff(diff_files(old, files))
    for name in sorted(set(group.shared_shortcuts) & set(group.local_shortcuts)):
        print(f"'{name}' in {PROJECT_FILE} is {group.source(name)} by {LOCAL_FILE} for you.")
    if not group.name:
        for name in sorted(set(group.shortcuts) & global_names):
            print(f"Inside this project, '{name}' hides your global '{name}'.")
    for name, data in sorted(group.shortcuts.items()):
        if data.get("export") and is_relative_cwd(data.get("cwd")):
            print(f"warning: '{name}' is exported, but its cwd '{data['cwd']}' only exists in this checkout",
                  file=sys.stderr)
    if not confirm_review("approve", old, files, f"approve {group.label} in a terminal, or pass -y").granted(assume_yes):
        return 1
    save_approval(group, shared, local)
    covers = "its definitions and its scripts folder" if group.name else "its definitions"
    print(f"Approved {group.label}. This covers {covers}, not what they call. "
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


def plan_import(
    source_base: str,
    dest_store_dir: str,
    dest_registry: Dict[str, Any],
    source_registry: Dict[str, Any],
) -> Tuple[Dict[str, Any], Dict[str, bytes], List[str]]:
    """What a source's .shortcuts would add to the global store, changing nothing: (entries, {stored script
    path: its bytes}, skipped). Only exported entries come. The bytes are read once here, so what gets
    copied is what was reviewed."""
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
            error = placement_error(entry, in_project=True, in_global=False)  # 'from' is promote's, never a source's
            if error:
                raise ValueError(error)
        except ValueError as e:
            skipped.append(f"{name} (invalid entry: {e})")
            continue
        if not entry.export:
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

        # A relative cwd means nothing once the entry leaves the file that defined it
        d = entry_data.get("cwd")
        if is_relative_cwd(d):
            skipped.append(f"{name} (exported, but its cwd '{d}' only exists in the project)")
            continue
        if d and d != CALLER and not os.path.isdir(os.path.expanduser(d)):
            skipped.append(f"{name} (cwd not found here: {d})")
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


def plan_namespaces(
    source_root: str, dest_store_dir: str, replace: bool = False
) -> Tuple[Dict[str, Dict[str, Tuple[bytes, bool]]], List[str]]:
    """What a folder of namespace files would add to the global store's imports/, changing nothing:
    ({namespace: {path under imports/: (its bytes, executable)}}, skipped). A namespace comes whole, as
    <ns>.shortcut and its scripts/<ns>/ folder, the layout it has in imports/, or not at all. With
    replace, one already in imports/ is planned too, to go in place of what's there."""
    plans: Dict[str, Dict[str, Tuple[bytes, bool]]] = {}
    skipped: List[str] = []
    dest = load_store(dest_store_dir)
    imports_dir = os.path.join(dest_store_dir, "imports")
    for filename in sorted(os.listdir(source_root)):
        if not filename.endswith(IMPORT_SUFFIX):
            continue
        ns = filename[:-len(IMPORT_SUFFIX)]
        path = os.path.join(source_root, filename)
        folder = os.path.join("scripts", ns)
        try:
            validate_name(ns)  # untrusted source
        except ValueError as e:
            skipped.append(f"{filename} ({e})")
            continue
        replacing = replace and ns in dest.namespaces and not is_listed_import(dest, ns)
        if os.path.islink(path) or not os.path.isfile(path):
            skipped.append(f"{ns} (not a regular file: {filename})")
        elif replace and is_listed_import(dest, ns):
            skipped.append(f"{ns} (imported from {dest.namespaces[ns].file}, so it isn't replaced)")
        elif not replacing and (ns in dest.namespaces or os.path.lexists(os.path.join(imports_dir, filename))):
            skipped.append(f"{ns} (already a namespace; --replace puts this one in its place)")
        elif ns in dest.main.shortcuts:
            skipped.append(f"{ns} (already a shortcut)")
        elif not replacing and os.path.lexists(os.path.join(imports_dir, folder)):
            skipped.append(f"{ns} (would replace imports/{folder})")
        elif (group := load_group(path, source_root, ns)).error:
            skipped.append(f"{ns} ({group.error})")
        elif problem := namespace_folder_problem(group, source_root, folder):
            skipped.append(f"{ns} ({problem})")
        else:
            files = {filename: (pathlib.Path(path).read_bytes(), False)}
            for dirpath, _, names in os.walk(os.path.join(source_root, folder)):
                for name in names:
                    file = os.path.join(dirpath, name)
                    files[os.path.relpath(file, source_root)] = (pathlib.Path(file).read_bytes(), os.access(file, os.X_OK))
            plans[ns] = files
    return plans, skipped


def init_namespaces(source_str: str, source_root: str, dest_store_dir: str, assume_yes: bool, replace: bool) -> int:
    """init --global from a folder of <ns>.shortcut files: copy each into imports/, or with replace in
    place of the one there. Like any namespace, it runs once shortcut update approves it."""
    plans, skipped = plan_namespaces(source_root, dest_store_dir, replace)
    installed = load_store(dest_store_dir).namespaces
    imports_dir = os.path.join(dest_store_dir, "imports")
    old: Dict[str, str] = {}
    for ns in list(plans):
        if ns not in installed:
            continue
        before = installed_files(installed[ns], imports_dir)
        if before == {os.path.join("imports", rel): reviewable(raw) for rel, (raw, _) in plans[ns].items()}:
            del plans[ns]
            skipped.append(f"{ns} (the same as what's installed)")
        else:
            old.update(before)
    if skipped:
        print(f"Skipped ({len(skipped)}):")
        for s in skipped:
            print(f"  {s}")
    if not plans:
        return 0
    files = {os.path.join("imports", rel): reviewable(raw) for plan in plans.values() for rel, (raw, _) in plan.items()}
    print(f"{'Changes' if old else 'New'} from {source_str}:")
    show_diff(diff_files(old, files))
    if not confirm_review("import", old, files, f"import {source_str} in a terminal, or pass -y").granted(assume_yes):
        return 1
    apply_namespaces(dest_store_dir, plans, [installed[ns] for ns in plans if ns in installed])
    print(f"Imported ({len(plans)}), to approve before they run:")
    for ns in plans:
        print(f"  shortcut update {ns}")
    return 0


def cmd_init(args: List[str]) -> int:
    parser = Parser(
        prog="shortcut init",
        description="With --global, create the global store, or review a project's exported shortcuts, or a folder of namespaces, and copy them there",
    )
    parser.add_argument("source", nargs="?", default=None, help="Local path or git URL to import from (needs --global)")
    parser.add_argument("--global", dest="is_global", action="store_true", help="Import into global store")
    parser.add_argument("-y", "--yes", action="store_true", help="Import without asking")
    parser.add_argument("--replace", action="store_true", help="Put a source's namespaces in place of installed ones of the same name")
    parsed = parser.parse_args(args)

    if not parsed.is_global:
        if parsed.source:
            die(
                f"a project's {PROJECT_FILE} is used in place, never copied: clone the project and run "
                "`shortcut update` there. To copy its exported shortcuts into your global store, add --global."
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

        # A source is a project: a .shortcuts at its root, whose relative script paths resolve from there.
        # Without one, it's a folder of namespaces laid out as in imports/.
        source_file = os.path.join(source_root, PROJECT_FILE)
        if not os.path.isfile(source_file):
            if any(name.endswith(IMPORT_SUFFIX) for name in os.listdir(source_root)):
                return init_namespaces(source_str, source_root, dest_store_dir, parsed.yes, parsed.replace)
            die(f"no {PROJECT_FILE} or <ns>{IMPORT_SUFFIX} found in {source_root}")
        if parsed.replace:
            die(f"--replace is for a folder of namespaces, and {source_root} has a {PROJECT_FILE}")

        entries, scripts, skipped = plan_import(
            source_base=source_root,
            dest_store_dir=dest_store_dir,
            dest_registry=load_registry(dest_store_dir) if registry_exists(dest_store_dir) else {},
            source_registry=load_registry_file(source_file),
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
        if not confirm_review("import", {}, files, f"import {source_str} in a terminal, or pass -y").granted(parsed.yes):
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
                    rm) flags="--global --local --namespace -y" ;;
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
            COMPREPLY=( $(compgen -W "--global --replace -y" -- "$cur") )
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
                    rm) compadd -- --global --local --namespace -y ;;
                    show) compadd -- --global --json ;;
                    promote) compadd -- --as -y --force ;;
                    *) compadd -- --global ;;
                esac
            elif (( $#typed <= 1 )); then
                compadd -- ${(f)"$(shortcut list --names $typed[1] 2>/dev/null)"}
            fi
            ;;
        init)
            compadd -- --global --replace -y
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
complete -c shortcut -n "__fish_seen_subcommand_from rm" -l namespace -r
complete -c shortcut -n "__fish_seen_subcommand_from show" -l json
complete -c shortcut -n "__fish_seen_subcommand_from init" -F -l global
complete -c shortcut -n "__fish_seen_subcommand_from init" -s y
complete -c shortcut -n "__fish_seen_subcommand_from init" -l replace
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
                                            from a template at the path it prints, or a file with
                                            --script or --copy
  shortcut list [--names [NAMESPACE]]       List visible shortcuts, with where each runs
  shortcut show [--global] [--json] <name>  Show shortcut metadata and script contents, or the entry as JSON
  shortcut location [--global] <name>       Print the path of its script (fails for inline)
  shortcut rm [--global|--local] <name> [-y]
                                            Remove a shortcut. With --local, take it out of .shortcuts.local,
                                            or hide a shared one from you
  shortcut rm --namespace <ns> [-y]         Remove a global namespace and its scripts/<ns>/ folder
  shortcut promote <name> [--as NEW] [-y] [--force]
                                            Add an approved, exported project shortcut to your global
                                            shortcuts. It runs this checkout's files as they are, not a
                                            copy. --force takes one that isn't exported
  shortcut update [group] [-y]              Review changes to project shortcuts and namespaces, and approve them
  shortcut init --global [SOURCE] [-y] [--replace]
                                            Review the exported shortcuts in a folder's or git URL's
                                            .shortcuts and copy them into the global store. Later
                                            changes to the source don't reach them. Without a .shortcuts,
                                            copy its <ns>.shortcut namespaces into imports/; --replace
                                            puts them in place of installed ones
  shortcut completion bash|zsh|fish         Print a shell completion script
  shortcut help, -h, --help                 Show this help message
  shortcut --version                        Show version

FLAGS for new:
  --script PATH             Run this file where it is
  --copy FILE|-             Copy this file, or stdin, into the store
  --desc D                  Description
  --cwd caller|DIR          Where it runs (default: caller, your current directory)
  --arg NAME[=DEFAULT]      Declare a required argument, or an optional one with a default.
                            NAME... takes the rest of the arguments; NAME...= makes it optional
  --arg-desc NAME=TEXT      Describe a declared argument, for show and usage errors
  --confirm                 Ask before running it
  --local                   Store it in .shortcuts.local in this directory (default: the global store)
  --namespace NAME          Put it in namespace NAME in the global store, creating it if needed
  --by C                    Creator identifier

Project shortcuts and namespaces don't run until approved with `shortcut update`,
and must be approved again after any change to their definitions. Approval covers
.shortcuts, .shortcuts.local, imports, the scripts they name and each namespace's
scripts/<ns>/ folder, not the programs those call. A change shortcut makes to .shortcuts.local or a namespace keeps its approval.
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
        print(f"error: unrecognized option '{first}'\nTry 'shortcut --help' for more information.", file=sys.stderr)
        sys.exit(2)

    print(f"error: unknown command '{first}' (to run a shortcut: shortcut run {first})", file=sys.stderr)
    sys.exit(2)
