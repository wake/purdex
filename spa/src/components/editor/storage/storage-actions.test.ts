import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  createStorageFile,
  createStorageFolder,
  renameStorageEntry,
} from './storage-actions'

// Configurable backend: default null so we exercise the "missing backend" guard.
let backend: unknown = null

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: () => backend,
  registerFsBackend: vi.fn(),
}))

beforeEach(() => {
  backend = null
})

describe('storage-actions — missing In-App backend is a failure, not silent success (codex R3)', () => {
  it('createStorageFile returns an error (so handleNew shows the banner, not a fake success)', async () => {
    const res = await createStorageFile()
    expect(res.error).toBeTruthy()
  })

  it('createStorageFolder returns an error when the backend is missing', async () => {
    const res = await createStorageFolder()
    expect('error' in res && res.error).toBeTruthy()
  })

  it('renameStorageEntry returns a kind:error outcome (so the rename popover is not closed as if it worked)', async () => {
    const res = await renameStorageEntry('/buffer/a.md', 'b.md')
    expect(res).toEqual({ kind: 'error', message: expect.any(String) })
  })
})

describe('createStorageFolder — mkdirUnique delegation (T1b-3)', () => {
  it('calls mkdirUnique with the target dir and returns the reserved path', async () => {
    const mkdirUnique = vi.fn().mockResolvedValue('/buffer/sub/New Folder')
    backend = { mkdirUnique }
    const res = await createStorageFolder('/buffer/sub')
    expect(mkdirUnique).toHaveBeenCalledWith('/buffer/sub')
    expect(res).toEqual({ path: '/buffer/sub/New Folder' })
  })

  it('defaults the target dir to the storage root', async () => {
    const mkdirUnique = vi.fn().mockResolvedValue('/buffer/New Folder')
    backend = { mkdirUnique }
    const res = await createStorageFolder()
    expect(mkdirUnique).toHaveBeenCalledWith('/buffer')
    expect(res).toEqual({ path: '/buffer/New Folder' })
  })

  it('surfaces a backend failure as an error outcome', async () => {
    backend = { mkdirUnique: vi.fn().mockRejectedValue(new Error('boom')) }
    const res = await createStorageFolder('/buffer')
    expect('error' in res && res.error).toBe('boom')
  })
})
