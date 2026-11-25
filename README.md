# Zap

Zap is a cross platform package manager written in Go for installing self-contained apps laid out as described by the [App Bundle File System Layout Specification](../app-bundle-spec/spec-1.1.md).

Goals:

* We want cross-platform and cross-distro packaging that just works.
* We want to make the process of building a package as simple as uploading a github repo.
* We want to encourage the proliferation of self-contained apps. Files should not be sprayed across the file-system heirarchy.
* We want to replace `curl install.sh | sh` with a more formalized package management system that just works across all distros and platforms.

## Install

`zap` needs to be with this script.

```
curl -fsSL https://raw.githubusercontent.com/huyng/zap/main/install.sh | sh
```

This downloads and unpack's the latest bundle package for zap into `~/.apps/io.github.huyng.zap` and creates a symlink for `~/.apps/io.github.huyng.zap/contents/bin/zap` in `~/.local/bin`

## Usage

```
# installs a bundle
zap install <archive|github-url>

# show all installed app bundles
zap list

# update a bundle or all bundles
zap update [<id>...] | --all

# remove an app bundle
zap remove <id>

# create a bundle from <dir> for release
zap bundle <dir>
```

### Where things go

Zap installs bundle packages into one of two locations: `~/.apps` for the default user-scoped installations, and `/opt/apps` for system-scoped installations.

Upon installation, if the bundle package contains an entrypoint, `zap` automatically create symlinks for the bundle's entrypoint into `~/.local/bin` and into `/usr/local/bin` for systemwide installation.

| Scope | App roots | Linked executables |
| --- | --- | --- |
| user (default) | `$APP_HOME` or `~/.apps` | `~/.local/bin` |
| system (`--system`) | `/opt/apps` | `/usr/local/bin` |

### What can be installed

From a simple git repo to pre-packaged binary distribution bundles, zap provides a smooth onramp for installing all of these project shapes:

- **A GitHub repository** - Any git repo can be installed. The head of the default branch is downloaded, wrapped into a bundle and installed.

  ```
  zap install https://github.com/foo/bar
  ```

- **A GitHub release** - the platform asset is downloaded, wrapped into a bundle
  and installed:

  ```
  zap install https://github.com/neovim/neovim/releases/tag/v0.12.2
  ```

- **A bundle archive** - Any `.tar.gz`, `.tgz` or `.zip` whose root conforms to the App Bundle File System Layout (holds exactly
  one `<id>/` directory containing `app.toml` and `contents/`.

  ```
  # install a pre-packaged bundle from local filesystem
  zap install example.tar.gz

  # install a pre-packed bundle from website
  zap install http://example.com/foo/bar.tar.gz
  ```


### Removing bundles

`zap remove <id>`

The command above deletes the app located at `<id>` but retains user data inside its `data/` folder.

`zap remove --purge <id>`

The command above deletes everything including user data.

### Updating installed bundles

Bundles installed via github releases or github urls may be updated to the latest release or url using:

`zap update <id>`

example:

```
zap update io.github.huyng.zap
```

All updatable bundles can be updated with the following:

```
zap update --all
```

A Github `release` source updates to the repository's latest release.  A Github `repo` source updates to the latest commit of the default branch.

### Publishing a bundle

`zap bundle <dir>` packs a directory tree into an archive you can publish as a release
asset, and that `zap install` can unpack and install.

The directory tree may take any of the following forms:

Either a bundle root (`app.toml` beside `contents/`) or a flat source tree (`app.toml` beside the
files). Zap will automatically move `app.toml` to the right location when generating the bundle.

 ```
zap bundle build/io.github.huyng.zap --out dist
  archive: dist/io.github.huyng.zap-linux-arm64.tar.gz
```

It is the project's repsonsibility to compile the code and to author `app.toml`. `zap bundle` packs it:

```make
ID := io.github.huyng.zap

bundle:
	GOOS=linux GOARCH=arm64 go build -trimpath -o build/$(ID)/contents/bin/zap .
	cp app.toml build/$(ID)/app.toml
	zap bundle build/$(ID) --out dist --os linux --arch arm64
```
## Further documentation

* [Homepage & Docs](https://huyng.github.io/zap)
* [DEVELOPMENT.md](DEVELOPMENT.md)