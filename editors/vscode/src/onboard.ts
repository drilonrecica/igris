// Onboarding (SPEC §15.9), as plain data: the first-run checklist from
// `igris doctor --json`, the install command for each missing tool, and the
// argv of `igris init` and of the run started with one button. No VS Code
// here, so the unit tests check exactly what is shown and run. The commands
// are fixed text, never built from doctor's output or the plan, and the
// extension never runs one: it puts it in a terminal for the owner to run.

import { OFFERED_MODES, skipsPermissions, SKIP_PERMISSIONS_REFUSAL } from "./answers";
import { obj } from "./protocol";
import { line, short } from "./textsafe";
import type { Check } from "./version";

export type Level = "ok" | "warn" | "fail";

export interface DoctorCheck {
  id: string;
  level: Level;
  message: string;
  next: string;
}

export type DoctorResult = { ok: true; checks: DoctorCheck[] } | { ok: false; line: string };

/** Reads `igris doctor --json`'s output (v 1). Text from the document is
 * cleaned as textsafe does before it is shown. */
export function parseDoctor(out: string): DoctorResult {
  let doc: unknown;
  try {
    doc = JSON.parse(out);
  } catch {
    return { ok: false, line: "igris doctor --json printed something that isn't JSON" };
  }
  const o = obj(doc);
  if (!o || o["v"] !== 1 || !Array.isArray(o["checks"])) return { ok: false, line: "igris doctor --json printed a document this extension doesn't know" };
  const checks: DoctorCheck[] = [];
  for (const c of o["checks"]) {
    const r = obj(c);
    if (!r || typeof r["id"] !== "string") continue;
    const level = r["level"] === "ok" || r["level"] === "warn" || r["level"] === "fail" ? r["level"] : "warn";
    checks.push({
      id: line(r["id"]),
      level,
      message: short(typeof r["message"] === "string" ? r["message"] : "", 300),
      next: short(typeof r["next"] === "string" ? r["next"] : "", 200),
    });
  }
  return { ok: true, checks };
}

/** The Linux package managers, by the `ID`/`ID_LIKE` words of os-release. */
function packageInstall(platform: string, osRelease: string): string | undefined {
  if (platform === "darwin") return "brew install";
  if (platform !== "linux") return undefined;
  const words = osRelease
    .split("\n")
    .filter((l) => /^ID(_LIKE)?=/.test(l))
    .flatMap((l) => l.slice(l.indexOf("=") + 1).replace(/["']/g, "").split(/\s+/));
  const has = (...ids: string[]): boolean => ids.some((i) => words.includes(i));
  if (has("debian", "ubuntu")) return "sudo apt install";
  if (has("fedora", "rhel", "centos")) return "sudo dnf install";
  if (has("arch")) return "sudo pacman -S";
  if (has("alpine")) return "sudo apk add";
  if (has("suse", "opensuse")) return "sudo zypper install";
  return undefined;
}

/** The command that installs what a doctor check found missing or too old,
 * or undefined when there is none to offer (herdr: its site says how). */
export function installCommand(id: string, platform: string = process.platform, osRelease = ""): string | undefined {
  switch (id) {
    case "igris-path":
      return "brew install drilonrecica/tap/igris";
    case "claude":
      return "curl -fsSL https://claude.ai/install.sh | bash";
    case "tmux":
    case "git": {
      const pkg = packageInstall(platform, osRelease);
      return pkg ? `${pkg} ${id}` : undefined;
    }
    default:
      return undefined;
  }
}

/** What a check that has no command points to instead. */
export function installHint(id: string): string | undefined {
  return id === "herdr" ? "Install herdr from https://herdr.dev, or use tmux." : undefined;
}

export type RowAction =
  | { kind: "install"; command: string }
  | { kind: "init" }
  | { kind: "plan" }
  | { kind: "docs"; url: string }
  | { kind: "terminal"; command: string }
  | { kind: "none" };

/** The commands doctor's `next` may name that the checklist offers as fixed
 * text (never doctor's output itself) in a terminal, not run. */
const TERMINAL_NEXT: Record<string, string> = { "git init": "git init", "git status": "git status" };

export interface Row {
  id: string;
  level: Level;
  label: string;
  detail: string;
  action: RowAction;
}

/** What to do about a check. An install command only when doctor says the
 * tool is not found in PATH (SPEC §11.4's wording), never for a warning
 * about its version or, for git, about the tree; `igris init` and the
 * starter plan when doctor's `next` names them; a git command from the
 * fixed list; else nothing, the line is information. */
function rowOf(c: DoctorCheck, platform: string, osRelease: string): Row {
  const notOk = c.level !== "ok";
  const missing = notOk && c.message.includes("not found in PATH");
  const cmd = missing ? installCommand(c.id, platform, osRelease) : undefined;
  const hint = missing ? installHint(c.id) : undefined;
  const detail = cmd ? `Install: ${cmd}` : (hint ?? c.next);
  let action: RowAction = { kind: "none" };
  if (cmd) action = { kind: "install", command: cmd };
  else if (missing && c.id === "herdr") action = { kind: "docs", url: "https://herdr.dev" };
  else if (notOk && c.next === "igris init") action = { kind: "init" };
  else if (noPlan(c)) action = { kind: "plan" };
  else if (notOk && TERMINAL_NEXT[c.next] !== undefined) action = { kind: "terminal", command: TERMINAL_NEXT[c.next]! };
  return { id: c.id, level: c.level, label: c.message || c.id, detail, action };
}

/** The rows the owner can act on first (failures before warnings, each in
 * doctor's order), then the warnings with nothing to offer, then the ok
 * rows: the picker's first line is the thing to do, and Enter on it does
 * it. */
function ordered(rows: Row[]): Row[] {
  const rank = (r: Row): number => (r.action.kind !== "none" ? (r.level === "fail" ? 0 : 1) : r.level === "fail" ? 2 : r.level === "warn" ? 3 : 4);
  return rows.map((r, i) => ({ r, i })).sort((a, b) => rank(a.r) - rank(b.r) || a.i - b.i).map((x) => x.r);
}

/** The checklist: igris itself (the activation check's verdict), doctor's
 * checks, and `igris init` when the folder has no project (a doctor check
 * whose `next` is `igris init` offers it itself), ordered with what the
 * owner can act on first. `doctor` is undefined when igris could not be
 * asked. */
export function checklist(opts: { install: Check; doctor: DoctorResult | undefined; hasProject: boolean; platform?: string; osRelease?: string }): Row[] {
  const platform = opts.platform ?? process.platform;
  const osRelease = opts.osRelease ?? "";
  const rows: Row[] = [];
  if (opts.install.ok) {
    rows.push({ id: "igris", level: "ok", label: `igris ${opts.install.version}`, detail: "", action: { kind: "none" } });
  } else {
    // Too old: igris updates itself the way it was installed; none: install it.
    const old = !opts.install.line.includes("found none in PATH");
    const command = old ? "igris update" : "brew install drilonrecica/tap/igris";
    rows.push({ id: "igris", level: "fail", label: opts.install.line, detail: `Install: ${command}`, action: { kind: "install", command } });
  }
  if (opts.doctor?.ok) for (const c of opts.doctor.checks) rows.push(rowOf(c, platform, osRelease));
  else if (opts.doctor) rows.push({ id: "doctor", level: "warn", label: opts.doctor.line, detail: "", action: { kind: "none" } });
  if (!opts.hasProject && opts.install.ok && !rows.some((r) => r.action.kind === "init")) {
    rows.push({ id: "init", level: "warn", label: "This folder is not an igris project yet", detail: "igris init creates igris.toml and .igris/ here", action: { kind: "init" } });
  }
  return ordered(rows);
}

/** Whether a run can be started: igris is accepted, nothing failed that
 * stops one, and there is a project with a plan. A warning does not stop
 * it. */
export function readyToStart(rows: Row[]): boolean {
  return rows.length > 0 && rows.every((r) => r.level !== "fail") && !rows.some((r) => r.action.kind === "init" || r.action.kind === "plan");
}

/** The starter plans `igris init` writes when the project has none (SPEC
 * §14): its templates and its example. */
export const PLAN_CHOICES = [
  { id: "go-cli", label: "Go CLI", detail: "a starter plan for a command-line tool in Go" },
  { id: "library", label: "Library", detail: "a starter plan for a library" },
  { id: "web-app", label: "Web app", detail: "a starter plan for a web application" },
  { id: "example", label: "The example plan", detail: "igris's own example, to see how a plan reads" },
] as const;

/** The argv that writes the starter plan id, fixed text; undefined for an
 * id that is not one of PLAN_CHOICES. `init` never overwrites a plan. */
export function planArgs(id: string): string[] | undefined {
  switch (id) {
    case "go-cli":
    case "library":
    case "web-app":
      return ["init", "--template", id];
    case "example":
      return ["init", "--example"];
    default:
      return undefined;
  }
}

/** Whether a doctor check says the plan file is missing: `plan-valid`, not
 * ok, `no plan at <plan>` (internal/checks/doctor.go; its `next` differs
 * with and without igris.toml). An invalid plan is another message. */
export function noPlan(c: DoctorCheck): boolean {
  return c.id === "plan-valid" && c.level !== "ok" && c.message.startsWith("no plan at ");
}

/** Whether doctor says the project has no plan file. */
export function planMissing(doctor: DoctorResult | undefined): boolean {
  return doctor !== undefined && doctor.ok && doctor.checks.some(noPlan);
}

/** The argv of `igris init` (SPEC §14): run in the folder, no flags. */
export function initArgs(): string[] {
  return ["init"];
}

export type StartArgs = { ok: true; args: string[] } | { ok: false; line: string };

/** The argv of the run the button starts: `igris serve [PHASE] [--mode M]`.
 * Without a mode, the project's own. Skip-permissions modes are refused
 * here too (§7.3), and a phase that serve would read as a flag or as its
 * `token` word is left to the terminal. */
export function startArgs(phase: string | undefined, mode: string | undefined): StartArgs {
  if (mode !== undefined && skipsPermissions(mode)) return { ok: false, line: SKIP_PERMISSIONS_REFUSAL };
  if (mode !== undefined && !OFFERED_MODES.includes(mode)) return { ok: false, line: `Igris: ${line(mode)} is not a mode.` };
  if (phase !== undefined && (phase === "" || phase.startsWith("-") || phase === "token")) {
    return { ok: false, line: `Igris: start phase ${line(phase)} from the terminal (igris arise).` };
  }
  return { ok: true, args: ["serve", ...(phase !== undefined ? [phase] : []), ...(mode !== undefined ? ["--mode", mode] : [])] };
}
