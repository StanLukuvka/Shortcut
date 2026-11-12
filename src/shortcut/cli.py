"""The commands and main."""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import pathlib
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from typing import Any, Dict, List, NoReturn, Optional

from .helpers import (
    _stored_script_field, add_entry_flags, add_global_flag, apply_import, approval_path, approval_state, arg_lines,
    CALLER, die, diff_files, entry_fields, find_project_root, get_global_store_dir, IMPORT_SUFFIX, init_store,
    is_git_source, is_listed_import, is_relative_cwd, join_command, load_approval, load_registry, load_registry_file,
    LOCAL_FILE, local_project_root, POSITIONAL, PROJECT_FILE, refuse_symlinks, registry_exists, registry_lock,
    REGISTRY_VERSION, remove_namespace_files, require_approved, resolve_run_dir, resolve_script_path, reviewable,
    rm_local_message, rm_message, save_approval, save_registry_atomic, save_registry_file, scripts_dir, shared_files,
    show_diff, stored_copy, usage_line, validate_name, VERSION,
)
from .shortcut_entry import ShortcutEntry
from .safe_output import SafeOutput
from .parser import Parser
from .group import Group
from .confirm import Confirm
from .stores import entry_errors, load_entry, load_group, load_project_group, load_store, reload_group, visible_stores
from .lookup import find_entry, resolve_name, resolve_shortcut
from .run import run_shortcut
from .writes import _register, namespace_is_approved, note_if_unapproved, target_group, write_local
from .approval import approve_group, confirm_review
from .importing import init_namespaces, plan_import
from .completion import COMPLETION_SCRIPTS


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
        print(f"error: missing shortcut name\n{RUN_USAGE}", file=sys.stderr)
        return 2
    return run_shortcut(args, skip_local=skip_local, assume_yes=assume_yes)


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

    global_names = set(stores[-1].main.shortcuts) | set(stores[-1].namespaces)
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
                hides = "  [hides global]" if group.is_project and name in global_names else ""
                section(f"Namespace {name} ({group.file}){hides}", group, f"{name} ")
    if not sections:
        print("No shortcuts yet. Make one with: shortcut new hi -- 'echo hello \"$1\"'", file=sys.stderr)
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
    print(f"Scope:       {scope}" + (f"  (hides your global '{resolved.display.split()[0]}')" if resolved.hides_global else ""))
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
    global_names = set(stores[-1].main.shortcuts) | set(stores[-1].namespaces)
    return max(approve_group(g, parsed.yes, global_names) for g in groups)


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

        source_registry = load_registry_file(source_file)
        entries, scripts, skipped = plan_import(
            source_base=source_root,
            dest_store_dir=dest_store_dir,
            dest_registry=load_registry(dest_store_dir) if registry_exists(dest_store_dir) else {},
            source_registry=source_registry,
        )
        # A source with a .shortcuts brings its exported entries alone
        skipped += [f"{name} (a namespace file: a source with a {PROJECT_FILE} brings only its exported shortcuts)"
                    for name in sorted(os.listdir(source_root)) if name.endswith(IMPORT_SUFFIX) and name != IMPORT_SUFFIX]
        listed = source_registry.get("imports")
        if isinstance(listed, dict) and listed:
            skipped.append(f"imports: {', '.join(sorted(listed))} (namespaces a {PROJECT_FILE} imports aren't copied)")
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
  shortcut new <name> [FLAGS] [-- CMD...]   Create a shortcut: an inline command after --, a new
                                            script with a bash header at the path it prints, or a
                                            file with --script or --copy
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
  shortcut help [command], -h, --help       Show this help message, or a command's
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


HANDLERS = {
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


def dispatch(args: List[str]) -> int:
    if not args:
        print_usage()
        return 0
    first = args[0]
    if first == "help" and len(args) > 1 and args[1] in HANDLERS:
        return HANDLERS[args[1]](["--help"])
    if first in ("--help", "-h", "help"):
        print_usage()
        return 0
    if first == "--version":
        print(f"shortcut {VERSION}")
        return 0
    if first in HANDLERS:
        try:
            return HANDLERS[first](args[1:])
        except BrokenPipeError:
            raise
        except (KeyError, ValueError, OSError, RuntimeError) as e:
            err_msg = e.args[0] if (isinstance(e, KeyError) and e.args) else str(e)
            die(err_msg)
    if first.startswith("-"):
        print(f"error: unrecognized option '{first}'\nTry 'shortcut --help' for more information.", file=sys.stderr)
        return 2
    print(f"error: unknown command '{first}' (to run a shortcut: shortcut run {first})", file=sys.stderr)
    return 2


def main(argv: Optional[List[str]] = None) -> NoReturn:
    args = sys.argv[1:] if argv is None else argv
    sys.stdout, sys.stderr = (SafeOutput(stream) if stream else None for stream in (sys.stdout, sys.stderr))
    try:
        try:
            rc = dispatch(args)
        except SystemExit as e:  # die, and argparse
            rc = e.code
        if sys.stdout:
            sys.stdout.flush()  # here, not at exit, so a closed pipe is caught below
    except BrokenPipeError:
        # Whatever read the output stopped (shortcut list | head): stop quietly, as a program SIGPIPE ends.
        # stdout points at devnull so the flush at exit doesn't fail again.
        os.dup2(os.open(os.devnull, os.O_WRONLY), 1)
        rc = 128 + 13
    sys.exit(rc)
