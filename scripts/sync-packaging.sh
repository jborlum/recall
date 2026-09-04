#!/bin/sh
# Sync packaging metadata to a release tag.
#
#   scripts/sync-packaging.sh v0.14.0            # .SRCINFO only, offline
#   scripts/sync-packaging.sh v0.14.0 --formula  # also repoint the formula
#
# PKGBUILD is the source of truth for the Arch package, and .SRCINFO only
# restates it, so .SRCINFO is derived here rather than edited. The formula's
# sha256 cannot be known until the tag's tarball exists on GitHub, which is why
# CI runs the --formula pass after the tag is pushed rather than before.
#
# Both passes are idempotent: running them against an already-current tree
# changes nothing.

set -eu

repo=https://github.com/jborlum/recall

die() {
	echo "sync-packaging: $*" >&2
	exit 1
}

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

tag=
with_formula=
while [ $# -gt 0 ]; do
	case $1 in
	--formula) with_formula=1 ;;
	-*) die "unknown option: $1" ;;
	*)
		[ -z "$tag" ] || die "tag given twice"
		tag=$1
		;;
	esac
	shift
done

case $tag in
v[0-9]*) ;;
*) die "expected a tag like v0.14.0, got '${tag:-}'" ;;
esac
version=${tag#v}

# Fail loudly rather than writing a .SRCINFO that disagrees with its PKGBUILD.
pkgver=$(sed -n 's|^pkgver=\(.*\)$|\1|p' PKGBUILD)
[ -n "$pkgver" ] || die "no pkgver in PKGBUILD"
[ "$pkgver" = "$version" ] || die "PKGBUILD pkgver is $pkgver, expected $version"

write() { # file, sed expressions...
	file=$1
	shift
	sed "$@" "$file" >"$file.new" && mv "$file.new" "$file"
}

write .SRCINFO \
	-e "s|^\([[:space:]]*\)pkgver = .*|\1pkgver = $version|" \
	-e "s|^\([[:space:]]*source = .*\)#tag=v.*|\1#tag=v$version|"
grep -q "	pkgver = $version\$" .SRCINFO || die ".SRCINFO pkgver did not update"
grep -q "#tag=$tag\$" .SRCINFO || die ".SRCINFO source tag did not update"

[ -n "$with_formula" ] || exit 0

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	die "neither sha256sum nor shasum is available"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

url=$repo/archive/refs/tags/$tag.tar.gz
# GitHub generates these tarballs on demand, so allow for a short 404 window
# right after the tag lands.
attempt=1
until curl -fsSL -o "$tmp/src.tar.gz" "$url"; do
	[ "$attempt" -lt 5 ] || die "no tarball at $url after $attempt attempts"
	attempt=$((attempt + 1))
	sleep 5
done

sha=$(sha256 "$tmp/src.tar.gz")
write Formula/recall.rb \
	-e "s|^  url \".*\"$|  url \"$url\"|" \
	-e "s|^  sha256 \".*\"$|  sha256 \"$sha\"|"
grep -q "^  url \"$url\"\$" Formula/recall.rb || die "formula url did not update"
grep -q "^  sha256 \"$sha\"\$" Formula/recall.rb || die "formula sha256 did not update"

echo "Formula points at $tag ($sha)"
