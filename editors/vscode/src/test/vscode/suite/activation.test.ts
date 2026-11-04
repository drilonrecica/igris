import * as assert from "node:assert/strict";
import * as path from "node:path";
import * as vscode from "vscode";

describe("activation", () => {
  before(() => {
    const bin = process.env["IGRIS_TEST_BIN"];
    assert.ok(bin, "IGRIS_TEST_BIN is set by runTest");
    process.env["PATH"] = `${bin}${path.delimiter}${process.env["PATH"] ?? ""}`;
  });

  it("activates with igris 1.8.1 in PATH and holds no refusal", async () => {
    const ext = vscode.extensions.getExtension<{ refusal(): string | undefined }>("drilonrecica.igris");
    assert.ok(ext, "extension drilonrecica.igris is not installed in the test host");
    const api = await ext.activate();
    assert.equal(api.refusal(), undefined);
  });

  it("contributes its command", async () => {
    const cmds = await vscode.commands.getCommands(true);
    assert.ok(cmds.includes("igris.checkInstall"));
  });
});
