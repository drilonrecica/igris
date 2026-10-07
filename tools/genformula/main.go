// Command genformula renders the Homebrew formula from the GoReleaser output
// in dist/ (SPEC §18). GoReleaser's own brews section is deprecated, so
// `make release-local` runs this instead. It only writes a file; the owner
// copies it into the tap by hand (docs/homebrew-tap.md).
package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed igris.rb.tmpl
var formulaTmpl string

// releaseURL is where the owner uploads the archives (a GitHub Release tagged
// v<version>); the names match archives.name_template in .goreleaser.yaml.
const releaseURL = "https://github.com/drilonrecica/igris/releases/download/v%s/%s"

type asset struct{ URL, SHA256 string }

type formula struct {
	Version                                          string
	DarwinARM64, DarwinAMD64, LinuxARM64, LinuxAMD64 asset
}

func main() {
	dist := flag.String("dist", "dist", "GoReleaser output directory")
	out := flag.String("out", "", "formula path (default <dist>/igris.rb)")
	flag.Parse()
	if err := run(*dist, *out); err != nil {
		fmt.Fprintln(os.Stderr, "genformula:", err)
		os.Exit(1)
	}
}

func run(dist, out string) error {
	if out == "" {
		out = filepath.Join(dist, "igris.rb")
	}
	meta, err := os.ReadFile(filepath.Join(dist, "metadata.json")) //nolint:gosec // dist is a build-tool flag, not untrusted input
	if err != nil {
		return fmt.Errorf("read metadata: %w (run goreleaser first)", err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(meta, &m); err != nil {
		return fmt.Errorf("parse metadata.json: %w", err)
	}
	sums, err := os.Open(filepath.Join(dist, "checksums.txt")) //nolint:gosec // dist is a build-tool flag, not untrusted input
	if err != nil {
		return fmt.Errorf("open checksums: %w", err)
	}
	defer func() { _ = sums.Close() }()
	body, err := render(m.Version, sums)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Println("wrote", out)
	return nil
}

// render fills the formula template for version from a checksums.txt reader.
func render(version string, checksums io.Reader) ([]byte, error) {
	if version == "" {
		return nil, fmt.Errorf("empty version")
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(checksums)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			sums[f[1]] = f[0]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	f := formula{Version: version}
	for _, t := range []struct {
		dst      *asset
		os, arch string
	}{
		{&f.DarwinARM64, "darwin", "arm64"},
		{&f.DarwinAMD64, "darwin", "amd64"},
		{&f.LinuxARM64, "linux", "arm64"},
		{&f.LinuxAMD64, "linux", "amd64"},
	} {
		name := fmt.Sprintf("igris_%s_%s_%s.tar.gz", version, t.os, t.arch)
		sum, ok := sums[name]
		if !ok {
			return nil, fmt.Errorf("checksums.txt has no entry for %s", name)
		}
		*t.dst = asset{URL: fmt.Sprintf(releaseURL, version, name), SHA256: sum}
	}
	var b strings.Builder
	if err := template.Must(template.New("igris.rb").Parse(formulaTmpl)).Execute(&b, f); err != nil {
		return nil, fmt.Errorf("render formula: %w", err)
	}
	return []byte(b.String()), nil
}
