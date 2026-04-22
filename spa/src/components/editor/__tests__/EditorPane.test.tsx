import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import type { Pane } from '../../../types/tab'
import type { FsBackend } from '../../../lib/fs-backend'

const getFsBackendMock = vi.hoisted(() => vi.fn())

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: getFsBackendMock,
}))

vi.mock('../MonacoWrapper', () => ({
  MonacoWrapper: () => <div data-testid="monaco-wrapper" />,
}))

vi.mock('../DiffView', () => ({
  DiffView: () => <div data-testid="diff-view" />,
}))

vi.mock('../EditorStatusBar', () => ({
  EditorStatusBar: () => <div data-testid="editor-status-bar" />,
}))

function createPane(filePath = '/notes/editor.md', paneId = 'pane-editor'): Pane {
  return {
    id: paneId,
    content: {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath,
    },
  }
}

function createBackend(): FsBackend & {
  read: ReturnType<typeof vi.fn>
  write: ReturnType<typeof vi.fn>
  stat: ReturnType<typeof vi.fn>
  rename: ReturnType<typeof vi.fn>
} {
  const read = vi.fn(async (_path: string) => new Uint8Array())
  const write = vi.fn(async (_path: string, _content: Uint8Array) => {})
  const stat = vi.fn(async (_path: string) => ({
    isFile: true,
    isDirectory: false,
    size: 0,
    mtime: 0,
  }))

  return {
    id: 'test-backend',
    label: 'Test Backend',
    available: vi.fn(() => true),
    read,
    write,
    stat,
    list: vi.fn(),
    mkdir: vi.fn(),
    delete: vi.fn(),
    rename: vi.fn(),
  } as FsBackend & {
    read: ReturnType<typeof vi.fn>
    write: ReturnType<typeof vi.fn>
    stat: ReturnType<typeof vi.fn>
    rename: ReturnType<typeof vi.fn>
  }
}

function getBufferKey(filePath: string): string {
  return `inapp:${filePath}`
}

function registerTabPane(pane: Pane, tabId = 'tab-1') {
  useTabStore.setState({
    tabs: {
      [tabId]: {
        id: tabId,
        pinned: false,
        locked: false,
        createdAt: 1,
        layout: { type: 'leaf', pane },
      },
    },
    tabOrder: [tabId],
    activeTabId: tabId,
    visitHistory: [],
  })
}

describe('EditorPane', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  })

  it('preserves pane-local editor state across unmount when the pane still exists in tabs', async () => {
    const pane = createPane('/notes/keep-state.md')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    const { unmount } = render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/keep-state.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')
      useEditorStore.getState().setShowDiff(pane.id, true)
    })

    unmount()

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/keep-state.md')]).toBeDefined()
    expect(useEditorStore.getState().paneStates[pane.id]).toMatchObject({
      editorMode: 'wysiwyg',
      showDiff: true,
    })
  })

  it('cleans up pane state when the pane is reused for non-editor content', async () => {
    const pane = createPane('/notes/reused.md')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    const { unmount } = render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/reused.md')]).toBeDefined()
    })

    useTabStore.setState({
      tabs: {
        'tab-1': {
          id: 'tab-1',
          pinned: false,
          locked: false,
          createdAt: 1,
          layout: { type: 'leaf', pane: { id: pane.id, content: { kind: 'dashboard' } } },
        },
      },
      tabOrder: ['tab-1'],
      activeTabId: 'tab-1',
      visitHistory: [],
    })

    unmount()

    expect(useEditorStore.getState().paneStates[pane.id]).toBeUndefined()
    expect(useEditorStore.getState().buffers[getBufferKey('/notes/reused.md')]).toBeUndefined()
  })

  it('re-reads a file when the same pane switches away and back', async () => {
    const backend = createBackend()
    backend.read
      .mockResolvedValueOnce(new TextEncoder().encode('file a v1'))
      .mockResolvedValueOnce(new TextEncoder().encode('file b v1'))
      .mockResolvedValueOnce(new TextEncoder().encode('file a v2'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    const paneA = createPane('/notes/a.md', 'pane-switch')
    const paneB = createPane('/notes/b.md', 'pane-switch')
    registerTabPane(paneA)

    const { rerender } = render(<EditorPane pane={paneA} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/a.md')]?.content).toBe('file a v1')
    })

    registerTabPane(paneB)
    rerender(<EditorPane pane={paneB} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/b.md')]?.content).toBe('file b v1')
    })

    registerTabPane(paneA)
    rerender(<EditorPane pane={paneA} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/a.md')]?.content).toBe('file a v2')
    })

    expect(backend.read).toHaveBeenNthCalledWith(1, '/notes/a.md')
    expect(backend.read).toHaveBeenNthCalledWith(2, '/notes/b.md')
    expect(backend.read).toHaveBeenNthCalledWith(3, '/notes/a.md')
  })

  it('loads the initial file content through FsBackend', async () => {
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    render(<EditorPane pane={createPane('/notes/loaded.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/loaded.md')]).toMatchObject({
        content: 'hello world',
        savedContent: 'hello world',
        isDirty: false,
        language: 'markdown',
        lastStat: { mtime: 123, size: 11 },
      })
    })

    expect(screen.getByText('loaded.md')).toBeInTheDocument()
    expect(backend.read).toHaveBeenCalledWith('/notes/loaded.md')
    expect(backend.stat).toHaveBeenCalledWith('/notes/loaded.md')
  })

  it('shows the full file path as breadcrumbs in the toolbar', async () => {
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    render(<EditorPane pane={createPane('/notes/project/loaded.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/project/loaded.md')]).toBeDefined()
    })

    expect(screen.getByText('notes')).toBeInTheDocument()
    expect(screen.getByText('project')).toBeInTheDocument()
    expect(screen.getByText('loaded.md')).toBeInTheDocument()
  })

  it('enters rename mode on double click and rejects invalid base names', async () => {
    const pane = createPane('/notes/rename.md')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/rename.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('rename.md'))
    fireEvent.change(screen.getByDisplayValue('rename.md'), { target: { value: '..' } })
    fireEvent.keyDown(screen.getByDisplayValue('..'), { key: 'Enter' })

    expect(screen.getByRole('alert')).toHaveTextContent('Invalid file name')
    expect(backend.rename).not.toHaveBeenCalled()
  })

  it('warns when the rename target already exists', async () => {
    const pane = createPane('/notes/rename.md')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 20,
        mtime: 456,
      })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/rename.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('rename.md'))
    fireEvent.change(screen.getByDisplayValue('rename.md'), { target: { value: 'taken.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('taken.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('File already exists')
    })

    expect(backend.rename).not.toHaveBeenCalled()
  })

  it('warns when the rename target is already open in memory', async () => {
    const pane = createPane('/notes/source.md')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockRejectedValueOnce(new Error('not found'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/taken.md'), 'draft', 'markdown')
    useEditorStore.getState().attachPane('pane-other', getBufferKey('/notes/taken.md'))

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/source.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('source.md'))
    fireEvent.change(screen.getByDisplayValue('source.md'), { target: { value: 'taken.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('taken.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('File already exists')
    })

    expect(backend.rename).not.toHaveBeenCalled()
  })

  it('renames all matching panes and preserves the shared buffer', async () => {
    const paneA = createPane('/notes/rename.md', 'pane-a')
    const paneB = createPane('/notes/rename.md', 'pane-b')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.rename.mockResolvedValue(undefined)
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockRejectedValueOnce(new Error('not found'))
    getFsBackendMock.mockReturnValue(backend)
    useTabStore.setState({
      tabs: {
        'tab-a': {
          id: 'tab-a',
          pinned: false,
          locked: false,
          createdAt: 1,
          layout: { type: 'leaf', pane: paneA },
        },
        'tab-b': {
          id: 'tab-b',
          pinned: false,
          locked: false,
          createdAt: 2,
          layout: { type: 'leaf', pane: paneB },
        },
      },
      tabOrder: ['tab-a', 'tab-b'],
      activeTabId: 'tab-a',
      visitHistory: [],
    })

    render(<EditorPane pane={paneA} isActive />)
    render(<EditorPane pane={paneB} isActive={false} />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/rename.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getAllByText('rename.md')[0])
    fireEvent.change(screen.getByDisplayValue('rename.md'), { target: { value: 'renamed.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('renamed.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(backend.rename).toHaveBeenCalledWith('/notes/rename.md', '/notes/renamed.md')
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/rename.md')]).toBeUndefined()
    expect(useEditorStore.getState().buffers[getBufferKey('/notes/renamed.md')]).toBeDefined()

    const tabPaths = Object.values(useTabStore.getState().tabs).map((tab) =>
      tab.layout.type === 'leaf' && tab.layout.pane.content.kind === 'editor'
        ? tab.layout.pane.content.filePath
        : null,
    )
    expect(tabPaths).toEqual(['/notes/renamed.md', '/notes/renamed.md'])
  })

  it('updates buffer language when a rename changes the file extension', async () => {
    const pane = createPane('/notes/example.ts')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('const a = 1'))
    backend.rename.mockResolvedValue(undefined)
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockRejectedValueOnce(new Error('not found'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/example.ts')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('example.ts'))
    fireEvent.change(screen.getByDisplayValue('example.ts'), { target: { value: 'example.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('example.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/example.md')]?.language).toBe('markdown')
    })
  })

  it('saves dirty content and marks the buffer clean on success', async () => {
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.write.mockResolvedValue(undefined)
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 15,
        mtime: 456,
      })
    getFsBackendMock.mockReturnValue(backend)

    render(<EditorPane pane={createPane('/notes/save.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/save.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/save.md'), 'changed content')
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/notes/save.md', new TextEncoder().encode('changed content'))
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/save.md')]).toMatchObject({
      content: 'changed content',
      savedContent: 'changed content',
      isDirty: false,
      lastStat: { mtime: 456, size: 15 },
    })
  })

  it('keeps the buffer dirty when save fails', async () => {
    const backend = createBackend()
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.write.mockRejectedValue(new Error('disk full'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    render(<EditorPane pane={createPane('/notes/save-fail.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/save-fail.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/save-fail.md'), 'changed content')
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(consoleError).toHaveBeenCalledWith('[editor] Save failed:', expect.any(Error))
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/save-fail.md')]).toMatchObject({
      content: 'changed content',
      savedContent: 'hello world',
      isDirty: true,
      lastStat: { mtime: 123, size: 11 },
    })

    consoleError.mockRestore()
  })

  it('cleans up the buffer on unmount', async () => {
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    const { unmount } = render(<EditorPane pane={createPane('/notes/unmount.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/unmount.md')]).toBeDefined()
    })

    unmount()

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/unmount.md')]).toBeUndefined()
  })

  it('reloads external changes when the tab becomes active and the buffer is clean', async () => {
    const backend = createBackend()
    backend.read
      .mockResolvedValueOnce(new TextEncoder().encode('hello world'))
      .mockResolvedValueOnce(new TextEncoder().encode('reloaded from disk'))
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 18,
        mtime: 789,
      })
    getFsBackendMock.mockReturnValue(backend)

    const { rerender } = render(<EditorPane pane={createPane('/notes/reload.md')} isActive={false} />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/reload.md')]).toMatchObject({
        content: 'hello world',
        savedContent: 'hello world',
        isDirty: false,
      })
    })

    rerender(<EditorPane pane={createPane('/notes/reload.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/reload.md')]).toMatchObject({
        content: 'reloaded from disk',
        savedContent: 'reloaded from disk',
        isDirty: false,
        lastStat: { mtime: 789, size: 18 },
      })
    })
  })

  it('does not overwrite dirty content during active reload', async () => {
    const backend = createBackend()
    backend.read
      .mockResolvedValueOnce(new TextEncoder().encode('hello world'))
      .mockResolvedValueOnce(new TextEncoder().encode('reloaded from disk'))
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 18,
        mtime: 789,
      })
    getFsBackendMock.mockReturnValue(backend)

    const { rerender } = render(<EditorPane pane={createPane('/notes/dirty-reload.md')} isActive={false} />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty-reload.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/dirty-reload.md'), 'local draft')
    })

    rerender(<EditorPane pane={createPane('/notes/dirty-reload.md')} isActive />)

    await waitFor(() => {
      expect(backend.stat).toHaveBeenCalledTimes(2)
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty-reload.md')]).toMatchObject({
      content: 'local draft',
      savedContent: 'hello world',
      isDirty: true,
      lastStat: { mtime: 123, size: 11 },
    })
  })
})
