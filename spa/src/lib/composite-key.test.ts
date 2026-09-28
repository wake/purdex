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
      // worker (execution) agent keys contain a colon of their own (spec §8.1)
      ['h1', 'exec:e1'],
      ['mlab:abc123', 'exec:01J9ZK'],
    ] as const) {
      expect(splitCompositeKey(compositeKey(hostId, sessionCode))).toEqual({ hostId, sessionCode })
    }
  })

  describe('exec split only applies at the `:exec:` immediately before the final segment', () => {
    it('a plain tmux key is unchanged', () => {
      expect(splitCompositeKey('h1:abc123')).toEqual({ hostId: 'h1', sessionCode: 'abc123' })
    })

    it('a tmux key whose host is literally "exec" is unchanged', () => {
      expect(splitCompositeKey('exec:abc123')).toEqual({ hostId: 'exec', sessionCode: 'abc123' })
    })

    it('a worker key splits host from the exec:<id> code', () => {
      expect(splitCompositeKey('h1:exec:e1')).toEqual({ hostId: 'h1', sessionCode: 'exec:e1' })
    })

    it('a host id containing ":" with a tmux code is unchanged', () => {
      expect(splitCompositeKey('mlab:abc123:ses001')).toEqual({ hostId: 'mlab:abc123', sessionCode: 'ses001' })
    })

    it('a ":exec:" that is not immediately before the final segment does not trigger the exec split', () => {
      // hostId "a:exec:b" with a plain tmux code "ses001" — the "exec:" here
      // sits mid-host, not right before the trailing id, so this must fall
      // back to the last-colon split, not be misread as a worker key.
      expect(splitCompositeKey('a:exec:b:ses001')).toEqual({ hostId: 'a:exec:b', sessionCode: 'ses001' })
    })
  })
})
