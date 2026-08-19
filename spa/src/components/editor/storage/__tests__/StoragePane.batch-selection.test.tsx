// T4.3 — the multi-selection that already existed (cmd/ctrl/shift-click) made
// visible: a per-row checkbox, a header select-all, and a selection action bar.
// All three drive the SAME `selected` set the modifier-click path uses, so the
// two gestures compose instead of competing.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { mockBackend, tSpy } from './storage-pane-mocks'
import {
  makePane,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals,
} from './storage-pane-harness'
import type { FileEntry } from '../../../../types/fs'

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

function threeFiles() {
  mockBackend.list.mockResolvedValue([
    { name: 'a.md', isDir: false, size: 10 },
    { name: 'b.md', isDir: false, size: 10 },
    { name: 'c.md', isDir: false, size: 10 },
  ] as FileEntry[])
}

async function rowFor(name: string): Promise<HTMLElement> {
  const rows = await screen.findAllByTestId('buffer-row')
  const row = rows.find((r) => r.getAttribute('data-name') === name)
  if (!row) throw new Error(`row ${name} not rendered`)
  return row
}

async function checkboxFor(name: string): Promise<HTMLInputElement> {
  return within(await rowFor(name)).getByTestId('row-checkbox') as HTMLInputElement
}

async function isSelected(name: string): Promise<boolean> {
  return (await rowFor(name)).getAttribute('aria-selected') === 'true'
}

describe('T4.3 — row checkboxes share the existing selection set', () => {
  it('a row checkbox toggles that row into / out of the selection', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await checkboxFor('a.md'))
    await waitFor(async () => expect(await isSelected('a.md')).toBe(true))
    expect((await checkboxFor('a.md')).checked).toBe(true)

    fireEvent.click(await checkboxFor('a.md'))
    await waitFor(async () => expect(await isSelected('a.md')).toBe(false))
    expect((await checkboxFor('a.md')).checked).toBe(false)
  })

  it('checkbox and modifier-click write to the SAME set (they accumulate)', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await checkboxFor('a.md'))
    fireEvent.click(await rowFor('b.md'), { metaKey: true })
    await waitFor(async () => expect(await isSelected('b.md')).toBe(true))
    expect(await isSelected('a.md')).toBe(true)
    expect((await checkboxFor('b.md')).checked).toBe(true)

    // A modifier-click can un-check what the checkbox selected, and vice versa.
    fireEvent.click(await rowFor('a.md'), { metaKey: true })
    await waitFor(async () => expect(await isSelected('a.md')).toBe(false))
    expect((await checkboxFor('a.md')).checked).toBe(false)
  })

  it('REGRESSION: cmd/shift-click selection semantics are unchanged', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    // Plain click replaces the selection…
    fireEvent.click(await rowFor('a.md'))
    fireEvent.click(await rowFor('b.md'))
    await waitFor(async () => expect(await isSelected('b.md')).toBe(true))
    expect(await isSelected('a.md')).toBe(false)
    // …a modifier click adds, and toggles back off.
    fireEvent.click(await rowFor('c.md'), { shiftKey: true })
    await waitFor(async () => expect(await isSelected('c.md')).toBe(true))
    expect(await isSelected('b.md')).toBe(true)
    fireEvent.click(await rowFor('c.md'), { ctrlKey: true })
    await waitFor(async () => expect(await isSelected('c.md')).toBe(false))
  })

  it('REGRESSION: clicking a checkbox never fires the row click (no open, no replace-selection)', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await checkboxFor('a.md'))
    fireEvent.click(await checkboxFor('b.md'))
    await waitFor(async () => expect(await isSelected('b.md')).toBe(true))
    // If the checkbox click had bubbled, b.md's plain row click would have
    // REPLACED the selection and dropped a.md.
    expect(await isSelected('a.md')).toBe(true)
    expect(openInAppFile as unknown as Mock).not.toHaveBeenCalled()

    // Double-clicking the checkbox must not open the file either.
    fireEvent.doubleClick(await checkboxFor('a.md'))
    expect(openInAppFile as unknown as Mock).not.toHaveBeenCalled()
  })
})

describe('T4.3 — header select-all', () => {
  it('selects every visible row, then clears them all', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await rowFor('a.md')
    const all = screen.getByTestId('select-all-checkbox') as HTMLInputElement
    fireEvent.click(all)
    await waitFor(async () => expect(await isSelected('a.md')).toBe(true))
    expect(await isSelected('b.md')).toBe(true)
    expect(await isSelected('c.md')).toBe(true)
    expect((screen.getByTestId('select-all-checkbox') as HTMLInputElement).checked).toBe(true)

    fireEvent.click(screen.getByTestId('select-all-checkbox'))
    await waitFor(async () => expect(await isSelected('a.md')).toBe(false))
    expect(await isSelected('b.md')).toBe(false)
    expect(await isSelected('c.md')).toBe(false)
  })

  it('is indeterminate while only some rows are selected', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    const all = () => screen.getByTestId('select-all-checkbox') as HTMLInputElement
    await rowFor('a.md')
    expect(all().indeterminate).toBe(false)
    expect(all().checked).toBe(false)

    fireEvent.click(await checkboxFor('a.md'))
    await waitFor(() => expect(all().indeterminate).toBe(true))
    expect(all().checked).toBe(false)

    fireEvent.click(await checkboxFor('b.md'))
    fireEvent.click(await checkboxFor('c.md'))
    await waitFor(() => expect(all().checked).toBe(true))
    expect(all().indeterminate).toBe(false)
  })

  it('covers rows revealed by an expanded folder, and ignores collapsed children', async () => {
    mockBackend.list.mockImplementation(
      pathAwareList(
        new Map([
          ['/buffer/dir', { isDir: true, size: 0 }],
          ['/buffer/dir/child.md', { isDir: false, size: 5 }],
          ['/buffer/top.md', { isDir: false, size: 5 }],
        ]),
      ),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const dirRow = await rowFor('dir')
    // Collapsed: select-all covers the folder + the top-level file only.
    fireEvent.click(screen.getByTestId('select-all-checkbox'))
    await waitFor(async () => expect(await isSelected('dir')).toBe(true))
    expect(await isSelected('top.md')).toBe(true)
    expect(screen.queryByText('child.md')).toBeNull()

    // Expand, and select-all now covers the newly visible child too.
    fireEvent.click(screen.getByTestId('select-all-checkbox')) // clear first
    fireEvent.click(within(dirRow).getByTestId('buffer-caret'))
    await rowFor('child.md')
    fireEvent.click(screen.getByTestId('select-all-checkbox'))
    await waitFor(async () => expect(await isSelected('child.md')).toBe(true))
    expect(await isSelected('dir')).toBe(true)
    expect(await isSelected('top.md')).toBe(true)
  })
})

describe('T4.3 — selection action bar', () => {
  it('is absent with an empty selection and shows the count once rows are selected', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await rowFor('a.md')
    expect(screen.queryByTestId('selection-action-bar')).toBeNull()

    fireEvent.click(await checkboxFor('a.md'))
    fireEvent.click(await checkboxFor('b.md'))
    const bar = await screen.findByTestId('selection-action-bar')
    expect(within(bar).getByTestId('selection-count')).toBeTruthy()
    // The count reaches i18n as an interpolation param (the mocked `t` returns
    // the bare key), so assert the param the way the upload banner suite does.
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.selected_count', { count: 2 })
    expect(within(bar).getByTestId('selection-delete')).toBeTruthy()
    expect(within(bar).getByTestId('selection-clear')).toBeTruthy()
  })

  it('Delete removes every selected entry through the existing batch delete', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await checkboxFor('a.md'))
    fireEvent.click(await checkboxFor('c.md'))
    fireEvent.click(
      within(await screen.findByTestId('selection-action-bar')).getByTestId('selection-delete'),
    )
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/a.md', false)
    })
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/c.md', false)
    expect(mockBackend.delete).toHaveBeenCalledTimes(2)
    expect(mockBackend.delete).not.toHaveBeenCalledWith('/buffer/b.md', false)
  })

  it('Clear selection empties the selection without deleting anything', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await checkboxFor('a.md'))
    fireEvent.click(await checkboxFor('b.md'))
    fireEvent.click(
      within(await screen.findByTestId('selection-action-bar')).getByTestId('selection-clear'),
    )
    await waitFor(() => {
      expect(screen.queryByTestId('selection-action-bar')).toBeNull()
    })
    expect(await isSelected('a.md')).toBe(false)
    expect(await isSelected('b.md')).toBe(false)
    expect(mockBackend.delete).not.toHaveBeenCalled()
  })
})

describe('T4.3 — a delete prunes the selection the way it prunes the tree', () => {
  // Deleting a folder takes its whole subtree with it, so a selected descendant
  // is gone too. Dropping only the exact deleted path leaves the selection
  // holding paths that no longer exist: the action bar counts them, and the next
  // batch delete aims at a file that is not there any more.
  const files = () =>
    new Map([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/dir/child.md', { isDir: false, size: 5 }],
      ['/buffer/top.md', { isDir: false, size: 5 }],
    ])

  /** The last count the action bar handed to i18n (the mocked `t` returns keys). */
  function renderedSelectionCount(): number | undefined {
    const counts = tSpy.mock.calls.filter((call) => call[0] === 'editor.buffers.selected_count')
    return (counts.at(-1)?.[1] as { count: number } | undefined)?.count
  }

  async function seedTreeWithSelectedChild(): Promise<void> {
    const tree = files()
    mockBackend.list.mockImplementation(pathAwareList(tree))
    mockBackend.stat.mockImplementation(async (path: string) => ({
      size: 0,
      mtime: 0,
      isDirectory: tree.get(path)?.isDir === true,
      isFile: tree.get(path)?.isDir !== true,
    }))
    mockBackend.delete.mockImplementation(async (path: string) => {
      for (const p of Array.from(tree.keys())) {
        if (p === path || p.startsWith(path + '/')) tree.delete(p)
      }
    })
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(within(await rowFor('dir')).getByTestId('buffer-caret'))
    await rowFor('child.md')
    fireEvent.click(await checkboxFor('child.md'))
    fireEvent.click(await checkboxFor('top.md'))
    await waitFor(() => expect(renderedSelectionCount()).toBe(2))
  }

  it('deleting a FOLDER drops the selected file inside it from the selection', async () => {
    await seedTreeWithSelectedChild()

    fireEvent.click(within(await rowFor('dir')).getByTestId('row-action-delete'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/dir', true))
    // Only `top.md` is left standing, so only `top.md` may be counted.
    await waitFor(() => expect(renderedSelectionCount()).toBe(1))
    expect(await isSelected('top.md')).toBe(true)
  })

  it('a follow-up batch delete never aims at the vanished descendant', async () => {
    await seedTreeWithSelectedChild()
    fireEvent.click(within(await rowFor('dir')).getByTestId('row-action-delete'))
    await waitFor(() => expect(renderedSelectionCount()).toBe(1))

    fireEvent.click(
      within(await screen.findByTestId('selection-action-bar')).getByTestId('selection-delete'),
    )

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/top.md', false))
    expect(mockBackend.delete).not.toHaveBeenCalledWith('/buffer/dir/child.md', false)
  })

  it('deleting a SIBLING leaves an unrelated prefix-sharing path selected', async () => {
    // The prune is a subtree rule, not a `startsWith`: `/buffer/dirty.md` is not
    // inside `/buffer/dir` and must survive its delete.
    const tree = new Map([
      ['/buffer/dir', { isDir: true, size: 0 }],
      ['/buffer/dirty.md', { isDir: false, size: 5 }],
    ])
    mockBackend.list.mockImplementation(pathAwareList(tree))
    mockBackend.stat.mockImplementation(async (path: string) => ({
      size: 0,
      mtime: 0,
      isDirectory: tree.get(path)?.isDir === true,
      isFile: tree.get(path)?.isDir !== true,
    }))
    mockBackend.delete.mockImplementation(async (path: string) => {
      for (const p of Array.from(tree.keys())) {
        if (p === path || p.startsWith(path + '/')) tree.delete(p)
      }
    })
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await checkboxFor('dirty.md'))
    await waitFor(() => expect(renderedSelectionCount()).toBe(1))

    fireEvent.click(within(await rowFor('dir')).getByTestId('row-action-delete'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/dir', true))
    expect(await isSelected('dirty.md')).toBe(true)
    expect(renderedSelectionCount()).toBe(1)
  })
})
