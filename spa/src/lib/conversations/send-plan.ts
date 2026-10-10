// spa/src/lib/conversations/send-plan.ts — what may be sent (iOS SendPlan rules, U3 plan D7). The daemon applies the same
// rules (internal/module/conversation/submit.go sanitizePrompt) and is the authority; this runs first so a refused draft
// never leaves the box. Pure functions, no I/O.

export const MAX_PROMPT_BYTES = 4000

export type PlanRefusal = 'empty' | 'too_long' | 'needs_terminal'
export type SendPlan = { ok: true; text: string } | { ok: false; refusal: PlanRefusal }

const utf8 = new TextEncoder()
export const utf8Bytes = (s: string): number => utf8.encode(s).length

// bidi embeddings / overrides / isolates, direction marks, zero-width space, word joiner, BOM (not ZWJ / ZWNJ: emoji need them).
// Built from strings so the source holds no invisible characters.
const HIDDEN = new RegExp('[\\u202A-\\u202E\\u2066-\\u2069\\u200E\\u200F\\u061C\\u200B\\u2060\\uFEFF]', 'g')
const LINE_SEP = new RegExp('[\\u2028\\u2029]', 'g')
const CONTROL = new RegExp('[\\u0000-\\u0009\\u000B-\\u001F\\u007F-\\u009F]', 'g')

/** The text as it would be sent: control characters but \n gone, tabs -> 4 spaces, trailing spaces and blank head / tail lines trimmed. */
export function normalizePrompt(raw: string): string {
  const s = raw.replace(/\r\n/g, '\n').replace(/\t/g, '    ').replace(LINE_SEP, '\n').replace(HIDDEN, '').replace(CONTROL, '')
  const lines = s.split('\n').map((l) => l.replace(/ +$/, ''))
  while (lines.length > 0 && lines[0] === '') lines.shift()
  while (lines.length > 0 && lines[lines.length - 1] === '') lines.pop()
  return lines.join('\n')
}

export function planSend(raw: string): SendPlan {
  const text = normalizePrompt(raw)
  if (text === '') return { ok: false, refusal: 'empty' }
  if (utf8Bytes(text) > MAX_PROMPT_BYTES) return { ok: false, refusal: 'too_long' }
  if (text[0] === '/' || text[0] === '!') return { ok: false, refusal: 'needs_terminal' }
  return { ok: true, text }
}

const DESTRUCTIVE: RegExp[] = [
  /\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r|-r\s+-f|-f\s+-r|--recursive\b.*--force|--force\b.*--recursive)/i,
  /\bgit\s+push\b[^\n]*(\s--force(-with-lease)?\b|\s-f\b|\s\+\S)/i,
  /\bgit\s+reset\s+--hard\b/i,
  /\bdrop\s+table\b/i,
  /\bmkfs(\.\w+)?\b/i,
  /\bdd\s+if=/i,
]

/** A line that looks destructive (rm -rf, a forced push, a hard reset, DROP TABLE, mkfs, dd if=): sending needs a second press. */
export function isDestructive(text: string): boolean {
  return text.split('\n').some((line) => DESTRUCTIVE.some((re) => re.test(line)))
}
