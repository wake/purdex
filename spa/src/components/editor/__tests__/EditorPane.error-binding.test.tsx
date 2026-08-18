// EditorPane — failures stay bound to the thing that produced them: a load error
// belongs to its own file, and a rename with no backend must say so rather than
// dismissing itself. Presentation is stubbed, so the assertions pin i18n keys.
import { act, fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  FILE,
  FILE_KEY,
  makeOtherPane,
  makePane,
  renderEditorPane,
  seedTab,
} from './editor-pane-stub-harness'
import {
  backendRef,
  readMock,
  renamePopover,
  resetBackend,
} from './editor-pane-stub-mocks'
import { Profiler } from 'react'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'

vi.mock('react-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-dom')>()
  const mocks = await import('./editor-pane-stub-mocks')
  return { ...actual, createPortal: mocks.inlinePortal }
})
vi.mock('../../../stores/useI18nStore', async () => ({
  useI18nStore: (await import('./editor-pane-stub-mocks')).useI18nStoreStub,
}))
vi.mock('../MonacoWrapper', async () => ({
  MonacoWrapper: (await import('./editor-pane-stub-mocks')).MonacoStub,
}))
vi.mock('../DiffView', async () => ({
  DiffView: (await import('./editor-pane-stub-mocks')).DiffViewStub,
}))
vi.mock('../EditorStatusBar', async () => ({
  EditorStatusBar: (await import('./editor-pane-stub-mocks')).EditorStatusBarStub,
}))
vi.mock('../TiptapEditor', async () => ({
  TiptapEditor: (await import('./editor-pane-stub-mocks')).TiptapStub,
}))
vi.mock('../../RenamePopover', async () => ({
  RenamePopover: (await import('./editor-pane-stub-mocks')).RenamePopoverStub,
}))
vi.mock('../../../lib/open-in-app-file', async () => {
  const mocks = await import('./editor-pane-stub-mocks')
  return { openInAppFile: (...args: unknown[]) => mocks.openInAppFileMock(...args) }
})
vi.mock('../../../lib/inapp-namer', async () => ({
  createUniqueInAppFile: (await import('./editor-pane-stub-mocks')).createUniqueInAppFileMock,
}))
vi.mock('../../../lib/fs-backend', async () => {
  const mocks = await import('./editor-pane-stub-mocks')
  return { getFsBackend: () => mocks.backendRef.value, registerFsBackend: vi.fn() }
})

// ---------------------------------------------------------------------------
// A load failure belongs to the file that produced it. The pane survives a file
// switch, so an unbound error paints over the NEXT file for a frame — with a
// Retry button that actually retries that next file.
// ---------------------------------------------------------------------------
describe('EditorPane — load error is bound to its own file', () => {
  beforeEach(() => {
    resetBackend()
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    seedTab(makePane())
  })

  it('does not paint the previous file error while the next file is loading', async () => {
    readMock.mockImplementation(async (path: string) => {
      if (path === FILE) throw new Error('read refused')
      // The next file never settles — the pane must show Loading, not the
      // leftover error surface.
      return new Promise<Uint8Array>(() => {})
    })

    // `Profiler.onRender` fires during commit, BEFORE passive effects run — it
    // is the only way to observe what the user actually sees in the frame
    // between the file switch and the load effect.
    const commits: string[] = []
    const onRender = () => commits.push(document.body.innerHTML)

    const { rerender } = render(
      <Profiler id="pane" onRender={onRender}>
        <EditorPane pane={makePane()} isActive={false} />
      </Profiler>,
    )
    await screen.findByTestId('editor-load-error')

    commits.length = 0
    rerender(
      <Profiler id="pane" onRender={onRender}>
        <EditorPane pane={makeOtherPane()} isActive={false} />
      </Profiler>,
    )

    expect(commits.length).toBeGreaterThan(0)
    expect(commits.some((html) => html.includes('editor-load-error'))).toBe(false)
    expect(screen.queryByTestId('editor-load-error')).toBeNull()
  })
})

describe('EditorPane — rename without a backend', () => {
  beforeEach(() => {
    resetBackend()
    renamePopover.props = null
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useEditorStore.getState().openBuffer(FILE_KEY, 'hello', { language: 'markdown' }, { mtime: 1, size: 5 })
    seedTab(makePane())
  })

  it('reports the missing backend instead of dismissing the rename silently', async () => {
    backendRef.value = undefined
    renderEditorPane(makePane())

    fireEvent.doubleClick(screen.getByRole('button', { name: 'note.md' }))
    await screen.findByTestId('rename-popover')
    await act(async () => {
      await renamePopover.props?.onConfirm('renamed.md')
    })

    expect(screen.getByTestId('rename-popover').textContent).toBe('editor.load_error.no_backend')
    // The buffer keeps its identity — nothing was renamed.
    expect(useEditorStore.getState().buffers[FILE_KEY]).toBeTruthy()
  })
})
