// spa/src/stores/useConversationStore.ts — the conversations this device follows (U3 plan D4). Keyed by
// `${hostId}\0${sessionId}`; each entry is the pure document of `lib/conversations/model` plus the connection's status.
//
// Ownership: a pane holds a conversation with `acquire` and lets go with the function it returns. The stream is ONE per
// conversation, ref-counted by its holders, and closed 30 s after the last one lets go (a tab switch unmounts the pane
// and mounts it again within that time without a reconnect); the entry is dropped with it.
//
// The loop for one conversation: a REST read first (the snapshot, or the increment from the held cursor — it is also what
// tells "the transcript is not there" apart from "the host is down", which a browser WebSocket cannot), then the stream
// from that cursor. A close, a `seq` gap or a failed read reconnects with backoff; every connect is a fresh ticket.
// Paging (`loadBefore`) and jumps (`jumpTo`) are one-at-a-time navigations: a new one aborts the earlier, and the model's
// generation fence drops an answer that a snapshot or a jump has outdated.
import { create } from 'zustand'
import {
  ConversationApiError, fetchConversationIncrement, fetchConversationSnapshot, fetchConversationSubagent, UNREADABLE_CODES,
  type SubagentAnswer,
} from '../lib/conversations/api'
import {
  applyApprovalOp, applyApprovals, applyAround, applyCapabilities, applyChanges, applyHeader, applyOlderPage, applySnapshot,
  emptyDoc, firstIndex, type ConversationDoc,
} from '../lib/conversations/model'
import { openConversationSocket, type ConversationSocket } from '../lib/conversations/ws'
import type { Frame } from '../lib/conversations/types'

export type ConversationStatus = 'loading' | 'live' | 'reconnecting' | 'unreadable' | 'error'

export interface SubagentState {
  state: 'loading' | 'ready' | 'error'
  answer?: SubagentAnswer
}

export interface ConversationEntry {
  doc: ConversationDoc
  status: ConversationStatus
  /** Why it is unreadable (`not_found` | `provider_unsupported`), else ''. */
  reason: string
  /** An older page is being read. */
  paging: boolean
  subagents: Record<string, SubagentState>
}

/** Timings (ms); exported so tests can say what they wait for. */
export const CLOSE_AFTER_MS = 30_000
export const BACKOFF_START_MS = 1_000
export const BACKOFF_MAX_MS = 15_000
/** A transcript that is not there yet may appear (a session that has only just started): ask again this often. */
export const NOT_FOUND_RETRY_MS = 5_000
/** A read (snapshot or increment) that has not answered by then is given up on and retried with backoff. */
export const READ_TIMEOUT_MS = 15_000
export const PAGE_TURNS = 20
export const WINDOW_TURNS = 20

export const conversationKey = (hostId: string, sessionId: string): string => `${hostId}\0${sessionId}`

interface Runtime {
  hostId: string
  sessionId: string
  refs: number
  closeTimer: ReturnType<typeof setTimeout> | null
  retryTimer: ReturnType<typeof setTimeout> | null
  socket: ConversationSocket | null
  /** Aborts the connect-time read. */
  connectAbort: AbortController | null
  /** The one navigation (page or jump) in flight. */
  nav: AbortController | null
  backoff: number
  stopped: boolean
  /** Bumped by every connect attempt: a callback of an earlier attempt's socket is ignored. */
  attempt: number
}

interface ConversationState {
  byKey: Record<string, ConversationEntry>
  acquire: (hostId: string, sessionId: string) => () => void
  loadBefore: (hostId: string, sessionId: string) => Promise<void>
  /** Resolves 'ok', or the API's error code (`item_not_found`, `item_not_shown`, …) when the jump could not be made. */
  jumpTo: (hostId: string, sessionId: string, itemId: string) => Promise<string>
  /** Leaves a jump: the newest window again. */
  returnToLive: (hostId: string, sessionId: string) => Promise<void>
  loadSubagent: (hostId: string, sessionId: string, agentId: string) => Promise<void>
}

const runtimes = new Map<string, Runtime>()

const freshEntry = (): ConversationEntry => ({ doc: emptyDoc(), status: 'loading', reason: '', paging: false, subagents: {} })

export const useConversationStore = create<ConversationState>()((set, get) => {
  const patch = (key: string, f: (e: ConversationEntry) => Partial<ConversationEntry>) =>
    set((s) => {
      const cur = s.byKey[key]
      if (!cur) return s // closed meanwhile
      return { byKey: { ...s.byKey, [key]: { ...cur, ...f(cur) } } }
    })
  const patchDoc = (key: string, f: (d: ConversationDoc) => ConversationDoc) => patch(key, (e) => ({ doc: f(e.doc) }))

  const clearTimers = (rt: Runtime) => {
    if (rt.retryTimer) clearTimeout(rt.retryTimer)
    rt.retryTimer = null
  }

  const stop = (key: string) => {
    const rt = runtimes.get(key)
    if (!rt) return
    rt.stopped = true
    clearTimers(rt)
    if (rt.closeTimer) clearTimeout(rt.closeTimer)
    rt.socket?.close()
    rt.connectAbort?.abort()
    rt.nav?.abort()
    runtimes.delete(key)
    set((s) => {
      if (!(key in s.byKey)) return s
      const { [key]: _gone, ...rest } = s.byKey
      return { byKey: rest }
    })
  }

  const scheduleRetry = (key: string, rt: Runtime, delay: number) => {
    clearTimers(rt)
    rt.retryTimer = setTimeout(() => { rt.retryTimer = null; void connect(key, rt) }, delay)
  }

  const reconnectLater = (key: string, rt: Runtime, immediate = false) => {
    if (rt.stopped) return
    patch(key, (e) => (e.status === 'unreadable' ? {} : { status: 'reconnecting' }))
    const delay = immediate ? 0 : rt.backoff
    rt.backoff = Math.min(rt.backoff * 2, BACKOFF_MAX_MS)
    scheduleRetry(key, rt, delay)
  }

  const onFrame = (key: string, rt: Runtime, frame: Frame) => {
    if (rt.stopped) return
    switch (frame.type) {
      case 'conversation.snapshot':
        patchDoc(key, (d) => applySnapshot(d, (frame as Extract<Frame, { type: 'conversation.snapshot' }>).value))
        break
      case 'conversation.changes':
        patchDoc(key, (d) => applyChanges(d, (frame as Extract<Frame, { type: 'conversation.changes' }>).value))
        break
      case 'conversation.header': {
        const v = (frame as Extract<Frame, { type: 'conversation.header' }>).value
        patchDoc(key, (d) => applyHeader(d, v.header, v.cursor))
        break
      }
      case 'conversation.capabilities':
        patchDoc(key, (d) => applyCapabilities(d, (frame as Extract<Frame, { type: 'conversation.capabilities' }>).value.capabilities))
        break
      case 'approvals.snapshot':
        patchDoc(key, (d) => applyApprovals(d, (frame as Extract<Frame, { type: 'approvals.snapshot' }>).value.approvals ?? []))
        break
      case 'approval': {
        const v = (frame as Extract<Frame, { type: 'approval' }>).value
        patchDoc(key, (d) => applyApprovalOp(d, v.op, v.approval))
        break
      }
      default: // `conversation.reset` announces a snapshot that follows; anything else is a later version's
        break
    }
    rt.backoff = BACKOFF_START_MS
    patch(key, (e) => (e.status === 'live' ? {} : { status: 'live', reason: '' }))
  }

  async function connect(key: string, rt: Runtime): Promise<void> {
    if (rt.stopped) return
    const attempt = ++rt.attempt
    rt.socket?.close()
    rt.socket = null
    rt.connectAbort?.abort()
    const ac = new AbortController()
    rt.connectAbort = ac
    // A half-open connection never answers and never fails: without a deadline the loop would wait for it forever.
    let timedOut = false
    const deadline = setTimeout(() => { timedOut = true; ac.abort() }, READ_TIMEOUT_MS)
    try {
      const cursor = get().byKey[key]?.doc.cursor ?? ''
      if (cursor) {
        const ans = await fetchConversationIncrement(rt.hostId, rt.sessionId, cursor, ac.signal)
        if (rt.stopped || attempt !== rt.attempt) return
        patchDoc(key, (d) => (ans.kind === 'snapshot' ? applySnapshot(d, ans.snapshot) : applyChanges(d, ans.increment)))
      } else {
        const snap = await fetchConversationSnapshot(rt.hostId, rt.sessionId, { turns: WINDOW_TURNS, signal: ac.signal })
        if (rt.stopped || attempt !== rt.attempt) return
        patchDoc(key, (d) => applySnapshot(d, snap))
      }
    } catch (err) {
      if (rt.stopped || attempt !== rt.attempt) return
      if (ac.signal.aborted && !timedOut) return // superseded by the next attempt or by stop
      if (err instanceof ConversationApiError && (UNREADABLE_CODES as readonly string[]).includes(err.code)) {
        patch(key, () => ({ status: 'unreadable', reason: err.code }))
        // a transcript that is not there may still appear; a provider this daemon does not read never will
        if (err.code === 'not_found') scheduleRetry(key, rt, NOT_FOUND_RETRY_MS)
        return
      }
      // a stale cursor the daemon cannot parse, or a host that is down or busy: start again with backoff
      if (err instanceof ConversationApiError && err.code === 'bad_cursor') patchDoc(key, (d) => ({ ...d, cursor: '' }))
      patch(key, () => ({ status: 'error' }))
      reconnectLater(key, rt)
      return
    } finally {
      clearTimeout(deadline)
    }
    const live = get().byKey[key]
    if (!live) return
    rt.socket = openConversationSocket({
      hostId: rt.hostId,
      sessionId: rt.sessionId,
      cursor: live.doc.cursor || undefined,
      turns: WINDOW_TURNS,
      onFrame: (f) => { if (attempt === rt.attempt) onFrame(key, rt, f) },
      onClose: (why) => {
        if (attempt !== rt.attempt || rt.stopped) return
        rt.socket = null
        // The approvals are the current connection's atomic set: with the connection gone they are unknown, and an
        // approval closed meanwhile (in the terminal, by another client) must not stay answerable. The next connection's
        // `approvals.snapshot` brings the real set back.
        patchDoc(key, (d) => (d.approvals.length ? applyApprovals(d, []) : d))
        reconnectLater(key, rt, why.gap)
      },
    })
  }

  return {
    byKey: {},

    acquire: (hostId, sessionId) => {
      const key = conversationKey(hostId, sessionId)
      let rt = runtimes.get(key)
      if (!rt) {
        rt = {
          hostId, sessionId, refs: 0, closeTimer: null, retryTimer: null, socket: null, connectAbort: null, nav: null,
          backoff: BACKOFF_START_MS, stopped: false, attempt: 0,
        }
        runtimes.set(key, rt)
        set((s) => ({ byKey: { ...s.byKey, [key]: freshEntry() } }))
        void connect(key, rt)
      }
      if (rt.closeTimer) { clearTimeout(rt.closeTimer); rt.closeTimer = null }
      rt.refs += 1
      const mine = rt
      let released = false
      return () => {
        if (released) return
        released = true
        mine.refs -= 1
        if (mine.refs > 0 || mine.stopped) return
        mine.closeTimer = setTimeout(() => { if (mine.refs === 0 && runtimes.get(key) === mine) stop(key) }, CLOSE_AFTER_MS)
      }
    },

    loadBefore: async (hostId, sessionId) => {
      const key = conversationKey(hostId, sessionId)
      const rt = runtimes.get(key)
      const cur = get().byKey[key]
      if (!rt || !cur || cur.paging || !cur.doc.hasMoreBefore) return
      const before = firstIndex(cur.doc)
      if (before === null) return
      rt.nav?.abort()
      const ac = new AbortController()
      rt.nav = ac
      const generation = cur.doc.generation
      patch(key, () => ({ paging: true }))
      try {
        const snap = await fetchConversationSnapshot(hostId, sessionId, { turns: PAGE_TURNS, before, signal: ac.signal })
        if (ac.signal.aborted) return
        patchDoc(key, (d) => applyOlderPage(d, snap, generation))
      } catch {
        /* the page stays unloaded; the holder asks again when the reader reaches the top again */
      } finally {
        if (rt.nav === ac) rt.nav = null
        if (runtimes.get(key) === rt) patch(key, () => ({ paging: false })) // also when a jump aborted it: paging must not stay set
      }
    },

    jumpTo: async (hostId, sessionId, itemId) => {
      const key = conversationKey(hostId, sessionId)
      const rt = runtimes.get(key)
      const cur = get().byKey[key]
      if (!rt || !cur) return 'not_open'
      rt.nav?.abort()
      const ac = new AbortController()
      rt.nav = ac
      const generation = cur.doc.generation
      try {
        const snap = await fetchConversationSnapshot(hostId, sessionId, { turns: WINDOW_TURNS, around: itemId, signal: ac.signal })
        if (ac.signal.aborted) return 'superseded'
        patchDoc(key, (d) => applyAround(d, snap, generation))
        return 'ok'
      } catch (err) {
        if (ac.signal.aborted) return 'superseded' // a real fetch rejects with AbortError when the next navigation aborts it
        return err instanceof ConversationApiError ? err.code || `http_${err.status}` : 'network'
      } finally {
        if (rt.nav === ac) rt.nav = null
      }
    },

    returnToLive: async (hostId, sessionId) => {
      const key = conversationKey(hostId, sessionId)
      const rt = runtimes.get(key)
      if (!rt || !get().byKey[key]) return
      rt.nav?.abort()
      rt.nav = null
      // Not a second snapshot applied beside the stream (it could overwrite a frame that arrived while it was out): the
      // connect path again, which reads the newest window and opens the stream from its cursor, in that order.
      clearTimers(rt)
      rt.backoff = BACKOFF_START_MS
      patchDoc(key, (d) => ({ ...d, cursor: '' }))
      await connect(key, rt)
    },

    loadSubagent: async (hostId, sessionId, agentId) => {
      const key = conversationKey(hostId, sessionId)
      const cur = get().byKey[key]
      const rt = runtimes.get(key)
      if (!cur || !rt || cur.subagents[agentId]?.state === 'loading' || cur.subagents[agentId]?.state === 'ready') return
      // a request that outlives its conversation (closed after the last holder, then opened again) must not patch the new one
      const setSub = (st: SubagentState) => { if (runtimes.get(key) === rt) patch(key, (e) => ({ subagents: { ...e.subagents, [agentId]: st } })) }
      setSub({ state: 'loading' })
      try {
        const answer = await fetchConversationSubagent(hostId, sessionId, agentId)
        setSub({ state: 'ready', answer })
      } catch {
        setSub({ state: 'error' })
      }
    },
  }
})

/** The entry of one conversation, or undefined when nobody holds it. */
export const selectConversation = (hostId: string, sessionId: string) =>
  (s: Pick<ConversationState, 'byKey'>): ConversationEntry | undefined => s.byKey[conversationKey(hostId, sessionId)]

/** Tests: drop every conversation and its timers and sockets. */
export function resetConversationStore(): void {
  for (const key of [...runtimes.keys()]) {
    const rt = runtimes.get(key)!
    rt.stopped = true
    if (rt.retryTimer) clearTimeout(rt.retryTimer)
    if (rt.closeTimer) clearTimeout(rt.closeTimer)
    rt.socket?.close()
    rt.connectAbort?.abort()
    rt.nav?.abort()
  }
  runtimes.clear()
  useConversationStore.setState({ byKey: {} })
}
