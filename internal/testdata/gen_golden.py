#!/usr/bin/env python3
# FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-3). Don't hand-edit without checking the plan.
"""One-off golden-data generator for the Go port (plan section 3.5). Stdlib only; runs src/shortcut read-only.

    python3 -I internal/testdata/gen_golden.py internal/testdata

Writes OUT/approval.json, OUT/diff_cases.json, OUT/text/*.json, OUT/../cli/help/*.txt and
OUT/../cli/completion/*.txt (OUT is internal/testdata). Everything runs in a throwaway HOME under /tmp/claude-1000/.
Removed with src/ at cutover; its outputs stay as frozen data.
"""
import contextlib
import hashlib
import io
import json
import os
import platform
import random
import re
import shutil
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SANDBOX_ROOT = "/tmp/claude-1000/"

if len(sys.argv) != 2:
    sys.exit("usage: gen_golden.py OUTDIR   (OUTDIR is internal/testdata)")
OUT = os.path.abspath(sys.argv[1])

# Sandbox first (plan section 13): HOME, then SHORTCUTS_HOME as a separate statement, both inside our own temp area.
_inherited = os.environ.get("HOME", "")
if not _inherited.startswith(SANDBOX_ROOT):
    sys.exit("abort: run through the sandbox runner; HOME must already be under " + SANDBOX_ROOT)
AREA = os.path.realpath(os.path.join(os.path.dirname(_inherited), "gen_golden_tmp"))
if not AREA.startswith(SANDBOX_ROOT):
    sys.exit("abort: temp area outside " + SANDBOX_ROOT)
shutil.rmtree(AREA, ignore_errors=True)
for _d in ("home", "cwd", "trees"):
    os.makedirs(os.path.join(AREA, _d))
os.environ["HOME"] = os.path.join(AREA, "home")
os.environ["SHORTCUTS_HOME"] = os.path.join(os.environ["HOME"], ".shortcuts")
if not (os.environ["HOME"].startswith(SANDBOX_ROOT) and os.environ["SHORTCUTS_HOME"].startswith(SANDBOX_ROOT)):
    sys.exit("abort: HOME or SHORTCUTS_HOME is outside " + SANDBOX_ROOT)
os.environ["COLUMNS"] = "80"  # argparse wraps to COLUMNS - 2
os.environ.pop("LINES", None)
os.chdir(os.path.join(AREA, "cwd"))
if sys.getfilesystemencoding().lower() != "utf-8":
    sys.exit("abort: filesystem encoding is not utf-8")

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.join(REPO, "src"))
from shortcut import cli, completion, helpers  # noqa: E402
from shortcut.group import Group  # noqa: E402
from shortcut.parser import Parser  # noqa: E402
from shortcut.stores import load_store  # noqa: E402

HEADER = {
    "python": platform.python_version(),
    "git_sha": subprocess.run(["git", "-C", REPO, "rev-parse", "HEAD"], capture_output=True, text=True, check=True).stdout.strip(),
    "generator": "internal/testdata/gen_golden.py",
}


def write_json(path: str, body: dict) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="ascii", newline="\n") as f:  # ensure_ascii: lone surrogates stay escapes
        json.dump({"header": HEADER, **body}, f, indent=2)
        f.write("\n")


def fs(raw: bytes) -> str:
    return os.fsdecode(raw)  # surrogateescape, as Python decodes paths


# ---------------------------------------------------------------------------------------------- approval.json

def files_json(files: dict) -> str:
    return json.dumps(sorted(files.items()))


def hash_case(name: str, files: dict) -> dict:
    return {"name": name, "files": files, "json": files_json(files), "hash": helpers.files_hash(files)}


PLAN_FILES = {
    "b.sh": "echo \u00e9 \U0001F600\n",
    ".shortcuts": json.dumps({"shortcuts": {"x": {"inline": "echo \u00e9", "n": 1.0, "big": 1e30}}, "version": 2},
                             indent=2, sort_keys=True) + "\n",
    "./cwd of x": "/tmp/x\n",
}
PLAN_HASH = "1a4f1b218e2fc8c36510309de93f45583a881826c19fbf2b451df5b99e1d6e8d"
PLAN_KEY_HASH = "05dec41a612a8346907a7968079cac5ce6531019beb8f1bb7a91f061501a9a16"

BAD = fs(b"caf\xe9.sh")
hash_cases = [
    hash_case("plan sample", PLAN_FILES),
    hash_case("empty map", {}),
    hash_case("one file", {"a.sh": "echo hi\n"}),
    hash_case("empty value", {"a.sh": ""}),
    hash_case("non-ASCII and astral text", {"a.sh": "caf\u00e9 \u4e2d\u6587 \U0001F600 \U00010000 \u2028 \x7f \x00\n"}),
    hash_case("non-ASCII key", {"caf\u00e9.sh": "x\n", "\U0001F600.sh": "y\n", "z.sh": "z\n"}),
    hash_case("lone surrogates in value", {"a.sh": "\ud800 \udc00 \udfff \ud83d\n", "b.sh": "\ud83d\ude00 (pair as two escapes)\n"}),
    hash_case("lone surrogate key", {"\ud800.sh": "x\n", "a.sh": "y\n"}),
    hash_case("non-UTF-8 file name b'caf\\xe9.sh'", {BAD: "echo x\n"}),
    hash_case("non-UTF-8 name under scripts/ns", {"scripts/ns/" + BAD: "echo x\n", "scripts/ns/a.sh": "echo a\n"}),
    hash_case("sort order: b'\\xff.sh' vs U+10000", {fs(b"\xff.sh"): "a\n", "\U00010000.sh": "b\n"}),
    hash_case("sort order: U+FFFF, U+E000, U+10000, U+DCFF", {"\U00010000.sh": "1\n", "\uffff.sh": "2\n", "\ue000.sh": "3\n", fs(b"\xff.sh"): "4\n"}),
    hash_case("sort order: case and punctuation", {"B.sh": "1\n", "a.sh": "2\n", "./cwd of a": "3\n", ".shortcuts": "4\n", ".shortcuts.local": "5\n", "scripts/a.sh": "6\n", "a b": "7\n", "a-b": "8\n", "a_b": "9\n"}),
    hash_case("sort order: prefix", {"a": "1\n", "a.sh": "2\n", "": "3\n"}),
    hash_case("control characters in key", {"a\tb": "1\n", "a\x7fb": "2\n", "a\x1fb": "3\n", 'a"b\\c': "4\n"}),
    hash_case("two keys, same value", {"a": "same\n", "b": "same\n"}),
    hash_case("binary and invalid marks", {"a.bin": helpers.reviewable(b"\x7fELF\x02\x01"), "b.sh": helpers.reviewable(b"a \xff\n")}),
    hash_case("long value", {"big.sh": "x" * 5000 + "\n" + "\u00e9" * 300 + "\n"}),
]

reviewable_raws = [
    b"", b"\n", b"echo hi\n", "echo \u00e9 \U0001F600\n".encode(), b"a \xff\n", b"\xff", b"\x80\x81", b"\x7fELF", b"\x7fELF\x02\x01\x01\x00",
    b" \x7fELF", b"\x00binary:abc", b"\x00binary:", b"\x00binary", b"\x00", b"a\x00b\n", b"\xef\xbb\xbfecho\n", b"a\r\nb\r\n",
    "\ufffd".encode(), "a\ufffdb\n".encode(), b"\xc0\x80", b"\xc1\xbf", b"\xe0\x80\x80", b"\xed\xa0\x80", b"\xed\xbf\xbf",
    b"\xe2\x82", b"\xe2\x82\xac", b"\xe2\x82\xe2\x82\xac", b"\xf0\x9f\x98", b"\xf0\x9f\x98\x80", b"\xf4\x90\x80\x80", b"\xf5\x80\x80\x80",
    b"\xf8\x88\x80\x80\x80", b"\xfe\xff", b"ok \xe9 ok \xe9\n", b"\x00not UTF-8, sha256 abc\n", b"x\x00not UTF-8, sha256 \n",
    b"a \xff\n\x00not UTF-8, sha256 deadbeef\n", b"\x1b[31mred\x1b[0m\n", "\u2028\u2029\x85\n".encode(), b"\x7f", b"#!/bin/sh\n\xff\xfe\n",
]
reviewable_cases = [{"raw_hex": raw.hex(), "expected": helpers.reviewable(raw)} for raw in reviewable_raws]

dumps_sources = [
    '{"a":[1,2.5,1e22,true,null],"k\\u00e9":"\\ud83d\\ude00"}',
    '{"version":2,"shortcuts":{"x":{"inline":"echo \\u00e9","n":1.0,"big":1e30}}}',
    '{"b":1,"a":2,"B":3,"\\u00e9":4,"\\ud83d\\ude00":5,"\\uffff":6,"\\ue000":7}',
    "{}", "[]", '{"a":{},"b":[],"c":[[],{}],"d":[{"e":[]}]}',
    '{"a": NaN, "b": Infinity, "c": -Infinity, "d": 1e400, "e": -1e400, "f": 1.0}',
    '{"a": 1, "a": 2}', '{"a": 1, "b": 2, "a": 3}',
    '{"n": [0, -0, 0.0, -0.0, 1e2, 1E2, 1e+2, 12345678901234567890123, -12345678901234567890123, 100000000000000000000000000000000000]}',
    '{"s": "\\ud800", "t": "\\udc00\\ud800", "u": "\\ud83d", "v": "\\u0000\\u007f\\u0080\\u2028\\u00a0"}',
    '{"s": "tab\\there \\"q\\" back\\\\ slash \\/ \\b\\f\\n\\r"}',
    '{"s": "\u00e9 \U0001F600 \U00010000 \u4e2d"}',
    '{"k\u00e9": 1, "\U0001F600": 2}',
    '{"version": 2, "description": "d", "shortcuts": {"a": {"script": "scripts/a.sh", "cwd": "caller", "args": [{"name": "x", "default": "1"}]}}}',
    ' \t\r\n{ "a" :\t[ 1 ,\n2 ] } \n', '{"a": 1.5e-7, "b": 123456789.123456789, "c": 0.1, "d": 5e-324}',
    '{"a": true, "b": false, "c": null}', '"str"', "42", "null", "[1, [2, [3]]]",
]
dumps_cases = []
for src in dumps_sources:
    data = json.loads(src)
    case = {"source": src, "compact": json.dumps(data), "indent2": json.dumps(data, indent=2) + "\n"}
    if isinstance(data, dict):
        case["indent2_sorted"] = json.dumps(data, indent=2, sort_keys=True) + "\n"
    dumps_cases.append(case)

float_literals = [
    "1.0", "1e22", "1e16", "1e15", "123456789012345678", "0.0001", "0.00001", "-0.0", "5e-324", "1.7976931348623157e308", "1e400",
    "0.0", "0", "-0", "1", "-1", "1e0", "1E2", "1e+2", "100.0", "0.1", "0.5", "1e-7", "1.5e300", "2.5e-5", "123456789.123", "1e21",
    "9999999999999998.0", "9999999999999999.0", "1.0000000000000002", "-1e-05", "0.30000000000000004", "12345678901234567890123",
    "-12345678901234567890123", "1e-4", "1.5e-4", "0.00012345", "123456789012345.6", "1234567890123456.7", "12345678901234567.0",
    "4.35", "2.675", "1e23", "8.41e21", "5e-5", "-5e-5", "1.7976931348623158e308", "2.2250738585072014e-308", "-1e400",
]
float_cases = []
for lit in float_literals:
    value = json.loads(lit)
    float_cases.append({"literal": lit, "dumps": json.dumps(value), "type": type(value).__name__})

loads_sources = [
    "{not json", '{"a": 1,}', "", " ", "[", '{"a"', '{"a":', '{"a": 1', "[1,]", "[1 2]", '{"a" 1}', "nul", "tru", "NaN", "-", "01", "1.", ".5",
    "+1", "'a'", '"a', '"\\x"', '"\\u12"', '"\\ud800\\u0041"', '"a\nb"', '"\t"', "\ufeff{}", "{} x", "{}{}", "[1]]", '{"a": NaN}',
    '{"a": Infinity}', '{"a": -Infinity}', '{"a": -}', "\u00a0{}", '{"a": 1e}', '{1: 2}', "{,}", '{"a": 1,, "b": 2}', "[,1]", "[\n1,\n2\n,]",
    '{\n  "a": 1,\n  "b": [1, 2,\n}', '"\\ud800"', '"\\ud83d\\ude00"', '{"\\ud800": 1}', "-0", "-0.0", "1e5", "1E-2", "00", "-01", "0x10",
]
loads_cases = []
for src in loads_sources:
    try:
        loads_cases.append({"source": src, "compact": json.dumps(json.loads(src))})
    except ValueError as e:
        loads_cases.append({"source": src, "error": str(e)})
# raw bytes the way load_registry_file reads them: utf-8 decode first
loads_bytes_cases = []
for raw in (b'{"a": "\xff"}', b"\xef\xbb\xbf{}", b'{"a": "\xc3\xa9"}', b"\xff", b'{"a": "\xed\xa0\x80"}'):
    try:
        loads_bytes_cases.append({"raw_hex": raw.hex(), "compact": json.dumps(json.loads(raw.decode("utf-8")))})
    except ValueError as e:  # UnicodeDecodeError or JSONDecodeError, both ValueError
        loads_bytes_cases.append({"raw_hex": raw.hex(), "error": f"{type(e).__name__}: {e}"})

# Group keys and record names from literal (store, name, file) triples.
key_cases = []
for store, name, file in [
    ("/tmp/s", "ns", "/tmp/s/imports/ns.shortcut"),
    ("/tmp/s", "docker", "/tmp/s/imports/docker.shortcut"),
    ("/tmp/caf\u00e9", "ns", "/tmp/caf\u00e9/imports/ns.shortcut"),
    ("/tmp/\U0001F600", "ns", "/tmp/\U0001F600/x.shortcut"),
    (fs(b"/tmp/caf\xe9"), "ns", fs(b"/tmp/caf\xe9/imports/ns.shortcut")),
    ("/tmp/s", "ns", fs(b"/tmp/s/imports/caf\xe9.shortcut")),
    ('/tmp/q"uote\\', "ns", "/tmp/s/a b.shortcut"),
]:
    key = json.dumps([store, name, file])
    key_cases.append({"kind": "namespace", "store": store, "name": name, "file": file, "key": key,
                      "record_name": hashlib.sha256(key.encode()).hexdigest() + ".json"})
for store in ["/tmp/s", "/tmp/caf\u00e9", "/tmp/\U0001F600", "/tmp/a b/c", fs(b"/tmp/caf\xe9")]:
    case = {"kind": "project", "store": store, "key": store}
    try:
        case["record_name"] = hashlib.sha256(store.encode()).hexdigest() + ".json"
    except UnicodeEncodeError as e:  # a Python bug, not a contract: approval_path raises for a non-UTF-8 project path
        case["python_error"] = f"{type(e).__name__}: {e}"
    key_cases.append(case)
assert key_cases[0]["record_name"] == PLAN_KEY_HASH + ".json", "plan key vector differs"

# Literal-map cases for files holding absolute paths (a relative cwd: "./cwd of x" -> realpath + "\n").
literal_abs_cases = [
    hash_case("cwd entry (absolute value, literal map only)", {".shortcuts": "{}\n", "./cwd of x": "/tmp/x\n"}),
    hash_case("cwd entry in .shortcuts.local", {"./cwd of x in .shortcuts.local": "/tmp/x\n"}),
    hash_case("absolute script key (display_path starts with ..)", {"/tmp/outside/tool.sh": "echo\n", ".shortcuts": "{}\n"}),
]

# ---- real trees: Python builds the maps, hash and record JSON; paths are templated with @ROOT@.
TREE_ROOT_RE = re.compile(r"^[A-Za-z0-9/_.-]+$")


def json_text(data: dict) -> str:
    return json.dumps(data, indent=2, sort_keys=True) + "\n"


def raw_of(e: dict) -> bytes:
    return e["content"] if isinstance(e["content"], bytes) else e["content"].encode("utf-8")


def build_tree(root: str, entries: list) -> None:
    os.makedirs(root)
    for e in entries:
        path = os.path.join(root, e["path"])
        kind = e.get("type", "file")
        if kind == "dir":
            os.makedirs(path, exist_ok=True)
            continue
        os.makedirs(os.path.dirname(path), exist_ok=True)
        if kind == "symlink":
            os.symlink(e["target"], path)
            continue
        with open(path, "wb") as f:
            f.write(raw_of(e))
        os.chmod(path, e.get("mode", 0o644))


def tree_json(entries: list) -> list:
    out = []
    for e in entries:
        item = {"path": e["path"], "type": e.get("type", "file")}
        if item["type"] == "symlink":
            item["target"] = e["target"]
        elif item["type"] == "file":
            raw = raw_of(e)
            try:
                item["content"] = raw.decode("utf-8")
            except UnicodeDecodeError:
                item["content_hex"] = raw.hex()
            item["mode"] = oct(e.get("mode", 0o644))
        out.append(item)
    return out


def group_json(group: Group, root: str) -> dict:
    shared, local = helpers.shared_files(group), helpers.local_files(group)
    helpers.save_approval(group, shared, local)
    with open(helpers.approval_path(group), "rb") as f:
        record_text = f.read().decode("utf-8")
    return {
        "name": group.name,
        "error": group.error,
        "shared_files": shared,
        "local_files": local,
        "shared_json": files_json(shared),
        "shared_hash": helpers.files_hash(shared),
        "local_json": files_json(local),
        "local_hash": helpers.files_hash(local),
        "key_template": group.key.replace(root, "@ROOT@"),
        "record_template": record_text.replace(root, "@ROOT@"),
    }


def proj(shortcuts: dict) -> str:
    return json_text({"version": 2, "shortcuts": shortcuts})


def with_imports(shortcuts: dict, imports: dict) -> str:
    return json_text({"version": 2, "shortcuts": shortcuts, "imports": imports})


ELF = b"\x7fELF\x02\x01\x01\x00" + bytes(range(8))
tree_specs = [
    ("project with a script and a local overlay", [
        {"path": ".shortcuts", "content": proj({"hi": {"script": "scripts/hi.sh"}, "inline": {"inline": "echo \u00e9"}})},
        {"path": "scripts/hi.sh", "content": "#!/usr/bin/env bash\necho hi\n", "mode": 0o755},
        {"path": ".shortcuts.local", "content": proj({"mine": {"inline": "echo mine"}, "hi": None})},
    ]),
    ("project without .shortcuts, only .shortcuts.local", [
        {"path": ".shortcuts.local", "content": proj({"mine": {"inline": "echo mine"}})},
    ]),
    ("project, local with its own script", [
        {"path": ".shortcuts", "content": proj({"a": {"inline": "true"}})},
        {"path": ".shortcuts.local", "content": proj({"b": {"script": "tools/b.sh"}})},
        {"path": "tools/b.sh", "content": "echo b\n", "mode": 0o700},
    ]),
    ("project scripts: binary, invalid UTF-8, empty, CRLF, non-ASCII", [
        {"path": ".shortcuts", "content": proj({
            "elf": {"script": "bin/prog"}, "bad": {"script": "bad.sh"}, "empty": {"script": "empty.sh"},
            "crlf": {"script": "crlf.sh"}, "uni": {"script": "caf\u00e9.sh"}, "marked": {"script": "marked.sh"}})},
        {"path": "bin/prog", "content": ELF, "mode": 0o755},
        {"path": "bad.sh", "content": b"echo \xff\xfe\n"},
        {"path": "empty.sh", "content": b""},
        {"path": "crlf.sh", "content": b"echo a\r\necho b\r\n"},
        {"path": "caf\u00e9.sh", "content": "echo \u00e9\n"},
        {"path": "marked.sh", "content": b"\x00binary:abc"},
    ]),
    ("project data as loaded: floats, big ints, NaN, duplicates, surrogates", [
        {"path": ".shortcuts", "content": (
            '{"version": 2, "extra": {"n": 1.0, "big": 1e30, "huge": 1e400, "nan": NaN, "neg": -Infinity, '
            '"int": 12345678901234567890123, "z": -0, "zf": -0.0, "e": 1E2, "s": "\\ud800 \\u00e9 \\ud83d\\ude00"}, '
            '"shortcuts": {"x": {"inline": "echo", "inline": "echo dup"}, "y": {"inline": "\\ud800"}}}')},
    ]),
    ("project script reached through a symlink", [
        {"path": ".shortcuts", "content": proj({"l": {"script": "scripts/link.sh"}})},
        {"path": "scripts/real.sh", "content": "echo real\n", "mode": 0o755},
        {"path": "scripts/link.sh", "type": "symlink", "target": "real.sh"},
    ]),
    ("project script missing or not a file is skipped", [
        {"path": ".shortcuts", "content": proj({"gone": {"script": "scripts/gone.sh"}, "dir": {"script": "scripts"}, "ok": {"script": "scripts/ok.sh"}})},
        {"path": "scripts/ok.sh", "content": "echo ok\n"},
    ]),
    ("project with a namespace: scripts folder, extra file, symlinks", [
        {"path": ".shortcuts", "content": with_imports({"p": {"inline": "echo p"}}, {"ns": "ns.shortcut"})},
        {"path": "ns.shortcut", "content": json_text({"version": 2, "description": "ns", "shortcuts": {
            "go": {"script": "scripts/ns/go.sh"}, "viaLink": {"script": "scripts/ns/link.sh"}}})},
        {"path": "scripts/ns/go.sh", "content": "#!/bin/sh\n. ./lib.sh\n", "mode": 0o755},
        {"path": "scripts/ns/lib.sh", "content": "echo lib\n", "mode": 0o644},
        {"path": "scripts/ns/sub/deep.sh", "content": "echo deep \u00e9\n"},
        {"path": "scripts/ns/link.sh", "type": "symlink", "target": "lib.sh"},
        {"path": "scripts/ns/dirlink", "type": "symlink", "target": "sub"},
        {"path": "scripts/ns/blob", "content": ELF, "mode": 0o755},
        {"path": "scripts/ns/bad.txt", "content": b"x \xe9\n"},
        {"path": "scripts/other/not-in-ns.sh", "content": "echo no\n"},
    ]),
    ("namespace with non-UTF-8 and astral file names", [
        {"path": ".shortcuts", "content": with_imports({}, {"ns": "ns.shortcut"})},
        {"path": "ns.shortcut", "content": json_text({"version": 2, "shortcuts": {"go": {"script": "scripts/ns/" + BAD}}})},
        {"path": "scripts/ns/" + BAD, "content": "echo x\n", "mode": 0o755},
        {"path": "scripts/ns/a.sh", "content": "echo a\n"},
        {"path": "scripts/ns/" + fs(b"\xff.sh"), "content": "echo ff\n"},
        {"path": "scripts/ns/\U00010000.sh", "content": "echo astral\n"},
    ]),
    ("namespace with a dangling symlink and an empty folder", [
        {"path": ".shortcuts", "content": with_imports({}, {"ns": "ns.shortcut"})},
        {"path": "ns.shortcut", "content": json_text({"version": 2, "shortcuts": {"a": {"inline": "true"}}})},
        {"path": "scripts/ns/dangling", "type": "symlink", "target": "nowhere.sh"},
        {"path": "scripts/ns/empty", "type": "dir"},
        {"path": "scripts/ns/ok.sh", "content": "echo ok\n"},
    ]),
]
tree_cases = []
for index, (title, entries) in enumerate(tree_specs):
    root = os.path.join(AREA, "trees", f"t{index:02d}")
    assert TREE_ROOT_RE.match(root), root
    build_tree(root, entries)
    store = load_store(root)
    groups = [group_json(g, root) for g in store.groups]
    tree_cases.append({"name": title, "tree": tree_json(entries), "problems": store.problems, "groups": groups})

write_json(os.path.join(OUT, "approval.json"), {
    "notes": [
        "Strings may hold lone surrogates (\\udXXX escapes): decode with pyjson, not encoding/json. A non-UTF-8 path byte 0xNN is U+DC00+NN.",
        "tree_cases: build tree[] under a temp root, load each group, compare shared_files/local_files/hashes; replace @ROOT@ with the root in key_template and record_template. Modes are octal strings; symlink targets are relative. content_hex replaces content when the bytes are not UTF-8.",
        "plan_sample_hash_matches_plan: whether hash_cases[0].hash equals the hash written in plan section 4.1.",
        "key_cases[].python_error: Python raises for a project path that is not valid UTF-8 (approval_path encodes the key); the Go port must not copy the crash.",
    ],
    "plan_sample_hash_matches_plan": hash_cases[0]["hash"] == PLAN_HASH,
    "hash_cases": hash_cases,
    "literal_abs_cases": literal_abs_cases,
    "reviewable_cases": reviewable_cases,
    "dumps_cases": dumps_cases,
    "float_cases": float_cases,
    "loads_cases": loads_cases,
    "loads_bytes_cases": loads_bytes_cases,
    "key_cases": key_cases,
    "tree_cases": tree_cases,
})

# ---------------------------------------------------------------------------------------------- diff_cases.json
rng = random.Random(20261113)
diff_cases = []


def add_diff(name: str, old: dict, new: dict) -> None:
    diff_cases.append({"name": name, "old": old, "new": new, "lines": helpers.diff_files(old, new)})


def numbered(n: int, prefix: str = "line") -> str:
    return "".join(f"{prefix} {i}\n" for i in range(n))


def lines_of(text: str) -> list:
    return text.splitlines(keepends=True)


add_diff("new file", {}, {"a.sh": "echo hi\n"})
add_diff("removed file", {"a.sh": "echo hi\n"}, {})
add_diff("changed line", {"a.sh": "echo a\n"}, {"a.sh": "echo b\n"})
add_diff("equal maps", {"a.sh": "x\n", "b.sh": "y\n"}, {"a.sh": "x\n", "b.sh": "y\n"})
add_diff("both empty", {}, {})
add_diff("empty file added", {}, {"a.sh": ""})
add_diff("empty file removed", {"a.sh": ""}, {})
add_diff("file emptied", {"a.sh": "x\n"}, {"a.sh": ""})
add_diff("file filled", {"a.sh": ""}, {"a.sh": "x\n"})
add_diff("empty both sides present", {"a.sh": ""}, {"a.sh": ""})
add_diff("multiple files, sorted", {"b.sh": "1\n", "a.sh": "1\n", ".shortcuts": "{}\n"}, {"b.sh": "2\n", "a.sh": "2\n", ".shortcuts": "{\n}\n", "c.sh": "3\n"})
add_diff("cwd key loses ./", {"./cwd of x": "/a\n"}, {"./cwd of x": "/b\n"})
add_diff("cwd key added", {}, {"./cwd of x": "/b\n", "./cwd of y in .shortcuts.local": "/c\n"})
add_diff("binary new", {}, {"bin": "\x00binary:abc"})
add_diff("binary replaces text", {"bin": "text\n"}, {"bin": "\x00binary:abc"})
add_diff("binary old, text new", {"bin": "\x00binary:abc"}, {"bin": "text\n"})
add_diff("binary old removed", {"bin": "\x00binary:abc"}, {})
add_diff("binary old and new differ", {"bin": "\x00binary:abc"}, {"bin": "\x00binary:def"})
add_diff("binary unchanged next to text change", {"bin": "\x00binary:abc", "a": "1\n"}, {"bin": "\x00binary:abc", "a": "2\n"})
add_diff("last line without newline, both", {"a": "x\ny"}, {"a": "x\nz"})
add_diff("last line without newline, new only", {"a": "x\ny\n"}, {"a": "x\ny"})
add_diff("last line without newline, old only", {"a": "x\ny"}, {"a": "x\ny\n"})
add_diff("no newline at all", {"a": "x"}, {"a": "y"})
add_diff("CRLF changed to LF", {"a": "x\r\ny\r\n"}, {"a": "x\ny\n"})
add_diff("CRLF both", {"a": "x\r\ny\r\nz\r\n"}, {"a": "x\r\nY\r\nz\r\n"})
add_diff("lone CR is a line end", {"a": "a\rb\rc\r"}, {"a": "a\rB\rc\r"})
add_diff("CR CR LF", {"a": "a\r\r\nb\n"}, {"a": "a\r\r\nB\n"})
add_diff("trailing blank lines", {"a": "x\n\n\n"}, {"a": "x\n"})
add_diff("only newlines", {"a": "\n\n\n"}, {"a": "\n\n"})
for sep_name, sep in [("VT", "\x0b"), ("FF", "\x0c"), ("FS", "\x1c"), ("GS", "\x1d"), ("RS", "\x1e"), ("NEL 0x85", "\x85"), ("LS U+2028", "\u2028"), ("PS U+2029", "\u2029")]:
    add_diff(f"separator {sep_name}", {"a": f"one{sep}two{sep}three\n"}, {"a": f"one{sep}TWO{sep}three\n"})
add_diff("separators mixed", {"a": "a\x0bb\x0cc\x1cd\x1de\x1ef\x85g\u2028h\u2029i\r\nj\rk\nl"}, {"a": "a\x0bb\x0cC\x1cd\x1de\x1eF\x85g\u2028h\u2029I\r\nj\rK\nl"})
add_diff("0x85 after non-ASCII text", {"a": "caf\u00e9\u0085x\n"}, {"a": "caf\u00e9\u0085y\n"})
add_diff("non-ASCII and astral", {"a": "caf\u00e9 \U0001F600\n"}, {"a": "caf\u00e9 \U0001F601\n"})
add_diff("repeated lines", {"a": "x\nx\nx\nx\n"}, {"a": "x\nx\ny\nx\nx\n"})
add_diff("all lines identical, count changes", {"a": "x\n" * 10}, {"a": "x\n" * 12})
add_diff("swap two lines", {"a": "a\nb\n"}, {"a": "b\na\n"})
add_diff("insert at start", {"a": numbered(8)}, {"a": "new\n" + numbered(8)})
add_diff("insert at end", {"a": numbered(8)}, {"a": numbered(8) + "new\n"})
add_diff("change in the middle of 20", {"a": numbered(20)}, {"a": numbered(20).replace("line 10\n", "LINE 10\n")})
add_diff("two changes 6 apart (one hunk)", {"a": numbered(30)}, {"a": numbered(30).replace("line 10\n", "X\n").replace("line 16\n", "Y\n")})
add_diff("two changes 7 apart (two hunks)", {"a": numbered(30)}, {"a": numbered(30).replace("line 10\n", "X\n").replace("line 17\n", "Y\n")})
add_diff("three hunks", {"a": numbered(60)}, {"a": numbered(60).replace("line 5\n", "X\n").replace("line 30\n", "Y\n").replace("line 55\n", "Z\n")})
add_diff("delete block", {"a": numbered(20)}, {"a": "".join(l for i, l in enumerate(lines_of(numbered(20))) if not 8 <= i < 12)})
add_diff("replace block with longer", {"a": numbered(12)}, {"a": numbered(5) + "a\nb\nc\nd\ne\nf\n" + "".join(lines_of(numbered(12))[8:])})
add_diff("single line 1 vs 2 range form", {"a": "x\n"}, {"a": "x\ny\n"})
add_diff("line starting with -- and ++", {"a": "-- x\n++ y\n"}, {"a": "--- x\n+++ y\n"})
add_diff("whitespace only change", {"a": "a b\n"}, {"a": "a  b \n"})
add_diff("json file change", {".shortcuts": '{\n  "shortcuts": {\n    "x": {\n      "inline": "echo a"\n    }\n  },\n  "version": 2\n}\n'},
         {".shortcuts": '{\n  "shortcuts": {\n    "x": {\n      "inline": "echo b"\n    },\n    "y": {\n      "inline": "echo c"\n    }\n  },\n  "version": 2\n}\n'})
add_diff("invalid mark suffix", {"a": "echo \\xff\n\n\x00not UTF-8, sha256 aa\n"}, {"a": "echo \\xfe\n\n\x00not UTF-8, sha256 bb\n"})
add_diff("symlink record", {"scripts/ns/d": "symlink to ../a\n"}, {"scripts/ns/d": "symlink to ../b\n"})

# autojunk: len(b) >= 200 and elements seen more than len(b)//100 + 1 times are dropped from the index
base200 = "".join(f"unique {i}\n" for i in range(200))
add_diff("200 lines, one changed", {"a": base200}, {"a": base200.replace("unique 100\n", "changed\n")})
base199 = "".join(f"unique {i}\n" for i in range(199))
add_diff("199 lines (no autojunk), one changed", {"a": base199}, {"a": base199.replace("unique 100\n", "changed\n")})
popular = "".join(("}\n" if i % 2 else "echo {\n") if i % 3 else f"line {i}\n" for i in range(240))
add_diff("popular lines trip autojunk", {"a": popular}, {"a": popular.replace("line 99\n", "LINE 99\n") + "tail\n"})
add_diff("blank lines are popular", {"a": "\n".join(f"l{i}" if i % 4 == 0 else "" for i in range(260)) + "\n"},
         {"a": "\n".join(f"l{i}" if i % 4 == 0 else "" for i in range(300)) + "\n"})
add_diff("old short, new 250 repeated", {"a": "x\ny\nx\n"}, {"a": "x\ny\n" * 125})
add_diff("old 250 repeated, new short", {"a": "x\ny\n" * 125}, {"a": "x\ny\nx\n"})
add_diff("300 identical lines vs 301", {"a": "same\n" * 300}, {"a": "same\n" * 301})
add_diff("200 lines shifted by one", {"a": base200}, {"a": "head\n" + base200})
add_diff("200 lines reversed", {"a": base200}, {"a": "".join(reversed(lines_of(base200)))})
alphabet = ["", "}", "{", "fi", "done", "x", "y", "echo a", "echo b", "set -e", "# c"]
for n in range(1, 13):
    old_len, new_len = rng.choice([30, 80, 150, 210, 260, 320]), rng.choice([30, 80, 150, 210, 260, 320])
    old = "".join(rng.choice(alphabet) + "\n" for _ in range(old_len))
    new = "".join(rng.choice(alphabet) + "\n" for _ in range(new_len))
    add_diff(f"random small alphabet {n} ({old_len} -> {new_len} lines)", {"a": old}, {"a": new})
for n in range(1, 7):
    count = rng.choice([220, 260, 400])
    old_lines = [f"line {rng.randrange(40)}\n" for _ in range(count)]
    new_lines = list(old_lines)
    for _ in range(rng.randrange(3, 12)):
        pos = rng.randrange(len(new_lines))
        op = rng.choice(["del", "ins", "rep"])
        if op == "del":
            del new_lines[pos]
        elif op == "ins":
            new_lines.insert(pos, f"new {rng.randrange(99)}\n")
        else:
            new_lines[pos] = f"changed {rng.randrange(99)}\n"
    add_diff(f"random edits on {count} lines {n}", {"a": "".join(old_lines)}, {"a": "".join(new_lines)})
for n in range(1, 4):
    old_lines = [f"w{rng.randrange(25)} \u00e9\r\n" if rng.random() < 0.5 else f"w{rng.randrange(25)}\n" for _ in range(rng.choice([50, 205]))]
    new_lines = list(old_lines)
    rng.shuffle(new_lines)
    add_diff(f"random shuffle with CRLF {n}", {"a": "".join(old_lines)}, {"a": "".join(new_lines)})

write_json(os.path.join(OUT, "diff_cases.json"), {"cases": diff_cases})

# ---------------------------------------------------------------------------------------------- text
env = {**os.environ, "COLUMNS": "80"}


def run_cli(argv: list) -> subprocess.CompletedProcess:
    return subprocess.run([sys.executable, "-B", os.path.join(REPO, "src"), *argv], capture_output=True, env=env,
                          cwd=os.path.join(AREA, "cwd"), stdin=subprocess.DEVNULL, timeout=60)


def write_bytes(path: str, data: bytes) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(data)


help_dir = os.path.join(os.path.dirname(OUT), "cli", "help")
completion_dir = os.path.join(os.path.dirname(OUT), "cli", "completion")
help_files = []
for command in ("run", "new", "list", "show", "location", "rm", "promote", "update", "init", "completion"):
    proc = run_cli(["help", command])
    assert proc.returncode == 0 and not proc.stderr, (command, proc.stderr)
    write_bytes(os.path.join(help_dir, command + ".txt"), proc.stdout)
    help_files.append(command + ".txt")
    other = run_cli([command, "-h"])  # `shortcut <cmd> -h` must print the same
    assert other.stdout == proc.stdout and other.returncode == 0, command
for shell, script in completion.COMPLETION_SCRIPTS.items():
    proc = run_cli(["completion", shell])
    assert proc.returncode == 0 and proc.stdout == script.encode(), shell
    write_bytes(os.path.join(completion_dir, shell + ".txt"), proc.stdout)

# argparse errors: the real CLI, one subprocess each. Every case must fail with exit 2 before touching a store.
error_argvs = {
    "new": [[], ["a", "b"], ["x", "--bogus"], ["x", "--desc"], ["x", "--script"], ["x", "--copy"], ["x", "--cwd"], ["x", "--namespace"],
            ["x", "--by"], ["x", "--arg"], ["x", "--arg-desc"], ["x", "--script", "f", "--copy", "g"], ["x", "--copy", "g", "--script", "f"],
            ["x", "--c"], ["x", "--co"], ["x", "--scr"], ["x", "--local=1"], ["x", "--confirm=yes"], ["--", "true"], ["x", "-z"],
            ["x", "--desc", "--local"], ["x", "--script", "--copy"], ["x", "--desc", "a", "--bogus", "--"], ["--bogus"], ["--desc", "d"]],
    "list": [["--bogus"], ["a"], ["--names", "a", "b"], ["-x"], ["--names=a", "b"], ["--na", "a", "--bogus"]],
    "show": [[], ["--bogus", "x"], ["--json"], ["x", "--json=1"], ["x", "--global=1"], ["--glo=1", "x"], ["x", "--json", "y"], ["a", "--json", "b"], ["-j", "x"]],
    "location": [[], ["--json", "x"], ["--bogus", "x"], ["x", "--global=1"], ["--global"], ["a", "--global", "b"]],
    "rm": [["--namespace"], ["--bogus"], ["--g=1"], ["-y=1"], ["--yes=1"], ["--local=1"], ["--namespace", "a", "--namespace"], ["-z"], ["x", "--namespace", "a", "--bogus"]],
    "promote": [[], ["--as"], ["a", "b"], ["a", "--bogus"], ["a", "--force=1"], ["a", "--as", "b", "--as"], ["--as", "b"], ["-y"], ["a", "--yes=1"]],
    "update": [["a", "b"], ["--bogus"], ["-y=1"], ["--ye=1"], ["a", "--bogus"], ["-z"]],
    "init": [["a", "b"], ["--bogus"], ["--global=1"], ["--repl=1"], ["-g"], ["--glob", "a", "b"], ["--replace=x"]],
    "completion": [["powershell"], ["bash", "zsh"], ["--bogus"], [""], ["BASH"], ["ba"], ["bash", "--bogus"], ["-s", "bash"], ["sh"]],
}
error_cases = []
for command, argvs in error_argvs.items():
    for argv in argvs:
        proc = run_cli([command, *argv])
        assert proc.returncode == 2 and not proc.stdout, (command, argv, proc.returncode, proc.stdout, proc.stderr)
        error_cases.append({"command": command, "argv": argv, "stderr": proc.stderr.decode("utf-8"), "exit": proc.returncode})
# usage errors that dispatch and `run` print themselves (C3, C4), not argparse; stdout empty, exit 2
for argv in (["run"], ["run", "-y"], ["r"], ["--bogus"], ["bogus"], ["-x"], ["bogus", "--help"]):
    proc = run_cli(argv)
    assert proc.returncode == 2 and not proc.stdout, (argv, proc.returncode, proc.stdout)
    error_cases.append({"command": "(dispatch)", "argv": argv, "stderr": proc.stderr.decode("utf-8"), "exit": proc.returncode})
write_json(os.path.join(OUT, "text", "argparse_errors.json"), {"columns": 80, "cases": error_cases})


# argparse outcomes in-process: Python's parser decides; the generator records the Namespace (or the exit) and stops
# before any command code runs. parser_argv is what `new` hands the parser after its own split at "--".
class Stop(BaseException):
    def __init__(self, argv: list, namespace: dict) -> None:
        self.argv, self.namespace = argv, namespace


class Recording(Parser):
    def parse_args(self, args=None, namespace=None):  # type: ignore[override]
        parsed = super().parse_args(args, namespace)
        raise Stop(list(args), vars(parsed))


cli.Parser = Recording
parse_argvs = {
    "new": [["x"], ["x", "--desc", "d"], ["x", "--desc=d"], ["x", "--de", "d"], ["--desc", "d", "x"], ["x", "--script", "f"], ["x", "--scr=f"],
            ["x", "--copy", "-"], ["x", "--copy=-"], ["x", "--local", "--namespace", "ns", "--by", "me"], ["x", "--namespace=ns", "--na"],
            ["x", "--arg", "a", "--arg", "b=1", "--arg-desc", "a=text", "--arg-desc=b=two"], ["x", "--arg=rest...="], ["x", "--arg", "-n"],
            ["x", "--confirm", "--cwd", "caller"], ["x", "--cwd", "-"], ["x", "--desc", "-5"], ["x", "--desc="], ["x", "--desc", ""],
            ["x", "--", "echo", "hi"], ["x", "--local", "--", "a", "--desc", "b"], ["x", "--arg", "a", "--", "echo", "$a"], ["x", "--"], ["x", "--", "--"],
            ["x", "--desc", "d", "--", "y"], ["x", "--script", "f", "--", "true"], ["x", "--conf"], ["x", "--cwd=a=b"], ["x", "-h"], ["--help"]],
    "list": [[], ["--names"], ["--names", "ns"], ["--names=ns"], ["--n"], ["--n", "ns"], ["--names="], ["--names", ""], ["--names", "--names"], ["-h"]],
    "show": [["x"], ["ns", "x"], ["--json", "x"], ["x", "--json"], ["x", "--global"], ["--glob", "--js", "x"], ["a", "--json", "b"], ["--", "--json"],
             ["--json", "--", "x"], ["x", "--", "y"], ["a", "b", "c", "--global"], ["-h"]],
    "location": [["x"], ["ns", "x"], ["--global", "x"], ["x", "--global"], ["--glo", "x"], ["a", "--global", "b"], ["--", "-x"], ["-h"]],
    "rm": [["x"], ["ns", "x"], ["-y", "x"], ["x", "-y"], ["--yes", "x"], ["--namespace", "ns"], ["--namespace=ns", "-y"], ["--local", "x"], ["--global", "x"],
           ["--glob", "x"], [], ["-y"], ["-yy", "x"], ["--nam", "ns"], ["x", "--local", "-y"], ["a", "-y", "b"], ["--local", "--global", "x"], ["-h"]],
    "promote": [["x"], ["x", "--as", "y"], ["x", "--as=y"], ["--as", "y", "x"], ["x", "-y", "--force"], ["x", "--fo"], ["x", "--yes"], ["-yx"], ["x", "--as", "-y"], ["-h"]],
    "update": [[], ["."], ["ns", "-y"], ["-y", "ns"], ["--yes"], ["--y"], ["-h"]],
    "init": [[], ["--global"], ["--global", "src"], ["src", "--global", "--replace", "-y"], ["--g", "--rep", "-y", "/p"], ["--global", "--"], ["--", "--global"],
             ["-y"], ["-h"]],
    "completion": [[], ["bash"], ["zsh"], ["fish"], ["bash", "--"], ["--", "zsh"], ["-h"]],
}
parse_cases = []
for command, argvs in parse_argvs.items():
    for argv in argvs:
        err, out = io.StringIO(), io.StringIO()
        try:
            with contextlib.redirect_stderr(err), contextlib.redirect_stdout(out):
                cli.HANDLERS[command](list(argv))
        except Stop as stop:
            assert not out.getvalue() and not err.getvalue(), (command, argv)
            parse_cases.append({"command": command, "argv": argv, "parser_argv": stop.argv, "namespace": stop.namespace})
        except SystemExit as exit_:
            parse_cases.append({"command": command, "argv": argv, "exit": exit_.code, "stdout": out.getvalue(), "stderr": err.getvalue()})
        else:
            raise SystemExit(f"abort: {command} {argv} ran command code")
write_json(os.path.join(OUT, "text", "argparse_parses.json"), {
    "columns": 80,
    "notes": ["namespace: vars() of the argparse Namespace; parser_argv: what the parser saw (new splits at the first -- first).",
              "a case with 'exit' is an argparse exit (error: code 2 and stderr; -h: code 0 and stdout)."],
    "cases": parse_cases,
})

print(f"approval.json: {len(hash_cases)} hash, {len(literal_abs_cases)} literal-abs, {len(reviewable_cases)} reviewable, "
      f"{len(dumps_cases)} dumps, {len(float_cases)} float, {len(loads_cases)} loads, {len(loads_bytes_cases)} loads-bytes, "
      f"{len(key_cases)} key, {len(tree_cases)} tree")
print(f"diff_cases.json: {len(diff_cases)}; argparse_errors: {len(error_cases)}; argparse_parses: {len(parse_cases)}; help: {len(help_files)}")
print(f"plan sample hash {hash_cases[0]['hash']} matches plan: {hash_cases[0]['hash'] == PLAN_HASH}")
