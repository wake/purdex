import { describe, it, expect, vi, beforeEach } from 'vitest'
import { postMissing, putBlob, postSnapshot } from './backup-api'
import type { SnapshotRequest } from './backup-api'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({
  hostFetch: (...args: unknown[]) => hostFetch(...args),
}))

beforeEach(() => {
  hostFetch.mockReset()
})

describe('backup-api client', () => {
  describe('postMissing', () => {
    it('POSTs hashes and returns the missing subset', async () => {
      hostFetch.mockResolvedValue(
        new Response(JSON.stringify({ missing: ['b'] }), { status: 200 }),
      )
      const missing = await postMissing('h1', ['a', 'b'])
      expect(missing).toEqual(['b'])
      const [hostId, path, init] = hostFetch.mock.calls[0]
      expect(hostId).toBe('h1')
      expect(path).toBe('/api/backup/missing')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ hashes: ['a', 'b'] })
    })

    it('throws surfacing the status on a non-2xx (e.g. 413)', async () => {
      hostFetch.mockResolvedValue(new Response('too many', { status: 413 }))
      await expect(postMissing('h1', ['a'])).rejects.toThrow(/413/)
    })
  })

  describe('putBlob', () => {
    it('PUTs raw bytes to /blob/{hash} and resolves on 204', async () => {
      hostFetch.mockResolvedValue(new Response(null, { status: 204 }))
      const bytes = new Uint8Array([1, 2, 3])
      await expect(putBlob('h1', 'deadbeef', bytes)).resolves.toBeUndefined()
      const [hostId, path, init] = hostFetch.mock.calls[0]
      expect(hostId).toBe('h1')
      expect(path).toBe('/api/backup/blob/deadbeef')
      expect(init.method).toBe('PUT')
      expect(init.body).toBe(bytes)
    })

    it('throws surfacing the status on a non-204 (e.g. 413 / 400)', async () => {
      hostFetch.mockResolvedValue(new Response('overflow', { status: 413 }))
      await expect(putBlob('h1', 'deadbeef', new Uint8Array(0))).rejects.toThrow(/413/)
    })
  })

  describe('postSnapshot', () => {
    const req: SnapshotRequest = {
      storeId: 'inapp:buffer',
      device: 'c_abc',
      parentId: null,
      trigger: 'auto',
      manifest: [{ path: 'a.txt', kind: 'file', hash: 'h', size: 1, words: 0 }],
    }

    it('POSTs the request and returns {snapshotId,isFork,currentHeadId}', async () => {
      hostFetch.mockResolvedValue(
        new Response(
          JSON.stringify({ snapshotId: 7, isFork: false, currentHeadId: 7 }),
          { status: 200 },
        ),
      )
      const res = await postSnapshot('h1', req)
      expect(res).toEqual({ snapshotId: 7, isFork: false, currentHeadId: 7 })
      const [, path, init] = hostFetch.mock.calls[0]
      expect(path).toBe('/api/backup/snapshot')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual(req)
    })

    it('throws surfacing the status on 409 (missing blob)', async () => {
      hostFetch.mockResolvedValue(new Response('missing blob', { status: 409 }))
      await expect(postSnapshot('h1', req)).rejects.toThrow(/409/)
    })

    it('throws surfacing the status on 400 (unsorted manifest)', async () => {
      hostFetch.mockResolvedValue(new Response('unsorted', { status: 400 }))
      await expect(postSnapshot('h1', req)).rejects.toThrow(/400/)
    })
  })
})
