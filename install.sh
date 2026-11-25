#!/bin/sh
# Install zap as an app root: download the bundle for this platform, unpack it
# to ~/.apps, and link its entrypoint. Usage: install.sh [version]
set -eu

id=io.github.huyng.zap
tag=${1:-latest}

case $(uname -sm) in
"Linux x86_64") arch=linux-amd64 ;;
"Linux aarch64") arch=linux-arm64 ;;
"Darwin x86_64") arch=darwin-amd64 ;;
"Darwin arm64") arch=darwin-arm64 ;;
*) echo "zap: unsupported platform $(uname -sm)" >&2; exit 1 ;;
esac

if [ "$tag" = latest ]; then
	page="https://github.com/huyng/zap/releases/latest"
	base="$page/download"
else
	page="https://github.com/huyng/zap/releases/tag/$tag"
	base="https://github.com/huyng/zap/releases/download/$tag"
fi
archive=$id-$arch.tar.gz
apps=${APP_HOME:-$HOME/.apps}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Download, then refuse to unpack anything the release does not vouch for.
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/$archive.sha256sum" -o "$tmp/sums" ||
	{ echo "zap: $tag publishes no checksum for $archive" >&2; exit 1; }
sha() { { sha256sum "$1" 2>/dev/null || shasum -a 256 "$1"; } | cut -d' ' -f1; }
got=$(sha "$tmp/$archive")
[ "$(cut -d' ' -f1 "$tmp/sums")" = "$got" ] || { echo "zap: checksum mismatch" >&2; exit 1; }

# Unpack next to the others and link the entrypoint. A re-run replaces the tree
# and leaves data/, cache/ and run/ alone; `zap update` does the atomic swap.
mkdir -p "$apps" "$HOME/.local/bin"
tar xzf "$tmp/$archive" -C "$apps"

printf '[source]\nkind = "release"\nurl = "%s"\n\n[artifact]\nname = "%s"\nsha256 = "%s"\n\n[installer]\nname = "zap-install"\n' \
	"$page" "$archive" "$got" >"$apps/$id/receipt.toml"

ln -sf "$apps/$id/contents/bin/zap" "$HOME/.local/bin/zap"
echo "installed $id to $apps/$id"
