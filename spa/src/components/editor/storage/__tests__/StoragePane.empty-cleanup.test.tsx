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
import { closePaneSpy, mockBackend, tSpy, tabStoreState } from './storage-pane-mocks'
import {
  makeEditorTab,
  makePane,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals,
} from './storage-pane-harness'
import { makeEditorBuffer, makeEditorPaneState } from '../../../../stores/__tests__/editor-buffer-fixture'
import type { TreeNode } from '../../../../lib/storage-tree'
import type { FileStat } from '../../../../types/fs'

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

// --- Re-verification before the delete (the snapshot the dialog holds) -------
//
// The candidate list is computed from the tree as it looked when the dialog
// opened, and the dialog stays up for as long as the user wants. In that window
// the user can open one of those paths in another pane (or another tab, or
// another window) and save real content into it. The old delete path took the
// snapshot at its word — it only stat'd for `isDirectory` — so confirming
// deleted a file that now held the user's work.

/** Make `stat` report `size` for `fullPath` and 0 B for everything else. */
function statWithContent(fullPath: string, size: number): void {
  mockBackend.stat.mockImplementation(
    async (p: string): Promise<FileStat> => ({
      size: p === fullPath ? size : 0,
      mtime: 0,
      isDirectory: false,
      isFile: true,
    }),
  )
}

describe('T4.2 hardening — a candidate that gained content is re-checked and skipped', () => {
  it('does NOT delete a candidate that is no longer 0 B, and still deletes the rest', async () => {
    mixedTree()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()

    // While the dialog is up, the user writes into one of the listed paths.
    statWithContent('/buffer/Untitled-1.txt', 42)

    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledTimes(2))
    expect(mockBackend.delete).not.toHaveBeenCalledWith('/buffer/Untitled-1.txt', expect.anything())
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/Untitled.txt', false)
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/dir/nested-empty.md', false)
  })

  it('reports the skip in the toast (deleted vs. skipped), not a plain success count', async () => {
    mixedTree()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()
    statWithContent('/buffer/Untitled-1.txt', 42)

    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() => expect(useUndoToast.getState().toast?.message).toBeTruthy())
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.clean_empty_partial', {
      deleted: 2,
      skipped: 1,
    })
    expect(tSpy).not.toHaveBeenCalledWith('editor.buffers.clean_empty_done', expect.anything())
  })

  it('all candidates filled in → nothing deleted at all', async () => {
    mixedTree()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()
    mockBackend.stat.mockResolvedValue({
      size: 7,
      mtime: 0,
      isDirectory: false,
      isFile: true,
    } as FileStat)

    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() => expect(useUndoToast.getState().toast?.message).toBeTruthy())
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.clean_empty_partial', {
      deleted: 0,
      skipped: 3,
    })
  })
})

describe('T4.2 hardening — exactly one confirmation, and the real guards survive', () => {
  it('the generic window.confirm is never raised (our dialog IS the confirmation)', async () => {
    mixedTree()
    const confirmSpy = vi.fn(() => true)
    vi.stubGlobal('confirm', confirmSpy)
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()

    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledTimes(3))
    expect(confirmSpy).not.toHaveBeenCalled()
  })

  it('regression: a candidate open in a LOCKED tab still refuses the whole sweep', async () => {
    mixedTree()
    tabStoreState.tabs = {
      ...tabStoreState.tabs,
      TA: makeEditorTab('TA', 'P1', '/buffer/Untitled.txt', true),
    }
    tabStoreState.tabOrder = ['storageTab', 'TA']
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()

    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() =>
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy(),
    )
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
  })

  it('regression: a DIRTY candidate still raises the dirty confirm — cancelling deletes nothing', async () => {
    mixedTree()
    tabStoreState.tabs = {
      ...tabStoreState.tabs,
      TA: makeEditorTab('TA', 'P1', '/buffer/Untitled.txt'),
    }
    tabStoreState.tabOrder = ['storageTab', 'TA']
    const editorModule = await import('../../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/Untitled.txt': makeEditorBuffer({
          content: 'unsaved work',
          savedContent: '',
          isDirty: true,
        }),
      },
      paneStates: { P1: makeEditorPaneState('inapp:/buffer/Untitled.txt') },
    })
    const confirmSpy = vi.fn((_message?: string) => false)
    vi.stubGlobal('confirm', confirmSpy)

    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('notes.md')
    const dialog = await openCleanupDialog()

    fireEvent.click(within(dialog).getByTestId('empty-cleanup-confirm'))

    await waitFor(() => expect(confirmSpy).toHaveBeenCalledTimes(1))
    expect(confirmSpy.mock.calls[0][0]).toContain('delete_dirty_confirm')
    expect(mockBackend.delete).not.toHaveBeenCalled()
    editorModule.useEditorStore.setState({ buffers: {}, paneStates: {} })
  })
})
