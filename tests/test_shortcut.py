#!/usr/bin/env python3

import importlib.machinery
import importlib.util
import fcntl
import json
import os
import pathlib
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import unittest
from typing import Any, Optional

SHORTCUT = pathlib.Path(__file__).resolve().parent.parent / "shortcut"

loader = importlib.machinery.SourceFileLoader("shortcut_mod", str(SHORTCUT))
spec = importlib.util.spec_from_loader("shortcut_mod", loader)
if spec is None or spec.loader is None:
    raise ImportError(f"Cannot load {SHORTCUT}")
shortcut_mod = importlib.util.module_from_spec(spec)
sys.modules["shortcut_mod"] = shortcut_mod
loader.exec_module(shortcut_mod)


class TestShortcutT1(unittest.TestCase):
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
        self.assertIn("Corrupt registry", str(ctx.exception))

        with self.assertRaises(ValueError) as ctx2:
            shortcut_mod.save_registry_atomic(str(self.shortcuts_home), data)
        self.assertIn("Cannot overwrite corrupt registry", str(ctx2.exception))

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
        # Global store has static shortcut 'build' and global shortcut 'g_check'
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {
                "version": 1,
                "shortcuts": {
                    "build": {
                        "description": "Global static build",
                        "inline": "echo global_build",
                        "dir": str(self.tmp_path),
                    },
                    "g_check": {
                        "description": "Global check",
                        "inline": "echo global_check",
                    },
                },
            },
        )

        # Project local store has local shortcut 'build'
        proj_dir = self.home_dir / "my_project"
        proj_shortcuts = proj_dir / ".shortcuts"
        proj_shortcuts.mkdir(parents=True)
        shortcut_mod.save_registry_atomic(
            str(proj_shortcuts),
            {
                "version": 1,
                "shortcuts": {
                    "build": {
                        "description": "Local project build",
                        "inline": "echo local_build",
                    }
                },
            },
        )

        # Inside project: local precedence
        res_local = shortcut_mod.resolve_shortcut("build", caller_cwd=str(proj_dir))
        self.assertEqual(res_local.scope, "local")
        self.assertEqual(res_local.store_dir, str(proj_shortcuts.resolve()))
        self.assertEqual(res_local.run_cwd, str(proj_dir.resolve()))
        self.assertEqual(res_local.entry.description, "Local project build")

        # Outside project: global static fallback
        res_static = shortcut_mod.resolve_shortcut("build", caller_cwd=str(self.home_dir))
        self.assertEqual(res_static.scope, "static")
        self.assertEqual(res_static.store_dir, str(self.shortcuts_home.resolve()))
        self.assertEqual(res_static.run_cwd, str(self.tmp_path.resolve()))
        self.assertEqual(res_static.entry.description, "Global static build")

        # Failure hints: check 'check' (without 'g_') hints 'g_check'
        with self.assertRaises(KeyError) as ctx:
            shortcut_mod.resolve_shortcut("check", caller_cwd=str(self.home_dir))
        self.assertIn("Did you mean 'g_check'?", str(ctx.exception))

        res_cli = self.run_cli(["check"], cwd=str(self.home_dir))
        self.assertNotEqual(res_cli.returncode, 0)
        self.assertIn("Did you mean 'g_check'?", res_cli.stderr)

    def test_run_inline_with_passthrough_args(self):
        res_new = self.run_cli(["new", "g_echo", "--", "echo name=$0 first=$1 all=$*"])
        self.assertEqual(res_new.returncode, 0)

        res = self.run_cli(["g_echo", "-v", "--option", "arg1", "arg2"])
        self.assertEqual(res.returncode, 0)
        self.assertEqual(res.stdout.strip(), "name=g_echo first=-v all=-v --option arg1 arg2")

        res_run = self.run_cli(["run", "g_echo", "-v", "--option", "arg1", "arg2"])
        self.assertEqual(res_run.returncode, 0)
        self.assertEqual(res_run.stdout.strip(), "name=g_echo first=-v all=-v --option arg1 arg2")

    def test_run_script_tee_logging_and_exit_code(self):
        script_file = self.tmp_path / "script_fail.sh"
        script_file.write_text("#!/usr/bin/env bash\necho stdout line\necho stderr line >&2\nexit 42\n", encoding="utf-8")
        script_file.chmod(0o755)

        res_add = self.run_cli(["add", "g_fail42", str(script_file), "--desc", "Fails with 42"])
        self.assertEqual(res_add.returncode, 0)

        res = self.run_cli(["run", "g_fail42"])
        self.assertEqual(res.returncode, 42)
        self.assertIn("stdout line", res.stdout)
        self.assertIn("stderr line", res.stdout)

        log_path = self.shortcuts_home / "logs" / "g_fail42.log"
        self.assertTrue(log_path.is_file())
        log_content = log_path.read_text(encoding="utf-8")
        self.assertTrue(log_content.startswith("=== SHORTCUT RUN: g_fail42 |"))
        self.assertIn("stdout line", log_content)
        self.assertIn("stderr line", log_content)
        self.assertTrue(log_content.rstrip().endswith("=== exit: 42 ==="))

        res_log = self.run_cli(["log", "g_fail42"])
        self.assertEqual(res_log.returncode, 0)
        self.assertIn("=== exit: 42 ===", res_log.stdout)

    def test_add_and_link_paths(self):
        # 1. Add copy from file
        src_file = self.tmp_path / "source.sh"
        src_file.write_text("#!/usr/bin/env bash\necho source\n", encoding="utf-8")
        src_file.chmod(0o644)

        res_add = self.run_cli(["add", "g_copied", str(src_file), "--desc", "Copied script"])
        self.assertEqual(res_add.returncode, 0)

        copied_path = self.shortcuts_home / "scripts" / "g_copied.sh"
        self.assertTrue(copied_path.is_file())
        self.assertTrue(os.access(copied_path, os.X_OK))
        self.assertTrue(src_file.is_file())  # original untouched

        # 2. Add from stdin
        res_pipe = self.run_cli(
            ["add", "g_piped", "-", "--desc", "Piped script"],
            input="#!/usr/bin/env bash\necho piped\n",
        )
        self.assertEqual(res_pipe.returncode, 0)
        piped_path = self.shortcuts_home / "scripts" / "g_piped.sh"
        self.assertTrue(piped_path.is_file())
        self.assertEqual(piped_path.read_text(encoding="utf-8"), "#!/usr/bin/env bash\necho piped\n")

        # 3. Link absolute external file
        ext_file = self.tmp_path / "external.sh"
        ext_file.write_text("#!/usr/bin/env bash\necho external\n", encoding="utf-8")
        ext_file.chmod(0o755)

        res_link = self.run_cli(["link", "g_linked", str(ext_file), "--desc", "Linked script"])
        self.assertEqual(res_link.returncode, 0)
        self.assertFalse((self.shortcuts_home / "scripts" / "g_linked.sh").exists())

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["g_linked"]["script"], str(ext_file.resolve()))

        # 4. Path containment check
        with self.assertRaises(ValueError) as ctx:
            shortcut_mod.resolve_script_path(str(self.shortcuts_home), "../../../etc/passwd")
        self.assertIn("Path traversal detected", str(ctx.exception))

    def test_rm_interactive_and_force(self):
        script_file = self.tmp_path / "stored.sh"
        script_file.write_text("#!/usr/bin/env bash\necho stored\n", encoding="utf-8")
        script_file.chmod(0o755)
        self.run_cli(["add", "g_stored", str(script_file), "--desc", "Stored shortcut"])
        self.run_cli(["run", "g_stored"])

        log_path = self.shortcuts_home / "logs" / "g_stored.log"
        stored_path = self.shortcuts_home / "scripts" / "g_stored.sh"
        self.assertTrue(log_path.is_file())
        self.assertTrue(stored_path.is_file())

        ext_file = self.tmp_path / "external_keep.sh"
        ext_file.write_text("#!/usr/bin/env bash\necho ext\n", encoding="utf-8")
        ext_file.chmod(0o755)
        self.run_cli(["link", "g_linked", str(ext_file), "--desc", "Linked shortcut"])

        # 1. Non-tty without -y should fail
        res_no_y = self.run_cli(["rm", "g_stored"])
        self.assertNotEqual(res_no_y.returncode, 0)
        self.assertIn("without -y", res_no_y.stderr)
        self.assertTrue(stored_path.is_file())
        self.assertTrue(log_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("g_stored", reg["shortcuts"])

        # 2. With -y, stored copy, log, and registry entry are deleted
        res_y = self.run_cli(["rm", "g_stored", "-y"])
        self.assertEqual(res_y.returncode, 0)
        self.assertFalse(stored_path.is_file())
        self.assertFalse(log_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("g_stored", reg["shortcuts"])

        # 3. Linked file is preserved when shortcut is removed with -y
        res_link_rm = self.run_cli(["rm", "g_linked", "-y"])
        self.assertEqual(res_link_rm.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("g_linked", reg["shortcuts"])
        self.assertTrue(ext_file.is_file(), "Linked external script must never be deleted!")

    def test_nano_inline_modification_roundtrip(self):
        res = self.run_cli(["new", "g_inline1", "--", "echo original"])
        self.assertEqual(res.returncode, 0)

        # 1. Edit modifying content (and asserting temp file suffix)
        editor_mod = (
            f'{sys.executable} -c "import sys, os; '
            f'path = sys.argv[1]; '
            f'assert os.path.isfile(path); '
            f'assert path.endswith(\'-g_inline1.sh\'); '
            f'open(path, \'w\').write(\'echo modified\\n\')"'
        )
        res_edit = self.run_cli(["nano", "g_inline1"], env={"SHORTCUT_EDITOR_CMD": editor_mod})
        self.assertEqual(res_edit.returncode, 0)
        self.assertIn("Updated inline shortcut 'g_inline1'.", res_edit.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["g_inline1"]["inline"], "echo modified")

        # 2. Edit without modifying content
        res_noop = self.run_cli(["nano", "g_inline1"], env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res_noop.returncode, 0)
        self.assertIn("No changes made to 'g_inline1'.", res_noop.stdout)

    def test_creation_scope_rules(self):
        # 1. Global-named rejects --static and --local
        res1 = self.run_cli(["new", "g_foo", "--static", str(self.tmp_path), "--", "echo 1"])
        self.assertNotEqual(res1.returncode, 0)
        self.assertIn("is global-named; --static/--local not allowed", res1.stderr)

        res2 = self.run_cli(["new", "g_foo", "--local", "--", "echo 1"])
        self.assertNotEqual(res2.returncode, 0)
        self.assertIn("is global-named; --static/--local not allowed", res2.stderr)

        # 2. Registered prefix rejects --static and --local
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {
                "version": 1,
                "shortcuts": {},
                "prefixes": {"P_": str(self.tmp_path)},
            },
        )
        res3 = self.run_cli(["new", "P_foo", "--static", str(self.tmp_path), "--", "echo 1"])
        self.assertNotEqual(res3.returncode, 0)
        self.assertIn("is prefix-named; --static/--local not allowed", res3.stderr)

        res4 = self.run_cli(["new", "P_foo", "--local", "--", "echo 1"])
        self.assertNotEqual(res4.returncode, 0)
        self.assertIn("is prefix-named; --static/--local not allowed", res4.stderr)

        # 3. --static and --local are mutually exclusive
        res5 = self.run_cli(["new", "unpref1", "--static", str(self.tmp_path), "--local", "--", "echo 1"])
        self.assertNotEqual(res5.returncode, 0)
        self.assertIn("--static and --local are mutually exclusive", res5.stderr)

        # 4. --static needs an existing dir and is stored absolute
        res6 = self.run_cli(["new", "unpref2", "--static", "/nonexistent/path/xyz", "--", "echo 1"])
        self.assertNotEqual(res6.returncode, 0)
        self.assertIn("--static dir does not exist", res6.stderr)

        res7 = self.run_cli(["new", "unpref_static", "--static", str(self.tmp_path), "--", "echo 1"])
        self.assertEqual(res7.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["unpref_static"]["dir"], str(self.tmp_path.resolve()))

        # 5. Unprefixed default creates ./.shortcuts/ with a stderr notice
        proj_dir = self.tmp_path / "fresh_proj"
        proj_dir.mkdir()
        res8 = self.run_cli(["new", "local_cmd", "--", "echo local"], cwd=str(proj_dir))
        self.assertEqual(res8.returncode, 0)
        self.assertIn("created local store", res8.stderr)
        self.assertIn("local_cmd", res8.stderr)
        self.assertTrue((proj_dir / ".shortcuts" / "shortcuts.json").is_file())

        # 6. Duplicate name error
        res9 = self.run_cli(["new", "local_cmd", "--", "echo duplicate"], cwd=str(proj_dir))
        self.assertNotEqual(res9.returncode, 0)
        self.assertIn("already exists in store", res9.stderr)

    def test_new_oneliner_and_template(self):
        # Inline oneliner: description defaults to command
        res1 = self.run_cli(["new", "g_hello", "--", "echo", "hello", "world"])
        self.assertEqual(res1.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["g_hello"]["inline"], "echo hello world")
        self.assertEqual(reg["shortcuts"]["g_hello"]["description"], "echo hello world")

        # Template script: description defaults to "(no description)"
        res2 = self.run_cli(["new", "g_template"], env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res2.returncode, 0)
        script_file = self.shortcuts_home / "scripts" / "g_template.sh"
        self.assertTrue(script_file.is_file())
        self.assertTrue(os.access(script_file, os.X_OK))
        content = script_file.read_text(encoding="utf-8")
        self.assertEqual(content, "#!/usr/bin/env bash\nset -euo pipefail\n\n")

        reg2 = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg2["shortcuts"]["g_template"]["description"], "(no description)")

    def test_run_ctrl_c_writes_footer(self):
        sleeper_script = self.tmp_path / "sleeper.sh"
        sleeper_script.write_text("#!/usr/bin/env bash\necho started\nsleep 10\n", encoding="utf-8")
        sleeper_script.chmod(0o755)

        res_add = self.run_cli(["add", "g_sleeper", str(sleeper_script), "--desc", "Sleeping script"])
        self.assertEqual(res_add.returncode, 0)

        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "g_sleeper"],
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

        log_path = self.shortcuts_home / "logs" / "g_sleeper.log"
        self.assertTrue(log_path.is_file())
        log_text = log_path.read_text(encoding="utf-8")
        self.assertTrue(log_text.rstrip().endswith("=== exit: 130 ==="), f"Footer missing in:\n{log_text}")

    def test_list_and_names(self):
        # Create a local store in proj_dir
        proj_dir = self.home_dir / "list_proj"
        proj_dir.mkdir()
        self.run_cli(["new", "local_cmd", "--", "echo local"], cwd=str(proj_dir))

        # Create global shortcuts and prefix in global store
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {
                "version": 1,
                "shortcuts": {
                    "g_glob": {"description": "Global desc", "inline": "echo 1"},
                    "penpot": {"description": "Static desc", "inline": "echo 2", "dir": str(self.tmp_path)},
                },
                "prefixes": {
                    "C_": "/C"
                }
            }
        )

        # list without --names inside project
        res_list = self.run_cli(["list"], cwd=str(proj_dir))
        self.assertEqual(res_list.returncode, 0)
        self.assertIn("Local shortcuts", res_list.stdout)
        self.assertIn("local_cmd", res_list.stdout)
        self.assertIn("Global shortcuts", res_list.stdout)
        self.assertIn("g_glob", res_list.stdout)
        self.assertIn("penpot", res_list.stdout)
        self.assertIn("Prefixes", res_list.stdout)
        self.assertIn("C_", res_list.stdout)

        # list --names
        res_names = self.run_cli(["list", "--names"], cwd=str(proj_dir))
        self.assertEqual(res_names.returncode, 0)
        names = res_names.stdout.strip().splitlines()
        self.assertEqual(names, ["local_cmd", "g_glob", "penpot"])

    def test_show_and_location(self):
        self.run_cli(["new", "g_inline", "--desc", "Inline item", "--", "echo hello"])
        script_file = self.tmp_path / "show_script.sh"
        script_file.write_text("#!/usr/bin/env bash\necho test\n", encoding="utf-8")
        script_file.chmod(0o755)
        self.run_cli(["add", "g_stored", str(script_file), "--desc", "Stored item"])

        # show
        res_show_inline = self.run_cli(["show", "g_inline"])
        self.assertEqual(res_show_inline.returncode, 0)
        self.assertIn("Name:        g_inline", res_show_inline.stdout)
        self.assertIn("echo hello", res_show_inline.stdout)

        res_show_script = self.run_cli(["show", "g_stored"])
        self.assertEqual(res_show_script.returncode, 0)
        self.assertIn("Name:        g_stored", res_show_script.stdout)
        self.assertIn("echo test", res_show_script.stdout)

        # location
        res_loc_inline = self.run_cli(["location", "g_inline"])
        self.assertEqual(res_loc_inline.returncode, 0)
        self.assertIn("shortcuts.json (inline)", res_loc_inline.stdout)

        res_loc_script = self.run_cli(["location", "g_stored"])
        self.assertEqual(res_loc_script.returncode, 0)
        self.assertTrue(res_loc_script.stdout.strip().endswith("scripts/g_stored.sh"))

    def test_prefix_grammar_and_reserved_rejection(self):
        # 1. Invalid grammar
        for bad_p in ("123_", "foo", "_foo", "A_B_", "p"):
            res = self.run_cli(["prefix", "add", bad_p, str(self.tmp_path)])
            self.assertNotEqual(res.returncode, 0, f"Expected {bad_p} to fail grammar validation")
            self.assertIn("Invalid prefix", res.stderr)

        # 2. Reserved prefix rejection
        for reserved_p in ("g_", "glob_", "global_"):
            res = self.run_cli(["prefix", "add", reserved_p, str(self.tmp_path)])
            self.assertNotEqual(res.returncode, 0, f"Expected {reserved_p} to be rejected as reserved")
            self.assertIn("reserved", res.stderr)

        # 3. Non-existent dir rejection
        res_nodir = self.run_cli(["prefix", "add", "C_", "/nonexistent/prefix/dir/xyz"])
        self.assertNotEqual(res_nodir.returncode, 0)
        self.assertIn("Directory does not exist", res_nodir.stderr)

        # 4. Valid prefix registration
        valid_dir = self.tmp_path / "valid_prefix_dir"
        valid_dir.mkdir()
        res_ok = self.run_cli(["prefix", "add", "C_", str(valid_dir)])
        self.assertEqual(res_ok.returncode, 0)
        self.assertIn("Registered prefix 'C_'", res_ok.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg.get("prefixes", {}).get("C_"), str(valid_dir.resolve()))

        # 5. prefix list
        res_list = self.run_cli(["prefix", "list"])
        self.assertEqual(res_list.returncode, 0)
        self.assertIn("C_", res_list.stdout)
        self.assertIn(str(valid_dir.resolve()), res_list.stdout)

        # 6. prefix rm
        res_rm = self.run_cli(["prefix", "rm", "C_"])
        self.assertEqual(res_rm.returncode, 0)
        self.assertIn("Removed prefix 'C_'", res_rm.stdout)

        reg_after = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("C_", reg_after.get("prefixes", {}))

        # 7. prefix rm non-existent
        res_rm_bad = self.run_cli(["prefix", "rm", "C_"])
        self.assertNotEqual(res_rm_bad.returncode, 0)
        self.assertIn("not found", res_rm_bad.stderr)

    def test_prefix_longest_match_dispatch(self):
        dir_c = self.tmp_path / "c_dir"
        dir_cc = self.tmp_path / "cc_dir"
        dir_c.mkdir()
        dir_cc.mkdir()

        res1 = self.run_cli(["prefix", "add", "C_", str(dir_c)])
        self.assertEqual(res1.returncode, 0)
        res2 = self.run_cli(["prefix", "add", "CC_", str(dir_cc)])
        self.assertEqual(res2.returncode, 0)

        # Create shortcut CC_pwd
        self.run_cli(["new", "CC_pwd", "--", "pwd"])
        res_cc = self.run_cli(["CC_pwd"])
        self.assertEqual(res_cc.returncode, 0)
        self.assertEqual(res_cc.stdout.strip(), str(dir_cc.resolve()))

        # Create shortcut C_pwd
        self.run_cli(["new", "C_pwd", "--", "pwd"])
        res_c = self.run_cli(["C_pwd"])
        self.assertEqual(res_c.returncode, 0)
        self.assertEqual(res_c.stdout.strip(), str(dir_c.resolve()))

    def test_init_empty_local_store_creation(self):
        proj_dir = self.tmp_path / "empty_init_proj"
        proj_dir.mkdir()

        res = self.run_cli(["init"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        self.assertIn("Initialized empty local store", res.stdout)

        store_json = proj_dir / ".shortcuts" / "shortcuts.json"
        self.assertTrue(store_json.is_file())
        data = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertEqual(data, {"version": 1, "shortcuts": {}})

    def test_init_source_local_merge_skips(self):
        proj_dir = self.tmp_path / "local_merge_proj"
        proj_dir.mkdir()

        # Existing shortcut in destination
        self.run_cli(["new", "collide_me", "--", "echo existing"], cwd=str(proj_dir))

        # Register P_ in global store
        pref_dir = self.tmp_path / "pref_target"
        pref_dir.mkdir()
        self.run_cli(["prefix", "add", "P_", str(pref_dir)])

        # External script to link
        ext_script = self.tmp_path / "external_source.sh"
        ext_script.write_text("#!/usr/bin/env bash\necho ext\n", encoding="utf-8")
        ext_script.chmod(0o755)

        # Build source store
        src_repo = self.tmp_path / "source_repo"
        src_scripts = src_repo / "scripts"
        src_scripts.mkdir(parents=True)

        valid_script = src_scripts / "valid_item.sh"
        valid_script.write_text("#!/usr/bin/env bash\necho valid\n", encoding="utf-8")
        valid_script.chmod(0o755)

        source_data = {
            "version": 1,
            "shortcuts": {
                "collide_me": {"description": "collision", "inline": "echo collide"},
                "ext_linked": {"description": "linked", "script": str(ext_script.resolve())},
                "g_glob": {"description": "global prefixed", "inline": "echo glob"},
                "P_pref": {"description": "prefix registered", "inline": "echo pref"},
                "valid_item": {"description": "Valid local item", "script": "scripts/valid_item.sh"},
            },
        }
        shortcut_mod.save_registry_atomic(str(src_repo), source_data)

        # Run local init from source_repo
        res_init = self.run_cli(["init", str(src_repo)], cwd=str(proj_dir))
        self.assertEqual(res_init.returncode, 0)

        # Verify stdout reports skipped entries
        self.assertIn("collide_me (already exists)", res_init.stdout)
        self.assertIn("ext_linked (linked external script)", res_init.stdout)
        self.assertIn("g_glob (global-prefixed name in local init)", res_init.stdout)
        self.assertIn("P_pref (registered prefix name in local init)", res_init.stdout)

        # Verify stdout reports imported entry
        self.assertIn("valid_item", res_init.stdout)
        self.assertIn("review with: shortcut show <name>", res_init.stdout)

        # Verify imported script was copied and is executable
        dest_script = proj_dir / ".shortcuts" / "scripts" / "valid_item.sh"
        self.assertTrue(dest_script.is_file())
        self.assertTrue(os.access(dest_script, os.X_OK))

        # Verify destination registry
        dest_reg = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertIn("valid_item", dest_reg["shortcuts"])
        self.assertNotIn("g_glob", dest_reg["shortcuts"])

    def test_init_source_global_dir_retention(self):
        # Register prefix C_
        c_dir = self.tmp_path / "c_target"
        c_dir.mkdir()
        self.run_cli(["prefix", "add", "C_", str(c_dir)])

        # Prepare source
        src_repo = self.tmp_path / "global_source_repo"
        src_scripts = src_repo / "scripts"
        src_scripts.mkdir(parents=True)

        existing_static_dir = self.tmp_path / "static_dir"
        existing_static_dir.mkdir()

        source_data = {
            "version": 1,
            "shortcuts": {
                "static_ok": {
                    "description": "Static ok",
                    "inline": "echo ok",
                    "dir": str(existing_static_dir.resolve()),
                },
                "static_bad": {
                    "description": "Static missing dir",
                    "inline": "echo bad",
                    "dir": "/nonexistent/static/dir/xyz",
                },
                "C_registered": {
                    "description": "Registered prefix",
                    "inline": "echo c",
                },
                "U_unregistered": {
                    "description": "Unregistered prefix",
                    "inline": "echo u",
                },
                "g_traversal": {
                    "description": "Path traversal script",
                    "script": "../../outside.sh",
                },
                "local_with_dir": {
                    "description": "Local with dir",
                    "inline": "echo local",
                    "dir": str(existing_static_dir.resolve()),
                },
            },
        }
        shortcut_mod.save_registry_atomic(str(src_repo), source_data)

        # 1. init --global
        res_global = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res_global.returncode, 0)

        self.assertIn("static_bad (static dir missing or not found here", res_global.stdout)
        self.assertIn("U_unregistered (prefix not registered here", res_global.stdout)
        self.assertIn("g_traversal (script path escapes the source", res_global.stdout)
        self.assertIn("static_ok", res_global.stdout)
        self.assertIn("C_registered", res_global.stdout)

        global_reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("static_ok", global_reg["shortcuts"])
        self.assertEqual(global_reg["shortcuts"]["static_ok"]["dir"], str(existing_static_dir.resolve()))
        self.assertNotIn("static_bad", global_reg["shortcuts"])
        self.assertIn("C_registered", global_reg["shortcuts"])
        self.assertNotIn("U_unregistered", global_reg["shortcuts"])
        self.assertNotIn("g_traversal", global_reg["shortcuts"])

        # 2. Local init drops dir with note
        proj_dir = self.tmp_path / "local_drop_dir_proj"
        proj_dir.mkdir()

        # Isolate source with just local_with_dir
        src_local = self.tmp_path / "src_local"
        shortcut_mod.save_registry_atomic(
            str(src_local),
            {
                "version": 1,
                "shortcuts": {
                    "local_with_dir": {
                        "description": "Local with dir",
                        "inline": "echo local",
                        "dir": str(existing_static_dir.resolve()),
                    }
                },
            },
        )

        res_local = self.run_cli(["init", str(src_local)], cwd=str(proj_dir))
        self.assertEqual(res_local.returncode, 0)
        self.assertIn("local_with_dir (dir dropped: local shortcuts run at the project root)", res_local.stdout)

        local_reg = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertIn("local_with_dir", local_reg["shortcuts"])
        self.assertNotIn("dir", local_reg["shortcuts"]["local_with_dir"])

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
        self.assertIn("penpot.sh -> g_penpot (Start Penpot (http://localhost:9001))", res_mig.stdout)
        self.assertIn("raw.sh -> g_raw ((migrated))", res_mig.stdout)

        # Original flat files must be moved
        self.assertFalse(penpot_script.exists())
        self.assertFalse(no_comment_script.exists())

        # New files in scripts/
        migrated_penpot = self.shortcuts_home / "scripts" / "g_penpot.sh"
        migrated_raw = self.shortcuts_home / "scripts" / "g_raw.sh"
        self.assertTrue(migrated_penpot.is_file())
        self.assertTrue(migrated_raw.is_file())
        self.assertTrue(os.access(migrated_penpot, os.X_OK))

        # Check registry
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("g_penpot", reg["shortcuts"])
        self.assertEqual(reg["shortcuts"]["g_penpot"]["description"], "Start Penpot (http://localhost:9001)")
        self.assertEqual(reg["shortcuts"]["g_penpot"]["script"], "scripts/g_penpot.sh")

        self.assertIn("g_raw", reg["shortcuts"])
        self.assertEqual(reg["shortcuts"]["g_raw"]["description"], "(migrated)")
        self.assertEqual(reg["shortcuts"]["g_raw"]["script"], "scripts/g_raw.sh")

        # Run migrated shortcut
        res_run = self.run_cli(["g_penpot"])
        self.assertEqual(res_run.returncode, 0)
        self.assertIn("penpot_running", res_run.stdout)

    def test_migrate_idempotent(self):
        script = self.shortcuts_home / "job.sh"
        script.write_text("#!/usr/bin/env bash\n# Run job\necho job\n", encoding="utf-8")
        script.chmod(0o755)

        res1 = self.run_cli(["migrate"])
        self.assertEqual(res1.returncode, 0)
        self.assertIn("job.sh -> g_job", res1.stdout)

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
        subprocess.run(["git", "-C", str(git_dir), "commit", "-m", "initial commit"], check=True)

        proj_dir = self.tmp_path / "git_import_proj"
        proj_dir.mkdir()

        res = self.run_cli(["init", f"file://{git_dir.resolve()}"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        self.assertIn("git_cmd", res.stdout)
        self.assertTrue((proj_dir / ".shortcuts" / "scripts" / "git_cmd.sh").is_file())

    def test_bundled_templates(self):
        templates_dir = pathlib.Path(__file__).resolve().parent.parent / "templates"
        self.assertTrue((templates_dir / "shortcuts.json").is_file())

        search_script = templates_dir / "scripts" / "g_search.sh"
        self.assertTrue(search_script.is_file())

        # g_search must print usage and exit 1 when given no pattern
        res_no_arg = subprocess.run([str(search_script)], capture_output=True, text=True)
        self.assertEqual(res_no_arg.returncode, 1)
        self.assertIn("Usage", res_no_arg.stderr)

        # g_search finds pattern when given
        test_file = self.tmp_path / "target_search_me.txt"
        test_file.write_text("hello", encoding="utf-8")
        res_arg = subprocess.run([str(search_script), "search_me", str(self.tmp_path)], capture_output=True, text=True)
        self.assertEqual(res_arg.returncode, 0)
        self.assertIn("target_search_me.txt", res_arg.stdout)

        # Import templates via shortcut init --global
        res_init = self.run_cli(["init", "--global", str(templates_dir)])
        self.assertEqual(res_init.returncode, 0)
        self.assertIn("g_search", res_init.stdout)
        self.assertIn("g_serve", res_init.stdout)
        self.assertIn("g_ports", res_init.stdout)

    def test_desktop_entry_generation_and_quoting(self):
        # Create shortcut with %, quotes, backticks, dollar signs in description and inline
        res = self.run_cli(["new", "g_desk_test", "--desc", 'Run 100% "special" `app` with $VAR', "--", "echo", "desktop_ok"])
        self.assertEqual(res.returncode, 0)

        res_desk = self.run_cli(["desktop", "g_desk_test"])
        self.assertEqual(res_desk.returncode, 0)

        desktop_file = self.xdg_data_home / "applications" / "shortcut-g_desk_test.desktop"
        self.assertTrue(desktop_file.is_file())

        content = desktop_file.read_text(encoding="utf-8")
        self.assertIn("[Desktop Entry]", content)
        self.assertIn("Version=1.0", content)
        self.assertIn("Type=Application", content)
        self.assertIn("Terminal=true", content)
        self.assertIn("Categories=Utility;", content)
        self.assertIn("Icon=utilities-terminal", content)
        self.assertIn('Name=Run 100% "special" `app` with $VAR', content)

        # Tool path in Exec must be absolute
        tool_realpath = os.path.realpath(str(SHORTCUT))
        self.assertIn(tool_realpath, content)

        # Exec line formatting checks
        exec_lines = [line for line in content.splitlines() if line.startswith("Exec=")]
        self.assertEqual(len(exec_lines), 1)
        exec_line = exec_lines[0]

        # Every '%' must be doubled ('%%')
        without_doubles = exec_line.replace("%%", "")
        self.assertNotIn("%", without_doubles)

        # Inner characters properly escaped
        self.assertIn('\\"', exec_line)
        self.assertIn('\\$', exec_line)

    def test_desktop_rejects_local_scope(self):
        proj_dir = self.tmp_path / "local_proj"
        proj_dir.mkdir(parents=True, exist_ok=True)
        res_new = self.run_cli(["new", "local_desk", "--local", "--desc", "Local task", "--", "echo", "local"], cwd=str(proj_dir))
        self.assertEqual(res_new.returncode, 0)

        res_desk = self.run_cli(["desktop", "local_desk"], cwd=str(proj_dir))
        self.assertNotEqual(res_desk.returncode, 0)
        self.assertIn("local", res_desk.stderr.lower())
        self.assertTrue(
            "context" in res_desk.stderr.lower() or "project" in res_desk.stderr.lower(),
            f"Expected error message explaining lack of project context, got: {res_desk.stderr}",
        )

        desktop_file = self.xdg_data_home / "applications" / "shortcut-local_desk.desktop"
        self.assertFalse(desktop_file.exists())

    def test_fix_output_formatting(self):
        res_new = self.run_cli(["new", "g_fix_sample", "--desc", "Fix sample description", "--", "echo", "fix output running"])
        self.assertEqual(res_new.returncode, 0)

        # Before any run: metadata and script present, "no log yet"
        res_fix_pre = self.run_cli(["fix", "g_fix_sample"])
        self.assertEqual(res_fix_pre.returncode, 0)
        self.assertIn("Fix sample description", res_fix_pre.stdout)
        self.assertIn("fix output running", res_fix_pre.stdout)
        self.assertIn("no log yet", res_fix_pre.stdout)
        self.assertTrue(res_fix_pre.stdout.strip().endswith("Last exit code: none"))

        # Run the shortcut
        res_run = self.run_cli(["run", "g_fix_sample"])
        self.assertEqual(res_run.returncode, 0)

        # After run: log contents present and ends with last exit code 0
        res_fix_post = self.run_cli(["fix", "g_fix_sample"])
        self.assertEqual(res_fix_post.returncode, 0)
        self.assertIn("Fix sample description", res_fix_post.stdout)
        self.assertIn("fix output running", res_fix_post.stdout)
        self.assertTrue(res_fix_post.stdout.strip().endswith("Last exit code: 0"))

    def test_completion_bash_and_list_names(self):
        res_comp = self.run_cli(["completion", "bash"])
        self.assertEqual(res_comp.returncode, 0)
        self.assertIn("_shortcut", res_comp.stdout)
        self.assertIn("complete -F _shortcut shortcut", res_comp.stdout)
        self.assertIn("list --names", res_comp.stdout)
        for cmd in ("run", "show", "nano", "edit", "location", "rm", "log", "fix", "desktop"):
            self.assertIn(cmd, res_comp.stdout)

        # Test shortcut list --names
        self.run_cli(["new", "g_comp1", "--desc", "c1", "--", "echo", "1"])
        self.run_cli(["new", "g_comp2", "--desc", "c2", "--", "echo", "2"])
        res_names = self.run_cli(["list", "--names"])
        self.assertEqual(res_names.returncode, 0)
        names = [line.strip() for line in res_names.stdout.splitlines() if line.strip()]
        self.assertIn("g_comp1", names)
        self.assertIn("g_comp2", names)

    def test_rm_cleans_desktop_file(self):
        res_new = self.run_cli(["new", "g_rm_desk_test", "--desc", "Clean desktop test", "--", "echo", "rm_desk"])
        self.assertEqual(res_new.returncode, 0)

        res_desk = self.run_cli(["desktop", "g_rm_desk_test"])
        self.assertEqual(res_desk.returncode, 0)

        desktop_file = self.xdg_data_home / "applications" / "shortcut-g_rm_desk_test.desktop"
        self.assertTrue(desktop_file.is_file())

        res_rm = self.run_cli(["rm", "g_rm_desk_test", "-y"])
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

        proj_dir = self.tmp_path / "init_invalid_proj"
        proj_dir.mkdir()

        res = self.run_cli(["init", str(src_repo)], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        self.assertIn("not_a_dict (invalid entry)", res.stdout)
        self.assertIn("neither_cmd (invalid entry)", res.stdout)
        self.assertIn("both_cmd (invalid entry)", res.stdout)
        self.assertIn("inline_not_str (invalid entry)", res.stdout)
        self.assertIn("script_not_str (invalid entry)", res.stdout)
        self.assertIn("valid_entry", res.stdout)

        reg = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
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

        proj_dir = self.tmp_path / "init_symlink_proj"
        proj_dir.mkdir()

        res = self.run_cli(["init", str(src_repo)], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        self.assertIn("sym_script (symlink in source)", res.stdout)
        self.assertIn("real_script", res.stdout)

        reg = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertIn("real_script", reg["shortcuts"])
        self.assertNotIn("sym_script", reg["shortcuts"])

        copied_script = proj_dir / ".shortcuts" / "scripts" / "real_script.sh"
        self.assertTrue(copied_script.is_file())
        self.assertFalse(copied_script.is_symlink())

    def test_migrate_skips_existing_target(self):
        scripts_dir = self.shortcuts_home / "scripts"
        scripts_dir.mkdir(parents=True, exist_ok=True)
        existing_script_a = scripts_dir / "g_script_a.sh"
        existing_script_a.write_text("#!/bin/sh\necho original_a\n", encoding="utf-8")

        init_reg = {
            "version": 1,
            "shortcuts": {
                "g_script_a": {
                    "description": "Original A",
                    "script": "scripts/g_script_a.sh",
                },
                "g_script_b": {
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
        self.assertIn("skipped script_a.sh (g_script_a already exists)", res.stdout)
        self.assertIn("skipped script_b.sh (g_script_b already exists)", res.stdout)

        # Legacy files left in place
        self.assertTrue(flat_a.is_file())
        self.assertTrue(flat_b.is_file())

        # Existing script and registry entries unchanged
        self.assertEqual(existing_script_a.read_text(encoding="utf-8"), "#!/bin/sh\necho original_a\n")
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["g_script_a"]["description"], "Original A")
        self.assertEqual(reg["shortcuts"]["g_script_b"]["description"], "Already registered B")
        self.assertEqual(reg["shortcuts"]["g_script_b"]["inline"], "echo orig_b")

    def test_run_nolog_ctrl_c_returns_130(self):
        sleeper = self.shortcuts_home / "scripts" / "g_nolog_sleep.sh"
        sleeper.parent.mkdir(parents=True, exist_ok=True)
        sleeper.write_text("#!/usr/bin/env bash\necho started\nsleep 10\n", encoding="utf-8")
        sleeper.chmod(0o755)

        reg = {
            "version": 1,
            "shortcuts": {
                "g_nolog_sleep": {
                    "description": "Sleeper without log",
                    "script": "scripts/g_nolog_sleep.sh",
                    "log": False,
                },
            },
        }
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")

        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "g_nolog_sleep"],
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
            [sys.executable, str(SHORTCUT), "new", "g_tmpl", "--desc", "x"],
            cwd=str(self.home_dir), env=env, stdin=subprocess.DEVNULL, text=True, capture_output=True,
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("Edit it with: shortcut edit g_tmpl", res.stdout)
        self.assertTrue((self.shortcuts_home / "scripts" / "g_tmpl.sh").is_file())

    def test_new_on_terminal_uses_EDITOR_and_nano_command_uses_nano(self):
        fakebin = self.tmp_path / "fakebin"
        fakebin.mkdir(parents=True, exist_ok=True)
        for editor in ("myeditor", "nano"):
            fake = fakebin / editor
            fake.write_text(f'#!/usr/bin/env bash\ntouch "$1.opened-by-{editor}"\n', encoding="utf-8")
            fake.chmod(0o755)
        path = f"{fakebin}:{self.env.get('PATH', '')}"
        self.env.pop("SHORTCUT_EDITOR_CMD", None)

        rc, out = self.run_in_terminal(["new", "g_tmpl", "--desc", "x"], env={"PATH": path, "VISUAL": "", "EDITOR": "myeditor"})
        self.assertEqual(rc, 0, out)
        script = self.shortcuts_home / "scripts" / "g_tmpl.sh"
        self.assertTrue(pathlib.Path(f"{script}.opened-by-myeditor").is_file(), out)

        res = self.run_cli(["nano", "g_tmpl"], env={"PATH": path, "VISUAL": "/bin/false", "EDITOR": "/bin/false"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertTrue(pathlib.Path(f"{script}.opened-by-nano").is_file())

    def test_new_with_missing_editor_creates_nothing(self):
        res = self.run_cli(["new", "g_tmpl", "--desc", "x"], env={"SHORTCUT_EDITOR_CMD": "no-such-editor-xyz"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("editor not found: no-such-editor-xyz", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "g_tmpl" in self.load_global())
        self.assertFalse((self.shortcuts_home / "scripts" / "g_tmpl.sh").exists())

    def load_global(self) -> dict:
        return json.loads((self.shortcuts_home / "shortcuts.json").read_text())["shortcuts"]

    def test_new_in_home_refuses_local_store(self):
        res = self.run_cli(["new", "hello", "--", "echo hi"], cwd=str(self.home_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("is the global store", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "hello" in self.load_global())

        res_init = self.run_cli(["init"], cwd=str(self.home_dir))
        self.assertEqual(res_init.returncode, 1)
        self.assertIn("is the global store", res_init.stderr)

    def test_run_on_terminal_uses_pty_and_logs(self):
        inline = '[ -t 1 ] && echo "stdout:tty" || echo "stdout:pipe"; stty size; echo ready; read -r line; echo "got:$line"'
        self.assertEqual(self.run_cli(["new", "g_tty", "--", inline]).returncode, 0)

        rc, out = self.run_in_terminal(["g_tty"], keys=[(b"ready", b"hello\r")])
        self.assertEqual(rc, 0, out)
        self.assertIn("stdout:tty", out)
        self.assertIn("30 100", out)
        self.assertIn("got:hello", out)

        log = (self.shortcuts_home / "logs" / "g_tty.log").read_bytes()
        self.assertIn(b"stdout:tty", log)
        self.assertIn(b"got:hello", log)
        self.assertNotIn(b"\r\n", log)
        self.assertTrue(log.rstrip().endswith(b"=== exit: 0 ==="), log)

        res = self.run_cli(["g_tty"], input="x\n")
        self.assertIn("stdout:pipe", res.stdout)
        self.assertIn("got:x", res.stdout)

    def test_ctrl_c_on_terminal_returns_130(self):
        self.assertEqual(self.run_cli(["new", "g_sleep", "--", "echo started; sleep 10"]).returncode, 0)
        start = time.time()
        rc, out = self.run_in_terminal(["g_sleep"], keys=[(b"started", b"\x03")])
        self.assertEqual(rc, 130, out)
        self.assertLess(time.time() - start, 8)
        log = (self.shortcuts_home / "logs" / "g_sleep.log").read_text()
        self.assertTrue(log.rstrip().endswith("=== exit: 130 ==="), log)

    def test_sigterm_on_terminal_restores_terminal(self):
        self.assertEqual(self.run_cli(["new", "g_sleep", "--", "echo started; sleep 10"]).returncode, 0)
        rc, out = self.run_in_terminal(["g_sleep"], keys=[(b"started", signal.SIGTERM)])
        self.assertEqual(rc, 143, out)
        self.assertTrue(self.terminal_lflag & termios.ICANON, "terminal left in raw mode")
        self.assertTrue(self.terminal_lflag & termios.ISIG, "terminal left in raw mode")
        log = (self.shortcuts_home / "logs" / "g_sleep.log").read_text()
        self.assertTrue(log.rstrip().endswith("=== exit: 143 ==="), log)

    def test_local_shortcut_hiding_global_warns(self):
        static_dir = self.tmp_path / "static"
        static_dir.mkdir()
        self.assertEqual(self.run_cli(["new", "penpot", "--static", str(static_dir), "--", "echo global"]).returncode, 0)
        proj = self.tmp_path / "proj"
        proj.mkdir()
        self.assertEqual(self.run_cli(["new", "penpot", "--local", "--", "echo local"], cwd=str(proj)).returncode, 0)

        res = self.run_cli(["penpot"], cwd=str(proj))
        self.assertEqual(res.stdout.strip(), "local")
        self.assertIn("your global 'penpot' is hidden here", res.stderr)

        res_global = self.run_cli(["run", "--global", "penpot"], cwd=str(proj))
        self.assertEqual(res_global.stdout.strip(), "global")

        listing = self.run_cli(["list"], cwd=str(proj)).stdout
        self.assertIn("[hides global]", listing)
        self.assertIn("[hidden here by local]", listing)

        res_elsewhere = self.run_cli(["penpot"], cwd=str(self.tmp_path))
        self.assertEqual(res_elsewhere.stdout.strip(), "global")
        self.assertEqual(res_elsewhere.stderr, "")

    def test_concurrent_new_keeps_every_entry(self):
        procs = [
            subprocess.Popen(
                [sys.executable, str(SHORTCUT), "new", f"g_n{i}", "--", f"echo {i}"],
                cwd=str(self.home_dir), env=self.env, stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
            )
            for i in range(16)
        ]
        for proc in procs:
            _, err = proc.communicate(timeout=30)
            self.assertEqual(proc.returncode, 0, err)
        self.assertEqual(sorted(self.load_global()), sorted(f"g_n{i}" for i in range(16)))

    def test_prefix_rm_refuses_while_used(self):
        target = self.tmp_path / "C"
        target.mkdir()
        self.assertEqual(self.run_cli(["prefix", "add", "C_", str(target)]).returncode, 0)
        self.assertEqual(self.run_cli(["new", "C_build", "--", "pwd"]).returncode, 0)

        res = self.run_cli(["prefix", "rm", "C_"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("still used by: C_build", res.stderr)

        res_force = self.run_cli(["prefix", "rm", "C_", "--force"])
        self.assertEqual(res_force.returncode, 0, res_force.stderr)
        res_run = self.run_cli(["C_build"])
        self.assertEqual(res_run.returncode, 1)
        self.assertIn("shortcut prefix add C_ DIR", res_run.stderr)

    def test_fix_strips_terminal_codes(self):
        inline = 'printf "\\033[31mred\\033[0m\\n50%%\\r100%%\\n"'
        self.assertEqual(self.run_cli(["new", "g_color", "--", inline]).returncode, 0)
        self.run_cli(["g_color"])
        fix = self.run_cli(["fix", "g_color"]).stdout
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
        self.run_cli(["new", "g_inline_fail", "--", "echo test"])
        res = self.run_cli(["nano", "g_inline_fail"], env={"SHORTCUT_EDITOR_CMD": "false"})
        self.assertEqual(res.returncode, 1)
        self.assertTrue(res.stderr.strip().startswith("error:"), f"Expected stderr to start with 'error:', got:\n{res.stderr}")
        self.assertNotIn("Traceback", res.stderr)


if __name__ == "__main__":
    unittest.main()
