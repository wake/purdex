// A rename is not just a backend re-key: open panes must follow the path and
// the editor-store buffer must be re-keyed (with its metadata refreshed, or its
// manual language override preserved).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { mockBackend, renameEditorPanesSpy, tabStoreState } from './storage-pane-mocks'
import {
  makeEditorTab,
  makePane,
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

describe('StoragePane — rename syncs the tab layout and the editor store', () => {
  it('B2-16: rename syncs tab layout + editor store (v1.5 G1)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/foo.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = 'T1'
    const editorModule = await import('../../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/foo.md': makeEditorBuffer({
          content: 'hello',
          savedContent: '',
          isDirty: true,
          modelId: 'm-foo',
        }),
      },
      paneStates: {
        P1: makeEditorPaneState('inapp:/buffer/foo.md'),
      },
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'bar.md' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))

    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/foo.md', '/buffer/bar.md')
    })
    await waitFor(() => {
      const leaf = tabStoreState.tabs.T1.layout
      expect(leaf.type).toBe('leaf')
      if (leaf.type !== 'leaf') throw new Error('expected leaf')
      expect(leaf.pane.content).toMatchObject({
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: '/buffer/bar.md',
      })
    })
    const editorState = editorModule.useEditorStore.getState()
    expect(editorState.buffers['inapp:/buffer/bar.md']).toBeDefined()
    expect(editorState.buffers['inapp:/buffer/bar.md']?.isDirty).toBe(true)
    expect(editorState.buffers['inapp:/buffer/foo.md']).toBeUndefined()
    expect(editorState.paneStates.P1?.bufferKey).toBe('inapp:/buffer/bar.md')
    expect(renameEditorPanesSpy).toHaveBeenCalledWith(
      { type: 'inapp' },
      '/buffer/foo.md',
      '/buffer/bar.md',
    )
  })

  it('B2-16b: rename across extensions refreshes buffer metadata (v1.5 R6 MED)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/foo.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = 'T1'
    const editorModule = await import('../../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/foo.md': makeEditorBuffer({
          content: 'hello',
          savedContent: '',
          modelId: 'm-foo',
        }),
      },
      paneStates: {
        P1: makeEditorPaneState('inapp:/buffer/foo.md'),
      },
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'foo.ts' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))

    await waitFor(() => {
      expect(mockBackend.rename).toHaveBeenCalledWith('/buffer/foo.md', '/buffer/foo.ts')
    })
    await waitFor(() => {
      const next = editorModule.useEditorStore.getState().buffers['inapp:/buffer/foo.ts']
      expect(next).toBeDefined()
      expect(next?.language).toBe('typescript')
      expect(next?.languageSource).toBe('extension')
    })
  })

  it('B2-16c: rename preserves manual language override (v1.5 R6 MED)', async () => {
    mockBackend.list.mockResolvedValue([
      { name: 'foo.md', isDir: false, size: 10 },
    ] as FileEntry[])
    mockBackend.stat.mockRejectedValue(new Error('not found'))
    tabStoreState.tabs = {
      T1: makeEditorTab('T1', 'P1', '/buffer/foo.md', false),
    }
    tabStoreState.tabOrder = ['T1']
    tabStoreState.activeTabId = 'T1'
    const editorModule = await import('../../../../stores/useEditorStore')
    editorModule.useEditorStore.setState({
      buffers: {
        'inapp:/buffer/foo.md': makeEditorBuffer({
          content: 'hello',
          savedContent: '',
          modelId: 'm-foo',
          language: 'rust',
          languageSource: 'manual',
        }),
      },
      paneStates: {
        P1: makeEditorPaneState('inapp:/buffer/foo.md'),
      },
    })
    render(<StoragePane pane={makePane()} isActive />)
    const row = await screen.findByTestId('buffer-row')
    fireEvent.click(row)
    fireEvent.click(screen.getByTestId('toolbar-rename'))
    const input = await screen.findByTestId('rename-input')
    fireEvent.change(input, { target: { value: 'foo.ts' } })
    fireEvent.click(screen.getByTestId('rename-confirm'))

    await waitFor(() => {
      const next = editorModule.useEditorStore.getState().buffers['inapp:/buffer/foo.ts']
      expect(next).toBeDefined()
      expect(next?.language).toBe('rust')
      expect(next?.languageSource).toBe('manual')
    })
  })
})
