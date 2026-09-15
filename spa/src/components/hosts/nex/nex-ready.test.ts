import { describe, it, expect } from 'vitest'
import { isNexReady } from './nex-ready'
import type { NexInfo } from '../../../lib/host-api'

const base: NexInfo = { configured: true, mounted: true, ready: true, init_error: '', effective: null }

describe('isNexReady', () => {
  it('is false without info', () => {
    expect(isNexReady(null)).toBe(false)
    expect(isNexReady(undefined)).toBe(false)
  })
  it('follows ready when the daemon reports it', () => {
    expect(isNexReady(base)).toBe(true)
    expect(isNexReady({ ...base, ready: false })).toBe(false)
  })
  it('falls back to mounted for an older daemon without ready', () => {
    const legacy = { configured: true, mounted: true, init_error: '', effective: null } as unknown as NexInfo
    expect(isNexReady(legacy)).toBe(true)
    expect(isNexReady({ ...legacy, mounted: false })).toBe(false)
  })
})
