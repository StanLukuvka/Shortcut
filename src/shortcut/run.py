"""Running a shortcut: approval, confirmation, arguments and the child process."""

from __future__ import annotations

import os
import shlex
import sys
from typing import Dict, List

from .helpers import (
    bind_args, exec_child, one_line, require_approved, resolve_script_path, safe, validate_name, write_styled,
)
from .confirm import Confirm
from .lookup import resolve_shortcut


def run_shortcut(words: List[str], skip_local: bool = False, assume_yes: bool = False) -> int:
    caller_cwd = os.getcwd()
    resolved, args = resolve_shortcut(words, caller_cwd, skip_local=skip_local, strict=True)
    require_approved(resolved.group)
    entry, name = resolved.entry, resolved.display
    validate_name(entry.name)  # list warns about a hand-written one; run refuses it
    typed = args  # as given, for the question and the command that answers it
    arg_env: Dict[str, str] = {}
    if entry.args is not None:
        args, arg_env = bind_args(name, entry.args, args)
    if not os.path.isdir(resolved.run_cwd):
        raise FileNotFoundError(f"run directory does not exist: {resolved.run_cwd}")
    with_args = f" with {one_line(shlex.join(typed))}" if typed else ""
    again = shlex.join(["shortcut", "r", "-y", *(["--global"] if skip_local else []), *name.split(), *typed])
    if resolved.hides_global and sys.stderr:  # before any question, and also with SHORTCUT_SHOW_DIR=0
        first = name.split()[0]
        print(f"note: '{first}' here is this project's; your global '{first}' runs with: "
              f"shortcut run --global {name}", file=sys.stderr)
    if entry.confirm and not Confirm(
        f"run '{name}'{with_args} in {os.path.realpath(resolved.run_cwd)}?",
        f"'{name}' asks for confirmation: run it in a terminal, or pass -y ({one_line(again)})",
        "" if entry.description in ("(no description)", entry.inline) else one_line(entry.description),
    ).granted(assume_yes):
        return 1
    if entry.confirm:
        require_approved(resolved.group)  # again: the files could change while the question waited
    if sys.stderr and os.environ.get("SHORTCUT_SHOW_DIR", "1") != "0":
        line = safe(f"→ {os.path.realpath(resolved.run_cwd)}")
        write_styled(sys.stderr, f"\033[2m{line}\033[0m\n" if sys.stderr.isatty() else f"{line}\n")
        sys.stderr.flush()

    # Build command argv
    if entry.inline is not None:
        cmd = ["bash", "-c", entry.inline, name, *args]
    else:
        assert entry.script is not None  # an entry has exactly one of inline and script
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
        if has_shebang and header.split(b"\n", 1)[0].endswith(b"\r"):
            raise ValueError(f"{script_path} has Windows line endings, so its #! line can't start it. "
                             f"Convert it: sed -i 's/\\r$//' {shlex.quote(script_path)}")
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
