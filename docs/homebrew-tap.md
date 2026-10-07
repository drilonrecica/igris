# Homebrew tap

`brew install drilonrecica/tap/igris` installs from the formula `Formula/igris.rb` in the repository [`drilonrecica/homebrew-tap`](https://github.com/drilonrecica/homebrew-tap). GoReleaser's `brews` section is deprecated in 2.x, so the formula is rendered by `tools/genformula` from `tools/genformula/igris.rb.tmpl`, using `dist/metadata.json` (version) and `dist/checksums.txt` (SHA-256 per archive). `make release-local` runs it right after a snapshot GoReleaser build and writes `dist/igris.rb`. Nothing is pushed anywhere.

## Publishing a release's formula

1. Tag the release, then build it from the tag: `git checkout vX.Y.Z && goreleaser release --clean --skip=publish && go run ./tools/genformula -dist dist`. `make release-local` is the snapshot dry run: it always builds with `--snapshot`, so its archives and formula carry a `-SNAPSHOT-…` version that must not be published.
2. Upload the `dist/*.tar.gz` archives and `checksums.txt` to the GitHub Release `vX.Y.Z`. The formula's URLs point at those assets, so the release has to exist before the formula is pushed.
3. Copy the formula into the tap and push it:

   ```sh
   cd ~/Code/Drilon/homebrew-tap          # a clone of drilonrecica/homebrew-tap
   cp ~/Code/Drilon/igris/dist/igris.rb Formula/igris.rb
   git add Formula/igris.rb
   git commit -m "igris X.Y.Z"
   git push
   ```

4. Check it: `brew update && brew install drilonrecica/tap/igris && brew test igris`. Without Homebrew on the host, run the same commands inside the `docker.io/homebrew/brew` container (for example with podman).

To change the formula (description, install steps), edit the template and run `make release-local` to preview it; `go test ./tools/...` renders it from a fake checksums file.
