import * as vscode from "vscode";
import { checkInstall } from "./igris";

/** The one line of the current refusal, if any (SPEC §15.9): while it holds
 * nothing else of the extension runs. */
let refused: string | undefined;

export async function activate(context: vscode.ExtensionContext): Promise<{ refusal(): string | undefined }> {
  context.subscriptions.push(
    vscode.commands.registerCommand("igris.checkInstall", async () => {
      await verify();
    }),
  );
  await verify();
  return { refusal: () => refused };
}

async function verify(): Promise<void> {
  const res = await checkInstall();
  refused = res.ok ? undefined : res.line;
  await vscode.commands.executeCommand("setContext", "igris.refused", refused !== undefined);
  if (!res.ok) void vscode.window.showWarningMessage(res.line);
}

export function deactivate(): void {}
