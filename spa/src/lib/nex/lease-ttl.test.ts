// spa/src/lib/nex/lease-ttl.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getLeaseTtlSeconds, DEFAULT_LEASE_TTL_S } from './lease-ttl'
import * as hostApi from '../host-api'
import * as api from './nex-api'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import type { NexInfo } from '../host-api'
import type { NexCapabilities } from './types'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  fetchInfo: vi.fn(),
}))
vi.mock('./nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./nex-api')>()),
  fetchNexCapabilities: vi.fn(),
}))

const H = 'h'
const info = (over: Partial<NexInfo> = {}): NexInfo =>
  ({ configured: true, mounted: true, ready: true, init_error: '', effective: null, ...over })
const capsWith = (lease: Partial<NexCapabilities['lease']> | undefined) =>
  ({ sandbox_profiles: [], roots: [], lease } as unknown as NexCapabilities)

describe('getLeaseTtlSeconds', () => {
  beforeEach(() => {
    vi.mocked(hostApi.fetchInfo).mockReset().mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve({ nex: info() }) } as Response)
    vi.mocked(api.fetchNexCapabilities).mockReset()
    useHostStore.setState({
      hosts: { [H]: { id: H, name: H, ip: '1.2.3.4', port: 7860, token: 't', order: 0 } },
      hostOrder: [H],
      runtime: { [H]: { status: 'connected' } },
    })
    useNexHostStore.setState({ byHost: {} })
  })

  it('reads ttl_seconds from the capabilities the store holds for the host', async () => {
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue(capsWith({ ttl_seconds: 90 }))
    expect(await getLeaseTtlSeconds(H)).toBe(90)
    expect(await getLeaseTtlSeconds(H)).toBe(90)
    // The store's TTL cache answers the second call: no second fetch.
    expect(api.fetchNexCapabilities).toHaveBeenCalledTimes(1)
  })

  it('falls back to the default when the capabilities fetch fails, and asks again next time', async () => {
    vi.mocked(api.fetchNexCapabilities).mockRejectedValueOnce(new Error('503'))
    expect(await getLeaseTtlSeconds(H)).toBe(DEFAULT_LEASE_TTL_S)
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue(capsWith({ ttl_seconds: 60 }))
    expect(await getLeaseTtlSeconds(H)).toBe(60)
  })

  it.each([
    ['lease section absent', undefined],
    ['ttl_seconds zero', { ttl_seconds: 0 }],
    ['ttl_seconds not a number', { ttl_seconds: '30' as unknown as number }],
  ])('falls back to the default when %s', async (_label, lease) => {
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue(capsWith(lease))
    expect(await getLeaseTtlSeconds(H)).toBe(DEFAULT_LEASE_TTL_S)
  })

  it('falls back to the default for a host the store does not know', async () => {
    expect(await getLeaseTtlSeconds('ghost')).toBe(DEFAULT_LEASE_TTL_S)
    expect(api.fetchNexCapabilities).not.toHaveBeenCalled()
  })

  it('after invalidate, changed capabilities yield the new TTL', async () => {
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue(capsWith({ ttl_seconds: 90 }))
    expect(await getLeaseTtlSeconds(H)).toBe(90)
    vi.mocked(api.fetchNexCapabilities).mockResolvedValue(capsWith({ ttl_seconds: 45 }))
    await useNexHostStore.getState().invalidate(H)
    expect(await getLeaseTtlSeconds(H)).toBe(45)
  })
})
