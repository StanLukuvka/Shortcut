#!/usr/bin/env python3
"""export: true marks a project entry made to work outside its repo."""

import json
import unittest

from fixtures import ShortcutTestCase


class TestExport(ShortcutTestCase):
    def test_update_warns_about_an_exported_relative_cwd_and_approves(self):
        proj = self.project({"rel": {"inline": "pwd", "cwd": "sub", "export": True}}, dirs=["sub"])
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("warning: 'rel' is exported, but its cwd 'sub' only exists in this checkout", res.stderr)
        self.assertIn("Approved", res.stdout)
        self.assertEqual(self.run_cli(["r", "rel"], cwd=str(proj)).stdout, f"{proj / 'sub'}\n")

    def test_update_doesnt_warn_about_other_cwds(self):
        fixed = self.folder("fixed")
        proj = self.project({
            "abs": {"inline": "echo abs", "cwd": str(fixed), "export": True},
            "here": {"inline": "echo here", "cwd": "caller", "export": True},
            "copied": {"script": "bin/tool", "export": True},
            "mine": {"inline": "echo mine", "cwd": "sub"},
        }, dirs=["sub"], files={"bin/tool": "#!/bin/sh\necho tool\n"})
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("warning", res.stderr)
        self.assertEqual(self.run_cli(["r", "abs"], cwd=str(proj)).stdout, "abs\n")
        self.assertIn("Export:      True", self.run_cli(["show", "abs"], cwd=str(proj)).stdout)

    def test_the_local_file_can_export(self):
        proj = self.project()
        (proj / ".shortcuts.local").write_text(json.dumps({"version": 2, "shortcuts": {
            "mine": {"inline": "echo mine", "export": True}}}))
        self.approve(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["r", "mine"], cwd=str(proj)).stdout, "mine\n")

    def test_export_is_refused_in_the_global_store_and_namespaces(self):
        self.write_store(self.shortcuts_home, {"g": {"inline": "echo g", "export": True}})
        res = self.run_cli(["r", "g"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'export' is only for .shortcuts and .shortcuts.local", res.stderr)
        self.assertIn("'export' is only for", self.run_cli(["list"]).stderr)

        self.write_import(self.shortcuts_home, "docker", {
            "version": 2, "shortcuts": {"ps": {"inline": "echo ps", "export": True}}})
        res = self.run_cli(["update", "docker", "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'export' is only for", res.stderr)

    def test_export_must_be_a_boolean(self):
        proj = self.project({"x": {"inline": "echo x", "export": "yes"}})
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("'export' must be a boolean", res.stderr)

    def test_init_from_a_project_imports_only_exported_entries(self):
        proj = self.project({
            "tool": {"description": "t", "script": "bin/tool", "export": True},
            "here": {"inline": "echo here", "export": True},
            "mine": {"inline": "echo mine"},
            "rel": {"inline": "echo rel", "cwd": "sub", "export": True},
        }, files={"bin/tool": "#!/bin/sh\necho tool\n"})
        res = self.run_cli(["init", "--global", str(proj), "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("mine (project-only)", res.stdout)
        self.assertIn("rel (exported, but its cwd 'sub' only exists in the project)", res.stdout)
        reg = self.load_global()
        self.assertEqual(sorted(reg), ["here", "tool"])
        self.assertEqual(reg["tool"], {"description": "t", "script": "scripts/tool.sh"})
        self.assertNotIn("export", reg["here"])
        self.assertEqual(self.run_cli(["r", "tool"]).stdout, "tool\n")

    def test_init_skips_an_exported_cwd_that_isnt_here(self):
        proj = self.project({
            "x": {"inline": "echo x", "cwd": "/nonexistent/xyz", "export": True},
            "y": {"inline": "echo y", "export": True},
        })
        res = self.run_cli(["init", "--global", str(proj), "-y"])
        self.assertIn("x (cwd not found here: /nonexistent/xyz)", res.stdout)
        self.assertEqual(sorted(self.load_global()), ["y"])


if __name__ == "__main__":
    unittest.main()
