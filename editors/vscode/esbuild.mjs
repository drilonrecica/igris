// Bundles the extension into dist/extension.js (no node_modules in the .vsix)
// and, with --tests, the tests into out/ (decisions.md V19-P1). `vscode` is
// provided by the editor; everything else is Node's builtins.
import { build } from "esbuild";
import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const tests = process.argv.includes("--tests");

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

const common = {
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node20",
  external: ["vscode", "mocha"],
  sourcemap: tests ? "inline" : false,
  logLevel: "warning",
};

if (tests) {
  const entries = walk("src/test").filter((f) => f.endsWith(".test.ts") || f.endsWith("runTest.ts") || f.endsWith("suite/index.ts"));
  await build({ ...common, entryPoints: entries, outdir: "out/test", outbase: "src/test" });
} else {
  await build({ ...common, entryPoints: ["src/extension.ts"], outfile: "dist/extension.js", minify: false, legalComments: "none" });
}
