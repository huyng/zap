# zap

A small package manager for self-contained apps laid out as described by the
[App Bundle Layout Specification](../app-bundle-spec/spec-1.1.md).

An app is one directory: an `app.toml` manifest and a `contents/` tree. To
install is to put that directory in place and link one executable; to remove is
to delete it. `zap` does exactly that, and nothing more: it runs no install
scripts and makes no changes outside the app root it owns.

## Install

```
zap install <archive|github-url>
zap list
zap remove <id>
```

### What can be installed

- **A bundle archive** — a `.tar.gz`, `.tgz` or `.zip` whose root holds exactly
  one `<id>/` directory containing `app.toml` and `contents/`. It is installed as
  it is.
- **A GitHub release** — the platform asset is downloaded, wrapped into a bundle
  and installed:

  ```
  zap install https://github.com/neovim/neovim/releases/tag/v0.12.2
  ```

- **A GitHub repository** — the head of the default branch is downloaded,
  wrapped into a bundle and installed. Useful for tools that cut no releases; the
  version recorded is the short commit hash:

  ```
  zap install https://github.com/huyng/bashmarks
  ```

For GitHub installs the bundle id defaults to `io.github.<owner>.<repo>` and the
name to the repository name. Override either with `--id` and `--name`.

`zap` never guesses an entrypoint. For releases and source trees it uses the one
declared by an `app.toml` at the tree's root, if there is one. A tree with no
manifest, or one that names no entrypoint, is installed as a payload with
nothing exposed on your `PATH`.

### Where things go

| Scope | App roots | Linked executables |
| --- | --- | --- |
| user (default) | `$APP_HOME` or `~/.apps` | `~/.local/bin` |
| system (`--system`) | `/opt/apps` | `/usr/local/bin` |

`--root` overrides the app root path. Inside a user scope the entrypoint is
a symlink into the app root; at system scope it is a small wrapper script that
sets `APP_ROOT`, because a system install has no default base to compute paths
from.

### Removing

`zap remove <id>` deletes `app.toml`, `contents/`, `cache/` and `run/`, and takes
away the link or wrapper that `zap` installed. `data/` is kept, so a later install
of the same id finds its data again. Pass `--purge` to delete `data/` as well.

Two safety valves: `--force` replaces an existing install (and its `data/`) or
deletes a directory that has no readable `app.toml`; and an existing link is
only removed when it really points into the root being removed.

## Layout of this repository

```
main.go              entrypoint
cmd/                 command line: flags, scope defaults, command wiring
internal/bundle/     reads, validates and verifies app.toml and bundle roots
internal/github/     parses GitHub URLs, picks release assets, resolves heads
internal/install/    install, wrap, extract, promote, expose and remove
internal/scope/      where app roots and links live, and listing them
```

Configuration reaches the core as arguments: `install.Installer`,
`install.Remover` and `scope.Scope` never read the environment. Only `cmd/`
resolves `$APP_HOME`, the home directory and the flag defaults.

## Building

```
go build -o zap .
go test ./...
```

Requires Go 1.25 or later. The `zap` file in this folder is a previous build
output for macOS.
