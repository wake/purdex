// spa/src/lib/conversations/asks.ts — the open questions of a conversation, as the dock draws them (U3 plan D8, spec §7).
// The source is the conversation WebSocket's approvals (NOT the host-wide approval store, which drops the hook kinds so the
// app-wide dialog never shows them). Only `hook_ask` (an AskUserQuestion the native dialog is showing) is a dock card;
// `hook_permission` is P8b. Everything read from the wire is untrusted: a field of the wrong type reads as absent.
import { decideApproval } from '../team/approval-api'
import { clientDescriptor } from '../team/client-label'
import type { ConversationApproval } from './types'

export interface AskOption { label: string; description?: string }
export interface AskQuestion { question: string; header?: string; multiple: boolean; options: AskOption[] }

export interface OpenAsk {
  /** The approval id: a card is bound to it (a card for another id is another card). */
  id: string
  toolUseId: string
  questions: AskQuestion[]
  /** No Purdex mod can take an answer: the card is read-only and says so. */
  terminalOnly: boolean
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

function readQuestion(v: unknown): AskQuestion | null {
  if (!isRecord(v) || typeof v.question !== 'string') return null
  const options: AskOption[] = []
  if (Array.isArray(v.options)) {
    for (const o of v.options) {
      if (isRecord(o) && typeof o.label === 'string' && !options.some((x) => x.label === o.label)) options.push({ label: o.label, ...(typeof o.description === 'string' ? { description: o.description } : {}) })
    }
  }
  // AskUserQuestion's own input calls the flag `multiSelect`; the conversation model calls it `multiple`.
  const multiple = v.multiSelect === true || v.multiple === true
  return { question: v.question, ...(typeof v.header === 'string' ? { header: v.header } : {}), multiple, options }
}

/** One approval as an ask card, or null when it is not an open `hook_ask` with at least one readable question. */
export function parseAsk(a: ConversationApproval): OpenAsk | null {
  // The WebSocket holds only open approvals; a row that says it is not open is not a card.
  if (a.kind !== 'hook_ask' || (a.state !== undefined && a.state !== 'open') || typeof a.id !== 'string') return null
  const p = a.payload
  if (!isRecord(p) || !Array.isArray(p.questions)) return null
  const questions = p.questions.map(readQuestion).filter((q): q is AskQuestion => q !== null)
  if (questions.length === 0) return null
  return { id: a.id, toolUseId: typeof p.tool_use_id === 'string' ? p.tool_use_id : '', questions, terminalOnly: p.terminal_only === true || questions.length !== p.questions.length || !remotelyAnswerable(questions) }
}

// The daemon keys hook.answers by question text and refuses what it cannot match exactly (decide's answersFit, #2548). A question
// text with U+FFFD (or a lone surrogate, which the daemon folds into it) is never matched, and two questions with one text share a
// key and so cannot be told apart (an unreadable question, dropped on parse, makes the answers misfit the same way); a blank option label cannot be told apart from no answer: such a card is read-only up front rather than refused after the person has answered.
const UNMATCHABLE = /\uFFFD|[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/
export function remotelyAnswerable(questions: readonly AskQuestion[]): boolean {
  const seen = new Set<string>()
  for (const q of questions) {
    if (q.question === '' || UNMATCHABLE.test(q.question) || seen.has(q.question)) return false
    // an option whose label is blank would be an answer the daemon reads as empty
    if (q.options.some((o) => o.label.trim() === '')) return false
    seen.add(q.question)
  }
  return true
}

/**
 * The rule decide and the mod's `answersFit` (hooks/ask.js) apply: exactly one non-empty string per question text and no other key.
 * Kept here so a test can hold `buildAnswers` to the shared fixture; the empty test is JavaScript's `trim`, as on the other two sides.
 */
export function answersFit(questions: readonly { question: string }[], answers: Record<string, unknown> | null): boolean {
  if (questions.length === 0 || answers === null) return false
  const asked = new Set(questions.map((q) => q.question))
  if (asked.has('')) return false
  const keys = Object.keys(answers)
  return keys.length === asked.size && keys.every((k) => asked.has(k) && typeof answers[k] === 'string' && (answers[k] as string).trim() !== '')
}

/** The open asks of the conversation's approvals, oldest first (the order the daemon holds them in). */
export function openAsks(approvals: readonly ConversationApproval[]): OpenAsk[] {
  const out: OpenAsk[] = []
  for (const a of approvals) {
    const ask = parseAsk(a)
    if (ask) out.push(ask)
  }
  return out
}

/** One question's pick: the options chosen, and the 「其他」 free text (empty when unused). */
export interface Pick { chosen: readonly string[]; other: string }

/**
 * The `hook.answers` map: question text → answer. A multi-select joins its answers with ", " (the daemon's form), the
 * free text standing in for / added to the chosen options. Null when any question has no answer yet.
 */
export function buildAnswers(questions: readonly AskQuestion[], picks: readonly Pick[]): Record<string, string> | null {
  const answers: Record<string, string> = {}
  for (let i = 0; i < questions.length; i++) {
    const q = questions[i]
    const pick = picks[i]
    const other = pick?.other.trim() ?? ''
    const parts = [...(pick?.chosen ?? []).filter((c) => q.options.some((o) => o.label === c)), ...(other === '' ? [] : [other])]
    if (parts.length === 0) return null
    // A single-select answers with one thing: the free text wins over a pick it contradicts.
    answers[q.question] = q.multiple ? parts.join(', ') : (other === '' ? parts[0] : other)
  }
  return answers
}

// ---- the reply in words (ask-chat spec §2.1; the daemon enforces the same, this is the early no) ----

export const REPLY_MAX_RUNES = 4000

export type ReplyRefusal = 'empty' | 'too_long' | 'bad_characters'

// Refused, as the daemon does: control characters but \n and \t, U+2028/2029, and every Unicode format character (Cf: the bidi
// controls, U+200B, U+FEFF, U+0600…) except ZWNJ / ZWJ and the emoji tag characters. The Cf test is the Unicode property, not a
// hand-kept list, so the two sides cannot drift on a character neither listed.
const FORMAT_CHAR = /^\p{Cf}$/u
function forbidden(ch: string): boolean {
  const cp = ch.codePointAt(0)!
  if (cp === 0x09 || cp === 0x0a) return false
  if (cp < 0x20 || (cp >= 0x7f && cp <= 0x9f) || cp === 0x2028 || cp === 0x2029) return true
  if (cp === 0x200c || cp === 0x200d || (cp >= 0xe0020 && cp <= 0xe007f)) return false
  return FORMAT_CHAR.test(ch)
}

/** The trimmed reply, or why it cannot be sent. */
export function checkReply(text: string): { ok: true; text: string } | { ok: false; reason: ReplyRefusal } {
  const t = text.trim()
  if (t === '') return { ok: false, reason: 'empty' }
  if ([...t].length > REPLY_MAX_RUNES) return { ok: false, reason: 'too_long' }
  if ([...t].some(forbidden)) return { ok: false, reason: 'bad_characters' }
  return { ok: true, text: t }
}

// ---- deciding ----

export type AskDecideResult =
  | { ok: true }
  /** Somebody (the terminal, another window) closed it first, or it is gone: the card locks. */
  | { ok: false; reason: 'changed' }
  | { ok: false; reason: 'terminal_only' }
  /** The daemon refused the answer as malformed (400 bad_request); `message` is its words, and the card stays open to be fixed. */
  | { ok: false; reason: 'rejected'; message: string }
  | { ok: false; reason: 'network' | 'failed' }

async function decide(hostId: string, id: string, body: { decision: 'approve' | 'deny'; hook: { answers?: Record<string, string>; message?: string } }): Promise<AskDecideResult> {
  try {
    await decideApproval(hostId, id, { ...body, client: await clientDescriptor() })
    return { ok: true }
  } catch (e: unknown) {
    const code = typeof e === 'object' && e !== null && 'code' in e ? String((e as { code: unknown }).code) : ''
    if (code === 'already_decided' || code === 'not_found') return { ok: false, reason: 'changed' }
    if (code === 'terminal_only') return { ok: false, reason: 'terminal_only' }
    if (code === 'bad_request') {
      const detail = typeof e === 'object' && e !== null && 'detail' in e ? String((e as { detail: unknown }).detail) : ''
      if (detail !== '') return { ok: false, reason: 'rejected', message: detail }
    }
    if (code === 'network') return { ok: false, reason: 'network' }
    return { ok: false, reason: 'failed' }
  }
}

/** Answer the ask with the picked answers (`approve` + `hook.answers`). Never retried by itself: the answer is the person's. */
export const answerAsk = (hostId: string, id: string, answers: Record<string, string>): Promise<AskDecideResult> =>
  decide(hostId, id, { decision: 'approve', hook: { answers } })

/** Answer the ask in words (`deny` + `hook.message`); the caller has run `checkReply`. */
export const replyToAsk = (hostId: string, id: string, message: string): Promise<AskDecideResult> =>
  decide(hostId, id, { decision: 'deny', hook: { message } })

// ---- the card, bound to its approval ----

export type CardPhase =
  /** Open and answerable. */
  | { kind: 'open' }
  /** The request closed or changed under the card: no taps, no retry (「題目已變更」). */
  | { kind: 'changed' }
  /** The terminal answered first, and said what (「已在終端機回答：X」). */
  | { kind: 'answered_in_terminal'; text: string }

export const LOCK_CLOSE_MS = 1500
export const ANSWERED_NOTE_MS = 3000

/**
 * What the transcript says the question was answered with, for the 「已在終端機回答」 note: the answers of the question step
 * whose id is the ask's `tool_use_id`, joined the way the dock reads them. Null when the step has no answers (yet).
 */
export function terminalAnswerText(items: readonly { type: string; id: string; question?: { answers?: string[][] } }[], toolUseId: string): string | null {
  if (toolUseId === '') return null
  const step = items.find((i) => i.type === 'step' && i.id === toolUseId)
  const answers = step?.question?.answers
  if (!Array.isArray(answers)) return null
  const flat = answers.map((a) => (Array.isArray(a) ? a.filter((x): x is string => typeof x === 'string').join(', ') : '')).filter((s) => s !== '')
  return flat.length === 0 ? null : flat.join(' / ')
}
