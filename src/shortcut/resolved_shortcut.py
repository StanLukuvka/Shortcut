"""A name looked up: the entry, its group and where it runs."""

from __future__ import annotations

import dataclasses
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .shortcut_entry import ShortcutEntry
    from .group import Group


@dataclasses.dataclass(frozen=True)
class ResolvedShortcut:
    entry: ShortcutEntry
    group: Group
    run_cwd: str          # computed execution directory
    hides_global: bool = False  # project entry with the same name as a global one

    @property
    def display(self) -> str:
        return self.group.display(self.entry.name)
