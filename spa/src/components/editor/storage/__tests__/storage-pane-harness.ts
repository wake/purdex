// spa/src/components/editor/storage/__tests__/storage-pane-harness.ts
//
// Everything the StoragePane suites share that is NOT a `vi.mock` target: the
// tab / pane fixtures, the path-aware backend `list`, and the one `beforeEach`
// reset that puts the mocked stores back to a known state.
//
// The mock REGISTRATIONS stay in each test file (`vi.mock` is hoisted per file)
// and the spies/stubs they reach for live in `storage-pane-mocks.tsx`.
import { vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { triggerDownload } from '../../../../lib/download-file'
import {
  addTabSpy,
  closePaneSpy,
  eventLog,
  makeMockBackend,
  makeTabStoreState,
  renameEditorPanesSpy,
  resetDragEnd,
  setActiveTabSpy,
  setMockBackend,
  setPaneContentSpy,
  setTabStoreState,
  tSpy,
  applyRenameEditorPanes,
} from './storage-pane-mocks'
import type { Mock } from 'vitest'
import type { FileEntry } from '../../../../types/fs'
import type { Pane, Tab } from '../../../../types/tab'

// --- pane / tab fixtures ---------------------------------------------------

export function makePane(): Pane {
  return { id: 'bufpane', content: { kind: 'editor-buffers' } }
}

/**
 * The tab that hosts the Storage pane itself, so `resolveWorkspaceId` finds
 * `bufpane` in a tab and maps it (→ ws1) instead of returning null. Seeded into
 * the default tab store; tests that reassign `tabStoreState.tabs` spread this
 * back in.
 */
export function makeStorageTab(): Tab {
  return {
    id: 'storageTab',
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: 'bufpane', content: { kind: 'editor-buffers' } } },
  }
}

export function makeEditorTab(tabId: string, paneId: string, filePath: string, locked = false): Tab {
  return {
    id: tabId,
    pinned: false,
    locked,
    createdAt: Date.now(),
    layout: {
      type: 'leaf',
      pane: { id: paneId, content: { kind: 'editor', source: { type: 'inapp' }, filePath } },
    },
  }
}

export function makePreviewTab(
  tabId: string,
  paneId: string,
  filePath: string,
  kind: 'image-preview' | 'pdf-preview',
  locked = false,
): Tab {
  return {
    id: tabId,
    pinned: false,
    locked,
    createdAt: Date.now(),
    layout: {
      type: 'leaf',
      pane: { id: paneId, content: { kind, source: { type: 'inapp' }, filePath } },
    },
  }
}

export function makeNonEditorTab(tabId: string, paneId: string): Tab {
  return {
    id: tabId,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: paneId, content: { kind: 'dashboard' } } },
  }
}

/** Build a path-aware `list` from a flat fixture map (mirrors useStorageTree.test). */
export function pathAwareList(paths: Map<string, { isDir: boolean; size: number }>): Mock {
  return vi.fn(async (path: string): Promise<FileEntry[]> => {
    const prefix = path.endsWith('/') ? path : path + '/'
    const seen = new Map<string, FileEntry>()
    for (const [p, meta] of paths) {
      if (!p.startsWith(prefix)) continue
      const rest = p.slice(prefix.length)
      if (rest.includes('/')) continue
      if (!seen.has(rest)) seen.set(rest, { name: rest, isDir: meta.isDir, size: meta.size })
    }
    return Array.from(seen.values())
  })
}

// --- batch delete ----------------------------------------------------------

/**
 * Drive a batch delete end to end: press the trigger, then answer the
 * path-listing confirmation it opens. The selection is re-verified against the
 * backend before that dialog appears, so the click and the confirm are separated
 * by an await — every batch-delete assertion has to go through here rather than
 * a bare `click(toolbar-delete)`.
 *
 * The generic `window.confirm` is NOT part of this path (the caller passes
 * `preconfirmed`); the dirty-buffer confirm still is, and is answered by the
 * suite's own `confirm` stub as before.
 */
export async function confirmBatchDelete(triggerTestId = 'toolbar-delete'): Promise<void> {
  fireEvent.click(screen.getByTestId(triggerTestId))
  const dialog = await screen.findByTestId('delete-selection-dialog')
  fireEvent.click(within(dialog).getByTestId('delete-selection-confirm'))
}

// --- shared reset ----------------------------------------------------------

/**
 * The `beforeEach` every StoragePane suite runs: fresh spies, a fresh mocked
 * backend, a tab store holding only the Storage tab, and a `confirm` that says
 * yes (tests that need a refusal re-stub it themselves).
 */
export function resetStoragePaneMocks(): void {
  eventLog.length = 0
  resetDragEnd()
  setPaneContentSpy.mockReset()
  setActiveTabSpy.mockReset()
  addTabSpy.mockReset()
  closePaneSpy.mockReset()
  renameEditorPanesSpy.mockReset()
  ;(openInAppFile as unknown as Mock).mockReset()
  ;(openInAppFile as unknown as Mock).mockReturnValue('opened-tab')
  ;(triggerDownload as unknown as Mock).mockReset()
  tSpy.mockClear()

  setPaneContentSpy.mockImplementation((tabId: string, paneId: string) => {
    eventLog.push(`setPaneContent:${tabId}:${paneId}`)
  })
  setActiveTabSpy.mockImplementation((tabId: string) => {
    eventLog.push(`setActiveTab:${tabId}`)
  })
  addTabSpy.mockImplementation(() => {
    eventLog.push('addTab')
  })
  closePaneSpy.mockImplementation((tabId: string, paneId: string) => {
    eventLog.push(`close:${tabId}:${paneId}`)
  })
  renameEditorPanesSpy.mockImplementation(applyRenameEditorPanes)

  const tabStore = makeTabStoreState()
  tabStore.tabs = { storageTab: makeStorageTab() }
  tabStore.tabOrder = ['storageTab']
  tabStore.activeTabId = 'storageTab'
  setTabStoreState(tabStore)

  setMockBackend(makeMockBackend())

  vi.stubGlobal('confirm', vi.fn(() => true))
}

export function restoreStoragePaneGlobals(): void {
  vi.unstubAllGlobals()
}
