#!/bin/sh
# Tests the plugin's hooks. confirm-guard.sh: tool calls it should send to the user, or leave
# alone. project-shortcuts.sh: what it prints in and out of a project, against the shortcut on
# PATH, in a temp HOME of its own.
# Run: sh tests/hooks-test.sh
cd "$(dirname "$0")/.." || exit 1
hooks=$(pwd)/hooks
fails=0
fail() { echo "FAIL: $*"; fails=$((fails + 1)); }

# json STRING: STRING as a JSON string, with \ " and newlines escaped.
json() {
	printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | awk 'NR > 1 { printf "\\n" } { printf "%s", $0 }')"
}

## confirm-guard.sh

bash() { # bash ask|none COMMAND
	payload=$(printf '{"session_id":"t","cwd":"/home/u/p","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%s,"description":"Run it"}}' "$(json "$2")")
	out=$(printf '%s' "$payload" | sh "$hooks/confirm-guard.sh")
	case $out in *'"permissionDecision":"ask"'*) got=ask ;; '') got=none ;; *) got="bad output: $out" ;; esac
	[ "$got" = "$1" ] || fail "want $1, got $got: $2"
}

bash ask 'shortcut r -y deploy'
bash ask 'shortcut r --yes deploy prod'
bash ask 'shortcut run -y . deploy'
bash ask 'shortcut r -y git push'
bash ask 'cd app && shortcut r -y . deploy'
bash ask '~/.local/bin/shortcut r -y deploy'
bash ask 'echo go
shortcut r -y deploy'
bash ask 'shortcut rm -y hi'
bash ask "shortcut r '-y' deploy"
bash ask 'shortcut r "-y" deploy'
bash ask 'shortcut rm "-y" x'
bash ask "'shortcut' r -y deploy"
bash ask 'shortcut rm hi -y'
bash ask 'shortcut rm --local -y mine'
bash ask 'shortcut rm --namespace git --yes'
bash ask 'shortcut rm -y temp'
bash ask 'shortcut rm -y temp a b'
bash ask 'shortcut rm --local -y temp a'
bash ask 'shortcut rm -y git a'
bash ask 'shortcut rm --namespace temp -y x'
bash ask 'shortcut rm --namespace=tmp -y'
bash ask 'shortcut rm -y temp a; shortcut rm -y hi'
bash ask 'shortcut rm -y temp a > out hi'
bash ask "script -qc 'shortcut r . deploy' /dev/null"
bash ask 'yes | unbuffer shortcut rm hi'
bash ask 'expect -c "spawn shortcut r deploy; send y\r"'
bash ask "python3 -c 'import pty; pty.spawn([\"shortcut\", \"rm\", \"hi\"])'"

bash none 'X=shortcut; $X r -y d' # known gap: a variable gets past the guard
bash none 'shortcut list'
bash none 'shortcut list --names .'
bash none 'shortcut show . test'
bash none 'shortcut show --field run_cwd . serve'
bash none 'shortcut r . test -v'
bash none 'shortcut r . deploy -y'
bash none 'shortcut r tool -- -y'
bash none 'shortcut rm hi'
bash none 'shortcut rm -y temp scratch'
bash none 'shortcut rm temp scratch --yes 2>&1'
bash none 'shortcut rm -y temp scratch >/dev/null'
bash none 'shortcut rm --namespace temp -y'
bash none 'shortcut rm -y --namespace=temp && shortcut list'
bash none 'shortcut new deploy --confirm --desc "Deploy" -- '"'"'./deploy.sh -y "$@"'"'"
bash none 'shortcut new t --script ./scripts/t.sh --desc "T"'
bash none 'apt-get install -y jq'
bash none 'rm -f /tmp/x -y'
bash none 'cd ~/scripts/shortcut && go test ./...'

## project-shortcuts.sh

command -v shortcut >/dev/null 2>&1 || { echo "shortcut isn't on PATH"; exit 1; }
# The real path: shortcut prints it, and on macOS a temp dir is under /var, a link to /private/var.
tmp=$(cd "$(mktemp -d)" && pwd -P) || exit 1
HOME=$tmp/home
export HOME
SHORTCUTS_HOME=$HOME/.shortcuts
export SHORTCUTS_HOME
mkdir -p "$HOME" "$tmp/proj/sub" "$tmp/plain" "$tmp/broken" "$tmp/big"

session() { # session DIR: the hook's output for a session started in DIR
	printf '{"session_id":"t","cwd":%s,"hook_event_name":"SessionStart","source":"startup"}' "$(json "$1")" |
		(cd / && sh "$hooks/project-shortcuts.sh")
}
contains() { case $1 in *"$2"*) ;; *) fail "output lacks '$2': $1" ;; esac; }

cat >"$tmp/proj/.shortcuts" <<'JSON'
{"version": 1, "shortcuts": {
  "test": {"description": "Run the tests", "inline": "go test ./..."},
  "bare": {"inline": "true"},
  "odd": {"description": "line one\nline two\u001b[31m red", "inline": "true"}
}}
JSON
(cd "$tmp/proj" && shortcut new mine --local --desc "My run" -- 'echo mine') >/dev/null 2>&1
(cd "$tmp" && shortcut new hi --desc "Global one" -- 'echo hi') >/dev/null 2>&1

out=$(session "$tmp/proj")
contains "$out" "Project shortcuts in $tmp/proj"
contains "$out" "- test: Run the tests"
contains "$out" "- mine: My run"
contains "$out" "- odd: line one line two [31m red"
printf '%s\n' "$out" | grep -qx -- '- bare' || fail "bare has no line of its own: $out"
case $out in *"Global one"* | *"- hi"*) fail "a global shortcut is listed: $out" ;; esac
[ "$(session "$tmp/proj/sub")" = "$out" ] || fail "a subfolder lists something else"
[ "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" -eq 5 ] || fail "want 5 lines: $out"

[ -z "$(session "$tmp/plain")" ] || fail "prints outside a project"

echo '{"version": 1, "shortcuts": {' >"$tmp/broken/.shortcuts"
contains "$(session "$tmp/broken")" "warns about this project's shortcut files"
printf '%s\n' "$out" | grep -q warns && fail "warns about a good project: $out"

i=0
{
	printf '{"version": 1, "shortcuts": {'
	while [ $i -lt 45 ]; do
		[ $i -gt 0 ] && printf ','
		printf '"s%02d": {"description": "Number %d", "inline": "true"}' $i $i
		i=$((i + 1))
	done
	printf '}}\n'
} >"$tmp/big/.shortcuts"
big=$(session "$tmp/big")
contains "$big" "- s39: Number 39"
contains "$big" "and 5 more"
case $big in *"- s40"*) fail "lists more than 40" ;; esac

if ! PATH=/usr/bin:/bin command -v shortcut >/dev/null 2>&1; then
	[ -z "$(PATH=/usr/bin:/bin session "$tmp/proj")" ] || fail "prints without shortcut on PATH"
fi

if [ "$fails" -eq 0 ]; then echo "all passed"; else echo "$fails failed"; exit 1; fi
