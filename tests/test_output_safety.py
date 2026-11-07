#!/usr/bin/env python3
"""Output safety: what a store holds can't rewrite the terminal or fake a prompt."""

import unittest

from fixtures import BIN_TOOL, CLI_SHELL, ShortcutTestCase


class TestOutputSafety(ShortcutTestCase):
    def test_stored_text_cannot_rewrite_the_terminal(self):
        self.write_store(self.shortcuts_home, {
            "go": {"description": "clean\x1b[2K\rfaked \x9b1m \u202egnp", "inline": "printf '\\033[31mred\\033[0m'"},
        })
        for args in (["list"], ["show", "go"], ["show", "--json", "go"]):
            res = self.run_cli(args)
            self.assertEqual(res.returncode, 0, res.stderr)
            for char in ("\x1b", "\r", "\x9b", "\u202e"):
                self.assertNotIn(char, res.stdout + res.stderr, args)
        self.assertIn("clean^[[2K^Mfaked", self.run_cli(["show", "go"]).stdout)
        # A shortcut's own output is its own business
        self.assertEqual(self.run_cli(["r", "go"]).stdout, "\x1b[31mred\x1b[0m")

    def test_a_terminal_prompt_cannot_be_forged(self):
        proj = self.project(name="proj\x1b[2Kx")
        self.write_store(self.shortcuts_home, {"go": {"inline": "echo ran", "confirm": True, "cwd": str(proj)}})
        code, output = self.run_in_terminal(["r", "go"], keys=[(b"[y/N]", b"n\n")])
        self.assertNotIn("\x1b[2Kx", output)
        self.assertIn("^[[2Kx", output)

    def test_help_stays_plain_on_a_terminal(self):
        code, output = self.run_in_terminal(["new", "--help"])
        self.assertEqual(code, 0, output)
        self.assertNotIn("^[", output)
        self.assertNotIn("\x1b", output)

    def test_escape_codes_in_a_diff_cannot_touch_the_terminal(self):
        proj = self.project(BIN_TOOL, files={"bin/tool": "echo \x1b[2Jhidden\r\n"})
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertNotIn("\x1b", res.stdout)
        self.assertIn("^[[2Jhidden", res.stdout)

    def test_a_closed_stream_changes_nothing(self):
        self.run_cli(["new", "go", "--", "echo ran"])
        for shell in ("{cmd} list >&-", "{cmd} r go 2>&-"):
            res = self.run_cmd(["bash", "-c", shell.format(cmd=CLI_SHELL)])
            self.assertEqual(res.returncode, 0, shell + res.stderr)


if __name__ == "__main__":
    unittest.main()
