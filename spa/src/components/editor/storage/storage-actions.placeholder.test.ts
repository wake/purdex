// T5.1 — the placeholder registry as seen from the Storage CRUD actions:
// `createStorageFile` is one of the three eager reservation sites (it registers),
// and rename / move / delete are three of the deregistration triggers.
//
// Deregistration is PERMANENT and fires on the FIRST such event: once a file has
// been renamed, moved, or deleted it is the user's (or gone), and nothing may
// auto-delete it later.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  createStorageFile,
  deleteStorageEntries,
  moveStorageEntry,
  renameStorageEntry,
  uploadFile,
} from './storage-actions'
import { usePlaceholderFilesStore } from '../../../stores/usePlaceholderFilesStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'

const INAPP = { type: 'inapp' } as const

// Configurable backend, same shape as the main storage-actions suite.
let backend: unknown = null

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: () => backend,
  registerFsBackend: vi.fn(),
  supportsCreateUnique: (b: { createUnique?: unknown } | undefined | null) =>
    typeof b?.createUnique === 'function',
  supportsMkdirUnique: (b: { mkdirUnique?: unknown } | undefined | null) =>
    typeof b?.mkdirUnique === 'function',
}))

/** A backend where the destination of a rename/move is always free. */
function makeBackend(overrides: Record<string, unknown> = {}) {
  return {
    createUnique: vi.fn().mockResolvedValue('/buffer/Untitled.md'),
    // `stat` rejecting = "nothing there" for the collision pre-check.
    stat: vi.fn().mockRejectedValue(new Error('ENOENT')),
    rename: vi.fn().mockResolvedValue(undefined),
    delete: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  }
}

const t = (key: string) => key

beforeEach(() => {
  backend = makeBackend()
  usePlaceholderFilesStore.setState({ paths: [] })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useEditorStore.setState({ buffers: {}, paneStates: {} })
  useRecentFilesStore.setState({ files: [] })
  vi.stubGlobal('confirm', vi.fn(() => true))
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('createStorageFile — registers the reserved path (reservation site 3/3)', () => {
  it('records the path the namer reserved', async () => {
    backend = makeBackend({ createUnique: vi.fn().mockResolvedValue('/buffer/sub/Untitled-2.md') })
    const res = await createStorageFile('/buffer/sub')
    expect(res.error).toBeUndefined()
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/sub/Untitled-2.md')).toBe(true)
  })

  it('records NOTHING when the reservation fails (no file, no entry)', async () => {
    backend = makeBackend({ createUnique: vi.fn().mockRejectedValue(new Error('boom')) })
    const res = await createStorageFile()
    expect(res.error).toBe('boom')
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })
})

describe('renameStorageEntry — a rename deregisters permanently', () => {
  it('leaves NEITHER the old nor the new path in the registry', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const res = await renameStorageEntry('/buffer/Untitled.md', 'notes.md')
    expect(res).toEqual({ ok: true })
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(false)
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/notes.md')).toBe(false)
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })

  it('a FOLDER rename deregisters every placeholder inside it', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/dir/Untitled.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/other.md')
    await renameStorageEntry('/buffer/dir', 'archive')
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/other.md'])
  })

  it('a FAILED rename leaves the registry untouched (the file is still an untouched placeholder)', async () => {
    backend = makeBackend({ rename: vi.fn().mockRejectedValue(new Error('nope')) })
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const res = await renameStorageEntry('/buffer/Untitled.md', 'notes.md')
    expect('kind' in res && res.kind).toBe('error')
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(true)
  })
})

describe('moveStorageEntry — a move deregisters permanently', () => {
  it('leaves neither the source nor the destination path registered', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const res = await moveStorageEntry('/buffer/Untitled.md', '/buffer/dir')
    expect(res).toEqual({ ok: true })
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })

  it('a no-op move (already in that dir) changes nothing', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const res = await moveStorageEntry('/buffer/Untitled.md', '/buffer')
    expect(res).toEqual({ kind: 'noop' })
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(true)
  })
})

describe('deleteStorageEntries — an explicit delete drops the entry with the file', () => {
  it('drops the deleted path', async () => {
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled-1.md')
    const res = await deleteStorageEntries(['/buffer/Untitled.md'], t)
    expect(res).toEqual({ status: 'deleted' })
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/Untitled-1.md'])
  })

  it('a folder delete drops every placeholder beneath it', async () => {
    backend = makeBackend({
      stat: vi.fn().mockResolvedValue({ size: 0, mtime: 0, isDirectory: true, isFile: false }),
    })
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/dir/Untitled.md')
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/keep.md')
    await deleteStorageEntries(['/buffer/dir'], t)
    expect(usePlaceholderFilesStore.getState().paths).toEqual(['/buffer/keep.md'])
  })

  it('a cancelled delete keeps the entry', async () => {
    vi.stubGlobal('confirm', vi.fn(() => false))
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')
    const res = await deleteStorageEntries(['/buffer/Untitled.md'], t)
    expect(res).toEqual({ status: 'cancelled' })
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(true)
  })
})

describe('uploadFile — an upload writes real bytes, which ends the placeholder life', () => {
  it('deregisters the destination path', async () => {
    // `createUnique` normally hands back a fresh `-N` name, so an upload landing
    // on a registered path needs a STALE entry first: the reserved file was
    // removed behind our back (another tab, a sync client, the OS), the name
    // became free again, and the upload claimed it. The entry standing over that
    // path is then authorization to delete somebody's real bytes on the first
    // close. A write ends the placeholder's life whichever writer performed it.
    backend = makeBackend({ createUnique: vi.fn().mockResolvedValue('/buffer/Untitled.md') })
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')

    const res = await uploadFile('/buffer', new File([new Uint8Array([1, 2, 3])], 'Untitled.md'))

    expect(res).toEqual({ path: '/buffer/Untitled.md' })
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(false)
  })

  it('a FAILED upload leaves the registry untouched (nothing was written)', async () => {
    backend = makeBackend({ createUnique: vi.fn().mockRejectedValue(new Error('boom')) })
    usePlaceholderFilesStore.getState().register(INAPP, '/buffer/Untitled.md')

    const res = await uploadFile('/buffer', new File([new Uint8Array([1])], 'Untitled.md'))

    expect('kind' in res && res.kind).toBe('error')
    expect(usePlaceholderFilesStore.getState().isPlaceholder(INAPP, '/buffer/Untitled.md')).toBe(true)
  })
})
