// spa/src/lib/conversations/send.ts — sending (K-2): the daemon types a message through POST /api/sessions/{code}/submit; interrupt is still the session mod's (POST …/interrupt). What each answer means, and the destructive guard.
// The queue is in send-queue.ts.
//
// At most once: a submit carries a client_msg_id; a `refused` answer did not run anything (except `text_left_in_terminal`). `unknown`, a lost
// connection and `text_left_in_terminal` MAY have run: the caller never resends those by itself. The mod-route kinds (dropped / busy / timeout /
// no_mod / not_owner) are what the INTERRUPT route still answers; no submit produces them any more.
import { pinnedHostFetch } from '../host-api'
import { isDestructive } from './send-plan'

/** The daemon answers within 10 s (its own hand-out timeout); the client waits longer so it hears that answer. */
export const SUBMIT_TIMEOUT_MS = 30_000

export type SendOutcome =
  | { kind: 'accepted' }
  /** The daemon typed the message into the session (K-2): `fill` = the composer was filled and Enter pressed, `keys` = typed without a mod's confirmation. */
  | { kind: 'handed'; mode: 'fill' | 'keys' }
  /** The daemon understood the request and declined it (409 / 429): nothing was sent, except `text_left_in_terminal`. */
  | { kind: 'refused'; reason: string; status: number }
  /** Local: neither the pane nor the session list knows the tmux generation, so nothing is sent. */
  | { kind: 'no_instance' }
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
export const mayHaveRun = (o: SendOutcome): boolean =>
  o.kind === 'unknown' || o.kind === 'network' || (o.kind === 'refused' && o.reason === 'text_left_in_terminal')

export interface SendPort {
  submit(text: string, clientMsgId: string): Promise<SendOutcome>
  interrupt(): Promise<SendOutcome>
  /**
   * How the transcript echoes a message this port handed over, when it is not the terminal's (a row stamped within seconds of the hand-out, source
   * `user`): the sources that count as the echo, and how long after the answer a row may still be taken for it. A worker's message may wait in
   * Nexen's own queue for a whole turn, so its row comes late and may be `queued`.
   */
  echo?: { sources: readonly string[]; afterMs: number }
}

/** Where a typed submit goes: the tmux session code (not the conversation id) and the generation the pane believes in. */
export interface SubmitTarget { code: string; instance: string }

const path = (sessionId: string, verb: 'interrupt') => `/api/conversations/claude/${encodeURIComponent(sessionId)}/${verb}`

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

/**
 * POST /api/sessions/{code}/submit (K-2): the daemon types the message. One answer shape `{outcome?, mode?, reason?, error?}`:
 * 200 handed / unknown, 409 / 429 refused (nothing sent, except text_left_in_terminal), 400 / 404 / 500 / 503 without an outcome (not run).
 */
export async function submitTyped(hostId: string, target: SubmitTarget, text: string, clientMsgId: string): Promise<SendOutcome> {
  const ac = new AbortController()
  const timer = setTimeout(() => ac.abort(), SUBMIT_TIMEOUT_MS)
  try {
    const res = await pinnedHostFetch(hostId, `/api/sessions/${encodeURIComponent(target.code)}/submit`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, signal: ac.signal,
      body: JSON.stringify({ text, client_msg_id: clientMsgId, expected_tmux_instance: target.instance }),
    })
    let json: { outcome?: string; mode?: string; reason?: string; error?: string } | null = null
    try { json = await res.json() } catch { /* no JSON body: the status alone */ }
    const code = typeof json?.error === 'string' && json.error ? json.error : typeof json?.reason === 'string' ? json.reason : ''
    if (res.ok) {
      if (json?.outcome === 'handed') return { kind: 'handed', mode: json.mode === 'keys' ? 'keys' : 'fill' }
      return { kind: 'unknown', ...(json?.reason ? { reason: json.reason } : {}) } // unknown, or an answer we cannot read: it may have run
    }
    if ((res.status === 409 || res.status === 429) && json?.outcome === 'refused') return { kind: 'refused', reason: code, status: res.status }
    if (res.status === 400 && code === 'needs_terminal') return { kind: 'needs_terminal' }
    if (res.status === 400 && (code === 'empty_text' || code === 'too_long' || code === 'bad_text')) return { kind: 'invalid', code }
    // Go's mux answers an unknown route 404 in plain text: a daemon older than the submit route. Ours always carries error / reason.
    if (res.status === 404 && !code) return { kind: 'rejected', status: 404, code: 'daemon_outdated' }
    return { kind: 'rejected', status: res.status, code } // not run
  } catch {
    return { kind: 'network' } // the request may have reached the daemon: unknown to us
  } finally {
    clearTimeout(timer)
  }
}

export function interruptSession(hostId: string, sessionId: string): Promise<SendOutcome> {
  return post(hostId, path(sessionId, 'interrupt'), {})
}

/**
 * The port of a queue made before its pane bound an adapter (a fallback `make`): it can interrupt, and it can NOT submit — the old mod
 * submit route is gone for a terminal pane, and a typed submit needs the pane's target, which only the bound adapter has.
 */
export const unboundSendPort = (hostId: string, sessionId: string): SendPort => ({
  submit: async () => ({ kind: 'rejected', status: 0, code: 'unbound' }),
  interrupt: () => interruptSession(hostId, sessionId),
})

/**
 * The terminal's port (K-2): submit types through the daemon, `target()` is read at each call so a generation stamped later is the one sent;
 * no target sends nothing. Interrupt stays on the mod route.
 */
export const typedSendPort = (hostId: string, sessionId: string, target: () => SubmitTarget | null): SendPort => ({
  submit: async (text, id) => {
    const t = target()
    if (!t || !t.code || !t.instance) return { kind: 'no_instance' }
    return submitTyped(hostId, t, text, id)
  },
  interrupt: () => interruptSession(hostId, sessionId),
})

export const newClientMsgId = (): string => crypto.randomUUID()

export interface OutcomeMessage {
  /** An i18n key under deck.send. */
  key: string
  params?: Record<string, string | number>
  tone: 'ok' | 'warn' | 'error'
}

/** The refusal codes a worker (Nexen) answers that have their own words (`execution.error.*`); any other code is shown as it is. */
const WORKER_REFUSALS = new Set(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])

const REFUSED_REASONS = new Set(['input_not_empty', 'dialog_open', 'pane_busy', 'waiting', 'generation', 'no_owner_yet', 'text_left_in_terminal', 'busy'])

function refusedMessage(o: { reason: string; status: number }): OutcomeMessage {
  if (REFUSED_REASONS.has(o.reason)) return { key: `deck.send.refused.${o.reason}`, tone: o.reason === 'text_left_in_terminal' || o.reason === 'busy' ? 'warn' : 'error' }
  return { key: 'deck.send.refused_other', params: { code: o.reason || String(o.status) }, tone: 'error' }
}

/** The message for an outcome (i18n keys; zh-TW and en in the locale files). */
export function outcomeMessage(o: SendOutcome): OutcomeMessage {
  if (o.kind === 'rejected' && o.code === 'pane_closed') return { key: 'deck.send.pane_closed', tone: 'warn' }
  if (o.kind === 'rejected' && WORKER_REFUSALS.has(o.code)) return { key: `execution.error.${o.code}`, tone: 'error' }
  if (o.kind === 'rejected' && o.code === 'daemon_outdated') return { key: 'deck.send.daemon_outdated', tone: 'error' }
  switch (o.kind) {
    case 'accepted': return { key: 'deck.send.accepted', tone: 'ok' }
    case 'handed': return { key: o.mode === 'keys' ? 'deck.send.handed_keys' : 'deck.send.handed', tone: 'ok' }
    case 'refused': return refusedMessage(o)
    case 'no_instance': return { key: 'deck.send.no_instance', tone: 'error' }
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
