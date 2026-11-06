#!/usr/bin/env python3
"""Stores and namespaces: where shortcuts live, how they're found, and what happens when a store is bad."""

import json
import os
import sys
import unittest

from fixtures import DOCKER_PS, HI, OWN, shortcut_mod, ShortcutTestCase


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
        self.assertIsNone(shortcut_mod.find_local_store_dir(str(self.home_dir)))
        deep_dir = self.home_dir / "a" / "b" / "c"
        deep_dir.mkdir(parents=True)
        self.assertIsNone(shortcut_mod.find_local_store_dir(str(deep_dir)))

        # Walking up from inside ~/.shortcuts itself must return None
        self.assertIsNone(shortcut_mod.find_local_store_dir(str(self.shortcuts_home)))

        # A real local store in a project must be found
        proj_dir = self.home_dir / "my_project"
        proj_shortcuts = proj_dir / ".shortcuts"
        proj_shortcuts.mkdir(parents=True)
        shortcut_mod.save_registry_atomic(str(proj_shortcuts), {"version": 1, "shortcuts": {}})

        sub_dir = proj_dir / "src" / "pkg"
        sub_dir.mkdir(parents=True)
        found = shortcut_mod.find_local_store_dir(str(sub_dir))
        self.assertEqual(found, str(proj_shortcuts.resolve()))

    def test_scope_resolution_order(self):
        shortcut_mod.save_registry_atomic(
            str(self.shortcuts_home),
            {"version": 2, "shortcuts": {"build": {"description": "Global build", "inline": "echo global_build"}}},
        )
        proj_dir = self.home_dir / "my_project"
        proj_shortcuts = proj_dir / ".shortcuts"
        proj_shortcuts.mkdir(parents=True)
        shortcut_mod.save_registry_atomic(
            str(proj_shortcuts),
            {"version": 2, "shortcuts": {"build": {"description": "Local project build", "inline": "echo local_build"}}},
        )

        # Inside a project: project precedence
        res_local, _ = shortcut_mod.resolve_shortcut(["build"], caller_cwd=str(proj_dir))
        self.assertEqual(res_local.group.store_dir, str(proj_shortcuts.resolve()))
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

        # 2. --local creates ./.shortcuts/ with a stderr notice, and it needs approving
        res = self.run_cli(["new", "local_cmd", "--local", "--", "echo local"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("created project store", res.stderr)
        self.assertIn("needs approving before it runs: shortcut update", res.stderr)
        self.assertTrue((proj_dir / ".shortcuts" / "shortcuts.json").is_file())
        self.assertNotEqual(self.run_cli(["run", "local_cmd"], cwd=str(proj_dir)).returncode, 0)
        self.allow(proj_dir / ".shortcuts")
        self.assertEqual(self.run_cli(["run", "local_cmd"], cwd=str(proj_dir)).stdout, "local\n")

        # 3. --cwd is stored against the defining file: absolute globally, relative inside the project
        res = self.run_cli(["new", "fixed", "--cwd", str(self.tmp_path), "--", "pwd"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["fixed"]["cwd"], str(self.tmp_path))
        res = self.run_cli(["new", "in_sub", "--local", "--cwd", "sub", "--", "pwd"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0, res.stderr)
        project = json.loads((proj_dir / ".shortcuts" / "shortcuts.json").read_text())["shortcuts"]
        self.assertEqual(project["in_sub"]["cwd"], "../sub")
        res = self.run_cli(["new", "explicit", "--cwd", "caller", "--", "pwd"])
        self.assertEqual(self.load_global()["explicit"]["cwd"], "caller")

        res = self.run_cli(["new", "nowhere", "--cwd", "/nonexistent/path/xyz", "--", "echo 1"])
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("--cwd directory does not exist", res.stderr)

        # 4. Duplicate name error
        res = self.run_cli(["new", "local_cmd", "--local", "--", "echo duplicate"], cwd=str(proj_dir))
        self.assertNotEqual(res.returncode, 0)
        self.assertIn("already exists in", res.stderr)

    def test_init_empty_local_store_creation(self):
        proj_dir = self.project(name="empty_init_proj")

        res = self.run_cli(["init"], cwd=str(proj_dir))
        self.assertEqual(res.returncode, 0)
        self.assertIn("Initialized empty project store", res.stdout)

        store_json = proj_dir / ".shortcuts" / "shortcuts.json"
        self.assertTrue(store_json.is_file())
        data = shortcut_mod.load_registry(str(proj_dir / ".shortcuts"))
        self.assertEqual(data, {"version": 2, "shortcuts": {}})
        # the lock stays out of git, and logs aren't kept here at all
        self.assertEqual((proj_dir / ".shortcuts" / ".gitignore").read_text(), ".lock\n")

    def test_new_in_home_refuses_local_store(self):
        res = self.run_cli(["new", "hello", "--local", "--", "echo hi"], cwd=str(self.home_dir))
        self.assertEqual(res.returncode, 1)
        self.assertIn("is the global store", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "hello" in self.load_global())

        res_init = self.run_cli(["init"], cwd=str(self.home_dir))
        self.assertEqual(res_init.returncode, 1)
        self.assertIn("is the global store", res_init.stderr)

    def test_default_home_store_is_never_a_project(self):
        """With SHORTCUTS_HOME elsewhere, ~/.shortcuts is still not a project of anything under ~."""
        self.run_cli(["init", "--global"], env={"SHORTCUTS_HOME": ""})
        real = self.tmp_path / "custom_store"
        env = {"SHORTCUTS_HOME": str(real)}
        (self.home_dir / "work").mkdir()
        res = self.run_cli(["new", "here", "--local", "--", "echo 1"], cwd=str(self.home_dir / "work"), env=env)
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertTrue((self.home_dir / "work" / ".shortcuts" / "shortcuts.json").is_file())
        self.assertEqual(self.load_global(), {})

    def test_local_shortcut_hiding_global_warns(self):
        fixed = self.folder("fixed")
        self.assertEqual(self.run_cli(["new", "penpot", "--cwd", str(fixed), "--", "echo global"]).returncode, 0)
        proj = self.project()
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
        proj = self.project()
        script = self.write_file(self.tmp_path / "tool.sh", "#!/usr/bin/env bash\n")
        self.run_cli(["new", "build", "--", "echo global"])
        self.run_cli(["new", "build", "--local", "--", "echo project"], cwd=str(proj))
        self.run_cli(["add", "tool", str(script), "--desc", "d"])
        self.run_cli(["add", "tool", str(script), "--desc", "d", "--local"], cwd=str(proj))
        self.allow(proj / ".shortcuts")

        self.assertIn("echo project", self.run_cli(["show", "build"], cwd=str(proj)).stdout)
        res = self.run_cli(["show", "--global", "build"], cwd=str(proj))
        self.assertIn("Scope:       global\n", res.stdout)
        self.assertIn("echo global", res.stdout)
        self.assertIn("echo global", self.run_cli(["fix", "--global", "build"], cwd=str(proj)).stdout)

        editor = "sed -i s/global/edited/"
        res = self.run_cli(["edit", "--global", "build"], cwd=str(proj), env={"SHORTCUT_EDITOR_CMD": editor})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["build"]["inline"], "echo edited")

        res = self.run_cli(["location", "--global", "tool"], cwd=str(proj))
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/scripts/tool.sh\n")

        self.assertEqual(self.run_cli(["rm", "build", "--global", "-y"], cwd=str(proj)).returncode, 0)
        self.assertNotIn("build", self.load_global())
        self.assertEqual(self.run_cli(["r", "build"], cwd=str(proj)).stdout, "project\n")
        res = self.run_cli(["show", "--global", "build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("not found", res.stderr)

    def test_fix_global_without_a_name_skips_the_project(self):
        proj = self.project(HI, name="thisproject")
        self.assertIn("thisproject", self.run_cli(["fix"], cwd=str(proj)).stdout)
        self.assertNotIn("thisproject", self.run_cli(["fix", "--global"], cwd=str(proj)).stdout)

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
        self.run_cli(["new", "local_cmd", "--local", "--cwd", ".", "--", "echo local"], cwd=str(proj_dir))
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

        self.allow(proj_dir / ".shortcuts")
        res_list = self.run_cli(["list"], cwd=str(proj_dir))
        self.assertEqual(res_list.returncode, 0)
        self.assertIn("Project shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"local_cmd +\.\. +echo local")
        self.assertIn("Global shortcuts", res_list.stdout)
        self.assertRegex(res_list.stdout, r"glob +caller +Global desc")
        self.assertRegex(res_list.stdout, rf"penpot +{self.tmp_path} +Fixed desc")
        self.assertEqual(
            self.run_cli(["list", "--names"], cwd=str(proj_dir)).stdout.split(), ["local_cmd", "glob", "penpot"]
        )

    def test_show_and_location(self):
        self.run_cli(["new", "inline", "--desc", "Inline item", "--", "echo hello"])
        script_file = self.script("show_script.sh", "#!/usr/bin/env bash\necho test\n")
        self.run_cli(["add", "stored", str(script_file), "--desc", "Stored item"])

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
        res = self.run_cli(["location", "--log", "inline_one"])
        self.assertEqual(res.stdout, f"{self.shortcuts_home}/logs/inline_one.log\n")


class TestEntries(ShortcutTestCase):
    def test_args_need_a_name_that_is_a_string(self):
        for arg in ({"required": True}, {"name": None}, {"name": True}):
            entry = {"inline": "echo hi", "args": [arg]}
            self.write_import(self.shortcuts_home, "ns", {"version": 2, "shortcuts": {"go": entry}})
            res = self.run_cli(["update", "ns", "-y"])
            self.assertEqual(res.returncode, 1, arg)
            self.assertIn("needs a 'name'", res.stderr)

    def test_commands_that_take_a_name_show_how_many(self):
        for cmd in ("show", "location", "rm", "edit"):
            self.assertIn("[namespace] name\n", self.run_cli([cmd, "--help"]).stdout)

    def test_init_global_leaves_an_existing_store_alone(self):
        self.run_cli(["new", "keep", "--", "echo keep"])
        res = self.run_cli(["init", "--global"])
        self.assertIn("Global store already exists", res.stdout)
        self.assertEqual(self.run_cli(["r", "keep"]).stdout, "keep\n")

    def test_add_and_link_paths(self):
        # 1. Add copy from file
        src_file = self.script("source.sh", "#!/usr/bin/env bash\necho source\n", mode=0o644)

        res_add = self.run_cli(["add", "copied", str(src_file), "--desc", "Copied script"])
        self.assertEqual(res_add.returncode, 0)

        copied_path = self.shortcuts_home / "scripts" / "copied.sh"
        self.assertTrue(copied_path.is_file())
        self.assertTrue(os.access(copied_path, os.X_OK))
        self.assertTrue(src_file.is_file())  # original untouched

        # 2. Add from stdin
        res_pipe = self.run_cli(
            ["add", "piped", "-", "--desc", "Piped script"],
            input="#!/usr/bin/env bash\necho piped\n",
        )
        self.assertEqual(res_pipe.returncode, 0)
        piped_path = self.shortcuts_home / "scripts" / "piped.sh"
        self.assertTrue(piped_path.is_file())
        self.assertEqual(piped_path.read_text(encoding="utf-8"), "#!/usr/bin/env bash\necho piped\n")

        # 3. Link absolute external file
        ext_file = self.script("external.sh", "#!/usr/bin/env bash\necho external\n")

        res_link = self.run_cli(["link", "linked", str(ext_file), "--desc", "Linked script"])
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
        self.run_cli(["add", "stored", str(script_file), "--desc", "Stored shortcut"])
        self.run_cli(["run", "stored"])

        log_path = self.shortcuts_home / "logs" / "stored.log"
        stored_path = self.shortcuts_home / "scripts" / "stored.sh"
        self.assertTrue(log_path.is_file())
        self.assertTrue(stored_path.is_file())

        ext_file = self.script("external_keep.sh", "#!/usr/bin/env bash\necho ext\n")
        self.run_cli(["link", "linked", str(ext_file), "--desc", "Linked shortcut"])

        # 1. Non-tty without -y should fail
        res_no_y = self.run_cli(["rm", "stored"])
        self.assertNotEqual(res_no_y.returncode, 0)
        self.assertIn("in a terminal, or pass -y", res_no_y.stderr)
        self.assertTrue(stored_path.is_file())
        self.assertTrue(log_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("stored", reg["shortcuts"])

        # 2. With -y, stored copy, log, and registry entry are deleted
        res_y = self.run_cli(["rm", "stored", "-y"])
        self.assertEqual(res_y.returncode, 0)
        self.assertFalse(stored_path.is_file())
        self.assertFalse(log_path.is_file())
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("stored", reg["shortcuts"])

        # 3. Linked file is preserved when shortcut is removed with -y
        res_link_rm = self.run_cli(["rm", "linked", "-y"])
        self.assertEqual(res_link_rm.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("linked", reg["shortcuts"])
        self.assertTrue(ext_file.is_file(), "Linked external script must never be deleted!")

    def test_edit_inline_roundtrip(self):
        res = self.run_cli(["new", "inline1", "--", "echo original"])
        self.assertEqual(res.returncode, 0)

        # 1. Edit modifying content (and asserting temp file suffix)
        editor_mod = (
            f'{sys.executable} -c "import sys, os; '
            f'path = sys.argv[1]; '
            f'assert os.path.isfile(path); '
            f'assert path.endswith(\'-inline1.sh\'); '
            f'open(path, \'w\').write(\'echo modified\\n\')"'
        )
        res_edit = self.run_cli(["edit", "inline1"], env={"SHORTCUT_EDITOR_CMD": editor_mod})
        self.assertEqual(res_edit.returncode, 0)
        self.assertIn("Updated inline shortcut 'inline1'.", res_edit.stdout)

        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["inline1"]["inline"], "echo modified")

        # 2. Edit without modifying content
        res_noop = self.run_cli(["edit", "inline1"], env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res_noop.returncode, 0)
        self.assertIn("No changes made to 'inline1'.", res_noop.stdout)

    def test_new_oneliner_and_template(self):
        # Inline oneliner: description defaults to command
        res1 = self.run_cli(["new", "hello", "--", "echo", "hello", "world"])
        self.assertEqual(res1.returncode, 0)
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["hello"]["inline"], "echo hello world")
        self.assertEqual(reg["shortcuts"]["hello"]["description"], "echo hello world")

        # Template script: description defaults to "(no description)"
        res2 = self.run_cli(["new", "template"], env={"SHORTCUT_EDITOR_CMD": "true"})
        self.assertEqual(res2.returncode, 0)
        script_file = self.shortcuts_home / "scripts" / "template.sh"
        self.assertTrue(script_file.is_file())
        self.assertTrue(os.access(script_file, os.X_OK))
        content = script_file.read_text(encoding="utf-8")
        self.assertEqual(content, "#!/usr/bin/env bash\nset -euo pipefail\n\n")

        reg2 = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg2["shortcuts"]["template"]["description"], "(no description)")

    def test_new_without_terminal_skips_editor(self):
        self.env.pop("SHORTCUT_EDITOR_CMD", None)
        res = self.run_cli(["new", "tmpl", "--desc", "x"], env={"EDITOR": "/bin/false"})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("Edit it with: shortcut edit tmpl", res.stdout)
        self.assertTrue((self.shortcuts_home / "scripts" / "tmpl.sh").is_file())

    def test_new_and_edit_use_EDITOR_then_fall_back_to_nano(self):
        fakebin = self.folder("fakebin")
        for editor in ("myeditor", "nano"):
            self.write_file(fakebin / editor, f'#!/usr/bin/env bash\necho "# edited by {editor}" >> "$1"\n', mode=0o755)
        path = f"{fakebin}:{self.env.get('PATH', '')}"
        self.env.pop("SHORTCUT_EDITOR_CMD", None)

        rc, out = self.run_in_terminal(["new", "tmpl", "--desc", "x"], env={"PATH": path, "VISUAL": "", "EDITOR": "myeditor"})
        self.assertEqual(rc, 0, out)
        script = self.shortcuts_home / "scripts" / "tmpl.sh"
        self.assertIn("# edited by myeditor", script.read_text(), out)

        res = self.run_cli(["edit", "tmpl"], env={"PATH": path, "VISUAL": "", "EDITOR": ""})
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("# edited by nano", script.read_text())

    def test_new_with_missing_editor_creates_nothing(self):
        res = self.run_cli(["new", "tmpl", "--desc", "x"], env={"SHORTCUT_EDITOR_CMD": "no-such-editor-xyz"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("editor not found: no-such-editor-xyz", res.stderr)
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists() and "tmpl" in self.load_global())
        self.assertFalse((self.shortcuts_home / "scripts" / "tmpl.sh").exists())

    def test_new_editor_failure_creates_nothing(self):
        res = self.run_cli(["new", "tmpl", "--desc", "x"], env={"SHORTCUT_EDITOR_CMD": "false"})
        self.assertEqual(res.returncode, 1)
        self.assertIn("was not created", res.stderr)
        self.assertNotIn("tmpl", self.load_global() if (self.shortcuts_home / "shortcuts.json").exists() else {})
        self.assertFalse((self.shortcuts_home / "scripts" / "tmpl.sh").exists())

    def test_editor_failure_is_clean_error(self):
        self.run_cli(["new", "inline_fail", "--", "echo test"])
        res = self.run_cli(["edit", "inline_fail"], env={"SHORTCUT_EDITOR_CMD": "false"})
        self.assertEqual(res.returncode, 1)
        self.assertTrue(res.stderr.strip().startswith("error:"), f"Expected stderr to start with 'error:', got:\n{res.stderr}")
        self.assertNotIn("Traceback", res.stderr)

    def test_rm_malformed_entry(self):
        reg = {"shortcuts": {"bad_entry": {"description": "x", "inline": "true", "cwd": 123}}}
        self.write_registry(self.shortcuts_home, reg)
        res = self.run_cli(["rm", "bad_entry", "-y"])
        self.assertEqual(res.returncode, 0)
        after = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertNotIn("bad_entry", after.get("shortcuts", {}))

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
            self.assertIn(message, self.run_cli(["fix"]).stdout)

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
        for words in (["r", "both"], ["show", "both"], ["location", "both"], ["edit", "both"]):
            res = self.run_cli(words, env={"SHORTCUT_EDITOR_CMD": "true"})
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
        proj = self.project(OWN, imports={"tools": "../../shared/tools.shortcut"}, approved=True)
        self.assertEqual(self.run_cli(["r", "tools", "hi"], cwd=str(proj)).stdout, "hi from tools\n")
        self.assertEqual(self.run_cli(["r", "own"], cwd=str(proj)).stdout, "own\n")

    def test_namespace_collision_and_nesting_errors(self):
        proj = self.project({"docker": {"inline": "echo shortcut"}, "ok": {"inline": "echo ok"}})
        imports = proj / ".shortcuts" / "imports"
        self.write_import(proj / ".shortcuts", "docker", DOCKER_PS)
        self.run_cli(["new", "outside", "--", "echo outside"])
        self.allow(proj / ".shortcuts")

        for words in (["docker"], ["docker", "ps"], ["ok"]):
            res = self.run_cli(["r", *words], cwd=str(proj))
            self.assertEqual(res.returncode, 1)
            self.assertIn("'docker' is both a shortcut and a namespace", res.stderr)
        # Only that store: names from a healthy one still run
        self.assertEqual(self.run_cli(["r", "outside"], cwd=str(proj)).stdout, "outside\n")
        self.assertIn("BROKEN: 'docker' is both", self.run_cli(["fix"], cwd=str(proj)).stdout)

        (imports / "docker.shortcut").unlink()
        (imports / "nested.shortcut").write_text('{"version": 2, "imports": {"x": "x.shortcut"}, "shortcuts": {}}', encoding="utf-8")
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

        # Adding to it is a change, so it needs approving again
        self.run_cli(["new", "ps", "--namespace", "docker", "--", "echo ps"])
        self.assertIn("changed since you approved it", self.run_cli(["r", "docker", "ps"]).stderr)
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
            res = self.run_cli(["add", "delete", str(source), "--namespace", group, "--desc", "d"])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertEqual(self.run_cli(["update", group, "-y"]).returncode, 0)
        self.assertEqual(self.run_cli(["r", "docker", "delete"]).stdout, "dock\n")
        self.assertEqual(self.run_cli(["r", "kube", "delete"]).stdout, "kub\n")

        copy = self.shortcuts_home / "imports" / "scripts" / "docker" / "delete.sh"
        self.assertEqual(self.run_cli(["location", "docker", "delete"]).stdout, f"{copy}\n")
        self.assertEqual(self.run_cli(["rm", "docker", "delete", "-y"]).returncode, 0)
        self.assertFalse(copy.exists())
        self.assertTrue((self.shortcuts_home / "imports" / "scripts" / "kube" / "delete.sh").exists())

    def test_namespace_flag_in_a_project_uses_the_files_folder_for_cwd(self):
        proj = self.project(dirs=["src"])
        res = self.run_cli(
            ["new", "where", "--namespace", "dev", "--local", "--cwd", str(proj / "src"), "--", "pwd"], cwd=str(proj)
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        path = proj / ".shortcuts" / "imports" / "dev.shortcut"
        self.assertEqual(json.loads(path.read_text(encoding="utf-8"))["shortcuts"]["where"]["cwd"], "../../src")
        self.assertEqual(self.run_cli(["update", "dev", "-y"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["r", "dev", "where"], cwd=str(proj)).stdout, f"{proj / 'src'}\n")

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

    def test_new_in_a_namespace_names_it_in_the_edit_hint(self):
        res = self.run_cli(["new", "ps", "--namespace", "docker", "--desc", "d"])
        self.assertIn("Edit it with: shortcut edit docker ps  (file:", res.stdout)


class TestBrokenStores(ShortcutTestCase):
    def test_broken_project_never_stops_core_commands_or_unrelated_global_shortcuts(self):
        self.run_cli(["new", "outside", "--", "echo outside"])
        proj = self.broken_project()

        res = self.run_cli(["r", "outside"], cwd=str(proj))
        self.assertEqual(res.stdout, "outside\n")
        self.assertIn("this is the global 'outside'", res.stderr)
        self.assertIn("outside", self.run_cli(["list", "--names"], cwd=str(proj)).stdout.split())
        self.assertIn("corrupt registry", self.run_cli(["list"], cwd=str(proj)).stderr)
        report = self.run_cli(["fix"], cwd=str(proj))
        self.assertEqual(report.returncode, 0)
        self.assertIn("BROKEN: corrupt registry", report.stdout)
        self.assertEqual(self.run_cli(["update"], cwd=str(proj)).returncode, 1)

    def test_broken_project_refuses_a_name_it_had_when_approved(self):
        self.run_cli(["new", "build", "--", "echo global"])
        self.run_cli(["new", "other", "--", "echo other"])
        proj = self.project({"build": {"inline": "echo project"}}, approved=True)
        registry = proj / ".shortcuts" / "shortcuts.json"
        good = registry.read_text(encoding="utf-8")
        registry.write_text("<<<<<<< HEAD\n" + good, encoding="utf-8")

        res = self.run_cli(["r", "build"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertEqual(res.stdout, "")
        self.assertIn("corrupt registry", res.stderr)
        self.assertIn("shortcut run --global build", res.stderr)
        self.assertEqual(self.run_cli(["r", "--global", "build"], cwd=str(proj)).stdout, "global\n")

        res = self.run_cli(["r", "other"], cwd=str(proj))
        self.assertEqual(res.stdout, "other\n")
        self.assertIn("this is the global 'other'", res.stderr)

        registry.write_text(good, encoding="utf-8")
        self.assertEqual(self.run_cli(["r", "build"], cwd=str(proj)).stdout, "project\n")

    def test_broken_project_stops_rm_and_edit_reaching_the_global_one(self):
        self.run_cli(["new", "x", "--", "echo g"])
        proj = self.broken_project()
        for args in (["rm", "x", "-y"], ["edit", "x"]):
            res = self.run_cli(args, cwd=str(proj))
            self.assertEqual(res.returncode, 1, args)
            self.assertIn("corrupt registry", res.stderr)
            self.assertIn("add --global", res.stderr)
        self.assertIn("x", self.load_global())

        self.assertEqual(self.run_cli(["show", "x"], cwd=str(proj)).returncode, 0)
        self.assertEqual(self.run_cli(["rm", "--global", "x", "-y"], cwd=str(proj)).returncode, 0)
        self.assertNotIn("x", self.load_global())

    def test_broken_project_offers_the_global_one_only_when_there_is_one(self):
        proj = self.project({"go": {"inline": "echo project"}}, approved=True)
        self.corrupt_store(proj / ".shortcuts")

        res = self.run_cli(["r", "go"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("Repair the file.", res.stderr)
        self.assertNotIn("--global", res.stderr)

        self.run_cli(["new", "go", "--", "echo global"])
        res = self.run_cli(["r", "go"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("shortcut run --global go", res.stderr)
        self.assertEqual(self.run_cli(["r", "--global", "go"], cwd=str(proj)).stdout, "global\n")

    def test_broken_project_refuses_a_namespace_it_imported(self):
        self.run_cli(["new", "docker", "--", "echo global"])
        proj = self.project(
            {},
            imports={"docker": "../tools/docker.shortcut"},
            files={"tools/docker.shortcut": json.dumps({"version": 2, "shortcuts": {"ps": {"inline": "true"}}})},
        )
        self.assertEqual(self.run_cli(["update", ".", "-y"], cwd=str(proj)).returncode, 0)
        self.corrupt_store(proj / ".shortcuts")

        res = self.run_cli(["r", "docker"], cwd=str(proj))
        self.assertEqual(res.returncode, 1)
        self.assertIn("shortcut run --global docker", res.stderr)

    def test_unknown_field_warning_does_not_say_the_project_is_skipped(self):
        self.run_cli(["new", "outside", "--", "echo outside"])
        proj = self.project({"inside": {"inline": "true"}})
        registry = proj / ".shortcuts" / "shortcuts.json"
        data = json.loads(registry.read_text(encoding="utf-8"))
        registry.write_text(json.dumps({**data, "bogus": 1}), encoding="utf-8")

        res = self.run_cli(["show", "outside"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("unknown field: bogus; this is the global 'outside'", res.stderr)
        self.assertNotIn("skipped", res.stderr)
        self.assertEqual(self.run_cli(["show", "inside"], cwd=str(proj)).returncode, 0)

    def test_broken_project_does_not_call_its_own_namespace_global(self):
        proj = self.broken_project()
        self.write_import(proj / ".shortcuts", "docker", {"version": 2, "shortcuts": {"ps": {"inline": "true"}}})
        res = self.run_cli(["show", "docker", "ps"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("this is the global", res.stderr)

    def test_broken_global_store_still_reports(self):
        self.corrupt_store(self.shortcuts_home)
        res = self.run_cli(["r", "anything"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("corrupt registry", res.stderr)
        self.assertNotIn("Traceback", res.stderr)
        self.assertIn("BROKEN: corrupt registry", self.run_cli(["fix", "anything"]).stdout)


class TestMigrate(ShortcutTestCase):
    def test_migrate_legacy_flat_store(self):
        # Create flat legacy scripts directly in ~/.shortcuts
        penpot_script = self.write_file(
            self.shortcuts_home / "penpot.sh",
            "#!/usr/bin/env bash\n"
            "# Start Penpot (http://localhost:9001)\n"
            "echo penpot_running\n",
            mode=0o755,
        )
        no_comment_script = self.write_file(
            self.shortcuts_home / "raw.sh",
            "#!/usr/bin/env bash\n"
            "echo raw_running\n",
            mode=0o755,
        )

        res_mig = self.run_cli(["migrate"])
        self.assertEqual(res_mig.returncode, 0)
        self.assertIn("penpot.sh -> penpot (Start Penpot (http://localhost:9001))", res_mig.stdout)
        self.assertIn("raw.sh -> raw ((migrated))", res_mig.stdout)

        # Original flat files must be moved
        self.assertFalse(penpot_script.exists())
        self.assertFalse(no_comment_script.exists())

        # New files in scripts/
        migrated_penpot = self.shortcuts_home / "scripts" / "penpot.sh"
        migrated_raw = self.shortcuts_home / "scripts" / "raw.sh"
        self.assertTrue(migrated_penpot.is_file())
        self.assertTrue(migrated_raw.is_file())
        self.assertTrue(os.access(migrated_penpot, os.X_OK))

        # Check registry
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertIn("penpot", reg["shortcuts"])
        self.assertEqual(reg["shortcuts"]["penpot"]["description"], "Start Penpot (http://localhost:9001)")
        self.assertEqual(reg["shortcuts"]["penpot"]["script"], "scripts/penpot.sh")

        self.assertIn("raw", reg["shortcuts"])
        self.assertEqual(reg["shortcuts"]["raw"]["description"], "(migrated)")
        self.assertEqual(reg["shortcuts"]["raw"]["script"], "scripts/raw.sh")

        # Run migrated shortcut
        res_run = self.run_cli(["run", "penpot"])
        self.assertEqual(res_run.returncode, 0)
        self.assertIn("penpot_running", res_run.stdout)

    def test_migrate_idempotent(self):
        self.write_file(self.shortcuts_home / "job.sh", "#!/usr/bin/env bash\n# Run job\necho job\n", mode=0o755)

        res1 = self.run_cli(["migrate"])
        self.assertEqual(res1.returncode, 0)
        self.assertIn("job.sh -> job", res1.stdout)

        reg1 = shortcut_mod.load_registry(str(self.shortcuts_home))

        res2 = self.run_cli(["migrate"])
        self.assertEqual(res2.returncode, 0)
        self.assertIn("Nothing to migrate.", res2.stdout)

        reg2 = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg1, reg2)

    def test_migrate_skips_existing_target(self):
        existing_script_a = self.write_file(self.shortcuts_home / "scripts" / "script_a.sh", "#!/bin/sh\necho original_a\n")

        init_reg = {
            "version": 1,
            "shortcuts": {
                "script_a": {
                    "description": "Original A",
                    "script": "scripts/script_a.sh",
                },
                "script_b": {
                    "description": "Already registered B",
                    "inline": "echo orig_b",
                },
            },
        }
        self.write_registry(self.shortcuts_home, init_reg)

        flat_a = self.write_file(self.shortcuts_home / "script_a.sh", "#!/bin/sh\n# Migrating A\necho new_a\n")

        flat_b = self.write_file(self.shortcuts_home / "script_b.sh", "#!/bin/sh\n# Migrating B\necho new_b\n")

        res = self.run_cli(["migrate"])
        self.assertEqual(res.returncode, 0)
        self.assertIn("skipped script_a.sh (script_a already exists)", res.stdout)
        self.assertIn("skipped script_b.sh (script_b already exists)", res.stdout)

        # Legacy files left in place
        self.assertTrue(flat_a.is_file())
        self.assertTrue(flat_b.is_file())

        # Existing script and registry entries unchanged
        self.assertEqual(existing_script_a.read_text(encoding="utf-8"), "#!/bin/sh\necho original_a\n")
        reg = shortcut_mod.load_registry(str(self.shortcuts_home))
        self.assertEqual(reg["shortcuts"]["script_a"]["description"], "Original A")
        self.assertEqual(reg["shortcuts"]["script_b"]["description"], "Already registered B")
        self.assertEqual(reg["shortcuts"]["script_b"]["inline"], "echo orig_b")

    def test_migrate_args_and_validation(self):
        # 1. migrate --help leaves old.sh in place
        old_script = self.write_file(self.shortcuts_home / "old.sh", "#!/bin/sh\n# Old script\necho old\n", mode=0o755)

        res_help = self.run_cli(["migrate", "--help"])
        self.assertEqual(res_help.returncode, 0)
        self.assertIn("usage: shortcut migrate", res_help.stdout)
        self.assertTrue(old_script.is_file(), "migrate --help must leave old.sh in place")
        self.assertFalse((self.shortcuts_home / "shortcuts.json").exists())

        # 2. my.tool.sh (invalid name) and symlink are reported as skipped and stay where they are
        invalid_script = self.write_file(self.shortcuts_home / "my.tool.sh", "#!/bin/sh\n# Dot in name\necho invalid\n", mode=0o755)

        target_script = self.write_file(self.tmp_path / "symlink_target.sh", "#!/bin/sh\n# Target\necho target\n")
        symlink_script = self.shortcuts_home / "symlink.sh"
        symlink_script.symlink_to(target_script)

        res_migrate = self.run_cli(["migrate"])
        self.assertEqual(res_migrate.returncode, 0)
        self.assertIn("skipped my.tool.sh", res_migrate.stdout)
        self.assertIn("skipped symlink.sh", res_migrate.stdout)

        # Both skipped files stay where they are
        self.assertTrue(invalid_script.is_file())
        self.assertTrue(symlink_script.is_symlink())

        # old.sh was valid and got migrated
        self.assertFalse(old_script.exists())
        self.assertTrue((self.shortcuts_home / "scripts" / "old.sh").is_file())

    def test_migrate_converts_names_prefixes_and_dir(self):
        fixed = self.folder("fixed")
        self.write_file(self.xdg_data_home / "applications" / "shortcut-g_old.desktop", "[Desktop Entry]\n")
        self.write_registry(self.shortcuts_home, {
            "version": 1,
            "shortcuts": {
                "g_search": {"description": "s", "inline": "pwd"},
                "glob_two": {"description": "t", "inline": "pwd"},
                "global_three": {"description": "t", "inline": "pwd"},
                "g_old": {"description": "o", "inline": "pwd"},
                "g_taken": {"description": "new", "inline": "echo new"},
                "taken": {"description": "existing", "inline": "echo existing"},
                "g_": {"description": "nothing left", "inline": "true"},
                "penpot": {"description": "p", "inline": "pwd", "dir": str(fixed)},
                "C_build": {"description": "b", "inline": "pwd"},
            },
            "prefixes": {"C_": str(fixed)},
        })

        res = self.run_cli(["migrate"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("g_search -> search (cwd: caller)", res.stdout)
        self.assertIn("skipped g_taken (taken already exists)", res.stdout)
        self.assertIn("shortcut r desktop old", res.stdout)
        data = json.loads((self.shortcuts_home / "shortcuts.json").read_text())
        reg = data["shortcuts"]
        self.assertEqual(data["version"], 2)
        self.assertNotIn("prefixes", data)
        self.assertEqual(reg["search"]["cwd"], "caller")
        self.assertIn("two", reg)
        self.assertIn("three", reg)
        self.assertNotIn("g_search", reg)
        self.assertEqual(reg["penpot"]["cwd"], str(fixed))
        self.assertNotIn("dir", reg["penpot"])
        self.assertEqual(reg["C_build"]["cwd"], str(fixed))

        # Collisions don't overwrite: both are still there, as they were
        self.assertEqual(reg["taken"]["inline"], "echo existing")
        self.assertEqual(reg["g_taken"]["inline"], "echo new")
        self.assertIn("g_", reg)

        self.assertEqual(self.run_cli(["r", "penpot"]).stdout, f"{fixed}\n")
        second = self.run_cli(["migrate"])
        self.assertEqual(json.loads((self.shortcuts_home / "shortcuts.json").read_text()), data)
        self.assertIn("skipped g_taken", second.stdout)

    def test_migrate_project_keeps_where_things_ran(self):
        proj = self.project(
            {
                "root": {"description": "r", "inline": "pwd"},
                "sub": {"description": "s", "inline": "pwd", "dir": "sub"},
                "g_here": {"description": "h", "inline": "pwd"},
            },
            version=1,
            dirs=["sub"],
        )

        res = self.run_cli(["r", "sub"], cwd=str(proj))
        self.assertIn("still uses the old 'dir' field; convert it with: shortcut migrate", res.stderr)

        res = self.run_cli(["migrate"], cwd=str(proj))
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("needs approving", res.stderr)
        self.allow(proj / ".shortcuts")
        self.assertEqual(self.run_cli(["r", "root"], cwd=str(proj / "sub")).stdout, f"{proj}\n")
        self.assertEqual(self.run_cli(["r", "sub"], cwd=str(proj)).stdout, f"{proj / 'sub'}\n")
        self.assertEqual(self.run_cli(["r", "here"], cwd=str(proj / "sub")).stdout, f"{proj / 'sub'}\n")

        # A 2.0 store is left alone: no 'cwd' there means caller
        fresh = self.project({"mine": {"inline": "pwd"}}, name="fresh")
        self.run_cli(["migrate"], cwd=str(fresh))
        self.assertNotIn("cwd", json.loads((fresh / ".shortcuts" / "shortcuts.json").read_text())["shortcuts"]["mine"])


if __name__ == "__main__":
    unittest.main()
