"""Finding a shortcut by name in the project, the global store and their namespaces."""

from __future__ import annotations

import os
import pathlib
import shlex
from typing import Any, List, Optional, Tuple

from .helpers import die, find_project_root, never_a_project, PROJECT_FILE, resolve_run_dir
from .group import Group
from .store import Store
from .resolved_shortcut import ResolvedShortcut
from .stores import load_entry, load_store, visible_stores


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
        # A project's name, shortcut or namespace, hides a global one of either kind
        hides = store is not home and (first in home.main.shortcuts or first in home.namespaces)
        if first in store.main.shortcuts and (not rest or first not in store.namespaces):
            return store.main, first, store.main.shortcuts[first], rest, hides
        if first in store.namespaces:
            group = store.namespaces[first]
            if group.error:
                raise ValueError(group.error)
            members = ", ".join(sorted(group.shortcuts)) or "(none)"
            if not rest:
                raise ValueError(f"'{first}' is a namespace; run one of its shortcuts: {members}")
            if rest[0] not in group.shortcuts:
                yours = home.namespaces.get(first) if hides else None
                note = (f". Your global '{first}' has it: shortcut r --global {first} {rest[0]}"
                        if yours is not None and rest[0] in yours.shortcuts else "")
                raise KeyError(f"'{rest[0]}' not found in namespace '{first}'. It has: {members}{note}")
            return group, rest[0], group.shortcuts[rest[0]], rest[1:], hides
    if rest:  # `first` may be meant as a namespace
        missing = (f"shortcut or namespace '{first}' not found. A global namespace is the file "
                   f"{os.path.join(home.main.store_dir, 'imports', first + '.shortcut')}.")
    else:
        missing = f"shortcut '{first}' not found."
    notes = [missing, *([home.main.error] if home.main.error else [])]
    root = find_project_root(caller_cwd)
    if root and skip_local:
        if has_name(load_store(root), first):
            notes.append(f"The project in {root} has its own '{first}': leave out --global to use it.")
    elif root:
        outer = find_project_root(os.path.dirname(root))
        if outer and outer != root and has_name(load_store(outer), first):
            notes.append(f"The project in {outer} has a '{first}', but only the nearest project, {root}, is used here.")
    else:
        old = old_project_folder(caller_cwd)
        if old:
            notes.append(f"{old} is a folder, which is how shortcut 1.x kept a project. This version reads a "
                         f"{PROJECT_FILE} file, so nothing in that folder is used.")
    raise KeyError(" ".join(notes))


def has_name(store: Store, name: str) -> bool:
    return name in store.main.shortcuts or name in store.namespaces


def old_project_folder(caller_cwd: Optional[str]) -> Optional[str]:
    """The nearest .shortcuts folder above the caller that isn't the global store."""
    curr = pathlib.Path(caller_cwd or os.getcwd()).resolve()
    for folder in [curr, *curr.parents]:
        path = folder / PROJECT_FILE
        if path.is_dir() and path.resolve() not in never_a_project():
            return str(path)
    return None


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
