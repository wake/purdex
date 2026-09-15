import { describe, it, expect } from 'vitest'
import { compositeKey, splitCompositeKey } from './composite-key'

describe('splitCompositeKey', () => {
  it('splits a plain hostId:sessionCode key', () => {
    expect(splitCompositeKey('mlab:ses001')).toEqual({ hostId: 'mlab', sessionCode: 'ses001' })
  })

  it('splits on the last colon so hostIds containing ":" survive intact', () => {
    expect(splitCompositeKey('mlab:abc123:ses001')).toEqual({ hostId: 'mlab:abc123', sessionCode: 'ses001' })
  })

  it('falls back to hostId="" and sessionCode=key when there is no colon', () => {
    expect(splitCompositeKey('ses001')).toEqual({ hostId: '', sessionCode: 'ses001' })
  })

  it('handles an empty hostId (leading colon)', () => {
    expect(splitCompositeKey(':ses001')).toEqual({ hostId: '', sessionCode: 'ses001' })
  })

  it('is the inverse of compositeKey', () => {
    for (const [hostId, sessionCode] of [
      ['mlab', 'ses001'],
      ['mlab:abc123', 'ses001'],
      ['a:b:c', 'z9x8y7'],
    ] as const) {
      expect(splitCompositeKey(compositeKey(hostId, sessionCode))).toEqual({ hostId, sessionCode })
    }
  })
})
