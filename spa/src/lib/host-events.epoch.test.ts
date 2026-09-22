// spa/src/lib/host-events.epoch.test.ts — the connection epoch lives inside the
// transport (spec §4.6). `reconnect` / `reconnectWithTicket` reuse the same
// connection object and the same `onEvent` closure, so a consumer-side epoch
// would reject the *new* socket's frames too; the filter has to sit where each
// socket's handlers are created.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { connectHostEvents } from './host-events'

class FakeSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3
  readyState = FakeSocket.CONNECTING
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = FakeSocket.CLOSED; this.onclose?.() })
  constructor(url: string) { this.url = url }
  /** Deliver a frame the way a real socket would. */
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const sessionsFrame = JSON.stringify({ type: 'sessions', session: '', value: '[]' })

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', class extends FakeSocket {
    constructor(url: string) { super(url); sockets.push(this) }
  })
})

afterEach(() => { vi.unstubAllGlobals() })

describe('connectHostEvents socket epoch', () => {
  it('drops frames from a socket that reconnect superseded', () => {
    const onEvent = vi.fn()
    const conn = connectHostEvents('ws://h/events', onEvent)
    expect(sockets).toHaveLength(1)

    conn.reconnect() // second socket supersedes the first
    expect(sockets).toHaveLength(2)

    sockets[0].emit(sessionsFrame)
    expect(onEvent).not.toHaveBeenCalled()

    sockets[1].emit(sessionsFrame)
    expect(onEvent).toHaveBeenCalledTimes(1)

    conn.close()
  })

  it('still delivers frames on a plain single connection', () => {
    const onEvent = vi.fn()
    const conn = connectHostEvents('ws://h/events', onEvent)
    sockets[0].emit(sessionsFrame)
    expect(onEvent).toHaveBeenCalledTimes(1)
    conn.close()
  })

  it('abandons a connect whose ticket resolved after a newer connect started', async () => {
    let releaseTicket!: (t: string) => void
    const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
    const conn = connectHostEvents('ws://h/events', vi.fn(), undefined, undefined, getTicket)

    conn.reconnectWithTicket('fresh') // supersedes the pending first attempt
    releaseTicket('stale')
    await new Promise((r) => setTimeout(r, 0))

    expect(sockets.filter((s) => s.url.includes('ticket=stale'))).toHaveLength(0)
    expect(sockets.filter((s) => s.url.includes('ticket=fresh'))).toHaveLength(1)

    conn.close()
  })

  // close() retires the socket (#1255 SPA spec §3.4): a host removed from
  // `hostOrder`, or whose endpoint changed, must not hear frames still queued
  // on the old socket — the hook has already torn that entry down.
  it('drops a frame delivered on the socket after close()', () => {
    const onEvent = vi.fn()
    const conn = connectHostEvents('ws://h/events', onEvent)
    const socket = sockets[0]
    conn.close()
    socket.emit(sessionsFrame)
    expect(onEvent).not.toHaveBeenCalled()
  })

  it('close() after a reconnect drops the live socket\'s frames too', () => {
    const onEvent = vi.fn()
    const conn = connectHostEvents('ws://h/events', onEvent)
    conn.reconnect()
    const live = sockets[1]
    conn.close()
    live.emit(sessionsFrame)
    expect(onEvent).not.toHaveBeenCalled()
  })

  it('does not call onClose from the socket\'s own close event after close()', () => {
    const onClose = vi.fn()
    const conn = connectHostEvents('ws://h/events', vi.fn(), onClose)
    const socket = sockets[0]
    conn.close()
    socket.onclose?.() // a late close event of the retired socket
    expect(onClose).not.toHaveBeenCalled()
  })

  it('opens no socket when close() lands while the lazy first ticket is pending', async () => {
    let releaseTicket!: (t: string) => void
    const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
    const onClose = vi.fn()
    const conn = connectHostEvents('ws://h/events', vi.fn(), onClose, undefined, getTicket, false, true)
    conn.reconnect() // the first connection of a lazy transport
    conn.close()
    releaseTicket('late')
    await new Promise((r) => setTimeout(r, 0))
    expect(sockets).toHaveLength(0)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('close() before reconnectWithTicket leaves nothing open', async () => {
    const conn = connectHostEvents('ws://h/events', vi.fn(), undefined, undefined, async () => 'tk', false, true)
    conn.close()
    conn.reconnectWithTicket('fresh')
    await new Promise((r) => setTimeout(r, 0))
    expect(sockets).toHaveLength(0)
  })

  it('close() during a superseded ticket await fires no hand-over', async () => {
    let releaseTicket!: (t: string) => void
    const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
    const conn = connectHostEvents('ws://h/events', vi.fn(), undefined, undefined, getTicket, false, true)
    conn.reconnect()                  // attempt 1 awaits its ticket
    conn.reconnectWithTicket('fresh') // supersedes it; bounces off the `connecting` guard
    conn.close()
    releaseTicket('stale')
    await new Promise((r) => setTimeout(r, 0))
    expect(sockets).toHaveLength(0)
  })

  // A retired socket's `open` event may already be queued when it is retired:
  // it must not reach `onOpen` — the hook would bump the connection generation,
  // mark the host connected and fetch for an entry that is gone (codex R1 P2).
  it('does not call onOpen for an open event queued on a socket retired by close()', () => {
    const onOpen = vi.fn()
    const conn = connectHostEvents('ws://h/events', vi.fn(), undefined, onOpen)
    const socket = sockets[0]
    conn.close()
    socket.onopen?.()
    expect(onOpen).not.toHaveBeenCalled()
  })

  it('does not call onOpen for an open event queued on a socket reconnect superseded; the new one still opens', () => {
    const onOpen = vi.fn()
    const conn = connectHostEvents('ws://h/events', vi.fn(), undefined, onOpen)
    const old = sockets[0]
    conn.reconnect()
    old.onopen?.()
    expect(onOpen).not.toHaveBeenCalled()
    sockets[1].onopen?.()
    expect(onOpen).toHaveBeenCalledTimes(1)
    conn.close()
  })
})
