import { describe, it, expect, beforeEach, vi } from 'vitest'
import { openInAppFile } from './open-in-app-file'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import {
  clearAllForHmr,
  registerFileOpener,
} from './file-opener-registry'
import { registerFsBackend, clearFsBackendRegistry } from './fs-backend'
import { editorModuleDefinition } from './register-modules/editor-module'
import { getPrimaryPane } from './pane-tree'
import type { PaneContent } from '../types/tab'
import type { FsBackend } from './fs-backend'
import type { FileStat } from '../types/fs'

// stat-gate (R2-1): openInAppFile stats the path before opening. The spy
// resolves (file exists) by default; individual tests make it reject to
// simulate a missing/stale entry.
const statMock = vi.fn<(path: string) => Promise<FileStat>>()

function registerStatBackend(): void {
  clearFsBackendRegistry()
  statMock.mockReset()
  statMock.mockResolvedValue({ size: 0, mtime: 0, isDirectory: false, isFile: true })
  registerFsBackend('inapp', {
    id: 'inapp',
    label: 'In-App',
    available: () => true,
    stat: statMock,
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
  })

  it('opens a .md file as an editor pane (registry dispatch)', async () => {
    const tabId = await openInAppFile('/buffer/x.md', 'w1')
    expect(tabId).toBeTruthy()
    const content = primaryContent(tabId!)
    expect(content?.kind).toBe('editor')
    expect((content as { filePath: string }).filePath).toBe('/buffer/x.md')
    expect((content as { source: { type: string } }).source.type).toBe('inapp')
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
})
