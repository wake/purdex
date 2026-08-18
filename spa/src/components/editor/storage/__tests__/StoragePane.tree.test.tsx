// Full-path identity, expand/collapse, the per-row metadata line (word count vs
// size) and the selection model (plain click never toggles off; modifier click
// does).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { mockBackend } from './storage-pane-mocks'
import {
  makePane,
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

describe('StoragePane — nested tree rendering and selection', () => {
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

    // Expand a → b appears; expand b → x.md appears. (Double-click toggles
    // expand now that single-click selects the folder — T0-1.)
    fireEvent.doubleClick(folderA)
    const folderB = await screen.findByText('b')
    fireEvent.doubleClick(folderB.closest('[data-testid="buffer-row"]')!)
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
    // Double-click expands the folders (single-click now selects — T0-1).
    fireEvent.doubleClick(d1.closest('[data-testid="buffer-row"]')!)
    fireEvent.doubleClick(d2.closest('[data-testid="buffer-row"]')!)
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
    // Expand the folder (double-click) → it switches to FolderOpen, and the md row resolves.
    fireEvent.doubleClick(byPath('/buffer/dir'))
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

  it('N4b: a text file over the size cap shows size only and is NOT read (R2-3)', async () => {
    const big = 300 * 1024 // > WORD_COUNT_MAX_BYTES (256 KiB)
    mockBackend.list.mockResolvedValue([
      { name: 'big.md', isDir: false, size: big },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    expect(row.textContent).toContain(`${big} B`)
    expect(row.textContent).not.toContain('words')
    expect(mockBackend.read).not.toHaveBeenCalled()
  })

  it('N4c: an unknown-extension row shows size only and is NOT read (R2-3)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'blob.bin', isDir: false, size: 12 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    expect(row.textContent).toContain('12 B')
    expect(row.textContent).not.toContain('words')
    expect(mockBackend.read).not.toHaveBeenCalled()
  })

  it('N4d: a leading-dot dotfile on the text allowlist still shows a word count (R3)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: '.env', isDir: false, size: 11 },
    ] as FileEntry[])
    mockBackend.read.mockImplementation(async (path: string) => {
      if (path === '/buffer/.env') return new TextEncoder().encode('A=1 B=2 C=3')
      return new Uint8Array(0)
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    await waitFor(() => expect(row.textContent).toContain('3 words'))
    expect(mockBackend.read).toHaveBeenCalledWith('/buffer/.env')
  })

  // --- Folder-selectable tree + target model (Phase 1b T1b-0) ---

  it('T0-1: clicking a folder name selects it (selected style), without expanding or opening', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/dir/x.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const folder = await screen.findByTestId('buffer-row')
    expect(folder.getAttribute('data-path')).toBe('/buffer/dir')
    fireEvent.click(folder)
    // Folder shows the selected style and stays collapsed; no tab opened.
    expect(folder.getAttribute('aria-selected')).toBe('true')
    expect(screen.queryByText('x.md')).toBeNull()
    expect(openInAppFile).not.toHaveBeenCalled()
  })

  it('T0-1b: the caret toggles expand independently of selection', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/dir/x.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByTestId('buffer-row') // wait for the async tree build
    const rowByPath = (p: string) =>
      screen.getAllByTestId('buffer-row').find((r) => r.getAttribute('data-path') === p)!
    fireEvent.click(rowByPath('/buffer/dir')) // select the folder
    expect(rowByPath('/buffer/dir').getAttribute('aria-selected')).toBe('true')
    // Expanding via the caret reveals the child but leaves selection intact.
    fireEvent.click(within(rowByPath('/buffer/dir')).getByTestId('buffer-caret'))
    await screen.findByText('x.md')
    expect(rowByPath('/buffer/dir').getAttribute('aria-selected')).toBe('true')
    expect(rowByPath('/buffer/dir/x.md').getAttribute('aria-selected')).toBe('false')
  })

  it('T0-2: targetDir derives from selection (none→root, folder→self, file→parent)', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/dir/x.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const region = await screen.findByTestId('storage-tree-region')
    await screen.findByTestId('buffer-row') // wait for the async tree build
    const rowByPath = (p: string) =>
      screen.getAllByTestId('buffer-row').find((r) => r.getAttribute('data-path') === p)!
    // Nothing selected → storage root.
    expect(region.getAttribute('data-target-dir')).toBe('/buffer')
    // Folder selected → the folder itself.
    fireEvent.click(rowByPath('/buffer/dir'))
    expect(region.getAttribute('data-target-dir')).toBe('/buffer/dir')
    // Deselect, expand (caret = no selection), select the nested file → its parent.
    fireEvent.click(rowByPath('/buffer/dir'))
    fireEvent.click(within(rowByPath('/buffer/dir')).getByTestId('buffer-caret'))
    await screen.findByText('x.md')
    fireEvent.click(rowByPath('/buffer/dir/x.md'))
    expect(region.getAttribute('data-target-dir')).toBe('/buffer/dir')
  })

  it('T0-3: double-clicking a folder toggles expand and never opens a tab', async () => {
    mockBackend.list = pathAwareList(
      new Map([
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/dir/x.md', { isDir: false, size: 3 }],
      ]),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const folder = await screen.findByTestId('buffer-row')
    fireEvent.doubleClick(folder)
    await screen.findByText('x.md') // expanded
    expect(openInAppFile).not.toHaveBeenCalled()
  })

  // --- Selection model: plain click never toggles off (codex B5) ---

  it('B5: a second plain click keeps the row selected (no toggle-off)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'a.md', isDir: false, size: 3 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    expect(row.getAttribute('aria-selected')).toBe('true')
    // Pre-fix this second plain click toggled the row OFF (the click→click→
    // dblclick sequence stranded the action target); now it stays selected.
    fireEvent.click(row)
    expect(row.getAttribute('aria-selected')).toBe('true')
  })

  it('B5b: a modifier click toggles the row out of a multi-selection', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'a.md', isDir: false, size: 3 },
      { name: 'b.md', isDir: false, size: 3 },
    ] as FileEntry[])
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    fireEvent.click(rows[0]) // a.md
    fireEvent.click(rows[1], { metaKey: true }) // + b.md
    expect(rows[0].getAttribute('aria-selected')).toBe('true')
    expect(rows[1].getAttribute('aria-selected')).toBe('true')
    // Modifier-click b.md again → toggles it back out.
    fireEvent.click(rows[1], { metaKey: true })
    expect(rows[1].getAttribute('aria-selected')).toBe('false')
    expect(rows[0].getAttribute('aria-selected')).toBe('true')
  })
})
