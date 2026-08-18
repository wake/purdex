// Straight CRUD through the toolbar: list, New File, New Folder, in-place
// rename (file + folder, incl. the refusals) and delete of a leaf path.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { mockBackend, closePaneSpy, tabStoreState } from './storage-pane-mocks'
import {
  makeEditorTab,
  makePane,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals
} from './storage-pane-harness'
import { makeEditorBuffer } from '../../../../stores/__tests__/editor-buffer-fixture'
import type { FileEntry, FileStat } from '../../../../types/fs'

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

describe('StoragePane — create / rename / delete of a single entry', () => {
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
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/foo.md', false)
    })
  })

  it('B2-4: New calls backend.createUnique(root, Untitled, md) and refreshes', async () => {
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    fireEvent.click(screen.getByTestId('toolbar-new'))
    await waitFor(() => {
      expect(mockBackend.createUnique).toHaveBeenCalledTimes(1)
    })
    // Eager unified namer (#854): no blind write(), reservation is atomic.
    expect(mockBackend.createUnique).toHaveBeenCalledWith('/buffer', 'Untitled', 'md')
    expect(mockBackend.write).not.toHaveBeenCalled()
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
      const editorModule = await import('../../../../stores/useEditorStore')
      editorModule.useEditorStore.setState({
        buffers: {
          'inapp:/buffer/z.md': makeEditorBuffer({
            content: 'dirty',
            savedContent: '',
            isDirty: true,
          }),
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
      const editorModule = await import('../../../../stores/useEditorStore')
      editorModule.useEditorStore.setState({ buffers: {}, paneStates: {} })
      const confirmTrue = vi.fn(() => true)
      vi.stubGlobal('confirm', confirmTrue)
      render(<StoragePane pane={makePane()} isActive />)
      const row = await screen.findByTestId('buffer-row')
      fireEvent.click(row)
      fireEvent.click(screen.getByTestId('toolbar-delete'))
      expect(confirmTrue).toHaveBeenCalledWith('editor.buffers.delete_one_confirm')
      await waitFor(() => {
        expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/only.md', false)
      })
    }
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
    // Double-click expands the folders (single-click now selects — T0-1).
    fireEvent.doubleClick(await screen.findByText('a').then((n) => n.closest('[data-testid="buffer-row"]')!))
    fireEvent.doubleClick((await screen.findByText('b')).closest('[data-testid="buffer-row"]')!)
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
    // Double-click expands the folders (single-click now selects — T0-1).
    fireEvent.doubleClick((await screen.findByText('a')).closest('[data-testid="buffer-row"]')!)
    fireEvent.doubleClick((await screen.findByText('b')).closest('[data-testid="buffer-row"]')!)
    const leafRow = (await screen.findByText('x.md')).closest('[data-testid="buffer-row"]')!
    fireEvent.click(leafRow)
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/a/b/x.md', false)
    })
  })

  // --- New Folder (mkdir) + New File targetDir wiring (Phase 1b T1b-3) ---

  it('T3-1: New Folder creates a directory (mkdirUnique) that appears in the tree', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>()
    mockBackend.list = pathAwareList(paths)
    mockBackend.mkdirUnique = vi.fn(async (dir: string) => {
      const path = `${dir}/New Folder`
      paths.set(path, { isDir: true, size: 0 })
      return path
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    fireEvent.click(screen.getByTestId('toolbar-new-folder'))
    await waitFor(() => {
      // No selection → targets the storage root.
      expect(mockBackend.mkdirUnique).toHaveBeenCalledWith('/buffer')
    })
    const row = await screen.findByTestId('buffer-row')
    expect(row.getAttribute('data-path')).toBe('/buffer/New Folder')
    expect(row.getAttribute('data-isdir')).toBe('true')
    // The new folder is auto-selected.
    expect(row.getAttribute('aria-selected')).toBe('true')
  })

  it('T3-2: a New File created with the new folder selected lands inside it (folder accepts children)', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>()
    mockBackend.list = pathAwareList(paths)
    mockBackend.mkdirUnique = vi.fn(async (dir: string) => {
      const path = `${dir}/New Folder`
      paths.set(path, { isDir: true, size: 0 })
      return path
    })
    mockBackend.createUnique = vi.fn(async (dir: string, base: string, ext: string) => {
      const path = `${dir}/${base}.${ext}`
      paths.set(path, { isDir: false, size: 0 })
      return path
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    // New Folder → auto-selected + auto-expanded.
    fireEvent.click(screen.getByTestId('toolbar-new-folder'))
    await screen.findByTestId('buffer-row') // tree rebuilt → selection resolves to the folder
    // New File now targets the selected folder (T1b-0 wiring).
    fireEvent.click(screen.getByTestId('toolbar-new'))
    await waitFor(() => {
      expect(mockBackend.createUnique).toHaveBeenCalledWith('/buffer/New Folder', 'Untitled', 'md')
    })
    // The child file shows up under the (auto-expanded) folder.
    await waitFor(() => {
      const child = screen
        .getAllByTestId('buffer-row')
        .find((r) => r.getAttribute('data-path') === '/buffer/New Folder/Untitled.md')
      expect(child).toBeTruthy()
    })
  })

  // --- In-place rename of a folder selection (Phase 1b T1b-4) ---

  it('T4-UI-1: renaming a selected folder routes through backend.rename and re-selects the new path', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/dir/x.md', { isDir: false, size: 3 }],
    ])
    mockBackend.list = pathAwareList(paths)
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    // Re-key the fixture like the real recursive backend.rename (T1b-1).
    mockBackend.rename = vi.fn(async (from: string, to: string) => {
      for (const [p, meta] of Array.from(paths)) {
        if (p === from || p.startsWith(from + '/')) {
          paths.delete(p)
          paths.set(to + p.slice(from.length), meta)
        }
      }
    })
    render(<StoragePane pane={makePane()} isActive />)
    const folder = await screen.findByTestId('buffer-row')
    fireEvent.click(folder) // select the folder
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    expect((input as HTMLInputElement).value).toBe('dir')
    fireEvent.change(input, { target: { value: 'docs' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/dir', '/buffer/docs')
    })
    expect(mockBackend.rename).toHaveBeenCalledTimes(1)
    // Tree rebuilt → the renamed folder is present and re-selected by new path.
    await waitFor(() => {
      const row = screen
        .getAllByTestId('buffer-row')
        .find((r) => r.getAttribute('data-path') === '/buffer/docs')
      expect(row).toBeTruthy()
      expect(row!.getAttribute('aria-selected')).toBe('true')
    })
  })

  it('T4-UI-2: folder rename onto an existing name shows the inline exists error (no mutation)', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/a', { isDir: true, size: 0 }],
        ['/buffer/z', { isDir: true, size: 0 }],
      ]),
    )
    mockBackend.stat.mockImplementation(async (p: string) => {
      if (p === '/buffer/z') return { size: 0, mtime: 0, isDirectory: true, isFile: false } as FileStat
      throw new Error('not found')
    })
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    fireEvent.click(rows.find((r) => r.getAttribute('data-path') === '/buffer/a')!)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'z' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(screen.getByTestId('rename-error').textContent).toBe('editor.buffers.rename_exists_error')
    })
    expect(mockBackend.rename).not.toHaveBeenCalled()
  })

  it('T3-3: New Folder does not overwrite an existing folder — it increments the name', async () => {
    // A "New Folder" already exists (e.g. from a prior session); creating
    // another must not clobber it (decision 7 — add-reserve increments).
    const paths = new Map<string, { isDir: boolean; size: number }>([
      ['/buffer/New Folder', { isDir: true, size: 0 }],
    ])
    mockBackend.list = pathAwareList(paths)
    mockBackend.mkdirUnique = vi.fn(async (dir: string) => {
      let n = 0
      let path = `${dir}/New Folder`
      while (paths.has(path)) {
        n += 1
        path = `${dir}/New Folder ${n}`
      }
      paths.set(path, { isDir: true, size: 0 })
      return path
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByTestId('buffer-row')
    fireEvent.click(screen.getByTestId('toolbar-new-folder'))
    await waitFor(() => {
      const names = screen
        .getAllByTestId('buffer-row')
        .map((r) => r.getAttribute('data-path'))
      expect(names).toContain('/buffer/New Folder')
      expect(names).toContain('/buffer/New Folder 1')
    })
  })

  // --- Same-name rename closes the popover without a backend rename (codex B4) ---

  it('B4-UI: confirming a rename with the SAME name closes the popover and never calls backend.rename', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    expect((input as HTMLInputElement).value).toBe('foo.md')
    // Confirm with the unchanged name.
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(screen.queryByTestId('rename-popover-harness')).toBeNull()
    })
    expect(mockBackend.rename).not.toHaveBeenCalled()
    expect(mockBackend.stat).not.toHaveBeenCalled()
  })

  // --- D2: rapid double new-file converges on the atomic namer (no shared key) ---

  it('D2: rapid double New File routes through the atomic createUnique namer with no shared key', async () => {
    let n = 0
    mockBackend.list.mockResolvedValue([] as FileEntry[])
    // Atomic reservation: each call hands back a distinct path (mirrors the IDB
    // `add` serialization point) — never a Date.now()-style shared key.
    mockBackend.createUnique = vi.fn(async () => {
      n += 1
      return n === 1 ? '/buffer/Untitled.md' : `/buffer/Untitled-${n - 1}.md`
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')
    const newBtn = screen.getByTestId('toolbar-new')
    // Two rapid clicks. The busy-disable guard may serialize them to a single
    // reservation, but whatever fires goes through the atomic namer — no blind
    // write, and any reservations are distinct (no shared key, #854).
    fireEvent.click(newBtn)
    fireEvent.click(newBtn)
    await waitFor(() => {
      expect(mockBackend.createUnique).toHaveBeenCalled()
    })
    expect(mockBackend.write).not.toHaveBeenCalled()
    const reserved = await Promise.all(
      (mockBackend.createUnique as Mock).mock.results.map((r) => r.value),
    )
    // No two reservations collide on the same key.
    expect(new Set(reserved).size).toBe(reserved.length)
  })
})
