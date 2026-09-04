#!/bin/sh
# Cut a release: bump the version everywhere it is pinned, tag, push, and open
# the GitHub release. CI then attaches the binaries and points the formula at
# the new tarball, so nothing is left to edit by hand.
#
#   mise run release 0.14.0
#   mise run release 0.14.0 --dry-run
#   mise run release 0.14.0 -n notes.md

set -eu

die() {
	echo "release: $*" >&2
	exit 1
}

usage() {
	cat >&2 <<'EOF'
usage: scripts/release.sh [-n FILE] [--dry-run] VERSION

  VERSION      the new version, without a leading v, e.g. 0.14.0
  -n, --notes  read the release notes from FILE instead of opening an editor
  --dry-run    run every check and show the changes, then undo them
EOF
	exit 2
}

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

version=
notes_file=
dry_run=
while [ $# -gt 0 ]; do
	case $1 in
	-n | --notes | --notes-file)
		[ $# -ge 2 ] || usage
		notes_file=$2
		shift
		;;
	--dry-run) dry_run=1 ;;
	-h | --help) usage ;;
	-*) die "unknown option: $1" ;;
	*)
		[ -z "$version" ] || usage
		version=$1
		;;
	esac
	shift
done

printf '%s' "${version:-}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
	die "expected a version like 0.14.0, got '${version:-}'"
tag=v$version
[ -z "$notes_file" ] || [ -r "$notes_file" ] || die "cannot read $notes_file"

for tool in git gh curl; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

echo "==> Checking the tree"
[ -z "$(git status --porcelain)" ] || die "working tree is not clean"
branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = main ] || die "on branch $branch; releases are cut from main"
git fetch -q origin main --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
	die "main and origin/main have diverged; pull or push first"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	die "$tag already exists"
fi
current=$(sed -n 's|^var version = "\(.*\)"$|\1|p' main.go)
[ "$current" != "$version" ] || die "main.go is already at $version"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated"

echo "==> Running the tests"
go test ./...
go vet ./...
[ -z "$(gofmt -l .)" ] || {
	gofmt -l .
	die "gofmt found unformatted files"
}

echo "==> Bumping $current to $version"
bump() { # file, sed expression
	sed "$2" "$1" >"$1.new" && mv "$1.new" "$1"
}
bump main.go "s|^var version = \".*\"$|var version = \"$version\"|"
bump PKGBUILD "s|^pkgver=.*|pkgver=$version|"
# Undo the bumps if anything below fails, so a botched run leaves no mess.
trap 'git checkout -q -- main.go PKGBUILD .SRCINFO 2>/dev/null || true' EXIT INT TERM
grep -q "^var version = \"$version\"\$" main.go || die "failed to bump main.go"
grep -q "^pkgver=$version\$" PKGBUILD || die "failed to bump PKGBUILD"
scripts/sync-packaging.sh "$tag"

notes=$(mktemp)
cleanup_notes=$notes
trap 'rm -f "$cleanup_notes"; git checkout -q -- main.go PKGBUILD .SRCINFO 2>/dev/null || true' EXIT INT TERM
if [ -n "$notes_file" ]; then
	cat "$notes_file" >"$notes"
else
	previous=$(git describe --tags --abbrev=0 2>/dev/null || true)
	{
		echo "### Changes"
		echo
		if [ -n "$previous" ]; then
			git log --reverse --format='- %s' "$previous..HEAD"
		fi
	} >"$notes"
	if [ -z "$dry_run" ]; then
		echo "==> Editing the release notes${previous:+ (prefilled from $previous..HEAD)}"
		"${VISUAL:-${EDITOR:-vi}}" "$notes"
	fi
fi
grep -q '[^[:space:]]' "$notes" || die "the release notes are empty"

if [ -n "$dry_run" ]; then
	echo
	echo "==> Would commit"
	git --no-pager diff --stat -- main.go PKGBUILD .SRCINFO
	git --no-pager diff -- main.go PKGBUILD .SRCINFO
	echo "==> Would release $tag with notes"
	sed 's|^|    |' "$notes"
	echo
	echo "==> Would then run"
	echo "    git commit -m 'Release $tag' main.go PKGBUILD .SRCINFO"
	echo "    git tag -a $tag -m 'Release $tag'"
	echo "    git push origin main $tag"
	echo "    gh release create $tag --title $tag --notes-file -"
	echo
	echo "Dry run, so nothing was changed. The version bumps have been undone."
	exit 0
fi

echo "==> Committing and tagging"
git commit -q -m "Release $tag" main.go PKGBUILD .SRCINFO
git tag -a "$tag" -m "Release $tag"
# Past this point the bumps are committed, so the revert trap must not fire.
trap 'rm -f "$cleanup_notes"' EXIT INT TERM

echo "==> Pushing"
git push -q origin main "$tag"

echo "==> Creating the release"
# The tag push starts CI, which creates the release if it beats us to it.
if ! gh release create "$tag" --title "$tag" --notes-file "$notes" 2>/dev/null; then
	gh release edit "$tag" --notes-file "$notes" >/dev/null
fi
gh release view "$tag" --json url --jq .url

echo
echo "CI is building the binaries and will commit the formula bump to main."
echo "Watch it with: gh run watch --exit-status"
echo "Then: git pull   # to pick up the formula commit"
