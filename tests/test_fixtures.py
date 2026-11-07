#!/usr/bin/env python3
"""The hub itself: the sandbox refuses to leave its temp dir, and spokes stay on the hub.

Nothing here points at the real home. The stand-in for it is a second temp dir, so if the
guard ever breaks, these tests fail without reaching ~/.shortcuts.
"""

import ast
import json
import os
import pathlib
import tempfile
import unittest
import unittest.mock

import fixtures
from fixtures import SandboxEscape, ShortcutTestCase, refuse_unless_sandboxed


class TestSandboxGuard(ShortcutTestCase):
    def setUp(self):
        super().setUp()
        outside = tempfile.TemporaryDirectory()
        self.addCleanup(outside.cleanup)
        self.outside = pathlib.Path(outside.name).resolve()

    def assert_refused(self, **env):
        for run in (
            lambda: self.run_cli(["new", "x", "--", "true"], env=env),
            lambda: self.run_in_terminal(["new", "x", "--", "true"], env=env),
            lambda: self.popen_cli(["new", "x", "--", "true"], env=env),
        ):
            with self.assertRaises(SandboxEscape):
                run()
        self.assertEqual(list(self.outside.iterdir()), [])

    def test_the_sandbox_is_inside_the_temp_dir(self):
        for name in ("HOME", "SHORTCUTS_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "BIN_DIR"):
            for env in (self.env, os.environ):
                self.assertTrue(pathlib.Path(env[name]).is_relative_to(self.tmp_path), name)

    def test_a_store_derived_from_the_old_home_is_refused(self):
        # What `export HOME=$(mktemp -d) SHORTCUTS_HOME=$HOME/.shortcuts` leaves behind
        self.assert_refused(SHORTCUTS_HOME=str(self.outside / ".shortcuts"))

    def test_a_home_outside_is_refused_even_when_the_store_is_inside(self):
        self.assert_refused(HOME=str(self.outside))

    def test_a_home_outside_is_refused_when_the_store_follows_it(self):
        self.assert_refused(HOME=str(self.outside), SHORTCUTS_HOME="")

    def test_an_empty_home_is_refused(self):
        self.assert_refused(HOME="")

    def test_a_tilde_store_follows_the_child_home(self):
        self.assert_refused(HOME=str(self.outside), SHORTCUTS_HOME="~/.shortcuts")
        res = self.run_cli(["new", "x", "--", "true"], env={"SHORTCUTS_HOME": "~/elsewhere"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertTrue((self.home_dir / "elsewhere" / "shortcuts.json").is_file())

    def test_the_other_places_a_run_writes_are_guarded(self):
        for name in ("XDG_DATA_HOME", "XDG_CONFIG_HOME", "BIN_DIR"):
            self.assert_refused(**{name: str(self.outside)})

    def test_a_path_that_only_looks_inside_is_refused(self):
        sibling = f"{self.tmp_path}-other"
        self.assert_refused(SHORTCUTS_HOME=sibling)
        self.assert_refused(SHORTCUTS_HOME=str(self.tmp_path / ".." / self.outside.name))
        link = self.tmp_path / "link"
        link.symlink_to(self.outside)
        self.assert_refused(SHORTCUTS_HOME=str(link / ".shortcuts"))

    def test_a_cwd_outside_is_refused(self):
        with self.assertRaises(SandboxEscape):
            self.run_cli(["new", "x", "--local", "--", "true"], cwd=str(self.outside))
        with self.assertRaises(SandboxEscape):
            self.run_in_terminal(["new", "x", "--local", "--", "true"], cwd=str(self.outside))
        self.assertEqual(list(self.outside.iterdir()), [])

    def test_changing_the_sandbox_env_mid_test_is_refused(self):
        self.env["SHORTCUTS_HOME"] = str(self.outside)
        with self.assertRaises(SandboxEscape):
            self.run_cli(["list"])

    def test_changing_os_environ_mid_test_is_refused(self):
        os.environ["HOME"] = str(self.outside)
        with self.assertRaises(SandboxEscape):
            self.run_cli(["list"])

    def test_a_fixture_file_outside_is_refused(self):
        with self.assertRaises(SandboxEscape):
            self.write_store(self.outside / ".shortcuts", {})
        self.assertEqual(list(self.outside.iterdir()), [])

    def test_setup_refuses_before_writing_anything(self):
        class Escaped(ShortcutTestCase):
            def runTest(self):
                raise AssertionError("ran outside the sandbox")

        env_at_start = dict(os.environ)
        with unittest.mock.patch("fixtures.SANDBOXED_PATHS", {"ELSEWHERE": str(self.outside)}):
            result = Escaped().run()
        self.assertEqual(len(result.errors), 1)
        self.assertIn("SandboxEscape", result.errors[0][1])
        self.assertEqual(dict(os.environ), env_at_start)

    def test_paths_left_unset_fall_under_home(self):
        env = {"HOME": str(self.home_dir)}
        refuse_unless_sandboxed(self.tmp_path, env, self.home_dir)
        env["SHORTCUTS_HOME"] = str(self.outside)
        with self.assertRaises(SandboxEscape):
            refuse_unless_sandboxed(self.tmp_path, env, self.home_dir)


class TestFixtures(ShortcutTestCase):
    def test_project_writes_what_it_is_given(self):
        proj = self.project(fixtures.GO, dirs=["src"], files={"bin/tool": b"\xff"}, imports={"a": "a.shortcut"})
        self.assertTrue((proj / "src").is_dir())
        self.assertEqual((proj / "bin" / "tool").read_bytes(), b"\xff")
        data = json.loads((proj / ".shortcuts").read_text())
        self.assertEqual(data, {"version": 2, "shortcuts": fixtures.GO, "imports": {"a": "a.shortcut"}})

    def test_project_without_shortcuts_has_no_store(self):
        self.assertFalse((self.project() / ".shortcuts").exists())

    def test_an_approved_project_runs_at_once(self):
        proj = self.project(fixtures.GO, approved=True)
        self.assertEqual(self.run_cli(["r", "go"], cwd=str(proj)).stdout, "ran\n")

    def test_a_broken_project_reads_as_corrupt(self):
        self.assertIn("corrupt registry", self.run_cli(["list"], cwd=str(self.broken_project())).stderr)

    def test_a_collection_imports(self):
        src = self.collection(fixtures.HI, files={"bin/a.sh": "echo a\n"}, mode=0o755)
        self.assertTrue(os.access(src / "bin" / "a.sh", os.X_OK))
        self.assertEqual(self.run_cli(["init", "--global", str(src), "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "hi"]).stdout, "hi\n")

    def test_shared_fixtures_are_left_as_they_were(self):
        before = repr(fixtures.GO)
        self.project(fixtures.GO, approved=True)
        self.assertEqual(repr(fixtures.GO), before)


class TestSpokes(unittest.TestCase):
    """Every spoke reaches the system through the hub, or the guard proves nothing."""

    def test_spokes_stay_on_the_hub(self):
        spokes = [p for p in pathlib.Path(__file__).parent.glob("test_*.py") if p.name != "test_fixtures.py"]
        self.assertTrue(spokes)
        for path in spokes:
            tree = ast.parse(path.read_text(encoding="utf-8"))
            for node in ast.walk(tree):
                if isinstance(node, (ast.Import, ast.ImportFrom)):
                    names = {alias.name for alias in node.names} | {getattr(node, "module", None)}
                    self.assertFalse(names & {"subprocess", "tempfile"}, f"{path.name}:{node.lineno}")
                if isinstance(node, ast.Attribute) and node.attr == "environ":
                    self.fail(f"{path.name}:{node.lineno} uses os.environ; pass env= to the hub")
                if isinstance(node, ast.ClassDef):
                    self.assertEqual([ast.unparse(b) for b in node.bases], ["ShortcutTestCase"], path.name)


if __name__ == "__main__":
    unittest.main()
