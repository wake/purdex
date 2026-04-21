import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
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

function createPane(filePath = '/notes/editor.md'): Pane {
  return {
    id: 'pane-editor',
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
} {
  return {
    id: 'test-backend',
    label: 'Test Backend',
    available: vi.fn(() => true),
    read: vi.fn(),
    write: vi.fn(),
    stat: vi.fn(),
    list: vi.fn(),
    mkdir: vi.fn(),
    delete: vi.fn(),
    rename: vi.fn(),
  }
}

function getBufferKey(filePath: string): string {
  return `inapp:${filePath}`
}

describe('EditorPane', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
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
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
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
      expect(consoleWarn).toHaveBeenCalledWith('[editor] External change detected for /notes/dirty-reload.md, buffer is dirty')
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty-reload.md')]).toMatchObject({
      content: 'local draft',
      savedContent: 'hello world',
      isDirty: true,
      lastStat: { mtime: 123, size: 11 },
    })

    consoleWarn.mockRestore()
  })
})
