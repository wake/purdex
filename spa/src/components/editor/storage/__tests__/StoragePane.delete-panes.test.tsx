// Delete has to reconcile with what is already open: every pane on the target
// path closes BEFORE the backend delete, a locked tab refuses the delete
// outright, and a background tab still gets its editor-store state cleaned.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { eventLog, mockBackend, closePaneSpy, tabStoreState } from './storage-pane-mocks'
import {
  confirmBatchDelete,
  makeEditorTab,
  makePane,
  makePreviewTab,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals
} from './storage-pane-harness'
import {
  makeEditorBuffer,
  makeEditorPaneState
} from '../../../../stores/__tests__/editor-buffer-fixture'
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

describe('StoragePane — delete vs. open panes and locked tabs', () => {
  it('B2-10: delete closes each open editor pane BEFORE backend.delete', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makeEditorTab('TA', 'P1', '/buffer/x.md'),
      TB: makeEditorTab('TB', 'P2', '/buffer/x.md'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    await confirmBatchDelete()
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/x.md', false)
    })
    const firstDeleteIdx = eventLog.findIndex((e) => e.startsWith('delete:'))
    const closeIdxs = eventLog
      .map((e, i) => (e.startsWith('close:') ? i : -1))
      .filter((i) => i !== -1)
    expect(closeIdxs.length).toBe(2)
    for (const i of closeIdxs) {
      expect(i).toBeLessThan(firstDeleteIdx)
    }
    expect(closePaneSpy).toHaveBeenCalledWith('TA', 'P1')
    expect(closePaneSpy).toHaveBeenCalledWith('TB', 'P2')
  })

  it('B2-11: delete with locked tab is refused outright (v1.4 F2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = { TA: makeEditorTab('TA', 'P1', '/buffer/x.md', true) }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    await confirmBatchDelete()
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy()
    })
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
    expect(tabStoreState.tabs.TA).toBeDefined()
  })

  it('B2-11b: delete closes an open image-preview / pdf-preview pane before backend.delete', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'p.png', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makePreviewTab('TA', 'P1', '/buffer/p.png', 'image-preview'),
      TB: makePreviewTab('TB', 'P2', '/buffer/p.png', 'pdf-preview'),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    await confirmBatchDelete()
    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/p.png', false)
    })
    const firstDeleteIdx = eventLog.findIndex((e) => e.startsWith('delete:'))
    const closeIdxs = eventLog
      .map((e, i) => (e.startsWith('close:') ? i : -1))
      .filter((i) => i !== -1)
    expect(closeIdxs.length).toBe(2)
    for (const i of closeIdxs) {
      expect(i).toBeLessThan(firstDeleteIdx)
    }
    expect(closePaneSpy).toHaveBeenCalledWith('TA', 'P1')
    expect(closePaneSpy).toHaveBeenCalledWith('TB', 'P2')
  })

  it('B2-11c: delete refused when a preview pane sits in a locked tab (v1.4 F2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'p.png', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = { TA: makePreviewTab('TA', 'P1', '/buffer/p.png', 'image-preview', true) }
    tabStoreState.tabOrder = ['TA']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    await confirmBatchDelete()
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy()
    })
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
    expect(tabStoreState.tabs.TA).toBeDefined()
  })

  it('B2-13: multi-select delete refused when ANY target is in a locked tab (v1.4 F2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
      { name: 'y.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      TA: makeEditorTab('TA', 'P1', '/buffer/x.md', false),
      TB: makeEditorTab('TB', 'P2', '/buffer/y.md', true),
    }
    tabStoreState.tabOrder = ['TA', 'TB']
    tabStoreState.activeTabId = 'TA'
    render(<StoragePane pane={makePane()} isActive />)
    const rows = await screen.findAllByTestId('buffer-row')
    fireEvent.click(rows[0]) // x.md (plain click selects only x.md)
    // Modifier click adds y.md to the multi-selection (codex B5: plain click no
    // longer accretes — additive multi-select needs cmd/ctrl/shift).
    fireEvent.click(rows[1], { ctrlKey: true }) // + y.md
    await confirmBatchDelete()
    await waitFor(() => {
      expect(screen.getByText('editor.buffers.delete_locked_refused')).toBeTruthy()
    })
    expect(mockBackend.delete).not.toHaveBeenCalled()
    expect(closePaneSpy).not.toHaveBeenCalled()
  })

  it('B2-17: delete cleans editor store for background tabs (v1.5 G2)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'x.md', isDir: false, size: 10 },
    ] as FileEntry[])
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/x.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = null // background (not active)
    const editorModule = await import('../../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/x.md': makeEditorBuffer({
          content: 'unsaved',
          savedContent: '',
          isDirty: true,
          modelId: 'm-x',
        }),
      },
      paneStates: {
        P1: makeEditorPaneState('inapp:/buffer/x.md'),
      },
    })
    vi.stubGlobal('confirm', vi.fn(() => true))

    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    await confirmBatchDelete()

    await waitFor(() => {
      expect(mockBackend.delete).toHaveBeenCalledWith('/buffer/x.md', false)
    })
    const editorState = editorModule.useEditorStore.getState()
    expect(editorState.buffers['inapp:/buffer/x.md']).toBeUndefined()
    expect(editorState.paneStates.P1).toBeUndefined()
    expect(closePaneSpy).toHaveBeenCalledWith('T1', 'P1')
  })
})
