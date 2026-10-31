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

SHORTCUT = pathlib.Path(__file__).resolve().parent.parent / "shortcut.py"

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

    def allow(self, store_dir: pathlib.Path) -> None:
        """Record consent for a project store, as `shortcut allow -y` would."""
        shortcut_mod.set_allowed(str(store_dir), shortcut_mod.snapshot_project(str(store_dir)).fingerprint)

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

        # Not allowed yet: the project's store is ignored
        res_ignored = shortcut_mod.resolve_shortcut("build", caller_cwd=str(proj_dir))
        self.assertEqual(res_ignored.scope, "static")
        self.allow(proj_shortcuts)

        # Inside an allowed project: project precedence
        res_local = shortcut_mod.resolve_shortcut("build", caller_cwd=str(proj_dir))
        self.assertEqual(res_local.scope, "project")
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
        self.assertIn("runs in your current directory (g_ name); --static/--local don't apply", res1.stderr)

        res2 = self.run_cli(["new", "g_foo", "--local", "--", "echo 1"])
        self.assertNotEqual(res2.returncode, 0)
        self.assertIn("runs in your current directory (g_ name); --static/--local don't apply", res2.stderr)

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
        self.assertIn("(P_ name); --static/--local don't apply", res3.stderr)

        res4 = self.run_cli(["new", "P_foo", "--local", "--", "echo 1"])
        self.assertNotEqual(res4.returncode, 0)
        self.assertIn("(P_ name); --static/--local don't apply", res4.stderr)

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
        self.assertIn("created project store", res8.stderr)
        self.assertTrue((proj_dir / ".shortcuts" / "shortcuts.json").is_file())
        # A store the user created is allowed: it runs straight away
        self.assertEqual(self.run_cli(["local_cmd"], cwd=str(proj_dir)).stdout, "local\n")

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
        self.assertIn("Project shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, rf"local_cmd +{proj_dir} +echo local")
        self.assertIn("Global shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"g_glob +\(current dir\) +Global desc")
        self.assertRegex(res_list.stdout, rf"penpot +{self.tmp_path} +Static desc")
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
        for reserved_p in ("g_",):
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
        self.assertIn("Initialized empty project store", res.stdout)

        store_json = proj_dir / ".shortcuts" / "shortcuts.json"
        self.assertTrue(store_json.is_file())
        data = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertEqual(data, {"version": 1, "shortcuts": {}})
        # The user made it, so it is allowed; logs and the lock stay out of git
        self.assertIsNotNone(shortcut_mod.allowed_registry(str(proj_dir / ".shortcuts")))
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
        self.run_cli(["new", "g_collide", "--", "echo existing"])
        pref_dir = self.tmp_path / "pref_target"
        pref_dir.mkdir()
        self.run_cli(["prefix", "add", "P_", str(pref_dir)])

        ext_script = self.tmp_path / "external_source.sh"
        ext_script.write_text("#!/usr/bin/env bash\necho ext\n", encoding="utf-8")
        src_repo = self.tmp_path / "source_repo"
        (src_repo / "scripts").mkdir(parents=True)
        (src_repo / "scripts" / "valid.sh").write_text("#!/usr/bin/env bash\necho valid\n", encoding="utf-8")
        shortcut_mod.save_registry_atomic(str(src_repo), {
            "version": 1,
            "shortcuts": {
                "g_collide": {"description": "collision", "inline": "echo collide"},
                "g_linked": {"description": "linked", "script": str(ext_script)},
                "g_with_dir": {"description": "lies", "inline": "pwd", "dir": str(self.tmp_path)},
                "P_with_dir": {"description": "lies", "inline": "pwd", "dir": str(self.tmp_path)},
                "P_pref": {"description": "prefix registered", "inline": "echo pref"},
                "g_valid": {"description": "valid", "script": "scripts/valid.sh"},
            },
        })

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("g_collide (already exists)", res.stdout)
        self.assertIn("g_linked (linked external script)", res.stdout)
        self.assertIn("g_with_dir (has a 'dir', but its name says it runs in your current directory)", res.stdout)
        self.assertIn("P_with_dir (has a 'dir', but its name says it runs in its prefix directory)", res.stdout)
        reg = self.load_global()
        self.assertIn("P_pref", reg)
        self.assertEqual(reg["g_valid"]["script"], "scripts/g_valid.sh")
        self.assertTrue(os.access(self.shortcuts_home / "scripts" / "g_valid.sh", os.X_OK))
        for skipped in ("g_linked", "g_with_dir", "P_with_dir"):
            self.assertNotIn(skipped, reg)

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
            '{"version": 1, "shortcuts": {"g_git_cmd": {"description": "From git", "script": "scripts/git_cmd.sh"}}}',
            encoding="utf-8",
        )

        subprocess.run(["git", "-C", str(git_dir), "add", "."], check=True)
        subprocess.run(["git", "-C", str(git_dir), "commit", "-m", "initial commit"], check=True, capture_output=True)

        res = self.run_cli(["init", "--global", f"file://{git_dir.resolve()}"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("g_git_cmd", res.stdout)
        self.assertEqual(self.run_cli(["g_git_cmd"]).stdout, "from_git\n")

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
        self.assertIn("run --global g_desk_test", exec_line)
        self.assertIn("command -v notify-send", exec_line)
        self.assertIn("Press Enter to close...", exec_line)

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
                "g_valid_entry": {"description": "ok", "inline": "echo ok"},
            },
        }
        (src_repo / "shortcuts.json").write_text(json.dumps(source_data), encoding="utf-8")

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0)
        self.assertIn("not_a_dict (invalid entry)", res.stdout)
        self.assertIn("neither_cmd (invalid entry)", res.stdout)
        self.assertIn("both_cmd (invalid entry)", res.stdout)
        self.assertIn("inline_not_str (invalid entry)", res.stdout)
        self.assertIn("script_not_str (invalid entry)", res.stdout)
        self.assertIn("g_valid_entry", res.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("g_valid_entry", reg["shortcuts"])
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
                "g_sym_script": {"description": "symlink script", "script": "scripts/sym_target.sh"},
                "g_real_script": {"description": "real script", "script": "scripts/real.sh"},
            },
        }
        (src_repo / "shortcuts.json").write_text(json.dumps(source_data), encoding="utf-8")

        res = self.run_cli(["init", "--global", str(src_repo)])
        self.assertEqual(res.returncode, 0)
        self.assertIn("g_sym_script (symlink in source)", res.stdout)
        self.assertIn("g_real_script", res.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("g_real_script", reg["shortcuts"])
        self.assertNotIn("g_sym_script", reg["shortcuts"])

        copied_script = self.shortcuts_home / "scripts" / "g_real_script.sh"
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
            fake.write_text(f'#!/usr/bin/env bash\necho "# edited by {editor}" >> "$1"\n', encoding="utf-8")
            fake.chmod(0o755)
        path = f"{fakebin}:{self.env.get('PATH', '')}"
        self.env.pop("SHORTCUT_EDITOR_CMD", None)

        rc, out = self.run_in_terminal(["new", "g_tmpl", "--desc", "x"], env={"PATH": path, "VISUAL": "", "EDITOR": "myeditor"})
        self.assertEqual(rc, 0, out)
        script = self.shortcuts_home / "scripts" / "g_tmpl.sh"
        self.assertIn("# edited by myeditor", script.read_text(), out)

        res = self.run_cli(["nano", "g_tmpl"], env={"PATH": path, "VISUAL": "/bin/false", "EDITOR": "/bin/false"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("# edited by nano", script.read_text())

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
        self.assertIn(f"\u2192 {proj}  (project shortcut; your global 'penpot' is hidden here", res.stderr)

        res_global = self.run_cli(["run", "--global", "penpot"], cwd=str(proj))
        self.assertEqual(res_global.stdout.strip(), "global")

        listing = self.run_cli(["list"], cwd=str(proj)).stdout
        self.assertIn("[hides global]", listing)
        self.assertIn("[hidden here by project]", listing)

        res_elsewhere = self.run_cli(["penpot"], cwd=str(self.tmp_path))
        self.assertEqual(res_elsewhere.stdout.strip(), "global")
        self.assertEqual(res_elsewhere.stderr, f"\u2192 {static_dir}\n")

    def test_g_prefix_means_current_directory_both_ways(self):
        work = self.tmp_path / "work"
        work.mkdir()
        self.assertEqual(self.run_cli(["new", "g_pwd", "--", "pwd"]).returncode, 0)
        self.assertEqual(self.run_cli(["g_pwd"], cwd=str(work)).stdout, f"{work}\n")

        prefix_dir = self.tmp_path / "C"
        prefix_dir.mkdir()
        reg = json.loads((self.shortcuts_home / "shortcuts.json").read_text())
        reg["prefixes"] = {"C_": str(prefix_dir)}
        reg["shortcuts"].update({
            "g_lies": {"description": "x", "inline": "pwd", "dir": str(self.tmp_path)},
            "C_lies": {"description": "x", "inline": "pwd", "dir": str(self.tmp_path)},
            "glob_old": {"description": "old alias", "inline": "pwd"},
            "plain": {"description": "x", "inline": "pwd"},
        })
        shortcut_mod.save_registry_atomic(str(self.shortcuts_home), reg)

        res = self.run_cli(["g_lies"], cwd=str(work))
        self.assertEqual(res.returncode, 1)
        self.assertIn("g_ names always run in your current directory", res.stderr)
        res = self.run_cli(["C_lies"], cwd=str(work))
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"C_ names always run in {prefix_dir}", res.stderr)
        # glob_/global_ are no longer markers: without a dir these can't run anywhere
        for name in ("glob_old", "plain"):
            res = self.run_cli([name], cwd=str(work))
            self.assertEqual(res.returncode, 1)
            self.assertIn("only g_ names run in your current directory", res.stderr)

        # A static shortcut can't turn into a prefix name that would move it
        static = self.tmp_path / "static"
        static.mkdir()
        self.assertEqual(self.run_cli(["new", "D_tool", "--static", str(static), "--", "pwd"]).returncode, 0)
        res = self.run_cli(["prefix", "add", "D_", str(self.tmp_path)])
        self.assertEqual(res.returncode, 1)
        self.assertIn("D_tool run in their own 'dir'", res.stderr)
        self.assertNotIn("D_", self.run_cli(["prefix", "list"]).stdout)

    def write_project(self, proj: pathlib.Path) -> pathlib.Path:
        """A cloned-looking project: shortcuts.json plus a script that lives in the repo itself."""
        (proj / "tools").mkdir(parents=True)
        (proj / "sub").mkdir()
        (proj / "tools" / "test.sh").write_text("#!/usr/bin/env bash\necho testing in $(pwd)\n", encoding="utf-8")
        (proj / ".shortcuts").mkdir()
        (proj / ".shortcuts" / "shortcuts.json").write_text(json.dumps({
            "version": 1,
            "shortcuts": {
                "build": {"description": "Build it", "inline": "echo built in $(pwd)", "dir": "sub"},
                "test": {"description": "Test it", "script": "tools/test.sh"},
                "escape": {"description": "x", "script": "../outside.sh"},
                "updir": {"description": "x", "inline": "pwd", "dir": "../.."},
            },
        }), encoding="utf-8")
        return proj / ".shortcuts"

    def test_allow_is_required_and_invalidated_by_changes(self):
        proj = self.tmp_path / "proj"
        store = self.write_project(proj)

        res = self.run_cli(["build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("haven't allowed; review them with: shortcut allow", res.stderr)
        self.assertIn("are not allowed yet", self.run_cli(["list"], cwd=str(proj)).stdout)
        self.assertEqual(self.run_cli(["list", "--names"], cwd=str(proj)).stdout, "")

        res = self.run_cli(["allow"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("confirm in a terminal", res.stderr)

        res = self.run_cli(["allow", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn(f"build\n  description: Build it\n  runs in: {proj / 'sub'}\n  command:\n    echo built in $(pwd)", res.stdout)
        self.assertIn(f"test\n  description: Test it\n  runs in: {proj}\n  script: {proj / 'tools' / 'test.sh'}\n    #!/usr/bin/env bash", res.stdout)
        self.assertIn("escape", res.stdout)
        self.assertIn("script: cannot read (Path traversal detected", res.stdout)
        self.assertIn(f"updir\n  description: x\n  runs in: {proj.parent.parent} (outside the project: won't run)", res.stdout)
        allowed = json.loads((self.shortcuts_home / "allowed.json").read_text())
        self.assertEqual(list(allowed), [str(proj.resolve())])

        # Read in place: relative paths resolve against the project root, nothing is copied
        self.assertEqual(self.run_cli(["build"], cwd=str(proj / "tools")).stdout, f"built in {proj / 'sub'}\n")
        self.assertEqual(self.run_cli(["test"], cwd=str(proj / "sub")).stdout, f"testing in {proj}\n")
        self.assertFalse((store / "scripts").exists())
        self.assertEqual(self.run_cli(["escape"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["updir"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("Path traversal detected: '../..'", res.stderr)
        self.assertIn("Already allowed", self.run_cli(["allow", "-y"], cwd=str(proj)).stdout)

        # Changing a script it uses, or the json itself, needs allowing again
        with open(proj / "tools" / "test.sh", "a", encoding="utf-8") as f:
            f.write("echo extra\n")
        self.assertIn("haven't allowed", self.run_cli(["build"], cwd=str(proj)).stderr)
        self.assertEqual(self.run_cli(["allow", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["build"], cwd=str(proj)).returncode, 0)
        (store / "shortcuts.json").write_text((store / "shortcuts.json").read_text() + " ", encoding="utf-8")
        self.assertIn("haven't allowed", self.run_cli(["build"], cwd=str(proj)).stderr)
        self.assertEqual(self.run_cli(["allow", "-y"], cwd=str(proj)).returncode, 0)

        # Changes made through shortcut itself keep it allowed
        self.assertEqual(self.run_cli(["new", "extra", "--local", "--", "echo extra"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["extra"], cwd=str(proj)).stdout, "extra\n")
        self.assertEqual(self.run_cli(["rm", "-y", "test"], cwd=str(proj)).returncode, 0)
        self.assertTrue((proj / "tools" / "test.sh").is_file(), "rm must never delete a project's own file")
        self.assertEqual(self.run_cli(["build"], cwd=str(proj)).returncode, 0)

        self.assertEqual(self.run_cli(["allow", "--revoke"], cwd=str(proj)).returncode, 0)
        self.assertIn("haven't allowed", self.run_cli(["build"], cwd=str(proj)).stderr)

    def test_allow_prompt_in_terminal(self):
        proj = self.tmp_path / "proj"
        self.write_project(proj)
        rc, out = self.run_in_terminal(["allow"], cwd=str(proj), keys=[(b"[y/N]", b"n\r")])
        self.assertEqual(rc, 1, out)
        self.assertIn("echo built in $(pwd)", out.split("[y/N]")[0])
        self.assertIn("Not allowed.", out)
        rc, out = self.run_in_terminal(["allow"], cwd=str(proj), keys=[(b"[y/N]", b"y\r")])
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.run_cli(["build"], cwd=str(proj)).returncode, 0)

    def test_run_directory_line(self):
        work = self.tmp_path / "work"
        work.mkdir()
        self.assertEqual(self.run_cli(["new", "g_pwd", "--", "pwd"]).returncode, 0)
        res = self.run_cli(["g_pwd"], cwd=str(work))
        self.assertEqual(res.stdout, f"{work}\n")
        self.assertEqual(res.stderr, f"\u2192 {work}\n")
        res = self.run_cli(["g_pwd"], cwd=str(work), env={"SHORTCUT_SHOW_DIR": "0"})
        self.assertEqual(res.stderr, "")
        rc, out = self.run_in_terminal(["g_pwd"], cwd=str(work))
        self.assertIn(f"\x1b[2m\u2192 {work}\x1b[0m", out)

    def test_new_editor_failure_creates_nothing(self):
        res = self.run_cli(["new", "g_tmpl", "--desc", "x"], env={"SHORTCUT_EDITOR_CMD": "false"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("was not created", res.stderr)
        self.assertNotIn("g_tmpl", self.load_global() if (self.shortcuts_home / "shortcuts.json").exists() else {})
        self.assertFalse((self.shortcuts_home / "scripts" / "g_tmpl.sh").exists())

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

    def test_prefix_named_project_entries_allow_and_list(self):
        pref_dir = self.tmp_path / "pref_p"
        pref_dir.mkdir()
        self.assertEqual(self.run_cli(["prefix", "add", "P_", str(pref_dir)]).returncode, 0)

        proj = self.tmp_path / "proj_p"
        proj.mkdir()
        (proj / ".shortcuts").mkdir()
        reg = {
            "version": 1,
            "shortcuts": {
                "P_sneaky": {"description": "Sneaky", "inline": "echo SNEAKY"},
            },
        }
        (proj / ".shortcuts" / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")

        res_allow = self.run_cli(["allow", "-y"], cwd=str(proj))
        self.assertEqual(res_allow.returncode, 0)
        self.assertIn("echo SNEAKY", res_allow.stdout)
        self.assertIn("ignored while P_ is a registered prefix", res_allow.stdout)

        res_list = self.run_cli(["list"], cwd=str(proj))
        self.assertEqual(res_list.returncode, 0)
        self.assertNotIn("P_sneaky", res_list.stdout)

    def test_concurrent_project_registration_keeps_allowed(self):
        proj = self.tmp_path / "proj_concurrent"
        proj.mkdir()
        self.assertEqual(self.run_cli(["init"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["allow", "-y"], cwd=str(proj)).returncode, 0)

        procs = []
        for i in range(1, 7):
            p = subprocess.Popen(
                [sys.executable, str(SHORTCUT), "new", f"c{i}", "--local", "--", f"echo {i}"],
                cwd=str(proj),
                env=self.env,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            procs.append((i, p))

        for i, p in procs:
            stdout, stderr = p.communicate(timeout=15)
            self.assertEqual(p.returncode, 0, f"c{i} failed: {stderr}")

        for i in range(1, 7):
            res = self.run_cli([f"c{i}"], cwd=str(proj))
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertEqual(res.stdout.strip(), str(i))

    def test_edit_only_reallows_own_change(self):
        proj = self.tmp_path / "proj_edit"
        proj.mkdir()
        store = proj / ".shortcuts"
        store.mkdir()
        scripts = proj / "scripts"
        scripts.mkdir()
        script_a = scripts / "a.sh"
        script_a.write_text("#!/bin/sh\necho a\n", encoding="utf-8")
        script_b = scripts / "b.sh"
        script_b.write_text("#!/bin/sh\necho b\n", encoding="utf-8")

        reg = {
            "version": 1,
            "shortcuts": {
                "a": {"description": "A", "script": "scripts/a.sh"},
                "b": {"description": "B", "script": "scripts/b.sh"},
            },
        }
        (store / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")
        self.assertEqual(self.run_cli(["allow", "-y"], cwd=str(proj)).returncode, 0)

        editor_sh = self.tmp_path / "sneaky_editor.sh"
        editor_sh.write_text(
            f'#!/usr/bin/env bash\necho "# edited a" >> "$1"\necho "# edited b" >> "{script_b.resolve()}"\n',
            encoding="utf-8",
        )
        editor_sh.chmod(0o755)

        res_edit = self.run_cli(["edit", "a"], cwd=str(proj), env={"SHORTCUT_EDITOR_CMD": str(editor_sh)})
        self.assertEqual(res_edit.returncode, 0)

        res_run = self.run_cli(["a"], cwd=str(proj))
        self.assertEqual(res_run.returncode, 1)
        self.assertIn("haven't allowed", res_run.stderr)

    def test_hash_empty_vs_unreadable(self):
        proj = self.tmp_path / "proj_a4"
        proj.mkdir()
        (proj / ".shortcuts").mkdir()
        reg = {
            "version": 1,
            "shortcuts": {
                "missing": {"description": "Missing", "script": "missing.sh"},
            },
        }
        (proj / ".shortcuts" / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")

        self.assertEqual(self.run_cli(["allow", "-y"], cwd=str(proj)).returncode, 0)

        (proj / "missing.sh").write_text("", encoding="utf-8")

        res = self.run_cli(["missing"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("haven't allowed", res.stderr)

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
        self.assertEqual(self.run_cli(["allow", "-y"], cwd=str(proj)).returncode, 0)

        res = self.run_cli(["bad"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertTrue(res.stderr.strip().startswith("error:"))
        self.assertNotIn("Traceback", res.stderr)

        src_dir = self.tmp_path / "src_a5"
        src_dir.mkdir()
        src_reg = {
            "version": 1,
            "shortcuts": {
                "bad_dir": {"description": "Bad dir", "inline": "echo hi", "dir": 123},
            },
        }
        (src_dir / "shortcuts.json").write_text(json.dumps(src_reg), encoding="utf-8")

        res_init = self.run_cli(["init", "--global", str(src_dir)])
        self.assertEqual(res_init.returncode, 0)
        self.assertIn("bad_dir (invalid entry)", res_init.stdout)
        self.assertNotIn("Traceback", res_init.stderr)

    def test_broken_output_pipe_exits_141_and_logs(self):
        self.assertEqual(self.run_cli(["new", "g_seq", "--", "seq 1 1000000"]).returncode, 0)
        p_shortcut = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "g_seq"],
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
        log_path = self.shortcuts_home / "logs" / "g_seq.log"
        self.assertTrue(log_path.is_file())
        log_content = log_path.read_text(encoding="utf-8")
        self.assertIn("=== exit: 141 ===", log_content)

    def test_signals_reach_child_group_in_pipe_mode(self):
        pid_file = self.tmp_path / "child.pid"
        inline = f"sh -c 'echo $$ > {pid_file}; exec sleep 300'; echo done"
        self.assertEqual(self.run_cli(["new", "g_group_sig", "--", inline]).returncode, 0)
        proc = subprocess.Popen(
            [sys.executable, str(SHORTCUT), "run", "g_group_sig"],
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
        self.assertEqual(self.run_cli(["add", "g_py", str(script), "--desc", "python"]).returncode, 0)
        res = self.run_cli(["g_py"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("py 2", res.stdout)

        sh_no_exec = self.tmp_path / "no_shebang.sh"
        sh_no_exec.write_text("echo nonexec\n", encoding="utf-8")
        sh_no_exec.chmod(0o644)
        self.assertEqual(self.run_cli(["add", "g_noshebang", str(sh_no_exec), "--desc", "noshebang"]).returncode, 0)
        res2 = self.run_cli(["g_noshebang"])
        self.assertEqual(res2.returncode, 0, res2.stderr)
        self.assertIn("nonexec", res2.stdout)

        sh_exec = self.tmp_path / "exec.sh"
        sh_exec.write_text("#!/usr/bin/env bash\necho exec_shebang\n", encoding="utf-8")
        sh_exec.chmod(0o755)
        self.assertEqual(self.run_cli(["add", "g_exec", str(sh_exec), "--desc", "exec"]).returncode, 0)
        res3 = self.run_cli(["g_exec"])
        self.assertEqual(res3.returncode, 0, res3.stderr)
        self.assertIn("exec_shebang", res3.stdout)

    def test_run_help(self):
        res1 = self.run_cli(["run", "--help"])
        self.assertEqual(res1.returncode, 0)
        self.assertIn("shortcut run [--global] <name> [args...]", res1.stdout)

        res2 = self.run_cli(["run", "-h"])
        self.assertEqual(res2.returncode, 0)
        self.assertIn("shortcut run [--global] <name> [args...]", res2.stdout)

        self.assertEqual(self.run_cli(["new", "g_echo_args", "--", "echo \"args: $@\""]).returncode, 0)
        res3 = self.run_cli(["run", "g_echo_args", "--help"])
        self.assertEqual(res3.returncode, 0)
        self.assertIn("args: --help", res3.stdout)

    def test_rm_local_keeps_desktop_and_prefix_unrunnable(self):
        # 1. Global penpot with desktop file plus project penpot
        proj_dir = self.tmp_path / "penpot_proj"
        proj_dir.mkdir(parents=True, exist_ok=True)
        self.assertEqual(
            self.run_cli(["new", "penpot", "--static", str(self.tmp_path), "--desc", "Global penpot", "--", "echo", "global"]).returncode,
            0,
        )
        self.assertEqual(self.run_cli(["desktop", "penpot"]).returncode, 0)
        desktop_file = self.xdg_data_home / "applications" / "shortcut-penpot.desktop"
        self.assertTrue(desktop_file.is_file())

        # Create project penpot and remove it
        self.assertEqual(
            self.run_cli(["new", "penpot", "--local", "--desc", "Project penpot", "--", "echo", "local"], cwd=str(proj_dir)).returncode,
            0,
        )
        res_rm = self.run_cli(["rm", "penpot", "-y"], cwd=str(proj_dir))
        self.assertEqual(res_rm.returncode, 0)
        self.assertTrue(desktop_file.is_file(), "Removing local shortcut must keep global desktop file")
        global_reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("penpot", global_reg["shortcuts"])

        # 2. prefix rm --force leaves unrunnable entry; rm -y must still remove it
        c_dir = self.tmp_path / "c_dir"
        c_dir.mkdir(parents=True, exist_ok=True)
        self.assertEqual(self.run_cli(["prefix", "add", "C_", str(c_dir)]).returncode, 0)
        self.assertEqual(self.run_cli(["new", "C_build", "--", "true"]).returncode, 0)
        self.assertEqual(self.run_cli(["prefix", "rm", "C_", "--force"]).returncode, 0)
        res_rm_c = self.run_cli(["rm", "C_build", "-y"])
        self.assertEqual(res_rm_c.returncode, 0)

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
        self.assertTrue((self.shortcuts_home / "scripts" / "g_old.sh").is_file())

    def test_rm_malformed_entry(self):
        reg = {"shortcuts": {"bad_entry": {"description": "x", "inline": "true", "dir": 123}}}
        (self.shortcuts_home / "shortcuts.json").write_text(json.dumps(reg), encoding="utf-8")
        res = self.run_cli(["rm", "bad_entry", "-y"])
        self.assertEqual(res.returncode, 0)
        after = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("bad_entry", after.get("shortcuts", {}))


if __name__ == "__main__":
    unittest.main()
