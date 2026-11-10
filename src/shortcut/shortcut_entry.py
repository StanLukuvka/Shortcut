"""One shortcut's definition, as stored in JSON."""

from __future__ import annotations

import dataclasses
from typing import Any, Dict, List, Optional

from .helpers import validate_args


@dataclasses.dataclass(frozen=True)
class ShortcutEntry:
    name: str
    description: str = ""
    inline: Optional[str] = None
    script: Optional[str] = None
    cwd: Optional[str] = None
    icon: Optional[str] = None
    created_by: Optional[str] = None
    created: Optional[str] = None
    confirm: bool = False
    export: bool = False  # made to work outside its project; only a project's own files can say so
    args: Optional[List[Dict[str, Any]]] = None
    origin: Optional[Dict[str, str]] = None  # "from" in JSON: the project promote took it from; global store only

    TEXT_FIELDS = ("inline", "script", "cwd", "icon", "created_by", "created")

    @classmethod
    def from_dict(cls, name: str, data: Any) -> ShortcutEntry:
        def bad(problem: str) -> ValueError:
            return ValueError(f"'{name}' is not a valid entry: {problem}")

        if not isinstance(data, dict):
            raise bad("expected a dictionary")
        # An ignored field could be a safety setting from a newer version, so never ignore one
        known = {f.name for f in dataclasses.fields(cls)} - {"name", "origin"} | {"from"}
        unknown = sorted(set(data) - known)
        if unknown:
            raise bad(f"unknown field: {', '.join(unknown)}")
        if not isinstance(data.get("description", ""), str):
            raise bad("'description' must be a string")
        for field in cls.TEXT_FIELDS:
            val = data.get(field)
            if val is not None and not isinstance(val, str):
                raise bad(f"'{field}' must be a string")
        for field in ("confirm", "export"):
            if not isinstance(data.get(field, False), bool):
                raise bad(f"'{field}' must be a boolean")
        if (data.get("inline") is None) == (data.get("script") is None):
            raise bad("it needs exactly one of 'inline' and 'script'")
        if data.get("args") is not None:
            validate_args(name, data["args"])
        data = dict(data)
        origin = data.pop("from", None)
        if origin is not None and not (isinstance(origin, dict) and set(origin) == {"source", "name"}
                                       and all(isinstance(v, str) for v in origin.values())):
            raise bad("'from' must be an object with a string 'source' and 'name'")
        return cls(name=name, origin=origin, **data)

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"description": self.description}
        for field in self.TEXT_FIELDS:
            if getattr(self, field) is not None:
                d[field] = getattr(self, field)
        if self.confirm:
            d["confirm"] = True
        if self.export:
            d["export"] = True
        if self.args is not None:
            d["args"] = self.args
        if self.origin is not None:
            d["from"] = self.origin
        return d
