import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { HostEvent } from '../host-events'
import {
  dispatchProfileWsEvent,
  setProfileEventListener,
  type ProfileRemoteEvent,
} from './profile-ws-dispatch'

const HASH = 'a'.repeat(64)
const HASH_B = '0123456789abcdef'.repeat(4)

function raw(value: string): HostEvent {
  return { type: 'profile', session: '', value }
}

function profileEvent(value: unknown): HostEvent {
  return raw(JSON.stringify(value))
}

/** The daemon's put shape (Go `profileEvent`, `deleted` omitted when false). */
function put(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    profileId: 'p_1', section: 'settings', rev: 7, hash: HASH, writerClientId: 'c_0123456789ab',
    ...overrides,
  }
}

function without(key: string): Record<string, unknown> {
  const v = put()
  delete v[key]
  return v
}

let received: ProfileRemoteEvent[]
let listener: ReturnType<typeof vi.fn<(e: ProfileRemoteEvent) => void>>

beforeEach(() => {
  received = []
  listener = vi.fn((e: ProfileRemoteEvent) => { received.push(e) })
  setProfileEventListener(listener)
})

afterEach(() => {
  setProfileEventListener(null)
  vi.restoreAllMocks()
})

describe('dispatchProfileWsEvent', () => {
  it("'profile' is a valid HostEvent type", () => {
    const ev: HostEvent = { type: 'profile', session: '', value: '{}' }
    expect(ev.type).toBe('profile')
  })

  it('hands a valid put event to the listener, with the closure hostId', () => {
    dispatchProfileWsEvent('h1', profileEvent(put()))
    expect(listener).toHaveBeenCalledTimes(1)
    expect(received[0]).toEqual({
      hostId: 'h1', profileId: 'p_1', section: 'settings', rev: 7, hash: HASH,
      writerClientId: 'c_0123456789ab',
    })
  })

  it('accepts rev 0 and an explicit deleted:false', () => {
    dispatchProfileWsEvent('h1', profileEvent(put({ rev: 0, deleted: false, hash: HASH_B })))
    expect(received[0]).toMatchObject({ rev: 0, hash: HASH_B })
  })

  it('passes writerClientId through verbatim — no own-event filtering here', () => {
    for (const id of ['c_0123456789ab', 'c_ffffffffffff', '', 'anything at all']) {
      dispatchProfileWsEvent('h1', profileEvent(put({ writerClientId: id })))
    }
    expect(received.map((e) => e.writerClientId))
      .toEqual(['c_0123456789ab', 'c_ffffffffffff', '', 'anything at all'])
  })

  describe('delete → hash: null', () => {
    it("the daemon's delete shape (hash '' + deleted true)", () => {
      dispatchProfileWsEvent('h1', profileEvent(put({ hash: '', deleted: true, rev: 8 })))
      expect(received[0]).toEqual({
        hostId: 'h1', profileId: 'p_1', section: 'settings', rev: 8, hash: null,
        writerClientId: 'c_0123456789ab',
      })
    })

    it("hash '' without deleted", () => {
      dispatchProfileWsEvent('h1', profileEvent(put({ hash: '' })))
      expect(received).toHaveLength(1)
      expect(received[0].hash).toBeNull()
    })

    it('deleted true with a non-empty hash — deleted wins', () => {
      dispatchProfileWsEvent('h1', profileEvent(put({ deleted: true })))
      expect(received).toHaveLength(1)
      expect(received[0].hash).toBeNull()
    })

    it('deleted true with a non-empty INVALID hash — deleted still wins', () => {
      dispatchProfileWsEvent('h1', profileEvent(put({ deleted: true, hash: 'junk' })))
      expect(received).toHaveLength(1)
      expect(received[0].hash).toBeNull()
    })
  })

  describe('malformed → dropped', () => {
    it.each([
      ['uppercase hash', put({ hash: 'A'.repeat(64) })],
      ['short hash', put({ hash: 'a'.repeat(63) })],
      ['long hash', put({ hash: 'a'.repeat(65) })],
      ['non-hex hash', put({ hash: 'g'.repeat(64) })],
      ['hash with trailing newline', put({ hash: `${HASH}\n` })],
      ['hash null', put({ hash: null })],
      ['hash number', put({ hash: 1 })],
      ['negative rev', put({ rev: -1 })],
      ['float rev', put({ rev: 1.5 })],
      ['string rev', put({ rev: '7' })],
      ['unsafe rev', put({ rev: 2 ** 53 })],
      ['null rev', put({ rev: null })],
      ['empty profileId', put({ profileId: '' })],
      ['numeric profileId', put({ profileId: 1 })],
      ['empty section', put({ section: '' })],
      ['numeric section', put({ section: 3 })],
      ['numeric writerClientId', put({ writerClientId: 5 })],
      ['non-boolean deleted', put({ deleted: 'true' })],
      ['deleted 1', put({ deleted: 1, hash: '' })],
      ['missing profileId', without('profileId')],
      ['missing section', without('section')],
      ['missing rev', without('rev')],
      ['missing hash', without('hash')],
      ['missing writerClientId', without('writerClientId')],
      ['empty object', {}],
    ])('%s', (_label, value) => {
      expect(() => dispatchProfileWsEvent('h1', profileEvent(value))).not.toThrow()
      expect(listener).not.toHaveBeenCalled()
    })

    it.each(['not json', 'null', '[]', '42', '"str"', 'true', ''])(
      'value %j → no throw, no listener call',
      (value) => {
        expect(() => dispatchProfileWsEvent('h1', raw(value))).not.toThrow()
        expect(listener).not.toHaveBeenCalled()
      },
    )
  })

  describe('listener slot', () => {
    it('no listener → dropped without throwing', () => {
      setProfileEventListener(null)
      expect(() => dispatchProfileWsEvent('h1', profileEvent(put()))).not.toThrow()
      expect(listener).not.toHaveBeenCalled()
    })

    it('a throwing listener does not escape onto the WS path', () => {
      const err = vi.spyOn(console, 'error').mockImplementation(() => {})
      setProfileEventListener(() => { throw new Error('boom') })
      expect(() => dispatchProfileWsEvent('h1', profileEvent(put()))).not.toThrow()
      expect(err).toHaveBeenCalledTimes(1)
    })

    it('setProfileEventListener(null) stops delivery', () => {
      dispatchProfileWsEvent('h1', profileEvent(put()))
      setProfileEventListener(null)
      dispatchProfileWsEvent('h1', profileEvent(put({ rev: 8 })))
      expect(listener).toHaveBeenCalledTimes(1)
    })

    it('a later listener replaces the earlier one', () => {
      const second = vi.fn()
      setProfileEventListener(second)
      dispatchProfileWsEvent('h2', profileEvent(put()))
      expect(listener).not.toHaveBeenCalled()
      expect(second).toHaveBeenCalledTimes(1)
      expect(second.mock.calls[0][0]).toMatchObject({ hostId: 'h2', rev: 7 })
    })
  })
})
