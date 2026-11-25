#!/bin/sh
# install.sh — install zap as a conforming app root.
#
# Downloads the release bundle for this platform, checks its checksum, and
# unpacks it to $APP_HOME or ~/.apps, exposing `zap` in ~/.local/bin. The result
# is an app root of the App Bundle Layout Specification: one directory, with a
# receipt.toml so `zap update io.github.huyng.zap` manages this install.
#
# Usage: install.sh [version]
set -eu

APP_ID=io.github.huyng.zap
REPO=huyng/zap
ENTRYPOINT=bin/zap

version=${1:-latest}

case $version in
-h | --help)
	printf 'Usage: %s [version]\n\n' "$0"
	printf 'Installs zap, or the release tagged <version>, from github.com/%s.\n' "$REPO"
	exit 0
	;;
esac

die() {
	printf 'zap: %s\n' "$1" >&2
	exit 1
}

# --- platform ---------------------------------------------------------------

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported operating system: $(uname -s)" ;;
esac

case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

asset=$APP_ID-$os-$arch.tar.gz

if [ "$version" = latest ]; then
	base=https://github.com/$REPO/releases/latest/download
	receipt_url=https://github.com/$REPO/releases/latest
	receipt_ref=
else
	base=https://github.com/$REPO/releases/download/$version
	receipt_url=https://github.com/$REPO/releases/tag/$version
	receipt_ref=$version
fi

# --- download and verify ----------------------------------------------------

command -v curl >/dev/null 2>&1 || die "need curl to download zap"

work=$(mktemp -d "${TMPDIR:-/tmp}/zap-install.XXXXXX")
staging=
trap 'rm -rf "$work" ${staging:+"$staging"}' EXIT HUP INT TERM

printf 'downloading %s\n' "$asset"
curl -fsSL "$base/$asset" -o "$work/$asset" || die "no $asset in $version"

# A release without a checksum is not installed: every zap release publishes one.
curl -fsSL "$base/$asset.sha256sum" -o "$work/sums" ||
	die "no checksum published for $asset; refusing to install unverified"

want=$(cut -d' ' -f1 "$work/sums")
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$work/$asset" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$work/$asset" | cut -d' ' -f1)
fi
[ "$want" = "$got" ] || die "$asset does not match the published checksum"

# --- unpack and check (§6) --------------------------------------------------

apps_dir=${APP_HOME:-$HOME/.apps}
bin_dir=$HOME/.local/bin
target=$apps_dir/$APP_ID

# Check the exposure point first, so a conflict changes nothing.
link=$bin_dir/zap
if [ -e "$link" ] && [ ! -L "$link" ]; then
	die "$link is not a symlink; move it aside and run this again"
fi

mkdir -p "$apps_dir"
staging=$(mktemp -d "$apps_dir/.install-XXXXXX")
tar -xzf "$work/$asset" -C "$staging"

staged=$staging/$APP_ID
[ -f "$staged/app.toml" ] || die "the archive holds no $APP_ID/app.toml"
[ -x "$staged/contents/$ENTRYPOINT" ] || die "the archive holds no executable $ENTRYPOINT"

# --- swap into place (§7.1), keeping mutable directories (§7.2) -------------

action=installed

if [ -d "$target" ]; then
	old=$(mktemp -d "$apps_dir/.install-old-XXXXXX")
	mv "$target" "$old/$APP_ID"
	if ! mv "$staged" "$target"; then
		mv "$old/$APP_ID" "$target"
		die "could not move the new tree into place; the previous install is untouched"
	fi
	for dir in data cache run; do
		[ -d "$old/$APP_ID/$dir" ] && mv "$old/$APP_ID/$dir" "$target/$dir"
	done
	rm -rf "$old"
	action=upgraded
else
	mv "$staged" "$target"
fi

chmod 0700 "$target"

# --- receipt (§6.1) ---------------------------------------------------------

cat >"$target/receipt.toml" <<EOF
[source]
  kind = "release"
  url = "$receipt_url"
EOF
[ -n "$receipt_ref" ] && printf '  ref = "%s"\n' "$receipt_ref" >>"$target/receipt.toml"
cat >>"$target/receipt.toml" <<EOF

[artifact]
  name = "$asset"
  sha256 = "$got"

[installer]
  name = "zap-install"
EOF

# --- expose the entrypoint (§6.1) -------------------------------------------

mkdir -p "$bin_dir"
rm -f "$link"
ln -s "$target/contents/$ENTRYPOINT" "$link"

# --- report -----------------------------------------------------------------

printf '%s %s\n' "$action" "$APP_ID"
printf '  root: %s\n' "$target"
printf '  from: %s\n' "$receipt_url"
printf '  link: %s\n' "$link"
case ":$PATH:" in
*":$bin_dir:"*) ;;
*) printf '  note: %s is not on your PATH\n' "$bin_dir" ;;
esac
