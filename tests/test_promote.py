#!/usr/bin/env python3
"""shortcut promote: a copy of an approved project shortcut becomes one of yours."""

import json
import os
import unittest

from fixtures import ShortcutTestCase

TOOL_SCRIPT = '#!/bin/sh\necho "tool in $PWD"\n'
TOOL = {"tool": {"description": "a tool", "script": "bin/tool", "cwd": "sub"}}


class TestPromote(ShortcutTestCase):
    def tool_project(self, approved: bool = True):
        return self.project(TOOL, dirs=["sub"], files={"bin/tool": TOOL_SCRIPT}, approved=approved)

    def test_promote_copies_the_script_and_makes_cwd_absolute(self):
        proj = self.tool_project()
        before = (proj / ".shortcuts").read_bytes()

        res = self.run_cli(["promote", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn('echo "tool in $PWD"', res.stdout)
        self.assertIn(f"It runs in {proj / 'sub'}, which was 'sub' relative to {proj}.", res.stdout)
        self.assertIn("shortcut r --global tool runs yours", res.stderr)

        entry = self.load_global()["tool"]
        self.assertEqual(entry["script"], "scripts/tool.sh")
        self.assertEqual(entry["cwd"], str(proj / "sub"))
        self.assertEqual(entry["description"], "a tool")
        copy = self.shortcuts_home / "scripts" / "tool.sh"
        self.assertFalse(copy.is_symlink())
        self.assertEqual(copy.read_text(), TOOL_SCRIPT)
        self.assertTrue(os.access(copy, os.X_OK))
        self.assertEqual((proj / ".shortcuts").read_bytes(), before)

        # The copy is its own: a later change in the project doesn't reach it
        (proj / "bin" / "tool").write_text('#!/bin/sh\necho changed\n')
        res = self.run_cli(["r", "tool"])
        self.assertEqual(res.stdout, f"tool in {proj / 'sub'}\n", res.stderr)

    def test_an_inline_entry_from_the_local_file_keeps_its_fields(self):
        proj = self.project(files={"mine.py": "print('mine')\n"})
        (proj / ".shortcuts.local").write_text(json.dumps({"version": 2, "shortcuts": {
            "hello": {"description": "hi", "inline": 'echo "hi $1"', "confirm": True, "cwd": "caller"},
            "py": {"script": "mine.py"}}}))
        self.approve(proj / ".shortcuts")

        res = self.run_cli(["promote", "py", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["py"]["script"], "scripts/py.py")
        self.assertEqual((self.shortcuts_home / "scripts" / "py.py").read_text(), "print('mine')\n")

        res = self.run_cli(["promote", "hello", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("It runs in", res.stdout)
        self.assertEqual(self.load_global()["hello"]["inline"], 'echo "hi $1"')
        self.assertEqual(self.load_global()["hello"]["confirm"], True)
        self.assertEqual(self.load_global()["hello"]["cwd"], "caller")
        self.assertEqual(sorted(p.name for p in (self.shortcuts_home / "scripts").iterdir()), ["py.py"])

    def test_promote_needs_approval_first(self):
        proj = self.tool_project(approved=False)
        res = self.run_cli(["promote", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("isn't approved yet", res.stderr)

        self.approve(proj / ".shortcuts")
        (proj / "bin" / "tool").write_text('#!/bin/sh\necho swapped\n')
        res = self.run_cli(["promote", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("changed since you approved it", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_a_global_name_clash_needs_as(self):
        self.run_cli(["new", "tool", "--", "echo mine"])
        proj = self.tool_project()

        res = self.run_cli(["promote", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("shortcut promote tool --as NEW", res.stderr)
        self.assertEqual(self.load_global()["tool"]["inline"], "echo mine")

        res = self.run_cli(["promote", "tool", "--as", "tool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("you already have a global 'tool'", res.stderr)

        res = self.run_cli(["promote", "tool", "--as", "projtool", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("still the project's", res.stderr)
        self.assertEqual(self.load_global()["projtool"]["script"], "scripts/projtool.sh")
        self.assertEqual(self.load_global()["tool"]["inline"], "echo mine")

    def test_without_a_terminal_it_needs_y(self):
        proj = self.tool_project()
        res = self.run_cli(["promote", "tool"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("promote 'tool' in a terminal, or pass -y", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_in_a_terminal_it_shows_the_entry_and_script_then_asks(self):
        proj = self.tool_project()
        code, out = self.run_in_terminal(["promote", "tool"], cwd=str(proj), keys=[(b"[y/N]", b"n\n")])
        self.assertEqual(code, 1)
        self.assertIn('"script": "scripts/tool.sh"', out)
        self.assertIn('echo "tool in $PWD"', out)
        self.assertIn("cwd: sub becomes", out)
        self.assertIn("Aborted.", out)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

        code, out = self.run_in_terminal(["promote", "tool"], cwd=str(proj), keys=[(b"[y/N]", b"y\n")])
        self.assertEqual(code, 0, out)
        self.assertIn("tool", self.load_global())

    def test_only_a_project_shortcut_from_inside_a_project(self):
        self.run_cli(["new", "mine", "--", "echo mine"])
        res = self.run_cli(["promote", "mine", "-y"], cwd=str(self.folder("plain")))
        self.assertEqual(res.returncode, 1)
        self.assertIn("promote runs inside a project", res.stderr)

        proj = self.tool_project()
        res = self.run_cli(["promote", "mine", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("'mine' isn't a project shortcut here", res.stderr)

    def test_bash_completion_offers_promote_flags(self):
        self.assertIn("promote", self.bash_complete("pro"))
        self.assertEqual(self.bash_complete("promote", "-"), ["--as", "-y"])


if __name__ == "__main__":
    unittest.main()
