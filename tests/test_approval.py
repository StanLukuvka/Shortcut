#!/usr/bin/env python3
"""Approval and diffs: nothing in a project runs until it's reviewed, and any change asks again."""

import json
import os
import unittest

from fixtures import BIN_TOOL, DOCKER_PS, GO, HI, KUBE_GET, OWN, shortcut_mod, ShortcutTestCase


class TestApproval(ShortcutTestCase):
    def test_approval_blocks_until_approved_and_after_any_change(self):
        data = {"version": 2, "shortcuts": {
            "hi": {"description": "Say hi", "script": "scripts/hi.sh"},
            "there": {"description": "There", "inline": "echo there"},
        }}
        proj = self.project(data["shortcuts"], files={"scripts/hi.sh": "#!/usr/bin/env bash\necho hi\n"})
        script = proj / "scripts" / "hi.sh"
        registry = proj / ".shortcuts"

        # First run blocked; full listing on first approval
        res = self.run_cli(["r", "hi"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("isn't approved yet", res.stderr)
        self.assertNotIn("hi\n", res.stdout)
        res = self.run_cli(["update"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("in a terminal, or pass -y", res.stderr)
        self.assertIn("+++ b/.shortcuts\n", res.stdout)
        self.assertIn("+++ b/scripts/hi.sh", res.stdout)
        self.assertIn("+echo hi", res.stdout)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)

        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "hi\n")
        self.assertIn("already approved", self.run_cli(["update", "."], cwd=str(proj)).stdout)
        self.assertIn("Nothing to approve here", self.run_cli(["update"], cwd=str(proj)).stdout)

        # Reordered keys and reformatting change nothing
        data_reordered = {"shortcuts": dict(reversed(list(data["shortcuts"].items()))), "version": 2}
        registry.write_text(json.dumps(data_reordered, indent=7), encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 0)

        # A change to the JSON blocks, also the shortcuts that didn't change
        data["shortcuts"]["there"]["inline"] = "echo changed"
        registry.write_text(json.dumps(data), encoding="utf-8")
        res = self.run_cli(["r", "hi"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"{proj} changed since you approved it; run: shortcut update", res.stderr)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn('-    "there": {', res.stdout.replace('-      "inline": "echo there"', '-    "there": {'))
        self.assertIn('+      "inline": "echo changed"', res.stdout)
        self.assertNotIn('+    "version"', res.stdout)

        # So does a change to a script
        script.write_text("#!/usr/bin/env bash\necho evil\n", encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("--- a/scripts/hi.sh", res.stdout)
        self.assertIn("-echo hi", res.stdout)
        self.assertIn("+echo evil", res.stdout)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "evil\n")

        # The script going away is a change, and can't be approved while the entry needs it
        script.unlink()
        self.assertEqual(self.run_cli(["r", "there"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("script not found", res.stderr)

    def test_approval_prompt_in_terminal_and_colour(self):
        proj = self.project(HI)
        rc, out = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"approve? [y/N]", b"n\n")])
        self.assertEqual(rc, 1)
        self.assertIn("Aborted.", out)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).returncode, 1)
        rc, out = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"approve? [y/N]", b"y\n")], env={"PAGER": ""})
        self.assertEqual(rc, 0)
        self.assertIn("\x1b[32m+", out)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "hi\n")
        self.assertNotIn("\x1b[", self.run_cli(["update", "."], cwd=str(proj)).stdout)

    def test_approval_is_kept_outside_what_it_approves(self):
        proj = self.project(HI)
        before = {p for p in proj.rglob("*")}
        self.approve(proj / ".shortcuts")
        self.assertEqual({p for p in proj.rglob("*")}, before)
        records = list((self.shortcuts_home / "approvals").glob("*.json"))
        self.assertEqual(len(records), 1)
        record = json.loads(records[0].read_text())
        self.assertEqual(record["key"], str(proj))
        self.assertIn(".shortcuts", record["snapshot"])

    def test_one_changed_group_leaves_the_others_running(self):
        proj = self.project(OWN)
        docker = self.write_import(proj / ".shortcuts", "docker", DOCKER_PS)
        self.write_import(proj / ".shortcuts", "kube", KUBE_GET)
        self.approve(proj / ".shortcuts")

        docker.write_text('{"version": 2, "shortcuts": {"ps": {"inline": "echo evil"}}}', encoding="utf-8")
        res = self.run_cli(["r", "docker", "ps"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("docker changed since you approved it; run: shortcut update docker", res.stderr)
        self.assertEqual(self.run_cli(["r", "kube", "get"], cwd=str(proj)).stdout, "get\n")
        self.assertEqual(self.run_cli(["r", "own"], cwd=str(proj)).stdout, "own\n")

        self.assertEqual(self.run_cli(["update", "docker", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "ps"], cwd=str(proj)).stdout, "evil\n")

    def test_swapping_two_approved_namespaces_needs_approval(self):
        proj = self.project(OWN)
        self.write_import(proj / ".shortcuts", "dev", {"version": 2, "shortcuts": {"deploy": {"inline": "echo dev"}}})
        self.write_import(proj / ".shortcuts", "prod", {"version": 2, "shortcuts": {"deploy": {"inline": "echo prod"}}})
        self.approve(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["r", "dev", "deploy"], cwd=str(proj)).stdout, "dev\n")

        registry = json.loads((proj / ".shortcuts").read_text(encoding="utf-8"))
        registry["imports"] = {"dev": "imports/prod.shortcut", "prod": "imports/dev.shortcut"}
        (proj / ".shortcuts").write_text(json.dumps(registry), encoding="utf-8")
        res = self.run_cli(["r", "dev", "deploy"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("dev isn't approved yet; review it with: shortcut update dev", res.stderr)
        self.assertNotIn("prod", res.stdout)

        self.assertEqual(self.run_cli(["update", "dev", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["r", "dev", "deploy"], cwd=str(proj)).stdout, "prod\n")

    def test_a_namespace_approved_in_one_project_is_new_in_another(self):
        first = self.project(OWN, name="first")
        tools = self.write_import(first / ".shortcuts", "tools", {"version": 2, "shortcuts": {"go": {"inline": "echo go"}}})
        self.approve(first / ".shortcuts")
        second = self.project({}, name="second", imports={"tools": str(tools)})
        res = self.run_cli(["r", "tools", "go"], cwd=str(second))
        self.assertEqual(res.returncode, 1)
        self.assertIn("tools isn't approved yet", res.stderr)
        self.assertEqual(self.run_cli(["r", "tools", "go"], cwd=str(first)).stdout, "go\n")
        self.assertIn(f"New to you: tools ({os.path.realpath(tools)})", self.run_cli(["update", "-y"], cwd=str(second)).stdout)

    def test_a_script_cannot_stand_in_for_a_cwd_record(self):
        proj = self.project(
            {
                "go": {"inline": "pwd", "cwd": "work"},
                "pad": {"script": "cwd of go"},
            },
            dirs=["safe"],
            files={"cwd of go": "x"},
        )
        (proj / "work").symlink_to(proj / "safe")
        store = shortcut_mod.load_store(str(proj))
        files = shortcut_mod.shared_files(store.main)
        self.assertEqual(files["cwd of go"], "x")
        self.assertEqual(files["./cwd of go"], f"{os.path.realpath(proj / 'safe')}\n")

    def test_approval_covers_a_project_script_linked_from_outside(self):
        outside = self.write_file(self.tmp_path / "outside.sh", "echo one\n")
        proj = self.project({"go": {"script": str(outside)}}, approved=True)
        outside.write_text("echo two\n", encoding="utf-8")
        res = self.run_cli(["r", "go"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("changed since you approved it", res.stderr)

    def test_approving_says_what_it_covers(self):
        proj = self.project(GO)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("not what they call", res.stdout)

    def test_a_cwd_with_a_nul_does_not_stop_the_tools_that_find_it(self):
        proj = self.project(GO, approved=True)
        self.write_store(proj / ".shortcuts", {"go": {"inline": "echo ran", "cwd": "a\0b"}})
        for args in (["list"], ["update", "-y"]):
            res = self.run_cli(args, cwd=str(proj))
            self.assertNotIn("embedded null", res.stderr, args)
            self.assertNotIn("Traceback", res.stderr, args)


class TestDiffs(ShortcutTestCase):
    def test_diff_details(self):
        old = {"a.txt": "one\ntwo\n", "gone.txt": "bye\n", "same.txt": "x\n", "bin": shortcut_mod.BINARY_MARK + "aa"}
        new = {"a.txt": "one\nthree\n", "added.txt": "hi\n", "same.txt": "x\n", "bin": shortcut_mod.BINARY_MARK + "bb"}
        lines = shortcut_mod.diff_files(old, new)
        self.assertIn("--- a/a.txt", lines)
        self.assertIn("+++ b/a.txt", lines)
        self.assertIn("-two", lines)
        self.assertIn("+three", lines)
        self.assertIn("-bye", lines)
        self.assertIn("+hi", lines)
        self.assertIn("Binary file bin changed", lines)
        self.assertFalse([line for line in lines if "same.txt" in line])

        # A new empty file is still a difference you can see
        self.assertTrue(shortcut_mod.diff_files({}, {"empty": ""}))

    def test_non_utf8_script(self):
        proj = self.project(BIN_TOOL, files={"bin/tool": b"#!/bin/sh\n# \xff\xfe\necho one\n"}, approved=True)
        scripts = proj / "bin"
        self.assertEqual(self.run_cli(["r", "tool"], cwd=str(proj)).stdout, "one\n")
        (scripts / "tool").write_bytes(b"#!/bin/sh\n# \xff\xfd\necho two\n")
        self.assertEqual(self.run_cli(["r", "tool"], cwd=str(proj)).returncode, 1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertNotIn("Binary file", res.stdout)
        self.assertIn("+echo two", res.stdout)
        self.assertIn("+# \\xff\\xfd", res.stdout)

    def test_a_nul_cannot_hide_a_script_bash_would_run(self):
        proj = self.project(BIN_TOOL, files={"bin/tool": b"#!/bin/bash\necho PWNED\n\0"})
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("+echo PWNED", res.stdout)
        self.assertNotIn("Binary file", res.stdout)

    def test_a_text_file_cannot_forge_either_mark(self):
        bad = b"a \xff\n"
        shown = shortcut_mod.reviewable(bad)
        self.assertNotEqual(shortcut_mod.reviewable(shown.encode()), shown)
        forged = shortcut_mod.BINARY_MARK.encode() + b"echo hidden\n"
        self.assertTrue(shortcut_mod.reviewable(forged).startswith(shortcut_mod.BINARY_MARK))
        self.assertNotIn("echo hidden", shortcut_mod.reviewable(forged))

    def test_invalid_utf8_and_its_escaped_text_are_different_files(self):
        one = shortcut_mod.reviewable(b"a \xff\n")
        two = shortcut_mod.reviewable(b"a \\xff\n")
        self.assertNotEqual(one, two)
        self.assertEqual(shortcut_mod.reviewable(b"plain\n"), "plain\n")

    def test_a_binary_needs_typing_out_at_the_prompt(self):
        proj = self.project(BIN_TOOL, files={"bin/tool": b"\x7fELF\0\0"})
        code, output = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"Type binary", b"y\n")])
        self.assertEqual(code, 1, output)
        self.assertIn("Aborted.", output)
        code, output = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"Type binary", b"binary\n")])
        self.assertEqual(code, 0, output)
        self.assertIn("Approved", output)
        # Once seen and unchanged, it's an ordinary prompt
        (proj / "bin" / "other").write_text("echo hi\n", encoding="utf-8")
        self.write_store(proj / ".shortcuts", {"tool": {"script": "bin/tool"}, "other": {"script": "bin/other"}})
        code, output = self.run_in_terminal(["update"], cwd=str(proj), keys=[(b"approve? [y/N]", b"y\n")])
        self.assertEqual(code, 0, output)

    def test_a_binary_replacing_text_says_so_and_text_replacing_a_binary_shows(self):
        lines = shortcut_mod.diff_files({"t": "echo hi\n"}, {"t": shortcut_mod.BINARY_MARK + "aa"})
        self.assertIn("Binary file t changed", lines)
        lines = shortcut_mod.diff_files({"t": shortcut_mod.BINARY_MARK + "aa"}, {"t": "echo hi\n"})
        self.assertIn("+echo hi", lines)


if __name__ == "__main__":
    unittest.main()
