// EditorPane — what a save attempt REPORTS. The write decides the outcome; the
// post-write stat only refreshes lastStat, so a stat failure must not claim the
// save failed. Presentation is stubbed, so the assertions pin i18n keys.
import { act, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clickSave,
  FILE,
  FILE_KEY,
  makePane,
  makeUntitledPane,
  notFound,
  renderEditorPane,
  saveUntitledAs,
  seedTab,
  seedUntitledBuffer,
  TARGET_KEY,
  TARGET_PATH,
  toastMessage,
  UNTITLED_KEY,
} from './editor-pane-stub-harness'
import {
  renamePopover,
  resetBackend,
  statMock,
  writeMock,
} from './editor-pane-stub-mocks'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'

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
// Untitled first save: naming a brand-new document must not silently overwrite
// a file that already sits at the target path.
// ---------------------------------------------------------------------------
describe('EditorPane — untitled first save', () => {
  beforeEach(() => {
    resetBackend()
    renamePopover.props = null
    useUndoToast.setState({ toast: null })
    useRecentFilesStore.setState({ files: [] })
    seedUntitledBuffer()
    seedTab(makeUntitledPane())
  })

  it('refuses to write when a file already exists at the chosen name', async () => {
    // The target exists on the backend but is NOT open in any pane, so the
    // in-store buffer check cannot see it.
    statMock.mockResolvedValue({ mtime: 111, size: 222, isFile: true, isDirectory: false })
    renderEditorPane(makeUntitledPane())

    await saveUntitledAs('report.md')

    expect(statMock).toHaveBeenCalledWith(TARGET_PATH)
    expect(writeMock).not.toHaveBeenCalled()
    expect(screen.getByTestId('rename-popover').textContent).toBe('File already exists')
    // The untitled buffer stays exactly where it was — nothing was renamed.
    expect(useEditorStore.getState().buffers[UNTITLED_KEY]).toBeTruthy()
    expect(useEditorStore.getState().buffers[TARGET_KEY]).toBeUndefined()
  })

  it('reports saved even when the post-write stat fails (the bytes did land)', async () => {
    // The write succeeds; every stat fails (host just went away / flaky link).
    // The file IS on disk, so the save must not be reported as a failure.
    statMock.mockRejectedValue(notFound())
    renderEditorPane(makeUntitledPane())

    await saveUntitledAs('report.md')

    expect(writeMock).toHaveBeenCalledTimes(1)
    expect(toastMessage()).toBe('editor.save.saved')
    const saved = useEditorStore.getState().buffers[TARGET_KEY]
    expect(saved).toBeTruthy()
    expect(saved.isDirty).toBe(false)
    expect(saved.untitled).toBeUndefined()
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toContain(TARGET_PATH)
  })

  it('reports failed when the write itself rejects (regression)', async () => {
    statMock.mockRejectedValue(notFound())
    writeMock.mockRejectedValue(new Error('disk full'))
    renderEditorPane(makeUntitledPane())

    await saveUntitledAs('report.md')

    expect(toastMessage()).toBe('editor.save.failed')
    // Nothing moved: the document is still the unsaved untitled buffer.
    expect(useEditorStore.getState().buffers[UNTITLED_KEY]).toBeTruthy()
    expect(useEditorStore.getState().buffers[TARGET_KEY]).toBeUndefined()
  })

  it('writes normally when nothing occupies the chosen name', async () => {
    // Physical model: the target does not exist until we write it.
    statMock.mockImplementation(async () => {
      if (writeMock.mock.calls.length === 0) throw notFound()
      return { mtime: 5, size: 10, isFile: true, isDirectory: false }
    })
    renderEditorPane(makeUntitledPane())

    await saveUntitledAs('report.md')

    expect(writeMock).toHaveBeenCalledTimes(1)
    expect(writeMock.mock.calls[0][0]).toBe(TARGET_PATH)
    expect(useEditorStore.getState().buffers[TARGET_KEY]).toBeTruthy()
    expect(toastMessage()).toBe('editor.save.saved')
  })
})

// ---------------------------------------------------------------------------
// Regular file save: the write decides the outcome. `stat` only refreshes
// `lastStat`, so a stat failure must not claim the save failed while the bytes
// are already on disk (and leave the buffer dirty).
// ---------------------------------------------------------------------------
describe('EditorPane — regular file save outcome', () => {
  beforeEach(() => {
    resetBackend()
    useUndoToast.setState({ toast: null })
    useRecentFilesStore.setState({ files: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useEditorStore.getState().openBuffer(FILE_KEY, 'hello', { language: 'markdown' }, { mtime: 1, size: 5 })
    useEditorStore.getState().updateContent(FILE_KEY, 'hello there')
    seedTab(makePane())
  })

  it('reports saved and clears dirty even when the post-write stat fails', async () => {
    statMock.mockRejectedValue(new Error('host unreachable'))
    renderEditorPane(makePane())

    await act(async () => {
      clickSave()
    })

    expect(writeMock).toHaveBeenCalledWith(FILE, new TextEncoder().encode('hello there'))
    expect(toastMessage()).toBe('editor.save.saved')
    const buf = useEditorStore.getState().buffers[FILE_KEY]
    expect(buf.isDirty).toBe(false)
    expect(buf.savedContent).toBe('hello there')
    // No fresh stat was available, so the previous one is kept rather than lost.
    expect(buf.lastStat).toEqual({ mtime: 1, size: 5 })
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toContain(FILE)
  })

  it('reports failed and stays dirty when the write rejects (regression)', async () => {
    writeMock.mockRejectedValue(new Error('read-only volume'))
    renderEditorPane(makePane())

    await act(async () => {
      clickSave()
    })

    expect(toastMessage()).toBe('editor.save.failed')
    expect(useEditorStore.getState().buffers[FILE_KEY].isDirty).toBe(true)
  })

  it('refreshes lastStat when the post-write stat succeeds', async () => {
    statMock.mockResolvedValue({ mtime: 99, size: 11, isFile: true, isDirectory: false })
    renderEditorPane(makePane())

    await act(async () => {
      clickSave()
    })

    expect(toastMessage()).toBe('editor.save.saved')
    expect(useEditorStore.getState().buffers[FILE_KEY].lastStat).toEqual({ mtime: 99, size: 11 })
  })
})
