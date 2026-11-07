#!/usr/bin/env python3
"""A project's two files: .shortcuts, shared, and .shortcuts.local, personal, laid over it."""

import json
import pathlib
import unittest

from fixtures import ShortcutTestCase

SHARED = {
    "keep": {"description": "shared keep", "inline": "echo shared keep"},
    "swap": {"description": "shared swap", "inline": "echo shared swap", "confirm": True},
    "gone": {"description": "shared gone", "inline": "echo shared gone"},
}


def write_local(proj: pathlib.Path, shortcuts: dict) -> None:
    (proj / ".shortcuts.local").write_text(json.dumps({"version": 2, "shortcuts": shortcuts}), encoding="utf-8")


def local_data(proj: pathlib.Path) -> dict:
    return json.loads((proj / ".shortcuts.local").read_text(encoding="utf-8"))["shortcuts"]


def run_here(case: ShortcutTestCase, proj: pathlib.Path, *args: str, **kwargs):
    return case.run_cli(list(args), cwd=str(proj), **kwargs)


def overlaid(case: ShortcutTestCase) -> pathlib.Path:
    """A project with an approved .shortcuts and a .shortcuts.local written by hand and approved."""
    proj = case.project(SHARED, dirs=["src"])
    write_local(proj, {"mine": {"inline": "echo mine"}, "swap": {"inline": "echo local swap"}, "gone": None})
    case.approve(proj / ".shortcuts")
    return proj


class TestOverlay(ShortcutTestCase):
    def test_local_adds_replaces_a_whole_entry_and_hides(self):
        proj = overlaid(self)
        self.assertEqual(run_here(self, proj, "r", "keep").stdout, "shared keep\n")
        self.assertEqual(run_here(self, proj, "r", "mine").stdout, "mine\n")
        # The whole entry: the shared one's confirm doesn't carry over, so this needs no -y
        self.assertEqual(run_here(self, proj, "r", "swap").stdout, "local swap\n")
        res = run_here(self, proj, "r", "gone")
        self.assertEqual(res.returncode, 1)
        self.assertIn("not found", res.stderr)

    def test_a_null_lets_a_global_shortcut_of_that_name_through(self):
        self.run_cli(["new", "gone", "--", "echo global gone"])
        proj = overlaid(self)
        self.assertEqual(run_here(self, proj, "r", "gone").stdout, "global gone\n")

    def test_relative_paths_resolve_from_the_project_root_in_both_files(self):
        proj = self.project(
            {"shared": {"script": "bin/run.sh", "cwd": "."}},
            dirs=["src"],
            files={"bin/run.sh": "#!/usr/bin/env bash\npwd\n"},
        )
        (proj / "bin" / "run.sh").chmod(0o755)
        write_local(proj, {"mine": {"script": "bin/run.sh", "cwd": "src"}})
        self.approve(proj / ".shortcuts")
        self.assertEqual(run_here(self, proj / "src", "r", "shared").stdout, f"{proj}\n")
        self.assertEqual(run_here(self, proj / "src", "r", "mine").stdout, f"{proj / 'src'}\n")

    def test_a_project_can_be_only_a_local_file(self):
        proj = self.project(dirs=["src"])
        self.assertEqual(run_here(self, proj, "new", "solo", "--local", "--", "echo solo").returncode, 0)
        self.assertEqual(run_here(self, proj / "src", "r", "solo").stdout, "solo\n")

    def test_imports_stay_in_the_shared_file(self):
        proj = self.project({})
        self.write_file(proj / ".shortcuts.local", json.dumps({"version": 2, "imports": {"x": "x.shortcut"}, "shortcuts": {}}))
        self.assertIn("can't have 'imports'", run_here(self, proj, "list").stderr)


class TestWrites(ShortcutTestCase):
    def test_the_cli_never_writes_the_shared_file(self):
        proj = overlaid(self)
        before = (proj / ".shortcuts").read_bytes()
        source = self.script("tool.sh", "#!/usr/bin/env bash\n")
        for args in (
            ["new", "a", "--local", "--", "echo a"],
            ["new", "b", "--script", str(source), "--desc", "d", "--local"],
            ["rm", "--local", "keep", "-y"],
            ["rm", "--local", "swap", "-y"],
        ):
            self.assertEqual(run_here(self, proj, *args).returncode, 0, args)
        self.assertEqual((proj / ".shortcuts").read_bytes(), before)

    def test_a_write_without_local_is_refused_for_a_project_entry(self):
        proj = overlaid(self)
        for args in (["rm", "keep", "-y"], ["rm", "mine", "-y"]):
            res = run_here(self, proj, *args)
            self.assertEqual(res.returncode, 1, args)
            self.assertIn("shortcut doesn't change .shortcuts", res.stderr)

    def test_rm_local_hides_a_shared_entry_and_undoes_itself(self):
        proj = overlaid(self)
        self.assertIn("Hid shortcut", run_here(self, proj, "rm", "--local", "keep", "-y").stdout)
        self.assertIsNone(local_data(proj)["keep"])
        self.assertEqual(run_here(self, proj, "r", "keep").returncode, 1)
        self.assertIn("Removed 'keep'", run_here(self, proj, "rm", "--local", "keep", "-y").stdout)
        self.assertEqual(run_here(self, proj, "r", "keep").stdout, "shared keep\n")

    def test_rm_local_of_an_override_brings_the_shared_entry_back(self):
        proj = overlaid(self)
        self.assertEqual(run_here(self, proj, "rm", "--local", "swap", "-y").returncode, 0)
        self.assertEqual(run_here(self, proj, "r", "-y", "swap").stdout, "shared swap\n")

    def test_new_local_overrides_a_shared_name_and_says_so(self):
        proj = overlaid(self)
        res = run_here(self, proj, "new", "keep", "--local", "--", "echo mine now")
        self.assertIn("this overrides 'keep'", res.stderr)
        self.assertEqual(run_here(self, proj, "r", "keep").stdout, "mine now\n")

    def test_new_local_takes_a_command_and_add_records_the_path(self):
        proj = self.project({}, files={"tools/run.sh": "#!/usr/bin/env bash\n"})
        outside = self.script("outside.sh", "#!/usr/bin/env bash\n")
        res = run_here(self, proj, "new", "x", "--local", "--desc", "d", env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("after '--'", res.stderr)
        self.assertEqual(run_here(self, proj, "new", "in", "--script", "tools/run.sh", "--desc", "d", "--local").returncode, 0)
        self.assertEqual(run_here(self, proj, "new", "out", "--script", str(outside), "--desc", "d", "--local").returncode, 0)
        self.assertEqual(local_data(proj)["in"]["script"], "tools/run.sh")
        self.assertEqual(local_data(proj)["out"]["script"], str(outside))
        self.assertEqual(sorted(p.name for p in proj.iterdir()), [".shortcuts", ".shortcuts.local", "tools"])

    def test_local_only_works_at_the_project_root(self):
        proj = overlaid(self)
        res = run_here(self, proj / "src", "new", "x", "--local", "--", "true")
        self.assertEqual(res.returncode, 1)
        self.assertIn("is inside the project at", res.stderr)
        self.assertFalse((proj / "src" / ".shortcuts.local").exists())
        self.assertEqual(run_here(self, proj / "src", "r", "keep").stdout, "shared keep\n")

    def test_a_new_local_file_is_excluded_from_git_once(self):
        proj = self.project({})
        self.run_cmd(["git", "init", "-q", str(proj)])
        run_here(self, proj, "new", "a", "--local", "--", "true")
        run_here(self, proj, "new", "b", "--local", "--", "true")
        exclude = (proj / ".git" / "info" / "exclude").read_text(encoding="utf-8")
        self.assertEqual(exclude.splitlines().count(".shortcuts.local"), 1)
        self.assertEqual(self.run_cmd(["git", "status", "--porcelain"], cwd=str(proj)).stdout, "?? .shortcuts\n")

    def test_outside_git_nothing_is_excluded(self):
        proj = self.project({})
        self.assertEqual(run_here(self, proj, "new", "a", "--local", "--", "true").returncode, 0)
        self.assertFalse((proj / ".git").exists())

    def test_show_json_prints_an_entry_to_paste(self):
        proj = overlaid(self)
        res = run_here(self, proj, "show", "--json", "mine")
        self.assertEqual(json.loads("{" + res.stdout + "}"), {"mine": {"inline": "echo mine"}})


class TestApproval(ShortcutTestCase):
    def test_a_hand_edit_to_local_needs_approving(self):
        proj = overlaid(self)
        write_local(proj, {"mine": {"inline": "echo evil"}})
        res = run_here(self, proj, "r", "keep")
        self.assertEqual(res.returncode, 1)
        self.assertIn("changed since you approved it", res.stderr)
        res = run_here(self, proj, "update", "-y")
        self.assertIn("+++ b/.shortcuts.local", res.stdout)
        self.assertIn("echo evil", res.stdout)
        self.assertEqual(run_here(self, proj, "r", "mine").stdout, "evil\n")

    def test_a_local_file_from_a_clone_needs_approving(self):
        proj = self.project({"keep": {"inline": "echo keep"}})
        write_local(proj, {"evil": {"inline": "echo evil"}})
        res = run_here(self, proj, "r", "evil")
        self.assertEqual(res.returncode, 1)
        self.assertIn("isn't approved yet", res.stderr)

    def test_a_cli_write_keeps_the_approval(self):
        proj = overlaid(self)
        res = run_here(self, proj, "new", "more", "--local", "--", "echo more")
        self.assertNotIn("needs approving", res.stderr)
        self.assertEqual(run_here(self, proj, "r", "more").stdout, "more\n")
        self.assertEqual(run_here(self, proj, "r", "keep").stdout, "shared keep\n")
        self.assertEqual(run_here(self, proj, "rm", "--local", "more", "-y").returncode, 0)
        self.assertEqual(run_here(self, proj, "r", "keep").stdout, "shared keep\n")

    def test_a_cli_write_does_not_approve_a_hand_edit_to_local(self):
        proj = overlaid(self)
        write_local(proj, {"mine": {"inline": "echo evil"}})
        res = run_here(self, proj, "new", "more", "--local", "--", "echo more")
        self.assertIn("needs approving", res.stderr)
        self.assertEqual(run_here(self, proj, "r", "mine").returncode, 1)
        self.assertEqual(run_here(self, proj, "r", "more").returncode, 1)

    def test_a_cli_write_does_not_approve_a_change_to_shared(self):
        proj = overlaid(self)
        self.write_store(proj / ".shortcuts", {**SHARED, "keep": {"inline": "echo evil"}})
        res = run_here(self, proj, "new", "more", "--local", "--", "echo more")
        self.assertIn("needs approving", res.stderr)
        self.assertEqual(run_here(self, proj, "r", "keep").returncode, 1)
        self.assertEqual(run_here(self, proj, "r", "more").returncode, 1)

    def test_a_cli_write_to_a_fresh_clone_does_not_approve_shared(self):
        proj = self.project({"keep": {"inline": "echo keep"}})
        res = run_here(self, proj, "new", "more", "--local", "--", "echo more")
        self.assertIn("needs approving", res.stderr)
        res = run_here(self, proj, "r", "keep")
        self.assertEqual(res.returncode, 1)
        self.assertIn("isn't approved yet", res.stderr)
        res = run_here(self, proj, "update", "-y")
        self.assertIn("+++ b/.shortcuts\n", res.stdout)
        self.assertEqual(run_here(self, proj, "r", "keep").stdout, "keep\n")

    def test_a_script_named_in_local_is_covered(self):
        proj = self.project({}, files={"run.sh": "#!/usr/bin/env bash\necho one\n"})
        (proj / "run.sh").chmod(0o755)
        self.assertEqual(run_here(self, proj, "new", "go", "--script", "run.sh", "--desc", "d", "--local").returncode, 0)
        self.approve(proj / ".shortcuts")
        self.assertEqual(run_here(self, proj, "r", "go").stdout, "one\n")
        (proj / "run.sh").write_text("#!/usr/bin/env bash\necho two\n", encoding="utf-8")
        self.assertEqual(run_here(self, proj, "r", "go").returncode, 1)

    def test_a_diff_marks_what_local_overrides_or_hides(self):
        proj = self.project(SHARED)
        write_local(proj, {"swap": {"inline": "echo local swap"}, "gone": None})
        res = run_here(self, proj, "update", "-y")
        self.assertIn("'swap' in .shortcuts is overridden by .shortcuts.local for you.", res.stdout)
        self.assertIn("'gone' in .shortcuts is hidden by .shortcuts.local for you.", res.stdout)


class TestList(ShortcutTestCase):
    def test_list_shows_each_entrys_source(self):
        res = run_here(self, overlaid(self), "list")
        self.assertRegex(res.stdout, r"keep +caller +shared ")
        self.assertRegex(res.stdout, r"mine +caller +local ")
        self.assertRegex(res.stdout, r"swap +caller +overridden ")
        self.assertRegex(res.stdout, r"gone +caller +hidden ")

    def test_a_hidden_name_is_not_offered(self):
        names = run_here(self, overlaid(self), "list", "--names").stdout.split()
        self.assertIn("keep", names)
        self.assertNotIn("gone", names)

    def test_show_names_the_file_that_defines_it(self):
        proj = overlaid(self)
        res = run_here(self, proj, "show", "swap")
        self.assertIn(f"Defined in:  {proj / '.shortcuts.local'}", res.stdout)
        self.assertIn("Source:      overridden", res.stdout)


class TestImportFromAProject(ShortcutTestCase):
    def test_a_project_can_be_a_collection(self):
        proj = self.project({"tool": {"description": "t", "inline": "echo tool"}})
        res = self.run_cli(["init", "--global", str(proj), "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("tool", self.load_global())


if __name__ == "__main__":
    unittest.main()
