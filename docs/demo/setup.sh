#!/bin/sh
# Creates the scratch project a tape records (see README.md here) and prints
# its path. Each run gets a new directory, so nothing is left from an earlier
# recording.
#
#   setup.sh        the demo plan (tasks.md, igris.toml here) for demo.tape
#   setup.sh home   igris's example plan, partly done, for home.tape
set -eu

here=$(cd "$(dirname "$0")" && pwd)

command -v igris >/dev/null || { echo "igris not found on PATH: run make install" >&2; exit 1; }

kind=${1:-demo}
case "$kind" in
demo) name=demo ;;
home) name=hello-cli ;;
*)
  echo "usage: setup.sh [demo|home]" >&2
  exit 2
  ;;
esac

# The project folder's name is what igris shows as the project.
dir=$(mktemp -d "${TMPDIR:-/tmp}/igris-demo.XXXXXX")/$name
mkdir "$dir"
cd "$dir"
git init -q

case "$kind" in
demo)
  cp "$here/tasks.md" "$here/igris.toml" .
  igris init >/dev/null
  ;;
home)
  igris init --example >/dev/null
  # P1-01 and the license are done, so the next two tasks are ready.
  sed -e 's/^\(| P1-01 .*\)| ready |/\1| done |/' \
    -e 's/^\(| P1-04 .*\)| ready |/\1| done |/' \
    -e 's/^\(| P1-0[23] .*\)| blocked |/\1| ready |/' tasks.md > tasks.md.new
  mv tasks.md.new tasks.md
  ;;
esac

git add -A
git -c user.name=igris-demo -c user.email=demo@igris.invalid commit -qm "demo plan"

echo "$dir"
