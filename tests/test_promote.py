#!/usr/bin/env python3
"""shortcut promote: an approved project shortcut becomes one of yours, running this checkout's files."""

import json
import unittest

from fixtures import ShortcutTestCase

TOOL_SCRIPT = '#!/bin/sh\necho "tool in $PWD"\n'
TOOL = {"tool": {"description": "a tool", "script": "bin/tool", "cwd": "sub"}}  # project-only: its cwd is here
WARNING = ("This runs the project's files as they are now, like typing the command there. "
           "It isn't reviewed again when they change.")


class TestPromote(ShortcutTestCase):
    def tool_project(self, approved: bool = True):
        return self.project(TOOL, dirs=["sub"], files={"bin/tool": TOOL_SCRIPT}, approved=approved)

    def test_an_exported_entry_promotes_without_its_export(self):
        proj = self.project({"tool": {"script": "bin/tool", "export": True}}, files={"bin/tool": TOOL_SCRIPT}, approved=True)
        res = self.run_cli(["promote", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["tool"], {
            "description": "", "script": str(proj / "bin" / "tool"), "from": {"source": str(proj), "name": "tool"}})
        elsewhere = self.folder("elsewhere")
        self.assertEqual(self.run_cli(["r", "tool"], cwd=str(elsewhere)).stdout, f"tool in {elsewhere}\n")

    def test_an_entry_that_isnt_exported_needs_force(self):
        proj = self.tool_project()
        res = self.run_cli(["promote", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("error: 'tool' is marked project-only; promote anyway with --force\n", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_force_points_at_the_checkout_and_copies_nothing(self):
        proj = self.tool_project()
        before = (proj / ".shortcuts").read_bytes()

        res = self.run_cli(["promote", "tool", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn(WARNING, res.stdout)
        self.assertIn("shortcut r --global tool runs yours", res.stderr)

        self.assertEqual(self.load_global()["tool"], {
            "description": "a tool",
            "script": str(proj / "bin" / "tool"),
            "cwd": str(proj / "sub"),
            "from": {"source": str(proj), "name": "tool"},
        })
        self.assertFalse((self.shortcuts_home / "scripts").exists())
        self.assertEqual((proj / ".shortcuts").read_bytes(), before)
        self.assertEqual(self.run_cli(["r", "tool"]).stdout, f"tool in {proj / 'sub'}\n")

    def test_it_runs_the_project_script_as_it_is_now(self):
        proj = self.tool_project()
        self.assertEqual(self.run_cli(["promote", "tool", "--force", "-y"], cwd=str(proj)).returncode, 0)

        (proj / "bin" / "tool").write_text('#!/bin/sh\necho changed\n')
        res = self.run_cli(["r", "tool"])
        self.assertEqual(res.stdout, "changed\n", res.stderr)

        (proj / "bin" / "tool").unlink()
        res = self.run_cli(["r", "tool"])
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"script file not found: {proj / 'bin' / 'tool'}", res.stderr)

    def test_a_missing_run_directory_fails_loudly(self):
        proj = self.project({"here": {"inline": "pwd", "cwd": "sub"}}, dirs=["sub"], approved=True)
        self.assertEqual(self.run_cli(["promote", "here", "--force", "-y"], cwd=str(proj)).returncode, 0)
        (proj / "sub").rmdir()
        res = self.run_cli(["r", "here"])
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"run directory does not exist: {proj / 'sub'}", res.stderr)

    def test_an_inline_entry_from_the_local_file_keeps_its_fields(self):
        proj = self.project(files={"mine.py": "print('mine')\n"})
        (proj / ".shortcuts.local").write_text(json.dumps({"version": 2, "shortcuts": {
            "hello": {"description": "hi", "inline": 'echo "hi $1"', "confirm": True, "cwd": "caller"},
            "abs": {"inline": "pwd", "cwd": "/"},
            "py": {"script": "mine.py"}}}))
        self.approve(proj / ".shortcuts")

        res = self.run_cli(["promote", "py", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["py"]["script"], str(proj / "mine.py"))

        res = self.run_cli(["promote", "hello", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["hello"], {
            "description": "hi", "inline": 'echo "hi $1"', "cwd": "caller", "confirm": True,
            "from": {"source": str(proj), "name": "hello"}})

        self.assertEqual(self.run_cli(["promote", "abs", "--force", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.load_global()["abs"]["cwd"], "/")
        self.assertFalse((self.shortcuts_home / "scripts").exists())

    def test_promote_needs_approval_first(self):
        proj = self.tool_project(approved=False)
        res = self.run_cli(["promote", "tool", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("isn't approved yet", res.stderr)

        self.approve(proj / ".shortcuts")
        (proj / "bin" / "tool").write_text('#!/bin/sh\necho swapped\n')
        res = self.run_cli(["promote", "tool", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("changed since you approved it", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_a_global_name_clash_needs_as(self):
        self.run_cli(["new", "tool", "--", "echo mine"])
        proj = self.tool_project()
        before = (proj / ".shortcuts").read_bytes()

        res = self.run_cli(["promote", "tool", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("shortcut promote tool --as NEW", res.stderr)
        self.assertEqual(self.load_global()["tool"]["inline"], "echo mine")

        res = self.run_cli(["promote", "tool", "--as", "tool", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("you already have a global 'tool'", res.stderr)

        res = self.run_cli(["promote", "tool", "--as", "projtool", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("still the project's", res.stderr)
        self.assertEqual(self.load_global()["projtool"]["from"], {"source": str(proj), "name": "tool"})
        self.assertEqual(self.load_global()["tool"]["inline"], "echo mine")
        self.assertEqual((proj / ".shortcuts").read_bytes(), before)

    def test_without_a_terminal_it_needs_y(self):
        proj = self.tool_project()
        res = self.run_cli(["promote", "tool", "--force"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("promote 'tool' in a terminal, or pass -y", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_in_a_terminal_it_shows_the_entry_and_warning_then_asks(self):
        proj = self.tool_project()
        code, out = self.run_in_terminal(["promote", "tool", "--force"], cwd=str(proj), keys=[(b"[y/N]", b"n\n")])
        self.assertEqual(code, 1)
        self.assertIn(f'"script": "{proj / "bin" / "tool"}"', out)
        self.assertIn(f'"cwd": "{proj / "sub"}"', out)
        self.assertIn(f'"source": "{proj}"', out)
        self.assertLess(out.index(WARNING), out.index("[y/N]"))
        self.assertIn("Aborted.", out)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

        code, out = self.run_in_terminal(["promote", "tool", "--force"], cwd=str(proj), keys=[(b"[y/N]", b"y\n")])
        self.assertEqual(code, 0, out)
        self.assertIn("tool", self.load_global())

    def test_only_a_project_shortcut_from_inside_a_project(self):
        self.run_cli(["new", "mine", "--", "echo mine"])
        res = self.run_cli(["promote", "mine", "--force", "-y"], cwd=str(self.folder("plain")))
        self.assertEqual(res.returncode, 1)
        self.assertIn("promote runs inside a project", res.stderr)

        proj = self.tool_project()
        res = self.run_cli(["promote", "mine", "--force", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("'mine' isn't a project shortcut here", res.stderr)

    def test_bash_completion_offers_promote_flags(self):
        self.assertIn("promote", self.bash_complete("pro"))
        self.assertEqual(self.bash_complete("promote", "-"), ["--as", "-y", "--force"])


class TestFrom(ShortcutTestCase):
    FROM = {"source": "/somewhere/proj", "name": "tool"}

    def test_show_prints_where_it_came_from(self):
        self.write_store(self.shortcuts_home, {"tool": {"inline": "echo tool", "from": self.FROM}})
        res = self.run_cli(["show", "tool"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("From:        'tool' in /somewhere/proj\n", res.stdout)
        self.assertEqual(json.loads(self.run_cli(["show", "--json", "tool"]).stdout)["entry"]["from"], self.FROM)
        self.run_cli(["new", "plain", "--", "echo plain"])
        self.assertNotIn("From:", self.run_cli(["show", "plain"]).stdout)

    def test_from_is_refused_in_a_project_and_a_namespace(self):
        proj = self.project({"tool": {"inline": "echo tool", "from": self.FROM}})
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("'tool' is not a valid entry: 'from' is only for the global store's shortcuts.json", res.stderr)

        local = self.project(name="local")
        (local / ".shortcuts.local").write_text(json.dumps({"version": 2, "shortcuts": {
            "tool": {"inline": "echo tool", "from": self.FROM}}}))
        self.assertIn("'from' is only for", self.run_cli(["update", "-y"], cwd=str(local)).stderr)

        self.write_import(self.shortcuts_home, "ns", {"version": 2, "shortcuts": {
            "tool": {"inline": "echo tool", "from": self.FROM}}})
        res = self.run_cli(["update", "ns", "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'from' is only for", res.stderr)

    def test_from_must_be_source_and_name(self):
        for bad in ("proj", {"source": "/p"}, {"source": "/p", "name": 1}, {"source": "/p", "name": "t", "x": "y"}):
            self.write_store(self.shortcuts_home, {"tool": {"inline": "echo tool", "from": bad}})
            res = self.run_cli(["r", "tool"])
            self.assertEqual(res.returncode, 1)
            self.assertIn("'from' must be an object with a string 'source' and 'name'", res.stderr)

    def test_origin_is_not_a_json_field(self):
        self.write_store(self.shortcuts_home, {"tool": {"inline": "echo tool", "origin": self.FROM}})
        self.assertIn("unknown field: origin", self.run_cli(["r", "tool"]).stderr)

    def test_init_skips_an_entry_with_from(self):
        src = self.collection({"x": {"inline": "echo x", "from": self.FROM}, "y": {"inline": "echo y"}})
        res = self.run_cli(["init", "--global", str(src), "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("x (invalid entry: 'x' is not a valid entry: 'from' is only for", res.stdout)
        self.assertEqual(sorted(self.load_global()), ["y"])


if __name__ == "__main__":
    unittest.main()
