// spa/src/lib/nex/nex-host-reducer.test.ts — the pure half of the nex host
// cache, checked without a network. The store-level behaviour (fetching,
// dedup, stale resolves, the watcher) stays in useNexHostStore.test.ts.
import { describe, it, expect } from 'vitest'
import {
  NEX_HOST_TTL_MS,
  commitLoaded,
  emptyEntry,
  hostFingerprint,
  isFresh,
  markStale,
  phaseOf,
  type NexHostEntry,
} from './nex-host-reducer'
import type { NexInfo } from '../host-api'
import type { NexCapabilities } from './types'

const info = (over: Partial<NexInfo> = {}): NexInfo =>
  ({ configured: true, mounted: true, ready: true, init_error: '', effective: null, ...over })
const caps = { sandbox_profiles: [], roots: [] } as unknown as NexCapabilities
const FP = '1.2.3.4:7860:t'

const ready = (over: Partial<NexHostEntry> = {}): NexHostEntry =>
  ({ info: info(), capabilities: caps, phase: 'ready', error: null, fetchedAt: 1000, generation: 1, fingerprint: FP, ...over })

describe('phaseOf — the invariant: ready ⇔ isNexReady(info) && capabilities !== null && no error', () => {
  it.each([
    ['no info', { info: null, capabilities: null, error: 'x' }, 'unavailable'],
    ['configured=false', { info: info({ configured: false, ready: false }), capabilities: null, error: null }, 'disabled'],
    ['mounted=false', { info: info({ mounted: false, ready: false }), capabilities: null, error: null }, 'disabled'],
    ['init_error', { info: info({ ready: false, init_error: 'bad' }), capabilities: null, error: 'bad' }, 'unavailable'],
    ['ready=false, no error', { info: info({ ready: false }), capabilities: null, error: null }, 'unavailable'],
    ['ready but capabilities missing', { info: info(), capabilities: null, error: null }, 'unavailable'],
    ['ready but capabilities failed', { info: info(), capabilities: null, error: 'engine down' }, 'unavailable'],
    ['ready with capabilities', { info: info(), capabilities: caps, error: null }, 'ready'],
  ] as const)('%s → %s', (_label, loaded, expected) => {
    expect(phaseOf(loaded)).toBe(expected)
  })

  it('never yields ready with an error attached, even with capabilities present', () => {
    expect(phaseOf({ info: info(), capabilities: caps, error: 'late' })).toBe('unavailable')
  })
})

describe('isFresh', () => {
  it('is true inside the TTL for a settled, non-unavailable entry of the same identity', () => {
    expect(isFresh(ready(), 1000 + NEX_HOST_TTL_MS - 1, FP)).toBe(true)
  })

  it.each([
    ['no entry', undefined, 1000, FP],
    ['never fetched', ready({ fetchedAt: 0 }), 1000, FP],
    ['unavailable', ready({ phase: 'unavailable', capabilities: null, error: 'x' }), 1000, FP],
    ['TTL elapsed', ready(), 1000 + NEX_HOST_TTL_MS, FP],
    ['another identity', ready(), 1000, '1.2.3.4:7860:rotated'],
  ] as const)('is false when %s', (_label, entry, now, fp) => {
    expect(isFresh(entry, now, fp)).toBe(false)
  })

  it('a disabled entry is cached like a ready one (the daemon said so; a reconnect refetches)', () => {
    expect(isFresh(ready({ phase: 'disabled', capabilities: null }), 1000, FP)).toBe(true)
  })
})

describe('entry transitions', () => {
  it('emptyEntry is a loading placeholder carrying its generation and identity', () => {
    expect(emptyEntry(7, FP)).toEqual({ info: null, capabilities: null, phase: 'loading', error: null, fetchedAt: 0, generation: 7, fingerprint: FP })
  })

  it('commitLoaded derives the phase and stamps time, generation and identity from the token', () => {
    const loaded = { info: info(), capabilities: caps, error: null }
    expect(commitLoaded(loaded, { generation: 3, fingerprint: FP }, 5000)).toEqual({ ...loaded, phase: 'ready', fetchedAt: 5000, generation: 3, fingerprint: FP })
  })

  it('markStale keeps the data but zeroes fetchedAt and takes the new generation', () => {
    const cur = ready()
    const next = markStale(cur, 9)
    expect(next).toEqual({ ...cur, fetchedAt: 0, generation: 9 })
    expect(next).not.toBe(cur)
    expect(cur.fetchedAt).toBe(1000)
  })
})

describe('hostFingerprint', () => {
  it('is ip:port:token, with a missing or cleared token as empty', () => {
    expect(hostFingerprint({ ip: '1.2.3.4', port: 7860, token: 't' })).toBe('1.2.3.4:7860:t')
    expect(hostFingerprint({ ip: '1.2.3.4', port: 7860 })).toBe('1.2.3.4:7860:')
    expect(hostFingerprint({ ip: '1.2.3.4', port: 7860, token: null })).toBe('1.2.3.4:7860:')
  })

  it('differs when any of the three changes', () => {
    const base = hostFingerprint({ ip: '1.2.3.4', port: 7860, token: 't' })
    expect(hostFingerprint({ ip: '1.2.3.5', port: 7860, token: 't' })).not.toBe(base)
    expect(hostFingerprint({ ip: '1.2.3.4', port: 7861, token: 't' })).not.toBe(base)
    expect(hostFingerprint({ ip: '1.2.3.4', port: 7860, token: 'u' })).not.toBe(base)
  })
})
