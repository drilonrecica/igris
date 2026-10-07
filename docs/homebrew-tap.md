# Homebrew tap

`brew install drilonrecica/tap/igris` installs from the formula `Formula/igris.rb` in the repository [`drilonrecica/homebrew-tap`](https://github.com/drilonrecica/homebrew-tap). GoReleaser's `brews` section is deprecated in 2.x, so the formula is rendered by `tools/genformula` from `tools/genformula/igris.rb.tmpl`, using `dist/metadata.json` (version) and `dist/checksums.txt` (SHA-256 per archive). `make release-local` runs it right after GoReleaser and writes `dist/igris.rb`. Nothing is pushed anywhere.

**Not before v0.2.0.** The owner decided (2026-10-07) that igris goes to Homebrew only with the v0.2.0 release: the first `Formula/igris.rb` is pushed as part of publishing v0.2.0 (task V02-R), never from a snapshot or an earlier release. The README and the project page carry the install line since V02-09, but it only works once V02-R has pushed the formula, so don't merge or release those docs before then.

## Publishing a release's formula

1. Tag the release, then build it from the tag: `git checkout vX.Y.Z && make release-local`. A build from an untagged commit is a snapshot, and its formula carries a `-SNAPSHOT-…` version that must not be published.
2. Upload the `dist/*.tar.gz` archives and `checksums.txt` to the GitHub Release `vX.Y.Z`. The formula's URLs point at those assets, so the release has to exist before the formula is pushed.
3. Copy the formula into the tap and push it:

   ```sh
   cd ~/Code/Drilon/homebrew-tap          # a clone of drilonrecica/homebrew-tap
   cp ~/Code/Drilon/igris/dist/igris.rb Formula/igris.rb
   git add Formula/igris.rb
   git commit -m "igris X.Y.Z"
   git push
   ```

4. Check it: `brew update && brew install drilonrecica/tap/igris && brew test igris`.

To change the formula (description, install steps), edit the template and run `make release-local`; `go test ./tools/...` renders it from a fake checksums file.
