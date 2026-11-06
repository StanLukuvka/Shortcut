#!/usr/bin/env python3
"""Running a shortcut: arguments, logs, exit codes, signals, terminals, directories."""

import os
import pathlib
import signal
import termios
import time
import unittest

from fixtures import ShortcutTestCase


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

    def test_run_script_tee_logging_and_exit_code(self):
        script_file = self.script("script_fail.sh", "#!/usr/bin/env bash\necho stdout line\necho stderr line >&2\nexit 42\n")

        res_add = self.run_cli(["add", "fail42", str(script_file), "--desc", "Fails with 42"])
        self.assertEqual(res_add.returncode, 0)

        res = self.run_cli(["run", "fail42"])
        self.assertEqual(res.returncode, 42)
        self.assertIn("stdout line", res.stdout)
        self.assertIn("stderr line", res.stdout)

        log_path = self.shortcuts_home / "logs" / "fail42.log"
        self.assertTrue(log_path.is_file())
        log_content = log_path.read_text(encoding="utf-8")
        self.assertTrue(log_content.startswith("=== SHORTCUT RUN: fail42 |"))
        self.assertIn("stdout line", log_content)
        self.assertIn("stderr line", log_content)
        self.assertTrue(log_content.rstrip().endswith("=== exit: 42 ==="))

        res_fix = self.run_cli(["fix", "fail42"])
        self.assertEqual(res_fix.returncode, 0)
        self.assertIn("Last exit code: 42", res_fix.stdout)

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
        self.assertEqual(self.run_cli(["add", "py", str(script), "--desc", "python"]).returncode, 0)
        res = self.run_cli(["run", "py"])
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertIn("py 2", res.stdout)

        sh_no_exec = self.script("no_shebang.sh", "echo nonexec\n", mode=0o644)
        self.assertEqual(self.run_cli(["add", "noshebang", str(sh_no_exec), "--desc", "noshebang"]).returncode, 0)
        res2 = self.run_cli(["run", "noshebang"])
        self.assertEqual(res2.returncode, 0, res2.stderr)
        self.assertIn("nonexec", res2.stdout)

        sh_exec = self.script("exec.sh", "#!/usr/bin/env bash\necho exec_shebang\n")
        self.assertEqual(self.run_cli(["add", "exec", str(sh_exec), "--desc", "exec"]).returncode, 0)
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
                "defining": {"inline": "pwd", "cwd": "."},
                "root": {"inline": "pwd", "cwd": ".."},
                "sibling": {"inline": "pwd", "cwd": "../frontend"},
                "fixed": {"inline": "pwd", "cwd": str(fixed)},
                "gone": {"inline": "pwd", "cwd": "../nope"},
            },
            dirs=["frontend", "src"],
            approved=True,
        )
        here = proj / "src"
        for name, expected in (
            ("default", here),
            ("caller", here),
            ("defining", proj / ".shortcuts"),
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

        # After the name, -y belongs to the script
        self.run_cli(["new", "echoes", "--", 'echo "$@"'])
        self.assertEqual(self.run_cli(["r", "echoes", "-y"]).stdout, "-y\n")

    def test_fix_output_formatting(self):
        res_new = self.run_cli(["new", "fix_sample", "--desc", "Fix sample description", "--", "echo", "fix output running"])
        self.assertEqual(res_new.returncode, 0)

        # Before any run: metadata and script present, "no log yet"
        res_fix_pre = self.run_cli(["fix", "fix_sample"])
        self.assertEqual(res_fix_pre.returncode, 0)
        self.assertIn("Fix sample description", res_fix_pre.stdout)
        self.assertIn("fix output running", res_fix_pre.stdout)
        self.assertIn("no log yet", res_fix_pre.stdout)
        self.assertTrue(res_fix_pre.stdout.strip().endswith("Last exit code: none"))

        # Run the shortcut
        res_run = self.run_cli(["run", "fix_sample"])
        self.assertEqual(res_run.returncode, 0)

        # After run: log contents present and ends with last exit code 0
        res_fix_post = self.run_cli(["fix", "fix_sample"])
        self.assertEqual(res_fix_post.returncode, 0)
        self.assertIn("Fix sample description", res_fix_post.stdout)
        self.assertIn("fix output running", res_fix_post.stdout)
        self.assertTrue(res_fix_post.stdout.strip().endswith("Last exit code: 0"))

    def test_removed_commands_are_gone(self):
        for word in ("allow", "prefix", "log", "desktop", "nano", "activate", "version"):
            res = self.run_cli([word, "x"])
            self.assertEqual(res.returncode, 2)
            self.assertEqual(res.stderr, f"unknown command '{word}' (to run a shortcut: shortcut run {word})\n")
        self.assertEqual(self.run_cli(["init", "-g"]).returncode, 2)


class TestSignalsAndTerminals(ShortcutTestCase):
    def test_run_ctrl_c_writes_footer(self):
        sleeper_script = self.script("sleeper.sh", "#!/usr/bin/env bash\necho started\nsleep 10\n")

        res_add = self.run_cli(["add", "sleeper", str(sleeper_script), "--desc", "Sleeping script"])
        self.assertEqual(res_add.returncode, 0)

        proc = self.popen_cli(["run", "sleeper"], bufsize=0, start_new_session=True)
        self.interrupt_once_started(proc)
        self.assertEqual(proc.returncode, 130)

        log_path = self.shortcuts_home / "logs" / "sleeper.log"
        self.assertTrue(log_path.is_file())
        log_text = log_path.read_text(encoding="utf-8")
        self.assertTrue(log_text.rstrip().endswith("=== exit: 130 ==="), f"Footer missing in:\n{log_text}")

    def test_run_nolog_ctrl_c_returns_130(self):
        self.write_file(
            self.shortcuts_home / "scripts" / "nolog_sleep.sh", "#!/usr/bin/env bash\necho started\nsleep 10\n", mode=0o755
        )

        reg = {
            "version": 1,
            "shortcuts": {
                "nolog_sleep": {
                    "description": "Sleeper without log",
                    "script": "scripts/nolog_sleep.sh",
                    "log": False,
                },
            },
        }
        self.write_registry(self.shortcuts_home, reg)

        proc = self.popen_cli(["run", "nolog_sleep"], bufsize=0, start_new_session=True)
        self.interrupt_once_started(proc)
        self.assertEqual(proc.returncode, 130)

    def test_run_on_terminal_uses_pty_and_logs(self):
        inline = '[ -t 1 ] && echo "stdout:tty" || echo "stdout:pipe"; stty size; echo ready; read -r line; echo "got:$line"'
        self.assertEqual(self.run_cli(["new", "tty", "--", inline]).returncode, 0)

        rc, out = self.run_in_terminal(["run", "tty"], keys=[(b"ready", b"hello\r")])
        self.assertEqual(rc, 0, out)
        self.assertIn("stdout:tty", out)
        self.assertIn("30 100", out)
        self.assertIn("got:hello", out)

        log = (self.shortcuts_home / "logs" / "tty.log").read_bytes()
        self.assertIn(b"stdout:tty", log)
        self.assertIn(b"got:hello", log)
        self.assertNotIn(b"\r\n", log)
        self.assertTrue(log.rstrip().endswith(b"=== exit: 0 ==="), log)

        res = self.run_cli(["run", "tty"], input="x\n")
        self.assertIn("stdout:pipe", res.stdout)
        self.assertIn("got:x", res.stdout)

    def test_ctrl_c_on_terminal_returns_130(self):
        self.assertEqual(self.run_cli(["new", "sleep", "--", "echo started; sleep 10"]).returncode, 0)
        start = time.time()
        rc, out = self.run_in_terminal(["run", "sleep"], keys=[(b"started", b"\x03")])
        self.assertEqual(rc, 130, out)
        self.assertLess(time.time() - start, 8)
        log = (self.shortcuts_home / "logs" / "sleep.log").read_text()
        self.assertTrue(log.rstrip().endswith("=== exit: 130 ==="), log)

    def test_sigterm_on_terminal_restores_terminal(self):
        self.assertEqual(self.run_cli(["new", "sleep", "--", "echo started; sleep 10"]).returncode, 0)
        rc, out = self.run_in_terminal(["run", "sleep"], keys=[(b"started", signal.SIGTERM)])
        self.assertEqual(rc, 143, out)
        self.assertTrue(self.terminal_lflag & termios.ICANON, "terminal left in raw mode")
        self.assertTrue(self.terminal_lflag & termios.ISIG, "terminal left in raw mode")
        log = (self.shortcuts_home / "logs" / "sleep.log").read_text()
        self.assertTrue(log.rstrip().endswith("=== exit: 143 ==="), log)

    def test_broken_output_pipe_exits_141_and_logs(self):
        self.assertEqual(self.run_cli(["new", "seq", "--", "seq 1 1000000"]).returncode, 0)
        p_shortcut = self.popen_cli(["run", "seq"], text=True)
        p_head = self.popen(["head", "-n", "1"], stdin=p_shortcut.stdout, text=True)
        assert p_shortcut.stdout is not None
        p_shortcut.stdout.close()
        head_out, _ = p_head.communicate(timeout=5)
        _, sc_err = p_shortcut.communicate(timeout=5)
        self.assertEqual(head_out.strip(), "1")
        self.assertEqual(p_shortcut.returncode, 141)
        self.assertNotIn("BrokenPipeError", sc_err)
        self.assertNotIn("Exception ignored", sc_err)
        log_path = self.shortcuts_home / "logs" / "seq.log"
        self.assertTrue(log_path.is_file())
        log_content = log_path.read_text(encoding="utf-8")
        self.assertIn("=== exit: 141 ===", log_content)

    def test_signals_reach_child_group_in_pipe_mode(self):
        pid_file = self.tmp_path / "child.pid"
        inline = f"sh -c 'echo $$ > {pid_file}; exec sleep 300'; echo done"
        self.assertEqual(self.run_cli(["new", "group_sig", "--", inline]).returncode, 0)
        proc = self.popen_cli(["run", "group_sig"], bufsize=0, start_new_session=True)
        start = time.time()
        child_pid = None
        while time.time() - start < 5:
            if pid_file.exists():
                text = pid_file.read_text().strip()
                if text.isdigit():
                    child_pid = int(text)
                    break
            time.sleep(0.05)
        self.assertIsNotNone(child_pid, "PIDFILE was not written in time")
        assert child_pid is not None
        def is_alive(pid: int) -> bool:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return False
            try:
                status_file = pathlib.Path(f"/proc/{pid}/status")
                if status_file.exists():
                    for line in status_file.read_text().splitlines():
                        if line.startswith("State:"):
                            return "Z" not in line
            except OSError:
                return False
            return True

        self.assertTrue(is_alive(child_pid), f"child process {child_pid} not running before SIGTERM")

        proc.send_signal(signal.SIGTERM)
        proc.communicate(timeout=5)

        deadline = time.time() + 2.0
        alive = True
        while time.time() < deadline:
            if not is_alive(child_pid):
                alive = False
                break
            time.sleep(0.05)
        self.assertFalse(alive, f"child pid {child_pid} still running after SIGTERM")


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

        # No `args` key: no checking
        self.assertEqual(self.run_cli(["r", "free", "1", "2", "3"]).stdout, "3\n")
        self.assertIn("Usage:       shortcut r kill <container> [signal] [extra...]", self.run_cli(["show", "kill"]).stdout)

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

    def test_invalid_args_declaration_is_an_error(self):
        self.write_store(self.shortcuts_home, {
            "bad": {"inline": "true", "args": [{"name": "a", "rest": True}, {"name": "b"}]},
        })
        res = self.run_cli(["r", "bad"])
        self.assertEqual(res.returncode, 1)
        self.assertIn("only the last entry in 'args' can be 'rest'", res.stderr)

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
