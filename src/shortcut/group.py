"""One set of shortcuts and where it comes from."""

from __future__ import annotations

import dataclasses
import json
import os
from typing import Any, Dict, Optional

from .helpers import is_global_store


@dataclasses.dataclass(frozen=True)
class Group:
    """One set of shortcuts: the global store's shortcuts.json (name ""), a project's .shortcuts with
    .shortcuts.local over it, or an imported namespace. store_dir is the global store or the project's root."""
    name: str
    file: str
    store_dir: str
    data: Dict[str, Any]
    error: Optional[str] = None  # why the file can't be used
    local_file: str = ""  # only a project's own group has one
    local_data: Dict[str, Any] = dataclasses.field(default_factory=dict)

    @property
    def shared_shortcuts(self) -> Dict[str, Any]:
        shortcuts = self.data.get("shortcuts", {})
        return shortcuts if isinstance(shortcuts, dict) else {}

    @property
    def local_shortcuts(self) -> Dict[str, Any]:
        """Raw, so a null that hides a shared entry is still here."""
        shortcuts = self.local_data.get("shortcuts", {})
        return shortcuts if isinstance(shortcuts, dict) else {}

    @property
    def shortcuts(self) -> Dict[str, Any]:
        """What can run: the shared entries, each replaced or hidden by .shortcuts.local."""
        merged = {**self.shared_shortcuts, **self.local_shortcuts}
        return {name: entry for name, entry in merged.items() if name not in self.local_shortcuts or entry is not None}

    def source(self, name: str) -> str:
        """Where a project entry comes from: shared, local, overridden or hidden."""
        if name not in self.local_shortcuts:
            return "shared"
        if self.local_shortcuts[name] is None:
            return "hidden"
        return "overridden" if name in self.shared_shortcuts else "local"

    def defining_file(self, name: str) -> str:
        return self.local_file if name in self.local_shortcuts else self.file

    @property
    def is_project(self) -> bool:
        return not is_global_store(self.store_dir)

    @property
    def folder(self) -> str:
        """Where relative 'cwd' values resolve: the folder of the defining file."""
        return os.path.dirname(os.path.abspath(self.file))

    @property
    def script_base(self) -> str:
        """Relative paths resolve against a namespace's folder, a project's root, or the global store."""
        return self.folder if self.name else self.store_dir

    def display(self, name: str) -> str:
        return f"{self.name} {name}" if self.name else name

    @property
    def needs_approval(self) -> bool:
        return bool(self.name) or not is_global_store(self.store_dir)

    @property
    def key(self) -> str:
        """What the approval is recorded against: the project's path, or for a namespace the store it's in,
        its name there and its file. Pointing a name at another file, even one approved elsewhere, is new."""
        store = os.path.realpath(self.store_dir)
        return json.dumps([store, self.name, os.path.realpath(self.file)]) if self.name else store

    @property
    def label(self) -> str:
        return self.name or self.key

    @property
    def update_cmd(self) -> str:
        return f"shortcut update {self.name}" if self.name else "shortcut update"
