#!/bin/sh
# Install zap as an app root: download the latest release bundle for this
# platform, unpack it to ~/.apps, and link its entrypoint.
set -eu

id=io.github.huyng.zap

case $(uname -sm) in
"Linux x86_64") arch=linux-amd64 ;;
"Linux aarch64") arch=linux-arm64 ;;
"Darwin x86_64") arch=darwin-amd64 ;;
"Darwin arm64") arch=darwin-arm64 ;;
*) echo "zap: unsupported platform $(uname -sm)" >&2; exit 1 ;;
esac

# Following /releases/latest is what keeps this script simple: it always
# installs the newest release, and the receipt records that, so `zap update`
# keeps following the same pointer.
page="https://github.com/huyng/zap/releases/latest"
base="$page/download"
archive=$id-$arch.tar.gz
apps=${APP_HOME:-$HOME/.apps}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$base/$archive" -o "$tmp/$archive"

# Verify against the release's published checksum before unpacking anything.
curl -fsSL "$base/$archive.sha256sum" -o "$tmp/sums"
sha() { { sha256sum "$1" 2>/dev/null || shasum -a 256 "$1"; } | cut -d' ' -f1; }
got=$(sha "$tmp/$archive")
[ "$(cut -d' ' -f1 "$tmp/sums")" = "$got" ] ||
	{ echo "zap: $archive does not match its published checksum" >&2; exit 1; }

# Unpack next to the others and link the entrypoint. A re-run replaces the tree
# and leaves data/, cache/ and run/ alone; `zap update` does the atomic swap.
mkdir -p "$apps" "$HOME/.local/bin"
tar xzf "$tmp/$archive" -C "$apps"

printf '[source]\nkind = "release"\nurl = "%s"\n\n[artifact]\nname = "%s"\nsha256 = "%s"\n\n[installer]\nname = "zap-install"\n' \
	"$page" "$archive" "$got" >"$apps/$id/receipt.toml"

ln -sf "$apps/$id/contents/bin/zap" "$HOME/.local/bin/zap"
echo "installed $id to $apps/$id"
