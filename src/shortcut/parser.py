"""The argument parser every command uses."""

from __future__ import annotations

import argparse
from typing import Any


class Parser(argparse.ArgumentParser):
    """Plain output: on 3.14 argparse adds colour, and SafeOutput would print it as ^[ text."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.color = False
