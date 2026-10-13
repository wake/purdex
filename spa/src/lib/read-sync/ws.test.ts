// spa/src/lib/read-sync/ws.test.ts — the two read frames of `/ws/host-events?reads=v1` (app-badge spec §7.2):
// `session.reads` (on connect: every remembered read) and `session.read` (one accepted read). Both are the usual
// host-event envelope, the body a JSON string in `value`. Applying is idempotent and monotonic, so the snapshot and the
// live frames need no relative order.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore, type NormalizedEvent } from '../../stores/useAgentStore'
import type { HostEvent } from '../host-events'
import { handleReadsEvent } from './ws'

const H = 'h1'
const unread = (code: string, host = H) => useAgentStore.getState().unread[`${host}:${code}`] === true
const ev = (status: string, epoch: string, seq: number): NormalizedEvent =>
  ({ agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1, epoch, seq })
function unreadBy(code: string, epoch: string, seq: number, host = H) {
  useAgentStore.getState().applyHookEvent(host, code, ev('running', epoch, seq - 1))
  useAgentStore.getState().applyHookEvent(host, code, ev('idle', epoch, seq))
}
const frame = (type: HostEvent['type'], session: string, value: unknown): HostEvent =>
  ({ type, session, value: typeof value === 'string' ? value : JSON.stringify(value) })

beforeEach(() => {
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {}, unreadCursor: {} })
})

describe('handleReadsEvent', () => {
  it('returns false for any other frame type, and touches nothing', () => {
    unreadBy('aaaaaa', 'E1', 7)
    expect(handleReadsEvent(H, frame('hook', 'aaaaaa', {}))).toBe(false)
    expect(unread('aaaaaa')).toBe(true)
  })

  describe('session.reads (the snapshot on connect)', () => {
    it('applies every read by the cover rule', () => {
      unreadBy('aaaaaa', 'E1', 7)
      unreadBy('bbbbbb', 'E1', 9)
      unreadBy('cccccc', 'E2', 1)
      const handled = handleReadsEvent(H, frame('session.reads', '', { reads: [
        { code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } },
        { code: 'bbbbbb', through: { epoch: 'E1', seq: 8 } },
        { code: 'cccccc', through: { epoch: 'E1', seq: 99 } },
      ] }))
      expect(handled).toBe(true)
      expect(unread('aaaaaa')).toBe(false) // covered
      expect(unread('bbbbbb')).toBe(true) // a later event stays unread
      expect(unread('cccccc')).toBe(true) // another epoch never clears
    })

    it('an empty snapshot changes nothing', () => {
      unreadBy('aaaaaa', 'E1', 7)
      expect(handleReadsEvent(H, frame('session.reads', '', { reads: [] }))).toBe(true)
      expect(unread('aaaaaa')).toBe(true)
    })

    it('a malformed entry is skipped, the others still apply', () => {
      unreadBy('aaaaaa', 'E1', 7)
      unreadBy('bbbbbb', 'E1', 7)
      handleReadsEvent(H, frame('session.reads', '', { reads: [
        { code: '', through: { epoch: 'E1', seq: 7 } },
        { code: 'aaaaaa', through: { epoch: '', seq: 7 } },
        { code: 'bbbbbb', through: { epoch: 'E1', seq: 7 } },
        null,
      ] }))
      expect(unread('aaaaaa')).toBe(true)
      expect(unread('bbbbbb')).toBe(false)
    })

    it.each([['not json', 'x{'], ['not an object', '5'], ['no reads', '{}'], ['reads not an array', '{"reads":{}}']])(
      'a frame whose envelope is wrong is dropped whole (%s)', (_name, value) => {
        unreadBy('aaaaaa', 'E1', 7)
        expect(handleReadsEvent(H, frame('session.reads', '', value))).toBe(true) // ours, and consumed
        expect(unread('aaaaaa')).toBe(true)
      },
    )
  })

  describe('session.read (one accepted read)', () => {
    it('applies the read of its code', () => {
      unreadBy('aaaaaa', 'E1', 7)
      unreadBy('bbbbbb', 'E1', 7)
      expect(handleReadsEvent(H, frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }))).toBe(true)
      expect(unread('aaaaaa')).toBe(false)
      expect(unread('bbbbbb')).toBe(true)
    })

    it('a later event is not cleared, nor is another epoch', () => {
      unreadBy('aaaaaa', 'E1', 9)
      handleReadsEvent(H, frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: 8 } }))
      handleReadsEvent(H, frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E2', seq: 99 } }))
      expect(unread('aaaaaa')).toBe(true)
    })

    it('an older read arriving after a newer unread changes nothing (idempotent, monotonic)', () => {
      unreadBy('aaaaaa', 'E1', 7)
      const r = frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } })
      handleReadsEvent(H, r)
      unreadBy('aaaaaa', 'E1', 12)
      handleReadsEvent(H, r)
      expect(unread('aaaaaa')).toBe(true)
    })

    it('another host\'s unread of the same code is untouched', () => {
      unreadBy('aaaaaa', 'E1', 7)
      unreadBy('aaaaaa', 'E1', 7, 'h2')
      handleReadsEvent(H, frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }))
      expect(unread('aaaaaa', 'h2')).toBe(true)
    })

    it.each([['not json', 'x{'], ['no code', '{"through":{"epoch":"E1","seq":7}}'], ['no through', '{"code":"aaaaaa"}'],
      ['bad through', '{"code":"aaaaaa","through":{"epoch":"","seq":7}}']])('a malformed body is dropped (%s)', (_name, value) => {
      unreadBy('aaaaaa', 'E1', 7)
      expect(handleReadsEvent(H, frame('session.read', 'aaaaaa', value))).toBe(true)
      expect(unread('aaaaaa')).toBe(true)
    })
  })

  it('a received read is never forwarded: applying it makes no request', () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    unreadBy('aaaaaa', 'E1', 7)
    handleReadsEvent(H, frame('session.read', 'aaaaaa', { code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }))
    handleReadsEvent(H, frame('session.reads', '', { reads: [{ code: 'aaaaaa', through: { epoch: 'E1', seq: 7 } }] }))
    expect(fetchSpy).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
})
