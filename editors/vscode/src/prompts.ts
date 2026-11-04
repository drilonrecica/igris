// The editor's own prompts for act.ts (SPEC §15.9: every surface is the
// editor's): QuickPick, InputBox, a modal for the second confirmation,
// notifications with buttons, an untitled document for a plan to read.

import * as vscode from "vscode";
import type { Ui } from "./act";
import { short } from "./textsafe";

export const editorUi: Ui = {
  async choose(choices, title, placeholder) {
    const items = choices.map((c) => ({ label: c.danger ? `$(warning) ${c.label}` : c.label, choice: c }));
    const got = await vscode.window.showQuickPick(items, { title, placeHolder: placeholder, ignoreFocusOut: true });
    return got?.choice;
  },
  async chooseAsk(asks) {
    const items = asks.map((a) => ({ label: a.title, description: a.task ? `${a.task} · ${a.kind}` : a.kind, detail: short(a.text, 200), ask: a }));
    const got = await vscode.window.showQuickPick(items, { title: "Igris: what needs you", placeHolder: "Choose one to answer", ignoreFocusOut: true });
    return got?.ask;
  },
  async input(i, title) {
    return vscode.window.showInputBox({
      title,
      prompt: i.prompt,
      ...(i.placeholder ? { placeHolder: i.placeholder } : {}),
      ignoreFocusOut: true,
      validateInput: (v) => (i.required && v.trim() === "" ? `${i.prompt} is needed.` : undefined),
    });
  },
  async pick(p, title) {
    const items = p.items.map((m) => ({ label: m, ...(m === p.picked ? { description: "current" } : {}) }));
    const got = await vscode.window.showQuickPick(items, { title: `${title}: ${p.prompt}`, placeHolder: p.note ?? p.prompt, ignoreFocusOut: true });
    return got?.label;
  },
  async confirm(c) {
    const got = await vscode.window.showWarningMessage(c.title, { modal: true, detail: c.text }, c.label);
    return got === c.label;
  },
  async notify(line, buttons) {
    return vscode.window.showInformationMessage(line, ...buttons);
  },
  async show(text, title) {
    const doc = await vscode.workspace.openTextDocument({ content: `# ${title}\n\n${text}\n`, language: "markdown" });
    await vscode.window.showTextDocument(doc, { preview: true });
  },
  told(ok, line) {
    if (ok) vscode.window.setStatusBarMessage(`$(check) ${line}`, 4000);
    else void vscode.window.showErrorMessage(line);
  },
};
