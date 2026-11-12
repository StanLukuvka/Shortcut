"""Loading stores: the global store, a project's files and its namespaces, as Groups and a Store."""

from __future__ import annotations

import dataclasses
import os
from typing import Any, Dict, List, Optional

from .helpers import (
    BARE_IMPORT, file_error, find_project_root, get_global_store_dir, IMPORT_SUFFIX, is_global_store,
    load_registry_file, LOCAL_FILE, placement_error, PROJECT_FILE, resolve_script_path, validate_name,
)
from .shortcut_entry import ShortcutEntry
from .group import Group
from .store import Store


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


def reload_group(group: Group) -> Group:
    return load_project_group(group.store_dir) if group.local_file else load_group(group.file, group.store_dir, group.name)
