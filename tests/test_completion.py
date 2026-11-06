#!/usr/bin/env python3
"""Shell completion."""

import unittest

from fixtures import ShortcutTestCase


class TestCompletion(ShortcutTestCase):
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

    def test_completion_zsh_and_fish(self):
        zsh = self.run_cli(["completion", "zsh"])
        self.assertEqual(zsh.returncode, 0)
        self.assertIn("compdef _shortcut shortcut", zsh.stdout)
        fish = self.run_cli(["completion", "fish"])
        self.assertEqual(fish.returncode, 0)
        self.assertIn("complete -c shortcut", fish.stdout)

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


if __name__ == "__main__":
    unittest.main()
