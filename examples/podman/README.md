# podman namespace

Shortcuts for podman containers, run as `shortcut r podman <name>`. `docker` works in their place if it's podman's shim.

## Install

From a clone:

```bash
shortcut init --global examples/podman
shortcut update podman
```

That copies `podman.shortcut` and `scripts/podman/` into `~/.shortcuts/imports/`. After changing either there, run `shortcut update podman` again.

## Picking a container

Each terminal can pick one container. `logs`, `sh`, `restart`, `ports`, `exec` and `stop` act on it when you don't name one.

```
shortcut r podman list        numbered list; * marks the pick
shortcut r podman use 3       pick row 3 of the last list (or a name)
shortcut r podman use         show the pick's row
shortcut r podman use none    clear the pick
shortcut r podman logs        follow the pick's logs
shortcut r podman exec -- ls -la
```

A number counts from the last `list` in the same terminal. Picks and lists are kept in `$XDG_STATE_HOME/shortcut-podman` (default `~/.local/state/shortcut-podman`).

## Shortcuts

`shortcut list --names podman` lists them, and `shortcut show podman <name>` shows one.

| Name | What |
|---|---|
| `ps`, `images`, `stats`, `pods`, `health` | What's there and how it's doing |
| `list`, `use`, `current` | Numbered list and the terminal's pick |
| `logs`, `sh`, `restart`, `ports`, `exec`, `stop` | Act on a container, the pick by default |
| `up`, `down`, `clogs` | `podman compose` in the current folder |
| `prune`, `nuke` | Clean up; both ask first |
