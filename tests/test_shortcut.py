#!/usr/bin/env python3

import importlib.machinery
import importlib.util
import fcntl
import json
import os
import pathlib
import select
import shlex
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unittest
from typing import Any, Optional

SHORTCUT = pathlib.Path(__file__).resolve().parent.parent / "shortcut.py"

loader = importlib.machinery.SourceFileLoader("shortcut_mod", str(SHORTCUT))
spec = importlib.util.spec_from_loader("shortcut_mod", loader)
if spec is None or spec.loader is None:
    raise ImportError(f"Cannot load {SHORTCUT}")
shortcut_mod = importlib.util.module_from_spec(spec)
sys.modules["shortcut_mod"] = shortcut_mod
loader.exec_module(shortcut_mod)


class TestShortcut(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.tmp_path = pathlib.Path(self.temp_dir.name).resolve()
        self.home_dir = self.tmp_path / "home"
        self.shortcuts_home = self.home_dir / ".shortcuts"
        self.xdg_data_home = self.home_dir / ".local" / "share"

        self.home_dir.mkdir(parents=True, exist_ok=True)
        self.shortcuts_home.mkdir(parents=True, exist_ok=True)
        self.xdg_data_home.mkdir(parents=True, exist_ok=True)

        self.env = os.environ.copy()
        self.env["HOME"] = str(self.home_dir)
        self.env["SHORTCUTS_HOME"] = str(self.shortcuts_home)
        self.env["XDG_DATA_HOME"] = str(self.xdg_data_home)

        self.orig_env = {}
        for key in ("HOME", "SHORTCUTS_HOME", "XDG_DATA_HOME"):
            self.orig_env[key] = os.environ.get(key)
            os.environ[key] = self.env[key]

        # Never touch the real ~/.shortcuts
        assert str(self.shortcuts_home).startswith(str(self.tmp_path)), "SHORTCUTS_HOME not in temp dir"

    def tearDown(self):
        for key, val in self.orig_env.items():
            if val is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = val
        self.temp_dir.cleanup()

    def run_cli(
        self,
        args: list[str],
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        input: Optional[str] = None,
    ) -> subprocess.CompletedProcess[str]:
        run_env = self.env.copy()
        if env:
            run_env.update(env)
        if input is not None:
            return subprocess.run(
                [sys.executable, str(SHORTCUT), *args],
                cwd=cwd or str(self.home_dir),
                env=run_env,
                input=input,
                text=True,
                capture_output=True,
            )
        else:
            return subprocess.run(
                [sys.executable, str(SHORTCUT), *args],
                cwd=cwd or str(self.home_dir),
                env=run_env,
                stdin=subprocess.DEVNULL,
                text=True,
                capture_output=True,
            )

    def run_in_terminal(
        self,
        args: list[str],
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        keys: Optional[list[tuple[bytes, Any]]] = None,
        timeout: float = 15,
    ) -> tuple[int, str]:
        """Run the CLI on a fresh 30x100 pseudo-terminal, as if typed into a real one.
        keys: (wait until this appears in the output, then type these bytes, or send this signal) pairs.
        Afterwards self.terminal_lflag holds the terminal's local modes, to check they were restored."""
        master, slave = os.openpty()
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
        run_env = self.env.copy()
        run_env.update(env or {})
        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), *args],
            cwd=cwd or str(self.home_dir),
            env=run_env,
            stdin=slave,
            stdout=slave,
            stderr=slave,
            start_new_session=True,
            preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0),
        )
        os.close(slave)
        pending = list(keys or [])
        out = b""
        deadline = time.time() + timeout
        try:
            while time.time() < deadline:
                if pending and pending[0][0] in out:
                    action = pending.pop(0)[1]
                    if isinstance(action, bytes):
                        os.write(master, action)
                    else:
                        proc.send_signal(action)
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError:
                        break
                    if not chunk:
                        break
                    out += chunk
                elif proc.poll() is not None:
                    break
            self.terminal_lflag = termios.tcgetattr(master)[3]
        finally:
            os.close(master)
        return proc.wait(timeout=timeout), out.decode("utf-8", errors="replace")

    def allow(self, store_dir: pathlib.Path) -> None:
        """Approve everything in a project, as `shortcut update -y` there does."""
        res = self.run_cli(["update", "-y"], cwd=str(store_dir.parent))
        self.assertEqual(res.returncode, 0, res.stderr)

    def write_store(self, store_dir: pathlib.Path, shortcuts: dict, **extra: Any) -> None:
        store_dir.mkdir(parents=True, exist_ok=True)
        (store_dir / "shortcuts.json").write_text(
            json.dumps({"version": 2, "shortcuts": shortcuts, **extra}), encoding="utf-8"
        )

    def install_bundled(self) -> None:
        """Import templates/ with a `shortcut` on PATH for them to call, as after a real install."""
        self.tool_bin = self.tmp_path / "bin"
        self.tool_bin.mkdir()
        wrapper = self.tool_bin / "shortcut"
        wrapper.write_text(f'#!/bin/sh\nexec "{sys.executable}" "{SHORTCUT}" "$@"\n', encoding="utf-8")
        wrapper.chmod(0o755)
        self.env["PATH"] = f"{self.tool_bin}{os.pathsep}{self.env['PATH']}"
        templates = SHORTCUT.parent / "templates"
        res = self.run_cli(["init", "--global", str(templates)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("Skipped", res.stdout)

    def test_atomic_registry_save_and_corrupt_protection(self):
        data = {
            "version": 1,
            "shortcuts": {
                "test": {"description": "test", "inline": "echo 1"}
            }
        }
        shortcut_mod.save_registry_atomic(str(self.shortcuts_home), data)
        loaded = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(loaded, data)

        # Corrupt JSON file protection
        reg_file = self.shortcuts_home / "shortcuts.json"
        reg_file.write_text('{"version": 1, corrupt json!', encoding="utf-8")

        with self.assertRaises(ValueError) as ctx:
            shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("corrupt registry", str(ctx.exception))

        with self.assertRaises(ValueError) as ctx2:
            shortcut_mod.save_registry_atomic(str(self.shortcuts_home), data)
        self.assertIn("cannot overwrite corrupt registry", str(ctx2.exception))

        # Verify the corrupt file was not replaced or overwritten
        self.assertEqual(reg_file.read_text(encoding="utf-8"), '{"version": 1, corrupt json!')

    def test_store_discovery_skips_global_root(self):
        # Write shortcuts.json in global store
        shortcut_mod.save_registry_atomic(str(self.shortcuts_home), {"version": 1, "shortcuts": {}})

        # Walking up from home or subdirectory must skip ~/.shortcuts
        self.assertIsNone(shortcut_mod.find_local_store_dir(str(self.home_dir)))
        deep_dir = self.home_dir / "a" / "b" / "c"
        deep_dir.mkdir(parents=True)
        self.assertIsNone(shortcut_mod.find_local_store_dir(str(deep_dir)))

        # Walking up from inside ~/.shortcuts itself must return None
        self.assertIsNone(shortcut_mod.find_local_store_dir(str(self.shortcuts_home)))

        # A real local store in a project must be found
        proj_dir = self.home_dir / "my_project"
        proj_shortcuts = proj_dir / ".shortcuts"
        proj_shortcuts.mkdir(parents=True)
        shortcut_mod.save_registry_atomic(str(proj_shortcuts), {"version": 1, "shortcuts": {}})

        sub_dir = proj_dir / "src" / "pkg"
        sub_dir.mkdir(parents=True)
        found = shortcut_mod.find_local_store_dir(str(sub_dir))
        self.assertEqual(found, str(proj_shortcuts.resolve()))

    def test_scope_resolution_order(self):
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {"version": 2, "shortcuts": {"build": {"description": "Global build", "inline": "echo global_build"}}},
        )
        proj_dir = self.home_dir / "my_project"
        proj_shortcuts = proj_dir / ".shortcuts"
        proj_shortcuts.mkdir(parents=True)
        shortcut_mod.save_registry_atomic(
            str(proj_shortcuts),
            {"version": 2, "shortcuts": {"build": {"description": "Local project build", "inline": "echo local_build"}}},
        )

        # Inside a project: project precedence
        res_local, _ = shortcut_mod.resolve_shortcut(["build"], caller_cwd=str(proj_dir))
        self.assertEqual(res_local.group.store_dir, str(proj_shortcuts.resolve()))
        self.assertEqual(res_local.entry.description, "Local project build")
        self.assertTrue(res_local.hides_global)

        # An unapproved project shortcut is refused, not swapped for the global one
        res_blocked = self.run_cli(["run", "build"], cwd=str(proj_dir))
        self.assertEqual(res_blocked.returncode, 1)
        self.assertIn("isn't approved yet", res_blocked.stderr)
        self.assertEqual(self.run_cli(["run", "--global", "build"], cwd=str(proj_dir)).stdout, "global_build\n")

        # Outside the project: the global one
        res_global, _ = shortcut_mod.resolve_shortcut(["build"], caller_cwd=str(self.home_dir))
        self.assertEqual(res_global.group.store_dir, str(self.shortcuts_home.resolve()))
        self.assertEqual(res_global.entry.description, "Global build")

        res_cli = self.run_cli(["run", "check"], cwd=str(self.home_dir))
        self.assertNotEqual(res_cli.returncode, 0)
        self.assertIn("shortcut 'check' not found", res_cli.stderr)

    def test_run_inline_with_passthrough_args(self):
        res_new = self.run_cli(["new", "echo", "--", "echo name=$0 first=$1 all=$*"])
        self.assertEqual(res_new.returncode, 0)

        res = self.run_cli(["run", "echo", "-v", "--option", "arg1", "arg2"])
        self.assertEqual(res.returncode, 0)
        self.assertEqual(res.stdout.strip(), "name=echo first=-v all=-v --option arg1 arg2")

        res_run = self.run_cli(["run", "echo", "-v", "--option", "arg1", "arg2"])
        self.assertEqual(res_run.returncode, 0)
        self.assertEqual(res_run.stdout.strip(), "name=echo first=-v all=-v --option arg1 arg2")

    def test_run_script_tee_logging_and_exit_code(self):
        script_file = self.tmp_path / "script_fail.sh"
        script_file.write_text("#!/usr/bin/env bash\necho stdout line\necho stderr line >&2\nexit 42\n", encoding="utf-8")
        script_file.chmod(0o755)

        res_add = self.run_cli(["add", "fail42", str(script_file), "--desc", "Fails with 42"])
        self.assertEqual(res_add.returncode, 0)

        res = self.run_cli(["run", "fail42"])
        self.assertEqual(res.returncode, 42)
        self.assertIn("stdout line", res.stdout)
        self.assertIn("stderr line", res.stdout)

        log_path = self.shortcuts_home / "logs" / "fail42.log"
        self.assertTrue(log_path.is_file())
        log_content = log_path.read_text(encoding="utf-8")
        self.assertTrue(log_content.startswith("=== SHORTCUT RUN: fail42 |"))
        self.assertIn("stdout line", log_content)
        self.assertIn("stderr line", log_content)
        self.assertTrue(log_content.rstrip().endswith("=== exit: 42 ==="))

        res_fix = self.run_cli(["fix", "fail42"])
        self.assertEqual(res_fix.returncode, 0)
        self.assertIn("Last exit code: 42", res_fix.stdout)

    def test_add_and_link_paths(self):
        # 1. Add copy from file
        src_file = self.tmp_path / "source.sh"
        src_file.write_text("#!/usr/bin/env bash\necho source\n", encoding="utf-8")
        src_file.chmod(0o644)

        res_add = self.run_cli(["add", "copied", str(src_file), "--desc", "Copied script"])
        self.assertEqual(res_add.returncode, 0)

        copied_path = self.shortcuts_home / "scripts" / "copied.sh"
        self.assertTrue(copied_path.is_file())
        self.assertTrue(os.access(copied_path, os.X_OK))
        self.assertTrue(src_file.is_file())  # original untouched

        # 2. Add from stdin
        res_pipe = self.run_cli(
            ["add", "piped", "-", "--desc", "Piped script"],
            input="#!/usr/bin/env bash\necho piped\n",
        )
        self.assertEqual(res_pipe.returncode, 0)
        piped_path = self.shortcuts_home / "scripts" / "piped.sh"
        self.assertTrue(piped_path.is_file())
        self.assertEqual(piped_path.read_text(encoding="utf-8"), "#!/usr/bin/env bash\necho piped\n")

        # 3. Link absolute external file
        ext_file = self.tmp_path / "external.sh"
        ext_file.write_text("#!/usr/bin/env bash\necho external\n", encoding="utf-8")
        ext_file.chmod(0o755)

        res_link = self.run_cli(["link", "linked", str(ext_file), "--desc", "Linked script"])
        self.assertEqual(res_link.returncode, 0)
        self.assertFalse((self.shortcuts_home / "scripts" / "linked.sh").exists())

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["linked"]["script"], str(ext_file.resolve()))

        # 4. Path containment check
        with self.assertRaises(ValueError) as ctx:
            shortcut_mod.resolve_script_path(str(self.shortcuts_home), "../../../etc/passwd")
        self.assertIn("path traversal detected", str(ctx.exception))

    def test_rm_interactive_and_force(self):
        script_file = self.tmp_path / "stored.sh"
        script_file.write_text("#!/usr/bin/env bash\necho stored\n", encoding="utf-8")
        script_file.chmod(0o755)
        self.run_cli(["add", "stored", str(script_file), "--desc", "Stored shortcut"])
        self.run_cli(["run", "stored"])

        log_path = self.shortcuts_home / "logs" / "stored.log"
        stored_path = self.shortcuts_home / "scripts" / "stored.sh"
        self.assertTrue(log_path.is_file())
        self.assertTrue(stored_path.is_file())

        ext_file = self.tmp_path / "external_keep.sh"
        ext_file.write_text("#!/usr/bin/env bash\necho ext\n", encoding="utf-8")
        ext_file.chmod(0o755)
        self.run_cli(["link", "linked", str(ext_file), "--desc", "Linked shortcut"])

        # 1. Non-tty without -y should fail
        res_no_y = self.run_cli(["rm", "stored"])
        self.assertNotEqual(res_no_y.returncode, 0)
        self.assertIn("in a terminal, or pass -y", res_no_y.stderr)
        self.assertTrue(stored_path.is_file())
        self.assertTrue(log_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("stored", reg["shortcuts"])

        # 2. With -y, stored copy, log, and registry entry are deleted
        res_y = self.run_cli(["rm", "stored", "-y"])
        self.assertEqual(res_y.returncode, 0)
        self.assertFalse(stored_path.is_file())
        self.assertFalse(log_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("stored", reg["shortcuts"])

        # 3. Linked file is preserved when shortcut is removed with -y
        res_link_rm = self.run_cli(["rm", "linked", "-y"])
        self.assertEqual(res_link_rm.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("linked", reg["shortcuts"])
        self.assertTrue(ext_file.is_file(), "Linked external script must never be deleted!")

    def test_edit_inline_roundtrip(self):
        res = self.run_cli(["new", "inline1", "--", "echo original"])
        self.assertEqual(res.returncode, 0)

        # 1. Edit modifying content (and asserting temp file suffix)
        editor_mod = (
            f'{sys.executable} -c "import sys, os; '
            f'path = sys.argv[1]; '
            f'assert os.path.isfile(path); '
            f'assert path.endswith(\'-inline1.sh\'); '
            f'open(path, \'w\').write(\'echo modified\\n\')"'
        )
        res_edit = self.run_cli(["edit", "inline1"], env={"SHORTCUT_EDITOR_CMD": editor_mod})
        self.assertEqual(res_edit.returncode, 0)
        self.assertIn("Updated inline shortcut 'inline1'.", res_edit.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["inline1"]["inline"], "echo modified")

        # 2. Edit without modifying content
        res_noop = self.run_cli(["edit", "inline1"], env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res_noop.returncode, 0)
        self.assertIn("No changes made to 'inline1'.", res_noop.stdout)

    def test_creation_scope_rules(self):
        proj_dir = self.tmp_path / "fresh_proj"
        (proj_dir / "sub").mkdir(parents=True)

        # 1. The global store by default, wherever you are
        self.assertEqual(self.run_cli(["new", "anywhere", "--", "echo 1"], cwd=str(proj_dir)).returncode, 0)
        self.assertIn("anywhere", self.load_global())
        self.assertFalse((proj_dir / ".shortcuts").exists())

        # 2. --local creates ./.shortcuts/ with a stderr notice, and it needs approving
        res = self.run_cli(["new", "local_cmd", "--local", "--", "echo local"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("created project store", res.stderr)
        self.assertIn("needs approving before it runs: shortcut update", res.stderr)
        self.assertTrue((proj_dir / ".shortcuts" / "shortcuts.json").is_file())
        self.assertNotEqual(self.run_cli(["run", "local_cmd"], cwd=str(proj_dir)).returncode, 0)
        self.allow(proj_dir / ".shortcuts")
        self.assertEqual(self.run_cli(["run", "local_cmd"], cwd=str(proj_dir)).stdout, "local\n")

        # 3. --cwd is stored against the defining file: absolute globally, relative inside the project
        res = self.run_cli(["new", "fixed", "--cwd", str(self.tmp_path), "--", "pwd"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["fixed"]["cwd"], str(self.tmp_path))
        res = self.run_cli(["new", "in_sub", "--local", "--cwd", "sub", "--", "pwd"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0, res.stderr)
        project = json.loads((proj_dir / ".shortcuts" / "shortcuts.json").read_text())["shortcuts"]
        self.assertEqual(project["in_sub"]["cwd"], "../sub")
        res = self.run_cli(["new", "explicit", "--cwd", "caller", "--", "pwd"])
        self.assertEqual(self.load_global()["explicit"]["cwd"], "caller")

        res = self.run_cli(["new", "nowhere", "--cwd", "/nonexistent/path/xyz", "--", "echo 1"])
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("--cwd directory does not exist", res.stderr)

        # 4. Duplicate name error
        res = self.run_cli(["new", "local_cmd", "--local", "--", "echo duplicate"], cwd=str(proj_dir))
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("already exists in", res.stderr)

    def test_new_oneliner_and_template(self):
        # Inline oneliner: description defaults to command
        res1 = self.run_cli(["new", "hello", "--", "echo", "hello", "world"])
        self.assertEqual(res1.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["hello"]["inline"], "echo hello world")
        self.assertEqual(reg["shortcuts"]["hello"]["description"], "echo hello world")

        # Template script: description defaults to "(no description)"
        res2 = self.run_cli(["new", "template"], env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res2.returncode, 0)
        script_file = self.shortcuts_home / "scripts" / "template.sh"
        self.assertTrue(script_file.is_file())
        self.assertTrue(os.access(script_file, os.X_OK))
        content = script_file.read_text(encoding="utf-8")
        self.assertEqual(content, "#!/usr/bin/env bash\nset -euo pipefail\n\n")

        reg2 = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg2["shortcuts"]["template"]["description"], "(no description)")

    def test_run_ctrl_c_writes_footer(self):
        sleeper_script = self.tmp_path / "sleeper.sh"
        sleeper_script.write_text("#!/usr/bin/env bash\necho started\nsleep 10\n", encoding="utf-8")
        sleeper_script.chmod(0o755)

        res_add = self.run_cli(["add", "sleeper", str(sleeper_script), "--desc", "Sleeping script"])
        self.assertEqual(res_add.returncode, 0)

        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "sleeper"],
            cwd=str(self.home_dir),
            env=self.env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
            start_new_session=True,
        )

        assert proc.stdout is not None
        line = b""
        start_time = time.time()
        while b"started" not in line and (time.time() - start_time < 5):
            chunk = proc.stdout.readline()
            if not chunk:
                break
            line += chunk

        os.killpg(proc.pid, signal.SIGINT)
        proc.communicate(timeout=15)

        self.assertEqual(proc.returncode, 130)

        log_path = self.shortcuts_home / "logs" / "sleeper.log"
        self.assertTrue(log_path.is_file())
        log_text = log_path.read_text(encoding="utf-8")
        self.assertTrue(log_text.rstrip().endswith("=== exit: 130 ==="), f"Footer missing in:\n{log_text}")

    def test_list_and_names(self):
        proj_dir = self.home_dir / "list_proj"
        proj_dir.mkdir()
        self.run_cli(["new", "local_cmd", "--local", "--cwd", ".", "--", "echo local"], cwd=str(proj_dir))
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {
                "version": 2,
                "shortcuts": {
                    "glob": {"description": "Global desc", "inline": "echo 1"},
                    "penpot": {"description": "Fixed desc", "inline": "echo 2", "cwd": str(self.tmp_path)},
                },
            },
        )

        # Nothing of an unapproved project shows, only how to review it
        res_list = self.run_cli(["list"], cwd=str(proj_dir))
        self.assertIn("aren't approved yet. Review them with: shortcut update", res_list.stdout)
        self.assertNotIn("local_cmd", res_list.stdout)
        self.assertEqual(self.run_cli(["list", "--names"], cwd=str(proj_dir)).stdout.split(), ["glob", "penpot"])

        self.allow(proj_dir / ".shortcuts")
        res_list = self.run_cli(["list"], cwd=str(proj_dir))
        self.assertEqual(res_list.returncode, 0)
        self.assertIn("Project shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"local_cmd +\.\. +echo local")
        self.assertIn("Global shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"glob +caller +Global desc")
        self.assertRegex(res_list.stdout, rf"penpot +{self.tmp_path} +Fixed desc")
        self.assertEqual(
            self.run_cli(["list", "--names"], cwd=str(proj_dir)).stdout.split(), ["local_cmd", "glob", "penpot"]
        )

    def test_show_and_location(self):
        self.run_cli(["new", "inline", "--desc", "Inline item", "--", "echo hello"])
        script_file = self.tmp_path / "show_script.sh"
        script_file.write_text("#!/usr/bin/env bash\necho test\n", encoding="utf-8")
        script_file.chmod(0o755)
        self.run_cli(["add", "stored", str(script_file), "--desc", "Stored item"])

        # show
        res_show_inline = self.run_cli(["show", "inline"])
        self.assertEqual(res_show_inline.returncode, 0)
        self.assertIn("Name:        inline", res_show_inline.stdout)
        self.assertIn("echo hello", res_show_inline.stdout)

        res_show_script = self.run_cli(["show", "stored"])
        self.assertEqual(res_show_script.returncode, 0)
        self.assertIn("Name:        stored", res_show_script.stdout)
        self.assertIn("echo test", res_show_script.stdout)

        # location
        res_loc_script = self.run_cli(["location", "stored"])
        self.assertEqual(res_loc_script.returncode, 0)
        self.assertTrue(res_loc_script.stdout.strip().endswith("scripts/stored.sh"))

    def test_init_empty_local_store_creation(self):
        proj_dir = self.tmp_path / "empty_init_proj"
        proj_dir.mkdir()

        res = self.run_cli(["init"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        self.assertIn("Initialized empty project store", res.stdout)

        store_json = proj_dir / ".shortcuts" / "shortcuts.json"
        self.assertTrue(store_json.is_file())
        data = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertEqual(data, {"version": 2, "shortcuts": {}})
        # logs and the lock stay out of git
        self.assertEqual((proj_dir / ".shortcuts" / ".gitignore").read_text(), "logs/\n.lock\n")

    def test_init_source_without_global_is_refused(self):
        src_repo = self.tmp_path / "source_repo"
        shortcut_mod.save_registry_atomic(
            str(src_repo), {"version": 1, "shortcuts": {"x": {"description": "x", "inline": "echo x"}}}
        )
        proj_dir = self.tmp_path / "proj"
        proj_dir.mkdir()
        res = self.run_cli(["init", str(src_repo)], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("used in place, never copied", res.stderr)
        self.assertFalse((proj_dir / ".shortcuts").exists())

    def test_init_global_merge_skips(self):
        self.run_cli(["new", "collide", "--", "echo existing"])
        ext_script = self.tmp_path / "external_source.sh"
        ext_script.write_text("#!/usr/bin/env bash\necho ext\n", encoding="utf-8")
        src_repo = self.tmp_path / "source_repo"
        (src_repo / "scripts").mkdir(parents=True)
        (src_repo / "scripts" / "valid.sh").write_text("#!/usr/bin/env bash\necho valid\n", encoding="utf-8")
        shortcut_mod.save_registry_atomic(str(src_repo), {
            "version": 2,
            "shortcuts": {
                "collide": {"description": "collision", "inline": "echo collide"},
                "linked": {"description": "linked", "script": str(ext_script)},
                "valid": {"description": "valid", "script": "scripts/valid.sh"},
            },
        })

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("collide (already exists)", res.stdout)
        self.assertIn("linked (linked external script)", res.stdout)
        reg = self.load_global()
        self.assertEqual(reg["valid"]["script"], "scripts/valid.sh")
        self.assertTrue(os.access(self.shortcuts_home / "scripts" / "valid.sh", os.X_OK))
        self.assertNotIn("linked", reg)

    def test_init_source_global_cwd_retention(self):
        src_repo = self.tmp_path / "global_source_repo"
        src_repo.mkdir()
        fixed = self.tmp_path / "fixed_dir"
        fixed.mkdir()
        shortcut_mod.save_registry_atomic(str(src_repo), {
            "version": 2,
            "shortcuts": {
                "fixed_ok": {"description": "ok", "inline": "echo ok", "cwd": str(fixed)},
                "fixed_bad": {"description": "missing", "inline": "echo bad", "cwd": "/nonexistent/fixed/xyz"},
                "relative": {"description": "relative", "inline": "echo rel", "cwd": "../sibling"},
                "caller": {"description": "caller", "inline": "echo c", "cwd": "caller"},
                "traversal": {"description": "traversal", "script": "../../outside.sh"},
            },
        })

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0)
        self.assertIn("fixed_bad (cwd is relative or not found here", res.stdout)
        self.assertIn("relative (cwd is relative or not found here", res.stdout)
        self.assertIn("traversal (script path escapes the source", res.stdout)
        reg = self.load_global()
        self.assertEqual(reg["fixed_ok"]["cwd"], str(fixed))
        self.assertIn("caller", reg)
        for skipped in ("fixed_bad", "relative", "traversal"):
            self.assertNotIn(skipped, reg)

    def test_migrate_legacy_flat_store(self):
        # Create flat legacy scripts directly in ~/.shortcuts
        penpot_script = self.shortcuts_home / "penpot.sh"
        penpot_script.write_text(
            "#!/usr/bin/env bash\n"
            "# Start Penpot (http://localhost:9001)\n"
            "echo penpot_running\n",
            encoding="utf-8",
        )
        penpot_script.chmod(0o755)

        no_comment_script = self.shortcuts_home / "raw.sh"
        no_comment_script.write_text(
            "#!/usr/bin/env bash\n"
            "echo raw_running\n",
            encoding="utf-8",
        )
        no_comment_script.chmod(0o755)

        res_mig = self.run_cli(["migrate"])
        self.assertEqual(res_mig.returncode, 0)
        self.assertIn("penpot.sh -> penpot (Start Penpot (http://localhost:9001))", res_mig.stdout)
        self.assertIn("raw.sh -> raw ((migrated))", res_mig.stdout)

        # Original flat files must be moved
        self.assertFalse(penpot_script.exists())
        self.assertFalse(no_comment_script.exists())

        # New files in scripts/
        migrated_penpot = self.shortcuts_home / "scripts" / "penpot.sh"
        migrated_raw = self.shortcuts_home / "scripts" / "raw.sh"
        self.assertTrue(migrated_penpot.is_file())
        self.assertTrue(migrated_raw.is_file())
        self.assertTrue(os.access(migrated_penpot, os.X_OK))

        # Check registry
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("penpot", reg["shortcuts"])
        self.assertEqual(reg["shortcuts"]["penpot"]["description"], "Start Penpot (http://localhost:9001)")
        self.assertEqual(reg["shortcuts"]["penpot"]["script"], "scripts/penpot.sh")

        self.assertIn("raw", reg["shortcuts"])
        self.assertEqual(reg["shortcuts"]["raw"]["description"], "(migrated)")
        self.assertEqual(reg["shortcuts"]["raw"]["script"], "scripts/raw.sh")

        # Run migrated shortcut
        res_run = self.run_cli(["run", "penpot"])
        self.assertEqual(res_run.returncode, 0)
        self.assertIn("penpot_running", res_run.stdout)

    def test_migrate_idempotent(self):
        script = self.shortcuts_home / "job.sh"
        script.write_text("#!/usr/bin/env bash\n# Run job\necho job\n", encoding="utf-8")
        script.chmod(0o755)

        res1 = self.run_cli(["migrate"])
        self.assertEqual(res1.returncode, 0)
        self.assertIn("job.sh -> job", res1.stdout)

        reg1 = shortcut_mod.load_registry(str(self.shortcuts_home))

        res2 = self.run_cli(["migrate"])
        self.assertEqual(res2.returncode, 0)
        self.assertIn("Nothing to migrate.", res2.stdout)

        reg2 = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg1, reg2)

    def test_init_git_source_clone(self):
        # Local git repo test (no network)
        git_dir = self.tmp_path / "git_source_repo"
        git_dir.mkdir()
        subprocess.run(["git", "init", str(git_dir)], check=True, capture_output=True)
        subprocess.run(["git", "-C", str(git_dir), "config", "user.name", "TestUser"], check=True)
        subprocess.run(["git", "-C", str(git_dir), "config", "user.email", "test@example.com"], check=True)

        scripts_dir = git_dir / "scripts"
        scripts_dir.mkdir()
        (scripts_dir / "git_cmd.sh").write_text("#!/usr/bin/env bash\necho from_git\n", encoding="utf-8")
        (scripts_dir / "git_cmd.sh").chmod(0o755)

        (git_dir / "shortcuts.json").write_text(
            '{"version": 1, "shortcuts": {"git_cmd": {"description": "From git", "script": "scripts/git_cmd.sh"}}}',
            encoding="utf-8",
        )

        subprocess.run(["git", "-C", str(git_dir), "add", "."], check=True)
        subprocess.run(["git", "-C", str(git_dir), "commit", "-m", "initial commit"], check=True, capture_output=True)

        res = self.run_cli(["init", "--global", f"file://{git_dir.resolve()}"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("git_cmd", res.stdout)
        self.assertEqual(self.run_cli(["run", "git_cmd"]).stdout, "from_git\n")

    def test_bundled_templates(self):
        templates_dir = pathlib.Path(__file__).resolve().parent.parent / "templates"
        self.assertTrue((templates_dir / "shortcuts.json").is_file())

        search_script = templates_dir / "scripts" / "search.sh"
        self.assertTrue(search_script.is_file())

        # search must print usage and exit 1 when given no pattern
        res_no_arg = subprocess.run([str(search_script)], capture_output=True, text=True)
        self.assertEqual(res_no_arg.returncode, 1)
        self.assertIn("usage:", res_no_arg.stderr)

        # search finds pattern when given
        test_file = self.tmp_path / "target_search_me.txt"
        test_file.write_text("hello", encoding="utf-8")
        res_arg = subprocess.run([str(search_script), "search_me", str(self.tmp_path)], capture_output=True, text=True)
        self.assertEqual(res_arg.returncode, 0)
        self.assertIn("target_search_me.txt", res_arg.stdout)

        # Import templates via shortcut init --global
        res_init = self.run_cli(["init", "--global", str(templates_dir)])
        self.assertEqual(res_init.returncode, 0)
        self.assertIn("search", res_init.stdout)
        self.assertIn("serve", res_init.stdout)
        self.assertIn("ports", res_init.stdout)
        self.assertNotIn("Skipped", res_init.stdout)
        for name in ("nano", "log", "desktop"):
            self.assertIn(name, self.load_global())

    def test_fix_output_formatting(self):
        res_new = self.run_cli(["new", "fix_sample", "--desc", "Fix sample description", "--", "echo", "fix output running"])
        self.assertEqual(res_new.returncode, 0)

        # Before any run: metadata and script present, "no log yet"
        res_fix_pre = self.run_cli(["fix", "fix_sample"])
        self.assertEqual(res_fix_pre.returncode, 0)
        self.assertIn("Fix sample description", res_fix_pre.stdout)
        self.assertIn("fix output running", res_fix_pre.stdout)
        self.assertIn("no log yet", res_fix_pre.stdout)
        self.assertTrue(res_fix_pre.stdout.strip().endswith("Last exit code: none"))

        # Run the shortcut
        res_run = self.run_cli(["run", "fix_sample"])
        self.assertEqual(res_run.returncode, 0)

        # After run: log contents present and ends with last exit code 0
        res_fix_post = self.run_cli(["fix", "fix_sample"])
        self.assertEqual(res_fix_post.returncode, 0)
        self.assertIn("Fix sample description", res_fix_post.stdout)
        self.assertIn("fix output running", res_fix_post.stdout)
        self.assertTrue(res_fix_post.stdout.strip().endswith("Last exit code: 0"))

    def test_completion_bash(self):
        res_comp = self.run_cli(["completion", "bash"])
        self.assertEqual(res_comp.returncode, 0)
        self.assertIn("_shortcut", res_comp.stdout)
        self.assertIn("complete -F _shortcut shortcut", res_comp.stdout)
        self.assertIn("list --names", res_comp.stdout)
        for cmd in ("run r", "show", "edit", "location", "rm", "fix", "update"):
            self.assertIn(cmd, res_comp.stdout)
        for cmd in (" log ", " desktop ", " prefix ", " allow "):
            self.assertNotIn(cmd, res_comp.stdout)

    def test_rm_cleans_desktop_file(self):
        self.install_bundled()
        res_new = self.run_cli(["new", "rm_desk_test", "--desc", "Clean desktop test", "--", "echo", "rm_desk"])
        self.assertEqual(res_new.returncode, 0)
        res_desk = self.run_cli(["r", "desktop", "rm_desk_test"])
        self.assertEqual(res_desk.returncode, 0, res_desk.stderr)

        desktop_file = self.xdg_data_home / "applications" / "shortcut-rm_desk_test.desktop"
        self.assertTrue(desktop_file.is_file())

        res_rm = self.run_cli(["rm", "rm_desk_test", "-y"])
        self.assertEqual(res_rm.returncode, 0)
        self.assertFalse(desktop_file.exists())

    def test_install_script_dry_run(self):
        install_script = pathlib.Path(__file__).resolve().parent.parent / "install.sh"
        self.assertTrue(install_script.is_file())

        test_home = self.tmp_path / "inst_home"
        bin_dir = test_home / "bin"
        xdg_data = test_home / "share"
        shortcuts_home = test_home / ".shortcuts"

        test_env = self.env.copy()
        test_env["HOME"] = str(test_home)
        test_env["BIN_DIR"] = str(bin_dir)
        test_env["XDG_DATA_HOME"] = str(xdg_data)
        test_env["SHORTCUTS_HOME"] = str(shortcuts_home)

        res = subprocess.run(
            [str(install_script)],
            cwd=str(self.tmp_path),
            env=test_env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(res.returncode, 0, f"install.sh failed: {res.stderr}")
        self.assertTrue((bin_dir / "shortcut").is_file())
        self.assertTrue(os.access(bin_dir / "shortcut", os.X_OK))

        compl_file = xdg_data / "bash-completion" / "completions" / "shortcut"
        self.assertTrue(compl_file.is_file())
        self.assertIn("complete -F _shortcut shortcut", compl_file.read_text(encoding="utf-8"))

        # Assert nothing lands outside the temp dir
        self.assertTrue(str(bin_dir).startswith(str(self.tmp_path)))
        self.assertTrue(str(compl_file).startswith(str(self.tmp_path)))
        self.assertTrue(str(shortcuts_home).startswith(str(self.tmp_path)))

    def test_init_skips_invalid_entries(self):
        src_repo = self.tmp_path / "invalid_entries_repo"
        src_repo.mkdir()
        src_scripts = src_repo / "scripts"
        src_scripts.mkdir()
        valid_script = src_scripts / "valid.sh"
        valid_script.write_text("#!/bin/sh\necho ok\n", encoding="utf-8")
        valid_script.chmod(0o755)

        source_data = {
            "version": 1,
            "shortcuts": {
                "not_a_dict": "echo hello",
                "neither_cmd": {"description": "no script or inline"},
                "both_cmd": {"description": "both", "inline": "echo 1", "script": "scripts/valid.sh"},
                "inline_not_str": {"inline": 123},
                "script_not_str": {"script": ["scripts/valid.sh"]},
                "valid_entry": {"description": "ok", "inline": "echo ok"},
            },
        }
        (src_repo / "shortcuts.json").write_text(json.dumps(source_data), encoding="utf-8")

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0)
        self.assertIn("not_a_dict (invalid entry", res.stdout)
        self.assertIn("neither_cmd (invalid entry", res.stdout)
        self.assertIn("both_cmd (invalid entry", res.stdout)
        self.assertIn("inline_not_str (invalid entry", res.stdout)
        self.assertIn("script_not_str (invalid entry", res.stdout)
        self.assertIn("valid_entry", res.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("valid_entry", reg["shortcuts"])
        self.assertNotIn("not_a_dict", reg["shortcuts"])
        self.assertNotIn("neither_cmd", reg["shortcuts"])
        self.assertNotIn("both_cmd", reg["shortcuts"])
        self.assertNotIn("inline_not_str", reg["shortcuts"])
        self.assertNotIn("script_not_str", reg["shortcuts"])

    def test_init_skips_symlinked_source_script(self):
        src_repo = self.tmp_path / "symlink_source_repo"
        src_repo.mkdir()
        src_scripts = src_repo / "scripts"
        src_scripts.mkdir()

        real_file = src_scripts / "real.sh"
        real_file.write_text("#!/bin/sh\necho real\n", encoding="utf-8")
        real_file.chmod(0o755)

        sym_file = src_scripts / "sym_target.sh"
        sym_file.symlink_to(real_file)

        source_data = {
            "version": 1,
            "shortcuts": {
                "sym_script": {"description": "symlink script", "script": "scripts/sym_target.sh"},
                "real_script": {"description": "real script", "script": "scripts/real.sh"},
            },
        }
        (src_repo / "shortcuts.json").write_text(json.dumps(source_data), encoding="utf-8")

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0)
        self.assertIn("sym_script (symlink in source)", res.stdout)
        self.assertIn("real_script", res.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("real_script", reg["shortcuts"])
        self.assertNotIn("sym_script", reg["shortcuts"])

        copied_script = self.shortcuts_home / "scripts" / "real_script.sh"
        self.assertTrue(copied_script.is_file())
        self.assertFalse(copied_script.is_symlink())

    def test_migrate_skips_existing_target(self):
        scripts_dir = self.shortcuts_home / "scripts"
        scripts_dir.mkdir(parents=True, exist_ok=True)
        existing_script_a = scripts_dir / "script_a.sh"
        existing_script_a.write_text("#!/bin/sh\necho original_a\n", encoding="utf-8")

        init_reg = {
            "version": 1,
            "shortcuts": {
                "script_a": {
                    "description": "Original A",
                    "script": "scripts/script_a.sh",
                },
                "script_b": {
                    "description": "Already registered B",
                    "inline": "echo orig_b",
                },
            },
        }
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(init_reg), encoding="utf-8")

        flat_a = self.shortcuts_home / "script_a.sh"
        flat_a.write_text("#!/bin/sh\n# Migrating A\necho new_a\n", encoding="utf-8")

        flat_b = self.shortcuts_home / "script_b.sh"
        flat_b.write_text("#!/bin/sh\n# Migrating B\necho new_b\n", encoding="utf-8")

        res = self.run_cli(["migrate"])
        self.assertEqual(res.returncode, 0)
        self.assertIn("skipped script_a.sh (script_a already exists)", res.stdout)
        self.assertIn("skipped script_b.sh (script_b already exists)", res.stdout)

        # Legacy files left in place
        self.assertTrue(flat_a.is_file())
        self.assertTrue(flat_b.is_file())

        # Existing script and registry entries unchanged
        self.assertEqual(existing_script_a.read_text(encoding="utf-8"), "#!/bin/sh\necho original_a\n")
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["script_a"]["description"], "Original A")
        self.assertEqual(reg["shortcuts"]["script_b"]["description"], "Already registered B")
        self.assertEqual(reg["shortcuts"]["script_b"]["inline"], "echo orig_b")

    def test_run_nolog_ctrl_c_returns_130(self):
        sleeper = self.shortcuts_home / "scripts" / "nolog_sleep.sh"
        sleeper.parent.mkdir(parents=True, exist_ok=True)
        sleeper.write_text("#!/usr/bin/env bash\necho started\nsleep 10\n", encoding="utf-8")
        sleeper.chmod(0o755)

        reg = {
            "version": 1,
            "shortcuts": {
                "nolog_sleep": {
                    "description": "Sleeper without log",
                    "script": "scripts/nolog_sleep.sh",
                    "log": False,
                },
            },
        }
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")

        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "nolog_sleep"],
            cwd=str(self.home_dir),
            env=self.env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
            start_new_session=True,
        )
        assert proc.stdout is not None
        line = b""
        start_time = time.time()
        while b"started" not in line and (time.time() - start_time < 5):
            chunk = proc.stdout.readline()
            if not chunk:
                break
            line += chunk

        os.killpg(proc.pid, signal.SIGINT)
        proc.communicate(timeout=15)
        self.assertEqual(proc.returncode, 130)

    def test_new_without_terminal_skips_editor(self):
        env = self.env.copy()
        env.pop("SHORTCUT_EDITOR_CMD", None)
        env["EDITOR"] = "/bin/false"
        res = subprocess.run(
            [sys.executable, str(SHORTCUT), "new", "tmpl", "--desc", "x"],
            cwd=str(self.home_dir), env=env, stdin=subprocess.DEVNULL, text=True, capture_output=True,
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("Edit it with: shortcut edit tmpl", res.stdout)
        self.assertTrue((self.shortcuts_home / "scripts" / "tmpl.sh").is_file())

    def test_new_and_edit_use_EDITOR_then_fall_back_to_nano(self):
        fakebin = self.tmp_path / "fakebin"
        fakebin.mkdir(parents=True, exist_ok=True)
        for editor in ("myeditor", "nano"):
            fake = fakebin / editor
            fake.write_text(f'#!/usr/bin/env bash\necho "# edited by {editor}" >> "$1"\n', encoding="utf-8")
            fake.chmod(0o755)
        path = f"{fakebin}:{self.env.get('PATH', '')}"
        self.env.pop("SHORTCUT_EDITOR_CMD", None)

        rc, out = self.run_in_terminal(["new", "tmpl", "--desc", "x"], env={"PATH": path, "VISUAL": "", "EDITOR": "myeditor"})
        self.assertEqual(rc, 0, out)
        script = self.shortcuts_home / "scripts" / "tmpl.sh"
        self.assertIn("# edited by myeditor", script.read_text(), out)

        res = self.run_cli(["edit", "tmpl"], env={"PATH": path, "VISUAL": "", "EDITOR": ""})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("# edited by nano", script.read_text())

    def test_a_name_alone_never_runs_a_shortcut(self):
        self.run_cli(["new", "hello", "--", "echo hello"])
        for argv in (["hello"], ["nano", "hello"]):
            res = self.run_cli(argv)
            self.assertEqual(res.returncode, 2)
            self.assertEqual(res.stdout, "")
            self.assertIn(f"unknown command '{argv[0]}'", res.stderr)
            self.assertIn(f"shortcut run {argv[0]}", res.stderr)

    def test_r_is_short_for_run(self):
        self.run_cli(["new", "hello", "--", 'echo "hello $1"'])
        self.assertEqual(self.run_cli(["r", "hello", "you"]).stdout, "hello you\n")
        self.assertIn("shortcut run|r [--global] [-y] <name>", self.run_cli(["r", "--help"]).stdout)

    def test_command_words_are_ordinary_shortcut_names(self):
        for name in ("new", "list", "run", "r", "nano"):
            res = self.run_cli(["new", name, "--", f"echo ran {name}"])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertEqual(self.run_cli(["run", name]).stdout, f"ran {name}\n")
        self.assertIn("run", self.run_cli(["list", "--names"]).stdout.split())

    def test_new_with_missing_editor_creates_nothing(self):
        res = self.run_cli(["new", "tmpl", "--desc", "x"], env={"SHORTCUT_EDITOR_CMD": "no-such-editor-xyz"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("editor not found: no-such-editor-xyz", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "tmpl" in self.load_global())
        self.assertFalse((self.shortcuts_home / "scripts" / "tmpl.sh").exists())

    def load_global(self) -> dict:
        return json.loads((self.shortcuts_home / "shortcuts.json").read_text())["shortcuts"]

    def test_new_in_home_refuses_local_store(self):
        res = self.run_cli(["new", "hello", "--local", "--", "echo hi"], cwd=str(self.home_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("is the global store", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "hello" in self.load_global())

        res_init = self.run_cli(["init"], cwd=str(self.home_dir))
        self.assertEqual(res_init.returncode, 1)
        self.assertIn("is the global store", res_init.stderr)

    def test_default_home_store_is_never_a_project(self):
        """With SHORTCUTS_HOME elsewhere, ~/.shortcuts is still not a project of anything under ~."""
        self.run_cli(["init", "--global"], env={"SHORTCUTS_HOME": ""})
        real = self.tmp_path / "custom_store"
        env = {"SHORTCUTS_HOME": str(real)}
        (self.home_dir / "work").mkdir()
        res = self.run_cli(["new", "here", "--local", "--", "echo 1"], cwd=str(self.home_dir / "work"), env=env)
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertTrue((self.home_dir / "work" / ".shortcuts" / "shortcuts.json").is_file())
        self.assertEqual(self.load_global(), {})

    def test_run_on_terminal_uses_pty_and_logs(self):
        inline = '[ -t 1 ] && echo "stdout:tty" || echo "stdout:pipe"; stty size; echo ready; read -r line; echo "got:$line"'
        self.assertEqual(self.run_cli(["new", "tty", "--", inline]).returncode, 0)

        rc, out = self.run_in_terminal(["run", "tty"], keys=[(b"ready", b"hello\r")])
        self.assertEqual(rc, 0, out)
        self.assertIn("stdout:tty", out)
        self.assertIn("30 100", out)
        self.assertIn("got:hello", out)

        log = (self.shortcuts_home / "logs" / "tty.log").read_bytes()
        self.assertIn(b"stdout:tty", log)
        self.assertIn(b"got:hello", log)
        self.assertNotIn(b"\r\n", log)
        self.assertTrue(log.rstrip().endswith(b"=== exit: 0 ==="), log)

        res = self.run_cli(["run", "tty"], input="x\n")
        self.assertIn("stdout:pipe", res.stdout)
        self.assertIn("got:x", res.stdout)

    def test_ctrl_c_on_terminal_returns_130(self):
        self.assertEqual(self.run_cli(["new", "sleep", "--", "echo started; sleep 10"]).returncode, 0)
        start = time.time()
        rc, out = self.run_in_terminal(["run", "sleep"], keys=[(b"started", b"\x03")])
        self.assertEqual(rc, 130, out)
        self.assertLess(time.time() - start, 8)
        log = (self.shortcuts_home / "logs" / "sleep.log").read_text()
        self.assertTrue(log.rstrip().endswith("=== exit: 130 ==="), log)

    def test_sigterm_on_terminal_restores_terminal(self):
        self.assertEqual(self.run_cli(["new", "sleep", "--", "echo started; sleep 10"]).returncode, 0)
        rc, out = self.run_in_terminal(["run", "sleep"], keys=[(b"started", signal.SIGTERM)])
        self.assertEqual(rc, 143, out)
        self.assertTrue(self.terminal_lflag & termios.ICANON, "terminal left in raw mode")
        self.assertTrue(self.terminal_lflag & termios.ISIG, "terminal left in raw mode")
        log = (self.shortcuts_home / "logs" / "sleep.log").read_text()
        self.assertTrue(log.rstrip().endswith("=== exit: 143 ==="), log)

    def test_local_shortcut_hiding_global_warns(self):
        fixed = self.tmp_path / "fixed"
        fixed.mkdir()
        self.assertEqual(self.run_cli(["new", "penpot", "--cwd", str(fixed), "--", "echo global"]).returncode, 0)
        proj = self.tmp_path / "proj"
        proj.mkdir()
        res = self.run_cli(["new", "penpot", "--local", "--cwd", ".", "--", "echo local"], cwd=str(proj))
        self.assertEqual(res.returncode, 0)
        update = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("Inside this project, 'penpot' hides your global 'penpot'.", update.stdout)

        res = self.run_cli(["run", "penpot"], cwd=str(proj))
        self.assertEqual(res.stdout.strip(), "local")
        self.assertIn(f"\u2192 {proj}  (project shortcut; your global 'penpot' is hidden here", res.stderr)

        res_global = self.run_cli(["run", "--global", "penpot"], cwd=str(proj))
        self.assertEqual(res_global.stdout.strip(), "global")

        listing = self.run_cli(["list"], cwd=str(proj)).stdout
        self.assertIn("[hides global]", listing)
        self.assertIn("[hidden here by project]", listing)

        res_elsewhere = self.run_cli(["run", "penpot"], cwd=str(self.tmp_path))
        self.assertEqual(res_elsewhere.stdout.strip(), "global")
        self.assertEqual(res_elsewhere.stderr, f"\u2192 {fixed}\n")

    def test_run_directory_line(self):
        work = self.tmp_path / "work"
        work.mkdir()
        self.assertEqual(self.run_cli(["new", "pwd", "--", "pwd"]).returncode, 0)
        res = self.run_cli(["run", "pwd"], cwd=str(work))
        self.assertEqual(res.stdout, f"{work}\n")
        self.assertEqual(res.stderr, f"\u2192 {work}\n")
        res = self.run_cli(["run", "pwd"], cwd=str(work), env={"SHORTCUT_SHOW_DIR": "0"})
        self.assertEqual(res.stderr, "")
        rc, out = self.run_in_terminal(["run", "pwd"], cwd=str(work))
        self.assertIn(f"\x1b[2m\u2192 {work}\x1b[0m", out)

    def test_new_editor_failure_creates_nothing(self):
        res = self.run_cli(["new", "tmpl", "--desc", "x"], env={"SHORTCUT_EDITOR_CMD": "false"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("was not created", res.stderr)
        self.assertNotIn("tmpl", self.load_global() if (self.shortcuts_home / "shortcuts.json").exists() else {})
        self.assertFalse((self.shortcuts_home / "scripts" / "tmpl.sh").exists())

    def test_concurrent_new_keeps_every_entry(self):
        procs = [
            subprocess.Popen(
                [sys.executable, str(SHORTCUT), "new", f"n{i}", "--", f"echo {i}"],
                cwd=str(self.home_dir), env=self.env, stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
            )
            for i in range(16)
        ]
        for proc in procs:
            _, err = proc.communicate(timeout=30)
            self.assertEqual(proc.returncode, 0, err)
        self.assertEqual(sorted(self.load_global()), sorted(f"n{i}" for i in range(16)))

    def test_fix_strips_terminal_codes(self):
        inline = 'printf "\\033[31mred\\033[0m\\n50%%\\r100%%\\n"'
        self.assertEqual(self.run_cli(["new", "color", "--", inline]).returncode, 0)
        self.run_cli(["run", "color"])
        fix = self.run_cli(["fix", "color"]).stdout
        log_part = fix.split("--- Last Log ---", 1)[1]
        self.assertNotIn("\x1b", log_part)
        self.assertIn("\nred\n", log_part)
        self.assertIn("\n100%\n", log_part)
        self.assertNotIn("50%", log_part)

    def test_completion_zsh_and_fish(self):
        zsh = self.run_cli(["completion", "zsh"])
        self.assertEqual(zsh.returncode, 0)
        self.assertIn("compdef _shortcut shortcut", zsh.stdout)
        fish = self.run_cli(["completion", "fish"])
        self.assertEqual(fish.returncode, 0)
        self.assertIn("complete -c shortcut", fish.stdout)

    def test_editor_failure_is_clean_error(self):
        self.run_cli(["new", "inline_fail", "--", "echo test"])
        res = self.run_cli(["edit", "inline_fail"], env={"SHORTCUT_EDITOR_CMD": "false"})
        self.assertEqual(res.returncode, 1)
        self.assertTrue(res.stderr.strip().startswith("error:"), f"Expected stderr to start with 'error:', got:\n{res.stderr}")
        self.assertNotIn("Traceback", res.stderr)

    def test_malformed_entries_never_crash(self):
        proj = self.tmp_path / "proj_a5"
        proj.mkdir()
        (proj / ".shortcuts").mkdir()
        reg = {
            "version": 1,
            "shortcuts": {
                "bad": "echo hi",
            },
        }
        (proj / ".shortcuts" / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("expected a dictionary", res.stderr)

        # The global store isn't approved, so a bad entry can reach `run`
        self.write_store(self.shortcuts_home, {"bad": "echo hi"})
        for cwd in (proj, self.tmp_path):
            res = self.run_cli(["run", "bad"], cwd=str(cwd))
            self.assertEqual(res.returncode, 1)
            self.assertTrue(res.stderr.strip().startswith("error:"))
            self.assertNotIn("Traceback", res.stderr)

        src_dir = self.tmp_path / "src_a5"
        src_dir.mkdir()
        src_reg = {
            "version": 1,
            "shortcuts": {
                "bad_dir": {"description": "Bad dir", "inline": "echo hi", "cwd": 123},
            },
        }
        (src_dir / "shortcuts.json").write_text(json.dumps(src_reg), encoding="utf-8")

        res_init = self.run_cli(["init", "--global", str(src_dir)])
        self.assertEqual(res_init.returncode, 0)
        self.assertIn("bad_dir (invalid entry: 'bad_dir' is not a valid entry: 'cwd' must be a string", res_init.stdout)
        self.assertNotIn("Traceback", res_init.stderr)

    def test_broken_output_pipe_exits_141_and_logs(self):
        self.assertEqual(self.run_cli(["new", "seq", "--", "seq 1 1000000"]).returncode, 0)
        p_shortcut = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "seq"],
            cwd=str(self.home_dir),
            env=self.env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        p_head = subprocess.Popen(
            ["head", "-n", "1"],
            stdin=p_shortcut.stdout,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        assert p_shortcut.stdout is not None
        p_shortcut.stdout.close()
        head_out, _ = p_head.communicate(timeout=5)
        _, sc_err = p_shortcut.communicate(timeout=5)
        self.assertEqual(head_out.strip(), "1")
        self.assertEqual(p_shortcut.returncode, 141)
        self.assertNotIn("BrokenPipeError", sc_err)
        self.assertNotIn("Exception ignored", sc_err)
        log_path = self.shortcuts_home / "logs" / "seq.log"
        self.assertTrue(log_path.is_file())
        log_content = log_path.read_text(encoding="utf-8")
        self.assertIn("=== exit: 141 ===", log_content)

    def test_signals_reach_child_group_in_pipe_mode(self):
        pid_file = self.tmp_path / "child.pid"
        inline = f"sh -c 'echo $$ > {pid_file}; exec sleep 300'; echo done"
        self.assertEqual(self.run_cli(["new", "group_sig", "--", inline]).returncode, 0)
        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "group_sig"],
            cwd=str(self.home_dir),
            env=self.env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
            start_new_session=True,
        )
        start = time.time()
        child_pid = None
        while time.time() - start < 5:
            if pid_file.exists():
                text = pid_file.read_text().strip()
                if text.isdigit():
                    child_pid = int(text)
                    break
            time.sleep(0.05)
        self.assertIsNotNone(child_pid, "PIDFILE was not written in time")
        assert child_pid is not None
        def is_alive(pid: int) -> bool:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return False
            try:
                status_file = pathlib.Path(f"/proc/{pid}/status")
                if status_file.exists():
                    for line in status_file.read_text().splitlines():
                        if line.startswith("State:"):
                            return "Z" not in line
            except OSError:
                return False
            return True

        self.assertTrue(is_alive(child_pid), f"child process {child_pid} not running before SIGTERM")

        proc.send_signal(signal.SIGTERM)
        proc.communicate(timeout=5)

        deadline = time.time() + 2.0
        alive = True
        while time.time() < deadline:
            if not is_alive(child_pid):
                alive = False
                break
            time.sleep(0.05)
        self.assertFalse(alive, f"child pid {child_pid} still running after SIGTERM")

    def test_script_shebang_without_exec_and_elf(self):
        script = self.tmp_path / "py_script.py"
        script.write_text("#!/usr/bin/env python3\nimport sys\nprint('py', 1 + 1)\n", encoding="utf-8")
        script.chmod(0o644)
        self.assertEqual(self.run_cli(["add", "py", str(script), "--desc", "python"]).returncode, 0)
        res = self.run_cli(["run", "py"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("py 2", res.stdout)

        sh_no_exec = self.tmp_path / "no_shebang.sh"
        sh_no_exec.write_text("echo nonexec\n", encoding="utf-8")
        sh_no_exec.chmod(0o644)
        self.assertEqual(self.run_cli(["add", "noshebang", str(sh_no_exec), "--desc", "noshebang"]).returncode, 0)
        res2 = self.run_cli(["run", "noshebang"])
        self.assertEqual(res2.returncode, 0, res2.stderr)
        self.assertIn("nonexec", res2.stdout)

        sh_exec = self.tmp_path / "exec.sh"
        sh_exec.write_text("#!/usr/bin/env bash\necho exec_shebang\n", encoding="utf-8")
        sh_exec.chmod(0o755)
        self.assertEqual(self.run_cli(["add", "exec", str(sh_exec), "--desc", "exec"]).returncode, 0)
        res3 = self.run_cli(["run", "exec"])
        self.assertEqual(res3.returncode, 0, res3.stderr)
        self.assertIn("exec_shebang", res3.stdout)

    def test_run_help(self):
        res1 = self.run_cli(["run", "--help"])
        self.assertEqual(res1.returncode, 0)
        self.assertIn("shortcut run|r [--global] [-y] <name> [args...]", res1.stdout)

        res2 = self.run_cli(["run", "-h"])
        self.assertEqual(res2.returncode, 0)
        self.assertIn("shortcut run|r [--global] [-y] <name> [args...]", res2.stdout)

        self.assertEqual(self.run_cli(["new", "echo_args", "--", "echo \"args: $@\""]).returncode, 0)
        res3 = self.run_cli(["run", "echo_args", "--help"])
        self.assertEqual(res3.returncode, 0)
        self.assertIn("args: --help", res3.stdout)

    def test_rm_local_keeps_global_desktop_file(self):
        self.install_bundled()
        proj_dir = self.tmp_path / "penpot_proj"
        proj_dir.mkdir(parents=True, exist_ok=True)
        self.assertEqual(self.run_cli(["new", "penpot", "--desc", "Global penpot", "--", "echo", "global"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "desktop", "penpot"]).returncode, 0)
        desktop_file = self.xdg_data_home / "applications" / "shortcut-penpot.desktop"
        self.assertTrue(desktop_file.is_file())

        res = self.run_cli(["new", "penpot", "--local", "--desc", "Project penpot", "--", "echo", "local"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        res_rm = self.run_cli(["rm", "penpot", "-y"], cwd=str(proj_dir))
        self.assertEqual(res_rm.returncode, 0)
        self.assertTrue(desktop_file.is_file(), "Removing local shortcut must keep global desktop file")
        self.assertIn("penpot", self.load_global())

    def test_migrate_args_and_validation(self):
        # 1. migrate --help leaves old.sh in place
        old_script = self.shortcuts_home / "old.sh"
        old_script.write_text("#!/bin/sh\n# Old script\necho old\n", encoding="utf-8")
        old_script.chmod(0o755)

        res_help = self.run_cli(["migrate", "--help"])
        self.assertEqual(res_help.returncode, 0)
        self.assertIn("usage: shortcut migrate", res_help.stdout)
        self.assertTrue(old_script.is_file(), "migrate --help must leave old.sh in place")
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

        # 2. my.tool.sh (invalid name) and symlink are reported as skipped and stay where they are
        invalid_script = self.shortcuts_home / "my.tool.sh"
        invalid_script.write_text("#!/bin/sh\n# Dot in name\necho invalid\n", encoding="utf-8")
        invalid_script.chmod(0o755)

        target_script = self.tmp_path / "symlink_target.sh"
        target_script.write_text("#!/bin/sh\n# Target\necho target\n", encoding="utf-8")
        symlink_script = self.shortcuts_home / "symlink.sh"
        symlink_script.symlink_to(target_script)

        res_migrate = self.run_cli(["migrate"])
        self.assertEqual(res_migrate.returncode, 0)
        self.assertIn("skipped my.tool.sh", res_migrate.stdout)
        self.assertIn("skipped symlink.sh", res_migrate.stdout)

        # Both skipped files stay where they are
        self.assertTrue(invalid_script.is_file())
        self.assertTrue(symlink_script.is_symlink())

        # old.sh was valid and got migrated
        self.assertFalse(old_script.exists())
        self.assertTrue((self.shortcuts_home / "scripts" / "old.sh").is_file())

    def test_rm_malformed_entry(self):
        reg = {"shortcuts": {"bad_entry": {"description": "x", "inline": "true", "cwd": 123}}}
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")
        res = self.run_cli(["rm", "bad_entry", "-y"])
        self.assertEqual(res.returncode, 0)
        after = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("bad_entry", after.get("shortcuts", {}))


    def test_location_prints_a_bare_path_or_fails(self):
        self.run_cli(["new", "inline_one", "--", "echo hi"])
        res = self.run_cli(["location", "inline_one"])
        self.assertEqual(res.returncode, 1)
        self.assertEqual(res.stdout, "")
        self.assertIn("inline shortcut", res.stderr)
        self.run_cli(["new", "scripted"])
        res = self.run_cli(["location", "scripted"])
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/scripts/scripted.sh\n")
        res = self.run_cli(["location", "--log", "inline_one"])
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/logs/inline_one.log\n")

    def test_run_cwd_modes(self):
        proj = self.tmp_path / "proj"
        (proj / "frontend").mkdir(parents=True)
        (proj / "src").mkdir()
        fixed = self.tmp_path / "fixed"
        fixed.mkdir()
        self.write_store(proj / ".shortcuts", {
            "default": {"inline": "pwd"},
            "caller": {"inline": "pwd", "cwd": "caller"},
            "defining": {"inline": "pwd", "cwd": "."},
            "root": {"inline": "pwd", "cwd": ".."},
            "sibling": {"inline": "pwd", "cwd": "../frontend"},
            "fixed": {"inline": "pwd", "cwd": str(fixed)},
            "gone": {"inline": "pwd", "cwd": "../nope"},
        })
        self.allow(proj / ".shortcuts")
        here = proj / "src"
        for name, expected in (
            ("default", here),
            ("caller", here),
            ("defining", proj / ".shortcuts"),
            ("root", proj),
            ("sibling", proj / "frontend"),
            ("fixed", fixed),
        ):
            res = self.run_cli(["r", name], cwd=str(here))
            self.assertEqual(res.stdout, f"{expected}\n", name)
            self.assertIn(f"→ {expected}\n", res.stderr)
        res = self.run_cli(["r", "gone"], cwd=str(here))
        self.assertEqual(res.returncode, 1)
        self.assertIn("run directory does not exist", res.stderr)

    def test_confirm(self):
        self.run_cli(["new", "danger", "--confirm", "--", "echo ran; pwd"])
        self.assertTrue(self.load_global()["danger"]["confirm"])
        work = self.tmp_path / "work"
        work.mkdir()

        res = self.run_cli(["r", "danger"], cwd=str(work))
        self.assertEqual(res.returncode, 1)
        self.assertEqual(res.stdout, "")
        self.assertIn("pass -y", res.stderr)

        res = self.run_cli(["r", "-y", "danger"], cwd=str(work))
        self.assertEqual(res.returncode, 0)
        self.assertIn("ran", res.stdout)

        prompt = f"run 'danger' in {work}? [y/N]".encode()
        rc, out = self.run_in_terminal(["r", "danger"], cwd=str(work), keys=[(prompt, b"y\n")])
        self.assertEqual(rc, 0)
        self.assertIn("ran", out)
        rc, out = self.run_in_terminal(["r", "danger"], cwd=str(work), keys=[(prompt, b"n\n")])
        self.assertEqual(rc, 1)
        self.assertNotIn("ran\r\n", out)

        # After the name, -y belongs to the script
        self.run_cli(["new", "echoes", "--", 'echo "$@"'])
        self.assertEqual(self.run_cli(["r", "echoes", "-y"]).stdout, "-y\n")

    def test_namespace_dispatch(self):
        imports = self.shortcuts_home / "imports"
        imports.mkdir()
        (imports / "scripts").mkdir()
        (imports / "scripts" / "del.sh").write_text('#!/usr/bin/env bash\necho "deleting $1"\n', encoding="utf-8")
        (imports / "docker.shortcut").write_text(json.dumps({"version": 2, "shortcuts": {
            "delete": {"script": "scripts/del.sh"},
            "where": {"inline": "pwd", "cwd": "."},
        }}), encoding="utf-8")
        self.run_cli(["init", "--global"])
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)

        res = self.run_cli(["r", "docker", "delete", "XYZ"])
        self.assertEqual(res.stdout, "deleting XYZ\n", res.stderr)
        self.assertEqual(self.run_cli(["r", "docker", "where"]).stdout, f"{imports}\n")
        self.assertEqual(self.run_cli(["location", "docker", "delete"]).stdout, f"{imports}/scripts/del.sh\n")
        self.assertIn("Name:        docker delete", self.run_cli(["show", "docker", "delete"]).stdout)
        self.assertIn("docker", self.run_cli(["list", "--names"]).stdout.split())
        self.assertRegex(self.run_cli(["list"]).stdout, r"docker delete +caller")

        res = self.run_cli(["r", "docker"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'docker' is a namespace; run one of its shortcuts: delete, where", res.stderr)
        res = self.run_cli(["r", "docker", "nope"])
        self.assertIn("'nope' not found in namespace 'docker'", res.stderr)

    def test_namespace_from_imports_key(self):
        elsewhere = self.tmp_path / "shared"
        elsewhere.mkdir()
        (elsewhere / "tools.shortcut").write_text(
            json.dumps({"version": 2, "shortcuts": {"hi": {"inline": "echo hi from tools"}}}), encoding="utf-8"
        )
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"own": {"inline": "echo own"}}, imports={"tools": "../../shared/tools.shortcut"})
        self.allow(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["r", "tools", "hi"], cwd=str(proj)).stdout, "hi from tools\n")
        self.assertEqual(self.run_cli(["r", "own"], cwd=str(proj)).stdout, "own\n")

    def test_namespace_collision_and_nesting_errors(self):
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"docker": {"inline": "echo shortcut"}, "ok": {"inline": "echo ok"}})
        imports = proj / ".shortcuts" / "imports"
        imports.mkdir()
        (imports / "docker.shortcut").write_text('{"version": 2, "shortcuts": {"ps": {"inline": "echo ps"}}}', encoding="utf-8")
        self.run_cli(["new", "outside", "--", "echo outside"])
        self.allow(proj / ".shortcuts")

        for words in (["docker"], ["docker", "ps"], ["ok"]):
            res = self.run_cli(["r", *words], cwd=str(proj))
            self.assertEqual(res.returncode, 1)
            self.assertIn("'docker' is both a shortcut and a namespace", res.stderr)
        # Only that store: names from a healthy one still run
        self.assertEqual(self.run_cli(["r", "outside"], cwd=str(proj)).stdout, "outside\n")
        self.assertIn("BROKEN: 'docker' is both", self.run_cli(["fix"], cwd=str(proj)).stdout)

        (imports / "docker.shortcut").unlink()
        (imports / "nested.shortcut").write_text('{"version": 2, "imports": {"x": "x.shortcut"}, "shortcuts": {}}', encoding="utf-8")
        res = self.run_cli(["r", "nested", "x"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("namespaces don't nest", res.stderr)

    def write_import(self, store_dir: pathlib.Path, name: str, data: dict) -> pathlib.Path:
        imports = store_dir / "imports"
        imports.mkdir(parents=True, exist_ok=True)
        path = imports / f"{name}.shortcut"
        path.write_text(json.dumps(data), encoding="utf-8")
        return path

    def test_imports_must_be_shortcut_files(self):
        imports = self.shortcuts_home / "imports"
        imports.mkdir()
        (imports / "docker.json").write_text('{"version": 2, "shortcuts": {"ps": {"inline": "echo ps"}}}', encoding="utf-8")
        self.run_cli(["init", "--global"])
        self.assertIn("'docker' not found", self.run_cli(["r", "docker", "ps"]).stderr)

        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"own": {"inline": "echo own"}}, imports={"tools": "tools.json"})
        self.allow(proj / ".shortcuts")
        res = self.run_cli(["r", "own"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("import 'tools'", res.stderr)
        self.assertIn("must be a .shortcut file", res.stderr)

    def test_shortcut_file_needs_the_current_version(self):
        self.run_cli(["init", "--global"])
        path = self.write_import(self.shortcuts_home, "docker", {"shortcuts": {"ps": {"inline": "echo ps"}}})
        res = self.run_cli(["update", "docker", "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn('needs "version": 2', res.stderr)

        path.write_text(json.dumps({"version": 3, "shortcuts": {"ps": {"inline": "echo ps"}}}), encoding="utf-8")
        res = self.run_cli(["update", "docker", "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("newer than this shortcut reads; upgrade shortcut", res.stderr)

    def test_newer_store_is_refused_not_rewritten(self):
        self.write_store(self.shortcuts_home, {"hi": {"inline": "echo hi"}}, version=3)
        before = (self.shortcuts_home / "shortcuts.json").read_text(encoding="utf-8")
        for args in (["r", "hi"], ["new", "more", "--", "echo more"]):
            res = self.run_cli(args)
            self.assertEqual(res.returncode, 1, args)
            self.assertIn("upgrade shortcut", res.stderr)
        self.assertEqual((self.shortcuts_home / "shortcuts.json").read_text(encoding="utf-8"), before)

    def test_unknown_fields_are_errors(self):
        self.run_cli(["init", "--global"])
        cases = {
            "top": ({"version": 2, "owner": "x", "shortcuts": {}}, "unknown field: owner"),
            "entry": ({"version": 2, "shortcuts": {"a": {"inline": "true", "sandbox": True}}}, "unknown field: sandbox"),
            "arg": (
                {"version": 2, "shortcuts": {"a": {"inline": "true", "args": [{"name": "x", "secret": True}]}}},
                "unknown args field: secret",
            ),
        }
        for name, (data, message) in cases.items():
            self.write_import(self.shortcuts_home, name, data)
            res = self.run_cli(["update", name, "-y"])
            self.assertEqual(res.returncode, 1, name)
            self.assertIn(message, res.stderr)
            self.assertIn(message, self.run_cli(["fix"]).stdout)

    def test_update_refuses_entries_that_cannot_run(self):
        self.run_cli(["init", "--global"])
        cases = {
            "neither": ({"a": {"description": "x"}}, "exactly one of 'inline' and 'script'"),
            "both": ({"a": {"inline": "true", "script": "a.sh"}}, "exactly one of 'inline' and 'script'"),
            "missing": ({"a": {"script": "scripts/a.sh"}}, "script not found: scripts/a.sh"),
            "escape": ({"a": {"script": "../../outside.sh"}}, "path traversal"),
            "badname": ({"a!": {"inline": "true"}}, "invalid shortcut name"),
        }
        for name, (shortcuts, message) in cases.items():
            self.write_import(self.shortcuts_home, name, {"version": 2, "shortcuts": shortcuts})
            res = self.run_cli(["update", name, "-y"])
            self.assertEqual(res.returncode, 1, name)
            self.assertIn(message, res.stderr)
            refused = self.run_cli(["r", name, *shortcuts])
            self.assertEqual(refused.returncode, 1, name)
            self.assertIn(message if "exactly one" in message else "isn't approved yet", refused.stderr)

    def test_group_description_shows_once_approved(self):
        self.run_cli(["init", "--global"])
        self.write_import(self.shortcuts_home, "docker", {
            "version": 2, "description": "Docker housekeeping", "shortcuts": {"ps": {"inline": "echo ps"}},
        })
        self.assertNotIn("Docker housekeeping", self.run_cli(["list"]).stdout)
        self.assertIn("Docker housekeeping", self.run_cli(["update", "docker", "-y"]).stdout)
        self.assertIn("  Docker housekeeping\n", self.run_cli(["list"]).stdout)

    def test_namespace_flag_creates_a_namespace(self):
        res = self.run_cli(["new", "delete", "--namespace", "docker", "--desc", "Remove one", "--", 'echo "rm $1"'])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("needs approving before it runs: shortcut update docker", res.stderr)
        path = self.shortcuts_home / "imports" / "docker.shortcut"
        data = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(data["version"], 2)
        self.assertEqual(data["shortcuts"]["delete"]["inline"], 'echo "rm $1"')
        self.assertNotIn("delete", self.load_global())

        self.assertIn("isn't approved yet", self.run_cli(["r", "docker", "delete", "x"]).stderr)
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "delete", "x"]).stdout, "rm x\n")

        # Adding to it is a change, so it needs approving again
        self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo ps"])
        self.assertIn("changed since you approved it", self.run_cli(["r", "docker", "ps"]).stderr)
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo again"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("already exists", res.stderr)

    def test_namespace_flag_collisions(self):
        self.run_cli(["new", "docker", "--", "echo plain"])
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo ps"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'docker' is a shortcut", res.stderr)
        self.assertFalse((self.shortcuts_home / "imports").exists())

        res = self.run_cli(["new", "ps", "--namespace", "not valid", "--", "echo ps"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("invalid shortcut name", res.stderr)

        self.run_cli(["new", "ps", "--namespace", "kube", "--", "echo ps"])
        res = self.run_cli(["new", "kube", "--", "echo plain"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'kube' is a namespace", res.stderr)

    def test_namespace_flag_keeps_each_groups_scripts_apart(self):
        source = self.tmp_path / "src.sh"
        for group, word in (("docker", "dock"), ("kube", "kub")):
            source.write_text(f"#!/usr/bin/env bash\necho {word}\n", encoding="utf-8")
            res = self.run_cli(["add", "delete", str(source), "--namespace", group, "--desc", "d"])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertEqual(self.run_cli(["update", group, "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "delete"]).stdout, "dock\n")
        self.assertEqual(self.run_cli(["r", "kube", "delete"]).stdout, "kub\n")

        copy = self.shortcuts_home / "imports" / "scripts" / "docker" / "delete.sh"
        self.assertEqual(self.run_cli(["location", "docker", "delete"]).stdout, f"{copy}\n")
        self.assertEqual(self.run_cli(["rm", "docker", "delete", "-y"]).returncode, 0)
        self.assertFalse(copy.exists())
        self.assertTrue((self.shortcuts_home / "imports" / "scripts" / "kube" / "delete.sh").exists())

    def test_namespace_flag_in_a_project_uses_the_files_folder_for_cwd(self):
        proj = self.tmp_path / "proj"
        (proj / "src").mkdir(parents=True)
        res = self.run_cli(
            ["new", "where", "--namespace", "dev", "--local", "--cwd", str(proj / "src"), "--", "pwd"], cwd=str(proj)
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        path = proj / ".shortcuts" / "imports" / "dev.shortcut"
        self.assertEqual(json.loads(path.read_text(encoding="utf-8"))["shortcuts"]["where"]["cwd"], "../../src")
        self.assertEqual(self.run_cli(["update", "dev", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["r", "dev", "where"], cwd=str(proj)).stdout, f"{proj / 'src'}\n")

    def test_global_flag_reaches_a_hidden_shortcut(self):
        proj = self.tmp_path / "proj"
        proj.mkdir()
        script = self.tmp_path / "tool.sh"
        script.write_text("#!/usr/bin/env bash\n", encoding="utf-8")
        self.run_cli(["new", "build", "--", "echo global"])
        self.run_cli(["new", "build", "--local", "--", "echo project"], cwd=str(proj))
        self.run_cli(["add", "tool", str(script), "--desc", "d"])
        self.run_cli(["add", "tool", str(script), "--desc", "d", "--local"], cwd=str(proj))
        self.allow(proj / ".shortcuts")

        self.assertIn("echo project", self.run_cli(["show", "build"], cwd=str(proj)).stdout)
        res = self.run_cli(["show", "--global", "build"], cwd=str(proj))
        self.assertIn("Scope:       global\n", res.stdout)
        self.assertIn("echo global", res.stdout)
        self.assertIn("echo global", self.run_cli(["fix", "--global", "build"], cwd=str(proj)).stdout)

        editor = "sed -i s/global/edited/"
        res = self.run_cli(["edit", "--global", "build"], cwd=str(proj), env={"SHORTCUT_EDITOR_CMD": editor})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["build"]["inline"], "echo edited")

        res = self.run_cli(["location", "--global", "tool"], cwd=str(proj))
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/scripts/tool.sh\n")

        self.assertEqual(self.run_cli(["rm", "build", "--global", "-y"], cwd=str(proj)).returncode, 0)
        self.assertNotIn("build", self.load_global())
        self.assertEqual(self.run_cli(["r", "build"], cwd=str(proj)).stdout, "project\n")
        res = self.run_cli(["show", "--global", "build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("not found", res.stderr)

    def test_new_says_when_a_command_ignores_arguments(self):
        hint = "won't see any arguments"
        self.assertIn(hint, self.run_cli(["new", "a", "--", "rg --hidden"]).stderr)
        self.assertIn(hint, self.run_cli(["new", "b", "--", "rg", "--hidden"]).stderr)
        for name, command in (
            ("c", 'rg "$@"'), ("d", "echo $1"), ("e", "echo ${2}"), ("f", "echo $*"), ("g", "echo $SC_ARG_WHO"),
        ):
            res = self.run_cli(["new", name, "--", command])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertNotIn(hint, res.stderr, command)

    def test_several_word_commands_keep_positional_arguments_live(self):
        cases = {
            ("echo", "$1"): 'echo "$1"',
            ("printf", "[%s]", "$@"): 'printf \'[%s]\' "$@"',
            ("echo", "a b", "${1}"): 'echo \'a b\' "${1}"',
            ("echo", "$HOME"): "echo '$HOME'",
            ("echo", 'say "$1"'): 'echo "say \\"$1\\""',
            ("echo", "$(id) `id` \\ $1"): 'echo "\\$(id) \\`id\\` \\\\ $1"',
        }
        for i, (words, stored) in enumerate(cases.items()):
            name = f"cmd{i}"
            self.assertEqual(self.run_cli(["new", name, "--", *words]).returncode, 0)
            self.assertEqual(self.load_global()[name]["inline"], stored)

        self.assertEqual(self.run_cli(["r", "cmd0", "hello world"]).stdout, "hello world\n")
        self.assertEqual(self.run_cli(["r", "cmd1", "a b", "c"]).stdout, "[a b][c]")
        self.assertEqual(self.run_cli(["r", "cmd3"]).stdout, "$HOME\n")
        self.assertEqual(self.run_cli(["r", "cmd5", "x"]).stdout, "$(id) `id` \\ x\n")

    def bash_complete(self, *words: str) -> list[str]:
        """What `shortcut <words><tab>` offers; the last word is the one being completed."""
        line = ["shortcut", *words]
        driver = (
            self.run_cli(["completion", "bash"]).stdout
            + f"COMP_WORDS=({shlex.join(line)}); COMP_CWORD={len(line) - 1}; _shortcut\n"
            + 'printf "%s\\n" "${COMPREPLY[@]}"\n'
        )
        res = subprocess.run(
            ["bash", "-c", driver], cwd=str(self.home_dir), env=self.env, capture_output=True, text=True
        )
        self.assertEqual(res.stderr, "")
        return [line for line in res.stdout.splitlines() if line]

    def test_bash_completion_past_flags_and_into_namespaces(self):
        self.install_bundled()
        self.run_cli(["new", "other", "--", "true"])
        self.run_cli(["new", "delete", "--namespace", "docker", "--", "true"])
        self.run_cli(["new", "ps", "--namespace", "docker", "--", "true"])
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)

        self.assertIn("other", self.bash_complete("r", "o"))
        self.assertEqual(self.bash_complete("r", "--global", "ot"), ["other"])
        self.assertEqual(self.bash_complete("r", "-y", "--global", "doc"), ["docker"])
        self.assertEqual(self.bash_complete("show", "--global", "ot"), ["other"])
        self.assertEqual(self.bash_complete("r", "docker", ""), ["delete", "ps"])
        self.assertEqual(self.bash_complete("rm", "--global", "docker", "d"), ["delete"])
        self.assertEqual(self.bash_complete("r", "docker", "ps", ""), [])
        self.assertEqual(self.bash_complete("r", "other", ""), [])

        self.assertEqual(self.bash_complete("r", "-"), ["--global", "-y"])
        self.assertEqual(self.bash_complete("show", "-"), ["--global"])
        self.assertEqual(self.bash_complete("location", "--"), ["--global", "--log"])
        self.assertEqual(self.bash_complete("r", "other", "--"), [])

    def test_list_names_of_a_namespace(self):
        self.run_cli(["new", "b", "--namespace", "docker", "--", "true"])
        self.run_cli(["new", "a", "--namespace", "docker", "--", "true"])
        self.assertEqual(self.run_cli(["list", "--names", "docker"]).stdout, "")
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["list", "--names", "docker"]).stdout, "a\nb\n")
        self.assertEqual(self.run_cli(["list", "--names", "nothing"]).stdout, "")

        # A project shortcut of that name is what `r docker` runs, so its arguments aren't names
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"docker": {"inline": "true"}})
        self.allow(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["list", "--names", "docker"], cwd=str(proj)).stdout, "")

    def test_declared_args(self):
        script = (
            'printf "[%s]" "$@"; echo; '
            'echo "c=$SC_ARG_CONTAINER s=$SC_ARG_SIGNAL e=$SC_ARG_EXTRA"'
        )
        reg = {"kill": {"inline": script, "args": [
            {"name": "container", "required": True, "desc": "Which one"},
            {"name": "signal", "default": "TERM"},
            {"name": "extra", "rest": True},
        ]}}
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps({"version": 2, "shortcuts": {
            **reg,
            "two": {"inline": 'echo "$#:$*"', "args": [{"name": "a"}, {"name": "b", "default": "B"}]},
            "free": {"inline": 'echo "$#"'},
            "one": {"inline": "true", "args": [{"name": "only"}]},
        }}), encoding="utf-8")

        res = self.run_cli(["r", "kill"])
        self.assertEqual(res.returncode, 2)
        self.assertIn("missing <container>", res.stderr)
        self.assertIn("usage: shortcut r kill <container> [signal] [extra...]", res.stderr)

        out = self.run_cli(["r", "kill", "web"]).stdout.splitlines()
        self.assertEqual(out, ["[web][TERM]", "c=web s=TERM e="])
        out = self.run_cli(["r", "kill", "web", "KILL", "a b", "c"]).stdout.splitlines()
        self.assertEqual(out, ["[web][KILL][a b][c]", "c=web s=KILL e='a b' c"])

        # Flags are neither counted nor moved, and a default keeps its position before them
        out = self.run_cli(["r", "kill", "web", "--force"]).stdout.splitlines()
        self.assertEqual(out[0], "[web][TERM][--force]")

        # A skipped optional keeps later defaults in their own position
        self.assertEqual(self.run_cli(["r", "two"]).stdout, "2: B\n")

        res = self.run_cli(["r", "one", "a", "b"])
        self.assertEqual(res.returncode, 2)
        self.assertIn("too many arguments", res.stderr)
        self.assertIn("usage: shortcut r one [only]", res.stderr)

        # No `args` key: no checking
        self.assertEqual(self.run_cli(["r", "free", "1", "2", "3"]).stdout, "3\n")
        self.assertIn("Usage:       shortcut r kill <container> [signal] [extra...]", self.run_cli(["show", "kill"]).stdout)

    def test_new_declares_args(self):
        res = self.run_cli(["new", "greet", "--arg", "who", "--arg", "greeting=hello", "--", 'echo "$2 $1"'])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(
            self.load_global()["greet"]["args"],
            [{"name": "who", "required": True}, {"name": "greeting", "default": "hello"}],
        )
        self.assertEqual(self.run_cli(["r", "greet", "you"]).stdout, "hello you\n")
        self.assertEqual(self.run_cli(["r", "greet"]).returncode, 2)
        res = self.run_cli(["new", "bad", "--arg", "no spaces", "--", "true"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'bad' is not a valid entry", res.stderr)

    def test_invalid_args_declaration_is_an_error(self):
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps({"version": 2, "shortcuts": {
            "bad": {"inline": "true", "args": [{"name": "a", "rest": True}, {"name": "b"}]},
        }}), encoding="utf-8")
        res = self.run_cli(["r", "bad"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("only the last entry in 'args' can be 'rest'", res.stderr)

    def test_approval_blocks_until_approved_and_after_any_change(self):
        proj = self.tmp_path / "proj"
        scripts = proj / ".shortcuts" / "scripts"
        scripts.mkdir(parents=True)
        script = scripts / "hi.sh"
        script.write_text("#!/usr/bin/env bash\necho hi\n", encoding="utf-8")
        registry = proj / ".shortcuts" / "shortcuts.json"
        data = {"version": 2, "shortcuts": {
            "hi": {"description": "Say hi", "script": ".shortcuts/scripts/hi.sh"},
            "there": {"description": "There", "inline": "echo there"},
        }}
        registry.write_text(json.dumps(data), encoding="utf-8")

        # First run blocked; full listing on first approval
        res = self.run_cli(["r", "hi"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("isn't approved yet", res.stderr)
        self.assertNotIn("hi\n", res.stdout)
        res = self.run_cli(["update"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("in a terminal, or pass -y", res.stderr)
        self.assertIn("+++ b/.shortcuts/shortcuts.json", res.stdout)
        self.assertIn("+++ b/.shortcuts/scripts/hi.sh", res.stdout)
        self.assertIn("+echo hi", res.stdout)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)

        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "hi\n")
        self.assertIn("already approved", self.run_cli(["update", "."], cwd=str(proj)).stdout)
        self.assertIn("Nothing to approve here", self.run_cli(["update"], cwd=str(proj)).stdout)

        # Reordered keys and reformatting change nothing
        data_reordered = {"shortcuts": dict(reversed(list(data["shortcuts"].items()))), "version": 2}
        registry.write_text(json.dumps(data_reordered, indent=7), encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 0)

        # A change to the JSON blocks, also the shortcuts that didn't change
        data["shortcuts"]["there"]["inline"] = "echo changed"
        registry.write_text(json.dumps(data), encoding="utf-8")
        res = self.run_cli(["r", "hi"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"{proj} changed since you approved it; run: shortcut update", res.stderr)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn('-    "there": {', res.stdout.replace('-      "inline": "echo there"', '-    "there": {'))
        self.assertIn('+      "inline": "echo changed"', res.stdout)
        self.assertNotIn('+    "version"', res.stdout)

        # So does a change to a script
        script.write_text("#!/usr/bin/env bash\necho evil\n", encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("--- a/.shortcuts/scripts/hi.sh", res.stdout)
        self.assertIn("-echo hi", res.stdout)
        self.assertIn("+echo evil", res.stdout)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "evil\n")

        # A change made through shortcut counts too
        res = self.run_cli(["new", "more", "--local", "--", "echo more"], cwd=str(proj))
        self.assertIn("needs approving", res.stderr)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)

        # The script going away is a change, and can't be approved while the entry needs it
        self.allow(proj / ".shortcuts")
        script.unlink()
        self.assertEqual(self.run_cli(["r", "there"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("script not found", res.stderr)

    def test_approval_prompt_in_terminal_and_colour(self):
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"hi": {"inline": "echo hi"}})
        rc, out = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"approve? [y/N]", b"n\n")])
        self.assertEqual(rc, 1)
        self.assertIn("Aborted.", out)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)
        rc, out = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"approve? [y/N]", b"y\n")], env={"PAGER": ""})
        self.assertEqual(rc, 0)
        self.assertIn("\x1b[32m+", out)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "hi\n")
        self.assertNotIn("\x1b[", self.run_cli(["update", "."], cwd=str(proj)).stdout)

    def test_approval_is_kept_outside_what_it_approves(self):
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"hi": {"inline": "echo hi"}})
        before = {p for p in proj.rglob("*")}
        self.allow(proj / ".shortcuts")
        self.assertEqual({p for p in proj.rglob("*")} - before, {proj / ".shortcuts" / ".lock"} & {p for p in proj.rglob("*")})
        records = list((self.shortcuts_home / "approvals").glob("*.json"))
        self.assertEqual(len(records), 1)
        record = json.loads(records[0].read_text())
        self.assertEqual(record["key"], str(proj))
        self.assertIn(".shortcuts/shortcuts.json", record["snapshot"])

    def test_one_changed_group_leaves_the_others_running(self):
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"own": {"inline": "echo own"}})
        imports = proj / ".shortcuts" / "imports"
        imports.mkdir()
        docker, kube = imports / "docker.shortcut", imports / "kube.shortcut"
        docker.write_text('{"version": 2, "shortcuts": {"ps": {"inline": "echo ps"}}}', encoding="utf-8")
        kube.write_text('{"version": 2, "shortcuts": {"get": {"inline": "echo get"}}}', encoding="utf-8")
        self.allow(proj / ".shortcuts")

        docker.write_text('{"version": 2, "shortcuts": {"ps": {"inline": "echo evil"}}}', encoding="utf-8")
        res = self.run_cli(["r", "docker", "ps"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("docker changed since you approved it; run: shortcut update docker", res.stderr)
        self.assertEqual(self.run_cli(["r", "kube", "get"], cwd=str(proj)).stdout, "get\n")
        self.assertEqual(self.run_cli(["r", "own"], cwd=str(proj)).stdout, "own\n")

        self.assertEqual(self.run_cli(["update", "docker", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "ps"], cwd=str(proj)).stdout, "evil\n")

    def test_diff_details(self):
        old = {"a.txt": "one\ntwo\n", "gone.txt": "bye\n", "same.txt": "x\n", "bin": shortcut_mod.BINARY_MARK + "aa"}
        new = {"a.txt": "one\nthree\n", "added.txt": "hi\n", "same.txt": "x\n", "bin": shortcut_mod.BINARY_MARK + "bb"}
        lines = shortcut_mod.diff_files(old, new)
        self.assertIn("--- a/a.txt", lines)
        self.assertIn("+++ b/a.txt", lines)
        self.assertIn("-two", lines)
        self.assertIn("+three", lines)
        self.assertIn("-bye", lines)
        self.assertIn("+hi", lines)
        self.assertIn("Binary file bin changed", lines)
        self.assertFalse([line for line in lines if "same.txt" in line])

        # A new empty file is still a difference you can see
        self.assertTrue(shortcut_mod.diff_files({}, {"empty": ""}))

    def test_non_utf8_script(self):
        proj = self.tmp_path / "proj"
        scripts = proj / "bin"
        scripts.mkdir(parents=True)
        (scripts / "tool").write_bytes(b"#!/bin/sh\n# \xff\xfe\necho one\n")
        self.write_store(proj / ".shortcuts", {"tool": {"script": "bin/tool"}})
        self.allow(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["r", "tool"], cwd=str(proj)).stdout, "one\n")
        (scripts / "tool").write_bytes(b"#!/bin/sh\n# \xff\xfd\necho two\n")
        self.assertEqual(self.run_cli(["r", "tool"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("Binary file bin/tool changed", res.stdout)

    def test_escape_codes_in_a_diff_cannot_touch_the_terminal(self):
        proj = self.tmp_path / "proj"
        (proj / "bin").mkdir(parents=True)
        (proj / "bin" / "tool").write_text("echo \x1b[2Jhidden\r\n", encoding="utf-8")
        self.write_store(proj / ".shortcuts", {"tool": {"script": "bin/tool"}})
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertNotIn("\x1b", res.stdout)
        self.assertIn("^[[2Jhidden", res.stdout)

    def test_broken_project_never_stops_core_commands_or_unrelated_global_shortcuts(self):
        self.run_cli(["new", "outside", "--", "echo outside"])
        proj = self.tmp_path / "proj"
        (proj / ".shortcuts").mkdir(parents=True)
        (proj / ".shortcuts" / "shortcuts.json").write_text("{not json", encoding="utf-8")

        res = self.run_cli(["r", "outside"], cwd=str(proj))
        self.assertEqual(res.stdout, "outside\n")
        self.assertIn("so this is the global 'outside'", res.stderr)
        self.assertIn("outside", self.run_cli(["list", "--names"], cwd=str(proj)).stdout.split())
        self.assertIn("corrupt registry", self.run_cli(["list"], cwd=str(proj)).stderr)
        report = self.run_cli(["fix"], cwd=str(proj))
        self.assertEqual(report.returncode, 0)
        self.assertIn("BROKEN: corrupt registry", report.stdout)
        self.assertEqual(self.run_cli(["update"], cwd=str(proj)).returncode, 1)

    def test_broken_project_refuses_a_name_it_had_when_approved(self):
        self.run_cli(["new", "build", "--", "echo global"])
        self.run_cli(["new", "other", "--", "echo other"])
        proj = self.tmp_path / "proj"
        self.write_store(proj / ".shortcuts", {"build": {"inline": "echo project"}})
        self.allow(proj / ".shortcuts")
        registry = proj / ".shortcuts" / "shortcuts.json"
        good = registry.read_text(encoding="utf-8")
        registry.write_text("<<<<<<< HEAD\n" + good, encoding="utf-8")

        res = self.run_cli(["r", "build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertEqual(res.stdout, "")
        self.assertIn("corrupt registry", res.stderr)
        self.assertIn("shortcut run --global build", res.stderr)
        self.assertEqual(self.run_cli(["r", "--global", "build"], cwd=str(proj)).stdout, "global\n")

        res = self.run_cli(["r", "other"], cwd=str(proj))
        self.assertEqual(res.stdout, "other\n")
        self.assertIn("so this is the global 'other'", res.stderr)

        registry.write_text(good, encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "build"], cwd=str(proj)).stdout, "project\n")

    def test_broken_project_refuses_a_namespace_it_imported(self):
        self.run_cli(["new", "docker", "--", "echo global"])
        proj = self.tmp_path / "proj"
        self.write_import(proj / ".shortcuts", "docker", {"version": 2, "shortcuts": {"ps": {"inline": "true"}}})
        tools = proj / "tools"
        tools.mkdir()
        (proj / ".shortcuts" / "imports" / "docker.shortcut").rename(tools / "docker.shortcut")
        registry = proj / ".shortcuts" / "shortcuts.json"
        registry.write_text(json.dumps({"version": 2, "imports": {"docker": "../tools/docker.shortcut"}, "shortcuts": {}}))
        self.assertEqual(self.run_cli(["update", ".", "-y"], cwd=str(proj)).returncode, 0)
        registry.write_text("{not json", encoding="utf-8")

        res = self.run_cli(["r", "docker"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("shortcut run --global docker", res.stderr)

    def test_broken_project_does_not_call_its_own_namespace_global(self):
        proj = self.tmp_path / "proj"
        self.write_import(proj / ".shortcuts", "docker", {"version": 2, "shortcuts": {"ps": {"inline": "true"}}})
        (proj / ".shortcuts" / "shortcuts.json").write_text("{not json", encoding="utf-8")
        res = self.run_cli(["show", "docker", "ps"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("so this is the global", res.stderr)

    def test_broken_project_warns_before_rm_reaches_the_global_one(self):
        self.run_cli(["new", "build", "--", "true"])
        proj = self.tmp_path / "proj"
        (proj / ".shortcuts").mkdir(parents=True)
        (proj / ".shortcuts" / "shortcuts.json").write_text("{not json", encoding="utf-8")
        self.assertIn("so this is the global 'build'", self.run_cli(["rm", "build", "-y"], cwd=str(proj)).stderr)

    def test_broken_global_store_still_reports(self):
        (self.shortcuts_home / "shortcuts.json").write_text("{not json", encoding="utf-8")
        res = self.run_cli(["r", "anything"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("corrupt registry", res.stderr)
        self.assertNotIn("Traceback", res.stderr)
        self.assertIn("BROKEN: corrupt registry", self.run_cli(["fix", "anything"]).stdout)

    def test_migrate_converts_names_prefixes_and_dir(self):
        fixed = self.tmp_path / "fixed"
        fixed.mkdir()
        applications = self.xdg_data_home / "applications"
        applications.mkdir()
        (applications / "shortcut-g_old.desktop").write_text("[Desktop Entry]\n", encoding="utf-8")
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps({
            "version": 1,
            "shortcuts": {
                "g_search": {"description": "s", "inline": "pwd"},
                "glob_two": {"description": "t", "inline": "pwd"},
                "global_three": {"description": "t", "inline": "pwd"},
                "g_old": {"description": "o", "inline": "pwd"},
                "g_taken": {"description": "new", "inline": "echo new"},
                "taken": {"description": "existing", "inline": "echo existing"},
                "g_": {"description": "nothing left", "inline": "true"},
                "penpot": {"description": "p", "inline": "pwd", "dir": str(fixed)},
                "C_build": {"description": "b", "inline": "pwd"},
            },
            "prefixes": {"C_": str(fixed)},
        }), encoding="utf-8")

        res = self.run_cli(["migrate"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("g_search -> search (cwd: caller)", res.stdout)
        self.assertIn("skipped g_taken (taken already exists)", res.stdout)
        self.assertIn("shortcut r desktop old", res.stdout)
        data = json.loads((self.shortcuts_home / "shortcuts.json").read_text())
        reg = data["shortcuts"]
        self.assertEqual(data["version"], 2)
        self.assertNotIn("prefixes", data)
        self.assertEqual(reg["search"]["cwd"], "caller")
        self.assertIn("two", reg)
        self.assertIn("three", reg)
        self.assertNotIn("g_search", reg)
        self.assertEqual(reg["penpot"]["cwd"], str(fixed))
        self.assertNotIn("dir", reg["penpot"])
        self.assertEqual(reg["C_build"]["cwd"], str(fixed))

        # Collisions don't overwrite: both are still there, as they were
        self.assertEqual(reg["taken"]["inline"], "echo existing")
        self.assertEqual(reg["g_taken"]["inline"], "echo new")
        self.assertIn("g_", reg)

        self.assertEqual(self.run_cli(["r", "penpot"]).stdout, f"{fixed}\n")
        second = self.run_cli(["migrate"])
        self.assertEqual(json.loads((self.shortcuts_home / "shortcuts.json").read_text()), data)
        self.assertIn("skipped g_taken", second.stdout)

    def test_migrate_project_keeps_where_things_ran(self):
        proj = self.tmp_path / "proj"
        (proj / "sub").mkdir(parents=True)
        (proj / ".shortcuts").mkdir()
        registry = proj / ".shortcuts" / "shortcuts.json"
        registry.write_text(json.dumps({"version": 1, "shortcuts": {
            "root": {"description": "r", "inline": "pwd"},
            "sub": {"description": "s", "inline": "pwd", "dir": "sub"},
            "g_here": {"description": "h", "inline": "pwd"},
        }}), encoding="utf-8")

        res = self.run_cli(["r", "sub"], cwd=str(proj))
        self.assertIn("still uses the old 'dir' field; convert it with: shortcut migrate", res.stderr)

        res = self.run_cli(["migrate"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("needs approving", res.stderr)
        self.allow(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["r", "root"], cwd=str(proj / "sub")).stdout, f"{proj}\n")
        self.assertEqual(self.run_cli(["r", "sub"], cwd=str(proj)).stdout, f"{proj / 'sub'}\n")
        self.assertEqual(self.run_cli(["r", "here"], cwd=str(proj / "sub")).stdout, f"{proj / 'sub'}\n")

        # A 2.0 store is left alone: no 'cwd' there means caller
        fresh = self.tmp_path / "fresh"
        self.write_store(fresh / ".shortcuts", {"mine": {"inline": "pwd"}})
        self.run_cli(["migrate"], cwd=str(fresh))
        self.assertNotIn("cwd", json.loads((fresh / ".shortcuts" / "shortcuts.json").read_text())["shortcuts"]["mine"])

    def test_bundled_nano_log_and_desktop(self):
        self.install_bundled()
        self.run_cli(["new", "job", "--desc", "A job", "--", "echo job output"])
        self.run_cli(["r", "job"])

        res = self.run_cli(["r", "log", "job"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("job output", res.stdout)
        self.assertIn("=== exit: 0 ===", res.stdout)
        res = self.run_cli(["r", "log", "unrun"])
        self.assertEqual(res.returncode, 1)
        self.run_cli(["new", "unrun", "--", "true"])
        res = self.run_cli(["r", "log", "unrun"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("no log yet", res.stderr)

        # nano wins over whatever editor is configured, and finds what a fixed folder couldn't
        fake = self.tool_bin / "nano"
        fake.write_text('#!/bin/sh\necho "nano opened $1"\n', encoding="utf-8")
        fake.chmod(0o755)
        res = self.run_cli(["r", "nano", "job"], env={"VISUAL": "false", "EDITOR": "false"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("nano opened", res.stdout)

        res = self.run_cli(["r", "desktop"])
        self.assertEqual(res.returncode, 2)
        self.assertIn("usage: shortcut r desktop <shortcut>", res.stderr)

    def test_bundled_desktop_entry_quoting(self):
        self.install_bundled()
        res = self.run_cli(["new", "desk_test", "--desc", 'Run 100% "special" `app` with $VAR', "--", "echo", "desktop_ok"])
        self.assertEqual(res.returncode, 0)
        res_desk = self.run_cli(["r", "desktop", "desk_test"])
        self.assertEqual(res_desk.returncode, 0, res_desk.stderr)
        self.assertIn("Created desktop entry", res_desk.stdout)

        desktop_file = self.xdg_data_home / "applications" / "shortcut-desk_test.desktop"
        lines = desktop_file.read_text(encoding="utf-8").splitlines()
        self.assertEqual(lines[0], "[Desktop Entry]")
        self.assertIn('Name=Run 100% "special" `app` with $VAR', lines)
        self.assertIn("Icon=utilities-terminal", lines)
        self.assertIn("Terminal=true", lines)
        exec_line = next(line for line in lines if line.startswith("Exec="))
        self.assertIn(f"{self.tool_bin}/shortcut run --global desk_test", exec_line)
        self.assertIn('\\"Press Enter to close...\\"', exec_line)
        self.assertIn("rc=\\$?", exec_line)
        self.assertIn("\\$rc", exec_line)

    def test_bundled_desktop_refuses_a_project_shortcut(self):
        self.install_bundled()
        proj = self.tmp_path / "proj"
        proj.mkdir()
        self.run_cli(["new", "local_desk", "--local", "--", "echo 1"], cwd=str(proj))
        res = self.run_cli(["r", "desktop", "local_desk"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("not found", res.stdout)  # a logged run merges stderr into stdout
        self.assertFalse((self.xdg_data_home / "applications" / "shortcut-local_desk.desktop").exists())

    def test_removed_commands_are_gone(self):
        for word in ("allow", "prefix", "log", "desktop", "nano", "activate", "version"):
            res = self.run_cli([word, "x"])
            self.assertEqual(res.returncode, 2)
            self.assertEqual(res.stderr, f"unknown command '{word}' (to run a shortcut: shortcut run {word})\n")
        self.assertEqual(self.run_cli(["init", "-g"]).returncode, 2)

    def test_an_entry_with_both_inline_and_script_is_refused(self):
        script = self.shortcuts_home / "both.sh"
        script.write_text("#!/usr/bin/env bash\necho script\n", encoding="utf-8")
        reg = {"version": 2, "shortcuts": {"both": {"description": "x", "inline": "echo inline", "script": "both.sh"}}}
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")
        for words in (["r", "both"], ["show", "both"], ["location", "both"], ["edit", "both"]):
            res = self.run_cli(words, env={"SHORTCUT_EDITOR_CMD": "true"})
            self.assertEqual(res.returncode, 1, words)
            self.assertIn("exactly one of 'inline' and 'script'", res.stderr)
            self.assertNotIn("inline\n", res.stdout)
        self.assertEqual(self.run_cli(["rm", "both", "-y"]).returncode, 0)
        self.assertNotIn("both", self.load_global())

    def test_new_in_a_namespace_names_it_in_the_edit_hint(self):
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--desc", "d"])
        self.assertIn("Edit it with: shortcut edit docker ps  (file:", res.stdout)

    def test_unknown_top_level_field_stops_a_global_shortcut(self):
        self.run_cli(["new", "hi", "--", "echo ran"])
        reg = json.loads((self.shortcuts_home / "shortcuts.json").read_text(encoding="utf-8"))
        reg["future_safety"] = True
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")
        res = self.run_cli(["r", "hi"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("unknown field: future_safety", res.stderr)
        self.assertNotIn("ran", res.stdout)
        self.assertEqual(self.run_cli(["rm", "hi", "-y"]).returncode, 0)

    def test_declining_rm_exits_1(self):
        self.run_cli(["new", "keep", "--", "true"])
        rc, out = self.run_in_terminal(["rm", "keep"], keys=[(b"[y/N]", b"n\n")])
        self.assertEqual(rc, 1)
        self.assertIn("Aborted.", out)
        self.assertIn("keep", self.load_global())

    def test_fix_global_without_a_name_skips_the_project(self):
        proj = self.tmp_path / "thisproject"
        self.write_store(proj / ".shortcuts", {"hi": {"inline": "echo hi"}})
        self.assertIn("thisproject", self.run_cli(["fix"], cwd=str(proj)).stdout)
        self.assertNotIn("thisproject", self.run_cli(["fix", "--global"], cwd=str(proj)).stdout)


if __name__ == "__main__":
    unittest.main()
