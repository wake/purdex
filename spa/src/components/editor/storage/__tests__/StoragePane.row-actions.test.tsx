// T4.1 — per-row hover/focus action cluster (Open / Rename / Delete).
//
// The cluster acts on ITS OWN row, independent of the current selection, and
// must never leak its click/keydown up into the row's select / open handlers
// (`StorageRow` binds the whole row as a click hot zone).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { mockBackend } from './storage-pane-mocks'
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

/** Two sibling files, so "acts on THIS row, not the selected one" is testable. */
function twoFiles() {
  mockBackend.list.mockResolvedValue([
    { name: 'a.md', isDir: false, size: 10 },
    { name: 'b.md', isDir: false, size: 10 },
  ] as FileEntry[])
}

async function rowFor(name: string): Promise<HTMLElement> {
  const rows = await screen.findAllByTestId('buffer-row')
  const row = rows.find((r) => r.getAttribute('data-name') === name)
  if (!row) throw new Error(`row ${name} not rendered`)
  return row
}

describe('T4.1 — per-row action cluster', () => {
  it('renders Open / Rename / Delete on a FILE row', async () => {
    twoFiles()
    render(<StoragePane pane={makePane()} isActive />)
    const row = await rowFor('a.md')
    const actions = within(row).getByTestId('row-actions')
    expect(within(actions).getByTestId('row-action-open')).toBeTruthy()
    expect(within(actions).getByTestId('row-action-rename')).toBeTruthy()
    expect(within(actions).getByTestId('row-action-delete')).toBeTruthy()
  })

  it('a FOLDER row offers Rename / Delete but NOT Open', async () => {
    mockBackend.list.mockImplementation(
      pathAwareList(new Map([['/buffer/dir', { isDir: true, size: 0 }]])),
    )
    render(<StoragePane pane={makePane()} isActive />)
    const row = await rowFor('dir')
    const actions = within(row).getByTestId('row-actions')
    expect(within(actions).queryByTestId('row-action-open')).toBeNull()
    expect(within(actions).getByTestId('row-action-rename')).toBeTruthy()
    expect(within(actions).getByTestId('row-action-delete')).toBeTruthy()
  })

  it('the buttons are real, keyboard-reachable buttons revealed on focus-within (not hover-only)', async () => {
    twoFiles()
    render(<StoragePane pane={makePane()} isActive />)
    const row = await rowFor('a.md')
    const actions = within(row).getByTestId('row-actions')
    // Visibility is CSS-only in jsdom, so pin the contract: the cluster reveals
    // on group-hover AND group-focus-within, never `hidden`/`display:none`
    // (which would take the buttons out of the tab order entirely).
    expect(actions.className).toContain('group-focus-within:opacity-100')
    expect(actions.className).toContain('group-hover:opacity-100')
    expect(actions.className).not.toContain('hidden')
    for (const id of ['row-action-open', 'row-action-rename', 'row-action-delete']) {
      const btn = within(actions).getByTestId(id) as HTMLButtonElement
      expect(btn.tagName).toBe('BUTTON')
      expect(btn.getAttribute('tabindex')).toBeNull()
      expect(btn.disabled).toBe(false)
    }
  })

  it('Open acts on ITS row even when a different row is selected', async () => {
    twoFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await rowFor('a.md')) // select a.md
    fireEvent.click(within(await rowFor('b.md')).getByTestId('row-action-open'))
    await waitFor(() => {
      expect(openInAppFile as unknown as Mock).toHaveBeenCalledWith('/buffer/b.md', 'ws1')
    })
  })

  it('Delete acts on ITS row even when a different row is selected', async () => {
    twoFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await rowFor('a.md')) // select a.md
    fireEvent.click(within(await rowFor('b.md')).getByTestId('row-action-delete'))
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/b.md', false)
    })
    expect(mockBackend.delete).toHaveBeenCalledTimes(1)
  })

  it('Rename opens the popover on ITS row (anchored there) and renames only that entry', async () => {
    twoFiles()
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await rowFor('a.md')) // select a.md
    fireEvent.click(within(await rowFor('b.md')).getByTestId('row-action-rename'))
    const input = (await screen.findByTestId('rename-input')) as HTMLInputElement
    // Popover targets b.md (the hovered row), not the selected a.md.
    expect(input.value).toBe('b.md')
    fireEvent.change(input, { target: { value: 'c.md' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))
    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/b.md', '/buffer/c.md')
    })
    expect(mockBackend.rename).toHaveBeenCalledTimes(1)
  })

  it('REGRESSION: clicking an action button does not fire the row select handler', async () => {
    twoFiles()
    render(<StoragePane pane={makePane()} isActive />)
    fireEvent.click(await rowFor('a.md'))
    expect((await rowFor('a.md')).getAttribute('aria-selected')).toBe('true')

    // Open on b.md — a.md must stay the selection, b.md must not become one.
    fireEvent.click(within(await rowFor('b.md')).getByTestId('row-action-open'))
    await waitFor(() => {
      expect(openInAppFile as unknown as Mock).toHaveBeenCalled()
    })
    expect((await rowFor('a.md')).getAttribute('aria-selected')).toBe('true')
    expect((await rowFor('b.md')).getAttribute('aria-selected')).toBe('false')

    // Rename on b.md — same: the popover opens without moving the selection.
    fireEvent.click(within(await rowFor('b.md')).getByTestId('row-action-rename'))
    await screen.findByTestId('rename-input')
    expect((await rowFor('a.md')).getAttribute('aria-selected')).toBe('true')
    expect((await rowFor('b.md')).getAttribute('aria-selected')).toBe('false')
  })

  it('REGRESSION: keyboard-activating an action button does not fire the row keydown handler', async () => {
    twoFiles()
    render(<StoragePane pane={makePane()} isActive />)
    const row = await rowFor('b.md')
    // Enter on the row itself opens the file; Enter on a nested action button
    // must not reach that handler.
    fireEvent.keyDown(within(row).getByTestId('row-action-rename'), { key: 'Enter' })
    expect(openInAppFile as unknown as Mock).not.toHaveBeenCalled()
    // Space on the row selects; from inside the cluster it must not.
    fireEvent.keyDown(within(row).getByTestId('row-action-delete'), { key: ' ' })
    expect((await rowFor('b.md')).getAttribute('aria-selected')).toBe('false')
  })
})
