// A port of internal/textsafe (SPEC §15.9, §16): text igris did not write
// (plan cells, notes, command output) is cleaned of escape sequences, control
// characters and the invisible characters that change what is read before
// the extension draws it. Tested against the Go package's cases
// (src/test/unit/textsafe.test.ts).
//
// JavaScript strings are UTF-16, so the Go rule "an invalid byte becomes
// U+FFFD" becomes "a lone surrogate becomes U+FFFD": Node has already
// replaced the invalid bytes when it decoded the socket's UTF-8.

const ESC = 0x1b;
const BEL = 0x07;
const CSI = 0x9b; // the one-rune form of ESC [

/** Go's unicode.IsControl: C0, DEL and C1. */
function isControl(c: number): boolean {
  return c < 0x20 || (c >= 0x7f && c <= 0x9f);
}

/** The bidi controls, zero-width and other invisible characters
 * internal/textsafe drops; the joiners U+200C and U+200D stay. */
function hidden(c: number): boolean {
  if (
    (c >= 0x202a && c <= 0x202e) ||
    (c >= 0x2066 && c <= 0x2069) ||
    (c >= 0x2061 && c <= 0x2064) ||
    (c >= 0x206a && c <= 0x206f) ||
    (c >= 0xfff9 && c <= 0xfffb) ||
    (c >= 0xe0000 && c <= 0xe007f)
  ) {
    return true;
  }
  return c === 0x200e || c === 0x200f || c === 0x061c || c === 0x200b || c === 0x2060 || c === 0xfeff || c === 0x00ad;
}

/** The length (in UTF-16 units) of the escape sequence s starts with at i,
 * s[i] being ESC. Every byte of a sequence is ASCII, so code units compare
 * as the Go bytes do. */
function escapeLen(s: string, i: number): number {
  if (i + 1 >= s.length) return 1;
  const next = s.charCodeAt(i + 1);
  switch (next) {
    case 0x5b: // [
      return 2 + csiLen(s, i + 2);
    case 0x5d: // ] OSC
    case 0x50: // P DCS
    case 0x58: // X SOS
    case 0x5e: // ^ PM
    case 0x5f: // _ APC
      // A string up to BEL or ST (ESC \); one that is never closed ends at
      // the line's end rather than eating the rest.
      for (let j = i + 2; j < s.length; j++) {
        const c = s.charCodeAt(j);
        if (c === BEL) return j + 1 - i;
        if (c === ESC && j + 1 < s.length && s.charCodeAt(j + 1) === 0x5c) return j + 2 - i;
        if (c === 0x0a) return j - i;
      }
      return s.length - i;
  }
  // Two-character sequences such as ESC c, with optional intermediates.
  let j = i + 1;
  while (j < s.length && s.charCodeAt(j) >= 0x20 && s.charCodeAt(j) <= 0x2f) j++;
  if (j < s.length && s.charCodeAt(j) >= 0x30 && s.charCodeAt(j) <= 0x7e) j++;
  return j - i;
}

/** The length of a CSI sequence's parameters and final byte from i. */
function csiLen(s: string, i: number): number {
  for (let j = i; j < s.length; j++) {
    const c = s.charCodeAt(j);
    if (c >= 0x40 && c <= 0x7e) return j + 1 - i;
    if (c < 0x20 || c > 0x7e) return j - i; // not part of a sequence: stop before it
  }
  return s.length - i;
}

/** Removes escape sequences, every control character except newline and
 * tab, and the invisible format characters (internal/textsafe.Clean). */
export function clean(s: string): string {
  let out = "";
  for (let i = 0; i < s.length; ) {
    const c = s.codePointAt(i)!;
    const size = c > 0xffff ? 2 : 1;
    if (c === ESC) {
      i += escapeLen(s, i);
    } else if (c === CSI) {
      i += 1 + csiLen(s, i + 1);
    } else if (c === 0x0a || c === 0x09) {
      out += s[i];
      i++;
    } else if (isControl(c) || hidden(c)) {
      i += size;
    } else if (c >= 0xd800 && c <= 0xdfff) {
      out += "�"; // a lone surrogate
      i++;
    } else {
      out += String.fromCodePoint(c);
      i += size;
    }
  }
  return out;
}

/** clean for text shown on one line: newlines and tabs become spaces. */
export function line(s: string | undefined): string {
  if (s === undefined) return "";
  return clean(s).replace(/[\n\t]/g, " ");
}

/** Go's textsafe.HasControl: a control character other than tab. */
export function hasControl(s: string): boolean {
  for (const ch of s) {
    const c = ch.codePointAt(0)!;
    if (c !== 0x09 && isControl(c)) return true;
  }
  return false;
}

/** line, cut to at most n characters with … marking the cut. */
export function short(s: string | undefined, n: number): string {
  const one = [...line(s)];
  return one.length <= n ? one.join("") : one.slice(0, n - 1).join("") + "…";
}
