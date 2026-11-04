// The activation check (SPEC §15.9): `igris --version` against the lowest
// igris whose commands and snapshot fields the extension relies on.

/** The lowest igris the extension accepts (SPEC §15.9, §19). */
export const MIN_IGRIS = "1.8.0";

export type Check = { ok: true; version: string } | { ok: false; line: string };

const ESCAPES = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b./g;
const CONTROLS = /[\x00-\x1f\x7f-\x9f]/g;

/** What `--version` printed, made safe to show on one line: escape sequences
 * and control characters gone, cut to 40 characters. */
export function clean(s: string): string {
  const one = s.replace(ESCAPES, "").replace(/[\n\t]/g, " ").replace(CONTROLS, "").trim();
  return [...one].slice(0, 40).join("");
}

/** Compares X.Y.Z of `v` with the minimum; `-…` and `+…` are ignored, so a
 * pre-release or pseudo-version of 1.8.0 counts as 1.8.0. */
export function parseVersion(v: string): [number, number, number] | undefined {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.+-]*)?(?:\+[0-9A-Za-z.-]*)?$/.exec(v);
  if (!m) return undefined;
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

function atLeast(a: [number, number, number], b: [number, number, number]): boolean {
  for (let i = 0; i < 3; i++) {
    if (a[i]! !== b[i]!) return a[i]! > b[i]!;
  }
  return true;
}

/** The refusal line for `found` (SPEC §15.9). */
export function refusal(found: string): string {
  return `Igris needs igris ${MIN_IGRIS} or newer (found ${found}); install it and reload the window.`;
}

/** Judges the output of `igris --version` (`undefined`: no igris in PATH).
 * `dev`, a build without a version, is accepted. */
export function checkVersionOutput(output: string | undefined, min: string = MIN_IGRIS): Check {
  if (output === undefined) return { ok: false, line: refusal("none in PATH") };
  const line = output.split("\n", 1)[0] ?? "";
  const words = line.trim().split(/\s+/);
  const second = words.length >= 2 ? words[1]! : "";
  if (second === "dev") return { ok: true, version: "dev" };
  const got = parseVersion(second);
  const want = parseVersion(min);
  if (!got || !want || words[0] !== "igris" || !atLeast(got, want)) {
    // "igris 1.7.9" is quoted as its version; anything else as it was printed.
    const shown = words[0] === "igris" && second !== "" ? second : output;
    return { ok: false, line: refusal(clean(shown) || "nothing") };
  }
  return { ok: true, version: second };
}
