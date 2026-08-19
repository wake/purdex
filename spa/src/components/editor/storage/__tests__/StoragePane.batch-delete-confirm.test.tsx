// Batch delete confirms against the LIVE tree, and says what it is about to
// delete.
//
// `selected` is a set of path STRINGS captured when the user clicked. It does
// not track the tree: between the click and the delete a path can be emptied out
// and re-created by something else, and the old confirmation ("Delete 3
// buffer(s)?") gave the user no way to notice. Two layers close most of that:
//
//   1. The confirmation lists every path, so the user reads what will go instead
//      of a bare count.
//   2. The selection is re-verified against the backend first, and paths that no
//      longer exist are pruned out of both the selection and the dialog — so what
//      is listed IS what will be deleted.
//
// What this deliberately does NOT solve is ABA: the same path holding a
// different file. The IDB backend keys entries BY PATH and exposes no file
// identity to compare, so detecting that needs a backend API change. Out of
// scope here; tracked separately.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { closePaneSpy, mockBackend, tSpy, tabStoreState } from './storage-pane-mocks'
import {
  makeEditorTab,
  makePane,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals,
} from './storage-pane-harness'
import { makeEditorBuffer, makeEditorPaneState } from '../../../../stores/__tests__/editor-buffer-fixture'
import type { FileEntry, FileStat } from '../../../../types/fs'

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

async function selectRows(...names: string[]): Promise<void> {
  for (const name of names) {
    fireEvent.click(within(await rowFor(name)).getByTestId('row-checkbox'))
  }
}

/** Every path the confirmation dialog lists, sorted. */
function listedPaths(dialog: HTMLElement): string[] {
  return within(dialog)
    .queryAllByTestId('delete-selection-item')
    .map((el) => el.textContent ?? '')
    .sort()
}

/** `stat` resolves for everything except `missing`, which rejects (gone). */
function statWithMissing(...missing: string[]): void {
  mockBackend.stat.mockImplementation(async (p: string): Promise<FileStat> => {
    if (missing.includes(p)) throw new Error('not found')
    return { size: 10, mtime: 0, isDirectory: false, isFile: true }
  })
}

describe('batch delete — the confirmation names every path', () => {
  it('lists the actual selected paths instead of only a count', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')

    fireEvent.click(screen.getByTestId('toolbar-delete'))

    const dialog = await screen.findByTestId('delete-selection-dialog')
    expect(listedPaths(dialog)).toEqual(['/buffer/a.md', '/buffer/c.md'])
    // Nothing is deleted until the dialog is answered.
    expect(mockBackend.delete).not.toHaveBeenCalled()
  })

  it('cancelling deletes nothing and keeps the selection', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    const dialog = await screen.findByTestId('delete-selection-dialog')

    fireEvent.click(within(dialog).getByTestId('delete-selection-cancel'))

    await waitFor(() => expect(screen.queryByTestId('delete-selection-dialog')).toBeNull())
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(screen.queryByTestId('selection-action-bar')).toBeTruthy()
  })

  it('regression: confirming still deletes exactly the selected entries', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    const dialog = await screen.findByTestId('delete-selection-dialog')

    fireEvent.click(within(dialog).getByTestId('delete-selection-confirm'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledTimes(2))
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/a.md', false)
    expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/c.md', false)
    expect(mockBackend.delete).not.toHaveBeenCalledWith('/buffer/b.md', false)
  })

  it('is the ONLY confirmation — the generic window.confirm is not raised', async () => {
    threeFiles()
    const confirmSpy = vi.fn(() => true)
    vi.stubGlobal('confirm', confirmSpy)
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')
    fireEvent.click(screen.getByTestId('toolbar-delete'))

    fireEvent.click(
      within(await screen.findByTestId('delete-selection-dialog')).getByTestId(
        'delete-selection-confirm',
      ),
    )

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledTimes(2))
    expect(confirmSpy).not.toHaveBeenCalled()
  })

  it('the action bar Delete opens the same dialog', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'b.md')

    fireEvent.click(
      within(await screen.findByTestId('selection-action-bar')).getByTestId('selection-delete'),
    )

    const dialog = await screen.findByTestId('delete-selection-dialog')
    expect(listedPaths(dialog)).toEqual(['/buffer/a.md', '/buffer/b.md'])
  })
})

describe('batch delete — the selection is re-verified before it is confirmed', () => {
  it('a selected path that no longer exists is pruned: not listed, not deleted', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'b.md', 'c.md')
    // `b.md` disappears while the user is deciding.
    statWithMissing('/buffer/b.md')

    fireEvent.click(screen.getByTestId('toolbar-delete'))

    const dialog = await screen.findByTestId('delete-selection-dialog')
    expect(listedPaths(dialog)).toEqual(['/buffer/a.md', '/buffer/c.md'])

    fireEvent.click(within(dialog).getByTestId('delete-selection-confirm'))

    await waitFor(() => expect(mockBackend.delete).toHaveBeenCalledTimes(2))
    expect(mockBackend.delete).not.toHaveBeenCalledWith('/buffer/b.md', expect.anything())
  })

  it('says so when the list shrank, instead of silently showing fewer paths', async () => {
    threeFiles()
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'b.md', 'c.md')
    statWithMissing('/buffer/b.md')

    fireEvent.click(screen.getByTestId('toolbar-delete'))

    await screen.findByTestId('delete-selection-dialog')
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.delete_pruned_note', { count: 1 })
  })

  it('every selected path gone → no dialog, no delete, an explicit message', async () => {
    threeFiles()
    const { useUndoToast } = await import('../../../../stores/useUndoToast')
    useUndoToast.setState({ toast: null })
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')
    statWithMissing('/buffer/a.md', '/buffer/c.md')

    fireEvent.click(screen.getByTestId('toolbar-delete'))

    await waitFor(() => expect(useUndoToast.getState().toast?.message).toBeTruthy())
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.delete_all_gone')
    expect(screen.queryByTestId('delete-selection-dialog')).toBeNull()
    expect(mockBackend.delete).not.toHaveBeenCalled()
  })
})

describe('batch delete — the real guards still stand behind the dialog', () => {
  it('regression: a target in a LOCKED tab is still refused after confirming', async () => {
    threeFiles()
    tabStoreState.tabs = {
      ...tabStoreState.tabs,
      TA: makeEditorTab('TA', 'P1', '/buffer/a.md', true),
    }
    tabStoreState.tabOrder = ['storageTab', 'TA']
    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')
    fireEvent.click(screen.getByTestId('toolbar-delete'))

    fireEvent.click(
      within(await screen.findByTestId('delete-selection-dialog')).getByTestId(
        'delete-selection-confirm',
      ),
    )

    await waitFor(() =>
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy(),
    )
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
  })

  it('regression: a DIRTY target still raises the dirty confirm after our dialog', async () => {
    threeFiles()
    tabStoreState.tabs = {
      ...tabStoreState.tabs,
      TA: makeEditorTab('TA', 'P1', '/buffer/a.md'),
    }
    tabStoreState.tabOrder = ['storageTab', 'TA']
    const editorModule = await import('../../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/a.md': makeEditorBuffer({
          content: 'unsaved work',
          savedContent: '',
          isDirty: true,
        }),
      },
      paneStates: { P1: makeEditorPaneState('inapp:/buffer/a.md') },
    })
    const confirmSpy = vi.fn((_message?: string) => false)
    vi.stubGlobal('confirm', confirmSpy)

    render(<StoragePane pane={makePane()} isActive />)
    await selectRows('a.md', 'c.md')
    fireEvent.click(screen.getByTestId('toolbar-delete'))
    fireEvent.click(
      within(await screen.findByTestId('delete-selection-dialog')).getByTestId(
        'delete-selection-confirm',
      ),
    )

    await waitFor(() => expect(confirmSpy).toHaveBeenCalledTimes(1))
    expect(confirmSpy.mock.calls[0][0]).toContain('delete_dirty_confirm')
    expect(mockBackend.delete).not.toHaveBeenCalled()
    editorModule.useEditorStore.setState({ buffers: {}, paneStates: {} })
  })
})
