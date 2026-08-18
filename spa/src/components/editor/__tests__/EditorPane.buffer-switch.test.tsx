// EditorPane — the breadcrumb quick-switch (T6) and the language default a fresh
// buffer opens in. Presentation is stubbed (see editor-pane-stub-mocks.tsx).
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  FILE,
  makePane,
  renderPane,
  seedLoadedBuffer,
  seedTab,
  WS_ID,
} from './editor-pane-stub-harness'
import {
  createUniqueInAppFileMock,
  listMock,
  openInAppFileMock,
  resetBackend,
} from './editor-pane-stub-mocks'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { bufferKey } from '../../../lib/editor-buffer-key'

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

describe('EditorPane — breadcrumb quick-switch (T6)', () => {
  beforeEach(() => {
    openInAppFileMock.mockReset()
    resetBackend()
    listMock.mockImplementation(async (path: string) => {
      if (path === '/buffer') {
        return [
          { name: 'dir', isDir: true, size: 0 },
          { name: 'note.md', isDir: false, size: 0 },
          { name: 'root.md', isDir: false, size: 0 },
        ]
      }
      if (path === '/buffer/dir') {
        return [{ name: 'pic.png', isDir: false, size: 0 }]
      }
      return []
    })

    // Seed a loaded (non-dirty) buffer so EditorPaneInner renders past its
    // Loading guard and the load effect early-returns.
    seedLoadedBuffer()

    // Seed a tab containing the pane, owned by a workspace.
    seedTab(makePane())
  })

  it('T6-3: switching to a nested .png routes through openInAppFile with full path + workspace id (not setPaneContent)', async () => {
    const setPaneContentSpy = vi.spyOn(useTabStore.getState(), 'setPaneContent')
    renderPane()

    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const pngButton = await waitFor(() => screen.getByRole('button', { name: 'dir/pic.png' }))
    fireEvent.click(pngButton)

    expect(openInAppFileMock).toHaveBeenCalledTimes(1)
    expect(openInAppFileMock).toHaveBeenCalledWith('/buffer/dir/pic.png', WS_ID)
    // The old hardcoded `{ kind: 'editor' }` swap must be gone.
    expect(setPaneContentSpy).not.toHaveBeenCalled()
  })

  it('T6-4: switching to a root-level .md still routes through openInAppFile', async () => {
    renderPane()

    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const mdButton = await waitFor(() => screen.getByRole('button', { name: /^root\.md$/ }))
    fireEvent.click(mdButton)

    expect(openInAppFileMock).toHaveBeenCalledTimes(1)
    expect(openInAppFileMock).toHaveBeenCalledWith('/buffer/root.md', WS_ID)
  })

  // D2 (AC-1b clause 6): the breadcrumb "new buffer" reserves through the atomic
  // namer; two reservations get distinct real paths — never a shared key.
  it('D2: new buffer routes through createUniqueInAppFile with distinct paths (no shared key)', async () => {
    // Empty buffer dir → the popover renders its "new buffer" affordance.
    listMock.mockResolvedValue([])
    createUniqueInAppFileMock
      .mockResolvedValueOnce('/buffer/Untitled.md')
      .mockResolvedValueOnce('/buffer/Untitled-1.md')
    const setPaneContentSpy = vi
      .spyOn(useTabStore.getState(), 'setPaneContent')
      .mockImplementation(() => {})
    renderPane()

    // First reservation.
    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const firstBtn = await waitFor(() => screen.getByTestId('breadcrumb-popover-new-buffer'))
    fireEvent.click(firstBtn)
    await waitFor(() => expect(setPaneContentSpy).toHaveBeenCalledTimes(1))

    // Second reservation (reopen the popover; the first dismissed it).
    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const secondBtn = await waitFor(() => screen.getByTestId('breadcrumb-popover-new-buffer'))
    fireEvent.click(secondBtn)
    await waitFor(() => expect(setPaneContentSpy).toHaveBeenCalledTimes(2))

    expect(createUniqueInAppFileMock).toHaveBeenCalledWith('/buffer', 'md')
    const filePaths = setPaneContentSpy.mock.calls.map((c) => (c[2] as { filePath: string }).filePath)
    expect(filePaths).toEqual(['/buffer/Untitled.md', '/buffer/Untitled-1.md'])
    expect(new Set(filePaths).size).toBe(2)
  })

  it('opens a markdown file in Live Mode (wysiwyg) by default', async () => {
    // beforeEach seeds a markdown buffer at FILE. With no explicit user mode
    // choice (paneState.editorMode null), markdown must resolve to wysiwyg.
    renderPane()
    expect(await screen.findByTestId('tiptap')).toBeTruthy()
    expect(screen.queryByTestId('monaco')).toBeNull()
  })

  it('opens a non-markdown file in raw (Monaco) by default', async () => {
    // Same path, but the buffer language is plaintext → default resolves to raw.
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useEditorStore.getState().openBuffer(bufferKey({ type: 'inapp' }, FILE), 'plain', {
      language: 'plaintext',
    })
    renderPane()
    expect(await screen.findByTestId('monaco')).toBeTruthy()
    expect(screen.queryByTestId('tiptap')).toBeNull()
  })
})
