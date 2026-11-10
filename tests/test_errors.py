"""docs/errors.md lists every error message in shortcut.py."""

import ast
import unittest

from fixtures import REPO, SHORTCUT, ShortcutTestCase

DOC = (REPO / "docs" / "errors.md").read_text(encoding="utf-8")
ENTRY_PREFIX = "is not a valid entry: "
RAISERS = {"die", "fail", "bad", "ValueError", "KeyError", "FileNotFoundError", "RuntimeError", "PermissionError", "OSError"}


def pieces(node: ast.AST) -> list[str] | None:
    """The fixed text of a message, split where a value goes in. None if it's built some other way."""
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return [node.value]
    if isinstance(node, ast.JoinedStr):
        out = [""]
        for value in node.values:
            if isinstance(value, ast.Constant):
                out[-1] += value.value
            else:
                out.append("")
        return out
    return None


def messages() -> list[tuple[int, list[str]]]:
    """(line, fixed text) of each message: what's raised or passed to die, a file_error, a store problem."""
    tree = ast.parse(SHORTCUT.read_text(encoding="utf-8"))
    found = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and node.args and ast.unparse(node.func) in RAISERS | {"problems.append"}:
            found.append((node.lineno, pieces(node.args[0])))
        if isinstance(node, ast.FunctionDef) and node.name == "file_error":
            for ret in ast.walk(node):
                if isinstance(ret, ast.Return) and ret.value is not None:
                    value = ret.value.body if isinstance(ret.value, ast.IfExp) else ret.value
                    found.append((ret.lineno, pieces(value)))
    return [(line, text) for line, text in found if text is not None]


class TestErrors(ShortcutTestCase):
    def test_every_message_is_in_the_table(self):
        found = messages()
        self.assertGreater(len(found), 80)
        missing = []
        for line, text in found:
            # The table names the entry prefix once, above its rows
            for piece in (part for whole in text for part in whole.split(ENTRY_PREFIX)):
                piece = piece.strip(" .:'")
                if len(piece) >= 6 and piece not in DOC:
                    missing.append(f"shortcut.py:{line}: {piece!r}")
        self.assertEqual(missing, [], "not in docs/errors.md")


if __name__ == "__main__":
    unittest.main()
