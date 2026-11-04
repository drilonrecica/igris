import * as assert from "node:assert/strict";
import { checkVersionOutput, clean, MIN_IGRIS, parseVersion } from "../../version";

describe("checkVersionOutput", () => {
  const ok = ["igris 1.8.0", "igris 1.8.1\n", "igris 1.9.0", "igris 2.0.0", "igris 1.8.1-0.20261026190653-cbb57ba79522+dirty", "igris 1.8.0-rc.1", "igris 1.8.0+build5", "igris dev"];
  for (const out of ok) {
    it(`accepts ${JSON.stringify(out)}`, () => {
      assert.equal(checkVersionOutput(out).ok, true);
    });
  }

  const bad: [string, string][] = [
    ["igris 1.7.9", "1.7.9"],
    ["igris 0.9.0", "0.9.0"],
    ["igris 1.8", "1.8"],
    ["", "nothing"],
    ["garbage", "garbage"],
    ["notigris 1.9.0", "notigris 1.9.0"],
  ];
  for (const [out, found] of bad) {
    it(`refuses ${JSON.stringify(out)}`, () => {
      const res = checkVersionOutput(out);
      assert.deepEqual(res, { ok: false, line: `Igris needs igris ${MIN_IGRIS} or newer (found ${found}); install it and reload the window.` });
    });
  }

  it("names a missing igris", () => {
    assert.deepEqual(checkVersionOutput(undefined), {
      ok: false,
      line: `Igris needs igris ${MIN_IGRIS} or newer (found none in PATH); install it and reload the window.`,
    });
  });

  it("cleans and cuts what it quotes", () => {
    const res = checkVersionOutput("\x1b[31mevil\x1b[0m\x07" + "x".repeat(100));
    assert.equal(res.ok, false);
    if (!res.ok) assert.equal(res.line, `Igris needs igris ${MIN_IGRIS} or newer (found evil${"x".repeat(36)}); install it and reload the window.`);
  });
});

describe("parseVersion / clean", () => {
  it("reads X.Y.Z with suffixes", () => {
    assert.deepEqual(parseVersion("1.8.1-0.2026+dirty"), [1, 8, 1]);
    assert.equal(parseVersion("1.8"), undefined);
  });
  it("keeps plain text and drops controls", () => {
    assert.equal(clean("a\u0000b\nc"), "ab c");
  });
});
