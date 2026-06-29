import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  postMissing,
  putBlob,
  postSnapshot,
  getHistory,
  getSnapshot,
  getBlob,
  BackupNotFoundError,
} from './backup-api'
import type { SnapshotRequest, SnapshotSummary, SnapshotDetail } from './backup-api'

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

  describe('getHistory', () => {
    it('GETs /history?storeId= and returns the summary rows', async () => {
      const rows: SnapshotSummary[] = [
        {
          id: 7,
          device: 'c_abc',
          parentId: 6,
          isFork: false,
          trigger: 'auto',
          createdAt: 1_751_230_000,
          fileCount: 2,
          dirCount: 1,
          totalSize: 42,
        },
      ]
      hostFetch.mockResolvedValue(new Response(JSON.stringify(rows), { status: 200 }))
      const got = await getHistory('h1', 'inapp:buffer')
      expect(got).toEqual(rows)
      const [hostId, path, init] = hostFetch.mock.calls[0]
      expect(hostId).toBe('h1')
      expect(path).toBe('/api/backup/history?storeId=inapp%3Abuffer')
      expect(init?.method ?? 'GET').toBe('GET')
    })

    it('throws surfacing the status on a non-2xx (e.g. 500)', async () => {
      hostFetch.mockResolvedValue(new Response('boom', { status: 500 }))
      await expect(getHistory('h1', 'inapp:buffer')).rejects.toThrow(/500/)
    })
  })

  describe('getSnapshot', () => {
    it('GETs /snapshot/{id} and returns the detail with manifest', async () => {
      const detail: SnapshotDetail = {
        id: 7,
        storeId: 'inapp:buffer',
        device: 'c_abc',
        parentId: null,
        isFork: false,
        trigger: 'auto',
        createdAt: 1_751_230_000,
        manifest: [{ path: 'a.txt', kind: 'file', hash: 'h', size: 1, words: 0 }],
      }
      hostFetch.mockResolvedValue(new Response(JSON.stringify(detail), { status: 200 }))
      const got = await getSnapshot('h1', 7)
      expect(got).toEqual(detail)
      const [, path] = hostFetch.mock.calls[0]
      expect(path).toBe('/api/backup/snapshot/7')
    })

    it('throws a typed BackupNotFoundError on 404', async () => {
      hostFetch.mockResolvedValue(new Response('snapshot not found', { status: 404 }))
      await expect(getSnapshot('h1', 99)).rejects.toBeInstanceOf(BackupNotFoundError)
      await expect(getSnapshot('h1', 99)).rejects.toThrow(/404/)
    })

    it('throws surfacing the status on a non-404 error (e.g. 500)', async () => {
      hostFetch.mockResolvedValue(new Response('boom', { status: 500 }))
      await expect(getSnapshot('h1', 7)).rejects.toThrow(/500/)
    })
  })

  describe('getBlob', () => {
    it('GETs /blob/{hash} and returns the raw bytes', async () => {
      const bytes = new Uint8Array([1, 2, 3, 4])
      hostFetch.mockResolvedValue(new Response(bytes, { status: 200 }))
      const got = await getBlob('h1', 'deadbeef')
      expect(Array.from(got)).toEqual([1, 2, 3, 4])
      const [hostId, path] = hostFetch.mock.calls[0]
      expect(hostId).toBe('h1')
      expect(path).toBe('/api/backup/blob/deadbeef')
    })

    it('throws surfacing the status on 404 (blob absent)', async () => {
      hostFetch.mockResolvedValue(new Response('blob not found', { status: 404 }))
      await expect(getBlob('h1', 'deadbeef')).rejects.toThrow(/404/)
    })

    it('throws surfacing the status on a non-2xx (e.g. 500)', async () => {
      hostFetch.mockResolvedValue(new Response('boom', { status: 500 }))
      await expect(getBlob('h1', 'deadbeef')).rejects.toThrow(/500/)
    })
  })
})
