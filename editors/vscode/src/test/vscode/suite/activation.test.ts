import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import { workspaceDir } from "./workspace";

describe("activation", () => {
  before(() => {
    const bin = process.env["IGRIS_TEST_BIN"];
    assert.ok(bin, "IGRIS_TEST_BIN is set by runTest");
    process.env["PATH"] = `${bin}${path.delimiter}${process.env["PATH"] ?? ""}`;
    // The scratch workspace becomes an igris project only now, after the
    // stand-in is first in PATH: nothing activates the extension earlier,
    // so the real igris never runs (AGENTS §5). runview.test.ts removes it.
    fs.mkdirSync(path.join(workspaceDir(), ".igris"), { recursive: true });
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
    assert.ok(cmds.includes("igris.viewChanges"));
    assert.ok(cmds.includes("igris.runTask"));
  });
});
