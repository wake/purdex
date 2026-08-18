// T4.2 — manual empty-file cleanup.
//
// The backlog this clears is real: eager reservation (#854) mints a genuine 0 B
// `Untitled-N.txt` the moment "New File" is pressed, and a tab that is opened
// but never typed into leaves that file behind forever. This is the MANUAL
// broom: scan the already-loaded tree for 0 B files, show exactly what would go,
// delete the confirmed set through the existing batch delete.
//
// Delete semantics (plan T4.2, corrected after review): `deleteStorageEntries`
// deletes path by path with NO transaction, so a mid-way failure leaves the
// earlier paths deleted. That is the accepted behaviour for a housekeeping
// action on 0 B files — the acceptance criterion is "the error is surfaced and
// the tree is refreshed", never "nothing was deleted".
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { findEmptyFiles } from '../storage-actions'
import { useUndoToast } from '../../../../stores/useUndoToast'
import { mockBackend, tSpy } from './storage-pane-mocks'
import {
  makePane,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals,
} from './storage-pane-harness'
import type { TreeNode } from '../../../../lib/storage-tree'

vi.mock('@dnd-kit/core', async () => (await import('./storage-pane-mocks')).dndKitMock())
vi.mock('../../../../lib/fs-backend', async () => (await import('./storage-pane-mocks')).fsBackendMock())
vi.mock('../../../../lib/open-in-app-file', async () => (await import('./storage-pane-mocks')).openInAppFileMock())
vi.mock('../../../../lib/download-file', async () => (await import('./storage-pane-mocks')).downloadFileMock())
vi.mock('../../../../features/workspace/store', async () => (await import('./storage-pane-mocks')).workspaceStoreMock())
vi.mock('../../../../stores/useI18nStore', async () => (await import('./storage-pane-mocks')).i18nStoreMock())
vi.mock('../../../../stores/useTabStore', async () => (await import('./storage-pane-mocks')).tabStoreMock())
vi.mock('../../../RenamePopover', async () => (await import('./storage-pane-mocks')).renamePopoverMock())

beforeEach(() => {
  resetStoragePaneMocks()
  useUndoToast.setState({ toast: null })
})
afterEach(restoreStoragePaneGlobals)

// --- the pure scan ---------------------------------------------------------

function file(path: string, size: number): TreeNode {
  return { path, name: path.split('/').pop() ?? path, isDir: false, size }
}

function dir(path: string, children: TreeNode[]): TreeNode {
  return { path, name: path.split('/').pop() ?? path, isDir: true, size: 0, children }
}

describe('findEmptyFiles — the pure 0 B scan', () => {
  it('returns exactly the 0 B FILES, recursing into subfolders', () => {
    const tree: TreeNode[] = [
      dir('/buffer/dir', [
        file('/buffer/dir/nested-empty.md', 0),
        file('/buffer/dir/nested-full.md', 3),
        dir('/buffer/dir/deep', [file('/buffer/dir/deep/deep-empty.txt', 0)]),
      ]),
      file('/buffer/Untitled.txt', 0),
      file('/buffer/notes.md', 42),
    ]
    expect(findEmptyFiles(tree).sort()).toEqual([
      '/buffer/Untitled.txt',
      '/buffer/dir/deep/deep-empty.txt',
      '/buffer/dir/nested-empty.md',
    ])
  })

  it('never returns a folder, even an empty one', () => {
    const tree: TreeNode[] = [dir('/buffer/empty-dir', []), dir('/buffer/outer', [dir('/buffer/outer/inner', [])])]
    expect(findEmptyFiles(tree)).toEqual([])
  })

  it('returns nothing for an empty tree or a tree of non-empty files', () => {
    expect(findEmptyFiles([])).toEqual([])
    expect(findEmptyFiles([file('/buffer/a.md', 1)])).toEqual([])
  })
})

// --- the toolbar action ----------------------------------------------------

/** Two 0 B files at the root, one nested in a folder, one non-empty file. */
function mixedTree() {
  mockBackend.list.mockImplementation(
    pathAwareList(
      new Map([
        ['/buffer/Untitled.txt', { isDir: false, size: 0 }],
        ['/buffer/Untitled-1.txt', { isDir: false, size: 0 }],
        ['/buffer/notes.md', { isDir: false, size: 12 }],
        ['/buffer/dir', { isDir: true, size: 0 }],
        ['/buffer/dir/nested-empty.md', { isDir: false, size: 0 }],
      ]),
    ),
  )
}

async function openCleanupDialog(): Promise<HTMLElement> {
  fireEvent.click(await screen.findByTestId('toolbar-clean-empty'))
  return screen.findByTestId('empty-cleanup-dialog')
}

describe('T4.2 — the cleanup dialog lists the candidates', () => {
  it('lists exactly the 0 B files (root + nested), and no folder or non-empty file', async () => {
    mixedTree()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()
    const listed = within(dialog)
      .getAllByTestId('empty-cleanup-item')
      .map((el) => el.textContent)
      .sort()
    expect(listed).toEqual([
      '/buffer/Untitled-1.txt',
      '/buffer/Untitled.txt',
      '/buffer/dir/nested-empty.md',
    ])
  })

  it('cancelling closes the dialog and deletes nothing', async () => {
    mixedTree()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()
    fireEvent.click(within(dialog).getByTestId('empty-cleanup-cancel'))
    await waitFor(() => expect(screen.queryByTestId('empty-cleanup-dialog')).toBeNull())
    expect(mockBackend.delete).not.toHaveBeenCalled()
  })
})

describe('T4.2 — confirming deletes the listed set and reports the count', () => {
  it('deletes exactly the listed paths in one pass and toasts the count', async () => {
    mixedTree()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()
    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledTimes(3))
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/Untitled.txt', false)
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/Untitled-1.txt', false)
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/dir/nested-empty.md', false)
    expect(mockBackend.delete).not.toHaveBeenCalledWith('/buffer/notes.md', false)
    // The count reaches i18n as an interpolation param (the mocked `t` returns
    // the key with placeholders substituted).
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.clean_empty_done', { count: 3 })
    await waitFor(() => expect(useUndoToast.getState().toast?.message).toBeTruthy())
    expect(screen.queryByTestId('empty-cleanup-dialog')).toBeNull()
  })
})

describe('T4.2 — zero candidates', () => {
  it('shows a distinct "nothing to clean" message and NO dialog', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'notes.md', isDir: false, size: 12 },
      { name: 'more.md', isDir: false, size: 3 },
    ])
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    fireEvent.click(screen.getByTestId('toolbar-clean-empty'))

    await waitFor(() => expect(useUndoToast.getState().toast?.message).toBeTruthy())
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.clean_empty_none')
    expect(screen.queryByTestId('empty-cleanup-dialog')).toBeNull()
    expect(mockBackend.delete).not.toHaveBeenCalled()
  })
})

describe('T4.2 — delete failure (NON-atomic: partial deletion is accepted)', () => {
  it('surfaces the error and refreshes the tree; the already-deleted entries stay deleted', async () => {
    mixedTree()
    // Fail on the second path: the first one is already gone by then, and this
    // action makes no attempt to put it back (`deleteStorageEntries` has no
    // transaction — plan T4.2).
    let calls = 0
    mockBackend.delete.mockImplementation(async () => {
      calls += 1
      if (calls === 2) throw new Error('delete blew up')
      return undefined
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const listCallsBefore = mockBackend.list.mock.calls.length

    const dialog = await openCleanupDialog()
    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    // Error surfaced…
    expect(await screen.findByText('delete blew up')).toBeTruthy()
    // …the first delete DID happen (partial deletion is the accepted semantics)…
    expect(mockBackend.delete).toHaveBeenCalledTimes(2)
    // …and the tree was re-read so the pane stops showing what is already gone.
    await waitFor(() => expect(mockBackend.list.mock.calls.length).toBeGreaterThan(listCallsBefore))
    // No success toast for a failed run.
    expect(tSpy).not.toHaveBeenCalledWith('editor.buffers.clean_empty_done', expect.anything())
  })
})
