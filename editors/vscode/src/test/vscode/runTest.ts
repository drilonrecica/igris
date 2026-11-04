// Runs the suite in a real VS Code (@vscode/test-electron). It starts no
// igris: the suite puts testdata/bin first in PATH, whose `igris` answers
// only --version and status --json (AGENTS §5); the run comes from a fake
// serve socket (src/test/support/fakeServe.ts). The VS Code under test is
// the oldest the extension claims.
import * as path from "node:path";
import { runTests } from "@vscode/test-electron";

async function main(): Promise<void> {
  const root = path.resolve(__dirname, "../../..");
  const bin = path.join(root, "testdata/bin");
  const workspace = path.join(root, "testdata/workspace");
  await runTests({
    version: process.env["IGRIS_TEST_VSCODE"] ?? "1.100.0",
    extensionDevelopmentPath: root,
    extensionTestsPath: path.join(root, "out/test/vscode/suite/index"),
    launchArgs: [workspace, "--disable-extensions", "--disable-workspace-trust"],
    // VS Code rewrites PATH for the extension host, so the suite puts the
    // stand-in first itself (suite/activation.test.ts).
    extensionTestsEnv: { IGRIS_TEST_BIN: bin, FAKE_IGRIS_VERSION: "1.8.1", FAKE_IGRIS_STATUS: path.join(root, "testdata/status.json") },
  });
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
