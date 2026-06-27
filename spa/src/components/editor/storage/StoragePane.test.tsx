import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Mock } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StoragePane } from './StoragePane'
import { openInAppFile } from '../../../lib/open-in-app-file'
import type { Pane, Tab } from '../../../types/tab'
import type { FsBackend } from '../../../lib/fs-backend'
import type { FileEntry, FileStat, FileSource } from '../../../types/fs'

// Mock fs-backend: list/write/delete/rename/read are per-test spies. The helper
// below installs a fresh mock into the registry before each test.
type MockBackend = {
  list: Mock
  write: Mock
  delete: Mock
  rename: Mock
  stat: Mock
  read: Mock
  mkdir: Mock
  id: 'inapp'
  label: string
  available: () => boolean
}

let mockBackend: MockBackend
const eventLog: string[] = []

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: () => mockBackend as unknown as FsBackend,
  registerFsBackend: vi.fn(),
}))

// Open routing is the StoragePane's collaboration boundary: the pane resolves
// the workspace id and hands (path, wsId) to `openInAppFile`. The registry
// kind-dispatch (md→editor / png→image-preview / pdf→pdf-preview) +
// open-or-focus + insertTab placement are exercised in
// `lib/open-in-app-file.test.ts`; here we only assert the pane routes through it.
vi.mock('../../../lib/open-in-app-file', () => ({
  openInAppFile: vi.fn(() => 'opened-tab'),
}))

// Workspace store: StoragePane.resolveWorkspaceId falls back to the active
// workspace when the pane isn't found in any tab layout (the harness case).
vi.mock('../../../features/workspace/store', () => ({
  useWorkspaceStore: {
    getState: () => ({ activeWorkspaceId: 'ws1', findWorkspaceByTab: () => null }),
  },
}))

vi.mock('../../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: { t: (k: string, p?: Record<string, string | number>) => string }) => unknown) =>
    selector({
      t: (key: string, params?: Record<string, string | number>): string => {
        if (!params) return key
        return key.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params[k] ?? ''))
      },
    }),
}))

const setPaneContentSpy = vi.fn()
const setActiveTabSpy = vi.fn()
const addTabSpy = vi.fn()
const closePaneSpy = vi.fn()
const renameEditorPanesSpy = vi.fn()

type TabStoreState = {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  setPaneContent: typeof setPaneContentSpy
  setActiveTab: typeof setActiveTabSpy
  addTab: typeof addTabSpy
  closePane: typeof closePaneSpy
  renameEditorPanes: typeof renameEditorPanesSpy
}

let tabStoreState: TabStoreState

vi.mock('../../../stores/useTabStore', () => ({
  useTabStore: {
    getState: () => tabStoreState,
  },
}))

vi.mock('../../RenamePopover', () => ({
  RenamePopover: ({
    currentName,
    onConfirm,
    onCancel,
    validateName,
    error,
  }: {
    currentName: string
    onConfirm: (name: string) => Promise<void>
    onCancel: () => void
    validateName?: (name: string, cur: string) => string | undefined
    error?: string
  }) => (
    <RenameHarness
      currentName={currentName}
      onConfirm={onConfirm}
      onCancel={onCancel}
      validateName={validateName}
      externalError={error}
    />
  ),
}))

function RenameHarness({
  currentName,
  onConfirm,
  onCancel,
  validateName,
  externalError,
}: {
  currentName: string
  onConfirm: (name: string) => Promise<void>
  onCancel: () => void
  validateName?: (name: string, cur: string) => string | undefined
  externalError?: string
}) {
  const [value, setValue] = useState(currentName)
  const validationError = validateName?.(value.trim(), currentName)
  const error = validationError ?? externalError
  return (
    <div data-testid="rename-popover-harness">
      <input
        data-testid="rename-input"
        value={value}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setValue(e.target.value)}
      />
      {error && <p data-testid="rename-error">{error}</p>}
      <button data-testid="rename-confirm" disabled={!!validationError} onClick={() => onConfirm(value.trim())}>
        confirm
      </button>
      <button data-testid="rename-cancel" onClick={onCancel}>
        cancel
      </button>
    </div>
  )
}

function makePane(): Pane {
  return { id: 'bufpane', content: { kind: 'editor-buffers' } }
}

function makeEditorTab(tabId: string, paneId: string, filePath: string, locked = false): Tab {
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

function makePreviewTab(
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

function makeNonEditorTab(tabId: string, paneId: string): Tab {
  return {
    id: tabId,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: paneId, content: { kind: 'dashboard' } } },
  }
}

/** Build a path-aware `list` from a flat fixture map (mirrors useStorageTree.test). */
function pathAwareList(paths: Map<string, { isDir: boolean; size: number }>): Mock {
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

beforeEach(() => {
  eventLog.length = 0
  setPaneContentSpy.mockReset()
  setActiveTabSpy.mockReset()
  addTabSpy.mockReset()
  closePaneSpy.mockReset()
  renameEditorPanesSpy.mockReset()
  ;(openInAppFile as unknown as Mock).mockReset()
  ;(openInAppFile as unknown as Mock).mockReturnValue('opened-tab')

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
  renameEditorPanesSpy.mockImplementation(
    (source: FileSource, oldPath: string, newPath: string) => {
      eventLog.push(`renameEditorPanes:${oldPath}:${newPath}`)
      const nextTabs: Record<string, Tab> = {}
      for (const [tabId, tab] of Object.entries(tabStoreState.tabs)) {
        if (tab.layout.type === 'leaf') {
          const c = tab.layout.pane.content
          if (c.kind === 'editor' && c.source.type === source.type && c.filePath === oldPath) {
            nextTabs[tabId] = {
              ...tab,
              layout: {
                type: 'leaf',
                pane: { ...tab.layout.pane, content: { ...c, filePath: newPath } },
              },
            }
            continue
          }
        }
        nextTabs[tabId] = tab
      }
      tabStoreState.tabs = nextTabs
    },
  )

  tabStoreState = {
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    setPaneContent: setPaneContentSpy,
    setActiveTab: setActiveTabSpy,
    addTab: addTabSpy,
    closePane: closePaneSpy,
    renameEditorPanes: renameEditorPanesSpy,
  }

  mockBackend = {
    id: 'inapp',
    label: 'In-App Storage',
    available: () => true,
    list: vi.fn().mockResolvedValue([] as FileEntry[]),
    write: vi.fn().mockResolvedValue(undefined),
    delete: vi.fn().mockImplementation(async (path: string) => {
      eventLog.push(`delete:${path}`)
    }),
    rename: vi.fn().mockResolvedValue(undefined),
    stat: vi.fn().mockResolvedValue({ size: 0, mtime: 0, isDirectory: false, isFile: true } as FileStat),
    read: vi.fn().mockResolvedValue(new Uint8Array(0)),
    mkdir: vi.fn().mockResolvedValue(undefined),
  }

  vi.stubGlobal('confirm', vi.fn(() => true))
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('StoragePane', () => {
  it('B2-1: shows empty state when list returns []', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.empty')).toBeTruthy()
    })
  })

  it('B2-2: lists entries by name ascending', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'c.md', isDir: false, size: 10 },
      { name: 'a.md', isDir: false, size: 10 },
      { name: 'b.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    expect(rows.map((r) => r.getAttribute('data-name'))).toEqual(['a.md', 'b.md', 'c.md'])
  })

  it('B2-3: delete (single) calls backend.delete with the right path', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row) // select
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/foo.md')
    })
  })

  it('B2-4: New calls backend.write with Untitled-<timestamp>.md and refreshes', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    fireEvent.click(screen.getByTestId('toolbar-new'))
    await waitFor(() => {
      expect(mockBackend.write).toHaveBeenCalledTimes(1)
    })
    const [path, content] = mockBackend.write.mock.calls[0]
    expect(path).toMatch(/^\/buffer\/Untitled-\d+\.md$/)
    expect(content).toBeInstanceOf(Uint8Array)
    expect(content.byteLength).toBe(0)
    // Refresh: list called a second time once the hook re-reads.
    await waitFor(() => {
      expect(mockBackend.list).toHaveBeenCalledTimes(2)
    })
  })

  it('B2-5: rename rejects slash via validateName; backend.rename NOT called', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'drafts/foo.md' } })
    expect(screen.getByTestId('rename-error').textContent).toBe('editor.buffers.rename_slash_error')
    expect((screen.getByTestId('rename-confirm') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByTestId('rename-confirm'))
    expect(mockBackend.rename).not.toHaveBeenCalled()
  })

  it('B2-6: flat rename calls backend.rename with old and new paths', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'bar.md' } })
    expect(screen.queryByTestId('rename-error')).toBeNull()
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/foo.md', '/buffer/bar.md')
    })
  })

  // --- Open via openInAppFile (rewritten smart-open tests B2-7/8/9/15/18) ---

  it('B2-7: toolbar Open routes a .md file through openInAppFile (no hijack)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    // Editor tabs exist — the old smart-open would have hijacked one of them.
    tabStoreState.tabs = {
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
    tabStoreState.tabs = { TA: makeNonEditorTab('TA', 'PA') }
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

  it('B2-10: delete closes each open editor pane BEFORE backend.delete', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makeEditorTab('TA', 'P1', '/buffer/x.md'),
      TB: makeEditorTab('TB', 'P2', '/buffer/x.md'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/x.md')
    })
    const firstDeleteIdx = eventLog.findIndex((e) => e.startsWith('delete:'))
    const closeIdxs = eventLog
      .map((e, i) => (e.startsWith('close:') ? i : -1))
      .filter((i) => i !== -1)
    expect(closeIdxs.length).toBe(2)
    for (const i of closeIdxs) {
      expect(i).toBeLessThan(firstDeleteIdx)
    }
    expect(closePaneSpy).toHaveBeenCalledWith('TA', 'P1')
    expect(closePaneSpy).toHaveBeenCalledWith('TB', 'P2')
  })

  it('B2-11: delete with locked tab is refused outright (v1.4 F2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = { TA: makeEditorTab('TA', 'P1', '/buffer/x.md', true) }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy()
    })
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
    expect(tabStoreState.tabs.TA).toBeDefined()
  })

  it('B2-11b: delete closes an open image-preview / pdf-preview pane before backend.delete', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'p.png', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makePreviewTab('TA', 'P1', '/buffer/p.png', 'image-preview'),
      TB: makePreviewTab('TB', 'P2', '/buffer/p.png', 'pdf-preview'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/p.png')
    })
    const firstDeleteIdx = eventLog.findIndex((e) => e.startsWith('delete:'))
    const closeIdxs = eventLog
      .map((e, i) => (e.startsWith('close:') ? i : -1))
      .filter((i) => i !== -1)
    expect(closeIdxs.length).toBe(2)
    for (const i of closeIdxs) {
      expect(i).toBeLessThan(firstDeleteIdx)
    }
    expect(closePaneSpy).toHaveBeenCalledWith('TA', 'P1')
    expect(closePaneSpy).toHaveBeenCalledWith('TB', 'P2')
  })

  it('B2-11c: delete refused when a preview pane sits in a locked tab (v1.4 F2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'p.png', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = { TA: makePreviewTab('TA', 'P1', '/buffer/p.png', 'image-preview', true) }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy()
    })
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
    expect(tabStoreState.tabs.TA).toBeDefined()
  })

  it('B2-12: rename aborts with inline error when destination exists (v1.4 F4)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
      { name: 'bar.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockImplementation(async (path: string) => {
      if (path === '/buffer/bar.md') {
        return { size: 10, mtime: 0, isDirectory: false, isFile: true } as FileStat
      }
      throw new Error('not found')
    })
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    fireEvent.click(rows[1]) // sorted: bar.md, foo.md → foo.md
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'bar.md' } })
    expect(screen.queryByTestId('rename-error')).toBeNull()
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(screen.getByTestId('rename-error').textContent).toBe(
        'editor.buffers.rename_exists_error',
      )
    })
    expect(mockBackend.rename).not.toHaveBeenCalled()
  })

  it('B2-13: multi-select delete refused when ANY target is in a locked tab (v1.4 F2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
      { name: 'y.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makeEditorTab('TA', 'P1', '/buffer/x.md', false),
      TB: makeEditorTab('TB', 'P2', '/buffer/y.md', true),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    fireEvent.click(rows[0]) // x.md
    fireEvent.click(rows[1]) // y.md
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy()
    })
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
  })

  it('B2-14: delete confirms with dirty-specific (cancel) and single-specific (OK) messages (v1.4 F5+F6)', async () => {
    // Sub-case (a): DIRTY buffer → dirty-specific message → cancel → no-op.
    {
      mockBackend.list.mockResolvedValue([
        { name: 'z.md', isDir: false, size: 10 },
      ] as FileEntry[])
      tabStoreState.tabs = {
        TA: makeEditorTab('TA', 'P1', '/buffer/z.md', false),
      }
      tabStoreState.tabOrder = ['TA']
      tabStoreState.activeTabId = 'TA'
      const editorModule = await import('../../../stores/useEditorStore')
      editorModule.useEditorStore.setState({
        buffers: {
          'inapp:/buffer/z.md': {
            content: 'dirty',
            savedContent: '',
            isDirty: true,
            lastStat: null,
            modelId: 'm1',
            language: 'markdown',
            languageSource: 'extension',
            eol: 'lf',
            encoding: 'utf8',
          },
        },
        paneStates: {},
      })
      const confirmFalse = vi.fn(() => false)
      vi.stubGlobal('confirm', confirmFalse)
      const { unmount } = render(<StoragePane pane={makePane()} isActive />)
      const row = await screen.findByTestId('buffer-row')
      fireEvent.click(row)
      fireEvent.click(screen.getByTestId('toolbar-delete'))
      expect(confirmFalse).toHaveBeenCalledWith('editor.buffers.delete_dirty_confirm')
      expect(mockBackend.delete).not.toHaveBeenCalled()
      expect(closePaneSpy).not.toHaveBeenCalled()
      unmount()
    }

    // Sub-case (b): CLEAN single-target → single-specific message → OK → delete fires.
    {
      mockBackend.list.mockResolvedValue([
        { name: 'only.md', isDir: false, size: 10 },
      ] as FileEntry[])
      tabStoreState.tabs = {}
      tabStoreState.tabOrder = []
      tabStoreState.activeTabId = null
      const editorModule = await import('../../../stores/useEditorStore')
      editorModule.useEditorStore.setState({ buffers: {}, paneStates: {} })
      const confirmTrue = vi.fn(() => true)
      vi.stubGlobal('confirm', confirmTrue)
      render(<StoragePane pane={makePane()} isActive />)
      const row = await screen.findByTestId('buffer-row')
      fireEvent.click(row)
      fireEvent.click(screen.getByTestId('toolbar-delete'))
      expect(confirmTrue).toHaveBeenCalledWith('editor.buffers.delete_one_confirm')
      await waitFor(() => {
        expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/only.md')
      })
    }
  })

  it('B2-16: rename syncs tab layout + editor store (v1.5 G1)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/foo.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = 'T1'
    const editorModule = await import('../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/foo.md': {
          content: 'hello',
          savedContent: '',
          isDirty: true,
          lastStat: null,
          modelId: 'm-foo',
          language: 'markdown',
          languageSource: 'extension',
          eol: 'lf',
          encoding: 'utf8',
        },
      },
      paneStates: {
        P1: {
          bufferKey: 'inapp:/buffer/foo.md',
          editorMode: 'raw',
          showDiff: false,
          cursorPosition: { line: 1, column: 1 },
          monacoViewState: null,
          tiptapViewState: null,
        },
      },
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'bar.md' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))

    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/foo.md', '/buffer/bar.md')
    })
    await waitFor(() => {
      const leaf = tabStoreState.tabs.T1.layout
      expect(leaf.type).toBe('leaf')
      if (leaf.type !== 'leaf') throw new Error('expected leaf')
      expect(leaf.pane.content).toMatchObject({
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: '/buffer/bar.md',
      })
    })
    const editorState = editorModule.useEditorStore.getState()
    expect(editorState.buffers['inapp:/buffer/bar.md']).toBeDefined()
    expect(editorState.buffers['inapp:/buffer/bar.md']?.isDirty).toBe(true)
    expect(editorState.buffers['inapp:/buffer/foo.md']).toBeUndefined()
    expect(editorState.paneStates.P1?.bufferKey).toBe('inapp:/buffer/bar.md')
    expect(renameEditorPanesSpy).toHaveBeenCalledWith(
      { type: 'inapp' },
      '/buffer/foo.md',
      '/buffer/bar.md',
    )
  })

  it('B2-16b: rename across extensions refreshes buffer metadata (v1.5 R6 MED)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/foo.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = 'T1'
    const editorModule = await import('../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/foo.md': {
          content: 'hello',
          savedContent: '',
          isDirty: false,
          lastStat: null,
          modelId: 'm-foo',
          language: 'markdown',
          languageSource: 'extension',
          eol: 'lf',
          encoding: 'utf8',
        },
      },
      paneStates: {
        P1: {
          bufferKey: 'inapp:/buffer/foo.md',
          editorMode: 'raw',
          showDiff: false,
          cursorPosition: { line: 1, column: 1 },
          monacoViewState: null,
          tiptapViewState: null,
        },
      },
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'foo.ts' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))

    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/foo.md', '/buffer/foo.ts')
    })
    await waitFor(() => {
      const next = editorModule.useEditorStore.getState().buffers['inapp:/buffer/foo.ts']
      expect(next).toBeDefined()
      expect(next?.language).toBe('typescript')
      expect(next?.languageSource).toBe('extension')
    })
  })

  it('B2-16c: rename preserves manual language override (v1.5 R6 MED)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/foo.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = 'T1'
    const editorModule = await import('../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/foo.md': {
          content: 'hello',
          savedContent: '',
          isDirty: false,
          lastStat: null,
          modelId: 'm-foo',
          language: 'rust',
          languageSource: 'manual',
          eol: 'lf',
          encoding: 'utf8',
        },
      },
      paneStates: {
        P1: {
          bufferKey: 'inapp:/buffer/foo.md',
          editorMode: 'raw',
          showDiff: false,
          cursorPosition: { line: 1, column: 1 },
          monacoViewState: null,
          tiptapViewState: null,
        },
      },
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'foo.ts' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))

    await waitFor(() => {
      const next = editorModule.useEditorStore.getState().buffers['inapp:/buffer/foo.ts']
      expect(next).toBeDefined()
      expect(next?.language).toBe('rust')
      expect(next?.languageSource).toBe('manual')
    })
  })

  it('B2-17: delete cleans editor store for background tabs (v1.5 G2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/x.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = null // background (not active)
    const editorModule = await import('../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/x.md': {
          content: 'unsaved',
          savedContent: '',
          isDirty: true,
          lastStat: null,
          modelId: 'm-x',
          language: 'markdown',
          languageSource: 'extension',
          eol: 'lf',
          encoding: 'utf8',
        },
      },
      paneStates: {
        P1: {
          bufferKey: 'inapp:/buffer/x.md',
          editorMode: 'raw',
          showDiff: false,
          cursorPosition: { line: 1, column: 1 },
          monacoViewState: null,
          tiptapViewState: null,
        },
      },
    })
    vi.stubGlobal('confirm', vi.fn(() => true))

    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))

    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/x.md')
    })
    const editorState = editorModule.useEditorStore.getState()
    expect(editorState.buffers['inapp:/buffer/x.md']).toBeUndefined()
    expect(editorState.paneStates.P1).toBeUndefined()
    expect(closePaneSpy).toHaveBeenCalledWith('T1', 'P1')
  })

  it('A2-6: pane keeps rendering when the editor module is disabled', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    const { rerender } = render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    const mod = await import('../../../stores/useModuleEnabledStore')
    mod.useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })
    rerender(<StoragePane pane={makePane()} isActive />)
    expect(screen.getByText('editor.buffers.empty')).toBeTruthy()
  })

  // --- Nested tree (Phase 1a full-path identity) ---

  it('N1: renders a nested file under expandable folders a → b', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/a', { isDir: true, size: 0 }],
        ['/buffer/a/b', { isDir: true, size: 0 }],
        ['/buffer/a/b/x.md', { isDir: false, size: 12 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    // Folder a is visible; its descendants are hidden until expanded.
    const folderA = await screen.findByTestId('buffer-row')
    expect(folderA.getAttribute('data-path')).toBe('/buffer/a')
    expect(folderA.getAttribute('data-isdir')).toBe('true')
    expect(screen.queryByText('x.md')).toBeNull()

    // Expand a → b appears; expand b → x.md appears.
    fireEvent.click(folderA)
    const folderB = await screen.findByText('b')
    fireEvent.click(folderB)
    const leaf = await screen.findByText('x.md')
    const leafRow = leaf.closest('[data-testid="buffer-row"]')
    expect(leafRow?.getAttribute('data-path')).toBe('/buffer/a/b/x.md')
    // Depth-2 indentation (8 + 2*16 = 40px).
    expect((leafRow as HTMLElement).style.paddingLeft).toBe('40px')
  })

  it('N2: same-basename files in different dirs are selected independently (full-path identity)', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/d1', { isDir: true, size: 0 }],
        ['/buffer/d1/x.md', { isDir: false, size: 3 }],
        ['/buffer/d2', { isDir: true, size: 0 }],
        ['/buffer/d2/x.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const d1 = await screen.findByText('d1')
    const d2 = await screen.findByText('d2')
    fireEvent.click(d1.closest('[data-testid="buffer-row"]')!)
    fireEvent.click(d2.closest('[data-testid="buffer-row"]')!)
    const leaves = await screen.findAllByText('x.md')
    const row1 = leaves[0].closest('[data-testid="buffer-row"]') as HTMLElement
    const row2 = leaves[1].closest('[data-testid="buffer-row"]') as HTMLElement
    expect(row1.getAttribute('data-path')).toBe('/buffer/d1/x.md')
    expect(row2.getAttribute('data-path')).toBe('/buffer/d2/x.md')
    // Select only the first; the second stays unselected.
    fireEvent.click(row1)
    expect(row1.getAttribute('aria-selected')).toBe('true')
    expect(row2.getAttribute('aria-selected')).toBe('false')
  })

  it('N3: rows render the correct extension/folder icon name', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/dir/note.md', { isDir: false, size: 5 }],
        ['/buffer/pic.png', { isDir: false, size: 9 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    const byPath = (p: string) => rows.find((r) => r.getAttribute('data-path') === p)!
    // Collapsed folder → Folder; png → FilePng.
    expect(byPath('/buffer/dir').getAttribute('data-icon')).toBe('Folder')
    expect(byPath('/buffer/pic.png').getAttribute('data-icon')).toBe('FilePng')
    // Expand the folder → it switches to FolderOpen, and the md row resolves.
    fireEvent.click(byPath('/buffer/dir'))
    await waitFor(() => {
      const updated = screen.getAllByTestId('buffer-row')
      const dir = updated.find((r) => r.getAttribute('data-path') === '/buffer/dir')!
      expect(dir.getAttribute('data-icon')).toBe('FolderOpen')
      const md = updated.find((r) => r.getAttribute('data-path') === '/buffer/dir/note.md')!
      expect(md.getAttribute('data-icon')).toBe('FileMd')
    })
  })

  it('N4: text rows show a word count; binary rows show size only', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'doc.md', isDir: false, size: 17 },
      { name: 'pic.png', isDir: false, size: 4096 },
    ] as FileEntry[])
    mockBackend.read.mockImplementation(async (path: string) => {
      if (path === '/buffer/doc.md') return new TextEncoder().encode('hello world foo')
      return new Uint8Array(0)
    })
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    const md = rows.find((r) => r.getAttribute('data-path') === '/buffer/doc.md')!
    const png = rows.find((r) => r.getAttribute('data-path') === '/buffer/pic.png')!
    await waitFor(() => {
      expect(md.textContent).toContain('3 words')
    })
    expect(md.textContent).toContain('17 B')
    // Binary: size only, never a word count, and read is not attempted.
    expect(png.textContent).toContain('4096 B')
    expect(png.textContent).not.toContain('words')
    expect(mockBackend.read).not.toHaveBeenCalledWith('/buffer/pic.png')
  })

  it('N5: rename of a nested leaf file targets its full path (same directory)', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/a', { isDir: true, size: 0 }],
        ['/buffer/a/b', { isDir: true, size: 0 }],
        ['/buffer/a/b/x.md', { isDir: false, size: 12 }],
      ]),
    )
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await screen.findByText('a').then((n) => n.closest('[data-testid="buffer-row"]')!))
    fireEvent.click((await screen.findByText('b')).closest('[data-testid="buffer-row"]')!)
    const leafRow = (await screen.findByText('x.md')).closest('[data-testid="buffer-row"]')!
    fireEvent.click(leafRow) // select
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    // currentName is the basename of the full path.
    expect((input as HTMLInputElement).value).toBe('x.md')
    fireEvent.change(input, { target: { value: 'y.md' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/a/b/x.md', '/buffer/a/b/y.md')
    })
  })

  it('N6: delete of a nested leaf file targets its full path', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/a', { isDir: true, size: 0 }],
        ['/buffer/a/b', { isDir: true, size: 0 }],
        ['/buffer/a/b/x.md', { isDir: false, size: 12 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click((await screen.findByText('a')).closest('[data-testid="buffer-row"]')!)
    fireEvent.click((await screen.findByText('b')).closest('[data-testid="buffer-row"]')!)
    const leafRow = (await screen.findByText('x.md')).closest('[data-testid="buffer-row"]')!
    fireEvent.click(leafRow)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/a/b/x.md')
    })
  })
})
