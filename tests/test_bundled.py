#!/usr/bin/env python3
"""What ships with shortcut: install.sh, and the bundled shortcuts in templates/."""

import os
import unittest

from fixtures import REPO, ShortcutTestCase, TEMPLATES


class TestBundled(ShortcutTestCase):
    def test_bundled_templates(self):
        templates_dir = TEMPLATES
        self.assertTrue((templates_dir / "shortcuts.json").is_file())

        search_script = templates_dir / "scripts" / "search.sh"
        self.assertTrue(search_script.is_file())

        # search must print usage and exit 1 when given no pattern
        res_no_arg = self.run_cmd([str(search_script)])
        self.assertEqual(res_no_arg.returncode, 1)
        self.assertIn("usage:", res_no_arg.stderr)

        # search finds pattern when given
        self.write_file(self.tmp_path / "target_search_me.txt", "hello")
        res_arg = self.run_cmd([str(search_script), "search_me", str(self.tmp_path)])
        self.assertEqual(res_arg.returncode, 0)
        self.assertIn("target_search_me.txt", res_arg.stdout)

        # Import templates via shortcut init --global
        res_init = self.run_cli(["init", "--global", str(templates_dir), "-y"])
        self.assertEqual(res_init.returncode, 0)
        self.assertIn("search", res_init.stdout)
        self.assertIn("serve", res_init.stdout)
        self.assertIn("ports", res_init.stdout)
        self.assertNotIn("Skipped", res_init.stdout)
        for name in ("nano", "log", "desktop"):
            self.assertIn(name, self.load_global())

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
        self.write_file(self.tool_bin / "nano", '#!/bin/sh\necho "nano opened $1"\n', mode=0o755)
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
        proj = self.project()
        self.run_cli(["new", "local_desk", "--local", "--", "echo 1"], cwd=str(proj))
        res = self.run_cli(["r", "desktop", "local_desk"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("not found", res.stdout)  # a logged run merges stderr into stdout
        self.assertFalse((self.xdg_data_home / "applications" / "shortcut-local_desk.desktop").exists())

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

    def test_rm_local_keeps_global_desktop_file(self):
        self.install_bundled()
        proj_dir = self.project({}, name="penpot_proj")
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

    def test_install_script_dry_run(self):
        install_script = REPO / "install.sh"
        self.assertTrue(install_script.is_file())

        test_home = self.tmp_path / "inst_home"
        bin_dir = test_home / "bin"
        xdg_data = test_home / "share"
        shortcuts_home = test_home / ".shortcuts"

        test_env = {
            "HOME": str(test_home),
            "BIN_DIR": str(bin_dir),
            "XDG_DATA_HOME": str(xdg_data),
            "SHORTCUTS_HOME": str(shortcuts_home),
        }

        res = self.run_cmd([str(install_script)], cwd=str(self.tmp_path), env=test_env)
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


if __name__ == "__main__":
    unittest.main()
