package main

import (
	"strings"
	"testing"
)

const fakeSums = `aaaa  igris_1.2.3_darwin_amd64.tar.gz
bbbb  igris_1.2.3_darwin_arm64.tar.gz
cccc  igris_1.2.3_linux_amd64.tar.gz
dddd  igris_1.2.3_linux_arm64.tar.gz
`

func TestRender(t *testing.T) {
	got, err := render("1.2.3", strings.NewReader(fakeSums))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		`class Igris < Formula`,
		`version "1.2.3"`,
		"on_macos do\n    if Hardware::CPU.arm?\n      url \"https://github.com/drilonrecica/igris/releases/download/v1.2.3/igris_1.2.3_darwin_arm64.tar.gz\"\n      sha256 \"bbbb\"\n    else\n      url \"https://github.com/drilonrecica/igris/releases/download/v1.2.3/igris_1.2.3_darwin_amd64.tar.gz\"\n      sha256 \"aaaa\"",
		"on_linux do\n    if Hardware::CPU.arm?\n      url \"https://github.com/drilonrecica/igris/releases/download/v1.2.3/igris_1.2.3_linux_arm64.tar.gz\"\n      sha256 \"dddd\"\n    else\n      url \"https://github.com/drilonrecica/igris/releases/download/v1.2.3/igris_1.2.3_linux_amd64.tar.gz\"\n      sha256 \"cccc\"",
		`generate_completions_from_executable(bin/"igris", "completion")`,
		`shell_output("#{bin}/igris version")`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("formula missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "{{") {
		t.Errorf("unrendered template action:\n%s", s)
	}
}

func TestRenderErrors(t *testing.T) {
	if _, err := render("1.2.3", strings.NewReader("aaaa  igris_1.2.3_darwin_amd64.tar.gz\n")); err == nil || !strings.Contains(err.Error(), "igris_1.2.3_darwin_arm64.tar.gz") {
		t.Errorf("missing checksum: err = %v", err)
	}
	if _, err := render("", strings.NewReader(fakeSums)); err == nil {
		t.Error("empty version: want error")
	}
}
