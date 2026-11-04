import * as assert from "node:assert/strict";
import { checklist, initArgs, installCommand, parseDoctor, planArgs, planMissing, readyToStart, startArgs } from "../../onboard";
import { checkVersionOutput } from "../../version";

const doctorOut = JSON.stringify({
  v: 1,
  checks: [
    { id: "claude", level: "fail", message: "claude not found in PATH; igris arise needs Claude Code 2.1.0 or later" },
    { id: "tmux", level: "warn", message: "tmux not found in PATH; igris arise needs tmux 3.2 or later" },
    { id: "herdr", level: "warn", message: "herdr not found in PATH" },
    { id: "git", level: "ok", message: "git repository" },
    { id: "plan-valid", level: "fail", message: "no plan at tasks.md", next: "igris adapt" },
  ],
});

describe("parseDoctor", () => {
  it("reads v1 and cleans text", () => {
    const r = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "git", level: "ok", message: "a\x1b[31mb\x1b[0m" }, { nope: 1 }] }));
    assert.ok(r.ok);
    assert.equal(r.checks.length, 1);
    assert.equal(r.checks[0]!.message, "ab");
  });
  it("refuses other documents", () => {
    assert.equal(parseDoctor("nope").ok, false);
    assert.equal(parseDoctor(JSON.stringify({ v: 2, checks: [] })).ok, false);
    assert.equal(parseDoctor(JSON.stringify({ v: 1 })).ok, false);
  });
  it("treats an unknown level as a warning", () => {
    const r = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "x", level: "??", message: "m" }] }));
    assert.ok(r.ok);
    assert.equal(r.checks[0]!.level, "warn");
  });
});

describe("installCommand", () => {
  const debian = 'ID=ubuntu\nID_LIKE=debian\n';
  it("picks the package manager", () => {
    assert.equal(installCommand("tmux", "linux", debian), "sudo apt install tmux");
    assert.equal(installCommand("tmux", "linux", 'ID="fedora"\n'), "sudo dnf install tmux");
    assert.equal(installCommand("git", "linux", "ID=arch\n"), "sudo pacman -S git");
    assert.equal(installCommand("tmux", "darwin"), "brew install tmux");
    assert.equal(installCommand("tmux", "linux", ""), undefined);
  });
  it("has the fixed commands", () => {
    assert.equal(installCommand("claude"), "curl -fsSL https://claude.ai/install.sh | bash");
    assert.equal(installCommand("igris-path"), "brew install drilonrecica/tap/igris");
    assert.equal(installCommand("herdr"), undefined);
    assert.equal(installCommand("backend; rm -rf /"), undefined);
  });
});

describe("checklist", () => {
  const ok = checkVersionOutput("igris 1.9.0");
  const doctor = parseDoctor(doctorOut);
  it("offers an install command for each missing tool, none for ok checks", () => {
    const rows = checklist({ install: ok, doctor, hasProject: true, platform: "linux", osRelease: "ID=debian\n" });
    const by = Object.fromEntries(rows.map((r) => [r.id, r]));
    assert.deepEqual(by["claude"]!.action, { kind: "install", command: "curl -fsSL https://claude.ai/install.sh | bash" });
    assert.deepEqual(by["tmux"]!.action, { kind: "install", command: "sudo apt install tmux" });
    assert.deepEqual(by["herdr"]!.action, { kind: "docs", url: "https://herdr.dev" });
    assert.deepEqual(by["git"]!.action, { kind: "none" });
    assert.equal(by["plan-valid"]!.detail, "igris adapt");
    assert.equal(readyToStart(rows), false);
  });
  it("puts what the owner can act on first, failures before warnings, ok rows last", () => {
    const rows = checklist({ install: ok, doctor, hasProject: true, platform: "linux", osRelease: "ID=debian\n" });
    assert.deepEqual(
      rows.map((r) => r.id),
      ["claude", "plan-valid", "tmux", "herdr", "igris", "git"],
    );
  });
  it("offers igris init in a folder without a project, first when nothing fails", () => {
    const good = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "git", level: "ok", message: "g" }] }));
    const rows = checklist({ install: ok, doctor: good, hasProject: false, platform: "linux" });
    assert.equal(rows[0]!.id, "init");
    assert.deepEqual(rows[0]!.action, { kind: "init" });
    assert.equal(rows.filter((r) => r.action.kind === "init").length, 1);
  });
  it("lets a doctor check whose next is igris init offer it, once", () => {
    const d = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "config", level: "ok", message: "no igris.toml here; using the defaults", next: "igris init" }, { id: "allow-rules", level: "warn", message: ".claude/settings.local.json not found", next: "igris init" }] }));
    const rows = checklist({ install: ok, doctor: d, hasProject: false, platform: "linux" });
    assert.deepEqual(rows[0]!.id, "allow-rules");
    assert.deepEqual(rows[0]!.action, { kind: "init" });
    assert.equal(rows.some((r) => r.id === "init"), false, "no second init row");
    assert.deepEqual(rows.find((r) => r.id === "config")!.action, { kind: "none" }, "an ok check offers nothing");
  });
  it("offers an install only for a tool not found in PATH, never for a version or tree warning", () => {
    const d = parseDoctor(JSON.stringify({ v: 1, checks: [
      { id: "git", level: "warn", message: "the working tree has uncommitted changes: the first task's session sees them as its own", next: "git status" },
      { id: "tmux", level: "warn", message: "tmux 3.1 is older than 3.2, the oldest version igris is verified with; update tmux" },
      { id: "claude", level: "warn", message: "Claude Code 3.0.0 is a newer major version than igris was verified with (2.1)" },
    ] }));
    const by = Object.fromEntries(checklist({ install: ok, doctor: d, hasProject: true, platform: "linux", osRelease: "ID=fedora\n" }).map((r) => [r.id, r]));
    assert.deepEqual(by["git"]!.action, { kind: "terminal", command: "git status" }, "git is there; its next is offered as fixed text");
    assert.deepEqual(by["tmux"]!.action, { kind: "none" });
    assert.equal(by["tmux"]!.detail, "");
    assert.deepEqual(by["claude"]!.action, { kind: "none" });
  });
  it("offers the starter plan when doctor says the plan is missing, and is not ready until then", () => {
    const d = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "git", level: "ok", message: "g" }, { id: "plan-valid", level: "warn", message: "no plan at tasks.md", next: "igris init --example" }] }));
    const rows = checklist({ install: ok, doctor: d, hasProject: true, platform: "linux" });
    assert.deepEqual(rows[0]!.action, { kind: "plan" });
    assert.equal(readyToStart(rows), false);
    assert.equal(planMissing(d), true);
    // After igris init doctor says the same with level fail and next `igris adapt` (doctor.go).
    const after = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "plan-valid", level: "fail", message: "no plan at tasks.md", next: "igris adapt" }] }));
    assert.equal(planMissing(after), true);
    assert.deepEqual(checklist({ install: ok, doctor: after, hasProject: true, platform: "linux" })[0]!.action, { kind: "plan" });
    assert.equal(planMissing(parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "plan-valid", level: "fail", message: "tasks.md:3: bad row", next: "igris check" }] }))), false, "an invalid plan is not a missing one");
    assert.equal(planMissing(undefined), false);
  });
  it("asks to install igris when it is missing, to update it when old", () => {
    const none = checklist({ install: checkVersionOutput(undefined), doctor: undefined, hasProject: false });
    assert.deepEqual(none, [{ id: "igris", level: "fail", label: none[0]!.label, detail: "Install: brew install drilonrecica/tap/igris", action: { kind: "install", command: "brew install drilonrecica/tap/igris" } }]);
    const old = checklist({ install: checkVersionOutput("igris 1.7.0"), doctor: undefined, hasProject: true });
    assert.deepEqual(old[0]!.action, { kind: "install", command: "igris update" });
  });
  it("is ready when nothing fails and a project exists", () => {
    const good = parseDoctor(JSON.stringify({ v: 1, checks: [{ id: "git", level: "ok", message: "g" }, { id: "notify", level: "warn", message: "n" }] }));
    assert.equal(readyToStart(checklist({ install: ok, doctor: good, hasProject: true })), true);
    assert.equal(readyToStart(checklist({ install: ok, doctor: good, hasProject: false })), false);
  });
});

describe("startArgs", () => {
  it("builds argv for serve", () => {
    assert.deepEqual(startArgs(undefined, undefined), { ok: true, args: ["serve"] });
    assert.deepEqual(startArgs("W1", "auto"), { ok: true, args: ["serve", "W1", "--mode", "auto"] });
  });
  it("never starts a skip-permissions mode", () => {
    for (const m of ["yolo", "plan:yolo"]) assert.equal(startArgs("W1", m).ok, false);
    assert.equal(startArgs("W1", "bogus").ok, false);
  });
  it("leaves odd phase names to the terminal", () => {
    for (const p of ["--force-unlock", "token", ""]) assert.equal(startArgs(p, undefined).ok, false);
  });
  it("init is argv without flags", () => {
    assert.deepEqual(initArgs(), ["init"]);
  });
  it("the starter plans are fixed argv", () => {
    assert.deepEqual(planArgs("go-cli"), ["init", "--template", "go-cli"]);
    assert.deepEqual(planArgs("library"), ["init", "--template", "library"]);
    assert.deepEqual(planArgs("web-app"), ["init", "--template", "web-app"]);
    assert.deepEqual(planArgs("example"), ["init", "--example"]);
    assert.equal(planArgs("../x"), undefined);
  });
});
