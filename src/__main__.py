"""Runs shortcut from src/: python3 src, and the entry point of the zipapp install.sh builds."""

import sys

if sys.version_info < (3, 10):  # before the package, which older versions can't parse
    sys.exit(f"error: shortcut needs Python 3.10 or newer, and this is {sys.version.split()[0]}")

from shortcut.cli import main  # noqa: E402

main()
