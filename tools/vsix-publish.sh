#!/bin/sh
# make vsix-publish TAG=vX.Y.Z [DRY=1] (decisions.md V19-P1): the owner's step.
# Runs vsix-check first (refuses when the .vsix rebuilt from the tag differs
# from CI's) and publishes the very .vsix it checked to the Marketplace
# (vsce) and Open VSX (ovsx), never another build lying in bin/.
# VSCE_PAT and OVSX_PAT come from the environment only and leave it at once:
# the check runs npm ci and the build, whose packages' scripts never see
# them; each goes back into the environment of its own publish command
# alone, so a token is never on a command line, in a file or in this
# script's output. DRY=1 prints the two commands with the tokens masked and
# publishes nothing.
set -eu
tag=${1:?usage: sh tools/vsix-publish.sh vX.Y.Z}
dry=${2:-}
case $tag in
v[0-9]*.[0-9]*.[0-9]*) ;;
*) echo "TAG must look like v1.9.0, got $tag"; exit 1 ;;
esac
vsce_pat=${VSCE_PAT:-}
ovsx_pat=${OVSX_PAT:-}
unset VSCE_PAT OVSX_PAT
version=${tag#v}
root=$(git rev-parse --show-toplevel)
bindir="$root/editors/vscode/node_modules/.bin"

if [ -z "$dry" ]; then
	[ -n "$vsce_pat" ] || { echo "VSCE_PAT is not set: export the Marketplace token, then run again"; exit 1; }
	[ -n "$ovsx_pat" ] || { echo "OVSX_PAT is not set: export the Open VSX token, then run again"; exit 1; }
	[ -x "$bindir/vsce" ] && [ -x "$bindir/ovsx" ] || { echo "vsce/ovsx not installed: run make vscode-deps"; exit 1; }
fi

checked=$(mktemp -d)
trap 'rm -rf "$checked"' EXIT
sh "$root/tools/vsix-check.sh" "$tag" "$checked"
pkg="$checked/igris-$version.vsix"
[ -f "$pkg" ] || { echo "vsix-check kept no igris-$version.vsix: do not publish"; exit 1; }

if [ -z "$dry" ]; then
	cd "$root"
	VSCE_PAT=$vsce_pat "$bindir/vsce" publish --packagePath "$pkg"
	OVSX_PAT=$ovsx_pat "$bindir/ovsx" publish "$pkg"
	echo "published igris-$version.vsix (the one checked against CI's) to the Marketplace and Open VSX"
	exit 0
fi

echo "DRY: would run (tokens masked, nothing published), with the .vsix just checked:"
echo "VSCE_PAT=*** vsce publish --packagePath igris-$version.vsix"
echo "OVSX_PAT=*** ovsx publish igris-$version.vsix"
