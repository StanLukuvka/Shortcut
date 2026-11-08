#!/usr/bin/env python3
"""docs/shortcut.schema.json against shortcut.py."""

import ast
import json
import unittest

from fixtures import REPO, SHORTCUT, ShortcutTestCase

SCHEMA = json.loads((REPO / "docs" / "shortcut.schema.json").read_text(encoding="utf-8"))
SOURCE = SHORTCUT.read_text(encoding="utf-8")
TREE = ast.parse(SOURCE)
SAMPLES = {"string": "x", "boolean": True, "array": []}
RESTRICTED = {"export": "Only allowed in .shortcuts and .shortcuts.local", "from": "Only allowed in the global store's shortcuts.json"}


def entry_fields() -> set[str]:
    entry = next(n for n in ast.walk(TREE) if isinstance(n, ast.ClassDef) and n.name == "ShortcutEntry")
    fields = {n.target.id for n in entry.body if isinstance(n, ast.AnnAssign)} - {"name"}
    return fields - {"origin"} | {"from"}  # origin is written "from" in JSON


def arg_fields() -> set[str]:
    for node in ast.walk(TREE):
        if isinstance(node, ast.Set):
            fields = {n.value for n in node.elts if isinstance(n, ast.Constant)}
            if "rest" in fields:
                return fields
    raise AssertionError("no set of args fields in shortcut.py")


class TestSchema(ShortcutTestCase):
    def test_fields_match_the_code(self):
        self.assertEqual(set(SCHEMA["$defs"]["entry"]["properties"]), entry_fields())
        self.assertEqual(set(SCHEMA["$defs"]["arg"]["properties"]), arg_fields())

    def test_format_matches_the_code(self):
        self.assertRegex(SOURCE, rf"(?m)^REGISTRY_VERSION: int = {SCHEMA['properties']['version']['const']}\b")
        self.assertIn(SCHEMA["properties"]["shortcuts"]["propertyNames"]["pattern"], SOURCE)
        self.assertIn(SCHEMA["$defs"]["arg"]["properties"]["name"]["pattern"].strip("^$"), SOURCE)

    def test_every_schema_field_is_accepted(self):
        properties = SCHEMA["$defs"]["entry"]["properties"]
        entry = {k: SAMPLES[v["type"]] for k, v in properties.items() if k not in RESTRICTED}
        del entry["script"]
        arg = {k: SAMPLES[v["type"]] for k, v in SCHEMA["$defs"]["arg"]["properties"].items()}
        arg["name"] = "x"
        entry["args"] = [arg]
        self.write_import(self.shortcuts_home, "ns", {"version": 2, "description": "x", "shortcuts": {"go": entry}})
        res = self.run_cli(["update", "ns", "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)

    def test_restricted_fields_are_accepted_where_the_schema_allows_them(self):
        for field in RESTRICTED:
            self.assertIn(RESTRICTED[field], SCHEMA["$defs"]["entry"]["properties"][field]["description"])
        source = {k: SAMPLES[v["type"]] for k, v in SCHEMA["$defs"]["entry"]["properties"]["from"]["properties"].items()}
        self.assertEqual(SCHEMA["$defs"]["entry"]["properties"]["from"]["required"], ["source", "name"])

        proj = self.project({"go": {"inline": "echo go", "export": True}}, approved=True)
        self.assertEqual(self.run_cli(["r", "go"], cwd=str(proj)).stdout, "go\n")
        self.write_store(self.shortcuts_home, {"go": {"inline": "echo go", "from": source}})
        self.assertEqual(self.run_cli(["r", "go"]).stdout, "go\n")


if __name__ == "__main__":
    unittest.main()
