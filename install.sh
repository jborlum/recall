#!/bin/sh
# Install or update recall from a GitHub release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/jborlum/recall/main/install.sh | sh
#
# Honours RECALL_VERSION (default: latest release) and PREFIX (default:
# ~/.local/bin). Pass -f to reinstall a version that is already present.

set -eu

repo=https://github.com/jborlum/recall
prefix=${PREFIX:-$HOME/.local/bin}
force=

for arg in "$@"; do
	case $arg in
	-f | --force) force=1 ;;
	*)
		echo "install.sh: unknown argument: $arg" >&2
		exit 2
		;;
	esac
done

os=$(uname -s)
arch=$(uname -m)
case "$os/$arch" in
Linux/x86_64) ;;
Darwin/*)
	echo "install.sh: on macOS, use the tap instead:" >&2
	echo "  brew tap jborlum/recall $repo.git" >&2
	echo "  brew install jborlum/recall/recall" >&2
	exit 1
	;;
*)
	echo "install.sh: no release binary for $os/$arch." >&2
	echo "Build from source: $repo#from-source-without-a-package-manager" >&2
	exit 1
	;;
esac

for tool in curl install uname; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "install.sh: $tool is required" >&2
		exit 1
	}
done

if command -v sha256sum >/dev/null 2>&1; then
	checksum="sha256sum -c --ignore-missing"
elif command -v shasum >/dev/null 2>&1; then
	checksum="shasum -a 256 -c --ignore-missing"
else
	echo "install.sh: neither sha256sum nor shasum is available" >&2
	exit 1
fi

tag=${RECALL_VERSION:-}
if [ -z "$tag" ]; then
	# The /releases/latest redirect names the tag without spending an
	# unauthenticated API call.
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
		{
			echo "install.sh: could not reach $repo" >&2
			exit 1
		}
	tag=${latest##*/}
fi
case $tag in
v*) ;;
*) tag=v$tag ;;
esac

if [ -z "$force" ] && [ -x "$prefix/recall" ] &&
	[ "v$("$prefix/recall" --version 2>/dev/null)" = "$tag" ]; then
	echo "recall $tag is already installed in $prefix. Pass -f to reinstall."
	exit 0
fi

asset=recall_${tag}_linux_amd64
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading recall $tag..."
curl -fsSL -o "$tmp/$asset" "$repo/releases/download/$tag/$asset" ||
	{
		echo "install.sh: no $asset published for $tag" >&2
		exit 1
	}
curl -fsSL -o "$tmp/SHA256SUMS" "$repo/releases/download/$tag/SHA256SUMS" ||
	{
		echo "install.sh: no SHA256SUMS published for $tag" >&2
		exit 1
	}

(cd "$tmp" && $checksum SHA256SUMS >/dev/null) ||
	{
		echo "install.sh: checksum mismatch for $asset" >&2
		exit 1
	}

# Land the binary with a rename so replacing a running recall cannot fail.
mkdir -p "$prefix"
install -m755 "$tmp/$asset" "$prefix/.recall.new"
mv -f "$prefix/.recall.new" "$prefix/recall"
echo "Installed recall $tag to $prefix/recall"

case ":$PATH:" in
*":$prefix:"*) ;;
*) echo "Note: $prefix is not on your PATH." >&2 ;;
esac
command -v fzf >/dev/null 2>&1 ||
	echo "Note: recall needs fzf for the picker; install it from your package manager." >&2
