import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  createStorageFile,
  createStorageFolder,
  deleteStorageEntries,
  downloadStorageFile,
  moveStorageEntry,
  renameStorageEntry,
  uploadFile,
  uploadFiles,
  SOFT_MAX_UPLOAD_BYTES,
} from './storage-actions'
import { InAppBackend } from '../../../lib/fs-backend-inapp'
import { closeAllIDB } from '../../../lib/storage/idb'
import { triggerDownload } from '../../../lib/download-file'
import { useTabStore } from '../../../stores/useTabStore'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import { makeEditorBuffer, makeEditorPaneState } from '../../../stores/__tests__/editor-buffer-fixture'
import type { RecentFileEntry } from '../../../stores/useRecentFilesStore'
import type { Tab } from '../../../types/tab'
import type { FileSource } from '../../../types/fs'

// Configurable backend: default null so we exercise the "missing backend" guard.
let backend: unknown = null

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: () => backend,
  registerFsBackend: vi.fn(),
  // Capability guards (codex H1) — narrow via the same typeof check the real
  // module uses, so the configurable `backend` here flows through unchanged.
  supportsCreateUnique: (b: { createUnique?: unknown } | undefined | null) =>
    typeof b?.createUnique === 'function',
  supportsMkdirUnique: (b: { mkdirUnique?: unknown } | undefined | null) =>
    typeof b?.mkdirUnique === 'function',
}))

// Stub the shared download util so the OS-file download is observable without
// jsdom's unimplemented createObjectURL / navigation. We capture the Blob it is
// handed and assert byte-identity against the stored content.
vi.mock('../../../lib/download-file', () => ({
  triggerDownload: vi.fn(),
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

// --- T1b-4: in-place rename (file + folder) via a SINGLE backend.rename +
// pure applyPathMutation -------------------------------------------------------
//
// These cases exercise the REAL InAppBackend (fake-indexeddb) + the REAL tab /
// editor stores, so we verify the full path: one backend re-key (recursive for
// folders, T1b-1) followed by a pure pane/buffer re-point that touches NO
// backend. The fs-backend module mock returns whatever `backend` points at, so
// assigning a real InAppBackend here routes storage-actions through it.

function deleteInappDB(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase('pdx-inapp-fs')
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
    req.onblocked = () => reject(new Error('deleteDatabase blocked — connection not closed'))
  })
}

function makeEditorTab(tabId: string, paneId: string, filePath: string): Tab {
  return {
    id: tabId,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: {
      type: 'leaf',
      pane: { id: paneId, content: { kind: 'editor', source: { type: 'inapp' }, filePath } },
    },
  }
}

function makePreviewTab(
  tabId: string,
  paneId: string,
  filePath: string,
  kind: 'image-preview' | 'pdf-preview',
): Tab {
  return {
    id: tabId,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: {
      type: 'leaf',
      pane: { id: paneId, content: { kind, source: { type: 'inapp' }, filePath } },
    },
  }
}

function seedBuffer(filePath: string, paneId: string, languageSource: 'extension' | 'manual' = 'extension', language = 'markdown') {
  useEditorStore.setState({
    buffers: {
      [`inapp:${filePath}`]: makeEditorBuffer({ language, languageSource }),
    },
    paneStates: {
      [paneId]: makeEditorPaneState(`inapp:${filePath}`),
    },
  })
}

describe('renameStorageEntry — file + folder via one backend.rename + pure applyPathMutation (T1b-4)', () => {
  let real: InAppBackend
  const enc = (s: string) => new TextEncoder().encode(s)
  const dec = (b: Uint8Array) => new TextDecoder().decode(b)

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real // overrides the top-level `backend = null`
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
  })

  it('T4-1: file rename — old gone, new present, content + mtime intact, exactly one backend.rename', async () => {
    await real.write('/buffer/a.md', enc('hello'))
    const before = await real.stat('/buffer/a.md')
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await renameStorageEntry('/buffer/a.md', 'b.md')

    expect(res).toEqual({ ok: true })
    expect(renameSpy).toHaveBeenCalledTimes(1)
    expect(renameSpy).toHaveBeenCalledWith('/buffer/a.md', '/buffer/b.md')
    await expect(real.stat('/buffer/a.md')).rejects.toThrow()
    expect(dec(await real.read('/buffer/b.md'))).toBe('hello')
    expect((await real.stat('/buffer/b.md')).mtime).toBe(before.mtime)
  })

  it('T4-2: file rename onto an existing path — refused, NO mutation (source + target untouched)', async () => {
    await real.write('/buffer/a.md', enc('AAA'))
    await real.write('/buffer/b.md', enc('BBB'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await renameStorageEntry('/buffer/a.md', 'b.md')

    expect(res).toEqual({ kind: 'exists' })
    expect(renameSpy).not.toHaveBeenCalled()
    expect(dec(await real.read('/buffer/a.md'))).toBe('AAA')
    expect(dec(await real.read('/buffer/b.md'))).toBe('BBB')
  })

  it('T4-3: folder rename moves ≥2 nested descendants (AC-1b), one backend.rename', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/a/c/d.md', enc('DD'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await renameStorageEntry('/buffer/a', 'z')

    expect(res).toEqual({ ok: true })
    expect(renameSpy).toHaveBeenCalledTimes(1)
    expect(renameSpy).toHaveBeenCalledWith('/buffer/a', '/buffer/z')
    expect(dec(await real.read('/buffer/z/b.md'))).toBe('BB')
    expect(dec(await real.read('/buffer/z/c/d.md'))).toBe('DD')
    await expect(real.stat('/buffer/a')).rejects.toThrow()
    await expect(real.stat('/buffer/a/b.md')).rejects.toThrow()
    await expect(real.stat('/buffer/a/c/d.md')).rejects.toThrow()
  })

  it('T4-4: folder rename onto an existing folder name — refused before any mutation', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/z/keep.md', enc('K')) // z already exists as a dir
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await renameStorageEntry('/buffer/a', 'z')

    expect(res).toEqual({ kind: 'exists' })
    expect(renameSpy).not.toHaveBeenCalled()
    expect(dec(await real.read('/buffer/a/b.md'))).toBe('BB')
    expect(dec(await real.read('/buffer/z/keep.md'))).toBe('K')
  })

  it('T4-5: folder rename re-points an open EDITOR descendant pane (no stale/orphan buffer)', async () => {
    await real.write('/buffer/a/b.md', enc('content'))
    useTabStore.setState({
      tabs: { T1: makeEditorTab('T1', 'P1', '/buffer/a/b.md') },
      tabOrder: ['T1'],
      activeTabId: 'T1',
      visitHistory: [],
    })
    seedBuffer('/buffer/a/b.md', 'P1')

    const res = await renameStorageEntry('/buffer/a', 'z')

    expect(res).toEqual({ ok: true })
    const tab = useTabStore.getState().tabs.T1
    expect(tab.layout.type).toBe('leaf')
    if (tab.layout.type !== 'leaf') throw new Error('expected leaf')
    expect(tab.layout.pane.content).toMatchObject({ kind: 'editor', filePath: '/buffer/z/b.md' })
    const ed = useEditorStore.getState()
    expect(ed.buffers['inapp:/buffer/z/b.md']).toBeDefined()
    expect(ed.buffers['inapp:/buffer/a/b.md']).toBeUndefined()
    expect(ed.paneStates.P1?.bufferKey).toBe('inapp:/buffer/z/b.md')
  })

  it('T4-5b: folder rename re-points an open IMAGE-PREVIEW descendant pane (no spurious editor buffer)', async () => {
    await real.write('/buffer/a/p.png', enc('PNG'))
    useTabStore.setState({
      tabs: { T2: makePreviewTab('T2', 'P2', '/buffer/a/p.png', 'image-preview') },
      tabOrder: ['T2'],
      activeTabId: 'T2',
      visitHistory: [],
    })
    useEditorStore.setState({ buffers: {}, paneStates: {} })

    const res = await renameStorageEntry('/buffer/a', 'z')

    expect(res).toEqual({ ok: true })
    const tab = useTabStore.getState().tabs.T2
    if (tab.layout.type !== 'leaf') throw new Error('expected leaf')
    expect(tab.layout.pane.content).toMatchObject({ kind: 'image-preview', filePath: '/buffer/z/p.png' })
    // A preview pane has no editor buffer — none must be conjured by the remap.
    expect(useEditorStore.getState().buffers['inapp:/buffer/z/p.png']).toBeUndefined()
  })
})

// --- T1b-5: recursive delete with folder-aware locked/dirty guards ------------
//
// Same real-backend harness: a folder delete must sweep every descendant
// (recursive), and the locked/dirty guards must fire when ANY affected pane is a
// descendant of a deleted folder (not just an exact-path match). The window
// confirm is stubbed per-case.

describe('deleteStorageEntries — recursive folder delete + descendant-aware guards (T1b-5)', () => {
  let real: InAppBackend
  const enc = (s: string) => new TextEncoder().encode(s)
  const t: (key: string, params?: Record<string, string | number>) => string = (k) => k
  const originalConfirm = window.confirm

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    window.confirm = () => true
  })

  afterEach(() => {
    window.confirm = originalConfirm
  })

  it('T5-1: folder delete removes the folder + ALL descendants (list empty under it)', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/a/c/d.md', enc('DD'))
    await real.write('/buffer/keep.md', enc('K'))

    const res = await deleteStorageEntries(['/buffer/a'], t)

    expect(res).toEqual({ status: 'deleted' })
    await expect(real.stat('/buffer/a')).rejects.toThrow()
    await expect(real.stat('/buffer/a/b.md')).rejects.toThrow()
    await expect(real.stat('/buffer/a/c/d.md')).rejects.toThrow()
    // Sibling outside the folder is untouched.
    expect(await real.list('/buffer')).toEqual([
      expect.objectContaining({ name: 'keep.md', isDir: false }),
    ])
  })

  it('T5-2: a descendant open in a LOCKED tab refuses the whole delete (nothing deleted)', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    useTabStore.setState({
      tabs: { T1: { ...makeEditorTab('T1', 'P1', '/buffer/a/b.md'), locked: true } },
      tabOrder: ['T1'],
      activeTabId: 'T1',
      visitHistory: [],
    })

    const res = await deleteStorageEntries(['/buffer/a'], t)

    expect(res.status).toBe('refused')
    // Refused before any mutation: the folder + descendant are intact.
    expect((await real.stat('/buffer/a')).isDirectory).toBe(true)
    expect(new TextDecoder().decode(await real.read('/buffer/a/b.md'))).toBe('BB')
  })

  it('T5-3: a DIRTY descendant triggers the dirty confirm — confirm deletes, cancel no-ops', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    useTabStore.setState({
      tabs: { T1: makeEditorTab('T1', 'P1', '/buffer/a/b.md') },
      tabOrder: ['T1'],
      activeTabId: 'T1',
      visitHistory: [],
    })
    seedBuffer('/buffer/a/b.md', 'P1')
    // Mark the descendant buffer dirty so the dirty-specific confirm wins.
    useEditorStore.setState((s) => ({
      buffers: { ...s.buffers, 'inapp:/buffer/a/b.md': { ...s.buffers['inapp:/buffer/a/b.md'], isDirty: true } },
    }))

    // Case 1: cancel → cancelled, nothing deleted.
    const cancelSpy = vi.fn((_message?: string) => false)
    window.confirm = cancelSpy
    const cancelled = await deleteStorageEntries(['/buffer/a'], t)
    expect(cancelled).toEqual({ status: 'cancelled' })
    expect(cancelSpy).toHaveBeenCalledTimes(1)
    expect(cancelSpy.mock.calls[0][0]).toContain('delete_dirty_confirm')
    expect((await real.stat('/buffer/a/b.md')).isDirectory).toBe(false)

    // Case 2: confirm → deleted (folder + descendant gone).
    const okSpy = vi.fn((_message?: string) => true)
    window.confirm = okSpy
    const deleted = await deleteStorageEntries(['/buffer/a'], t)
    expect(deleted).toEqual({ status: 'deleted' })
    expect(okSpy.mock.calls[0][0]).toContain('delete_dirty_confirm')
    await expect(real.stat('/buffer/a')).rejects.toThrow()
    await expect(real.stat('/buffer/a/b.md')).rejects.toThrow()
  })

  it('T5-4: file delete (1a regression) still guarded + works', async () => {
    await real.write('/buffer/x.md', enc('XX'))
    const okSpy = vi.fn((_message?: string) => true)
    window.confirm = okSpy

    const res = await deleteStorageEntries(['/buffer/x.md'], t)

    expect(res).toEqual({ status: 'deleted' })
    expect(okSpy).toHaveBeenCalledTimes(1)
    // A lone file uses the single-file confirm, not the dirty one.
    expect(okSpy.mock.calls[0][0]).toContain('delete_one_confirm')
    await expect(real.stat('/buffer/x.md')).rejects.toThrow()
  })

  it('T5-4b: file delete open in a LOCKED tab is refused (regression)', async () => {
    await real.write('/buffer/x.md', enc('XX'))
    useTabStore.setState({
      tabs: { T1: { ...makeEditorTab('T1', 'P1', '/buffer/x.md'), locked: true } },
      tabOrder: ['T1'],
      activeTabId: 'T1',
      visitHistory: [],
    })

    const res = await deleteStorageEntries(['/buffer/x.md'], t)

    expect(res.status).toBe('refused')
    expect(new TextDecoder().decode(await real.read('/buffer/x.md'))).toBe('XX')
  })
})

// --- T1b-6a: pure recursive move (drag target = a directory) via a SINGLE
// backend.rename + pure applyPathMutation --------------------------------------
//
// Same real-backend + real-store harness as rename: `moveStorageEntry(from,
// targetDir)` computes `to = targetDir/basename(from)` and re-uses the exact
// rename machinery — one recursive backend.rename (T1b-1) + the pure
// applyPathMutation re-point (T1b-4). The no-op guards (already-there / into self
// / into own descendant) and the pre-mutation collision check must fire WITHOUT
// any backend.rename, asserted with a spy.

describe('moveStorageEntry — pure recursive move + guards (T1b-6a)', () => {
  let real: InAppBackend
  const enc = (s: string) => new TextEncoder().encode(s)
  const dec = (b: Uint8Array) => new TextDecoder().decode(b)

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
  })

  it('T6-1: move a file into a folder — old gone, new under folder, content intact, one rename', async () => {
    await real.write('/buffer/x.md', enc('hello'))
    await real.mkdir('/buffer/dir')
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await moveStorageEntry('/buffer/x.md', '/buffer/dir')

    expect(res).toEqual({ ok: true })
    expect(renameSpy).toHaveBeenCalledTimes(1)
    expect(renameSpy).toHaveBeenCalledWith('/buffer/x.md', '/buffer/dir/x.md')
    await expect(real.stat('/buffer/x.md')).rejects.toThrow()
    expect(dec(await real.read('/buffer/dir/x.md'))).toBe('hello')
  })

  it('T6-2: move a folder into another folder — ≥2 nested descendants re-keyed (AC-1b), one rename', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/a/c/d.md', enc('DD'))
    await real.mkdir('/buffer/dest')
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await moveStorageEntry('/buffer/a', '/buffer/dest')

    expect(res).toEqual({ ok: true })
    expect(renameSpy).toHaveBeenCalledTimes(1)
    expect(renameSpy).toHaveBeenCalledWith('/buffer/a', '/buffer/dest/a')
    expect(dec(await real.read('/buffer/dest/a/b.md'))).toBe('BB')
    expect(dec(await real.read('/buffer/dest/a/c/d.md'))).toBe('DD')
    await expect(real.stat('/buffer/a')).rejects.toThrow()
    await expect(real.stat('/buffer/a/b.md')).rejects.toThrow()
    await expect(real.stat('/buffer/a/c/d.md')).rejects.toThrow()
  })

  it('T6-3a: move onto the CURRENT parent dir — no-op, no mutation', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await moveStorageEntry('/buffer/a/b.md', '/buffer/a')

    expect(res).toEqual({ kind: 'noop' })
    expect(renameSpy).not.toHaveBeenCalled()
    expect(dec(await real.read('/buffer/a/b.md'))).toBe('BB')
  })

  it('T6-3b: move a folder onto ITSELF — no-op, no mutation', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await moveStorageEntry('/buffer/a', '/buffer/a')

    expect(res).toEqual({ kind: 'noop' })
    expect(renameSpy).not.toHaveBeenCalled()
    expect((await real.stat('/buffer/a')).isDirectory).toBe(true)
    expect(dec(await real.read('/buffer/a/b.md'))).toBe('BB')
  })

  it('T6-3c: move a folder into its OWN descendant — no-op, no mutation', async () => {
    await real.write('/buffer/a/c/d.md', enc('DD'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await moveStorageEntry('/buffer/a', '/buffer/a/c')

    expect(res).toEqual({ kind: 'noop' })
    expect(renameSpy).not.toHaveBeenCalled()
    expect(dec(await real.read('/buffer/a/c/d.md'))).toBe('DD')
  })

  it('T6-4: move where the target name already exists — refused (exists), no mutation', async () => {
    await real.write('/buffer/x.md', enc('AAA'))
    await real.write('/buffer/dir/x.md', enc('BBB')) // collision at /buffer/dir/x.md
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await moveStorageEntry('/buffer/x.md', '/buffer/dir')

    expect(res).toEqual({ kind: 'exists' })
    expect(renameSpy).not.toHaveBeenCalled()
    expect(dec(await real.read('/buffer/x.md'))).toBe('AAA')
    expect(dec(await real.read('/buffer/dir/x.md'))).toBe('BBB')
  })

  it('T6-5: moving a folder with an OPEN editor descendant re-points the pane (no stale buffer)', async () => {
    await real.write('/buffer/a/b.md', enc('content'))
    await real.mkdir('/buffer/dest')
    useTabStore.setState({
      tabs: { T1: makeEditorTab('T1', 'P1', '/buffer/a/b.md') },
      tabOrder: ['T1'],
      activeTabId: 'T1',
      visitHistory: [],
    })
    seedBuffer('/buffer/a/b.md', 'P1')

    const res = await moveStorageEntry('/buffer/a', '/buffer/dest')

    expect(res).toEqual({ ok: true })
    const tab = useTabStore.getState().tabs.T1
    if (tab.layout.type !== 'leaf') throw new Error('expected leaf')
    expect(tab.layout.pane.content).toMatchObject({ kind: 'editor', filePath: '/buffer/dest/a/b.md' })
    const ed = useEditorStore.getState()
    expect(ed.buffers['inapp:/buffer/dest/a/b.md']).toBeDefined()
    expect(ed.buffers['inapp:/buffer/a/b.md']).toBeUndefined()
    expect(ed.paneStates.P1?.bufferKey).toBe('inapp:/buffer/dest/a/b.md')
  })

  it('T6-error: surfaces a backend rename failure as a kind:error outcome', async () => {
    await real.write('/buffer/x.md', enc('hello'))
    vi.spyOn(real, 'rename').mockRejectedValueOnce(new Error('boom'))

    const res = await moveStorageEntry('/buffer/x.md', '/buffer/dir')

    expect(res).toEqual({ kind: 'error', message: 'boom' })
  })
})

// --- codex B2: deleting a folder + one of its descendants together must not
// half-delete then error on the now-missing descendant -----------------------

describe('deleteStorageEntries — overlapping (folder + descendant) selection is normalized (B2)', () => {
  let real: InAppBackend
  const enc = (s: string) => new TextEncoder().encode(s)
  const t: (key: string, params?: Record<string, string | number>) => string = (k) => k
  const originalConfirm = window.confirm

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    window.confirm = () => true
  })

  afterEach(() => {
    window.confirm = originalConfirm
  })

  it('B2: selecting a folder AND its descendant deletes cleanly (status deleted, not error)', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/a/c/d.md', enc('DD'))
    // Select the folder AND a nested descendant of it. Pre-fix the loop deleted
    // the folder recursively, then re-deleted the now-missing descendant →
    // not-found → status:error after the folder was already (partly) gone.
    const deleteSpy = vi.spyOn(real, 'delete')

    const res = await deleteStorageEntries(['/buffer/a', '/buffer/a/c/d.md'], t)

    expect(res).toEqual({ status: 'deleted' })
    // The descendant covered by the folder ancestor is dropped from the set, so
    // exactly one delete (the folder) runs.
    expect(deleteSpy).toHaveBeenCalledTimes(1)
    expect(deleteSpy).toHaveBeenCalledWith('/buffer/a', true)
    await expect(real.stat('/buffer/a')).rejects.toThrow()
    await expect(real.stat('/buffer/a/c/d.md')).rejects.toThrow()
  })

  it('B2: a non-overlapping multi-select still deletes every target', async () => {
    await real.write('/buffer/x.md', enc('XX'))
    await real.write('/buffer/y.md', enc('YY'))

    const res = await deleteStorageEntries(['/buffer/x.md', '/buffer/y.md'], t)

    expect(res).toEqual({ status: 'deleted' })
    await expect(real.stat('/buffer/x.md')).rejects.toThrow()
    await expect(real.stat('/buffer/y.md')).rejects.toThrow()
  })
})

// --- codex B4: renaming to the current name is a no-op success ----------------

describe('renameStorageEntry — same-name rename is a no-op success (B4)', () => {
  let real: InAppBackend
  const enc = (s: string) => new TextEncoder().encode(s)
  const dec = (b: Uint8Array) => new TextDecoder().decode(b)

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
  })

  it('B4: rename to the same basename returns ok WITHOUT calling backend.rename', async () => {
    await real.write('/buffer/foo.md', enc('hello'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await renameStorageEntry('/buffer/foo.md', 'foo.md')

    expect(res).toEqual({ ok: true })
    expect(renameSpy).not.toHaveBeenCalled()
    // Untouched: the same-name short-circuit never reached the collision scan
    // (which would have rejected the source as its own target).
    expect(dec(await real.read('/buffer/foo.md'))).toBe('hello')
  })

  it('B4: same-name rename of a folder is also a no-op success', async () => {
    await real.write('/buffer/dir/x.md', enc('X'))
    const renameSpy = vi.spyOn(real, 'rename')

    const res = await renameStorageEntry('/buffer/dir', 'dir')

    expect(res).toEqual({ ok: true })
    expect(renameSpy).not.toHaveBeenCalled()
    expect(dec(await real.read('/buffer/dir/x.md'))).toBe('X')
  })
})

// --- codex D2 (AC-1b clause 6): rapid/concurrent new-file reservations from the
// Storage entry point converge on the atomic namer — distinct keys, never shared

describe('createStorageFile — concurrent reservations get distinct keys (D2 / #854)', () => {
  let real: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
  })

  it('D2: two concurrent createStorageFile calls reserve two distinct paths (no shared key)', async () => {
    const [a, b] = await Promise.all([
      createStorageFile('/buffer'),
      createStorageFile('/buffer'),
    ])
    expect(a.error).toBeUndefined()
    expect(b.error).toBeUndefined()
    // Both reservations materialized as two distinct files under /buffer.
    const entries = await real.list('/buffer')
    const names = entries.map((e) => e.name).sort()
    expect(names).toEqual(['Untitled-1.md', 'Untitled.md'])
  })
})

// --- T1c-2: download / export a stored file via the shared triggerDownload util
//
// Real InAppBackend (fake-indexeddb) so the download reads back the exact bytes
// that were written; triggerDownload is mocked (module-level) so we can capture
// the Blob and assert byte-identity + filename without jsdom's missing
// createObjectURL.

describe('downloadStorageFile — byte-identical export via shared triggerDownload (T1c-2)', () => {
  let real: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
    ;(triggerDownload as unknown as ReturnType<typeof vi.fn>).mockReset()
  })

  it('T2-1: reads the stored bytes and hands triggerDownload a byte-identical Blob named by basename', async () => {
    // Arbitrary binary content (incl. a zero byte) so any text round-trip bug
    // would corrupt it.
    const bytes = new Uint8Array([0x00, 0x01, 0x42, 0xff, 0x10, 0x7a])
    await real.write('/buffer/sub/report.bin', bytes)

    const res = await downloadStorageFile('/buffer/sub/report.bin')

    expect(res).toEqual({ ok: true })
    expect(triggerDownload).toHaveBeenCalledTimes(1)
    const [blob, filename] = (triggerDownload as unknown as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(filename).toBe('report.bin')
    expect(blob).toBeInstanceOf(Blob)
    // Byte-identical: read the Blob back and compare every byte.
    const roundTrip = new Uint8Array(await (blob as Blob).arrayBuffer())
    expect(Array.from(roundTrip)).toEqual(Array.from(bytes))
  })

  it('T2-1b: missing backend returns an error and never calls triggerDownload', async () => {
    backend = null
    const res = await downloadStorageFile('/buffer/x.md')
    expect('error' in res && res.error).toBeTruthy()
    expect(triggerDownload).not.toHaveBeenCalled()
  })

  it('T2-1c: a directory path is a read failure → error, no download', async () => {
    await real.write('/buffer/dir/f.md', new TextEncoder().encode('x'))
    const res = await downloadStorageFile('/buffer/dir')
    expect('error' in res && res.error).toBeTruthy()
    expect(triggerDownload).not.toHaveBeenCalled()
  })

  it('T2-1d: a missing file is a read failure → error, no download', async () => {
    const res = await downloadStorageFile('/buffer/nope.md')
    expect('error' in res && res.error).toBeTruthy()
    expect(triggerDownload).not.toHaveBeenCalled()
  })
})

// --- T1c-1: upload (OS-file ingest) via the atomic createUnique(+content) ------
//
// Real InAppBackend so the byte write + atomic -N suffix collision are genuinely
// exercised; uploadFiles partial-success is asserted with a stub backend so one
// file can deterministically fail.

describe('uploadFile / uploadFiles — OS-file ingest via atomic createUnique (T1c-1)', () => {
  let real: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
  })

  it('T1-1: stores a file with its bytes under a parsed (baseName, ext)', async () => {
    const file = new File([new Uint8Array([1, 2, 3])], 'photo.png', { type: 'image/png' })
    const res = await uploadFile('/buffer', file)
    expect(res).toEqual({ path: '/buffer/photo.png' })
    expect(Array.from(await real.read('/buffer/photo.png'))).toEqual([1, 2, 3])
  })

  it('parses an ext-less name with NO trailing dot', async () => {
    const file = new File([new Uint8Array([9])], 'README')
    const res = await uploadFile('/buffer', file)
    expect(res).toEqual({ path: '/buffer/README' })
    expect(Array.from(await real.read('/buffer/README'))).toEqual([9])
  })

  it('splits on the LAST dot (archive.tar.gz → archive.tar + gz)', async () => {
    const file = new File([new Uint8Array([4])], 'archive.tar.gz')
    const res = await uploadFile('/buffer', file)
    expect(res).toEqual({ path: '/buffer/archive.tar.gz' })
  })

  it('T1-3: a name collision reserves an atomic -N suffix, original intact', async () => {
    await real.write('/buffer/report.pdf', new Uint8Array([0]))
    const file = new File([new Uint8Array([7, 7])], 'report.pdf')
    const res = await uploadFile('/buffer', file)
    expect(res).toEqual({ path: '/buffer/report-1.pdf' })
    expect(Array.from(await real.read('/buffer/report.pdf'))).toEqual([0])
    expect(Array.from(await real.read('/buffer/report-1.pdf'))).toEqual([7, 7])
  })

  it('T1-5: uploadFiles returns every uploaded path in the summary (no failures)', async () => {
    const files = [
      new File([new Uint8Array([1])], 'a.txt'),
      new File([new Uint8Array([2])], 'b.png'),
      new File([new Uint8Array([3])], 'c.docx'),
    ]
    const summary = await uploadFiles('/buffer', files)
    expect(summary.failed).toEqual([])
    expect(summary.uploaded.sort()).toEqual(
      ['/buffer/a.txt', '/buffer/b.png', '/buffer/c.docx'].sort(),
    )
  })

  it('T1-6: a partial failure populates BOTH uploaded and failed (not just the first error)', async () => {
    // Stub backend so a specific file name throws — one good, one bad.
    backend = {
      createUnique: vi.fn(async (dir: string, base: string, ext: string) => {
        if (base === 'bad') throw new Error('disk boom')
        return ext === '' ? `${dir}/${base}` : `${dir}/${base}.${ext}`
      }),
    }
    const good = new File([new Uint8Array([1])], 'ok.txt')
    const bad = new File([new Uint8Array([2])], 'bad.bin')
    const summary = await uploadFiles('/buffer', [good, bad])
    expect(summary.uploaded).toEqual(['/buffer/ok.txt'])
    expect(summary.failed).toEqual([{ name: 'bad.bin', reason: 'disk boom', kind: 'other' }])
  })

  it('missing backend → uploadFile returns a typed error (no silent no-op)', async () => {
    backend = null
    const res = await uploadFile('/buffer', new File([new Uint8Array([1])], 'x.txt'))
    expect(res).toEqual({ kind: 'error', name: 'x.txt', message: expect.any(String) })
  })
})

// --- codex R2 C1: file.name sanitisation (path-traversal / hidden-data guard)
//
// The browser File API hands back whatever the OS reported. A name carrying a
// path separator or a traversal token must never form a key OUTSIDE the target
// dir or one the tree cannot surface. uploadFile flattens to a basename, strips
// control characters, and rejects empty / dot-only names — proven here against
// a REAL InAppBackend so we can also assert NOTHING escaped the storage root.

describe('uploadFile — file.name sanitisation (C1 security)', () => {
  let real: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
  })

  /** Every TREE key written to the real backend must live under the storage root.
   * The backend's internal reserved revision record (a non-path meta key) is not
   * a tree entry and is excluded here. */
  async function assertAllKeysUnderRoot(): Promise<string[]> {
    const db = await (real as unknown as { db(): Promise<{ getAllKeys(store: string): Promise<string[]> }> }).db()
    const allKeys = await db.getAllKeys('files')
    const keys = allKeys.filter((k) => k.startsWith('/'))
    for (const k of keys) {
      expect(k === '/buffer' || k.startsWith('/buffer/')).toBe(true)
    }
    return keys
  }

  it('flattens a name carrying a forward slash to its basename (no nested key)', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([1])], 'sub/evil.txt'))
    expect(res).toEqual({ path: '/buffer/evil.txt' })
    // The flattened file is a DIRECT child of /buffer — no stray `sub` dir, and
    // crucially no invisible `/buffer/sub/evil.txt` the tree could not show.
    const keys = await assertAllKeysUnderRoot()
    expect(keys).toEqual(['/buffer/evil.txt'])
  })

  it('flattens a name carrying a backslash to its basename', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([2])], 'sub\\evil.txt'))
    expect(res).toEqual({ path: '/buffer/evil.txt' })
    await assertAllKeysUnderRoot()
  })

  it('a `../` traversal name flattens to its basename and never escapes the root', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([3])], '../evil.txt'))
    expect(res).toEqual({ path: '/buffer/evil.txt' })
    const keys = await assertAllKeysUnderRoot()
    expect(keys).toEqual(['/buffer/evil.txt'])
  })

  it('rejects a `..` traversal token (no write)', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([4])], '..'))
    expect(res).toMatchObject({ kind: 'error' })
    expect(await assertAllKeysUnderRoot()).toEqual([])
  })

  it('rejects an un-normalized traversal in targetDir before any write (C1b defense-in-depth)', async () => {
    // `isUnderRoot` is a prefix check and `join` keeps `..` literal, so a
    // targetDir carrying a traversal token must be rejected by the explicit
    // segment guard, not slip a `/buffer/../evil.txt` candidate past the root.
    const res = await uploadFile('/buffer/..', new File([new Uint8Array([9])], 'evil.txt'))
    expect(res).toMatchObject({ kind: 'error' })
    expect(await assertAllKeysUnderRoot()).toEqual([])
  })

  it('rejects a lone `.` name (no write)', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([5])], '.'))
    expect(res).toMatchObject({ kind: 'error' })
    expect(await assertAllKeysUnderRoot()).toEqual([])
  })

  it('rejects a pure-whitespace name (no write)', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([6])], '   '))
    expect(res).toMatchObject({ kind: 'error' })
    expect(await assertAllKeysUnderRoot()).toEqual([])
  })

  it('strips control characters from the name before reserving the key', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([7])], 'a\u0000\u0001b.txt'))
    expect(res).toEqual({ path: '/buffer/ab.txt' })
    await assertAllKeysUnderRoot()
  })

  it('does NOT silently rewrite a trailing-dot name (`foo.` stays `foo.`, not `foo`)', async () => {
    const res = await uploadFile('/buffer', new File([new Uint8Array([8])], 'foo.'))
    // The trailing dot is preserved as part of the basename — never collapsed to
    // `/buffer/foo` behind the user's back.
    expect(res).toEqual({ path: '/buffer/foo.' })
    expect(await assertAllKeysUnderRoot()).toEqual(['/buffer/foo.'])
  })
})

// --- T1c-4: soft size cap + quota guard on upload (owns ALL upload guards) -----
//
// The cap is checked BEFORE any write (an over-cap file never reaches
// `createUnique`); a quota throw at write time is caught via the shared
// `isQuotaError` and surfaced (never silent). `uploadFiles` folds both into the
// summary's `failed[]` with the right typed reason.

/** A File whose `size` is forced past the cap WITHOUT allocating the bytes. */
function oversizedFile(name: string, size: number): File {
  const file = new File([new Uint8Array([0])], name)
  Object.defineProperty(file, 'size', { value: size })
  return file
}

describe('uploadFile / uploadFiles — size cap + quota guards (T1c-4)', () => {
  it('T4-1: a file over the cap returns a typed too-large result and never calls createUnique', async () => {
    const createUnique = vi.fn()
    backend = { createUnique }
    const file = oversizedFile('huge.bin', SOFT_MAX_UPLOAD_BYTES + 1)
    const res = await uploadFile('/buffer', file)
    expect(res).toEqual({ kind: 'too-large', name: 'huge.bin', cap: SOFT_MAX_UPLOAD_BYTES })
    expect(createUnique).not.toHaveBeenCalled()
  })

  it('a file exactly at the cap is accepted (boundary is strictly-greater)', async () => {
    const createUnique = vi.fn(async () => '/buffer/edge.bin')
    backend = { createUnique }
    const res = await uploadFile('/buffer', oversizedFile('edge.bin', SOFT_MAX_UPLOAD_BYTES))
    expect(res).toEqual({ path: '/buffer/edge.bin' })
    expect(createUnique).toHaveBeenCalledTimes(1)
  })

  it('T4-2: a QuotaExceededError thrown by the write surfaces as a typed quota result (not silent)', async () => {
    const createUnique = vi.fn(async () => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    backend = { createUnique }
    const res = await uploadFile('/buffer', new File([new Uint8Array([1])], 'x.txt'))
    expect(res).toEqual({ kind: 'quota', name: 'x.txt' })
  })

  it('uploadFiles folds tooLarge + quota into failed[] with typed reasons', async () => {
    const createUnique = vi.fn(async (dir: string, base: string, ext: string) => {
      if (base === 'boom') throw new DOMException('full', 'QuotaExceededError')
      return ext === '' ? `${dir}/${base}` : `${dir}/${base}.${ext}`
    })
    backend = { createUnique }
    const summary = await uploadFiles('/buffer', [
      new File([new Uint8Array([1])], 'ok.txt'),
      oversizedFile('huge.bin', SOFT_MAX_UPLOAD_BYTES + 1),
      new File([new Uint8Array([2])], 'boom.bin'),
    ])
    expect(summary.uploaded).toEqual(['/buffer/ok.txt'])
    expect(summary.failed).toEqual([
      { name: 'huge.bin', reason: 'too-large', kind: 'too-large', cap: SOFT_MAX_UPLOAD_BYTES },
      { name: 'boom.bin', reason: 'quota', kind: 'quota' },
    ])
  })
})

// --- T3.2: the recent-files list follows every path mutation -----------------
//
// The Recent list used to keep pointing at the OLD path after a rename/move and
// to keep listing deleted files, because no storage action ever told the store.
// These cases pin the wiring at the action layer with NO pane open, so they
// prove the remap is unconditional and not a side effect of the pane re-point.
describe('storage-actions — recent files follow rename / move / delete (T3.2)', () => {
  let real: InAppBackend
  const enc = (s: string) => new TextEncoder().encode(s)
  const t: (key: string, params?: Record<string, string | number>) => string = (k) => k
  const originalConfirm = window.confirm

  function recent(
    path: string,
    source: FileSource = { type: 'inapp' },
    openedAt = 1,
  ): RecentFileEntry {
    return {
      source,
      path,
      name: path.split('/').pop() ?? path,
      kind: 'editor',
      openedAt,
    }
  }

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    real = new InAppBackend()
    backend = real
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useRecentFilesStore.setState({ files: [] })
    window.confirm = () => true
  })

  afterEach(() => {
    window.confirm = originalConfirm
  })

  it('T3.2-1: a file rename re-points its recent entry (path + name) with no pane open', async () => {
    await real.write('/buffer/a.md', enc('hello'))
    useRecentFilesStore.setState({ files: [recent('/buffer/a.md')] })

    const res = await renameStorageEntry('/buffer/a.md', 'b.md')

    expect(res).toEqual({ ok: true })
    expect(useRecentFilesStore.getState().files).toEqual([
      { ...recent('/buffer/a.md'), path: '/buffer/b.md', name: 'b.md' },
    ])
  })

  it('T3.2-1b: an identically-pathed entry on another source is untouched by an in-app rename', async () => {
    await real.write('/buffer/a.md', enc('hello'))
    const remote = recent('/buffer/a.md', { type: 'daemon', hostId: 'hostB' }, 2)
    useRecentFilesStore.setState({ files: [recent('/buffer/a.md'), remote] })

    await renameStorageEntry('/buffer/a.md', 'b.md')

    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual([
      '/buffer/b.md',
      '/buffer/a.md',
    ])
    expect(useRecentFilesStore.getState().files[1]).toEqual(remote)
  })

  it('T3.2-2: a folder MOVE remaps every recent descendant (same applyPathMutation call site)', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/a/c/d.md', enc('DD'))
    await real.mkdir('/buffer/dest')
    useRecentFilesStore.setState({
      files: [recent('/buffer/a/b.md'), recent('/buffer/a/c/d.md'), recent('/buffer/ab.md')],
    })

    const res = await moveStorageEntry('/buffer/a', '/buffer/dest')

    expect(res).toEqual({ ok: true })
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual([
      '/buffer/dest/a/b.md',
      '/buffer/dest/a/c/d.md',
      // Trailing-slash boundary: the sibling `ab.md` is NOT under `a`.
      '/buffer/ab.md',
    ])
  })

  it('T3.2-2b: a folder RENAME remaps every recent descendant', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    useRecentFilesStore.setState({ files: [recent('/buffer/a/b.md')] })

    await renameStorageEntry('/buffer/a', 'z')

    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual(['/buffer/z/b.md'])
  })

  it('T3.2-3: deleting a folder drops its recent descendants and keeps everything else', async () => {
    await real.write('/buffer/a/b.md', enc('BB'))
    await real.write('/buffer/a/c/d.md', enc('DD'))
    await real.write('/buffer/keep.md', enc('K'))
    await real.write('/buffer/ab.md', enc('AB'))
    useRecentFilesStore.setState({
      files: [
        recent('/buffer/a/b.md'),
        recent('/buffer/a/c/d.md'),
        recent('/buffer/keep.md'),
        recent('/buffer/ab.md'),
      ],
    })

    const res = await deleteStorageEntries(['/buffer/a'], t)

    expect(res).toEqual({ status: 'deleted' })
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual([
      '/buffer/keep.md',
      '/buffer/ab.md',
    ])
  })

  it('T3.2-3b: deleting several files drops exactly those recent entries', async () => {
    await real.write('/buffer/x.md', enc('X'))
    await real.write('/buffer/y.md', enc('Y'))
    await real.write('/buffer/z.md', enc('Z'))
    useRecentFilesStore.setState({
      files: [recent('/buffer/x.md'), recent('/buffer/y.md'), recent('/buffer/z.md')],
    })

    const res = await deleteStorageEntries(['/buffer/x.md', '/buffer/z.md'], t)

    expect(res).toEqual({ status: 'deleted' })
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual(['/buffer/y.md'])
  })
})
