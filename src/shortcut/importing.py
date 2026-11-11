"""Planning init --global: a project's exported shortcuts, or a folder of namespace files."""

from __future__ import annotations

import os
import pathlib
from typing import Any, Dict, List, Tuple

from .helpers import (
    _stored_script_field, apply_namespaces, CALLER, diff_files, IMPORT_SUFFIX, installed_files, is_listed_import,
    is_relative_cwd, namespace_folder_problem, placement_error, resolve_script_path, reviewable, show_diff,
    validate_name,
)
from .shortcut_entry import ShortcutEntry
from .stores import load_group, load_store
from .approval import confirm_review


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
