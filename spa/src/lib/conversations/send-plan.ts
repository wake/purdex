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
// eslint-disable-next-line no-control-regex -- stripping control characters is the point
const CONTROL =new RegExp('[\\u0000-\\u0009\\u000B-\\u001F\\u007F-\\u009F]', 'g')

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
  /\bgit\s+reset\s+--hard\b/i,
  /\bdrop\s+table\b/i,
  /\bmkfs(\.\w+)?\b/i,
  /\bdd\s+if=/i,
]

/** The flags of a command's argument list: [short letters, long names], up to `--` or the end. */
function flagsOf(args: string[]): { short: string; long: string[] } {
  let short = ''
  const long: string[] = []
  for (const a of args) {
    if (a === '--') break
    if (a.startsWith('--')) long.push(a.split('=')[0])
    else if (a.length > 1 && a[0] === '-') short += a.slice(1)
  }
  return { short, long }
}

// the tokens of one simple command after the program word; `name` may be preceded by sudo / env words
function argsAfter(words: string[], name: string): string[] | null {
  const i = words.findIndex((w) => w === name || w.endsWith(`/${name}`))
  return i < 0 ? null : words.slice(i + 1)
}

function isForcedPush(words: string[]): boolean {
  const g = words.findIndex((w) => w === 'git' || w.endsWith('/git'))
  if (g < 0) return false
  let i = g + 1
  while (i < words.length && words[i].startsWith('-')) i += ['-C', '-c', '--git-dir', '--work-tree'].includes(words[i]) ? 2 : 1
  if (words[i] !== 'push') return false
  const args = words.slice(i + 1)
  const { short, long } = flagsOf(args)
  return short.includes('f') || long.includes('--force') || long.includes('--force-with-lease') || args.some((a) => /^\+\S/.test(a))
}

function isForcedRecursiveRm(words: string[]): boolean {
  const args = argsAfter(words, 'rm')
  if (!args) return false
  const { short, long } = flagsOf(args)
  const recursive = /[rR]/.test(short) || long.includes('--recursive')
  const force = short.includes('f') || long.includes('--force')
  return recursive && force
}

/**
 * A line that looks destructive (rm with both recursive and force flags in any spelling or order, a forced push, a hard
 * reset, DROP TABLE, mkfs, dd if=): sending needs a second press. Backslash continuations are joined first.
 */
export function isDestructive(text: string): boolean {
  const joined = text.replace(/\\\r?\n/g, ' ')
  return joined.split('\n').some((line) =>
    DESTRUCTIVE.some((re) => re.test(line)) ||
    line.split(/[;&|()`]+/).some((cmd) => {
      const words = cmd.trim().split(/\s+/)
      return isForcedRecursiveRm(words) || isForcedPush(words)
    }))
}
