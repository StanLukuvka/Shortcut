"""Reviewing a diff and approving a project or namespace, for shortcut update."""

from __future__ import annotations

import os
import sys
from typing import Dict, Set

from .helpers import (
    approval_state, BINARY_MARK, diff_files, is_relative_cwd, load_approval, LOCAL_FILE, local_files, PROJECT_FILE,
    safe, save_approval, shared_files, show_diff,
)
from .group import Group
from .confirm import Confirm
from .stores import entry_errors


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
