#!/bin/sh
# Assembles the project page (decisions.md P0-09) into a static directory:
# site/ plus the brand files and demo GIF it uses, which stay in docs/, and the
# docs pages that tools/sitedocs renders from markdown (site/docs/ and docs/).
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
cp "$root/docs/demo/igris-demo.gif" "$root/docs/demo/igris-home.png" "$root/docs/demo/igris-home-narrow.png" \
  "$root/docs/demo/igris-run.png" "$root/docs/demo/igris-phone.png" "$out/"

# The version pill shows the latest stable tag, so it can't go stale; the
# checked-in text is the fallback for a checkout without tags.
tag=$(git -C "$root" tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -v -- '-' | head -n 1 || true)
if [ -n "$tag" ]; then
  sed "s|\(class=\"pill pill-version\"[^>]*>\)v[0-9][0-9.]*<|\1$tag<|" "$out/index.html" > "$out/index.html.tmp"
  mv "$out/index.html.tmp" "$out/index.html"
fi
: > "$out/.nojekyll"

(cd "$root" && go run ./tools/sitedocs -root . -out "$out/docs")

echo "site built in $out"
