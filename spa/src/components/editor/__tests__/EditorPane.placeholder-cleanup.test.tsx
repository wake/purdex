// T5.2 — the automatic placeholder sweep seen from its real call site: the
// EditorPane unmount cleanup.
//
// `EditorPane`'s cleanup fires on ANY unmount of that leaf, which is why the
// unmount alone must never authorize a delete. The load-bearing case here is a
// PANE MOVE: `moveTabContentIntoPane` attaches the destination pane to the
// buffer BEFORE the source tab is retired, so at the post-close check the
// buffer still has a live reference and the file survives. This suite drives
// that through the real mover, not through a hand-made unmount.
import { act, render, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import {
  createBackend,
  createPane,
  getBufferKey,
  registerTabPane,
  renderEditorPane,
  resetEditorPaneStores,
} from './editor-pane-harness'
import { getFsBackendMock } from './editor-pane-mocks'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { usePlaceholderFilesStore } from '../../../stores/usePlaceholderFilesStore'
import { moveTabContentIntoPane } from '../../../lib/pane-move'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { createTab, type Pane, type PaneContent } from '../../../types/tab'

vi.mock('../../../lib/fs-backend', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/fs-backend')>()
  const mocks = await import('./editor-pane-mocks')
  mocks.fsBackendActual.current = actual
  return { ...actual, getFsBackend: mocks.getFsBackendMock }
})
vi.mock('../MonacoWrapper', async () => ({
  MonacoWrapper: (await import('./editor-pane-mocks')).MonacoWrapperStub,
}))
vi.mock('../DiffView', async () => ({
  DiffView: (await import('./editor-pane-mocks')).DiffViewStub,
}))
vi.mock('../EditorStatusBar', async () => ({
  EditorStatusBar: (await import('./editor-pane-mocks')).EditorStatusBarStub,
}))
vi.mock('../TiptapEditor', async () => ({
  TiptapEditor: (await import('./editor-pane-mocks')).TiptapEditorStub,
}))

const INAPP = { type: 'inapp' } as const
const PLACEHOLDER = '/buffer/Untitled.md'
const KEY = getBufferKey(PLACEHOLDER)

type TestBackend = ReturnType<typeof createBackend> & { delete: ReturnType<typeof vi.fn> }

function seedBackend(): TestBackend {
  const backend = createBackend() as TestBackend
  backend.read.mockResolvedValue(new TextEncoder().encode(''))
  backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 0, mtime: 1 })
  backend.delete.mockResolvedValue(undefined)
  getFsBackendMock.mockReturnValue(backend)
  return backend
}

async function waitForBuffer() {
  await waitFor(() => {
    expect(useEditorStore.getState().buffers[KEY]).toBeDefined()
  })
}

beforeEach(() => {
  resetEditorPaneStores()
  useWorkspaceStore.getState().reset()
  usePlaceholderFilesStore.setState({ paths: [PLACEHOLDER] })
})

describe('T5.2 — closing an untouched placeholder pane', () => {
  it('deletes the file and clears the registry entry', async () => {
    const backend = seedBackend()
    const pane = createPane(PLACEHOLDER, 'pane-ph-close')
    registerTabPane(pane)
    const view = renderEditorPane(pane)
    await waitForBuffer()

    // The pane is gone from the tab tree first — that is what makes React
    // unmount the component in the app.
    act(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })
    act(() => {
      view.unmount()
    })

    // The delete is gated on a live `stat` confirming the file is still 0 B, so
    // it lands a tick after the unmount.
    await waitFor(() => expect(backend.delete).toHaveBeenCalledWith(PLACEHOLDER))
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
    expect(useEditorStore.getState().buffers[KEY]).toBeUndefined()
  })

  it('leaves an ordinary (unregistered) file alone', async () => {
    usePlaceholderFilesStore.setState({ paths: [] })
    const backend = seedBackend()
    const pane = createPane(PLACEHOLDER, 'pane-ph-plain')
    registerTabPane(pane)
    const view = renderEditorPane(pane)
    await waitForBuffer()

    act(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })
    act(() => {
      view.unmount()
    })

    expect(backend.delete).not.toHaveBeenCalled()
  })

  it('leaves the file alone when the pane is still showing it (tab switch, not a close)', async () => {
    const backend = seedBackend()
    const pane = createPane(PLACEHOLDER, 'pane-ph-hidden')
    registerTabPane(pane)
    const view = renderEditorPane(pane)
    await waitForBuffer()

    // The tab tree still holds this pane on this file → not a close.
    act(() => {
      view.unmount()
    })

    expect(backend.delete).not.toHaveBeenCalled()
    expect(usePlaceholderFilesStore.getState().paths).toEqual([PLACEHOLDER])
  })

  it('a re-render with a new-but-equal source object is not a close', async () => {
    // Regression guard for the cleanup effect's dependency list: `source` is an
    // object, so listing it would make every re-render that mints a fresh
    // (equal) source tear the pane down — closing the buffer and, here, deleting
    // the file. `sourceIdentity` covers the only change that matters.
    const backend = seedBackend()
    const pane = createPane(PLACEHOLDER, 'pane-ph-rerender')
    const view = renderEditorPane(pane)
    await waitForBuffer()

    view.rerender(<EditorPane pane={createPane(PLACEHOLDER, 'pane-ph-rerender')} isActive />)

    expect(backend.delete).not.toHaveBeenCalled()
    expect(useEditorStore.getState().buffers[KEY]).toBeDefined()
    expect(usePlaceholderFilesStore.getState().paths).toEqual([PLACEHOLDER])
  })
})

describe('T5.2 — a pane move never deletes the moved placeholder', () => {
  function seedMove(): { sourcePane: Pane; sourceTabId: string; targetTabId: string; targetPaneId: string } {
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    const content: PaneContent = { kind: 'editor', source: INAPP, filePath: PLACEHOLDER }
    const sourceTab = createTab(content)
    const targetTab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(sourceTab)
    useTabStore.getState().addTab(targetTab)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, sourceTab.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, targetTab.id)

    return {
      sourcePane: getPrimaryPane(useTabStore.getState().tabs[sourceTab.id].layout),
      sourceTabId: sourceTab.id,
      targetTabId: targetTab.id,
      targetPaneId: getPrimaryPane(useTabStore.getState().tabs[targetTab.id].layout).id,
    }
  }

  it('survives the move, then is swept when its LAST pane finally closes', async () => {
    const backend = seedBackend()
    const { sourcePane, sourceTabId, targetTabId, targetPaneId } = seedMove()
    const sourceView = render(<EditorPane pane={sourcePane} isActive />)
    await waitForBuffer()

    let moved = false
    act(() => {
      moved = moveTabContentIntoPane(sourceTabId, targetTabId, targetPaneId)
    })
    expect(moved).toBe(true)

    // The source leaf unmounts as part of the move commit. The destination pane
    // was pre-bound to the buffer, so the placeholder is still open.
    act(() => {
      sourceView.unmount()
    })

    expect(backend.delete).not.toHaveBeenCalled()
    expect(usePlaceholderFilesStore.getState().paths).toEqual([PLACEHOLDER])
    expect(useEditorStore.getState().buffers[KEY]).toBeDefined()
    expect(useEditorStore.getState().paneStates[targetPaneId]?.bufferKey).toBe(KEY)

    // Now close the destination for real — the last reference goes with it.
    const targetPane = getPrimaryPane(useTabStore.getState().tabs[targetTabId].layout)
    const targetView = render(<EditorPane pane={targetPane} isActive />)
    await waitForBuffer()
    act(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })
    act(() => {
      targetView.unmount()
    })

    await waitFor(() => expect(backend.delete).toHaveBeenCalledWith(PLACEHOLDER))
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })
})
