// EditorPane — getting a buffer into the store: the load effect, the retryable
// failure surface (T1.2 / T1.2b), external-change reload and pane teardown.
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createBackend,
  createPane,
  createUntitledPane,
  getBufferKey,
  registerTabPane,
  renderEditorPane,
  resetEditorPaneStores,
} from './editor-pane-harness'
import { getFsBackendMock } from './editor-pane-mocks'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'

vi.mock('../../../lib/fs-backend', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/fs-backend')>()
  const mocks = await import('./editor-pane-mocks')
  mocks.fsBackendActual.current = actual
  return { ...actual, getFsBackend: mocks.getFsBackendMock }
})
vi.mock('../MonacoWrapper', async () => ({
  MonacoWrapper: (await import('./editor-pane-mocks')).MonacoWrapperStub,
}))
vi.mock('../DiffView', async () => ({
  DiffView: (await import('./editor-pane-mocks')).DiffViewStub,
}))
vi.mock('../EditorStatusBar', async () => ({
  EditorStatusBar: (await import('./editor-pane-mocks')).EditorStatusBarStub,
}))
vi.mock('../TiptapEditor', async () => ({
  TiptapEditor: (await import('./editor-pane-mocks')).TiptapEditorStub,
}))

describe('EditorPane', () => {
  beforeEach(resetEditorPaneStores)

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

    const { unmount } = renderEditorPane(pane)

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

    const { unmount } = renderEditorPane(pane)

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

    const { rerender } = renderEditorPane(paneA)

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

    renderEditorPane(createPane('/notes/loaded.md'))

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

    renderEditorPane(createPane('/notes/project/loaded.md'))

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/project/loaded.md')]).toBeDefined()
    })

    expect(screen.getByText('notes')).toBeInTheDocument()
    expect(screen.getByText('project')).toBeInTheDocument()
    expect(screen.getByText('loaded.md')).toBeInTheDocument()
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

    const { unmount } = renderEditorPane(createPane('/notes/unmount.md'))

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

    const { rerender } = renderEditorPane(createPane('/notes/reload.md'), false)

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

    const { rerender } = renderEditorPane(createPane('/notes/dirty-reload.md'), false)

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

describe('EditorPane — load failure (T1.2)', () => {
  beforeEach(resetEditorPaneStores)

  it('renders a load error and creates no buffer when the read fails', async () => {
    const pane = createPane('/notes/read-fail.md', 'pane-read-fail')
    const backend = createBackend()
    backend.read.mockRejectedValue(new Error('network unreachable'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(screen.getByTestId('editor-load-error')).toBeInTheDocument()
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/read-fail.md')]).toBeUndefined()
    expect(screen.getByTestId('editor-load-error')).toHaveTextContent('network unreachable')
    expect(screen.queryByTestId('monaco-wrapper')).not.toBeInTheDocument()
  })

  it('renders a load error and creates no buffer when the stat fails after a successful read', async () => {
    const pane = createPane('/notes/stat-fail.md', 'pane-stat-fail')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('real remote content'))
    backend.stat.mockRejectedValue(new Error('stat refused'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(screen.getByTestId('editor-load-error')).toBeInTheDocument()
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/stat-fail.md')]).toBeUndefined()
    expect(screen.getByTestId('editor-load-error')).toHaveTextContent('stat refused')
  })

  it('retries the load and opens the buffer normally once the read succeeds', async () => {
    const pane = createPane('/notes/retry.txt', 'pane-retry')
    const backend = createBackend()
    backend.read
      .mockRejectedValueOnce(new Error('temporarily unreachable'))
      .mockResolvedValue(new TextEncoder().encode('recovered content'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 17, mtime: 99 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(screen.getByTestId('editor-load-error')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('editor-load-error-retry'))

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/retry.txt')]).toMatchObject({
        content: 'recovered content',
        lastStat: { mtime: 99, size: 17 },
      })
    })

    expect(backend.read).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('editor-load-error')).not.toBeInTheDocument()
    expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
  })

  it('still opens an empty buffer for an untitled pane without reading the backend', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-untitled-load')
    const backend = createBackend()
    backend.read.mockRejectedValue(new Error('untitled panes must not read'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toMatchObject({
        content: '',
      })
    })

    expect(backend.read).not.toHaveBeenCalled()
    expect(screen.queryByTestId('editor-load-error')).not.toBeInTheDocument()
  })

  it('ignores a stale load failure after the pane switched to another file', async () => {
    const backend = createBackend()
    let rejectFirstRead: ((error: Error) => void) | undefined
    backend.read.mockImplementation((path: string) => {
      if (path === '/notes/stale-fail.md') {
        return new Promise<Uint8Array>((_resolve, reject) => {
          rejectFirstRead = reject
        })
      }
      return Promise.resolve(new TextEncoder().encode('second file'))
    })
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 11, mtime: 5 })
    getFsBackendMock.mockReturnValue(backend)

    const firstPane = createPane('/notes/stale-fail.md', 'pane-stale')
    const secondPane = createPane('/notes/stale-ok.txt', 'pane-stale')
    registerTabPane(firstPane)

    const { rerender } = renderEditorPane(firstPane)

    await waitFor(() => {
      expect(backend.read).toHaveBeenCalledWith('/notes/stale-fail.md')
    })

    registerTabPane(secondPane)
    rerender(<EditorPane pane={secondPane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/stale-ok.txt')]).toBeDefined()
    })

    await act(async () => {
      rejectFirstRead?.(new Error('too late'))
      await Promise.resolve()
    })

    expect(screen.queryByTestId('editor-load-error')).not.toBeInTheDocument()
    expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
    expect(useEditorStore.getState().buffers[getBufferKey('/notes/stale-fail.md')]).toBeUndefined()
  })
})

describe('EditorPane — unavailable FS backend (T1.2b)', () => {
  beforeEach(resetEditorPaneStores)

  it('renders the load error instead of a permanent spinner when no backend resolves', async () => {
    const pane = createPane('/notes/no-backend.md', 'pane-no-backend')
    getFsBackendMock.mockReturnValue(undefined)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(screen.getByTestId('editor-load-error')).toBeInTheDocument()
    })

    expect(screen.getByTestId('editor-load-error')).toHaveTextContent(
      'No FS backend is available for this file.',
    )
    expect(useEditorStore.getState().buffers[getBufferKey('/notes/no-backend.md')]).toBeUndefined()
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    expect(screen.queryByTestId('monaco-wrapper')).not.toBeInTheDocument()
  })

  it('retries and loads normally once a backend becomes available', async () => {
    const pane = createPane('/notes/backend-later.txt', 'pane-backend-later')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('now reachable'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 13, mtime: 3 })
    getFsBackendMock.mockReturnValue(undefined)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(screen.getByTestId('editor-load-error')).toBeInTheDocument()
    })

    getFsBackendMock.mockReturnValue(backend)
    fireEvent.click(screen.getByTestId('editor-load-error-retry'))

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/backend-later.txt')]).toMatchObject({
        content: 'now reachable',
        lastStat: { mtime: 3, size: 13 },
      })
    })

    expect(screen.queryByTestId('editor-load-error')).not.toBeInTheDocument()
    expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
  })

  it('leaves the untitled path unaffected when no backend resolves', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-untitled-no-backend')
    getFsBackendMock.mockReturnValue(undefined)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toMatchObject({
        content: '',
      })
    })

    expect(screen.queryByTestId('editor-load-error')).not.toBeInTheDocument()
  })
})
