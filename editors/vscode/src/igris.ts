import { execFile } from "node:child_process";
import { checkVersionOutput, type Check } from "./version";

/** Runs `igris --version` from PATH (argv, 5 s timeout, never a shell) and
 * judges it (SPEC §15.9). `env` lets a test point PATH at a stand-in. */
export function checkInstall(env: NodeJS.ProcessEnv = process.env): Promise<Check> {
  return new Promise((resolve) => {
    execFile("igris", ["--version"], { timeout: 5000, env, windowsHide: true }, (err, stdout) => {
      if (err && (err as NodeJS.ErrnoException).code === "ENOENT") {
        resolve(checkVersionOutput(undefined));
      } else {
        resolve(checkVersionOutput(stdout));
      }
    });
  });
}
