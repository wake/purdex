import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createDaemonProvider } from './daemon-provider'
import type { SyncBundle } from '../types'

// ---------------------------------------------------------------------------
// Mock hostFetch
// ---------------------------------------------------------------------------

const mockHostFetch = vi.fn()
vi.mock('../../host-api', () => ({
  hostFetch: (...args: unknown[]) => mockHostFetch(...args),
}))

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const HOST_ID = 'host1'
const CLIENT_ID = 'c_test'

const makeBundle = (): SyncBundle => ({
  version: 1,
  timestamp: 1000000,
  device: 'test-device',
  collections: {},
})

beforeEach(() => {
  mockHostFetch.mockReset()
})

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('DaemonProvider', () => {
  it('has id "daemon"', () => {
    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    expect(provider.id).toBe('daemon')
  })

  it('push calls POST /api/sync/push with clientId and bundle body', async () => {
    mockHostFetch.mockResolvedValue(new Response('', { status: 200 }))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    const bundle = makeBundle()

    await provider.push(bundle)

    expect(mockHostFetch).toHaveBeenCalledWith(
      HOST_ID,
      `/api/sync/push?clientId=${CLIENT_ID}`,
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify(bundle),
      }),
    )
  })

  it('push sets Content-Type application/json', async () => {
    mockHostFetch.mockResolvedValue(new Response('', { status: 200 }))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    await provider.push(makeBundle())

    const init = mockHostFetch.mock.calls[0][2] as RequestInit
    const headers = new Headers(init.headers)
    expect(headers.get('Content-Type')).toBe('application/json')
  })

  it('push throws when response is not ok', async () => {
    mockHostFetch.mockResolvedValue(new Response('', { status: 500 }))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    await expect(provider.push(makeBundle())).rejects.toThrow()
  })

  it('pull calls GET /api/sync/pull with clientId and parses JSON', async () => {
    const bundle = makeBundle()
    mockHostFetch.mockResolvedValue(new Response(JSON.stringify(bundle)))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    const result = await provider.pull()

    expect(mockHostFetch).toHaveBeenCalledWith(
      HOST_ID,
      `/api/sync/pull?clientId=${CLIENT_ID}`,
      undefined,
    )
    expect(result).toEqual(bundle)
  })

  it('pull returns null when daemon has no data (response body is "null")', async () => {
    mockHostFetch.mockResolvedValue(new Response('null'))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    const result = await provider.pull()

    expect(result).toBeNull()
  })

  it('pull throws when response is not ok', async () => {
    mockHostFetch.mockResolvedValue(new Response('', { status: 404 }))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    await expect(provider.pull()).rejects.toThrow()
  })

  it('pushChunks is a stub and does not throw', async () => {
    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    await expect(provider.pushChunks({})).resolves.toBeUndefined()
  })

  it('pullChunks is a stub and returns empty object', async () => {
    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    await expect(provider.pullChunks(['hash1'])).resolves.toEqual({})
  })

  it('listHistory calls GET /api/sync/history with clientId and limit', async () => {
    const snapshots = [
      { id: 's1', timestamp: 2000000, device: 'dev1', source: 'remote', trigger: 'auto', bundleRef: 'ref1' },
    ]
    mockHostFetch.mockResolvedValue(new Response(JSON.stringify(snapshots)))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    const result = await provider.listHistory(10)

    expect(mockHostFetch).toHaveBeenCalledWith(
      HOST_ID,
      `/api/sync/history?clientId=${CLIENT_ID}&limit=10`,
      undefined,
    )
    expect(result).toEqual(snapshots)
  })

  it('listHistory throws when response is not ok', async () => {
    mockHostFetch.mockResolvedValue(new Response('', { status: 503 }))

    const provider = createDaemonProvider(HOST_ID, CLIENT_ID)
    await expect(provider.listHistory(5)).rejects.toThrow()
  })
})
