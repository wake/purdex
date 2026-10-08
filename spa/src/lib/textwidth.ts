// spa/src/lib/textwidth.ts — the product-defined weight of a text (team-label spec D-L1/D-L2): East Asian wide
// characters and emoji in the wide ranges weigh 2, combining marks and zero-width characters (ZWJ, variation
// selectors, emoji modifiers) 0, everything else 1; counted per code point. It mirrors `internal/textwidth` in the
// daemon, and both read testdata/textwidth/cases.json, so the two cannot drift. It makes no promise about any
// terminal's columns: its job is to keep a label about five Chinese characters long.

const COMBINING = /^[\p{Mn}\p{Me}]$/u

/** The weight of one code point. */
export function runeWidth(cp: number): number {
  if (
    (cp >= 0x200b && cp <= 0x200f) || cp === 0xfe0f || (cp >= 0x1f3fb && cp <= 0x1f3ff) ||
    (cp >= 0xd800 && cp <= 0xdfff ? false : COMBINING.test(String.fromCodePoint(cp)))
  ) return 0
  if (
    (cp >= 0x1100 && cp <= 0x115f) ||
    (cp >= 0x2e80 && cp <= 0xa4cf) ||
    (cp >= 0xac00 && cp <= 0xd7a3) ||
    (cp >= 0xf900 && cp <= 0xfaff) ||
    (cp >= 0xfe30 && cp <= 0xfe6f) ||
    (cp >= 0xff00 && cp <= 0xff60) ||
    (cp >= 0xffe0 && cp <= 0xffe6) ||
    (cp >= 0x1f300 && cp <= 0x1f64f) ||
    (cp >= 0x1f680 && cp <= 0x1f6ff) ||
    (cp >= 0x1f900 && cp <= 0x1faff) ||
    (cp >= 0x20000 && cp <= 0x3fffd)
  ) return 2
  return 1
}

/** The weight of a string: the sum over its code points (never `.length`, which counts UTF-16 units). */
export function cellWidth(s: string): number {
  let n = 0
  for (const ch of s) n += runeWidth(ch.codePointAt(0)!)
  return n
}
