#!/bin/sh
# Assembles the project page (decisions.md P0-09) into a static directory:
# site/ plus the brand files and demo GIF it uses, which stay in docs/.
# Usage: sh site/build.sh [OUT_DIR]   (default: _site)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:-"$root/_site"}

rm -rf "$out"
mkdir -p "$out/brand" "$out/fonts"

cp "$root/site/index.html" "$root/site/style.css" "$out/"
cp "$root/site/fonts/"* "$out/fonts/"
for f in igris-logo-dark.svg igris-mark-dark.svg igris-mark-dark-512.png igris-icon-180.png favicon.ico; do
  cp "$root/docs/brand/$f" "$out/brand/"
done
cp "$root/docs/demo/igris-demo.gif" "$root/docs/demo/igris-home.png" "$root/docs/demo/igris-home-narrow.png" "$out/"
: > "$out/.nojekyll"

echo "site built in $out"
