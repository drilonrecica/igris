#!/bin/sh
# make vsix-check TAG=vX.Y.Z (decisions.md V19-P1): rebuilds the extension's
# .vsix from the tag in a scratch copy and compares its sha256 with the .vsix
# CI attached to the tag's GitHub release. Needs git, node/npm and gh; changes
# nothing in the working tree. `make vsix-publish` runs it first, with a
# directory as the second argument: on a match the checked .vsix is copied
# there, and that file is the one published.
set -eu
tag=${1:?usage: sh tools/vsix-check.sh vX.Y.Z [DIR]}
keep=${2:-}
root=$(git rev-parse --show-toplevel)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git -C "$root" rev-parse --verify --quiet "refs/tags/$tag^{commit}" >/dev/null || { echo "no tag $tag here"; exit 1; }
SOURCE_DATE_EPOCH=$(git -C "$root" log -1 --format=%ct "$tag")
export SOURCE_DATE_EPOCH

mkdir "$work/src"
git -C "$root" archive "$tag" editors/vscode | tar -x -C "$work/src"
(
	cd "$work/src/editors/vscode"
	npm ci --silent
	sh scripts/vsix.sh "$work/built" >/dev/null
)
mine=$(ls "$work"/built/*.vsix)
name=$(basename "$mine")

command -v gh >/dev/null || { echo "gh not found: https://cli.github.com"; exit 1; }
mkdir "$work/ci"
gh release download "$tag" --pattern "$name" --dir "$work/ci" || { echo "CI's $name is not attached to release $tag"; exit 1; }

sum() {
	if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
a=$(sum "$mine")
b=$(sum "$work/ci/$name")
echo "rebuilt  $a  $name"
echo "CI's     $b  $name"
[ "$a" = "$b" ] || { echo "the .vsix rebuilt from $tag differs from CI's: do not publish"; exit 1; }
echo "ok: the .vsix is reproducible from $tag"
[ -z "$keep" ] || cp "$work/ci/$name" "$keep/$name"
