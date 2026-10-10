// spa/src/lib/notification-normalise.ts — the Mac's notification text, by the same rule as the phone push (#2144).
//
// A port of `Normalise` in internal/push/content.go (push spec §5.3): link and image syntax reduced to their text, code fences,
// backticks, bold markers, heading hashes, quote marks and list bullets dropped, every run of whitespace one space, cut at
// `maxRunes` with an ellipsis. internal/push/testdata/normalise.json holds input → expected for both; the Go test and
// notification-normalise.test.ts run the same file, so a change to one side that the other does not follow fails a test.
//
// Written to behave exactly like Go's RE2, not like JS's defaults:
// - `\s` in Go is ASCII `[\t\n\f\r ]`; the classes below say so.
// - Go's `(?m)^` / `$` are line starts / ends at `\n` only; JS's `m` flag also counts `\r`, U+2028 and U+2029. So the
//   anchors are `(?<![^\n])` (start, or right after `\n`) and `(?![^\n])` (end, or right before `\n`).
// - `strings.NewReplacer` is one left-to-right pass, so the markers go in a single regex, not one replace after another
//   (which would join what a removed character had kept apart).
// - `unicode.IsSpace` / `IsControl` / `Cf`: spelled out per code point in `plainChar`.

/** Go's `\s`. */
const WS = '[\\t\\n\\f\\r ]'
const LINE_START = '(?<![^\\n])'
const LINE_END = '(?![^\\n])'

const RE_IMAGE = /!\[([^\]]*)\]\([^)]*\)/g
const RE_LINK = /\[([^\]]*)\]\([^)]*\)/g
const RE_FENCE = new RegExp(`${LINE_START}${WS}*\`\`\`[^\\n]*${LINE_END}`, 'g')
const RE_QUOTE = new RegExp(`${LINE_START}${WS}*>+${WS}?`, 'g')
const RE_HEAD = new RegExp(`${LINE_START}${WS}*#{1,6}${WS}+`, 'g')
const RE_BULLET = new RegExp(`${LINE_START}${WS}*(?:[-*+]|[0-9]+[.)])${WS}+`, 'g')
const RE_MARKERS = /`|\*\*|__/g
const RE_SPACE = new RegExp(`${WS}+`, 'g')

/** Go's `unicode.IsSpace`: ASCII white space, NEL, and every Z category (no-break space, line / paragraph separators…). */
function isGoSpace(c: number): boolean {
  return (c >= 0x09 && c <= 0x0d) || c === 0x20 || c === 0x85 || c === 0xa0 || c === 0x1680
    || (c >= 0x2000 && c <= 0x200a) || c === 0x2028 || c === 0x2029 || c === 0x202f || c === 0x205f || c === 0x3000
}

const RE_DROPPED = /^[\p{Cc}\p{Cf}]$/u

/**
 * Keeps a text honest on a lock screen: every kind of space becomes a plain space, and control and format characters
 * (direction marks such as RLO / LRI / PDI, zero-width marks, the BOM) are dropped, so a title written by a session cannot
 * reorder or hide the words around it.
 */
function plainChars(s: string): string {
  let out = ''
  for (const ch of s) {
    const c = ch.codePointAt(0)!
    if (isGoSpace(c)) out += ' '
    else if (c >= 0xd800 && c <= 0xdfff) out += '\ufffd' // a lone surrogate: Go reads it as invalid UTF-8, which is U+FFFD
    else if (!RE_DROPPED.test(ch)) out += ch
  }
  return out
}

function cutRunes(s: string, max: number): string {
  const runes = Array.from(s)
  return runes.length <= max ? s : runes.slice(0, max).join('') + '…'
}

/** Makes a Markdown text fit a lock screen. Same rule as `Normalise` in internal/push/content.go. */
export function normaliseNotificationText(s: string, maxRunes: number): string {
  s = s.replace(RE_IMAGE, '$1')
  s = s.replace(RE_LINK, '$1')
  s = s.replace(RE_FENCE, '')
  s = s.replace(RE_HEAD, '')
  s = s.replace(RE_QUOTE, '')
  s = s.replace(RE_BULLET, '')
  s = s.replace(RE_MARKERS, '')
  s = plainChars(s)
  s = s.replace(RE_SPACE, ' ').replace(/^ | $/g, '')
  return cutRunes(s, maxRunes)
}

/**
 * A name or title as plain text (the phone push's title rule: `cutRunes` only, after the space / control / format
 * clean-up): NOT Markdown-processed, so a session called `my__session__x` or a tool called `mcp__srv__tool` keeps its name.
 */
export function plainNotificationText(s: string, maxRunes: number): string {
  return cutRunes(plainChars(s), maxRunes)
}

/** The push limits (internal/push/content.go): a body, and a title. */
export const NOTIFICATION_BODY_RUNES = 240
export const NOTIFICATION_TITLE_RUNES = 120
