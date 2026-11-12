"""Output that a file's contents can't use to rewrite the screen."""

from __future__ import annotations

from typing import Any

from .helpers import safe


class SafeOutput:
    """Wraps sys.stdout and sys.stderr so nothing printed from a file can rewrite the screen a person
    approves on. A child's output goes to .buffer and isn't touched; write_styled adds our own colour."""

    def __init__(self, stream: Any) -> None:
        self.stream = stream

    def write(self, text: str) -> int:
        return self.stream.write(safe(text))

    def __getattr__(self, name: str) -> Any:
        return getattr(self.stream, name)
