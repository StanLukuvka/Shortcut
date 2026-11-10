"""The confirmation shortcut asks before it runs or changes something."""

from __future__ import annotations

import dataclasses
import sys
from typing import Tuple

from .helpers import ask, die


@dataclasses.dataclass(frozen=True)
class Confirm:
    """Every question shortcut asks before it runs or changes something. message says what will happen
    and is shown above the question. -y answers yes. Without a terminal it stops with no_terminal."""
    question: str
    no_terminal: str
    message: str = ""
    accept: Tuple[str, ...] = ("y", "yes")  # anything else means the answer has to be typed out
    tell_on_yes: bool = False  # show message with -y too, for a change that can't be taken back

    def granted(self, assume_yes: bool) -> bool:
        if assume_yes:
            if self.tell_on_yes and self.message:
                print(self.message)
            return True
        if not sys.stdin.isatty():
            die(self.no_terminal)
        if self.message:
            print(self.message, file=sys.stderr)
        suffix = " [y/N] " if self.accept == Confirm.accept else " "
        if ask(self.question + suffix, self.accept):
            return True
        print("Aborted.", file=sys.stderr)
        return False
