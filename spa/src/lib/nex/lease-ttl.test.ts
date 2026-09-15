// spa/src/lib/nex/lease-ttl.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getLeaseTtlSeconds, resetLeaseTtlCacheForTests, DEFAULT_LEASE_TTL_S } from './lease-ttl'
import * as api from './nex-api'

vi.mock('./nex-api', () => ({ fetchNexCapabilities: vi.fn() }))

describe('getLeaseTtlSeconds', () => {
  beforeEach(() => { resetLeaseTtlCacheForTests(); vi.mocked(api.fetchNexCapabilities).mockReset() })

  it('reads capabilities once per host and caches', async () => {
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue({ lease: { ttl_seconds: 90 } } as never)
    expect(await getLeaseTtlSeconds('h')).toBe(90)
    expect(await getLeaseTtlSeconds('h')).toBe(90)
    expect(api.fetchNexCapabilities).toHaveBeenCalledTimes(1)
  })

  it('falls back to the default on failure and does not cache the failure', async () => {
    vi.mocked(api.fetchNexCapabilities).mockRejectedValueOnce(new Error('503'))
    expect(await getLeaseTtlSeconds('h')).toBe(DEFAULT_LEASE_TTL_S)
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue({ lease: { ttl_seconds: 60 } } as never)
    expect(await getLeaseTtlSeconds('h')).toBe(60)
  })
})
