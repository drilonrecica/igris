// The sidebar tree (SPEC §15.9): view.ts's nodes as the editor's tree items.
// Red only for what needs the owner, Ember for a warning, dim for what is
// done, skipped or blocked; every row also says its state in words (§15.4:
// never color alone).

import * as vscode from "vscode";
import type { Icon, Node } from "./view";

const icons: Record<Icon, [string, string | undefined]> = {
  done: ["pass", "disabledForeground"],
  running: ["circle-filled", "charts.blue"],
  needs: ["bell-dot", "charts.red"],
  ready: ["circle-outline", undefined],
  blocked: ["circle-slash", "disabledForeground"],
  skipped: ["dash", "disabledForeground"],
  warn: ["warning", "charts.yellow"],
  fail: ["error", "charts.yellow"],
  waiting: ["clock", undefined],
  phase: ["layers", undefined],
};

export class RunTree implements vscode.TreeDataProvider<Node> {
  private roots: Node[] = [];
  private parents = new Map<string, Node | undefined>();
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  /** Draws nodes in place of what was there. */
  set(nodes: Node[]): void {
    this.roots = nodes;
    this.parents.clear();
    const walk = (ns: Node[], parent: Node | undefined): void => {
      for (const n of ns) {
        this.parents.set(n.id, parent);
        walk(n.children, n);
      }
    };
    walk(nodes, undefined);
    this.changed.fire(undefined);
  }

  get nodes(): Node[] {
    return this.roots;
  }

  getChildren(n?: Node): Node[] {
    return n ? n.children : this.roots;
  }

  getParent(n: Node): Node | undefined {
    return this.parents.get(n.id);
  }

  getTreeItem(n: Node): vscode.TreeItem {
    const state =
      n.expanded === undefined
        ? vscode.TreeItemCollapsibleState.None
        : n.expanded
          ? vscode.TreeItemCollapsibleState.Expanded
          : vscode.TreeItemCollapsibleState.Collapsed;
    const item = new vscode.TreeItem(n.label, state);
    item.id = n.id;
    item.description = n.description;
    item.tooltip = n.tooltip;
    // igris.<kind>, then .ask when it asks the owner something and .mode
    // when its next session's mode can be set: the menus match on these.
    item.contextValue = `igris.${n.kind}${n.ask !== undefined ? ".ask" : ""}${n.modable ? ".mode" : ""}`;
    if (n.ask !== undefined) item.command = { command: "igris.answer", title: "Answer", arguments: [n] };
    const [icon, color] = icons[n.icon];
    item.iconPath = new vscode.ThemeIcon(icon, color ? new vscode.ThemeColor(color) : undefined);
    return item;
  }

  dispose(): void {
    this.changed.dispose();
  }
}
