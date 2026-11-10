#!/usr/bin/env python3
"""Running a shortcut: arguments, exit codes, signals, terminals, directories."""

import json
import signal
import time
import unittest
import unittest.mock

from fixtures import CLI_SHELL, ShortcutTestCase, shortcut_mod


class TestRunning(ShortcutTestCase):
    def test_run_inline_with_passthrough_args(self):
        res_new = self.run_cli(["new", "echo", "--", "echo name=$0 first=$1 all=$*"])
        self.assertEqual(res_new.returncode, 0)

        res = self.run_cli(["run", "echo", "-v", "--option", "arg1", "arg2"])
        self.assertEqual(res.returncode, 0)
        self.assertEqual(res.stdout.strip(), "name=echo first=-v all=-v --option arg1 arg2")

        res_run = self.run_cli(["run", "echo", "-v", "--option", "arg1", "arg2"])
        self.assertEqual(res_run.returncode, 0)
        self.assertEqual(res_run.stdout.strip(), "name=echo first=-v all=-v --option arg1 arg2")

    def test_the_environment_says_what_runs_and_from_where(self):
        show = 'echo "$SHORTCUT_NAME|$SHORTCUT_NAMESPACE|$SHORTCUT_CALLER_CWD|${SC_ARG_OLD-unset}|${SC_ARG_X-unset}"'
        self.write_import(self.shortcuts_home, "dk", {"version": 2, "shortcuts": {
            "env": {"inline": show, "args": [{"name": "x", "default": "d"}]}}})
        self.run_cli(["update", "-y", "dk"])
        self.run_cli(["new", "env", "--", show])
        caller = self.folder("caller")
        outer = {"SC_ARG_OLD": "outer", "SC_ARG_X": "outer", "SHORTCUT_NAMESPACE": "outer"}
        res = self.run_cli(["r", "dk", "env"], cwd=str(caller), env=outer)
        self.assertEqual(res.stdout, f"dk env|dk|{caller}|unset|d\n", res.stderr)
        res = self.run_cli(["r", "env"], cwd=str(caller), env=outer)
        self.assertEqual(res.stdout, f"env||{caller}|unset|unset\n", res.stderr)

    def test_a_name_alone_never_runs_a_shortcut(self):
        self.run_cli(["new", "hello", "--", "echo hello"])
        for argv in (["hello"], ["nano", "hello"]):
            res = self.run_cli(argv)
            self.assertEqual(res.returncode, 2)
            self.assertEqual(res.stdout, "")
            self.assertIn(f"unknown command '{argv[0]}'", res.stderr)
            self.assertIn(f"shortcut run {argv[0]}", res.stderr)

    def test_r_is_short_for_run(self):
        self.run_cli(["new", "hello", "--", 'echo "hello $1"'])
        self.assertEqual(self.run_cli(["r", "hello", "you"]).stdout, "hello you\n")
        self.assertIn("shortcut run|r [--global] [-y] <name>", self.run_cli(["r", "--help"]).stdout)

    def test_command_words_are_ordinary_shortcut_names(self):
        for name in ("new", "list", "run", "r", "nano"):
            res = self.run_cli(["new", name, "--", f"echo ran {name}"])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertEqual(self.run_cli(["run", name]).stdout, f"ran {name}\n")
        self.assertIn("run", self.run_cli(["list", "--names"]).stdout.split())

    def test_script_shebang_without_exec_and_elf(self):
        script = self.script("py_script.py", "#!/usr/bin/env python3\nimport sys\nprint('py', 1 + 1)\n", mode=0o644)
        self.assertEqual(self.run_cli(["new", "py", "--copy", str(script), "--desc", "python"]).returncode, 0)
        res = self.run_cli(["run", "py"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("py 2", res.stdout)

        sh_no_exec = self.script("no_shebang.sh", "echo nonexec\n", mode=0o644)
        self.assertEqual(self.run_cli(["new", "noshebang", "--copy", str(sh_no_exec), "--desc", "noshebang"]).returncode, 0)
        res2 = self.run_cli(["run", "noshebang"])
        self.assertEqual(res2.returncode, 0, res2.stderr)
        self.assertIn("nonexec", res2.stdout)

        sh_exec = self.script("exec.sh", "#!/usr/bin/env bash\necho exec_shebang\n")
        self.assertEqual(self.run_cli(["new", "exec", "--copy", str(sh_exec), "--desc", "exec"]).returncode, 0)
        res3 = self.run_cli(["run", "exec"])
        self.assertEqual(res3.returncode, 0, res3.stderr)
        self.assertIn("exec_shebang", res3.stdout)

    def test_run_help(self):
        res1 = self.run_cli(["run", "--help"])
        self.assertEqual(res1.returncode, 0)
        self.assertIn("shortcut run|r [--global] [-y] <name> [args...]", res1.stdout)

        res2 = self.run_cli(["run", "-h"])
        self.assertEqual(res2.returncode, 0)
        self.assertIn("shortcut run|r [--global] [-y] <name> [args...]", res2.stdout)

        self.assertEqual(self.run_cli(["new", "echo_args", "--", "echo \"args: $@\""]).returncode, 0)
        res3 = self.run_cli(["run", "echo_args", "--help"])
        self.assertEqual(res3.returncode, 0)
        self.assertIn("args: --help", res3.stdout)

    def test_run_cwd_modes(self):
        fixed = self.folder("fixed")
        proj = self.project(
            {
                "default": {"inline": "pwd"},
                "caller": {"inline": "pwd", "cwd": "caller"},
                "root": {"inline": "pwd", "cwd": "."},
                "sibling": {"inline": "pwd", "cwd": "frontend"},
                "fixed": {"inline": "pwd", "cwd": str(fixed)},
                "gone": {"inline": "pwd", "cwd": "nope"},
            },
            dirs=["frontend", "src"],
            approved=True,
        )
        here = proj / "src"
        for name, expected in (
            ("default", here),
            ("caller", here),
            ("root", proj),
            ("sibling", proj / "frontend"),
            ("fixed", fixed),
        ):
            res = self.run_cli(["r", name], cwd=str(here))
            self.assertEqual(res.stdout, f"{expected}\n", name)
            self.assertIn(f"→ {expected}\n", res.stderr)
        res = self.run_cli(["r", "gone"], cwd=str(here))
        self.assertEqual(res.returncode, 1)
        self.assertIn("run directory does not exist", res.stderr)

    def test_run_directory_line(self):
        work = self.folder("work")
        self.assertEqual(self.run_cli(["new", "pwd", "--", "pwd"]).returncode, 0)
        res = self.run_cli(["run", "pwd"], cwd=str(work))
        self.assertEqual(res.stdout, f"{work}\n")
        self.assertEqual(res.stderr, f"\u2192 {work}\n")
        res = self.run_cli(["run", "pwd"], cwd=str(work), env={"SHORTCUT_SHOW_DIR": "0"})
        self.assertEqual(res.stderr, "")
        rc, out = self.run_in_terminal(["run", "pwd"], cwd=str(work))
        self.assertIn(f"\x1b[2m\u2192 {work}\x1b[0m", out)

    def test_confirm(self):
        self.run_cli(["new", "danger", "--confirm", "--", "echo ran; pwd"])
        self.assertTrue(self.load_global()["danger"]["confirm"])
        work = self.folder("work")

        res = self.run_cli(["r", "danger"], cwd=str(work))
        self.assertEqual(res.returncode, 1)
        self.assertEqual(res.stdout, "")
        self.assertIn("pass -y", res.stderr)

        res = self.run_cli(["r", "-y", "danger"], cwd=str(work))
        self.assertEqual(res.returncode, 0)
        self.assertIn("ran", res.stdout)

        prompt = f"run 'danger' in {work}? [y/N]".encode()
        rc, out = self.run_in_terminal(["r", "danger"], cwd=str(work), keys=[(prompt, b"y\n")])
        self.assertEqual(rc, 0)
        self.assertIn("ran", out)
        rc, out = self.run_in_terminal(["r", "danger"], cwd=str(work), keys=[(prompt, b"n\n")])
        self.assertEqual(rc, 1)
        self.assertNotIn("ran\r\n", out)

        # The description shows above the question, and not with -y
        self.run_cli(["new", "wipe", "--confirm", "--desc", "Wipes the cache", "--", "echo ran"])
        prompt = f"run 'wipe' in {work}? [y/N]".encode()
        rc, out = self.run_in_terminal(["r", "wipe"], cwd=str(work), keys=[(prompt, b"n\n")])
        self.assertEqual(rc, 1)
        self.assertIn("Wipes the cache\r\n", out)
        self.assertLess(out.index("Wipes the cache"), out.index("run 'wipe'"))
        self.assertNotIn("Wipes the cache", self.run_cli(["r", "-y", "wipe"], cwd=str(work)).stdout)
        # No description given: nothing above the question, not a placeholder or the command
        self.run_cli(["new", "plain", "--confirm", "--", "echo ran"])
        self.write_store(self.shortcuts_home, {**self.load_global(),
                                               "noted": {"script": "scripts/none.sh", "description": "(no description)", "confirm": True}})
        for name in ("plain", "noted"):
            prompt = f"run '{name}' in {work}? [y/N]".encode()
            rc, out = self.run_in_terminal(["r", name], cwd=str(work), keys=[(prompt, b"n\n")])
            self.assertEqual(rc, 1)
            self.assertNotIn("echo ran", out)
            self.assertNotIn("(no description)", out)
        # The description and question go to the terminal even when stdout is a file
        rc, out = self.run_in_terminal(["r", "wipe"], cwd=str(work), stdout_file=self.tmp_path / "out.txt",
                                       keys=[(b"[y/N]", b"y\n")])
        self.assertEqual(rc, 0)
        self.assertIn("Wipes the cache", out)
        self.assertEqual((self.tmp_path / "out.txt").read_text(), "ran\n")

        # The question names the arguments, and the hint is the same run with -y
        self.run_cli(["new", "drop", "--confirm", "--", 'echo "dropped $*"'])
        prompt = f"run 'drop' with -f 'my db' in {work}? [y/N]".encode()
        rc, out = self.run_in_terminal(["r", "drop", "-f", "my db"], cwd=str(work), keys=[(prompt, b"n\n")])
        self.assertEqual(rc, 1, out)
        res = self.run_cli(["r", "--global", "drop", "-f", "my db"], cwd=str(work))
        self.assertIn("pass -y (shortcut r -y --global drop -f 'my db')\n", res.stderr)
        self.assertEqual(self.run_cli(["r", "-y", "--global", "drop", "-f", "my db"], cwd=str(work)).stdout, "dropped -f my db\n")

        # After the name, -y belongs to the script
        self.run_cli(["new", "echoes", "--", 'echo "$@"'])
        self.assertEqual(self.run_cli(["r", "echoes", "-y"]).stdout, "-y\n")

    def test_show_prints_the_entry_script_cwd_and_args(self):
        script_file = self.script("shown.sh", "#!/usr/bin/env bash\necho show output running\n")
        self.run_cli(["new", "shown", "--copy", str(script_file), "--desc", "Show sample", "--cwd", str(self.tmp_path),
                      "--arg", "target", "--arg", "mode=fast"])
        res = self.run_cli(["show", "shown"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("Description: Show sample\n", res.stdout)
        self.assertIn("echo show output running", res.stdout)
        self.assertIn(f"Run CWD:     {self.tmp_path.resolve()}\n", res.stdout)
        self.assertIn("Usage:       shortcut r shown <target> [mode]\n", res.stdout)

    def test_show_says_when_the_run_directory_or_script_is_gone(self):
        self.write_store(self.shortcuts_home, {"gone": {"inline": "true", "cwd": "/no/such/folder"}})
        self.assertIn("/no/such/folder", self.run_cli(["show", "gone"]).stdout)
        self.write_store(self.shortcuts_home, {"gone": {"script": "scripts/none.sh"}})
        self.assertIn("[file missing:", self.run_cli(["show", "gone"]).stdout)

    def test_show_json_prints_an_entry_show_refuses(self):
        self.write_store(self.shortcuts_home, {"bad": {"inline": "true", "cwd": 123}})
        self.assertIn("'cwd' must be a string", self.run_cli(["show", "bad"]).stderr)
        res = self.run_cli(["show", "--json", "bad"])
        self.assertEqual(res.returncode, 0, res.stderr)
        info = json.loads(res.stdout)
        self.assertEqual(info["entry"], {"inline": "true", "cwd": 123})
        self.assertIn("'cwd' must be a string", info["error"])
        self.assertEqual(info["name"], "bad")
        self.assertIsNone(info["cwd"])
        self.assertIsNone(info["run_cwd"])
        self.assertIsNone(info["script_path"])

    def test_show_json_is_one_object_with_where_it_resolves(self):
        self.write_store(self.shortcuts_home, {"s": {"script": "scripts/s.sh", "cwd": "/tmp"}, "c": {"inline": "true"}})
        info = json.loads(self.run_cli(["show", "--json", "s"]).stdout)
        self.assertEqual(info["name"], "s")
        self.assertIsNone(info["namespace"])
        self.assertEqual(info["scope"], "global")
        self.assertEqual(info["defined_in"], str(self.shortcuts_home / "shortcuts.json"))
        self.assertIsNone(info["source"])
        self.assertEqual(info["script_path"], str(self.shortcuts_home / "scripts" / "s.sh"))
        self.assertEqual(info["cwd"], "/tmp")
        self.assertEqual(info["run_cwd"], "/tmp")
        self.assertEqual(info["approval"], "ok")
        self.assertIsNone(info["error"])
        self.assertIs(info["hides_global"], False)
        self.assertEqual(info["entry"], {"script": "scripts/s.sh", "cwd": "/tmp"})

        info = json.loads(self.run_cli(["show", "--json", "c"], cwd=str(self.tmp_path)).stdout)
        self.assertIsNone(info["cwd"])
        self.assertEqual(info["run_cwd"], str(self.tmp_path))

    def test_show_json_names_a_namespace_entry_bare(self):
        self.write_import(self.shortcuts_home, "dk", {"version": 2, "shortcuts": {"ps": {"inline": "true", "cwd": "."}}})
        self.run_cli(["init", "--global"])
        info = json.loads(self.run_cli(["show", "--json", "dk", "ps"]).stdout)
        self.assertEqual(info["name"], "ps")
        self.assertEqual(info["namespace"], "dk")
        self.assertEqual(info["scope"], "namespace")
        self.assertEqual(info["defined_in"], str(self.shortcuts_home / "imports" / "dk.shortcut"))
        self.assertEqual(info["cwd"], ".")
        self.assertEqual(info["run_cwd"], str(self.shortcuts_home / "imports"))
        self.assertEqual(info["approval"], "new")

    def test_unknown_commands_point_to_run(self):
        for word in ("nano", "activate", "version"):
            res = self.run_cli([word, "x"])
            self.assertEqual(res.returncode, 2)
            self.assertEqual(res.stderr, f"error: unknown command '{word}' (to run a shortcut: shortcut run {word})\n")
        res = self.run_cli(["--bogus"])
        self.assertEqual(res.returncode, 2)
        self.assertTrue(res.stderr.startswith("error: unrecognized option '--bogus'\n"), res.stderr)
        self.assertEqual(self.run_cli(["init", "-g"]).returncode, 2)


class TestSignalsAndTerminals(ShortcutTestCase):
    def test_a_run_keeps_the_real_terminal(self):
        inline = '[ -t 1 ] && echo "stdout:tty" || echo "stdout:pipe"; stty size; echo ready; read -r line; echo "got:$line"'
        self.assertEqual(self.run_cli(["new", "tty", "--", inline]).returncode, 0)

        rc, out = self.run_in_terminal(["run", "tty"], keys=[(b"ready", b"hello\r")])
        self.assertEqual(rc, 0, out)
        self.assertIn("stdout:tty", out)
        self.assertIn("30 100", out)
        self.assertIn("got:hello", out)

        res = self.run_cli(["run", "tty"], input="x\n")
        self.assertIn("stdout:pipe", res.stdout)
        self.assertIn("got:x", res.stdout)

    def test_ctrl_c_reaches_the_shortcut(self):
        self.assertEqual(self.run_cli(["new", "sleep", "--", "echo started; sleep 10"]).returncode, 0)
        start = time.time()
        rc, out = self.run_in_terminal(["run", "sleep"], keys=[(b"started", b"\x03")])
        self.assertEqual(rc, -signal.SIGINT, out)
        self.assertLess(time.time() - start, 8)

    def test_exit_code_and_signals_pass_straight_through(self):
        # stdout and stderr stay apart, and the exit code is the shortcut's own
        self.run_cli(["new", "fail", "--", "echo out; echo err >&2; exit 42"])
        res = self.run_cli(["r", "fail"], env={"SHORTCUT_SHOW_DIR": "0"})
        self.assertEqual((res.returncode, res.stdout, res.stderr), (42, "out\n", "err\n"))

        # The run is the shortcut's process: same pid, and nothing in between to relay a signal
        self.run_cli(["new", "pid", "--", 'echo "$$"; exec sleep 30'])
        for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGKILL):
            proc = self.popen_cli(["run", "pid"], text=True)
            assert proc.stdout is not None
            self.assertEqual(int(proc.stdout.readline()), proc.pid)
            proc.send_signal(sig)
            proc.communicate(timeout=5)
            self.assertEqual(proc.returncode, -sig)

        # A signal the shortcut handles is its to answer
        self.run_cli(["new", "trap", "--", "trap 'kill $!; exit 7' USR1; echo ready; sleep 30 >/dev/null 2>&1 & wait"])
        proc = self.popen_cli(["run", "trap"], text=True)
        assert proc.stdout is not None
        self.assertEqual(proc.stdout.readline(), "ready\n")
        proc.send_signal(signal.SIGUSR1)
        proc.communicate(timeout=5)
        self.assertEqual(proc.returncode, 7)

    def test_a_shortcut_starts_with_sigpipe_on(self):
        # Python ignores SIGPIPE, and exec would pass that on: seq would print errors instead of dying
        self.assertEqual(self.run_cli(["new", "seq", "--", "seq 1 1000000"]).returncode, 0)
        p_shortcut = self.popen_cli(["run", "seq"], text=True)
        p_head = self.popen(["head", "-n", "1"], stdin=p_shortcut.stdout, text=True)
        assert p_shortcut.stdout is not None
        p_shortcut.stdout.close()
        head_out, _ = p_head.communicate(timeout=5)
        _, sc_err = p_shortcut.communicate(timeout=5)
        self.assertEqual(head_out.strip(), "1")
        self.assertEqual(p_shortcut.returncode, -signal.SIGPIPE)
        self.assertNotIn("Broken pipe", sc_err)

    def test_a_closed_stdout_is_the_shortcuts_problem(self):
        self.run_cli(["new", "quiet", "--", "true"])
        res = self.run_cmd(["sh", "-c", f"{CLI_SHELL} r quiet >&-"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertNotIn("Traceback", res.stderr)

    def test_a_run_that_cant_start_is_an_error(self):
        self.write_store(self.shortcuts_home, {"go": {"inline": "true"}})
        res = self.run_cli(["r", "go"], env={"PATH": str(self.tmp_path)})
        self.assertEqual(res.returncode, 1)
        self.assertIn("error:", res.stderr)

    def test_on_windows_a_run_is_a_child_with_the_same_exit_code(self):
        cmd = ["sh", "-c", 'test "$PWD" = "$0" && exit 9', str(self.tmp_path)]
        with unittest.mock.patch.object(shortcut_mod, "WINDOWS", True):
            rc = shortcut_mod.exec_child(cmd, str(self.tmp_path), {"PATH": "/usr/bin:/bin"})
        self.assertEqual(rc, 9)

class TestArguments(ShortcutTestCase):
    def test_declared_args(self):
        script = (
            'printf "[%s]" "$@"; echo; '
            'echo "c=$SC_ARG_CONTAINER s=$SC_ARG_SIGNAL e=$SC_ARG_EXTRA"'
        )
        reg = {"kill": {"inline": script, "args": [
            {"name": "container", "required": True, "desc": "Which one"},
            {"name": "signal", "default": "TERM"},
            {"name": "extra", "rest": True},
        ]}}
        self.write_store(self.shortcuts_home, {
            **reg,
            "two": {"inline": 'echo "$#:$*"', "args": [{"name": "a"}, {"name": "b", "default": "B"}]},
            "free": {"inline": 'echo "$#"'},
            "one": {"inline": "true", "args": [{"name": "only"}]},
        })

        res = self.run_cli(["r", "kill"])
        self.assertEqual(res.returncode, 2)
        self.assertIn("missing <container>", res.stderr)
        self.assertIn("usage: shortcut r kill <container> [signal] [extra...]", res.stderr)
        # With a desc on any argument, the error lists them all
        self.assertIn("  container  required       Which one\n", res.stderr)
        self.assertIn("  signal     default: TERM\n", res.stderr)
        self.assertIn("  extra...   optional\n", res.stderr)

        out = self.run_cli(["r", "kill", "web"]).stdout.splitlines()
        self.assertEqual(out, ["[web][TERM]", "c=web s=TERM e="])
        out = self.run_cli(["r", "kill", "web", "KILL", "a b", "c"]).stdout.splitlines()
        self.assertEqual(out, ["[web][KILL][a b][c]", "c=web s=KILL e='a b' c"])

        # Flags are neither counted nor moved, and a default keeps its position before them
        out = self.run_cli(["r", "kill", "web", "--force"]).stdout.splitlines()
        self.assertEqual(out[0], "[web][TERM][--force]")

        # A skipped optional keeps later defaults in their own position
        self.assertEqual(self.run_cli(["r", "two"]).stdout, "2: B\n")

        res = self.run_cli(["r", "one", "a", "b"])
        self.assertEqual(res.returncode, 2)
        self.assertIn("too many arguments", res.stderr)
        self.assertIn("usage: shortcut r one [only]", res.stderr)
        self.assertNotIn("  only", res.stderr)  # no desc anywhere, no list

        # After --, every word is a value, even one starting with a dash; the -- itself is dropped
        self.assertIn("missing <container>", self.run_cli(["r", "kill", "-5"]).stderr)
        out = self.run_cli(["r", "kill", "--force", "--", "-5", "-x"]).stdout.splitlines()
        self.assertEqual(out, ["[--force][-5][-x]", "c=-5 s=-x e="])
        self.assertEqual(self.run_cli(["r", "two", "--", "-a"]).stdout, "2:-a B\n")

        # No `args` key: no checking, and -- passes through
        self.assertEqual(self.run_cli(["r", "free", "1", "2", "3"]).stdout, "3\n")
        self.assertEqual(self.run_cli(["r", "free", "--", "x"]).stdout, "2\n")
        shown = self.run_cli(["show", "kill"]).stdout
        self.assertIn("Usage:       shortcut r kill <container> [signal] [extra...]\n", shown)
        self.assertIn("Arguments:\n  container  required       Which one\n  signal     default: TERM\n  extra...   optional\n", shown)
        self.assertNotIn("Arguments:", self.run_cli(["show", "free"]).stdout)

    def test_new_declares_args(self):
        res = self.run_cli(["new", "greet", "--arg", "who", "--arg", "greeting=hello", "--", 'echo "$2 $1"'])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(
            self.load_global()["greet"]["args"],
            [{"name": "who", "required": True}, {"name": "greeting", "default": "hello"}],
        )
        self.assertEqual(self.run_cli(["r", "greet", "you"]).stdout, "hello you\n")
        self.assertEqual(self.run_cli(["r", "greet"]).returncode, 2)
        res = self.run_cli(["new", "bad", "--arg", "no spaces", "--", "true"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("'bad' is not a valid entry", res.stderr)

    def test_new_declares_rest_args_and_descs(self):
        res = self.run_cli(["new", "ex", "--arg", "container", "--arg", "cmd...", "--arg-desc", "container=Which one",
                            "--arg-desc", "cmd=What to run", "--", 'echo "$SC_ARG_CONTAINER|$SC_ARG_CMD"'])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(self.load_global()["ex"]["args"], [
            {"name": "container", "required": True, "desc": "Which one"},
            {"name": "cmd", "rest": True, "required": True, "desc": "What to run"},
        ])
        self.assertEqual(self.run_cli(["r", "ex", "web", "ls", "/tmp"]).stdout, "web|ls /tmp\n")
        res = self.run_cli(["r", "ex", "web"])
        self.assertEqual(res.returncode, 2)
        self.assertIn("missing <cmd>", res.stderr)
        self.assertIn("What to run", res.stderr)

        # NAME...= is optional, with no default; NAME...=x has one
        self.run_cli(["new", "opt", "--arg", "rest...=", "--", 'echo "$#"'])
        self.assertEqual(self.load_global()["opt"]["args"], [{"name": "rest", "rest": True}])
        self.assertEqual(self.run_cli(["r", "opt"]).stdout, "0\n")
        self.run_cli(["new", "dflt", "--arg", "rest...=x", "--", 'echo "$*"'])
        self.assertEqual(self.load_global()["dflt"]["args"], [{"name": "rest", "rest": True, "default": "x"}])

        # --arg-desc takes the name with or without its ...; a newline shows as \\n
        self.run_cli(["new", "dots", "--arg", "files...", "--arg-desc", "files...=Two\nlines", "--", 'echo "$@"'])
        self.assertEqual(self.load_global()["dots"]["args"][0]["desc"], "Two\nlines")
        self.assertIn("  files...  required  Two\\nlines\n", self.run_cli(["show", "dots"]).stdout)

        for flags, message in (
            (["--arg", "a...", "--arg", "b"], "only the last entry in 'args' can be 'rest'"),
            (["--arg", "a", "--arg", "a"], "arg 'a' is declared twice"),
            (["--arg", "a", "--arg-desc", "a=one", "--arg-desc", "a=two"], "--arg-desc a=two: 'a' already has a desc"),
            (["--arg", "a", "--arg-desc", "b=Nope"], "--arg-desc b=Nope: needs NAME=TEXT, with NAME declared by --arg"),
            (["--arg", "a", "--arg-desc", "a"], "--arg-desc a: needs NAME=TEXT"),
            (["--arg-desc", "a=No args"], "--arg-desc a=No args: needs NAME=TEXT"),
        ):
            res = self.run_cli(["new", "bad", *flags, "--", 'echo "$@"'])
            self.assertEqual(res.returncode, 1, flags)
            self.assertIn(message, res.stderr)
            self.assertNotIn("bad", self.load_global())

    def test_invalid_args_declaration_is_an_error(self):
        self.write_store(self.shortcuts_home, {
            "bad": {"inline": "true", "args": [{"name": "a", "rest": True}, {"name": "b"}]},
        })
        res = self.run_cli(["r", "bad"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("only the last entry in 'args' can be 'rest'", res.stderr)
        self.write_store(self.shortcuts_home, {
            "both": {"inline": "true", "args": [{"name": "a", "required": True, "default": "x"}]},
        })
        res = self.run_cli(["r", "both"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("arg 'a' can't be both 'required' and have a 'default'", res.stderr)

    def test_new_says_when_a_command_ignores_arguments(self):
        hint = "won't see any arguments"
        self.assertIn(hint, self.run_cli(["new", "a", "--", "rg --hidden"]).stderr)
        self.assertIn(hint, self.run_cli(["new", "b", "--", "rg", "--hidden"]).stderr)
        for name, command in (
            ("c", 'rg "$@"'), ("d", "echo $1"), ("e", "echo ${2}"), ("f", "echo $*"), ("g", "echo $SC_ARG_WHO"),
        ):
            res = self.run_cli(["new", name, "--", command])
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertNotIn(hint, res.stderr, command)

    def test_several_word_commands_keep_positional_arguments_live(self):
        cases = {
            ("echo", "$1"): 'echo "$1"',
            ("printf", "[%s]", "$@"): 'printf \'[%s]\' "$@"',
            ("echo", "a b", "${1}"): 'echo \'a b\' "${1}"',
            ("echo", "$HOME"): "echo '$HOME'",
            ("echo", 'say "$1"'): 'echo "say \\"$1\\""',
            ("echo", "$(id) `id` \\ $1"): 'echo "\\$(id) \\`id\\` \\\\ $1"',
        }
        for i, (words, stored) in enumerate(cases.items()):
            name = f"cmd{i}"
            self.assertEqual(self.run_cli(["new", name, "--", *words]).returncode, 0)
            self.assertEqual(self.load_global()[name]["inline"], stored)

        self.assertEqual(self.run_cli(["r", "cmd0", "hello world"]).stdout, "hello world\n")
        self.assertEqual(self.run_cli(["r", "cmd1", "a b", "c"]).stdout, "[a b][c]")
        self.assertEqual(self.run_cli(["r", "cmd3"]).stdout, "$HOME\n")
        self.assertEqual(self.run_cli(["r", "cmd5", "x"]).stdout, "$(id) `id` \\ x\n")


if __name__ == "__main__":
    unittest.main()
