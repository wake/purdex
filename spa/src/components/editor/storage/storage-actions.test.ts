import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  createStorageFile,
  createStorageFolder,
  deleteStorageEntries,
  renameStorageEntry,
} from './storage-actions'
import { InAppBackend } from '../../../lib/fs-backend-inapp'
import { closeAllIDB } from '../../../lib/storage/idb'
import { useTabStore } from '../../../stores/useTabStore'
import { useEditorStore } from '../../../stores/useEditorStore'
import type { Tab } from '../../../types/tab'

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

// --- T1b-4: in-place rename (file + folder) via a SINGLE backend.rename +
// pure remapPanesUnder -------------------------------------------------------
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
      [`inapp:${filePath}`]: {
        content: 'X',
        savedContent: 'X',
        isDirty: false,
        lastStat: null,
        modelId: 'm1',
        language,
        languageSource,
        eol: 'lf',
        encoding: 'utf8',
      },
    },
    paneStates: {
      [paneId]: {
        bufferKey: `inapp:${filePath}`,
        editorMode: 'raw',
        showDiff: false,
        cursorPosition: { line: 1, column: 1 },
        monacoViewState: null,
        tiptapViewState: null,
      },
    },
  })
}

describe('renameStorageEntry — file + folder via one backend.rename + pure remapPanesUnder (T1b-4)', () => {
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
    const cancelSpy = vi.fn(() => false)
    window.confirm = cancelSpy
    const cancelled = await deleteStorageEntries(['/buffer/a'], t)
    expect(cancelled).toEqual({ status: 'cancelled' })
    expect(cancelSpy).toHaveBeenCalledTimes(1)
    expect(cancelSpy.mock.calls[0][0]).toContain('delete_dirty_confirm')
    expect((await real.stat('/buffer/a/b.md')).isDirectory).toBe(false)

    // Case 2: confirm → deleted (folder + descendant gone).
    const okSpy = vi.fn(() => true)
    window.confirm = okSpy
    const deleted = await deleteStorageEntries(['/buffer/a'], t)
    expect(deleted).toEqual({ status: 'deleted' })
    expect(okSpy.mock.calls[0][0]).toContain('delete_dirty_confirm')
    await expect(real.stat('/buffer/a')).rejects.toThrow()
    await expect(real.stat('/buffer/a/b.md')).rejects.toThrow()
  })

  it('T5-4: file delete (1a regression) still guarded + works', async () => {
    await real.write('/buffer/x.md', enc('XX'))
    const okSpy = vi.fn(() => true)
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
