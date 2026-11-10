"""A store: its main group and its namespaces."""

from __future__ import annotations

import dataclasses
from typing import TYPE_CHECKING, Dict, List

if TYPE_CHECKING:
    from .group import Group


@dataclasses.dataclass(frozen=True)
class Store:
    main: Group
    namespaces: Dict[str, Group]
    problems: List[str]  # load errors: a name that is both a shortcut and a namespace

    @property
    def groups(self) -> List[Group]:
        return [self.main, *self.namespaces.values()]
