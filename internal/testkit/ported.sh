#!/bin/sh
#
# Port-completeness check. Lists every `file::test_name` under tests/test_*.py, every `// py: file::test_name`
# comment in *_test.go, and every line of dropped.txt, then prints
#     ported N, dropped D, missing M of T
# and the missing names. Exists only until the Python tests are deleted (C-1).
#   (no flag)  report only, exit 0
#   --strict   exit 1 if any test is missing, claimed twice, named but not in tests/, or both ported and dropped
#   --final    --strict, and a `deferred` line in dropped.txt is refused too (the gate from M5 on)
# POSIX sh and awk only.
mode=report
case "$1" in
    "") ;;
    --strict) mode=strict ;;
    --final) mode=final ;;
    *) echo "usage: sh ported.sh [--strict | --final]" >&2; exit 2 ;;
esac

here=$(cd "$(dirname "$0")" && pwd) || exit 2
root=$(cd "$here/../.." && pwd) || exit 2
list=$(mktemp "${TMPDIR:-/tmp}/ported.XXXXXX") || exit 2
trap 'rm -f "$list"' EXIT HUP INT TERM

# P name: a Python test. G name: one claim in a Go test. D name rest: a dropped.txt line.
awk '
    /^[ \t]*def[ \t]+test_[A-Za-z0-9_]*[ \t]*\(/ {
        name = $0
        sub(/^[ \t]*def[ \t]+/, "", name)
        sub(/[ \t]*\(.*/, "", name)
        n = split(FILENAME, parts, "/")
        print "P", parts[n] "::" name
    }
' "$root"/tests/test_*.py >"$list"

find "$root" \( -name .git -o -name .lad \) -prune -o -name '*_test.go' -print | sort | while IFS= read -r f; do
    awk '
        /^[ \t]*\/\/ py: / {
            line = $0
            sub(/^[ \t]*\/\/ py:[ \t]*/, "", line)
            split(line, w, /[ \t]+/)
            if (w[1] ~ /::/) print "G", w[1]
        }
    ' "$f"
done >>"$list"

awk '
    /^[ \t]*(#|$)/ { next }
    { name = $1; rest = $0; sub(/^[^ \t]+[ \t]*/, "", rest); print "D", name, rest }
' "$here/dropped.txt" >>"$list"

awk -v mode="$mode" '
    $1 == "P" { if (!($2 in py)) { py[$2] = 1; order[++total] = $2 } }
    $1 == "G" { claims[$2]++ }
    $1 == "D" { drop[$2] = 1; rest = $0; sub(/^D [^ ]+ ?/, "", rest); if (rest ~ /^deferred R-4:/) deferred[$2] = 1 }
    END {
        for (i = 1; i <= total; i++) {
            n = order[i]
            if (n in claims && n in drop) both[++nb] = n
            else if (n in claims) ported++
            else if (n in drop) dropped++
            else miss[++missing] = n
            if (claims[n] > 1) twice[++nt] = n
        }
        for (n in claims) if (!(n in py)) unknown[++nu] = n
        for (n in drop) if (!(n in py)) unknown[++nu] = n
        for (n in deferred) nd++
        printf "ported %d, dropped %d, missing %d of %d\n", ported, dropped, missing, total
        for (i = 1; i <= missing; i++) print "missing: " miss[i]
        for (i = 1; i <= nt; i++) print "claimed twice: " twice[i]
        for (i = 1; i <= nb; i++) print "ported and dropped: " both[i]
        for (i = 1; i <= nu; i++) print "not in tests/: " unknown[i]
        for (n in deferred) print "deferred: " n
        bad = (missing + nt + nb + nu > 0)
        if (mode == "final" && nd > 0) bad = 1
        if (mode == "report") bad = 0
        exit bad
    }
' "$list"
