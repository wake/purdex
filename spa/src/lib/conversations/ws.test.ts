import { describe, it, expect, vi, beforeEach } from 'vitest'
import { openConversationSocket } from './ws'
import type { Frame } from './types'

vi.mock('../host-api', () => ({
  fetchWsTicket: vi.fn(async () => 'tk'),
  hostWsUrl: (_h: string, path: string) => `ws://daemon.test${path}`,
}))

class FakeWS {
  static all: FakeWS[] = []
  url: string
  onopen: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  constructor(url: string) {
    this.url = url
    FakeWS.all.push(this)
  }
  close() {
    this.closed = true
    this.onclose?.()
  }
  open() { this.onopen?.() }
  send(f: unknown) { this.onmessage?.({ data: typeof f === 'string' ? f : JSON.stringify(f) }) }
  drop() { this.onclose?.() }
}
const Impl = FakeWS as unknown as typeof WebSocket
const SID = '11111111-2222-4333-8444-555555555555'
const tick = () => new Promise((r) => setTimeout(r, 0))
const fr = (seq: number, type = 'conversation.changes'): Frame => ({ type, seq, value: {} }) as Frame

async function open(over: Partial<Parameters<typeof openConversationSocket>[0]> = {}) {
  const frames: Frame[] = []
  const closes: Array<{ gap: boolean; failed: boolean }> = []
  const onOpen = vi.fn()
  const sock = openConversationSocket({
    hostId: 'h1', sessionId: SID, WebSocketImpl: Impl, onOpen, onFrame: (f) => frames.push(f), onClose: (w) => closes.push(w), ...over,
  })
  await tick()
  return { sock, frames, closes, onOpen, ws: () => FakeWS.all[FakeWS.all.length - 1] }
}

beforeEach(() => { FakeWS.all = [] })

describe('openConversationSocket', () => {
  it('connects to the conversation stream with a fresh ticket, the cursor and the window size', async () => {
    const getTicket = vi.fn().mockResolvedValueOnce('t-1').mockResolvedValueOnce('t-2')
    const a = await open({ cursor: 'e:7', turns: 30, getTicket })
    const u = new URL(a.ws().url)
    expect(u.pathname).toBe(`/ws/conversations/claude/${SID}`)
    expect(u.searchParams.get('ticket')).toBe('t-1')
    expect(u.searchParams.get('after')).toBe('e:7')
    expect(u.searchParams.get('turns')).toBe('30')
    await open({ getTicket })
    expect(new URL(FakeWS.all[1].url).searchParams.get('ticket')).toBe('t-2') // never the spent one
  })

  it('no cursor and no turns → neither is in the URL', async () => {
    const u = new URL((await open()).ws().url)
    expect(u.searchParams.has('after')).toBe(false)
    expect(u.searchParams.has('turns')).toBe(false)
  })

  it('opens, then delivers frames in order', async () => {
    const a = await open()
    a.ws().open()
    a.ws().send(fr(1, 'conversation.snapshot'))
    a.ws().send(fr(2))
    expect(a.onOpen).toHaveBeenCalledTimes(1)
    expect(a.frames.map((f) => [f.seq, f.type])).toEqual([[1, 'conversation.snapshot'], [2, 'conversation.changes']])
  })

  describe('seq must start at 1 and stay contiguous', () => {
    it.each([
      ['a first frame that is not 1', [fr(2)]],
      ['a gap', [fr(1), fr(3)]],
      ['a repeat', [fr(1), fr(1)]],
    ])('%s retires the socket and reports a gap, once', async (_n, frames) => {
      const a = await open()
      a.ws().open()
      for (const f of frames) a.ws().send(f)
      expect(a.closes).toEqual([{ gap: true, failed: false }])
      expect(a.ws().closed).toBe(true)
      const delivered = a.frames.length
      a.ws().send(fr(99)) // nothing is applied after the retire
      expect(a.frames.length).toBe(delivered)
    })
  })

  it('a drop after it opened is reported as not failed; before it opened, as failed', async () => {
    const a = await open()
    a.ws().open()
    a.ws().drop()
    expect(a.closes).toEqual([{ gap: false, failed: false }])
    const b = await open()
    b.ws().drop()
    expect(b.closes).toEqual([{ gap: false, failed: true }])
  })

  it('a ticket that cannot be fetched is a failure and opens no socket', async () => {
    const a = await open({ getTicket: vi.fn().mockRejectedValue(new Error('401')) })
    expect(FakeWS.all).toHaveLength(0)
    expect(a.closes).toEqual([{ gap: false, failed: true }])
  })

  it('close() ends it and fires nothing afterwards', async () => {
    const a = await open()
    a.ws().open()
    a.sock.close()
    a.sock.close()
    expect(a.ws().closed).toBe(true)
    a.ws().send(fr(1))
    expect(a.frames).toHaveLength(0)
    expect(a.closes).toHaveLength(0)
  })

  it('close() while the ticket is in flight opens no socket', async () => {
    let release: (t: string) => void = () => {}
    const getTicket = vi.fn(() => new Promise<string>((r) => { release = r }))
    const frames: Frame[] = []
    const sock = openConversationSocket({ hostId: 'h1', sessionId: SID, WebSocketImpl: Impl, getTicket, onFrame: (f) => frames.push(f), onClose: () => {} })
    sock.close()
    release('late')
    await tick()
    expect(FakeWS.all).toHaveLength(0)
  })

  it('a message that is not JSON, or has no type and seq, is ignored without dropping the connection', async () => {
    const a = await open()
    a.ws().open()
    a.ws().send('not json')
    a.ws().send({ hello: 1 })
    a.ws().send(fr(1))
    expect(a.frames).toHaveLength(1)
    expect(a.closes).toHaveLength(0)
  })
})
