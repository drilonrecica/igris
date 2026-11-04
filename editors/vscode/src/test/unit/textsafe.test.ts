import * as assert from "node:assert/strict";
import { clean, hasControl, line, short } from "../../textsafe";

// The cases of internal/textsafe/textsafe_test.go, so the port cleans as
// igris does (SPEC §15.9). Go's "invalid UTF-8 becomes U+FFFD" is a lone
// surrogate here: a JavaScript string holds no invalid bytes.
const cleanCases: [string, string, string][] = [
  ["plain", "M0-01 Go module — done ✓", "M0-01 Go module — done ✓"],
  ["newline and tab stay", "a\n\tb", "a\n\tb"],
  ["color", "\x1b[31mFAIL\x1b[0m ok", "FAIL ok"],
  ["clear screen", "\x1b[2J\x1b[Hspoof", "spoof"],
  ["paste end then shift-tab", "out\x1b[201~\x1b[Z", "out"],
  ["osc title with bel", "a\x1b]0;evil\x07b", "ab"],
  ["osc 52 with st", "a\x1b]52;c;ZXZpbA==\x1b\\b", "ab"],
  ["unclosed osc ends at the line", "a\x1b]0;evil\nnext", "a\nnext"],
  ["dcs", "a\x1bPq#0\x1b\\b", "ab"],
  ["two-char escape", "a\x1bcb", "ab"],
  ["escape with intermediate", "a\x1b(Bb", "ab"],
  ["lone escape at the end", "a\x1b", "a"],
  ["unfinished csi", "a\x1b[12;", "a"],
  ["csi cut by a control char", "a\x1b[1\x07b", "ab"],
  ["c1 csi rune", "a\u009b31mb", "ab"],
  ["other c1", "a\u0085b\u009cc", "abc"],
  ["c0 and del", "a\x00\x07\x08\r\x7fb", "ab"],
  ["crlf", "a\r\nb", "a\nb"],
  ["lone surrogate", "a\ud800b", "a�b"],
  ["bidi overrides", "a‮cba‬ b", "acba b"],
  ["bidi embeddings and isolates", "‪‫‭⁦⁧⁨⁩x", "x"],
  ["bidi marks", "a‎‏؜b", "ab"],
  ["zero width and bom", "a​b⁠c﻿d", "abcd"],
  ["tag characters", "a\u{e0001}\u{e0069}\u{e0067}\u{e007f}b", "ab"],
  ["invisible operators and deprecated formats", "a⁡⁢⁣⁤⁪⁫⁯￹￺￻­b", "ab"],
  ["zwj emoji kept", "👩‍💻 and a‌b", "👩‍💻 and a‌b"],
  ["empty", "", ""],
];

describe("textsafe", () => {
  for (const [name, input, want] of cleanCases) {
    it(`cleans: ${name}`, () => {
      const got = clean(input);
      assert.equal(got, want);
      assert.equal(clean(got), got, "clean is stable");
    });
  }

  it("puts text on one line", () => {
    assert.equal(line("one line"), "one line");
    assert.equal(line("two\nlines\tand a tab"), "two lines and a tab");
    assert.equal(line("note\x1b[2J\nspoofed prompt"), "note spoofed prompt");
    assert.equal(line(undefined), "");
  });

  it("finds control characters", () => {
    assert.equal(hasControl("| M0-01 | **Go module** |\tready |"), false);
    assert.equal(hasControl("| M0-01 | \x1b[31mred | ready |"), true);
    assert.equal(hasControl("bell\x07"), true);
    assert.equal(hasControl("c1 \u009b"), true);
    assert.equal(hasControl(""), false);
  });

  it("cuts on characters, marking the cut", () => {
    assert.equal(short("abcdef", 10), "abcdef");
    assert.equal(short("abcdef", 4), "abc…");
    assert.equal(short("👩👩👩", 2), "👩…");
  });
});
