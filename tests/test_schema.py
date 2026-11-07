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


def entry_fields() -> set[str]:
    entry = next(n for n in ast.walk(TREE) if isinstance(n, ast.ClassDef) and n.name == "ShortcutEntry")
    return {n.target.id for n in entry.body if isinstance(n, ast.AnnAssign)} - {"name"}


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
        entry = {k: SAMPLES[v["type"]] for k, v in SCHEMA["$defs"]["entry"]["properties"].items()}
        del entry["script"]
        arg = {k: SAMPLES[v["type"]] for k, v in SCHEMA["$defs"]["arg"]["properties"].items()}
        arg["name"] = "x"
        entry["args"] = [arg]
        self.write_import(self.shortcuts_home, "ns", {"version": 2, "description": "x", "shortcuts": {"go": entry}})
        res = self.run_cli(["update", "ns", "-y"])
        self.assertEqual(res.returncode, 0, res.stderr)


if __name__ == "__main__":
    unittest.main()
