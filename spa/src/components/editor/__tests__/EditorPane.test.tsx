import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useEditorSettingsStore } from '../../../stores/useEditorSettingsStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import type { Pane } from '../../../types/tab'
import type { FsBackend } from '../../../lib/fs-backend'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { registerBuiltinFsBackends } from '../../../lib/register-modules/fs-backends'
import { useHostStore } from '../../../stores/useHostStore'
import type { PlatformCapabilities } from '../../../lib/platform'

const getFsBackendMock = vi.hoisted(() => vi.fn())
const editorStatusBarMock = vi.hoisted(() => vi.fn())
const tiptapPropsSpy = vi.hoisted(() => vi.fn())
const monacoPropsSpy = vi.hoisted(() => vi.fn())

// The real module is kept reachable (`fsBackendActual`) so the host-binding
// suite at the bottom can drive EditorPane through the REAL registry — a
// mocked getFsBackend can never prove which host a read lands on.
const fsBackendActual = vi.hoisted(() => ({
  current: null as typeof import('../../../lib/fs-backend') | null,
}))

vi.mock('../../../lib/fs-backend', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/fs-backend')>()
  fsBackendActual.current = actual
  return { ...actual, getFsBackend: getFsBackendMock }
})

vi.mock('../MonacoWrapper', () => ({
  MonacoWrapper: (props: { isActive?: boolean; initialViewState?: unknown }) => {
    monacoPropsSpy(props)
    return <div data-testid="monaco-wrapper" data-active={props.isActive ? 'true' : 'false'} />
  },
}))

vi.mock('../DiffView', () => ({
  DiffView: () => <div data-testid="diff-view" />,
}))

vi.mock('../EditorStatusBar', () => ({
  EditorStatusBar: (props: { language: string; eol: 'lf' | 'crlf'; encoding: 'utf8'; isMarkdown: boolean; editorMode: 'raw' | 'wysiwyg'; contentWidth?: 'narrow' | 'full'; onContentWidthChange?: (v: 'narrow' | 'full') => void }) => {
    editorStatusBarMock(props)
    return (
      <div
        data-testid="editor-status-bar"
        data-language={props.language}
        data-eol={props.eol}
        data-encoding={props.encoding}
        data-is-markdown={props.isMarkdown ? 'true' : 'false'}
        data-editor-mode={props.editorMode}
      />
    )
  },
}))

vi.mock('../TiptapEditor', () => ({
  TiptapEditor: (props: { initialViewState: unknown; onViewStateChange: (vs: unknown) => void }) => {
    tiptapPropsSpy(props)
    return (
      <button
        data-testid="tiptap-editor"
        onClick={() => props.onViewStateChange({ scrollTop: 42, selection: { type: 'text', from: 2, to: 3 } })}
      />
    )
  },
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

function createUntitledPane(name = 'Untitled', suggestedExtension: '.txt' | '.md' = '.md', paneId = 'pane-editor'): Pane {
  return {
    id: paneId,
    content: {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: `untitled:${name}`,
      untitled: {
        name,
        suggestedExtension,
        hasBeenRenamed: false,
      },
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
    createUnique: vi.fn(),
    mkdirUnique: vi.fn(),
  } as FsBackend & {
    read: ReturnType<typeof vi.fn>
    write: ReturnType<typeof vi.fn>
    stat: ReturnType<typeof vi.fn>
    rename: ReturnType<typeof vi.fn>
  }
}

function getBufferKey(filePath: string): string {
  return bufferKey({ type: 'inapp' }, filePath)
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
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    useRecentFilesStore.setState({ files: [] })
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

  it('uses rename popover UI instead of inline filename input', async () => {
    const pane = createPane('/notes/rename.md')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/rename.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('rename.md'))

    expect(screen.getByDisplayValue('rename.md')).toBeInTheDocument()
    expect(screen.queryByLabelText('Rename file')).not.toBeInTheDocument()
  })

  it('passes active state through to the editor content for refocus', async () => {
    const pane = createPane('/notes/focus.txt')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })

    const { rerender } = render(<EditorPane pane={pane} isActive={false} />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/focus.txt')]).toBeDefined()
    })

    rerender(<EditorPane pane={pane} isActive />)

    expect(screen.getByTestId('monaco-wrapper')).toHaveAttribute('data-active', 'true')
  })

  it('keeps non-markdown files in source mode even if the pane state was previously live mode', async () => {
    const pane = createPane('/notes/plain.txt', 'pane-txt')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/plain.txt'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/plain.txt')]).toBeDefined()
    })

    expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
    expect(screen.queryByTestId('tiptap-editor')).not.toBeInTheDocument()
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-is-markdown', 'false')
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'raw')
  })

  it('uses buffer metadata instead of the file extension to allow markdown live mode', async () => {
    const pane = createPane('/notes/plain.txt', 'pane-manual-markdown')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)

    useEditorStore.getState().openBuffer(getBufferKey('/notes/plain.txt'), '# hello', {
      language: 'markdown',
      languageSource: 'manual',
      eol: 'lf',
      encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/plain.txt'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(screen.getByTestId('tiptap-editor')).toBeInTheDocument()
    })

    expect(screen.queryByTestId('monaco-wrapper')).not.toBeInTheDocument()
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-language', 'markdown')
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-is-markdown', 'true')
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'wysiwyg')
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

    expect(screen.getByText('Invalid file name')).toBeInTheDocument()
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
      expect(screen.getByText('File already exists')).toBeInTheDocument()
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
    useEditorStore.getState().openBuffer(getBufferKey('/notes/taken.md'), 'draft', { language: 'markdown' })
    useEditorStore.getState().attachPane('pane-other', getBufferKey('/notes/taken.md'))

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/source.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('source.md'))
    fireEvent.change(screen.getByDisplayValue('source.md'), { target: { value: 'taken.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('taken.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(screen.getByText('File already exists')).toBeInTheDocument()
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

  it('renames an untitled buffer without calling backend rename', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-unsaved-rename')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('Untitled'))
    fireEvent.change(screen.getByDisplayValue('Untitled'), { target: { value: 'notes.txt' } })
    fireEvent.keyDown(screen.getByDisplayValue('notes.txt'), { key: 'Enter' })

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:notes.txt')]).toBeDefined()
    })

    expect(backend.rename).not.toHaveBeenCalled()
    expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeUndefined()
    expect(useEditorStore.getState().buffers[getBufferKey('untitled:notes.txt')]).toMatchObject({
      language: 'plaintext',
      languageSource: 'extension',
      lastStat: null,
      untitled: {
        name: 'notes.txt',
        suggestedExtension: '.md',
        hasBeenRenamed: true,
      },
    })
  })

  it('prompts for a file name before first save of an unrenamed untitled document', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-save-prompt')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    expect(screen.getByDisplayValue('Untitled.md')).toBeInTheDocument()
    expect(backend.write).not.toHaveBeenCalled()
  })

  it('saves an untitled document to in-app after confirming the suggested name', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-save-untitled')
    const backend = createBackend()
    backend.write.mockResolvedValue(undefined)
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 0,
      mtime: 456,
    })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))
    fireEvent.keyDown(screen.getByDisplayValue('Untitled.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/buffer/Untitled.md', new TextEncoder().encode(''))
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/buffer/Untitled.md')]).toMatchObject({
      lastStat: { mtime: 456, size: 0 },
      untitled: undefined,
    })
  })

  it('saves a renamed untitled document directly to in-app without prompting', async () => {
    const pane = createUntitledPane('notes.txt', '.txt', 'pane-save-renamed')
    const backend = createBackend()
    backend.write.mockResolvedValue(undefined)
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 5,
      mtime: 456,
    })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane({
      ...pane,
      content: {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'untitled:notes.txt',
        untitled: {
          name: 'notes.txt',
          suggestedExtension: '.txt',
          hasBeenRenamed: true,
        },
      },
    })
    useEditorStore.getState().openBuffer(getBufferKey('untitled:notes.txt'), 'hello', {
      language: 'plaintext',
      languageSource: 'extension',
      untitled: {
        name: 'notes.txt',
        suggestedExtension: '.txt',
        hasBeenRenamed: true,
      },
    })

    render(<EditorPane pane={{
      ...pane,
      content: {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'untitled:notes.txt',
        untitled: {
          name: 'notes.txt',
          suggestedExtension: '.txt',
          hasBeenRenamed: true,
        },
      },
    }} isActive />)

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/buffer/notes.txt', new TextEncoder().encode('hello'))
    })

    expect(screen.queryByDisplayValue('notes.txt')).not.toBeInTheDocument()
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

    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toContain('/notes/save.md')
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

  it('passes tiptapViewState into TiptapEditor and saves it back on change (AC9)', async () => {
    const pane = createPane('/notes/vs.md', 'pane-vs')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/vs.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/vs.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')
    useEditorStore.getState().saveTiptapViewState(pane.id, { scrollTop: 7, selection: { type: 'text', from: 1, to: 1 } })

    render(<EditorPane pane={pane} isActive />)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    // initialViewState 確實傳入
    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(
      expect.objectContaining({ initialViewState: { scrollTop: 7, selection: { type: 'text', from: 1, to: 1 } } }),
    )
    // onViewStateChange 回呼確實寫回 store
    fireEvent.click(screen.getByTestId('tiptap-editor'))
    expect(useEditorStore.getState().paneStates[pane.id].tiptapViewState).toEqual({ scrollTop: 42, selection: { type: 'text', from: 2, to: 3 } })
  })

  it('passes the store contentWidth into TiptapEditor (wysiwyg path)', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'full' })
    const pane = createPane('/notes/cw.md', 'pane-cw')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    render(<EditorPane pane={pane} isActive />)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(expect.objectContaining({ contentWidth: 'full' }))
  })

  it('passes contentWidth + onContentWidthChange into EditorStatusBar and the callback updates the store', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    const pane = createPane('/notes/cw2.md', 'pane-cw2')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw2.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw2.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    render(<EditorPane pane={pane} isActive />)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    expect(editorStatusBarMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ contentWidth: 'narrow', onContentWidthChange: expect.any(Function) }),
    )

    const onContentWidthChange = editorStatusBarMock.mock.calls.at(-1)?.[0].onContentWidthChange as (v: 'narrow' | 'full') => void
    act(() => onContentWidthChange('full'))
    expect(useEditorSettingsStore.getState().contentWidth).toBe('full')
  })

  it('withholds the width-toggle handler while DiffView is active, even entering diff from Live Mode (AC4)', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    const pane = createPane('/notes/cw-diff.md', 'pane-cw-diff')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw-diff.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw-diff.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    render(<EditorPane pane={pane} isActive />)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    // Live Mode exposes the handler → toggle visible.
    expect(editorStatusBarMock.mock.calls.at(-1)?.[0].onContentWidthChange).toEqual(expect.any(Function))

    // Enter diff: DiffView mounts and Tiptap unmounts, but editorMode stays
    // 'wysiwyg'. The handler must be withheld so EditorStatusBar hides the toggle.
    act(() => useEditorStore.getState().setShowDiff(pane.id, true))
    await waitFor(() => {
      expect(editorStatusBarMock.mock.calls.at(-1)?.[0].onContentWidthChange).toBeUndefined()
    })
  })

  it('keeps the store contentWidth after a raw ↔ live round trip (AC8)', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'full' })
    const pane = createPane('/notes/cw3.md', 'pane-cw3')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw3.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw3.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    render(<EditorPane pane={pane} isActive />)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(expect.objectContaining({ contentWidth: 'full' }))

    // Switch to raw and back to wysiwyg.
    act(() => useEditorStore.getState().setEditorMode(pane.id, 'raw'))
    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    tiptapPropsSpy.mockClear()
    act(() => useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg'))
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(expect.objectContaining({ contentWidth: 'full' }))
  })

  it('does not mount TiptapEditor against stale paneState even when lazy is cached (stale→raw derivation, supersedes R3 gating)', async () => {
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/g-a.md'), '# A', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })
    useEditorStore.getState().openBuffer(getBufferKey('/notes/g-b.md'), '# B', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })

    // 1) Warm the React.lazy cache so TiptapEditor mounts SYNCHRONOUSLY afterwards
    //    (this is exactly the condition R3 flagged: lazy cached → no Suspense gap).
    useEditorStore.getState().attachPane('pane-warm', getBufferKey('/notes/g-a.md'))
    useEditorStore.getState().setEditorMode('pane-warm', 'wysiwyg')
    const warm = render(<EditorPane pane={createPane('/notes/g-a.md', 'pane-warm')} isActive />)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    warm.unmount()

    // 2) Pane aligned to buffer A in wysiwyg, then FREEZE attachPane so paneState
    //    stays on A while we render the pane pointing at buffer B — deterministically
    //    reproducing the transient window (paneState.bufferKey=A, key=B) without
    //    racing the post-commit effect.
    useEditorStore.getState().attachPane('pane-gate', getBufferKey('/notes/g-a.md'))
    useEditorStore.getState().setEditorMode('pane-gate', 'wysiwyg')
    const spy = vi.spyOn(useEditorStore.getState(), 'attachPane').mockImplementation(() => {})
    try {
      tiptapPropsSpy.mockClear()
      render(<EditorPane pane={createPane('/notes/g-b.md', 'pane-gate')} isActive />)
      // lazy is cached now; WITHOUT the stale→raw derivation TiptapEditor would mount
      // synchronously against the stale paneState and lock didRestoreRef. Because the
      // stale paneState (bufferKey=A ≠ key=B) is treated as unaligned, editorMode
      // falls back to raw and the wysiwyg branch is never reached — raw Monaco mounts
      // instead, so Tiptap is never instantiated against a stale state.
      expect(tiptapPropsSpy).not.toHaveBeenCalled()
      expect(screen.queryByTestId('tiptap-editor')).toBeNull()
      expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
    } finally {
      spy.mockRestore()
    }
  })

  it('switching to a new markdown buffer while previous mode was wysiwyg renders raw Monaco, not a Loading editor flicker (#863)', async () => {
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/f-a.md'), '# A', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })
    useEditorStore.getState().openBuffer(getBufferKey('/notes/f-b.md'), '# B', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })

    // Pane aligned to buffer A in wysiwyg, carrying stale A-only state across every
    // paneState-derived field — proves NONE of it leaks onto buffer B's render:
    // monacoViewState (must not seed B's Monaco), showDiff (must not pre-mount
    // DiffView), cursorPosition (must not leak to the status bar).
    useEditorStore.getState().attachPane('pane-flicker', getBufferKey('/notes/f-a.md'))
    useEditorStore.getState().setEditorMode('pane-flicker', 'wysiwyg')
    useEditorStore.getState().saveMonacoViewState('pane-flicker', { stale: 'A' } as unknown as import('monaco-editor').editor.ICodeEditorViewState)
    useEditorStore.getState().setShowDiff('pane-flicker', true)
    useEditorStore.getState().updateCursor('pane-flicker', 5, 9)

    // Freeze attachPane so paneState stays stale on A (bufferKey=A) while we render
    // the pane now pointing at buffer B — deterministically reproducing the transient
    // window that #863 paints as a `Loading editor…` flicker.
    const spy = vi.spyOn(useEditorStore.getState(), 'attachPane').mockImplementation(() => {})
    try {
      tiptapPropsSpy.mockClear()
      monacoPropsSpy.mockClear()
      editorStatusBarMock.mockClear()
      render(<EditorPane pane={createPane('/notes/f-b.md', 'pane-flicker')} isActive />)

      // AC1: raw Monaco shown; no Tiptap; no `Loading editor…` fallback.
      expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
      expect(screen.queryByTestId('tiptap-editor')).toBeNull()
      expect(tiptapPropsSpy).not.toHaveBeenCalled()
      expect(screen.queryByText(/Loading editor/)).toBeNull()

      // AC2: Monaco receives null initialViewState — stale A viewState must not leak.
      expect(monacoPropsSpy).toHaveBeenCalled()
      expect(monacoPropsSpy.mock.calls.every(([p]) => p.initialViewState === null)).toBe(true)

      // AC5: stale showDiff must not pre-mount DiffView; stale cursor must not leak.
      expect(screen.queryByTestId('diff-view')).toBeNull()
      expect(editorStatusBarMock).toHaveBeenCalled()
      expect(editorStatusBarMock.mock.calls.at(-1)?.[0]).toMatchObject({ line: 1, column: 1 })
    } finally {
      spy.mockRestore()
    }
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

describe('EditorPane — load failure (T1.2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    useRecentFilesStore.setState({ files: [] })
  })

  it('renders a load error and creates no buffer when the read fails', async () => {
    const pane = createPane('/notes/read-fail.md', 'pane-read-fail')
    const backend = createBackend()
    backend.read.mockRejectedValue(new Error('network unreachable'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

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

    render(<EditorPane pane={pane} isActive />)

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

    render(<EditorPane pane={pane} isActive />)

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

    render(<EditorPane pane={pane} isActive />)

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

    const { rerender } = render(<EditorPane pane={firstPane} isActive />)

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
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    useRecentFilesStore.setState({ files: [] })
  })

  it('renders the load error instead of a permanent spinner when no backend resolves', async () => {
    const pane = createPane('/notes/no-backend.md', 'pane-no-backend')
    getFsBackendMock.mockReturnValue(undefined)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

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

    render(<EditorPane pane={pane} isActive />)

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

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toMatchObject({
        content: '',
      })
    })

    expect(screen.queryByTestId('editor-load-error')).not.toBeInTheDocument()
  })
})

describe('EditorPane — canSave semantics and dirty affordances (T1.3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    useRecentFilesStore.setState({ files: [] })
  })

  it('keeps Save disabled and shows no dirty dot for a clean loaded buffer with no stat', () => {
    const pane = createPane('/notes/no-stat.md', 'pane-no-stat')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    // A clean, non-untitled buffer whose stat is missing. Pre-opened so the load
    // effect short-circuits on the existing buffer.
    useEditorStore.getState().openBuffer(getBufferKey('/notes/no-stat.md'), 'loaded body', {
      language: 'markdown',
    })

    render(<EditorPane pane={pane} isActive />)

    const buffer = useEditorStore.getState().buffers[getBufferKey('/notes/no-stat.md')]
    expect(buffer.isDirty).toBe(false)
    expect(buffer.lastStat).toBeNull()
    // A missing stat must not masquerade as "modified".
    expect(screen.getByTitle('Save (⌘S)')).toBeDisabled()
    expect(screen.queryByTitle('Unsaved changes')).not.toBeInTheDocument()
  })

  it('keeps Save enabled for an untitled buffer that has never been saved', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-untitled-cansave')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    const buffer = useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]
    expect(buffer.isDirty).toBe(false)
    expect(buffer.lastStat).toBeNull()
    expect(buffer.untitled).toBeDefined()
    expect(screen.getByTitle('Save (⌘S)')).not.toBeDisabled()
  })

  it('shows the dirty dot, an enabled Save and the Diff button for a dirty buffer', async () => {
    const pane = createPane('/notes/dirty.md', 'pane-dirty')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('saved body'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 10, mtime: 7 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    render(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/dirty.md'), 'edited body')
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty.md')].isDirty).toBe(true)
    expect(screen.getByTitle('Unsaved changes')).toBeInTheDocument()
    expect(screen.getByTitle('Save (⌘S)')).not.toBeDisabled()
    expect(screen.getByTitle('Diff against saved')).toBeInTheDocument()
  })
})

const HOST_BOUND_CAPS: PlatformCapabilities = {
  isElectron: false,
  canTearOffTab: false,
  canMergeWindow: false,
  canBrowserPane: false,
  canSystemTray: false,
  canNotification: false,
  devUpdateEnabled: false,
  hasLocalFilesystem: false,
}

describe('EditorPane — host-bound backend resolution', () => {
  const REMOTE_PATH = '/remote/notes.md'
  let fetchMock: ReturnType<typeof vi.fn>

  function remotePane(hostId: string): Pane {
    return {
      id: 'pane-remote',
      content: { kind: 'editor', source: { type: 'daemon', hostId }, filePath: REMOTE_PATH },
    }
  }

  beforeEach(() => {
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useRecentFilesStore.setState({ files: [] })

    // Drive the component through the REAL registry — a mocked getFsBackend
    // could never prove which host the read actually lands on.
    const actual = fsBackendActual.current!
    actual.clearFsBackendRegistry()
    getFsBackendMock.mockImplementation(actual.getFsBackend)

    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, token: 'tokenA', order: 0 },
        hostB: { id: 'hostB', name: 'B', ip: '10.0.0.2', port: 7861, token: 'tokenB', order: 1 },
      },
      hostOrder: ['hostA', 'hostB'],
      activeHostId: 'hostA',
      runtime: {},
    })
    registerBuiltinFsBackends(HOST_BOUND_CAPS)

    fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith('/api/fs/read')) {
        return { ok: true, arrayBuffer: async () => new TextEncoder().encode('remote body').buffer }
      }
      return { ok: true, json: async () => ({ size: 11, mtime: 42, isDirectory: false, isFile: true }) }
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    getFsBackendMock.mockReset()
    fsBackendActual.current!.clearFsBackendRegistry()
    useHostStore.getState().reset()
    useEditorStore.getState().clearAllBuffers()
  })

  it('reads and stats a remote file on its own host while another host is active', async () => {
    render(<EditorPane pane={remotePane('hostB')} isActive />)

    await waitFor(() => {
      expect(
        useEditorStore.getState().buffers[bufferKey({ type: 'daemon', hostId: 'hostB' }, REMOTE_PATH)],
      ).toBeDefined()
    })

    const urls = fetchMock.mock.calls.map((c) => c[0] as string)
    expect(urls).toContain('http://10.0.0.2:7861/api/fs/read')
    expect(urls).toContain('http://10.0.0.2:7861/api/fs/stat')
    expect(urls.every((u) => u.startsWith('http://10.0.0.2:7861'))).toBe(true)
  })

  it('still resolves the active host for a pane bound to it', async () => {
    render(<EditorPane pane={remotePane('hostA')} isActive />)

    await waitFor(() => {
      expect(
        useEditorStore.getState().buffers[bufferKey({ type: 'daemon', hostId: 'hostA' }, REMOTE_PATH)],
      ).toBeDefined()
    })

    const urls = fetchMock.mock.calls.map((c) => c[0] as string)
    expect(urls.every((u) => u.startsWith('http://10.0.0.1:7860'))).toBe(true)
  })
})
