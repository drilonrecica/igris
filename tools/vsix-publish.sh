#!/bin/sh
# make vsix-publish TAG=vX.Y.Z [DRY=1] (decisions.md V19-P1): the owner's step.
# Runs vsix-check first (refuses when the .vsix rebuilt from the tag differs
# from CI's), then publishes bin/igris-X.Y.Z.vsix to the Marketplace (vsce)
# and Open VSX (ovsx). VSCE_PAT and OVSX_PAT come from the environment only;
# vsce and ovsx read them from there themselves, so a token is never on a
# command line, in a file or in this script's output. DRY=1 prints the two
# commands with the tokens masked and publishes nothing.
set -eu
tag=${1:?usage: sh tools/vsix-publish.sh vX.Y.Z}
dry=${2:-}
case $tag in
v[0-9]*.[0-9]*.[0-9]*) ;;
*) echo "TAG must look like v1.9.0, got $tag"; exit 1 ;;
esac
version=${tag#v}
root=$(git rev-parse --show-toplevel)
pkg="bin/igris-$version.vsix"
bindir="$root/editors/vscode/node_modules/.bin"

sh "$root/tools/vsix-check.sh" "$tag"

if [ -z "$dry" ]; then
	[ -n "${VSCE_PAT:-}" ] || { echo "VSCE_PAT is not set: export the Marketplace token, then run again"; exit 1; }
	[ -n "${OVSX_PAT:-}" ] || { echo "OVSX_PAT is not set: export the Open VSX token, then run again"; exit 1; }
	[ -f "$root/$pkg" ] || { echo "$pkg is missing: run make vsix at $tag first"; exit 1; }
	[ -x "$bindir/vsce" ] && [ -x "$bindir/ovsx" ] || { echo "vsce/ovsx not installed: run make vscode-deps"; exit 1; }
	cd "$root"
	"$bindir/vsce" publish --packagePath "$pkg"
	"$bindir/ovsx" publish "$pkg"
	echo "published $pkg to the Marketplace and Open VSX"
	exit 0
fi

echo "DRY: would run (tokens masked, nothing published):"
echo "VSCE_PAT=*** vsce publish --packagePath $pkg"
echo "OVSX_PAT=*** ovsx publish $pkg"
