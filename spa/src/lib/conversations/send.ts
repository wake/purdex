// spa/src/lib/conversations/send.ts — sending through the session's mod (U3 plan D7): POST …/submit and …/interrupt, what
// each answer means, and the destructive guard. Nothing here types into a terminal. The queue is in send-queue.ts.
//
// At most once: a submit carries a client_msg_id; only `busy` (and a `timeout`, which the daemon withdrew unhanded) did not
// run. `unknown`, a lost connection and `not_owner` MAY have run: the caller never resends those by itself.
import { pinnedHostFetch } from '../host-api'
import { isDestructive } from './send-plan'

/** The daemon answers within 10 s (its own hand-out timeout); the client waits longer so it hears that answer. */
export const SUBMIT_TIMEOUT_MS = 30_000

export type SendOutcome =
  | { kind: 'accepted' }
  | { kind: 'dropped'; reason: string }
  | { kind: 'busy' }
  | { kind: 'timeout' }
  | { kind: 'unknown'; reason?: string }
  | { kind: 'not_owner' }
  | { kind: 'no_mod' }
  | { kind: 'needs_terminal' }
  | { kind: 'invalid'; code: string }
  | { kind: 'rejected'; status: number; code: string }
  | { kind: 'network' }

/** The request may have run (or still run): never resent automatically, settled from the transcript. */
export const mayHaveRun = (o: SendOutcome): boolean => o.kind === 'unknown' || o.kind === 'not_owner' || o.kind === 'network'

export interface SendPort {
  submit(text: string, clientMsgId: string): Promise<SendOutcome>
  interrupt(): Promise<SendOutcome>
}

const path = (sessionId: string, verb: 'submit' | 'interrupt') => `/api/conversations/claude/${encodeURIComponent(sessionId)}/${verb}`

async function post(hostId: string, p: string, body: unknown): Promise<SendOutcome> {
  const ac = new AbortController()
  const timer = setTimeout(() => ac.abort(), SUBMIT_TIMEOUT_MS)
  try {
    const res = await pinnedHostFetch(hostId, p, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: ac.signal })
    let json: { status?: string; reason?: string; error?: string } | null = null
    try { json = await res.json() } catch { /* no JSON body: the status alone */ }
    if (res.ok) return fromStatus(json?.status, json?.reason)
    const code = typeof json?.error === 'string' ? json.error : ''
    if (res.status === 409 && code === 'no_mod') return { kind: 'no_mod' }
    if (res.status === 409 && code === 'not_owner') return { kind: 'not_owner' }
    if (res.status === 400 && code === 'needs_terminal') return { kind: 'needs_terminal' }
    if (res.status === 400 && (code === 'empty_text' || code === 'too_long' || code === 'bad_text')) return { kind: 'invalid', code }
    // an answer from the daemon (400 / 404 / 429 / 503 …): refused before anything was handed to a mod
    return { kind: 'rejected', status: res.status, code }
  } catch {
    return { kind: 'network' } // the request may have reached the daemon: unknown to us
  } finally {
    clearTimeout(timer)
  }
}

function fromStatus(status?: string, reason?: string): SendOutcome {
  switch (status) {
    case 'accepted': return { kind: 'accepted' }
    case 'dropped': return { kind: 'dropped', reason: reason ?? '' }
    case 'busy': return { kind: 'busy' }
    case 'timeout': return { kind: 'timeout' }
    case 'no_mod': return { kind: 'no_mod' }
    default: return reason === 'not_owner' ? { kind: 'not_owner' } : { kind: 'unknown', reason }
  }
}

export function submitPrompt(hostId: string, sessionId: string, text: string, clientMsgId: string): Promise<SendOutcome> {
  return post(hostId, path(sessionId, 'submit'), { text, client_msg_id: clientMsgId })
}

export function interruptSession(hostId: string, sessionId: string): Promise<SendOutcome> {
  return post(hostId, path(sessionId, 'interrupt'), {})
}

export const hostSendPort = (hostId: string, sessionId: string): SendPort => ({
  submit: (text, id) => submitPrompt(hostId, sessionId, text, id),
  interrupt: () => interruptSession(hostId, sessionId),
})

export const newClientMsgId = (): string => crypto.randomUUID()

export interface OutcomeMessage {
  /** An i18n key under deck.send. */
  key: string
  params?: Record<string, string | number>
  tone: 'ok' | 'warn' | 'error'
}

/** The message for an outcome (i18n keys; zh-TW and en in the locale files). */
export function outcomeMessage(o: SendOutcome): OutcomeMessage {
  switch (o.kind) {
    case 'accepted': return { key: 'deck.send.accepted', tone: 'ok' }
    case 'dropped': return { key: 'deck.send.dropped', params: { reason: o.reason || '-' }, tone: 'error' }
    case 'busy': return { key: 'deck.send.busy', tone: 'warn' }
    case 'timeout': return { key: 'deck.send.timeout', tone: 'error' }
    case 'unknown': return { key: 'deck.send.unknown', tone: 'warn' }
    case 'not_owner': return { key: 'deck.send.not_owner', tone: 'warn' }
    case 'no_mod': return { key: 'deck.send.no_mod', tone: 'error' }
    case 'needs_terminal': return { key: 'deck.send.needs_terminal', tone: 'warn' }
    case 'invalid': return { key: o.code === 'too_long' ? 'deck.send.too_long' : 'deck.send.empty', tone: 'error' }
    case 'rejected': return { key: 'deck.send.rejected', params: { code: o.code || String(o.status) }, tone: 'error' }
    case 'network': return { key: 'deck.send.network', tone: 'warn' }
  }
}

/** The second-press guard: a destructive-looking draft is sent only when the same text is submitted again within `windowMs`. */
export const DESTRUCTIVE_WINDOW_MS = 5000

export class DestructiveGuard {
  private pending: { text: string; at: number } | null = null
  private readonly now: () => number
  private readonly windowMs: number

  constructor(now: () => number = Date.now, windowMs = DESTRUCTIVE_WINDOW_MS) {
    this.now = now
    this.windowMs = windowMs
  }

  /** 'confirm': ask 「真的要送出？」 and wait for a second press; 'send': go ahead. */
  check(text: string): 'send' | 'confirm' {
    if (!isDestructive(text)) { this.pending = null; return 'send' }
    const p = this.pending
    if (p && p.text === text && this.now() - p.at <= this.windowMs) { this.pending = null; return 'send' }
    this.pending = { text, at: this.now() }
    return 'confirm'
  }

  reset(): void { this.pending = null }
}
