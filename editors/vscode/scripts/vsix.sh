#!/bin/sh
# Builds igris-<version>.vsix (decisions.md V19-P1). Reproducible: the .vsix
# holds only dist/extension.js and the files .vscodeignore lets through, whose
# times are set to the commit's (SOURCE_DATE_EPOCH, else HEAD's) and modes
# normalized, so the same commit gives the same bytes on any machine
# (`make vsix-check`). Needs `npm ci` done. Usage: sh scripts/vsix.sh [OUTDIR]
set -eu
cd "$(dirname "$0")/.."
out=${1:-.}
epoch=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
rm -rf dist
node esbuild.mjs
for f in dist/extension.js package.json README.md CHANGELOG.md LICENSE resources/igris.svg; do
  chmod 644 "$f"
  touch -d "@$epoch" "$f"
done
touch -d "@$epoch" dist resources
mkdir -p "$out"
name=$(node -p "const p=require('./package.json'); p.name+'-'+p.version+'.vsix'")
rm -f "$out/$name"
npx --no-install vsce package --no-dependencies --out "$out/$name" >&2
node scripts/normalize-zip.mjs "$out/$name"
echo "$out/$name"
