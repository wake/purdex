// The pane's collaboration boundary: it resolves a workspace id and hands
// (path, wsId) to `openInAppFile` — it never mutates or hijacks an editor tab
// itself. Download and the folder guards on Open/Download live here too.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { triggerDownload } from '../../../../lib/download-file'
import {
  mockBackend,
  setPaneContentSpy,
  setActiveTabSpy,
  addTabSpy,
  tabStoreState
} from './storage-pane-mocks'
import {
  makeEditorTab,
  makeNonEditorTab,
  makePane,
  makeStorageTab,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals
} from './storage-pane-harness'
import type { FileEntry } from '../../../../types/fs'

// `vi.mock` is hoisted per file, so every suite re-registers the same set; the
// factory bodies themselves live once in `./storage-pane-mocks`.
vi.mock('@dnd-kit/core', async () => (await import('./storage-pane-mocks')).dndKitMock())
vi.mock('../../../../lib/fs-backend', async () => (await import('./storage-pane-mocks')).fsBackendMock())
vi.mock('../../../../lib/open-in-app-file', async () => (await import('./storage-pane-mocks')).openInAppFileMock())
vi.mock('../../../../lib/download-file', async () => (await import('./storage-pane-mocks')).downloadFileMock())
vi.mock('../../../../features/workspace/store', async () => (await import('./storage-pane-mocks')).workspaceStoreMock())
vi.mock('../../../../stores/useI18nStore', async () => (await import('./storage-pane-mocks')).i18nStoreMock())
vi.mock('../../../../stores/useTabStore', async () => (await import('./storage-pane-mocks')).tabStoreMock())
vi.mock('../../../RenamePopover', async () => (await import('./storage-pane-mocks')).renamePopoverMock())

beforeEach(resetStoragePaneMocks)
afterEach(restoreStoragePaneGlobals)

describe('StoragePane — open routing, download and toolbar enablement', () => {
  // --- Open via openInAppFile (rewritten smart-open tests B2-7/8/9/15/18) ---

  it('B2-7: toolbar Open routes a .md file through openInAppFile (no hijack)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    // Editor tabs exist — the old smart-open would have hijacked one of them.
    tabStoreState.tabs = {
      storageTab: makeStorageTab(),
      TA: makeEditorTab('TA', 'P1', '/buffer/other.md'),
      TB: makeEditorTab('TB', 'P2', '/buffer/another.md'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row) // select
    fireEvent.click(screen.getByTestId('toolbar-open'))
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/target.md', 'ws1')
    })
    // No cross-tab hijack: existing editor panes untouched.
    expect(setPaneContentSpy).not.toHaveBeenCalled()
    expect(setActiveTabSpy).not.toHaveBeenCalled()
    expect(addTabSpy).not.toHaveBeenCalled()
  })

  it('B2-8: opening does not mutate or activate any existing editor tab', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      storageTab: makeStorageTab(),
      TA: makeNonEditorTab('TA', 'PA'),
      TB: makeEditorTab('TB', 'P2', '/buffer/existing.md'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.doubleClick(row)
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/target.md', 'ws1')
    })
    expect(setPaneContentSpy).not.toHaveBeenCalled()
    expect(setActiveTabSpy).not.toHaveBeenCalled()
    expect(addTabSpy).not.toHaveBeenCalled()
  })

  it('B2-9: double-clicking a .pdf routes through openInAppFile with its full path', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'report.pdf', isDir: false, size: 2048 },
    ] as FileEntry[])
    tabStoreState.tabs = { storageTab: makeStorageTab(), TA: makeNonEditorTab('TA', 'PA') }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.doubleClick(row)
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/report.pdf', 'ws1')
    })
    expect(addTabSpy).not.toHaveBeenCalled()
  })

  it('B2-15: opening a .png never reuses a dirty/non-inapp editor pane', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'pic.png', isDir: false, size: 4096 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      storageTab: makeStorageTab(),
      TA: makeEditorTab('TA', 'P1', '/buffer/dirty.md', false),
      TB: {
        id: 'TB',
        pinned: false,
        locked: false,
        createdAt: Date.now(),
        layout: {
          type: 'leaf',
          pane: {
            id: 'PD',
            content: {
              kind: 'editor',
              source: { type: 'daemon', hostId: 'remote' },
              filePath: '/home/user/file.md',
            },
          },
        },
      },
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.doubleClick(row)
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/pic.png', 'ws1')
    })
    expect(setPaneContentSpy).not.toHaveBeenCalled()
  })

  it('B2-18: double-click opens the row actually clicked (v1.5 G3)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'a.md', isDir: false, size: 10 },
      { name: 'b.md', isDir: false, size: 10 },
      { name: 'c.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    // Sorted alphabetically: a, b, c. Double-click b.md (index 1).
    fireEvent.doubleClick(rows[1])
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/b.md', 'ws1')
    })
    expect(openInAppFile).toHaveBeenCalledTimes(1)
  })

  it('A2-6: pane keeps rendering when the editor module is disabled', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    const { rerender } = render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    const mod = await import('../../../../stores/useModuleEnabledStore')
    mod.useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })
    rerender(<StoragePane pane={makePane()} isActive />)
    expect(screen.getByText('editor.buffers.empty')).toBeTruthy()
  })

  it('R2-2: passes a null workspace id straight through when the pane has no owning workspace', async () => {
    // bufpane is in no tab → resolveWorkspaceId returns null (no active guess).
    tabStoreState.tabs = {}
    tabStoreState.tabOrder = []
    tabStoreState.activeTabId = null
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 5 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.doubleClick(row)
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/x.md', null)
    })
  })

  it('R2-1: refreshes the tree when openInAppFile aborts (stale/missing entry)', async () => {
    ;(openInAppFile as unknown as Mock).mockResolvedValue(undefined)
    mockBackend.list.mockResolvedValue([
      { name: 'stale.md', isDir: false, size: 5 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    mockBackend.list.mockClear()
    fireEvent.doubleClick(row)
    await waitFor(() => {
      // refresh() bumps the tree nonce → re-lists the root.
      expect(mockBackend.list).toHaveBeenCalled()
    })
  })

  // --- Folder Open guard (codex B3) ---

  it('B3: selecting a folder disables the toolbar Open button (folders are not openable)', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/note.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    const rowByPath = (p: string) =>
      screen.getAllByTestId('buffer-row').find((r) => r.getAttribute('data-path') === p)!
    const openBtn = screen.getByTestId('toolbar-open') as HTMLButtonElement
    // Folder selected → Open disabled, and clicking it never routes to open.
    fireEvent.click(rowByPath('/buffer/dir'))
    expect(openBtn.disabled).toBe(true)
    fireEvent.click(openBtn)
    expect(openInAppFile).not.toHaveBeenCalled()
    // A file selection re-enables Open.
    fireEvent.click(rowByPath('/buffer/note.md'))
    expect(openBtn.disabled).toBe(false)
  })

  // --- Download / export a stored file (T1c-2) ---

  it('T2-2a: selecting a file enables Download and clicking dispatches downloadStorageFile', async () => {
    const bytes = new TextEncoder().encode('hello world')
    mockBackend.list.mockResolvedValue([
      { name: 'note.md', isDir: false, size: bytes.byteLength },
    ] as FileEntry[])
    mockBackend.read.mockResolvedValue(bytes)
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    const downloadBtn = screen.getByTestId('toolbar-download') as HTMLButtonElement
    // No selection → disabled.
    expect(downloadBtn.disabled).toBe(true)
    fireEvent.click(row) // select the file
    expect(downloadBtn.disabled).toBe(false)
    fireEvent.click(downloadBtn)
    await waitFor(() => {
      // downloadStorageFile read the file's bytes …
      expect(mockBackend.read).toHaveBeenCalledWith('/buffer/note.md')
    })
    await waitFor(() => {
      // … and handed them to the shared download util as a Blob named by basename.
      expect(triggerDownload).toHaveBeenCalledTimes(1)
    })
    const [blob, filename] = (triggerDownload as unknown as Mock).mock.calls[0]
    expect(blob).toBeInstanceOf(Blob)
    expect(filename).toBe('note.md')
  })

  it('T2-2b: selecting a folder disables Download and clicking never downloads', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/note.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    const rowByPath = (p: string) =>
      screen.getAllByTestId('buffer-row').find((r) => r.getAttribute('data-path') === p)!
    const downloadBtn = screen.getByTestId('toolbar-download') as HTMLButtonElement
    // Folder selected → Download disabled, and a click never reads / downloads.
    fireEvent.click(rowByPath('/buffer/dir'))
    expect(downloadBtn.disabled).toBe(true)
    fireEvent.click(downloadBtn)
    expect(triggerDownload).not.toHaveBeenCalled()
    expect(mockBackend.read).not.toHaveBeenCalledWith('/buffer/dir')
    // A file selection re-enables Download.
    fireEvent.click(rowByPath('/buffer/note.md'))
    expect(downloadBtn.disabled).toBe(false)
  })
})
