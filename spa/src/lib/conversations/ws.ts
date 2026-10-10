// spa/src/lib/conversations/ws.ts — one connection to the conversation stream (U1 spec §8.2 "Live stream"; U3 plan D4).
// `GET /ws/conversations/claude/{session_id}?after=<cursor>&turns=N&ticket=…`. A FRESH one-time ticket on every connect
// (a ticket is spent by the upgrade); frames `{type, seq, value}` with `seq` starting at 1 and contiguous per connection,
// so a gap, a repeat or a first frame that is not 1 retires the socket and the owner reconnects. This module only
// carries frames: what they mean, and the reconnect loop around it, are the store's.
import { fetchWsTicket, hostWsUrl } from '../host-api'
import type { Frame } from './types'

export interface ConversationSocketOptions {
  hostId: string
  sessionId: string
  /** Resume from this cursor (the daemon answers with the catch-up, or a snapshot when it is stale). */
  cursor?: string
  /** The window size of every snapshot on this connection. */
  turns?: number
  onOpen?: () => void
  onFrame: (frame: Frame) => void
  /** The connection ended: `gap` when this side retired it for a bad `seq`, `failed` when it never opened. Once. */
  onClose: (why: { gap: boolean; failed: boolean }) => void
  /** How long the ticket and the upgrade may take before the attempt is given up as failed. */
  connectTimeoutMs?: number
  /** Tests. */
  WebSocketImpl?: typeof WebSocket
  getTicket?: (hostId: string) => Promise<string>
}

export interface ConversationSocket {
  /** Ends the connection; no callback fires after it. Safe to call more than once. */
  close: () => void
}

export const CONNECT_TIMEOUT_MS = 15_000

export function openConversationSocket(opts: ConversationSocketOptions): ConversationSocket {
  let closed = false
  let ended = false
  let ws: WebSocket | null = null
  let opened = false
  let last = 0

  const end = (why: { gap: boolean; failed: boolean }) => {
    if (ended || closed) return
    ended = true
    opts.onClose(why)
  }

  void (async () => {
    const limit = opts.connectTimeoutMs ?? CONNECT_TIMEOUT_MS
    let ticket: string
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      // a ticket request over a half-open connection neither answers nor fails
      ticket = await Promise.race([
        (opts.getTicket ?? fetchWsTicket)(opts.hostId),
        new Promise<never>((_r, rej) => { timer = setTimeout(() => rej(new Error('ticket timeout')), limit) }),
      ])
    } catch {
      end({ gap: false, failed: true })
      return
    } finally {
      clearTimeout(timer)
    }
    if (closed) return
    let url: string
    try {
      const u = new URL(hostWsUrl(opts.hostId, `/ws/conversations/claude/${encodeURIComponent(opts.sessionId)}`))
      u.searchParams.set('ticket', ticket)
      if (opts.cursor) u.searchParams.set('after', opts.cursor)
      if (opts.turns !== undefined) u.searchParams.set('turns', String(opts.turns))
      url = u.toString()
    } catch {
      end({ gap: false, failed: true })
      return
    }
    const Impl = opts.WebSocketImpl ?? WebSocket
    const sock = new Impl(url)
    ws = sock
    // ... and so can the upgrade: a socket that has not opened by the deadline is closed (reported as failed)
    const openTimer = setTimeout(() => { if (!opened) { try { sock.close() } catch { /* already closing */ } } }, limit)
    sock.onopen = () => {
      clearTimeout(openTimer)
      if (closed || ended) return
      opened = true
      opts.onOpen?.()
    }
    sock.onmessage = (ev: MessageEvent) => {
      if (closed || ended) return
      let frame: Frame
      try {
        frame = JSON.parse(String(ev.data)) as Frame
      } catch {
        return // not JSON: nothing to apply, and not a reason to drop the connection
      }
      if (typeof frame?.type !== 'string' || typeof frame.seq !== 'number') return
      if (frame.seq !== last + 1) { // a gap, a repeat, or a first frame that is not 1: the stream cannot be trusted
        end({ gap: true, failed: false })
        try { sock.close() } catch { /* already closing */ }
        return
      }
      last = frame.seq
      opts.onFrame(frame)
    }
    sock.onerror = () => { /* the close that follows reports it */ }
    sock.onclose = () => { clearTimeout(openTimer); end({ gap: false, failed: !opened }) }
  })()

  return {
    close: () => {
      if (closed) return
      closed = true
      try { ws?.close() } catch { /* already closing */ }
    },
  }
}
