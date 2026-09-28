import { describe, it, expect } from 'vitest'
import { compositeKey, splitCompositeKey } from './composite-key'
import { execAgentCode } from './nex/worker-agent-status'

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
      // a host id ending in "exec" is not special
      ['mlab:exec', 'ses001'],
      // worker (execution) agent codes carry no colon (spec §8.1)
      ['h1', 'exec-e1'],
      ['mlab:abc123', 'exec-exc_01J9ZK'],
    ] as const) {
      expect(splitCompositeKey(compositeKey(hostId, sessionCode))).toEqual({ hostId, sessionCode })
    }
  })

  it('a worker key splits at the last colon into host and exec-<id> code', () => {
    expect(splitCompositeKey(compositeKey('h1', execAgentCode('exc_1')))).toEqual({ hostId: 'h1', sessionCode: 'exec-exc_1' })
  })

  it('a key containing ":exec:" is split at the last colon like any other', () => {
    expect(splitCompositeKey('h1:exec:e1')).toEqual({ hostId: 'h1:exec', sessionCode: 'e1' })
  })
})
