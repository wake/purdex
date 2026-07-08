import { describe, it, expect, beforeEach, vi } from 'vitest'
import { openInAppFile } from './open-in-app-file'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useRecentFilesStore } from '../stores/useRecentFilesStore'
import {
  clearAllForHmr,
  registerFileOpener,
} from './file-opener-registry'
import { registerFsBackend, clearFsBackendRegistry } from './fs-backend'
import { editorModuleDefinition } from './register-modules/editor-module'
import { getPrimaryPane } from './pane-tree'
import { triggerDownload } from './download-file'
import type { PaneContent } from '../types/tab'
import type { FsBackend } from './fs-backend'
import type { FileStat } from '../types/fs'

// T1c-3: the download-disposition branch calls triggerDownload (side-effect,
// no tab). Mock it so jsdom never performs a real anchor download and we can
// assert the bytes/filename it was handed.
vi.mock('./download-file', () => ({ triggerDownload: vi.fn() }))

// stat-gate (R2-1): openInAppFile stats the path before opening. The spy
// resolves (file exists) by default; individual tests make it reject to
// simulate a missing/stale entry.
const statMock = vi.fn<(path: string) => Promise<FileStat>>()
// T1c-3: download-disposition reads bytes before triggerDownload.
const readMock = vi.fn<(path: string) => Promise<Uint8Array>>()

function registerStatBackend(): void {
  clearFsBackendRegistry()
  statMock.mockReset()
  statMock.mockResolvedValue({ size: 0, mtime: 0, isDirectory: false, isFile: true })
  readMock.mockReset()
  readMock.mockResolvedValue(new Uint8Array([1, 2, 3]))
  vi.mocked(triggerDownload).mockClear()
  registerFsBackend('inapp', {
    id: 'inapp',
    label: 'In-App',
    available: () => true,
    stat: statMock,
    read: readMock,
  } as unknown as FsBackend)
}

// Register the REAL editor file-openers so registry dispatch
// (md→editor, png→image-preview, pdf→pdf-preview) is genuinely exercised,
// not mocked away.
function registerEditorOpeners(): void {
  clearAllForHmr()
  for (const opener of editorModuleDefinition.fileOpeners ?? []) {
    registerFileOpener({ ...opener, ownerModuleId: 'editor' })
  }
}

function setupWorkspace(): void {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useWorkspaceStore.setState({
    workspaces: [
      { id: 'w1', name: 'W1', tabs: [], activeTabId: null, moduleConfig: {} },
    ],
    activeWorkspaceId: 'w1',
  })
}

/** The primary-pane content of a tab, or null. */
function primaryContent(tabId: string): PaneContent | null {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab) return null
  return getPrimaryPane(tab.layout).content
}

describe('openInAppFile', () => {
  beforeEach(() => {
    registerEditorOpeners()
    registerStatBackend()
    setupWorkspace()
    useRecentFilesStore.setState({ files: [] })
  })

  it('opens a .md file as an editor pane (registry dispatch)', async () => {
    const tabId = await openInAppFile('/buffer/x.md', 'w1')
    expect(tabId).toBeTruthy()
    const content = primaryContent(tabId!)
    expect(content?.kind).toBe('editor')
    expect((content as { filePath: string }).filePath).toBe('/buffer/x.md')
    expect((content as { source: { type: string } }).source.type).toBe('inapp')
  })

  it('records the opened file in recent list', async () => {
    await openInAppFile('/buffer/x.md', 'w1')
    const files = useRecentFilesStore.getState().files
    expect(files.map((f) => f.path)).toContain('/buffer/x.md')
  })

  it('opens a .png file as an image-preview pane (registry dispatch)', async () => {
    const tabId = await openInAppFile('/buffer/p.png', 'w1')
    expect(primaryContent(tabId!)?.kind).toBe('image-preview')
  })

  it('opens a .pdf file as a pdf-preview pane (registry dispatch)', async () => {
    const tabId = await openInAppFile('/buffer/d.pdf', 'w1')
    expect(primaryContent(tabId!)?.kind).toBe('pdf-preview')
  })

  it('focuses the existing tab when the same file is already open (no duplicate)', async () => {
    const first = await openInAppFile('/buffer/x.md', 'w1')
    expect(useTabStore.getState().tabOrder).toHaveLength(1)

    const second = await openInAppFile('/buffer/x.md', 'w1')
    expect(second).toBe(first)
    // No duplicate tab created.
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
    // The existing tab is focused.
    expect(useTabStore.getState().activeTabId).toBe(first)
  })

  it('never reuses an unrelated editor pane — a different filePath gets a new tab', async () => {
    const first = await openInAppFile('/buffer/a.md', 'w1')
    const second = await openInAppFile('/buffer/b.md', 'w1')
    expect(second).not.toBe(first)
    expect(useTabStore.getState().tabOrder).toHaveLength(2)
    expect(primaryContent(second!)).toMatchObject({
      kind: 'editor',
      filePath: '/buffer/b.md',
    })
  })

  it('places the new tab in the workspace via insertTab (ws.tabs + activeTabId synced)', async () => {
    const tabId = await openInAppFile('/buffer/x.md', 'w1')
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')!
    expect(ws.tabs).toContain(tabId)
    expect(ws.activeTabId).toBe(tabId)
  })

  it('distinguishes same-basename files in different dirs (full-path identity)', async () => {
    const a = await openInAppFile('/buffer/dir1/x.md', 'w1')
    const b = await openInAppFile('/buffer/dir2/x.md', 'w1')
    expect(b).not.toBe(a)
    expect(useTabStore.getState().tabOrder).toHaveLength(2)
  })

  it('returns undefined and opens nothing when no opener matches', async () => {
    // A directory-like path with no matching opener (isDirectory false but
    // the editor opener matches any non-binary file, so simulate "no opener"
    // by clearing the registry).
    clearAllForHmr()
    const tabId = await openInAppFile('/buffer/x.md', 'w1')
    expect(tabId).toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
  })

  // --- R2-1: stat-gate (missing/stale path must NOT open a pane) ---

  it('does NOT open a tab when the path is missing (stat rejects)', async () => {
    statMock.mockRejectedValue(new Error('not found'))
    const tabId = await openInAppFile('/buffer/ghost.md', 'w1')
    expect(tabId).toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
  })

  it('opens normally when the path exists (stat resolves)', async () => {
    statMock.mockResolvedValue({ size: 12, mtime: 0, isDirectory: false, isFile: true })
    const tabId = await openInAppFile('/buffer/real.md', 'w1')
    expect(tabId).toBeTruthy()
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
    expect(statMock).toHaveBeenCalledWith('/buffer/real.md')
  })

  // --- R2-2: null workspace must REFUSE (no insert, no guess) ---

  it('refuses (opens nothing, no stat) when workspaceId is null', async () => {
    const tabId = await openInAppFile('/buffer/x.md', null)
    expect(tabId).toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
    // Refused before touching the backend.
    expect(statMock).not.toHaveBeenCalled()
  })

  // --- T1c-3: binary open-disposition (non-previewable → download, no tab) ---

  it('T3-1: opening a .docx triggers a download and opens NO tab', async () => {
    readMock.mockResolvedValue(new Uint8Array([10, 20, 30]))
    const tabId = await openInAppFile('/buffer/report.docx', 'w1')
    // No tab opened — download is a side-effect, not a pane.
    expect(tabId).toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
    // Bytes read and handed to triggerDownload with the basename.
    expect(readMock).toHaveBeenCalledWith('/buffer/report.docx')
    expect(triggerDownload).toHaveBeenCalledTimes(1)
    const [blobArg, nameArg] = vi.mocked(triggerDownload).mock.calls[0]
    expect(blobArg).toBeInstanceOf(Blob)
    expect(nameArg).toBe('report.docx')
  })

  it('T3-2: opening a .png still mounts the image-preview pane (no download)', async () => {
    const tabId = await openInAppFile('/buffer/p.png', 'w1')
    expect(primaryContent(tabId!)?.kind).toBe('image-preview')
    expect(triggerDownload).not.toHaveBeenCalled()
  })

  it('T3-3: opening a .md still mounts the monaco editor (no download)', async () => {
    const tabId = await openInAppFile('/buffer/x.md', 'w1')
    expect(primaryContent(tabId!)?.kind).toBe('editor')
    expect(triggerDownload).not.toHaveBeenCalled()
  })

  it('T3-4: opening a .zip triggers a download and opens NO tab', async () => {
    const tabId = await openInAppFile('/buffer/bundle.zip', 'w1')
    expect(tabId).toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
    expect(triggerDownload).toHaveBeenCalledTimes(1)
    expect(vi.mocked(triggerDownload).mock.calls[0][1]).toBe('bundle.zip')
  })

  it('T3: a missing .docx (stat rejects) does NOT download (stat-gate still applies)', async () => {
    statMock.mockRejectedValue(new Error('not found'))
    const tabId = await openInAppFile('/buffer/ghost.docx', 'w1')
    expect(tabId).toBeUndefined()
    expect(triggerDownload).not.toHaveBeenCalled()
    expect(readMock).not.toHaveBeenCalled()
  })

  it('C2: a .docx deleted BETWEEN stat and read (read rejects) quietly refuses — no throw, no tab, no download', async () => {
    // TOCTOU: stat resolves (file existed), but the read fails (the entry was
    // removed, or the backend errored, in the gap). The download branch must
    // swallow it and return undefined, matching the stat-gate's silent abort —
    // never an unhandled rejection.
    statMock.mockResolvedValue({ size: 4, mtime: 0, isDirectory: false, isFile: true })
    readMock.mockRejectedValue(new Error('vanished'))
    await expect(openInAppFile('/buffer/gone.docx', 'w1')).resolves.toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
    expect(triggerDownload).not.toHaveBeenCalled()
  })
})
