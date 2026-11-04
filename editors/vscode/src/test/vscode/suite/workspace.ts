import * as assert from "node:assert/strict";
import * as vscode from "vscode";

/** The scratch workspace the test host opened (testdata/workspace). */
export function workspaceDir(): string {
  const f = vscode.workspace.workspaceFolders?.[0];
  assert.ok(f, "the test host opens testdata/workspace");
  return f.uri.fsPath;
}
