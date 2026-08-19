// The pure resolver `computeMoveFromDragEnd` is unit-tested in
// `storage-dnd.test.ts`; the cases below drive the wired `onDragEnd` with a
// synthetic drop event whose `over.data.current.targetDir` mirrors what the
// row / root droppables publish (codex B1).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { mockBackend, mockDragEnd } from './storage-pane-mocks'
import {
  makePane,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals
} from './storage-pane-harness'
import type { FileEntry, FileStat } from '../../../../types/fs'
import type { DragEndEvent } from '@dnd-kit/core'

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

describe('StoragePane — drag-and-drop move wiring (Phase 1b T1b-6b)', () => {
  // --- Drag-and-drop move wiring (Phase 1b T1b-6b) ---
  // The pure resolver `computeMoveFromDragEnd` is unit-tested in
  // `storage-dnd.test.ts`; the cases below drive the wired `onDragEnd` with a
  // synthetic drop event whose `over.data.current.targetDir` mirrors what the
  // row / root droppables publish (codex B1).

  it('T6b-1: dragging a file onto a folder moves it into that folder and refreshes', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/a.md', { isDir: false, size: 3 }],
    ])
    mockBackend.list = pathAwareList(paths)
    mockBackend.stat.mockRejectedValue(new Error('not found')) // no collision
    mockBackend.rename = vi.fn(async (from: string, to: string) => {
      const meta = paths.get(from)!
      paths.delete(from)
      paths.set(to, meta)
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    expect(mockDragEnd).toBeTypeOf('function') // DndContext mounted + wired
    await act(async () => {
      await mockDragEnd!({
        active: { id: '/buffer/a.md' },
        over: { id: '/buffer/dir', data: { current: { targetDir: '/buffer/dir' } } },
      } as unknown as DragEndEvent)
    })
    expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/a.md', '/buffer/dir/a.md')
    // Tree refreshed: the file now lives under the (expandable) folder.
    await waitFor(() => {
      const names = screen.getAllByTestId('buffer-row').map((r) => r.getAttribute('data-path'))
      expect(names).not.toContain('/buffer/a.md')
    })
  })

  it('T6b-2: dropping a nested file on the root region moves it to STORAGE_ROOT', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/dir/a.md', { isDir: false, size: 3 }],
    ])
    mockBackend.list = pathAwareList(paths)
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    mockBackend.rename = vi.fn(async (from: string, to: string) => {
      const meta = paths.get(from)!
      paths.delete(from)
      paths.set(to, meta)
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    await act(async () => {
      await mockDragEnd!({
        active: { id: '/buffer/dir/a.md' },
        over: { id: '/buffer', data: { current: { targetDir: '/buffer' } } },
      } as unknown as DragEndEvent)
    })
    expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/dir/a.md', '/buffer/a.md')
  })

  it('T6b-3: dropping onto self or own descendant is a no-op (no backend mutation)', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/dir/sub', { isDir: true, size: 0 }],
    ])
    mockBackend.list = pathAwareList(paths)
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    // onto self
    await act(async () => {
      await mockDragEnd!({
        active: { id: '/buffer/dir' },
        over: { id: '/buffer/dir', data: { current: { targetDir: '/buffer/dir' } } },
      } as unknown as DragEndEvent)
    })
    // into own descendant
    await act(async () => {
      await mockDragEnd!({
        active: { id: '/buffer/dir' },
        over: { id: '/buffer/dir/sub', data: { current: { targetDir: '/buffer/dir/sub' } } },
      } as unknown as DragEndEvent)
    })
    expect(mockBackend.rename).not.toHaveBeenCalled()
    expect(mockBackend.stat).not.toHaveBeenCalled()
  })

  it('T6b-3b: dropping onto an existing same-named entry surfaces the inline exists error', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/dir/a.md', { isDir: false, size: 3 }],
      ['/buffer/a.md', { isDir: false, size: 9 }],
    ])
    mockBackend.list = pathAwareList(paths)
    // The target /buffer/dir/a.md already exists.
    mockBackend.stat.mockImplementation(async (p: string) => {
      if (paths.has(p)) return { size: 0, mtime: 0, isDirectory: false, isFile: true } as FileStat
      throw new Error('not found')
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    await act(async () => {
      await mockDragEnd!({
        active: { id: '/buffer/a.md' },
        over: { id: '/buffer/dir', data: { current: { targetDir: '/buffer/dir' } } },
      } as unknown as DragEndEvent)
    })
    expect(mockBackend.rename).not.toHaveBeenCalled()
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.rename_exists_error')).toBeTruthy()
    })
  })

  it('T6b-4: click still selects and double-click still opens (no DnD regression)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'a.md', isDir: false, size: 3 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    expect(row.getAttribute('aria-selected')).toBe('true')
    fireEvent.doubleClick(row)
    await waitFor(() => {
      expect(openInAppFile).toHaveBeenCalledWith('/buffer/a.md', 'ws1')
    })
  })

  it('B1-UI: dropping a file onto a same-dir sibling is a no-op (targetDir = shared parent)', async () => {
    // The sibling row publishes /buffer as its targetDir; the move then collapses
    // to a no-op (targetDir === parentOf(from)) instead of bouncing to root.
    mockBackend.list.mockResolvedValue([
      { name: 'a.md', isDir: false, size: 3 },
      { name: 'b.md', isDir: false, size: 3 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findAllByTestId('buffer-row')
    await act(async () => {
      await mockDragEnd!({
        active: { id: '/buffer/a.md' },
        over: { id: '/buffer/b.md', data: { current: { targetDir: '/buffer' } } },
      } as unknown as DragEndEvent)
    })
    expect(mockBackend.rename).not.toHaveBeenCalled()
    expect(mockBackend.stat).not.toHaveBeenCalled()
  })
})
