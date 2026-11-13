#!/bin/sh
# FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (R-2/R-4). Don't hand-edit without checking the plan.
#
# Installs the shortcut binary:  curl -fsSL <release>/install.sh | sh
#
# It downloads the archive for this system, checks it against checksums.txt, and puts `shortcut` in BIN_DIR
# (replacing it by an atomic rename). It then writes shell completions for bash, fish and zsh. For zsh it adds
# one marked block to ~/.zshrc, once. At a terminal it offers the bundled shortcuts (`shortcut init --global
# --bundled`); you review and answer that, it never runs with -y.
#
# Environment:
#   BIN_DIR                   where the binary goes (default ~/.local/bin)
#   XDG_DATA_HOME             bash and zsh completion files (default ~/.local/share)
#   XDG_CONFIG_HOME           fish completion file (default ~/.config)
#   SHORTCUT_VERSION          install this version (0.1.0 or v0.1.0); default is the latest release
#   SHORTCUT_DOWNLOAD_BASE    download from here instead of GitHub (used by the tests)
#   SHORTCUT_NO_RC            do not touch ~/.zshrc
#   SHORTCUT_NO_BUNDLED       do not offer the bundled shortcuts
#
# With SHORTCUT_DOWNLOAD_BASE=file://<dir>, <dir> is flat and holds VERSION (the version number, read when
# SHORTCUT_VERSION is not set), shortcut_<version>_<os>_<arch>.tar.gz (a tar.gz with `shortcut` at its top) and
# checksums.txt (sha256sum format). Otherwise the files come from
# https://github.com/StanLukuvka/Shortcut/releases/download/v<version>/ (the latest release: .../latest/download/).
#
# The whole script is one function called on the last line, so a download cut short runs nothing.

main() {
  set -eu

  repo="StanLukuvka/Shortcut"
  base="${SHORTCUT_DOWNLOAD_BASE:-https://github.com/$repo/releases}"
  bin_dir="${BIN_DIR:-$HOME/.local/bin}"
  data="${XDG_DATA_HOME:-$HOME/.local/share}"
  conf="${XDG_CONFIG_HOME:-$HOME/.config}"

  say() { printf '%s\n' "$*"; }
  die() { printf 'error: %s\n' "$*" >&2; exit 1; }
  have() { command -v "$1" >/dev/null 2>&1; }
  fetch() { if have curl; then curl -fsSL "$1"; else wget -qO- "$1"; fi; }
  fetch_to() { if have curl; then curl -fsSL -o "$2" "$1"; else wget -qO "$2" "$1"; fi; }

  # platform
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported system $(uname -s): Linux and macOS only (on Windows use WSL)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported CPU $(uname -m)" ;;
  esac
  # a shell running under Rosetta reports x86_64 on an Apple Silicon Mac
  if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
    arch=arm64
  fi

  # version
  have curl || have wget || die "needs curl or wget to download shortcut"
  if [ -n "${SHORTCUT_VERSION:-}" ]; then
    version="${SHORTCUT_VERSION#v}"
  else
    if [ -n "${SHORTCUT_DOWNLOAD_BASE:-}" ]; then latest="$base"; else latest="$base/latest/download"; fi
    version="$(fetch "$latest/VERSION")" || die "could not find the latest release at $latest/VERSION (check the connection, or set SHORTCUT_VERSION)"
    version="$(printf '%s' "$version" | tr -d ' \r\n')"
    [ -n "$version" ] || die "empty VERSION at $latest/VERSION"
  fi
  if [ -n "${SHORTCUT_DOWNLOAD_BASE:-}" ]; then down="$base"; else down="$base/download/v$version"; fi
  archive="shortcut_${version}_${os}_${arch}.tar.gz"

  # download and verify
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  trap 'exit 1' INT TERM
  fetch_to "$down/$archive" "$tmp/$archive" || die "no download for $os/$arch at $down/$archive"
  fetch_to "$down/checksums.txt" "$tmp/checksums.txt" || die "could not download checksums.txt"
  want="$(awk -v f="$archive" '$2 == f || $2 == "*" f {print $1}' "$tmp/checksums.txt")"
  [ -n "$want" ] || die "$archive is not in checksums.txt"
  if have sha256sum; then
    got="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
  elif have shasum; then
    got="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
  else
    die "needs sha256sum or shasum to verify the download"
  fi
  [ "$got" = "$want" ] || die "checksum mismatch for $archive (expected $want, got $got); nothing was installed"
  tar -xzf "$tmp/$archive" -C "$tmp" shortcut || die "could not unpack $archive"

  # install: write next to the target, then rename, so a running shortcut is never half replaced
  mkdir -p "$bin_dir"
  was_link=0
  if [ -L "$bin_dir/shortcut" ]; then was_link=1; fi
  new="$bin_dir/.shortcut.new.$$"
  install -m 0755 "$tmp/shortcut" "$new" || die "could not write to $bin_dir"
  mv -f "$new" "$bin_dir/shortcut" || { rm -f "$new"; die "could not replace $bin_dir/shortcut"; }
  say "Installed shortcut $version to $bin_dir/shortcut"
  if [ "$was_link" = 1 ]; then
    say "Note: $bin_dir/shortcut was a link (uv or pipx?) and is replaced. Run 'uv tool uninstall shortcut' or 'pipx uninstall shortcut' to clean up."
  fi

  # completions
  mkdir -p "$data/bash-completion/completions"
  "$bin_dir/shortcut" completion bash > "$data/bash-completion/completions/shortcut"
  if have fish; then
    mkdir -p "$conf/fish/completions"
    "$bin_dir/shortcut" completion fish > "$conf/fish/completions/shortcut.fish"
  fi
  if have zsh && [ -z "${SHORTCUT_NO_RC:-}" ] && { [ "$(basename "${SHELL:-}")" = zsh ] || [ -f "$HOME/.zshrc" ]; }; then
    mkdir -p "$data/shortcut"
    "$bin_dir/shortcut" completion zsh > "$data/shortcut/completion.zsh"
    if ! grep -q '# >>> shortcut completion >>>' "$HOME/.zshrc" 2>/dev/null; then
      {
        printf '\n# >>> shortcut completion >>>\n'
        printf '(( $+functions[compdef] )) || { autoload -Uz compinit; compinit; }\n'
        printf '[ -r "%s/shortcut/completion.zsh" ] && source "%s/shortcut/completion.zsh"\n' "$data" "$data"
        printf '# <<< shortcut completion <<<\n'
      } >> "$HOME/.zshrc"
      say "Added a marked completion block to ~/.zshrc (open a new terminal)"
    fi
  fi

  # bundled shortcuts: the user reviews and answers, never -y
  if [ -z "${SHORTCUT_NO_BUNDLED:-}" ] && [ -r /dev/tty ] && [ -w /dev/tty ]; then
    "$bin_dir/shortcut" init --global --bundled </dev/tty || say "Skipped the bundled shortcuts. Add them later: shortcut init --global --bundled"
  else
    say "Bundled shortcuts: shortcut init --global --bundled"
  fi

  # PATH notice
  case ":$PATH:" in
    *":$bin_dir:"*) ;;
    *)
      say "Notice: $bin_dir is not in your PATH."
      case "$(basename "${SHELL:-sh}")" in
        fish) say "  fish_add_path $bin_dir" ;;
        *) say "  echo 'export PATH=\"$bin_dir:\$PATH\"' >> ~/.profile   (bash: ~/.bashrc, zsh: ~/.zshrc)" ;;
      esac ;;
  esac
  say "Try: shortcut new hi -- 'echo hello \"\$1\"' && shortcut r hi you"
}

main "$@"
