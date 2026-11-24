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
zap list [--origin]
zap update [<id>...] | --all
zap remove <id>
zap bundle <dir>
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
name to the repository name. A tree that ships its own `app.toml` overrides
both with the identity its author declared — that id is the stable one, so a
rename or transfer of the repository does not move an installed app root.
`--id` and `--name` override everything, for when you want a different name
locally.

Release downloads are checked against the digest the release publishes: the
`digest` GitHub computes for the asset, or failing that a checksum file beside
it (`foo.tar.gz.sha256sum`) or one covering the whole release (`SHA256SUMS`).
A mismatch aborts before anything is installed. A release that publishes no
digest is installed unverified, and `zap` says so rather than implying a check
it did not make.

`zap` never guesses an entrypoint. For releases and source trees it uses the one
declared by an `app.toml` at the tree's root, if there is one. A tree with no
manifest, or one that names no entrypoint, is installed as a payload with
nothing exposed on your `PATH`.

### Where things go

| Scope | App roots | Linked executables |
| --- | --- | --- |
| user (default) | `$APP_HOME` or `~/.apps` | `~/.local/bin` |
| system (`--system`) | `/opt/apps` | `/usr/local/bin` |

`--apps-dir` overrides the directory holding app roots. Inside a user scope the
entrypoint is a symlink into the app root; at system scope it is a small wrapper
script that sets `APP_ROOT`, because a system install has no default base to
compute paths from.

### Removing

`zap remove <id>` deletes `app.toml`, `receipt.toml`, `contents/`, `cache/` and
`run/`, and takes away the link or wrapper that `zap` installed. `data/` is
kept, so a later install of the same id finds its data again. Pass `--purge` to
delete `data/` as well.

Two safety valves: `--force` replaces an existing install (and its `data/`) or
deletes a directory that has no readable `app.toml`; and an existing link is
only removed when it really points into the root being removed.

### Knowing where an app came from

Every install writes `receipt.toml` beside `app.toml`: where the bundle came
from, the digest of what was downloaded, and which `zap` installed it. It is
never shipped in a bundle, is never read by the app, and is deleted with the
installation, so it describes an install rather than an app.

```toml
[source]
kind = "release"
url = "https://github.com/neovim/neovim/releases/tag/v0.12.2"
ref = "v0.12.2"

[artifact]
name = "nvim-linux-x86_64.tar.gz"
sha256 = "9f2c…"
fetched_at = "2025-11-24T09:14:02Z"

[installer]
name = "zap"
version = "0.1.0"
at = "2025-11-24T09:14:03Z"
```

`zap list --origin` shows it as a column.

### Updating

`zap update <id>` re-fetches whatever the receipt names and swaps in the new
tree, keeping `data/`, `cache/` and `run/` untouched:

```
zap update io.github.huyng.bashmarks
zap update --all
zap update --check --all
```

A `release` source follows the repository's latest release; a `repo` source
follows the default branch; an `archive` source re-reads the same file. Since
version strings carry no ordering, `update` reports that the source offers
something *different*, not something newer, and `--check` says what it would do
without installing.

A source that now builds a different id is refused rather than installed over
the existing app. An app with no receipt, or one whose archive has been deleted,
cannot be updated: reinstall it to record where it came from.

### Publishing a bundle

`zap bundle <dir>` packs a tree into an archive you can publish as a release
asset, and `zap install` unpacks it. The tree is either a bundle root
(`app.toml` beside `contents/`) or a flat source tree (`app.toml` beside the
files). A flat tree gets the one transform the spec allows: the manifest moves
up, everything else moves down into `contents/`.

```
zap bundle build/io.github.huyng.zap --out dist
  archive: dist/io.github.huyng.zap-linux-arm64.tar.gz
```

The archive always holds a single `<id>/` directory named from the manifest, so
the directory you pass can be called anything. `--os` and `--arch` default to
the machine you are on, and name the archive so `zap install <release-url>` can
find it: the asset name is what picks a platform. Archives are reproducible, so
the same tree always gives the same bytes.

Compiling stays the project's job. A four-line make target assembles the tree
and `zap bundle` packs it:

```make
ID := io.github.huyng.zap

bundle:
	GOOS=linux GOARCH=arm64 go build -trimpath -o build/$(ID)/contents/bin/zap .
	cp app.toml build/$(ID)/app.toml
	zap bundle build/$(ID) --out dist --os linux --arch arm64
```

## Layout of this repository

```
main.go              entrypoint
cmd/                 command line: flags, scope defaults, command wiring
internal/bundle/     reads, validates and verifies app.toml and bundle roots
internal/github/     parses GitHub URLs, picks release assets, resolves heads
internal/install/    install, wrap, extract, promote, expose and remove
internal/pack/       packs an app tree into a bundle archive
internal/scope/      where app roots and links live, and listing them
```

Configuration reaches the core as arguments: `install.Installer`,
`install.Remover`, `pack.Options` and `scope.Scope` never read the environment.
Only `cmd/` resolves `$APP_HOME`, the home directory, the platform tokens and
the flag defaults.

## Building

```
go build -o zap .
go test ./...
```

Requires Go 1.25 or later. The `zap` file in this folder is a previous build
output for macOS.
