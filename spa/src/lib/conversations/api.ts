// spa/src/lib/conversations/api.ts — the conversation REST calls (U1 spec §8.2; U3 plan D4). Pinned to the pane's host:
// a host this device does not have is a rejection, never a request to another daemon (`pinnedHostFetch`).
import { pinnedHostFetch } from '../host-api'
import type { ConversationItem, Increment, Snapshot } from './types'

/** An error answer of the conversation API: the HTTP status and the daemon's `{error}` code ('' when there is none). */
export class ConversationApiError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string) {
    super(`conversation api: ${status} ${code}`)
    this.name = 'ConversationApiError'
    this.status = status
    this.code = code
  }
}

/** Codes that mean "this conversation cannot be read (yet)", as the unreadable states of U3 D11 name them. */
export const UNREADABLE_CODES = ['not_found', 'provider_unsupported'] as const

const base = (sessionId: string) => `/api/conversations/claude/${encodeURIComponent(sessionId)}`

async function readJson<T>(res: Response): Promise<T> {
  if (res.ok) return (await res.json()) as T
  let code = ''
  try {
    const body = await res.json()
    if (body && typeof body.error === 'string') code = body.error
  } catch { /* an error answer with no JSON body: the status alone */ }
  throw new ConversationApiError(res.status, code)
}

export interface SnapshotOptions {
  /** How many turns (daemon default 20, 1–200). */
  turns?: number
  /** A `Turn.index`: the window is the turns before it (paging toward older). */
  before?: number
  /** An item id: the window holding that item's turn (a jump back to a remembered reading position). */
  around?: string
  signal?: AbortSignal
}

export function fetchConversationSnapshot(hostId: string, sessionId: string, opts: SnapshotOptions = {}): Promise<Snapshot> {
  const q = new URLSearchParams()
  if (opts.turns !== undefined) q.set('turns', String(opts.turns))
  if (opts.before !== undefined) q.set('before', String(opts.before))
  if (opts.around !== undefined) q.set('around', opts.around)
  const qs = q.toString()
  return pinnedHostFetch(hostId, `${base(sessionId)}${qs ? `?${qs}` : ''}`, { signal: opts.signal }).then((r) => readJson<Snapshot>(r))
}

/** The answer to a cursor: the changes after it, or — for a stale cursor — a fresh snapshot marked `reset`. */
export type IncrementAnswer =
  | { kind: 'changes'; increment: Increment }
  | { kind: 'snapshot'; snapshot: Snapshot }

export async function fetchConversationIncrement(hostId: string, sessionId: string, after: string, signal?: AbortSignal): Promise<IncrementAnswer> {
  const res = await pinnedHostFetch(hostId, `${base(sessionId)}?after=${encodeURIComponent(after)}`, { signal })
  const body = await readJson<Increment & Partial<Snapshot>>(res)
  return body.reset === true && body.conversation
    ? { kind: 'snapshot', snapshot: body as Snapshot }
    : { kind: 'changes', increment: body }
}

export interface SubagentAnswer {
  items: ConversationItem[]
  /** A read error cut the list short: what is here is true, there may be more. */
  partial: boolean
}

export function fetchConversationSubagent(hostId: string, sessionId: string, agentId: string, signal?: AbortSignal): Promise<SubagentAnswer> {
  return pinnedHostFetch(hostId, `${base(sessionId)}/subagents/${encodeURIComponent(agentId)}`, { signal })
    .then((r) => readJson<{ items?: ConversationItem[]; partial?: boolean }>(r))
    .then((b) => ({ items: b.items ?? [], partial: b.partial === true }))
}
