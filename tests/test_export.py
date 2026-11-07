#!/usr/bin/env python3
"""export: true marks a project entry made to work outside its repo."""

import json
import unittest

from fixtures import ShortcutTestCase


class TestExport(ShortcutTestCase):
    def test_update_needs_an_exported_cwd_to_be_caller_or_absolute(self):
        proj = self.project({"rel": {"inline": "echo rel", "cwd": "sub", "export": True}}, dirs=["sub"])
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("'rel' is exported, so its cwd must be caller or an absolute path, not 'sub'", res.stderr)

        fixed = self.folder("fixed")
        self.write_store(proj / ".shortcuts", {
            "abs": {"inline": "echo abs", "cwd": str(fixed), "export": True},
            "here": {"inline": "echo here", "cwd": "caller", "export": True},
            "copied": {"script": "bin/tool", "export": True},
            "mine": {"inline": "echo mine", "cwd": "sub"},
        })
        self.write_file(proj / "bin" / "tool", "#!/bin/sh\necho tool\n", 0o755)
        self.approve(proj / ".shortcuts")
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
        self.assertIn("rel (invalid entry: 'rel' is exported, so its cwd must be", res.stdout)
        reg = self.load_global()
        self.assertEqual(sorted(reg), ["here", "tool"])
        self.assertEqual(reg["tool"], {"description": "t", "script": "scripts/tool.sh"})
        self.assertNotIn("export", reg["here"])
        self.assertEqual(self.run_cli(["r", "tool"]).stdout, "tool\n")

    def test_a_collection_cant_export(self):
        src = self.collection({"x": {"inline": "echo x", "export": True}, "y": {"inline": "echo y"}})
        res = self.run_cli(["init", "--global", str(src), "-y"])
        self.assertIn("x (invalid entry: 'x' is not a valid entry: 'export' is only for", res.stdout)
        self.assertEqual(sorted(self.load_global()), ["y"])


if __name__ == "__main__":
    unittest.main()
