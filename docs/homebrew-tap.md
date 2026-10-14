# Homebrew tap

`brew install drilonrecica/tap/igris` installs from the formula `Formula/igris.rb` in the repository [`drilonrecica/homebrew-tap`](https://github.com/drilonrecica/homebrew-tap). GoReleaser's `brews` section is deprecated in 2.x, so the formula is rendered by `tools/genformula` from `tools/genformula/igris.rb.tmpl`, using `dist/metadata.json` (version) and `dist/checksums.txt` (SHA-256 per archive). `make release-local` runs it right after a snapshot GoReleaser build and writes `dist/igris.rb`. Nothing is pushed anywhere.

## Publishing a release's formula

1. Tag the release, then build it from the tag. From v0.6.0 the owner does this step (V06-01, decisions.md V06-P1); the key's passphrase is the owner's alone, so the agent stops here and asks:
   - The agent commits the release (CHANGELOG `## [X.Y.Z]` section, version references) and pushes it to `master`.
   - The owner runs `make sign VERSION=X.Y.Z` (`tools/sign.sh`). It refuses unless the tree is clean, `HEAD` is the pushed `origin/master`, the tag exists nowhere yet, the CHANGELOG has the section and `~/.ssh/igris-release.pub` is in `allowed_signers`. It then creates the signed tag `vX.Y.Z` (not pushed), builds `dist/` from it with genformula, signs `dist/checksums.txt` into `dist/checksums.txt.sig` and checks all three. It asks for the passphrase twice (once with `ssh-add ~/.ssh/igris-release` first). `make sign-check` signs and verifies a test file only, to try the key.
   - The agent checks again: `git -c gpg.ssh.allowedSignersFile=allowed_signers verify-tag vX.Y.Z`, `ssh-keygen -Y verify -f allowed_signers -I igris -n file -s dist/checksums.txt.sig < dist/checksums.txt` and `(cd dist && sha256sum -c --ignore-missing checksums.txt)`, then pushes the tag (`git push origin vX.Y.Z`).

   Before v0.6.0: `git checkout vX.Y.Z && goreleaser release --clean --skip=publish && go run ./tools/genformula -dist dist`. `make release-local` is the snapshot dry run: it always builds with `--snapshot`, so its archives and formula carry a `-SNAPSHOT-…` version that must not be published.
2. Upload the `dist/*.tar.gz` archives, `checksums.txt` and, from v0.6.0, `checksums.txt.sig` and the `*.sbom.json` files to the GitHub Release `vX.Y.Z` (`gh release create vX.Y.Z --verify-tag …`). The formula's URLs point at those assets, so the release has to exist before the formula is pushed.
3. Copy the formula into the tap and push it:

   ```sh
   cd ~/Code/Drilon/homebrew-tap          # a clone of drilonrecica/homebrew-tap
   cp ~/Code/Drilon/igris/dist/igris.rb Formula/igris.rb
   git add Formula/igris.rb
   git commit -m "igris X.Y.Z"
   git push
   ```

4. Capture the release for the compatibility suite: `make compat TAG=vX.Y.Z`, then commit `testdata/compat/vX.Y.Z/` (`compat: vX.Y.Z`). It builds the tag in a temporary worktree and records its `init` config and plan, its examples and its signal files; `go test` fails at the next release's commit if this is skipped.
5. Check it: `brew update && brew install drilonrecica/tap/igris && brew test igris`. Without Homebrew on the host, run the same commands inside the `docker.io/homebrew/brew` container (for example with podman).

To change the formula (description, install steps), edit the template and run `make release-local` to preview it; `go test ./tools/...` renders it from a fake checksums file.
