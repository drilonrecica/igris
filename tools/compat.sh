#!/bin/sh
# Captures what an igris release wrote into testdata/compat/<tag>/, for the
# compatibility suite (SPEC §19, cmd/igris/compat_test.go): the release is
# built from its tag in a temporary worktree and run in a scratch project.
#
# Usage: sh tools/compat.sh vX.Y.Z    (make compat TAG=vX.Y.Z)
#
# Run once per release, after tagging; commit the result. It writes:
#   init/      igris.toml from `igris init`, and tasks.md: the plan
#              `init --example` wrote, or the release's examples/tasks.md
#              where init had no --example (v0.1.x)
#   examples/  the release's examples/igris.toml and examples/tasks.md
#   advanced/  the release's examples/advanced/, when it has one
#   templates/ the release's plan templates (examples/templates/<name>/tasks.md),
#              when it has them (1.2 on)
#   signals/   the files `igris done` and `igris skip` wrote
# state.json and runs.jsonl need a live session, so they are not captured
# here: testdata/compat/state/ and runs/ hold them, shaped after each
# release's structs.
set -eu

tag=${1:-}
case $tag in
v[0-9]*.[0-9]*.[0-9]*) ;;
*)
  echo "usage: sh tools/compat.sh vX.Y.Z" >&2
  exit 2
  ;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null || {
  echo "compat: no tag $tag" >&2
  exit 1
}
out=$root/testdata/compat/$tag
work=$(mktemp -d)
trap 'git worktree remove --force "$work/src" 2>/dev/null; rm -rf "$work"' EXIT

git worktree add -q --detach "$work/src" "$tag"
(cd "$work/src" && CGO_ENABLED=0 go build -o "$work/igris" ./cmd/igris)
igris=$work/igris

rm -rf "$out"
mkdir -p "$out/init" "$out/examples" "$out/signals"

# What `igris init` wrote.
mkdir "$work/proj"
cd "$work/proj"
if "$igris" init --example >/dev/null 2>&1; then :; else
  "$igris" init >/dev/null
  git -C "$root" show "$tag:examples/tasks.md" >tasks.md
fi
cp igris.toml tasks.md "$out/init/"

# The signal files a session's `igris done` and `igris skip` wrote.
"$igris" done P1-02 --note "compat capture" >/dev/null
"$igris" skip P1-03 --reason "compat capture" >/dev/null
cp .igris/signals/*.json "$out/signals/"
cd "$root"

# The examples the release shipped.
git show "$tag:examples/igris.toml" >"$out/examples/igris.toml"
git show "$tag:examples/tasks.md" >"$out/examples/tasks.md"
if git cat-file -e "$tag:examples/advanced" 2>/dev/null; then
  git archive "$tag" examples/advanced | tar -x -C "$work"
  cp -R "$work/examples/advanced" "$out/advanced"
fi

if git cat-file -e "$tag:examples/templates" 2>/dev/null; then
  git archive "$tag" examples/templates | tar -x -C "$work"
  cp -R "$work/examples/templates" "$out/templates"
fi

echo "captured $tag into testdata/compat/$tag"
