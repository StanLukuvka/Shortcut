#!/usr/bin/env python3
"""Symlink safety: a symlink in a project or a collection can't redirect a write or a run."""

import unittest

from fixtures import GO, helpers, ShortcutTestCase


class TestSymlinkSafety(ShortcutTestCase):
    def test_a_run_writes_nothing_in_the_project_or_the_store(self):
        proj = self.project(GO)
        self.approve(proj / ".shortcuts")
        before = sorted(self.shortcuts_home.rglob("*")), sorted(proj.rglob("*"))
        self.assertEqual(self.run_cli(["r", "go"], cwd=str(proj)).stdout, "ran\n")
        self.assertEqual((sorted(self.shortcuts_home.rglob("*")), sorted(proj.rglob("*"))), before)

    def test_moving_a_symlink_under_a_relative_cwd_needs_approval(self):
        proj = self.project({"go": {"inline": "pwd", "cwd": "work"}}, dirs=["safe"])
        elsewhere = self.folder("elsewhere")
        (proj / "work").symlink_to(proj / "safe")
        self.approve(proj / ".shortcuts")
        res = self.run_cli(["r", "go"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn(f"→ {proj / 'safe'}", res.stderr)

        (proj / "work").unlink()
        (proj / "work").symlink_to(elsewhere)
        self.assertEqual(self.run_cli(["r", "go"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn(f"+{elsewhere}", res.stdout)
        self.assertIn("--- a/cwd of go\n", res.stdout)
        self.assertEqual(self.run_cli(["r", "go"], cwd=str(proj)).returncode, 0)

    def test_a_store_never_writes_through_a_symlink(self):
        victim = self.write_file(self.tmp_path / "victim", "keep\n")
        for name in (".gitignore", ".lock"):
            proj = self.project(name=f"proj{name}", dirs=[".shortcuts"])
            (proj / ".shortcuts" / name).symlink_to(victim)
            res = self.run_cli(["init"], cwd=str(proj))
            self.assertEqual(res.returncode, 1, name)
            self.assertEqual(victim.read_text(encoding="utf-8"), "keep\n", name)

    def test_local_never_writes_through_a_committed_symlink(self):
        victim = self.write_file(self.tmp_path / "victim", "keep\n")
        proj = self.project({})
        (proj / ".shortcuts.local").symlink_to(victim)
        res = self.run_cli(["new", "build", "--local", "--", "true"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("is a symlink", res.stderr)
        self.assertEqual(victim.read_text(encoding="utf-8"), "keep\n")

    def test_init_skips_symlinked_source_script(self):
        src_repo = self.collection(
            {
                "sym_script": {"description": "symlink script", "script": "scripts/sym_target.sh"},
                "real_script": {"description": "real script", "script": "scripts/real.sh"},
            },
            name="symlink_source_repo",
            version=1,
            files={"scripts/real.sh": "#!/bin/sh\necho real\n"},
            mode=0o755,
        )
        (src_repo / "scripts" / "sym_target.sh").symlink_to(src_repo / "scripts" / "real.sh")

        res = self.run_cli(["init", "--global", str(src_repo), "-y"])
        self.assertEqual(res.returncode, 0)
        self.assertIn("sym_script (symlink in source)", res.stdout)
        self.assertIn("real_script", res.stdout)

        reg = helpers.load_registry(str(self.shortcuts_home))
        self.assertIn("real_script", reg["shortcuts"])
        self.assertNotIn("sym_script", reg["shortcuts"])

        copied_script = self.shortcuts_home / "scripts" / "real_script.sh"
        self.assertTrue(copied_script.is_file())
        self.assertFalse(copied_script.is_symlink())


if __name__ == "__main__":
    unittest.main()
