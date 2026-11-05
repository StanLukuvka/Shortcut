#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
COMPL_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"

mkdir -p "$BIN_DIR"
mkdir -p "$COMPL_DIR"

cp "$SCRIPT_DIR/shortcut.py" "$BIN_DIR/shortcut"
chmod +x "$BIN_DIR/shortcut"

"$BIN_DIR/shortcut" completion bash > "$COMPL_DIR/shortcut"

"$BIN_DIR/shortcut" migrate

echo "Installed shortcut to $BIN_DIR/shortcut"
echo "Installed bash completion to $COMPL_DIR/shortcut"
echo "Bundled shortcuts (log, desktop, nano): shortcut init --global $SCRIPT_DIR/templates"

if command -v fish >/dev/null 2>&1; then
    FISH_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions"
    mkdir -p "$FISH_DIR"
    "$BIN_DIR/shortcut" completion fish > "$FISH_DIR/shortcut.fish"
    echo "Installed fish completion to $FISH_DIR/shortcut.fish"
fi
if command -v zsh >/dev/null 2>&1; then
    echo "For zsh completion, add to ~/.zshrc after compinit:  source <(shortcut completion zsh)"
fi

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
        echo "Notice: $BIN_DIR is not in your PATH. Consider adding it to your ~/.bashrc:"
        echo "  export PATH=\"$BIN_DIR:\$PATH\""
        ;;
esac
