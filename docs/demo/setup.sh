#!/bin/sh
# Creates the scratch project the demo records (see README.md here) and
# prints its path. Each run gets a new directory, so Claude Code always asks
# to trust it and demo.tape can answer that prompt the same way every time.
set -eu

here=$(cd "$(dirname "$0")" && pwd)

command -v igris >/dev/null || { echo "igris not found on PATH: run make install" >&2; exit 1; }

dir=$(mktemp -d "${TMPDIR:-/tmp}/igris-demo.XXXXXX")
cp "$here/tasks.md" "$here/igris.toml" "$dir/"

cd "$dir"
git init -q
igris init >/dev/null
git add -A
git -c user.name=igris-demo -c user.email=demo@igris.invalid commit -qm "demo plan"

echo "$dir"
