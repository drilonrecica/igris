#!/bin/sh
# Tags, builds and signs a release with the owner's release key (SPEC §18,
# decisions.md V06-P1). Run by the owner only: the key's passphrase is never
# typed anywhere else. Nothing is pushed or published here.
#
# Usage: sh tools/sign.sh X.Y.Z    (make sign VERSION=X.Y.Z)
#        sh tools/sign.sh --check  (make sign-check: sign and verify a test
#                                   file; no tag, no build)
# KEY is the private key (default ~/.ssh/igris-release); KEY.pub must be
# next to it and listed in allowed_signers.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
key=${KEY:-"$HOME/.ssh/igris-release"}
signers=allowed_signers

die() {
  echo "sign: $*" >&2
  exit 1
}

# The key and its public half, and the public half must be the one in
# allowed_signers: a release signed with any other key wouldn't verify.
[ -f "$key" ] || die "no release key at $key: make one with ssh-keygen -t ed25519 -C \"igris release\" -f $key (or set KEY=)"
[ -f "$key.pub" ] || die "no $key.pub next to the key: ssh-keygen -y -f $key > $key.pub"
[ -f "$signers" ] || die "no $signers in $root"
pub=$(awk '{print $1, $2}' "$key.pub")
grep -v '^#' "$signers" | grep -qF "$pub" || die "$key.pub is not in $signers: add it there (and to the README) first"

verify_file() { # file sig
  ssh-keygen -Y verify -f "$signers" -I igris -n file -s "$2" < "$1"
}

if [ "${1:-}" = "--check" ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  echo "igris release key check" > "$tmp/check.txt"
  ssh-keygen -Y sign -f "$key" -n file "$tmp/check.txt"
  verify_file "$tmp/check.txt" "$tmp/check.txt.sig"
  echo "sign: the key signs, and allowed_signers verifies it"
  exit 0
fi

version=${1:-}
echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$' || die "VERSION must be X.Y.Z or X.Y.Z-rc.N (no v), got \"$version\": make sign VERSION=0.6.0"
tag=v$version

# The release commit: clean, on master and already pushed, so the tag the
# agent pushes later points at a commit everyone has.
[ -z "$(git status --porcelain)" ] || die "the working tree has changes: commit or stash them"
[ "$(git rev-parse --abbrev-ref HEAD)" = master ] || die "not on master"
git fetch -q origin master
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/master)" ] || die "HEAD is not origin/master: push the release commit (or pull) first"
! git rev-parse -q --verify "refs/tags/$tag" >/dev/null || die "tag $tag exists here already"
[ -z "$(git ls-remote --tags origin "refs/tags/$tag")" ] || die "tag $tag exists on origin already"
grep -qF "## [$version]" CHANGELOG.md || die "CHANGELOG.md has no \"## [$version]\" section"
command -v goreleaser >/dev/null || die "goreleaser not found: https://goreleaser.com/install/"
command -v syft >/dev/null || die "syft not found (makes the SBOMs): https://github.com/anchore/syft#installation"

echo "== tag $tag (passphrase 1 of 2; ssh-add $key first to type it once)"
git -c gpg.format=ssh -c user.signingkey="$key.pub" tag -s "$tag" -m "igris $version"

echo "== build $tag"
goreleaser check
goreleaser release --clean --skip=publish
go run ./tools/genformula -dist dist

echo "== sign dist/checksums.txt (passphrase 2 of 2)"
ssh-keygen -Y sign -f "$key" -n file dist/checksums.txt

echo "== check"
git -c gpg.ssh.allowedSignersFile="$signers" verify-tag "$tag"
verify_file dist/checksums.txt dist/checksums.txt.sig
(cd dist && if command -v sha256sum >/dev/null; then sha256sum -c --ignore-missing --quiet checksums.txt; else shasum -a 256 -c --ignore-missing --quiet checksums.txt; fi)

cat <<EOF

sign: $tag is tagged (not pushed), built and signed in dist/.
Next, for the agent: verify again, git push origin $tag, then gh release create
$tag with dist/*.tar.gz, dist/*.sbom.json, dist/checksums.txt and
dist/checksums.txt.sig; dist/igris.rb goes to the tap (docs/homebrew-tap.md).
To undo instead: git tag -d $tag
EOF
case $version in
*-rc.*) echo "This is a release candidate: gh release create --prerelease, and leave the tap's formula alone (Homebrew installs stable releases only)." ;;
esac
