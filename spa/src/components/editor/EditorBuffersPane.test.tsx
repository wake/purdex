import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { EditorBuffersPane } from './EditorBuffersPane'
import type { Pane, Tab } from '../../types/tab'
import type { FsBackend } from '../../lib/fs-backend'
import type { FileEntry, FileStat } from '../../types/fs'

// Mock fs-backend: list/write/delete/rename are per-test spies. `stat/mkdir/read`
// are stubbed with inert defaults. The helper below installs a fresh mock into
// the registry before each test.
type MockBackend = {
  list: ReturnType<typeof vi.fn>
  write: ReturnType<typeof vi.fn>
  delete: ReturnType<typeof vi.fn>
  rename: ReturnType<typeof vi.fn>
  stat: ReturnType<typeof vi.fn>
  read: ReturnType<typeof vi.fn>
  mkdir: ReturnType<typeof vi.fn>
  id: 'inapp'
  label: string
  available: () => boolean
}

let mockBackend: MockBackend
const eventLog: string[] = []

vi.mock('../../lib/fs-backend', () => ({
  // The pane consumes only `getFsBackend({type:'inapp'})`. The test sets
  // `mockBackend` in beforeEach.
  getFsBackend: () => mockBackend as unknown as FsBackend,
  registerFsBackend: vi.fn(),
}))

// Mock useI18nStore so `t(key)` just returns key (or interpolates `{{count}}`).
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: { t: (k: string, p?: Record<string, string | number>) => string }) => unknown) =>
    selector({
      t: (key: string, params?: Record<string, string | number>): string => {
        if (!params) return key
        return key.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params[k] ?? ''))
      },
    }),
}))

// useTabStore mock — spies + controllable state. `setPaneContent`, `setActiveTab`,
// `addTab`, and `closePane` all log an event so ordering assertions are
// straightforward.
const setPaneContentSpy = vi.fn()
const setActiveTabSpy = vi.fn()
const addTabSpy = vi.fn()
const closePaneSpy = vi.fn()

type TabStoreState = {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  setPaneContent: typeof setPaneContentSpy
  setActiveTab: typeof setActiveTabSpy
  addTab: typeof addTabSpy
  closePane: typeof closePaneSpy
}

let tabStoreState: TabStoreState

vi.mock('../../stores/useTabStore', () => ({
  useTabStore: {
    getState: () => tabStoreState,
  },
}))

// Mock RenamePopover so we can drive confirm/validate without dealing with
// DOMRect or click-outside.
vi.mock('../RenamePopover', () => ({
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

// Helper harness component re-exported from the mock (kept here for clarity).
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

function makeNonEditorTab(tabId: string, paneId: string): Tab {
  return {
    id: tabId,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: paneId, content: { kind: 'dashboard' } } },
  }
}

beforeEach(() => {
  eventLog.length = 0
  setPaneContentSpy.mockReset()
  setActiveTabSpy.mockReset()
  addTabSpy.mockReset()
  closePaneSpy.mockReset()

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

  tabStoreState = {
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    setPaneContent: setPaneContentSpy,
    setActiveTab: setActiveTabSpy,
    addTab: addTabSpy,
    closePane: closePaneSpy,
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

  // Default window.confirm → true so multi-delete proceeds.
  vi.stubGlobal('confirm', vi.fn(() => true))
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('EditorBuffersPane', () => {
  it('B2-1: shows empty state when list returns []', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    render(<EditorBuffersPane pane={makePane()} isActive />)
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
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    expect(rows.map((r) => r.getAttribute('data-name'))).toEqual(['a.md', 'b.md', 'c.md'])
  })

  it('B2-3: delete (single) calls backend.delete with the right path', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row) // select
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/foo.md')
    })
  })

  it('B2-4: New calls backend.write with Untitled-<timestamp>.md and refreshes', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    render(<EditorBuffersPane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    fireEvent.click(screen.getByTestId('toolbar-new'))
    await waitFor(() => {
      expect(mockBackend.write).toHaveBeenCalledTimes(1)
    })
    const [path, content] = mockBackend.write.mock.calls[0]
    expect(path).toMatch(/^\/buffer\/Untitled-\d+\.md$/)
    expect(content).toBeInstanceOf(Uint8Array)
    expect(content.byteLength).toBe(0)
    // Refresh: list called a second time once the refreshKey bump flushes.
    await waitFor(() => {
      expect(mockBackend.list).toHaveBeenCalledTimes(2)
    })
  })

  it('B2-5: rename rejects slash via validateName; backend.rename NOT called', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'drafts/foo.md' } })
    // Validator fires synchronously; error visible + confirm disabled.
    expect(screen.getByTestId('rename-error').textContent).toBe('editor.buffers.rename_slash_error')
    expect((screen.getByTestId('rename-confirm') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByTestId('rename-confirm'))
    // Backend not called.
    expect(mockBackend.rename).not.toHaveBeenCalled()
  })

  it('B2-6: flat rename calls backend.rename with old and new paths', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    // F4 (v1.4) pre-check: rename stat-probes the destination; reject
    // for unknown paths so the handler proceeds.
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    render(<EditorBuffersPane pane={makePane()} isActive />)
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

  it('B2-7: smart-open targets the active tab editor pane first', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makeEditorTab('TA', 'P1', '/buffer/other.md'),
      TB: makeEditorTab('TB', 'P2', '/buffer/another.md'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-open'))
    await waitFor(() => {
      expect(setPaneContentSpy).toHaveBeenCalledWith('TA', 'P1', {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: '/buffer/target.md',
      })
    })
    expect(setActiveTabSpy).toHaveBeenCalledWith('TA')
    expect(addTabSpy).not.toHaveBeenCalled()
  })

  it('B2-8: smart-open falls back to another tab with editor pane when active tab has none', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makeNonEditorTab('TA', 'PA'),
      TB: makeEditorTab('TB', 'P2', '/buffer/existing.md'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-open'))
    await waitFor(() => {
      expect(setPaneContentSpy).toHaveBeenCalledWith('TB', 'P2', {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: '/buffer/target.md',
      })
    })
    expect(setActiveTabSpy).toHaveBeenCalledWith('TB')
    expect(addTabSpy).not.toHaveBeenCalled()
  })

  it('B2-9: smart-open falls back to a new tab when no editor panes exist anywhere', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = { TA: makeNonEditorTab('TA', 'PA') }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-open'))
    await waitFor(() => {
      expect(addTabSpy).toHaveBeenCalledTimes(1)
    })
    const [newTab] = addTabSpy.mock.calls[0]
    expect(newTab.layout.type).toBe('leaf')
    expect(newTab.layout.pane.content).toEqual({
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: '/buffer/target.md',
    })
    expect(setActiveTabSpy).toHaveBeenCalledWith(newTab.id)
    expect(setPaneContentSpy).not.toHaveBeenCalled()
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
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/x.md')
    })
    // Every `close:*` event must precede the `delete:*` event.
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
    // Under v1.4 §4.9.5 the delete pre-check refuses if any affected pane
    // lives in a locked tab. This replaces v1.3's closePane-no-op flow
    // (which allowed backend.delete to fire, silently resurrected on
    // save). B2-13 covers the same refusal with a multi-select scenario;
    // B2-11 stays as the single-target smoke test.
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = { TA: makeEditorTab('TA', 'P1', '/buffer/x.md', true) }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    // The refusal surfaces via the inline error banner; backend.delete
    // and closePane are both untouched.
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
    // Stat resolves for the destination → it exists → rename must abort
    // before any backend mutation, surfacing the inline error key.
    mockBackend.stat.mockImplementation(async (path: string) => {
      if (path === '/buffer/bar.md') {
        return { size: 10, mtime: 0, isDirectory: false, isFile: true } as FileStat
      }
      throw new Error('not found')
    })
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    // Select foo.md.
    fireEvent.click(rows[1]) // sorted alphabetically: bar.md, foo.md
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'bar.md' } })
    // Slash validator doesn't fire for flat name; the stat pre-check
    // surfaces the existence error after confirm.
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
    // y.md lives in a locked tab; x.md's tab is unlocked. The pre-check
    // must refuse the whole operation — no partial delete.
    tabStoreState.tabs = {
      TA: makeEditorTab('TA', 'P1', '/buffer/x.md', false),
      TB: makeEditorTab('TB', 'P2', '/buffer/y.md', true),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<EditorBuffersPane pane={makePane()} isActive />)
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
    // Two sub-cases share one `it()` to keep the test count aligned with
    // spec v1.4 (+7 new tests). Each branch re-renders with a fresh pane
    // so they don't cross-pollute.

    // Sub-case (a): DIRTY buffer → dirty-specific message → cancel → no-op.
    {
      mockBackend.list.mockResolvedValueOnce([
        { name: 'z.md', isDir: false, size: 10 },
      ] as FileEntry[])
      tabStoreState.tabs = {
        TA: makeEditorTab('TA', 'P1', '/buffer/z.md', false),
      }
      tabStoreState.tabOrder = ['TA']
      tabStoreState.activeTabId = 'TA'
      const editorModule = await import('../../stores/useEditorStore')
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
      const { unmount } = render(<EditorBuffersPane pane={makePane()} isActive />)
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
      mockBackend.list.mockResolvedValueOnce([
        { name: 'only.md', isDir: false, size: 10 },
      ] as FileEntry[])
      tabStoreState.tabs = {}
      tabStoreState.tabOrder = []
      tabStoreState.activeTabId = null
      const editorModule = await import('../../stores/useEditorStore')
      editorModule.useEditorStore.setState({ buffers: {}, paneStates: {} })
      const confirmTrue = vi.fn(() => true)
      vi.stubGlobal('confirm', confirmTrue)
      render(<EditorBuffersPane pane={makePane()} isActive />)
      const row = await screen.findByTestId('buffer-row')
      fireEvent.click(row)
      fireEvent.click(screen.getByTestId('toolbar-delete'))
      expect(confirmTrue).toHaveBeenCalledWith('editor.buffers.delete_one_confirm')
      await waitFor(() => {
        expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/only.md')
      })
    }
  })

  it('B2-15: smart-open skips dirty and non-inapp panes, falls through to a new tab (v1.4 F3)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'target.md', isDir: false, size: 10 },
    ] as FileEntry[])
    // Active tab: inapp editor pane but buffer is dirty → ineligible.
    // Other tab: daemon editor pane (non-inapp) → ineligible.
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
    // Seed editor store: inapp pane is dirty.
    const editorModule = await import('../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/dirty.md': {
          content: 'unsaved',
          savedContent: '',
          isDirty: true,
          lastStat: null,
          modelId: 'm2',
          language: 'markdown',
          languageSource: 'extension',
          eol: 'lf',
          encoding: 'utf8',
        },
      },
      paneStates: {},
    })
    render(<EditorBuffersPane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-open'))
    await waitFor(() => {
      expect(addTabSpy).toHaveBeenCalledTimes(1)
    })
    // Neither pane was targeted for overwrite.
    expect(setPaneContentSpy).not.toHaveBeenCalled()
  })

  it('A2-6: pane keeps rendering when the editor module is disabled', async () => {
    // Pane lifetime is bound to the tab, not to module registration; a
    // post-mount module-disable must not unmount the pane.
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    const { rerender } = render(<EditorBuffersPane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    // Simulate module disable via a direct store write (the pane has no
    // subscription that would hide itself — it should keep rendering).
    const mod = await import('../../stores/useModuleEnabledStore')
    mod.useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })
    rerender(<EditorBuffersPane pane={makePane()} isActive />)
    // Still mounted.
    expect(screen.getByText('editor.buffers.empty')).toBeTruthy()
  })
})
