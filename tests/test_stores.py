#!/usr/bin/env python3
"""Stores and namespaces: where shortcuts live, how they're found, and what happens when a store is bad."""

import json
import os
import sys
import unittest

from fixtures import DOCKER_PS, OWN, shortcut_mod, ShortcutTestCase


class TestStores(ShortcutTestCase):
    def test_atomic_registry_save_and_corrupt_protection(self):
        data = {
            "version": 1,
            "shortcuts": {
                "test": {"description": "test", "inline": "echo 1"}
            }
        }
        shortcut_mod.save_registry_atomic(str(self.shortcuts_home), data)
        loaded = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(loaded, data)

        # Corrupt JSON file protection
        reg_file = self.write_file(self.shortcuts_home / "shortcuts.json", '{"version": 1, corrupt json!')

        with self.assertRaises(ValueError) as ctx:
            shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("corrupt registry", str(ctx.exception))

        with self.assertRaises(ValueError) as ctx2:
            shortcut_mod.save_registry_atomic(str(self.shortcuts_home), data)
        self.assertIn("cannot overwrite corrupt registry", str(ctx2.exception))

        # Verify the corrupt file was not replaced or overwritten
        self.assertEqual(reg_file.read_text(encoding="utf-8"), '{"version": 1, corrupt json!')

    def test_store_discovery_skips_global_root(self):
        # Write shortcuts.json in global store
        shortcut_mod.save_registry_atomic(str(self.shortcuts_home), {"version": 1, "shortcuts": {}})

        # Walking up from home or subdirectory must skip ~/.shortcuts
        self.assertIsNone(shortcut_mod.find_project_root(str(self.home_dir)))
        deep_dir = self.home_dir / "a" / "b" / "c"
        deep_dir.mkdir(parents=True)
        self.assertIsNone(shortcut_mod.find_project_root(str(deep_dir)))

        # Walking up from inside ~/.shortcuts itself must return None
        self.assertIsNone(shortcut_mod.find_project_root(str(self.shortcuts_home)))

        # A real local store in a project must be found
        proj_dir = self.home_dir / "my_project"
        self.write_store(proj_dir / ".shortcuts", {})

        sub_dir = proj_dir / "src" / "pkg"
        sub_dir.mkdir(parents=True)
        self.assertEqual(shortcut_mod.find_project_root(str(sub_dir)), str(proj_dir.resolve()))

    def test_scope_resolution_order(self):
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {"version": 2, "shortcuts": {"build": {"description": "Global build", "inline": "echo global_build"}}},
        )
        proj_dir = self.home_dir / "my_project"
        self.write_store(proj_dir / ".shortcuts", {"build": {"description": "Local project build", "inline": "echo local_build"}})

        # Inside a project: project precedence
        res_local, _ = shortcut_mod.resolve_shortcut(["build"], caller_cwd=str(proj_dir))
        self.assertEqual(res_local.group.store_dir, str(proj_dir.resolve()))
        self.assertEqual(res_local.entry.description, "Local project build")
        self.assertTrue(res_local.hides_global)

        # An unapproved project shortcut is refused, not swapped for the global one
        res_blocked = self.run_cli(["run", "build"], cwd=str(proj_dir))
        self.assertEqual(res_blocked.returncode, 1)
        self.assertIn("isn't approved yet", res_blocked.stderr)
        self.assertEqual(self.run_cli(["run", "--global", "build"], cwd=str(proj_dir)).stdout, "global_build\n")

        # Outside the project: the global one
        res_global, _ = shortcut_mod.resolve_shortcut(["build"], caller_cwd=str(self.home_dir))
        self.assertEqual(res_global.group.store_dir, str(self.shortcuts_home.resolve()))
        self.assertEqual(res_global.entry.description, "Global build")

        res_cli = self.run_cli(["run", "check"], cwd=str(self.home_dir))
        self.assertNotEqual(res_cli.returncode, 0)
        self.assertIn("shortcut 'check' not found", res_cli.stderr)

    def test_creation_scope_rules(self):
        proj_dir = self.project(name="fresh_proj", dirs=["sub"])

        # 1. The global store by default, wherever you are
        self.assertEqual(self.run_cli(["new", "anywhere", "--", "echo 1"], cwd=str(proj_dir)).returncode, 0)
        self.assertIn("anywhere", self.load_global())
        self.assertFalse((proj_dir / ".shortcuts").exists())

        # 2. --local creates .shortcuts.local on demand, and what it adds runs straight away
        res = self.run_cli(["new", "local_cmd", "--local", "--", "echo local"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("needs approving", res.stderr)
        self.assertTrue((proj_dir / ".shortcuts.local").is_file())
        self.assertFalse((proj_dir / ".shortcuts").exists())
        self.assertEqual(self.run_cli(["run", "local_cmd"], cwd=str(proj_dir)).stdout, "local\n")

        # 3. --cwd is stored against the defining file: absolute globally, relative inside the project
        res = self.run_cli(["new", "fixed", "--cwd", str(self.tmp_path), "--", "pwd"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["fixed"]["cwd"], str(self.tmp_path))
        res = self.run_cli(["new", "in_sub", "--local", "--cwd", "sub", "--", "pwd"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0, res.stderr)
        project = json.loads((proj_dir / ".shortcuts.local").read_text())["shortcuts"]
        self.assertEqual(project["in_sub"]["cwd"], "sub")
        res = self.run_cli(["new", "explicit", "--cwd", "caller", "--", "pwd"])
        self.assertEqual(self.load_global()["explicit"]["cwd"], "caller")

        res = self.run_cli(["new", "nowhere", "--cwd", "/nonexistent/path/xyz", "--", "echo 1"])
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("--cwd directory does not exist", res.stderr)

        # 4. Duplicate name error
        res = self.run_cli(["new", "local_cmd", "--local", "--", "echo duplicate"], cwd=str(proj_dir))
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("already exists in", res.stderr)

    def test_init_does_not_create_a_project(self):
        proj_dir = self.project(name="empty_init_proj")
        res = self.run_cli(["init"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("a file you write by hand", res.stderr)
        self.assertEqual(list(proj_dir.iterdir()), [])

    def test_new_in_home_refuses_local_store(self):
        res = self.run_cli(["new", "hello", "--local", "--", "echo hi"], cwd=str(self.home_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("holds the global store", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "hello" in self.load_global())

    def test_default_home_store_is_never_a_project(self):
        """With SHORTCUTS_HOME elsewhere, ~/.shortcuts is still not a project of anything under ~."""
        self.run_cli(["init", "--global"], env={"SHORTCUTS_HOME": ""})
        real = self.tmp_path / "custom_store"
        env = {"SHORTCUTS_HOME": str(real)}
        (self.home_dir / "work").mkdir()
        res = self.run_cli(["new", "here", "--local", "--", "echo 1"], cwd=str(self.home_dir), env=env)
        self.assertEqual(res.returncode, 1)
        self.assertIn("holds the global store", res.stderr)
        self.assertEqual(self.load_global(), {})
        res = self.run_cli(["new", "here", "--local", "--", "echo 1"], cwd=str(self.home_dir / "work"), env=env)
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertTrue((self.home_dir / "work" / ".shortcuts.local").is_file())
        self.assertEqual(self.load_global(), {})

    def test_local_only_uses_a_store_in_this_directory(self):
        proj = self.project({"up": {"inline": "true"}}, dirs=["sub"])
        registry = proj / ".shortcuts"
        before = registry.read_text(encoding="utf-8")
        source = self.script("tool.sh", "#!/usr/bin/env bash\n", mode=0o644)

        for cmd in (["new", "x", "--local", "--", "true"], ["new", "x", "--script", str(source), "--desc", "d", "--local"]):
            res = self.run_cli(cmd, cwd=str(proj / "sub"))
            self.assertEqual(res.returncode, 1, cmd)
            self.assertIn("is inside the project at", res.stderr)
        self.assertFalse((proj / "sub" / ".shortcuts.local").exists())
        self.assertEqual(registry.read_text(encoding="utf-8"), before)

    def test_local_refuses_anywhere_in_the_global_store(self):
        self.run_cli(["init", "--global"])
        for folder in (self.shortcuts_home, self.shortcuts_home / "scripts"):
            folder.mkdir(exist_ok=True)
            for cmd in (["new", "z", "--local", "--", "true"], ["rm", "--local", "z", "-y"]):
                res = self.run_cli(cmd, cwd=str(folder), timeout=10)
                self.assertEqual(res.returncode, 1, (folder, cmd, res.stderr))
                self.assertIn("global store", res.stderr)
        self.assertEqual(self.load_global(), {})
        self.assertFalse((self.shortcuts_home / ".shortcuts.local").exists())

    def test_new_takes_one_source(self):
        proj = self.project({})
        source = self.script("tool.sh", "#!/usr/bin/env bash\n")
        for cmd, message in (
            (["new", "x", "--script", str(source), "--", "true"], "use one of"),
            (["new", "x", "--script", str(source), "--copy", str(source)], "not allowed with"),
            (["new", "x", "--local", "--copy", str(source)], "Use --script"),
            (["new", "x", "--script", "", "--", "true"], "need a file name"),
            (["new", "x", "--copy", ""], "need a file name"),
            (["new", "x", "--script", ""], "need a file name"),
        ):
            res = self.run_cli(cmd, cwd=str(proj))
            self.assertNotEqual(res.returncode, 0, cmd)
            self.assertIn(message, res.stderr)
        self.assertFalse((proj / ".shortcuts.local").exists())
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

    def test_local_shortcut_hiding_global_warns(self):
        fixed = self.folder("fixed")
        self.assertEqual(self.run_cli(["new", "penpot", "--cwd", str(fixed), "--", "echo global"]).returncode, 0)
        proj = self.project({})
        res = self.run_cli(["new", "penpot", "--local", "--cwd", ".", "--", "echo local"], cwd=str(proj))
        self.assertEqual(res.returncode, 0)
        update = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertIn("Inside this project, 'penpot' hides your global 'penpot'.", update.stdout)

        res = self.run_cli(["run", "penpot"], cwd=str(proj))
        self.assertEqual(res.stdout.strip(), "local")
        self.assertIn(f"\u2192 {proj}  (project shortcut; your global 'penpot' is hidden here", res.stderr)

        res_global = self.run_cli(["run", "--global", "penpot"], cwd=str(proj))
        self.assertEqual(res_global.stdout.strip(), "global")

        listing = self.run_cli(["list"], cwd=str(proj)).stdout
        self.assertIn("[hides global]", listing)
        self.assertIn("[hidden here by project]", listing)

        res_elsewhere = self.run_cli(["run", "penpot"], cwd=str(self.tmp_path))
        self.assertEqual(res_elsewhere.stdout.strip(), "global")
        self.assertEqual(res_elsewhere.stderr, f"\u2192 {fixed}\n")

    def test_global_flag_reaches_a_hidden_shortcut(self):
        proj = self.project({})
        script = self.write_file(self.tmp_path / "tool.sh", "#!/usr/bin/env bash\n")
        self.run_cli(["new", "build", "--", "echo global"])
        self.run_cli(["new", "build", "--local", "--", "echo project"], cwd=str(proj))
        self.run_cli(["new", "tool", "--copy", str(script), "--desc", "d"])
        self.run_cli(["new", "tool", "--script", str(script), "--desc", "d", "--local"], cwd=str(proj))
        self.approve(proj / ".shortcuts")

        self.assertIn("echo project", self.run_cli(["show", "build"], cwd=str(proj)).stdout)
        res = self.run_cli(["show", "--global", "build"], cwd=str(proj))
        self.assertIn("Scope:       global\n", res.stdout)
        self.assertIn("echo global", res.stdout)

        res = self.run_cli(["location", "--global", "tool"], cwd=str(proj))
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/scripts/tool.sh\n")

        self.assertEqual(self.run_cli(["rm", "build", "--global", "-y"], cwd=str(proj)).returncode, 0)
        self.assertNotIn("build", self.load_global())
        self.assertEqual(self.run_cli(["r", "build"], cwd=str(proj)).stdout, "project\n")
        res = self.run_cli(["show", "--global", "build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("not found", res.stderr)

    def test_concurrent_new_keeps_every_entry(self):
        procs = [self.popen_cli(["new", f"n{i}", "--", f"echo {i}"]) for i in range(16)]
        for proc in procs:
            _, err = proc.communicate(timeout=30)
            self.assertEqual(proc.returncode, 0, err)
        self.assertEqual(sorted(self.load_global()), sorted(f"n{i}" for i in range(16)))

    def test_newer_store_is_refused_not_rewritten(self):
        self.write_store(self.shortcuts_home, {"hi": {"inline": "echo hi"}}, version=3)
        before = (self.shortcuts_home / "shortcuts.json").read_text(encoding="utf-8")
        for args in (["r", "hi"], ["new", "more", "--", "echo more"]):
            res = self.run_cli(args)
            self.assertEqual(res.returncode, 1, args)
            self.assertIn("upgrade shortcut", res.stderr)
        self.assertEqual((self.shortcuts_home / "shortcuts.json").read_text(encoding="utf-8"), before)

    def test_list_and_names(self):
        proj_dir = self.home_dir / "list_proj"
        proj_dir.mkdir()
        self.write_store(proj_dir / ".shortcuts", {"local_cmd": {"description": "echo local", "inline": "echo local", "cwd": "."}})
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {
                "version": 2,
                "shortcuts": {
                    "glob": {"description": "Global desc", "inline": "echo 1"},
                    "penpot": {"description": "Fixed desc", "inline": "echo 2", "cwd": str(self.tmp_path)},
                },
            },
        )

        # Nothing of an unapproved project shows, only how to review it
        res_list = self.run_cli(["list"], cwd=str(proj_dir))
        self.assertIn("aren't approved yet. Review them with: shortcut update", res_list.stdout)
        self.assertNotIn("local_cmd", res_list.stdout)
        self.assertEqual(self.run_cli(["list", "--names"], cwd=str(proj_dir)).stdout.split(), ["glob", "penpot"])

        self.approve(proj_dir / ".shortcuts")
        res_list = self.run_cli(["list"], cwd=str(proj_dir))
        self.assertEqual(res_list.returncode, 0)
        self.assertIn("Project shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"local_cmd +\. +shared +echo local")
        self.assertIn("Global shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"glob +caller +Global desc")
        self.assertRegex(res_list.stdout, rf"penpot +{self.tmp_path} +Fixed desc")
        self.assertEqual(
            self.run_cli(["list", "--names"], cwd=str(proj_dir)).stdout.split(), ["local_cmd", "glob", "penpot"]
        )

    def test_show_and_location(self):
        self.run_cli(["new", "inline", "--desc", "Inline item", "--", "echo hello"])
        script_file = self.script("show_script.sh", "#!/usr/bin/env bash\necho test\n")
        self.run_cli(["new", "stored", "--copy", str(script_file), "--desc", "Stored item"])

        # show
        res_show_inline = self.run_cli(["show", "inline"])
        self.assertEqual(res_show_inline.returncode, 0)
        self.assertIn("Name:        inline", res_show_inline.stdout)
        self.assertIn("echo hello", res_show_inline.stdout)

        res_show_script = self.run_cli(["show", "stored"])
        self.assertEqual(res_show_script.returncode, 0)
        self.assertIn("Name:        stored", res_show_script.stdout)
        self.assertIn("echo test", res_show_script.stdout)

        # location
        res_loc_script = self.run_cli(["location", "stored"])
        self.assertEqual(res_loc_script.returncode, 0)
        self.assertTrue(res_loc_script.stdout.strip().endswith("scripts/stored.sh"))

    def test_location_prints_a_bare_path_or_fails(self):
        self.run_cli(["new", "inline_one", "--", "echo hi"])
        res = self.run_cli(["location", "inline_one"])
        self.assertEqual(res.returncode, 1)
        self.assertEqual(res.stdout, "")
        self.assertIn("inline shortcut", res.stderr)
        self.run_cli(["new", "scripted"])
        res = self.run_cli(["location", "scripted"])
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/scripts/scripted.sh\n")


class TestEntries(ShortcutTestCase):
    def test_args_need_a_name_that_is_a_string(self):
        for arg in ({"required": True}, {"name": None}, {"name": True}):
            entry = {"inline": "echo hi", "args": [arg]}
            self.write_import(self.shortcuts_home, "ns", {"version": 2, "shortcuts": {"go": entry}})
            res = self.run_cli(["update", "ns", "-y"])
            self.assertEqual(res.returncode, 1, arg)
            self.assertIn("needs a 'name'", res.stderr)

    def test_commands_that_take_a_name_show_how_many(self):
        for cmd in ("show", "location", "rm"):
            self.assertIn("[namespace] name\n", self.run_cli([cmd, "--help"]).stdout)

    def test_init_global_leaves_an_existing_store_alone(self):
        self.run_cli(["new", "keep", "--", "echo keep"])
        res = self.run_cli(["init", "--global"])
        self.assertIn("Global store already exists", res.stdout)
        self.assertEqual(self.run_cli(["r", "keep"]).stdout, "keep\n")

    def test_add_and_link_paths(self):
        # 1. Add copy from file
        src_file = self.script("source.sh", "#!/usr/bin/env bash\necho source\n", mode=0o644)

        res_add = self.run_cli(["new", "copied", "--copy", str(src_file), "--desc", "Copied script"])
        self.assertEqual(res_add.returncode, 0)

        copied_path = self.shortcuts_home / "scripts" / "copied.sh"
        self.assertTrue(copied_path.is_file())
        self.assertTrue(os.access(copied_path, os.X_OK))
        self.assertTrue(src_file.is_file())  # original untouched

        # 2. Add from stdin
        res_pipe = self.run_cli(
            ["new", "piped", "--copy", "-", "--desc", "Piped script"],
            input="#!/usr/bin/env bash\necho piped\n",
        )
        self.assertEqual(res_pipe.returncode, 0)
        piped_path = self.shortcuts_home / "scripts" / "piped.sh"
        self.assertTrue(piped_path.is_file())
        self.assertEqual(piped_path.read_text(encoding="utf-8"), "#!/usr/bin/env bash\necho piped\n")

        # 3. Link absolute external file
        ext_file = self.script("external.sh", "#!/usr/bin/env bash\necho external\n")

        res_link = self.run_cli(["new", "linked", "--script", str(ext_file), "--desc", "Linked script"])
        self.assertEqual(res_link.returncode, 0)
        self.assertFalse((self.shortcuts_home / "scripts" / "linked.sh").exists())

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["linked"]["script"], str(ext_file.resolve()))

        # 4. Path containment check
        with self.assertRaises(ValueError) as ctx:
            shortcut_mod.resolve_script_path(str(self.shortcuts_home), "../../../etc/passwd")
        self.assertIn("path traversal detected", str(ctx.exception))

    def test_rm_interactive_and_force(self):
        script_file = self.script("stored.sh", "#!/usr/bin/env bash\necho stored\n")
        self.run_cli(["new", "stored", "--copy", str(script_file), "--desc", "Stored shortcut"])

        stored_path = self.shortcuts_home / "scripts" / "stored.sh"
        self.assertTrue(stored_path.is_file())

        ext_file = self.script("external_keep.sh", "#!/usr/bin/env bash\necho ext\n")
        self.run_cli(["new", "linked", "--script", str(ext_file), "--desc", "Linked shortcut"])

        # 1. Non-tty without -y should fail
        res_no_y = self.run_cli(["rm", "stored"])
        self.assertNotEqual(res_no_y.returncode, 0)
        self.assertIn("in a terminal, or pass -y", res_no_y.stderr)
        self.assertTrue(stored_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("stored", reg["shortcuts"])

        # 2. With -y, stored copy and registry entry are deleted
        res_y = self.run_cli(["rm", "stored", "-y"])
        self.assertEqual(res_y.returncode, 0)
        self.assertFalse(stored_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("stored", reg["shortcuts"])

        # 3. Linked file is preserved when shortcut is removed with -y
        res_link_rm = self.run_cli(["rm", "linked", "-y"])
        self.assertEqual(res_link_rm.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("linked", reg["shortcuts"])
        self.assertTrue(ext_file.is_file(), "Linked external script must never be deleted!")

    def test_rm_keeps_a_script_file_that_lives_in_the_scripts_folder(self):
        self.run_cli(["init", "--global"])
        mine = self.write_file(self.shortcuts_home / "scripts" / "mine.sh", "#!/usr/bin/env bash\n", mode=0o755)
        self.assertEqual(self.run_cli(["new", "m", "--script", str(mine)]).returncode, 0)
        res = self.run_cli(["rm", "m", "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertTrue(mine.is_file(), "rm must only delete copies it made")

    def test_new_oneliner_and_template(self):
        # Inline oneliner: description defaults to command
        res1 = self.run_cli(["new", "hello", "--", "echo", "hello", "world"])
        self.assertEqual(res1.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["hello"]["inline"], "echo hello world")
        self.assertEqual(reg["shortcuts"]["hello"]["description"], "echo hello world")

        # Template script: description defaults to "(no description)"
        res2 = self.run_cli(["new", "template"])
        self.assertEqual(res2.returncode, 0)
        script_file = self.shortcuts_home / "scripts" / "template.sh"
        self.assertTrue(script_file.is_file())
        self.assertTrue(os.access(script_file, os.X_OK))
        content = script_file.read_text(encoding="utf-8")
        self.assertEqual(content, "#!/usr/bin/env bash\nset -euo pipefail\n\n")

        reg2 = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg2["shortcuts"]["template"]["description"], "(no description)")

    def test_new_opens_no_editor_and_prints_the_script_path(self):
        rc, out = self.run_in_terminal(["new", "tmpl", "--desc", "x"], env={"VISUAL": "false", "EDITOR": "false"})
        self.assertEqual(rc, 0, out)
        script = self.shortcuts_home / "scripts" / "tmpl.sh"
        self.assertIn(f"Edit it at: {script}", out)
        self.assertEqual(script.read_text(encoding="utf-8"), "#!/usr/bin/env bash\nset -euo pipefail\n\n")
        self.assertIn("tmpl", self.load_global())

    def test_rm_malformed_entry(self):
        reg = {"shortcuts": {"bad_entry": {"description": "x", "inline": "true", "cwd": 123}}}
        self.write_registry(self.shortcuts_home, reg)
        res = self.run_cli(["rm", "bad_entry", "-y"])
        self.assertEqual(res.returncode, 0)
        after = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("bad_entry", after.get("shortcuts", {}))

    def test_rm_says_what_it_removes_and_what_happens_to_the_script(self):
        source = self.script("src.sh", "#!/usr/bin/env bash\n")
        self.run_cli(["new", "copied", "--copy", str(source)])
        self.run_cli(["new", "linked", "--script", str(source)])
        rc, out = self.run_in_terminal(["rm", "copied"], keys=[(b"[y/N]", b"n\n")])
        self.assertEqual(rc, 1, out)
        self.assertIn(f"Removes 'copied' from your global shortcuts: {self.shortcuts_home / 'shortcuts.json'}", out)
        self.assertIn(f"Deletes its script copy: {self.shortcuts_home / 'scripts' / 'copied.sh'}", out)
        rc, out = self.run_in_terminal(["rm", "linked"], keys=[(b"[y/N]", b"y\n")])
        self.assertEqual(rc, 0, out)
        self.assertIn(f"Keeps its script: {source}", out)
        self.assertTrue(source.is_file())
        self.assertNotIn("linked", self.load_global())

    def test_declining_rm_exits_1(self):
        self.run_cli(["new", "keep", "--", "true"])
        rc, out = self.run_in_terminal(["rm", "keep"], keys=[(b"[y/N]", b"n\n")])
        self.assertEqual(rc, 1)
        self.assertIn("Aborted.", out)
        self.assertIn("keep", self.load_global())

    def test_malformed_entries_never_crash(self):
        proj = self.project({"bad": "echo hi"}, name="proj_a5", version=1)
        res = self.run_cli(["update", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("expected a dictionary", res.stderr)

        # The global store isn't approved, so a bad entry can reach `run`
        self.write_store(self.shortcuts_home, {"bad": "echo hi"})
        for cwd in (proj, self.tmp_path):
            res = self.run_cli(["run", "bad"], cwd=str(cwd))
            self.assertEqual(res.returncode, 1)
            self.assertTrue(res.stderr.strip().startswith("error:"))
            self.assertNotIn("Traceback", res.stderr)

        src_dir = self.collection(
            {"bad_dir": {"description": "Bad dir", "inline": "echo hi", "cwd": 123}}, name="src_a5", version=1
        )

        res_init = self.run_cli(["init", "--global", str(src_dir), "-y"])
        self.assertEqual(res_init.returncode, 0)
        self.assertIn("bad_dir (invalid entry: 'bad_dir' is not a valid entry: 'cwd' must be a string", res_init.stdout)
        self.assertNotIn("Traceback", res_init.stderr)

    def test_unknown_fields_are_errors(self):
        self.run_cli(["init", "--global"])
        cases = {
            "top": ({"version": 2, "owner": "x", "shortcuts": {}}, "unknown field: owner"),
            "entry": ({"version": 2, "shortcuts": {"a": {"inline": "true", "sandbox": True}}}, "unknown field: sandbox"),
            "arg": (
                {"version": 2, "shortcuts": {"a": {"inline": "true", "args": [{"name": "x", "secret": True}]}}},
                "unknown args field: secret",
            ),
        }
        for name, (data, message) in cases.items():
            self.write_import(self.shortcuts_home, name, data)
            res = self.run_cli(["update", name, "-y"])
            self.assertEqual(res.returncode, 1, name)
            self.assertIn(message, res.stderr)
            self.assertIn(message, self.run_cli(["list"]).stderr)

    def test_update_refuses_entries_that_cannot_run(self):
        self.run_cli(["init", "--global"])
        cases = {
            "neither": ({"a": {"description": "x"}}, "exactly one of 'inline' and 'script'"),
            "both": ({"a": {"inline": "true", "script": "a.sh"}}, "exactly one of 'inline' and 'script'"),
            "missing": ({"a": {"script": "scripts/a.sh"}}, "script not found: scripts/a.sh"),
            "escape": ({"a": {"script": "../../outside.sh"}}, "path traversal"),
            "badname": ({"a!": {"inline": "true"}}, "invalid shortcut name"),
        }
        for name, (shortcuts, message) in cases.items():
            self.write_import(self.shortcuts_home, name, {"version": 2, "shortcuts": shortcuts})
            res = self.run_cli(["update", name, "-y"])
            self.assertEqual(res.returncode, 1, name)
            self.assertIn(message, res.stderr)
            refused = self.run_cli(["r", name, *shortcuts])
            self.assertEqual(refused.returncode, 1, name)
            self.assertIn(message if "exactly one" in message else "isn't approved yet", refused.stderr)

    def test_an_entry_with_both_inline_and_script_is_refused(self):
        self.write_file(self.shortcuts_home / "both.sh", "#!/usr/bin/env bash\necho script\n")
        reg = {"version": 2, "shortcuts": {"both": {"description": "x", "inline": "echo inline", "script": "both.sh"}}}
        self.write_registry(self.shortcuts_home, reg)
        for words in (["r", "both"], ["show", "both"], ["location", "both"]):
            res = self.run_cli(words)
            self.assertEqual(res.returncode, 1, words)
            self.assertIn("exactly one of 'inline' and 'script'", res.stderr)
            self.assertNotIn("inline\n", res.stdout)
        self.assertEqual(self.run_cli(["rm", "both", "-y"]).returncode, 0)
        self.assertNotIn("both", self.load_global())

    def test_unknown_top_level_field_stops_a_global_shortcut(self):
        self.run_cli(["new", "hi", "--", "echo ran"])
        reg = json.loads((self.shortcuts_home / "shortcuts.json").read_text(encoding="utf-8"))
        reg["future_safety"] = True
        self.write_registry(self.shortcuts_home, reg)
        res = self.run_cli(["r", "hi"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("unknown field: future_safety", res.stderr)
        self.assertNotIn("ran", res.stdout)
        self.assertEqual(self.run_cli(["rm", "hi", "-y"]).returncode, 0)


class TestNamespaces(ShortcutTestCase):
    def test_namespace_dispatch(self):
        imports = self.shortcuts_home / "imports"
        self.write_file(imports / "scripts" / "del.sh", '#!/usr/bin/env bash\necho "deleting $1"\n')
        self.write_import(self.shortcuts_home, "docker", {"version": 2, "shortcuts": {
            "delete": {"script": "scripts/del.sh"},
            "where": {"inline": "pwd", "cwd": "."},
        }})
        self.run_cli(["init", "--global"])
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)

        res = self.run_cli(["r", "docker", "delete", "XYZ"])
        self.assertEqual(res.stdout, "deleting XYZ\n", res.stderr)
        self.assertEqual(self.run_cli(["r", "docker", "where"]).stdout, f"{imports}\n")
        self.assertEqual(self.run_cli(["location", "docker", "delete"]).stdout, f"{imports}/scripts/del.sh\n")
        self.assertIn("Name:        docker delete", self.run_cli(["show", "docker", "delete"]).stdout)
        self.assertIn("docker", self.run_cli(["list", "--names"]).stdout.split())
        self.assertRegex(self.run_cli(["list"]).stdout, r"docker delete +caller")

        res = self.run_cli(["r", "docker"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'docker' is a namespace; run one of its shortcuts: delete, where", res.stderr)
        res = self.run_cli(["r", "docker", "nope"])
        self.assertIn("'nope' not found in namespace 'docker'", res.stderr)

    def test_namespace_from_imports_key(self):
        self.write_file(
            self.tmp_path / "shared" / "tools.shortcut",
            json.dumps({"version": 2, "shortcuts": {"hi": {"inline": "echo hi from tools"}}}),
        )
        proj = self.project(OWN, imports={"tools": "../shared/tools.shortcut"}, approved=True)
        self.assertEqual(self.run_cli(["r", "tools", "hi"], cwd=str(proj)).stdout, "hi from tools\n")
        self.assertEqual(self.run_cli(["r", "own"], cwd=str(proj)).stdout, "own\n")

    def test_namespace_collision_and_nesting_errors(self):
        proj = self.project({"docker": {"inline": "echo shortcut"}, "ok": {"inline": "echo ok"}})
        self.write_import(proj / ".shortcuts", "docker", DOCKER_PS)
        self.run_cli(["new", "outside", "--", "echo outside"])
        self.approve(proj / ".shortcuts")

        for words in (["docker"], ["docker", "ps"], ["ok"]):
            res = self.run_cli(["r", *words], cwd=str(proj))
            self.assertEqual(res.returncode, 1)
            self.assertIn("'docker' is both a shortcut and a namespace", res.stderr)
        # Only that store: names from a healthy one still run
        self.assertEqual(self.run_cli(["r", "outside"], cwd=str(proj)).stdout, "outside\n")
        self.assertIn("'docker' is both", self.run_cli(["list"], cwd=str(proj)).stderr)

        self.write_store(proj / ".shortcuts", {"ok": {"inline": "echo ok"}})
        self.write_import(proj / ".shortcuts", "nested", {"version": 2, "imports": {"x": "x.shortcut"}, "shortcuts": {}})
        res = self.run_cli(["r", "nested", "x"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("namespaces don't nest", res.stderr)

    def test_imports_must_be_shortcut_files(self):
        self.write_file(self.shortcuts_home / "imports" / "docker.json", json.dumps(DOCKER_PS))
        self.run_cli(["init", "--global"])
        self.assertIn("'docker' not found", self.run_cli(["r", "docker", "ps"]).stderr)

        proj = self.project(OWN, imports={"tools": "tools.json"}, approved=True)
        res = self.run_cli(["r", "own"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("import 'tools'", res.stderr)
        self.assertIn("must be a .shortcut file", res.stderr)

    def test_a_bare_shortcut_file_is_refused_with_why(self):
        self.run_cli(["new", "hi", "--", "echo hi"])
        bare = self.write_file(self.shortcuts_home / "imports" / ".shortcut", json.dumps(DOCKER_PS))
        res = self.run_cli(["list"])
        self.assertIn(f"warning: {bare}: a namespace file needs a name, e.g. docker.shortcut", res.stderr)
        self.assertNotIn("invalid shortcut name", res.stderr)
        self.assertEqual(self.run_cli(["r", "hi"]).stdout, "hi\n")
        bare.unlink()

        proj = self.project(OWN, imports={"x": ".shortcut"}, files={".shortcut": json.dumps(DOCKER_PS)}, approved=True)
        for cmd in (["r", "x", "ps"], ["r", "own"]):
            res = self.run_cli(cmd, cwd=str(proj))
            self.assertEqual(res.returncode, 1)
            self.assertIn("import 'x'", res.stderr)
            self.assertIn("a namespace file needs a name, e.g. docker.shortcut", res.stderr)
        self.assertEqual(self.run_cli(["r", "hi"], cwd=str(proj)).stdout, "hi\n")

    def test_shortcut_file_needs_the_current_version(self):
        self.run_cli(["init", "--global"])
        path = self.write_import(self.shortcuts_home, "docker", {"shortcuts": {"ps": {"inline": "echo ps"}}})
        res = self.run_cli(["update", "docker", "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn('needs "version": 2', res.stderr)

        path.write_text(json.dumps({"version": 3, "shortcuts": {"ps": {"inline": "echo ps"}}}), encoding="utf-8")
        res = self.run_cli(["update", "docker", "-y"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("newer than this shortcut reads; upgrade shortcut", res.stderr)

    def test_group_description_shows_once_approved(self):
        self.run_cli(["init", "--global"])
        self.write_import(self.shortcuts_home, "docker", {
            "version": 2, "description": "Docker housekeeping", "shortcuts": {"ps": {"inline": "echo ps"}},
        })
        self.assertNotIn("Docker housekeeping", self.run_cli(["list"]).stdout)
        self.assertIn("Docker housekeeping", self.run_cli(["update", "docker", "-y"]).stdout)
        self.assertIn("  Docker housekeeping\n", self.run_cli(["list"]).stdout)

    def test_namespace_flag_creates_a_namespace(self):
        res = self.run_cli(["new", "delete", "--namespace", "docker", "--desc", "Remove one", "--", 'echo "rm $1"'])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("needs approving before it runs: shortcut update docker", res.stderr)
        path = self.shortcuts_home / "imports" / "docker.shortcut"
        data = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(data["version"], 2)
        self.assertEqual(data["shortcuts"]["delete"]["inline"], 'echo "rm $1"')
        self.assertNotIn("delete", self.load_global())

        self.assertIn("isn't approved yet", self.run_cli(["r", "docker", "delete", "x"]).stderr)
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "delete", "x"]).stdout, "rm x\n")

        self.assertEqual(self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo ps"]).returncode, 0)
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo again"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("already exists", res.stderr)

    def test_namespace_flag_collisions(self):
        self.run_cli(["new", "docker", "--", "echo plain"])
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo ps"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'docker' is a shortcut", res.stderr)
        self.assertFalse((self.shortcuts_home / "imports").exists())

        res = self.run_cli(["new", "ps", "--namespace", "not valid", "--", "echo ps"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("invalid shortcut name", res.stderr)

        self.run_cli(["new", "ps", "--namespace", "kube", "--", "echo ps"])
        res = self.run_cli(["new", "kube", "--", "echo plain"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'kube' is a namespace", res.stderr)

    def test_namespace_flag_keeps_each_groups_scripts_apart(self):
        source = self.tmp_path / "src.sh"
        for group, word in (("docker", "dock"), ("kube", "kub")):
            source.write_text(f"#!/usr/bin/env bash\necho {word}\n", encoding="utf-8")
            res = self.run_cli(["new", "delete", "--copy", str(source), "--namespace", group, "--desc", "d"])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertEqual(self.run_cli(["update", group, "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "delete"]).stdout, "dock\n")
        self.assertEqual(self.run_cli(["r", "kube", "delete"]).stdout, "kub\n")

        copy = self.shortcuts_home / "imports" / "scripts" / "docker" / "delete.sh"
        self.assertEqual(self.run_cli(["location", "docker", "delete"]).stdout, f"{copy}\n")
        self.assertEqual(self.run_cli(["rm", "docker", "delete", "-y"]).returncode, 0)
        self.assertFalse(copy.exists())
        self.assertTrue((self.shortcuts_home / "imports" / "scripts" / "kube" / "delete.sh").exists())

    def test_namespace_flag_is_refused_with_local(self):
        proj = self.project({})
        res = self.run_cli(["new", "where", "--namespace", "dev", "--local", "--", "pwd"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("--namespace can't be used with --local", res.stderr)
        self.assertFalse((proj / ".shortcuts.local").exists())

    def test_list_names_of_a_namespace(self):
        self.run_cli(["new", "b", "--namespace", "docker", "--", "true"])
        self.run_cli(["new", "a", "--namespace", "docker", "--", "true"])
        self.assertEqual(self.run_cli(["list", "--names", "docker"]).stdout, "")
        self.assertEqual(self.run_cli(["update", "docker", "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["list", "--names", "docker"]).stdout, "a\nb\n")
        self.assertEqual(self.run_cli(["list", "--names", "nothing"]).stdout, "")

        # A project shortcut of that name is what `r docker` runs, so its arguments aren't names
        proj = self.project({"docker": {"inline": "true"}}, approved=True)
        self.assertEqual(self.run_cli(["list", "--names", "docker"], cwd=str(proj)).stdout, "")

    def test_cli_writes_keep_an_approved_namespace_approved(self):
        self.run_cli(["new", "a", "--namespace", "dk", "--", "echo a"])
        self.assertEqual(self.run_cli(["update", "dk", "-y"]).returncode, 0)

        res = self.run_cli(["new", "ps", "--namespace", "dk", "--", "echo ps"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("needs approving", res.stderr)
        self.assertEqual(self.run_cli(["r", "dk", "ps"]).stdout, "ps\n")

        self.assertEqual(self.run_cli(["rm", "dk", "ps", "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "dk", "a"]).stdout, "a\n")

        source = self.write_file(self.tmp_path / "src.sh", "#!/usr/bin/env bash\necho copied\n")
        res = self.run_cli(["new", "cp", "--copy", str(source), "--namespace", "dk", "--desc", "d"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.run_cli(["r", "dk", "cp"]).stdout, "copied\n")

        # A hand edit still needs update, and a later CLI write doesn't approve it on the way
        path = self.shortcuts_home / "imports" / "dk.shortcut"
        data = json.loads(path.read_text(encoding="utf-8"))
        data["shortcuts"]["a"]["inline"] = "echo edited"
        path.write_text(json.dumps(data), encoding="utf-8")
        self.assertIn("changed since you approved it", self.run_cli(["r", "dk", "a"]).stderr)
        res = self.run_cli(["new", "more", "--namespace", "dk", "--", "echo more"])
        self.assertIn("needs approving before it runs: shortcut update dk", res.stderr)
        self.assertIn("changed since you approved it", self.run_cli(["r", "dk", "more"]).stderr)
        self.assertEqual(self.run_cli(["rm", "dk", "more", "-y"]).returncode, 0)
        self.assertIn("changed since you approved it", self.run_cli(["r", "dk", "a"]).stderr)

    def test_new_in_a_namespace_gives_its_script_path(self):
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--desc", "d"])
        self.assertIn(f"Edit it at: {self.shortcuts_home / 'imports' / 'scripts' / 'docker' / 'ps.sh'}", res.stdout)


class TestBrokenStores(ShortcutTestCase):
    def test_broken_project_stops_every_run_inside_it(self):
        self.run_cli(["new", "outside", "--", "echo outside"])
        proj = self.broken_project()
        registry = str(proj / ".shortcuts")

        for name in ("outside", "nothing"):
            res = self.run_cli(["r", name], cwd=str(proj))
            self.assertEqual(res.returncode, 1, name)
            self.assertEqual(res.stdout, "", name)
            self.assertIn(f"corrupt registry {registry}", res.stderr, name)
            self.assertIn(
                f". Nothing runs inside this project until it's fixed; for your own: shortcut r --global {name}\n",
                res.stderr, name)
        self.assertEqual(self.run_cli(["r", "--global", "outside"], cwd=str(proj)).stdout, "outside\n")

        # Looking is not running: show and location read past the file, quietly
        for cmd in (["show", "outside"], ["show", "--json", "outside"], ["location", "outside"]):
            res = self.run_cli(cmd, cwd=str(proj))
            self.assertNotIn("corrupt registry", res.stderr, cmd)
            self.assertNotIn("warning", res.stderr, cmd)
        self.assertIn("Scope:       global", self.run_cli(["show", "outside"], cwd=str(proj)).stdout)
        self.assertIn("'nothing' not found", self.run_cli(["show", "nothing"], cwd=str(proj)).stderr)

        # rm doesn't reach past the broken file either; --global does
        self.assertEqual(self.run_cli(["rm", "outside", "-y"], cwd=str(proj)).returncode, 1)
        self.assertIn("outside", self.load_global())
        self.assertEqual(self.run_cli(["rm", "--global", "outside", "-y"], cwd=str(proj)).returncode, 0)
        self.assertNotIn("outside", self.load_global())
        self.assertIn("corrupt registry", self.run_cli(["list"], cwd=str(proj)).stderr)
        self.assertEqual(self.run_cli(["update"], cwd=str(proj)).returncode, 1)

    def test_a_repaired_project_runs_again(self):
        self.run_cli(["new", "build", "--", "echo global"])
        proj = self.project({"build": {"inline": "echo project"}}, approved=True)
        registry = proj / ".shortcuts"
        good = registry.read_text(encoding="utf-8")
        registry.write_text("<<<<<<< HEAD\n" + good, encoding="utf-8")
        res = self.run_cli(["r", "build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("Nothing runs inside this project", res.stderr)
        self.assertEqual(self.run_cli(["r", "--global", "build"], cwd=str(proj)).stdout, "global\n")
        registry.write_text(good, encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "build"], cwd=str(proj)).stdout, "project\n")

    def test_rm_in_a_broken_project_never_reaches_a_global_one(self):
        self.run_cli(["new", "build", "--", "echo global"])
        proj = self.project({"build": {"inline": "echo project"}}, approved=True)
        self.corrupt_store(proj / ".shortcuts")
        res = self.run_cli(["rm", "build", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("Inside this project rm only takes --local", res.stderr)
        self.assertIn("for your own: shortcut rm --global build", res.stderr)
        self.assertIn("build", self.load_global())
        self.assertEqual(self.run_cli(["rm", "--global", "build", "-y"], cwd=str(proj)).returncode, 0)
        self.assertNotIn("build", self.load_global())

    def test_rm_local_hides_a_name_when_the_project_file_cant_be_read(self):
        proj = self.project({"build": {"inline": "echo project"}}, approved=True)
        self.corrupt_store(proj / ".shortcuts")
        broken = (proj / ".shortcuts").read_bytes()
        rc, out = self.run_in_terminal(["rm", "--local", "build"], cwd=str(proj), keys=[(b"[y/N]", b"y\n")])
        self.assertEqual(rc, 0, out)
        self.assertIn(".shortcuts stays as it is.", out)
        self.assertIn('Hides \'build\' from you: writes "build": null to', out)
        local = json.loads((proj / ".shortcuts.local").read_text(encoding="utf-8"))["shortcuts"]
        self.assertEqual(local, {"build": None})
        self.assertEqual((proj / ".shortcuts").read_bytes(), broken)

    def test_broken_project_refuses_a_namespace_it_imported(self):
        self.run_cli(["new", "docker", "--", "echo global"])
        proj = self.project(
            {},
            imports={"docker": "../tools/docker.shortcut"},
            files={"tools/docker.shortcut": json.dumps({"version": 2, "shortcuts": {"ps": {"inline": "true"}}})},
        )
        self.assertEqual(self.run_cli(["update", ".", "-y"], cwd=str(proj)).returncode, 0)
        self.corrupt_store(proj / ".shortcuts")

        res = self.run_cli(["r", "docker", "ps"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("for your own: shortcut r --global docker", res.stderr)
        # The file can't be read, so its imports are gone too: show finds the global 'docker'
        self.assertIn("Scope:       global", self.run_cli(["show", "docker"], cwd=str(proj)).stdout)

    def test_an_unknown_top_level_field_stops_the_project_too(self):
        self.run_cli(["new", "outside", "--", "echo outside"])
        proj = self.project({"inside": {"inline": "true"}, "mine": {"inline": "true"}}, approved=True)
        self.assertEqual(self.run_cli(["new", "mine", "--local", "--", "echo mine"], cwd=str(proj)).returncode, 0)
        registry = proj / ".shortcuts"
        data = json.loads(registry.read_text(encoding="utf-8"))
        registry.write_text(json.dumps({**data, "bogus": 1}), encoding="utf-8")

        for name in ("inside", "mine", "outside"):
            res = self.run_cli(["r", name], cwd=str(proj))
            self.assertEqual(res.returncode, 1, name)
            self.assertIn(f"{registry} has an unknown field: bogus. Nothing runs inside this project", res.stderr)

        # The file still parses, so show and rm --local find its entries
        res = self.run_cli(["show", "inside"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("Scope:       project", res.stdout)
        self.assertIn("Approval:    changed", res.stdout)
        self.assertEqual(self.run_cli(["rm", "inside", "-y"], cwd=str(proj)).returncode, 1)  # a project entry, as ever
        res = self.run_cli(["rm", "--local", "mine", "-y"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("mine", json.loads((proj / ".shortcuts.local").read_text(encoding="utf-8"))["shortcuts"])
        self.assertIn('"bogus": 1', registry.read_text(encoding="utf-8"))

    def test_broken_global_store_still_reports(self):
        self.corrupt_store(self.shortcuts_home)
        res = self.run_cli(["r", "anything"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("corrupt registry", res.stderr)
        self.assertNotIn("Traceback", res.stderr)
        self.assertIn("corrupt registry", self.run_cli(["list"]).stderr)

    def test_list_warns_about_every_entry_that_cannot_run(self):
        self.write_store(self.shortcuts_home, {
            "good": {"inline": "true"},
            "gone": {"script": "scripts/none.sh"},
            "bad": {"inline": "true", "cwd": 123},
        })
        self.write_import(self.shortcuts_home, "ns", {"version": 2, "shortcuts": {"both": {"inline": "true", "script": "a.sh"}}})
        res = self.run_cli(["list"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("script not found: scripts/none.sh", res.stderr)
        self.assertIn("'cwd' must be a string", res.stderr)
        self.assertIn("warning: ns: 'both'", res.stderr)
        self.assertIn("good", res.stdout)
        self.assertEqual(self.run_cli(["list", "--names"]).stderr, "")

    def test_a_fifo_script_never_blocks(self):
        """An approved script swapped for a FIFO must not hang the hashing that checks approval."""
        proj = self.project({"p": {"script": "pipe"}, "q": {"inline": "true"}}, files={"pipe": "#!/bin/sh\n"})
        self.assertEqual(self.run_cli(["update", ".", "-y"], cwd=str(proj)).returncode, 0)
        (proj / "pipe").unlink()
        os.mkfifo(proj / "pipe")
        for cmd in (["list"], ["list", "--names"], ["r", "q"], ["update", ".", "-y"]):
            res = self.run_cli(cmd, cwd=str(proj), timeout=10)
            self.assertNotIn("Traceback", res.stderr, cmd)
        self.assertIn("script not found", self.run_cli(["list"], cwd=str(proj), timeout=10).stderr)


if __name__ == "__main__":
    unittest.main()
