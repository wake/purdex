import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createStorageFile, renameStorageEntry } from './storage-actions'

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

  it('renameStorageEntry returns a kind:error outcome (so the rename popover is not closed as if it worked)', async () => {
    const res = await renameStorageEntry('/buffer/a.md', 'b.md')
    expect(res).toEqual({ kind: 'error', message: expect.any(String) })
  })
})
