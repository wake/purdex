// EditorPane — the in-editor rename and the recent-files remap it drives (T3.2).
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createBackend,
  createPane,
  createUntitledPane,
  getBufferKey,
  registerTabPane,
  renderEditorPane,
  resetEditorPaneStores,
  statMissingUntilWritten,
} from './editor-pane-harness'
import { getFsBackendMock } from './editor-pane-mocks'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import type { Pane } from '../../../types/tab'
import type { FileSource } from '../../../types/fs'

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

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/rename.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('rename.md'))

    expect(screen.getByDisplayValue('rename.md')).toBeInTheDocument()
    expect(screen.queryByLabelText('Rename file')).not.toBeInTheDocument()
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

    renderEditorPane(pane)

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

    renderEditorPane(pane)

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

    renderEditorPane(pane)

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

    renderEditorPane(paneA)
    renderEditorPane(paneB, false)

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

    renderEditorPane(pane)

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

    renderEditorPane(pane)

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

})

// --- T3.2: the in-editor rename is the third path-mutating call site ---------
//
// Storage rename/move/delete are covered in storage-actions.test.ts; this is the
// rename the user performs from the editor toolbar, which is also the ONLY remap
// path a remote (daemon) file can take — it goes through the resolved backend, so
// the same code covers both sources.
describe('EditorPane — in-editor rename remaps the recent entry (T3.2)', () => {
  function seedRecent(source: FileSource, path: string, openedAt = 5) {
    useRecentFilesStore.setState({
      files: [{ source, path, name: path.split('/').pop()!, kind: 'editor', openedAt }],
    })
  }

  beforeEach(resetEditorPaneStores)

  it('T3.2-4: renaming a saved in-app file re-points its recent entry (path + name)', async () => {
    const pane = createPane('/notes/old.md', 'pane-rename-recent')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('body'))
    backend.rename.mockResolvedValue(undefined)
    backend.stat
      // 1) the initial load, 2) the rename-target existence probe (must reject).
      .mockResolvedValueOnce({ isFile: true, isDirectory: false, size: 4, mtime: 1 })
      .mockRejectedValueOnce(new Error('not found'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    seedRecent({ type: 'inapp' }, '/notes/old.md')

    renderEditorPane(pane)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/old.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('old.md'))
    fireEvent.change(screen.getByDisplayValue('old.md'), { target: { value: 'new.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('new.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(backend.rename).toHaveBeenCalledWith('/notes/old.md', '/notes/new.md')
    })
    await waitFor(() => {
      expect(useRecentFilesStore.getState().files).toEqual([
        { source: { type: 'inapp' }, path: '/notes/new.md', name: 'new.md', kind: 'editor', openedAt: 5 },
      ])
    })
  })

  it('T3.2-4b: the same path covers a REMOTE file — the daemon entry follows its host', async () => {
    const remoteSource: FileSource = { type: 'daemon', hostId: 'hostB' }
    const pane: Pane = {
      id: 'pane-remote-rename',
      content: { kind: 'editor', source: remoteSource, filePath: '/remote/old.md' },
    }
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('body'))
    backend.rename.mockResolvedValue(undefined)
    backend.stat
      .mockResolvedValueOnce({ isFile: true, isDirectory: false, size: 4, mtime: 1 })
      .mockRejectedValueOnce(new Error('not found'))
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    // Same path on another host must NOT be dragged along.
    useRecentFilesStore.setState({
      files: [
        { source: remoteSource, path: '/remote/old.md', name: 'old.md', kind: 'editor', openedAt: 7 },
        {
          source: { type: 'daemon', hostId: 'hostA' },
          path: '/remote/old.md',
          name: 'old.md',
          kind: 'editor',
          openedAt: 6,
        },
      ],
    })

    renderEditorPane(pane)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[bufferKey(remoteSource, '/remote/old.md')]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('old.md'))
    fireEvent.change(screen.getByDisplayValue('old.md'), { target: { value: 'new.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('new.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(backend.rename).toHaveBeenCalledWith('/remote/old.md', '/remote/new.md')
    })
    await waitFor(() => {
      expect(
        useRecentFilesStore.getState().files.map((f) => [
          f.source.type === 'daemon' ? f.source.hostId : f.source.type,
          f.path,
        ]),
      ).toEqual([
        ['hostB', '/remote/new.md'],
        ['hostA', '/remote/old.md'],
      ])
    })
  })

  it('T3.2-5: the untitled first save records the real path and never attempts a remap', async () => {
    const renameSpy = vi.spyOn(useRecentFilesStore.getState(), 'renamePath')
    const pane = createUntitledPane('Untitled', '.md', 'pane-untitled-no-remap')
    const backend = createBackend()
    backend.write.mockResolvedValue(undefined)
    statMissingUntilWritten(backend, { size: 0, mtime: 456 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))
    fireEvent.keyDown(screen.getByDisplayValue('Untitled.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/buffer/Untitled.md', new TextEncoder().encode(''))
    })
    // The unsaved buffer was never in the list, so there is nothing to re-point:
    // the first save records the real path directly.
    expect(renameSpy).not.toHaveBeenCalled()
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual(['/buffer/Untitled.md'])
    renameSpy.mockRestore()
  })
})
