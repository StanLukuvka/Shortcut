"""Adding an entry to a store: which file it goes in, under that store's lock, keeping an approval that still
holds."""

from __future__ import annotations

import argparse
import os
import sys
from typing import Any, Dict, Optional

from .helpers import (
    approval_state, die, exclude_from_git, get_global_store_dir, IMPORT_SUFFIX, init_store, load_approval,
    load_registry_file, LOCAL_FILE, local_files, local_is_approved, local_project_root, PROJECT_FILE,
    registry_exists, registry_lock, REGISTRY_VERSION, save_approval, save_registry_file, scripts_dir, shared_files,
    validate_name,
)
from .shortcut_entry import ShortcutEntry
from .group import Group
from .stores import load_project_group, load_store, reload_group


def note_if_unapproved(group: Group) -> None:
    """After a change made through shortcut: say so if it needs approving before it runs."""
    if group.needs_approval and approval_state(reload_group(group)) != "ok":
        print(f"note: {group.label} needs approving before it runs: {group.update_cmd}", file=sys.stderr)


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
