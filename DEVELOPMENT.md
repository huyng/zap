# Development notes

Below are helpful tips for working with the Zap repository and developing it.


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
make build      # a host binary, ./zap
make test       # go vet and go test
make release    # one bundle archive per platform, with checksums, in dist/
```

`make bundle` assembles `build/<os>-<arch>/io.github.huyng.zap/` — an `app.toml`
beside `contents/bin/zap` — and packs each one with `zap bundle`. The archives in
`dist/` are what `install.sh` downloads and what `zap install <release-url>`
installs. `VERSION` defaults to `git describe`; override it with
`make release VERSION=v0.1.0`.

Requires Go 1.25 or later.

## Releasing

Push a tag and the workflow does the rest:

```
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` runs the tests, builds the bundles for all four
platforms, checks that each unpacked archive really is a bundle root with no
`receipt.toml`, and publishes a GitHub release with the archives and their
checksums attached. Re-running a failed job finishes the release rather than
erroring on the tag already existing.

To release without CI, run `make release VERSION=v0.1.0` and upload
`dist/*.tar.gz` and `dist/*.sha256sum` to a release yourself. The checksums are
not optional: `install.sh` verifies the archive against its published
`.sha256sum`, and a release without one cannot be installed.
