import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createFileProvider } from './file-provider'
import type { SyncBundle } from '../types'

// ---------------------------------------------------------------------------
// Mock FileSystemIpc
// ---------------------------------------------------------------------------

const mockFs = {
  readFile: vi.fn(),
  writeFile: vi.fn(),
  readdir: vi.fn(),
  mkdir: vi.fn(),
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SYNC_FOLDER = '/sync/purdex'

const makeBundle = (): SyncBundle => ({
  version: 1,
  timestamp: 1745000000000,
  device: 'test-device',
  collections: {},
})

beforeEach(() => {
  vi.clearAllMocks()
  mockFs.mkdir.mockResolvedValue(undefined)
  mockFs.writeFile.mockResolvedValue(undefined)
  mockFs.readdir.mockResolvedValue([])
})

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('FileProvider', () => {
  it('has id "file"', () => {
    const provider = createFileProvider(SYNC_FOLDER, mockFs)
    expect(provider.id).toBe('file')
  })

  describe('push', () => {
    it('writes manifest.json and a history snapshot', async () => {
      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const bundle = makeBundle()

      await provider.push(bundle)

      // manifest.json
      const manifestCall = mockFs.writeFile.mock.calls.find(([path]: [string]) =>
        path.endsWith('manifest.json'),
      )
      expect(manifestCall).toBeDefined()
      expect(manifestCall[0]).toBe(`${SYNC_FOLDER}/manifest.json`)
      expect(JSON.parse(manifestCall[1])).toEqual(bundle)

      // history snapshot
      const historyCall = mockFs.writeFile.mock.calls.find(([path]: [string]) =>
        path.includes('/history/'),
      )
      expect(historyCall).toBeDefined()
      expect(historyCall[0]).toMatch(/\/history\/.*\.json$/)
      expect(JSON.parse(historyCall[1])).toEqual(bundle)
    })

    it('history snapshot filename uses ISO timestamp with colons replaced', async () => {
      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      await provider.push(makeBundle())

      const historyCall = mockFs.writeFile.mock.calls.find(([path]: [string]) =>
        path.includes('/history/'),
      )
      const filename = historyCall[0].split('/').pop() as string
      // e.g. 2026-04-16T12-00-00-000Z.json
      expect(filename).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z\.json$/)
    })

    it('calls ensureDirs before writing', async () => {
      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      await provider.push(makeBundle())

      expect(mockFs.mkdir).toHaveBeenCalledWith(SYNC_FOLDER)
      expect(mockFs.mkdir).toHaveBeenCalledWith(`${SYNC_FOLDER}/history`)
      expect(mockFs.mkdir).toHaveBeenCalledWith(`${SYNC_FOLDER}/chunks`)
    })
  })

  describe('pull', () => {
    it('reads manifest.json and parses JSON', async () => {
      const bundle = makeBundle()
      mockFs.readFile.mockResolvedValue(JSON.stringify(bundle))

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.pull()

      expect(mockFs.readFile).toHaveBeenCalledWith(`${SYNC_FOLDER}/manifest.json`)
      expect(result).toEqual(bundle)
    })

    it('returns null when manifest does not exist (ENOENT)', async () => {
      const err = Object.assign(new Error('no such file'), { code: 'ENOENT' })
      mockFs.readFile.mockRejectedValue(err)

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.pull()

      expect(result).toBeNull()
    })

    it('rethrows non-ENOENT errors', async () => {
      const err = Object.assign(new Error('permission denied'), { code: 'EACCES' })
      mockFs.readFile.mockRejectedValue(err)

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      await expect(provider.pull()).rejects.toThrow('permission denied')
    })
  })

  describe('pushChunks', () => {
    it('writes each chunk as base64 to chunks/{hash}.bin', async () => {
      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const data = new Uint8Array([1, 2, 3, 4, 5])
      await provider.pushChunks({ abc123: data })

      const chunkCall = mockFs.writeFile.mock.calls.find(([path]: [string]) =>
        path.includes('/chunks/'),
      )
      expect(chunkCall).toBeDefined()
      expect(chunkCall[0]).toBe(`${SYNC_FOLDER}/chunks/abc123.bin`)
      // value must be base64-encoded string
      const decoded = Buffer.from(chunkCall[1], 'base64')
      expect(new Uint8Array(decoded)).toEqual(data)
    })

    it('calls ensureDirs before writing chunks', async () => {
      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      await provider.pushChunks({ h1: new Uint8Array([9]) })

      expect(mockFs.mkdir).toHaveBeenCalledWith(SYNC_FOLDER)
      expect(mockFs.mkdir).toHaveBeenCalledWith(`${SYNC_FOLDER}/chunks`)
    })

    it('handles empty chunks map without writing', async () => {
      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      await provider.pushChunks({})

      expect(mockFs.writeFile).not.toHaveBeenCalled()
    })
  })

  describe('pullChunks', () => {
    it('reads and decodes chunk files', async () => {
      const data = new Uint8Array([10, 20, 30])
      const b64 = Buffer.from(data).toString('base64')
      mockFs.readFile.mockResolvedValue(b64)

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.pullChunks(['deadbeef'])

      expect(mockFs.readFile).toHaveBeenCalledWith(`${SYNC_FOLDER}/chunks/deadbeef.bin`)
      expect(result['deadbeef']).toEqual(data)
    })

    it('skips missing chunks (ENOENT)', async () => {
      const err = Object.assign(new Error('no such file'), { code: 'ENOENT' })
      mockFs.readFile.mockRejectedValue(err)

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.pullChunks(['missing1', 'missing2'])

      expect(result).toEqual({})
    })

    it('returns only present chunks when some are missing', async () => {
      const data = new Uint8Array([99])
      const b64 = Buffer.from(data).toString('base64')
      const notFound = Object.assign(new Error('no such file'), { code: 'ENOENT' })

      mockFs.readFile.mockImplementation(async (path: string) => {
        if (path.includes('present')) return b64
        throw notFound
      })

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.pullChunks(['present', 'missing'])

      expect(Object.keys(result)).toEqual(['present'])
      expect(result['present']).toEqual(data)
    })
  })

  describe('listHistory', () => {
    it('returns sorted snapshot entries (newest first), sliced to limit', async () => {
      mockFs.readdir.mockResolvedValue([
        '2026-04-16T10-00-00-000Z.json',
        '2026-04-14T08-00-00-000Z.json',
        '2026-04-15T09-00-00-000Z.json',
        'not-a-json-file.txt',
      ])

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.listHistory(2)

      expect(result).toHaveLength(2)
      // newest first
      expect(result[0].bundleRef).toContain('2026-04-16')
      expect(result[1].bundleRef).toContain('2026-04-15')
    })

    it('snapshot entries have source "remote" and trigger "auto"', async () => {
      mockFs.readdir.mockResolvedValue(['2026-04-16T10-00-00-000Z.json'])

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const [snap] = await provider.listHistory(10)

      expect(snap.source).toBe('remote')
      expect(snap.trigger).toBe('auto')
    })

    it('snapshot bundleRef is full path', async () => {
      mockFs.readdir.mockResolvedValue(['2026-04-16T10-00-00-000Z.json'])

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const [snap] = await provider.listHistory(10)

      expect(snap.bundleRef).toBe(`${SYNC_FOLDER}/history/2026-04-16T10-00-00-000Z.json`)
    })

    it('filters out non-.json files from history', async () => {
      mockFs.readdir.mockResolvedValue([
        'notes.txt',
        '.DS_Store',
        '2026-04-16T10-00-00-000Z.json',
      ])

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.listHistory(10)

      expect(result).toHaveLength(1)
    })

    it('returns empty array when history dir is empty', async () => {
      mockFs.readdir.mockResolvedValue([])

      const provider = createFileProvider(SYNC_FOLDER, mockFs)
      const result = await provider.listHistory(10)

      expect(result).toEqual([])
    })
  })
})
