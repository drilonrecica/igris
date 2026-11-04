// A task's changes in the editor's native diff (SPEC §15.9): the commits or
// the branch in a QuickPick, then its files, each opened with the diff
// editor. The two sides are virtual documents (scheme igris-git) that
// changes.ts reads from git; nothing is checked out or written.

import * as vscode from "vscode";
import { contentAt, filesOf, gitIn, sourcesOf, type FileChange, type Git, type Source } from "./changes";
import { line, short } from "./textsafe";

const SCHEME = "igris-git";

/** Serves `igris-git:/<file>?<ref>` from git in the project. */
export class GitContent implements vscode.TextDocumentContentProvider {
  private git: Git | undefined;

  setRoot(root: string | undefined): void {
    this.git = root ? gitIn(root) : undefined;
  }

  provideTextDocumentContent(uri: vscode.Uri): Promise<string> {
    if (!this.git) return Promise.resolve("");
    return contentAt(this.git, decodeURIComponent(uri.query), uri.path.replace(/^\//, ""));
  }
}

export const SCHEME_NAME = SCHEME;

function uriOf(ref: string, file: string): vscode.Uri {
  return vscode.Uri.from({ scheme: SCHEME, path: `/${file}`, query: encodeURIComponent(ref) });
}

const letters: Record<string, string> = { A: "added", M: "modified", D: "deleted", R: "renamed", C: "copied", T: "type changed" };

/** Opens task's changes: asks which source when there are several, then
 * which file, until the owner backs out. `told` says what went wrong. */
export async function viewChanges(
  root: string,
  task: string,
  runIds: string[],
  told: (line: string) => void,
): Promise<void> {
  const git = gitIn(root);
  let sources: Source[];
  try {
    sources = await sourcesOf(root, git, task, runIds);
  } catch (e) {
    told(`Igris: couldn't read ${line(task)}'s changes: ${short(e instanceof Error ? e.message : String(e), 200)}`);
    return;
  }
  if (sources.length === 0) {
    told(`Igris: ${line(task)} has no commit or branch to read yet.`);
    return;
  }
  let source: Source | undefined = sources[0];
  if (sources.length > 1) {
    const got = await vscode.window.showQuickPick(
      sources.map((s) => ({ label: s.label, description: s.description, source: s })),
      { title: `Igris: ${line(task)}'s changes`, placeHolder: "Choose a commit or the branch", ignoreFocusOut: true },
    );
    source = got?.source;
  }
  if (!source) return;
  let files: FileChange[];
  try {
    files = await filesOf(git, source);
  } catch (e) {
    told(`Igris: couldn't read ${source.label}: ${short(e instanceof Error ? e.message : String(e), 200)}`);
    return;
  }
  if (files.length === 0) {
    told(`Igris: ${source.label} changes no file.`);
    return;
  }
  const items = files.map((f) => ({
    label: line(f.path),
    description: f.oldPath ? `${letters[f.status] ?? f.status} from ${line(f.oldPath)}` : (letters[f.status] ?? f.status),
    file: f,
  }));
  const s = source;
  for (;;) {
    const got = await vscode.window.showQuickPick(items, {
      title: `Igris: ${line(task)} · ${s.label}`,
      placeHolder: `${files.length} file${files.length === 1 ? "" : "s"} changed; Esc to close`,
      ignoreFocusOut: true,
    });
    if (!got) return;
    const f = got.file;
    await vscode.commands.executeCommand(
      "vscode.diff",
      uriOf(s.left, f.oldPath ?? f.path),
      uriOf(s.right, f.path),
      `${f.path} · ${line(task)} (${s.kind === "branch" ? s.label : s.right.slice(0, 8)})`,
      { preview: true },
    );
  }
}
