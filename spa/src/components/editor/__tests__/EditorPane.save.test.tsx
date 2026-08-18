// EditorPane — getting the buffer back onto disk: the ordinary save, the untitled
// first save, canSave semantics (T1.3), the outcome toast (T3.3) and the keyboard
// path into the naming popover.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createBackend,
  createPane,
  createUntitledPane,
  getBufferKey,
  pressCmdS,
  registerTabPane,
  renderEditorPane,
  renderLoaded,
  renderUntitled,
  resetEditorPaneStores,
  statMissingUntilWritten,
} from './editor-pane-harness'
import { getFsBackendMock } from './editor-pane-mocks'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import type { Pane } from '../../../types/tab'

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

describe('EditorPane', () => {
  beforeEach(resetEditorPaneStores)

  it('prompts for a file name before first save of an unrenamed untitled document', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-save-prompt')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    expect(screen.getByDisplayValue('Untitled.md')).toBeInTheDocument()
    expect(backend.write).not.toHaveBeenCalled()
  })

  it('saves an untitled document to in-app after confirming the suggested name', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-save-untitled')
    const backend = createBackend()
    backend.write.mockResolvedValue(undefined)
    statMissingUntilWritten(backend, { size: 0, mtime: 456 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))
    fireEvent.keyDown(screen.getByDisplayValue('Untitled.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/buffer/Untitled.md', new TextEncoder().encode(''))
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/buffer/Untitled.md')]).toMatchObject({
      lastStat: { mtime: 456, size: 0 },
      untitled: undefined,
    })
  })

  it('saves a renamed untitled document directly to in-app without prompting', async () => {
    const pane = createUntitledPane('notes.txt', '.txt', 'pane-save-renamed')
    const backend = createBackend()
    backend.write.mockResolvedValue(undefined)
    statMissingUntilWritten(backend, { size: 5, mtime: 456 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane({
      ...pane,
      content: {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'untitled:notes.txt',
        untitled: {
          name: 'notes.txt',
          suggestedExtension: '.txt',
          hasBeenRenamed: true,
        },
      },
    })
    useEditorStore.getState().openBuffer(getBufferKey('untitled:notes.txt'), 'hello', {
      language: 'plaintext',
      languageSource: 'extension',
      untitled: {
        name: 'notes.txt',
        suggestedExtension: '.txt',
        hasBeenRenamed: true,
      },
    })

    render(<EditorPane pane={{
      ...pane,
      content: {
        kind: 'editor',
        source: { type: 'inapp' },
        filePath: 'untitled:notes.txt',
        untitled: {
          name: 'notes.txt',
          suggestedExtension: '.txt',
          hasBeenRenamed: true,
        },
      },
    }} isActive />)

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/buffer/notes.txt', new TextEncoder().encode('hello'))
    })

    expect(screen.queryByDisplayValue('notes.txt')).not.toBeInTheDocument()
  })

  it('saves dirty content and marks the buffer clean on success', async () => {
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.write.mockResolvedValue(undefined)
    backend.stat
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 11,
        mtime: 123,
      })
      .mockResolvedValueOnce({
        isFile: true,
        isDirectory: false,
        size: 15,
        mtime: 456,
      })
    getFsBackendMock.mockReturnValue(backend)

    renderEditorPane(createPane('/notes/save.md'))

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/save.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/save.md'), 'changed content')
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/notes/save.md', new TextEncoder().encode('changed content'))
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/save.md')]).toMatchObject({
      content: 'changed content',
      savedContent: 'changed content',
      isDirty: false,
      lastStat: { mtime: 456, size: 15 },
    })

    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toContain('/notes/save.md')
  })

  it('keeps the buffer dirty when save fails', async () => {
    const backend = createBackend()
    // T3.3: the failure is reported through the toast now — the console.error the
    // user could never see is gone, so the toast is what this pins.
    useUndoToast.setState({ toast: null })
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.write.mockRejectedValue(new Error('disk full'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    renderEditorPane(createPane('/notes/save-fail.md'))

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/save-fail.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/save-fail.md'), 'changed content')
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe('Save failed: disk full')
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/save-fail.md')]).toMatchObject({
      content: 'changed content',
      savedContent: 'hello world',
      isDirty: true,
      lastStat: { mtime: 123, size: 11 },
    })
  })

})

describe('EditorPane — canSave semantics and dirty affordances (T1.3)', () => {
  beforeEach(resetEditorPaneStores)

  it('keeps Save disabled and shows no dirty dot for a clean loaded buffer with no stat', () => {
    const pane = createPane('/notes/no-stat.md', 'pane-no-stat')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    // A clean, non-untitled buffer whose stat is missing. Pre-opened so the load
    // effect short-circuits on the existing buffer.
    useEditorStore.getState().openBuffer(getBufferKey('/notes/no-stat.md'), 'loaded body', {
      language: 'markdown',
    })

    renderEditorPane(pane)

    const buffer = useEditorStore.getState().buffers[getBufferKey('/notes/no-stat.md')]
    expect(buffer.isDirty).toBe(false)
    expect(buffer.lastStat).toBeNull()
    // A missing stat must not masquerade as "modified".
    expect(screen.getByTitle('Save (⌘S)')).toBeDisabled()
    expect(screen.queryByTitle('Unsaved changes')).not.toBeInTheDocument()
  })

  it('keeps Save enabled for an untitled buffer that has never been saved', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-untitled-cansave')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    const buffer = useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]
    expect(buffer.isDirty).toBe(false)
    expect(buffer.lastStat).toBeNull()
    expect(buffer.untitled).toBeDefined()
    expect(screen.getByTitle('Save (⌘S)')).not.toBeDisabled()
  })

  it('shows the dirty dot, an enabled Save and the Diff button for a dirty buffer', async () => {
    const pane = createPane('/notes/dirty.md', 'pane-dirty')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('saved body'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 10, mtime: 7 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty.md')]).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/dirty.md'), 'edited body')
    })

    expect(useEditorStore.getState().buffers[getBufferKey('/notes/dirty.md')].isDirty).toBe(true)
    expect(screen.getByTitle('Unsaved changes')).toBeInTheDocument()
    expect(screen.getByTitle('Save (⌘S)')).not.toBeDisabled()
    expect(screen.getByTitle('Diff against saved')).toBeInTheDocument()
  })
})

// --- T3.3: every save attempt reports its outcome ---------------------------
//
// `handleSave` used to return silently when there was nothing to save and only
// `console.error` on failure, so ⌘S was indistinguishable from a dead key. The
// three outcomes now go through the existing bottom-centre toast
// (`useUndoToast` / `GlobalUndoToast`) — no second toast system.
describe('EditorPane — save result toast (T3.3)', () => {
  beforeEach(() => {
    resetEditorPaneStores()
    useUndoToast.setState({ toast: null })
  })

  it('T3.3-1: a dirty save writes and raises the saved toast naming the file', async () => {
    const pane = createPane('/notes/toast.txt', 'pane-toast-saved')
    const backend = await renderLoaded(pane)

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/toast.txt'), 'changed')
    })
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe('Saved toast.txt')
    })
    expect(backend.write).toHaveBeenCalledTimes(1)
    // A save outcome is informational — no undo/retry button.
    expect(useUndoToast.getState().toast?.action).toBeUndefined()
  })

  it('T3.3-2: ⌘S on a clean saved buffer raises the unchanged toast and never writes', async () => {
    const pane = createPane('/notes/clean.txt', 'pane-toast-unchanged')
    const backend = await renderLoaded(pane)

    // The toolbar button is disabled for a clean buffer (canSave semantics, T1.3),
    // so ⌘S is the reachable trigger for this outcome — and it used to do nothing.
    expect(screen.getByTitle('Save (⌘S)')).toBeDisabled()
    await pressCmdS()

    expect(backend.write).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast?.message).toBe('No changes to save')
  })

  it('T3.3-3: a rejected write raises the failure toast with the reason, keeps the buffer dirty and never marks it saved', async () => {
    const pane = createPane('/notes/fail.txt', 'pane-toast-failed')
    const backend = await renderLoaded(pane)
    backend.write.mockRejectedValue(new Error('disk full'))
    const markSaved = vi.spyOn(useEditorStore.getState(), 'markSaved')

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey('/notes/fail.txt'), 'changed')
    })
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe('Save failed: disk full')
    })
    expect(useEditorStore.getState().buffers[getBufferKey('/notes/fail.txt')]).toMatchObject({
      content: 'changed',
      savedContent: 'hello',
      isDirty: true,
    })
    expect(markSaved).not.toHaveBeenCalled()
    markSaved.mockRestore()
  })

  it('T3.3-4: ⌘S and the toolbar button produce the same outcome', async () => {
    const pane = createPane('/notes/parity.txt', 'pane-toast-parity')
    const backend = await renderLoaded(pane)
    const key = getBufferKey('/notes/parity.txt')

    act(() => {
      useEditorStore.getState().updateContent(key, 'via keyboard')
    })
    await pressCmdS()
    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe('Saved parity.txt')
    })
    const fromKeyboard = useUndoToast.getState().toast

    useUndoToast.setState({ toast: null })
    act(() => {
      useEditorStore.getState().updateContent(key, 'via button')
    })
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useUndoToast.getState().toast).toEqual(fromKeyboard)
    })
    expect(backend.write).toHaveBeenCalledTimes(2)
  })

  it('T3.3-4b: a save with no resolvable backend reports a failure instead of doing nothing', async () => {
    const pane = createPane('/notes/no-backend.txt', 'pane-toast-no-backend')
    const key = getBufferKey('/notes/no-backend.txt')
    // Seed the buffer so the pane renders, then take the backend away.
    useEditorStore.getState().openBuffer(key, 'hello', { language: 'plaintext' }, { mtime: 1, size: 5 })
    getFsBackendMock.mockReturnValue(null)
    registerTabPane(pane)

    renderEditorPane(pane)
    act(() => {
      useEditorStore.getState().updateContent(key, 'changed')
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe(
        'Save failed: No FS backend is available for this file.',
      )
    })
    expect(useEditorStore.getState().buffers[key]).toMatchObject({ isDirty: true })
  })

  // The untitled branch had its own silent `return` for a missing backend, so
  // T3.3-4b's guarantee stopped at the door of the one flow where the file does
  // not exist yet — the user sees the popover close and assumes it was written.
  it('T3.3-4c: an untitled save with no resolvable backend reports a failure instead of doing nothing', async () => {
    const untitled = { name: 'notes.txt', suggestedExtension: '.txt' as const, hasBeenRenamed: true }
    const content = {
      kind: 'editor' as const,
      source: { type: 'inapp' as const },
      filePath: 'untitled:notes.txt',
      untitled,
    }
    const pane: Pane = { id: 'pane-untitled-no-backend', content }
    useEditorStore.getState().openBuffer(getBufferKey('untitled:notes.txt'), 'hello', {
      language: 'plaintext',
      languageSource: 'extension',
      untitled,
    })
    const markSaved = vi.spyOn(useEditorStore.getState(), 'markSaved')
    getFsBackendMock.mockReturnValue(null)
    registerTabPane(pane)

    renderEditorPane(pane)
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe(
        'Save failed: No FS backend is available for this file.',
      )
    })
    expect(markSaved).not.toHaveBeenCalled()
    // Still an unsaved untitled buffer under its original key — nothing renamed,
    // nothing marked clean.
    expect(useEditorStore.getState().buffers[getBufferKey('untitled:notes.txt')]).toMatchObject({
      untitled: { name: 'notes.txt' },
      lastStat: null,
    })
    expect(useEditorStore.getState().buffers[getBufferKey('/buffer/notes.txt')]).toBeUndefined()
    markSaved.mockRestore()
  })

  it('T3.3-5: opening the untitled rename popover is not a save outcome — no toast', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-toast-untitled')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    expect(screen.getByDisplayValue('Untitled.md')).toBeInTheDocument()
    expect(backend.write).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('T3.3-5b: confirming the untitled name IS a save outcome — saved toast for the real file', async () => {
    const pane = createUntitledPane('Untitled', '.md', 'pane-toast-untitled-confirm')
    const backend = createBackend()
    backend.write.mockResolvedValue(undefined)
    statMissingUntilWritten(backend, { size: 0, mtime: 9 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)

    renderEditorPane(pane)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))
    fireEvent.keyDown(screen.getByDisplayValue('Untitled.md'), { key: 'Enter' })

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe('Saved Untitled.md')
    })
    expect(backend.write).toHaveBeenCalledWith('/buffer/Untitled.md', new TextEncoder().encode(''))
  })
})

// --- Keyboard save must reach the naming popover, not die on a missing anchor
//
// Monaco / Tiptap call `onSave()` with no arguments (they have no toolbar
// button to measure), and the never-named-untitled branch used to require an
// `anchorRect` to open the naming popover - so the keyboard save inside the
// editing surface was a dead key and only the toolbar button worked. The anchor
// is now decoupled from the decision: the Save button's own rect is the
// fallback.
describe('EditorPane - keyboard save opens the naming popover for an unnamed untitled document', () => {
  beforeEach(() => {
    resetEditorPaneStores()
    useUndoToast.setState({ toast: null })
  })

  it('opens the naming popover from the keyboard path, which passes no anchorRect', async () => {
    await renderUntitled('pane-cmds-untitled')
    await pressCmdS()

    expect(screen.getByDisplayValue('Untitled.txt')).toBeInTheDocument()
  })

  it('writes the file and raises the saved toast once the name is confirmed', async () => {
    const backend = await renderUntitled('pane-cmds-untitled-confirm')
    await pressCmdS()

    fireEvent.keyDown(screen.getByDisplayValue('Untitled.txt'), { key: 'Enter' })

    await waitFor(() => {
      expect(useUndoToast.getState().toast?.message).toBe('Saved Untitled.txt')
    })
    expect(backend.write).toHaveBeenCalledWith('/buffer/Untitled.txt', new TextEncoder().encode(''))
  })

  it('covers the Live-Mode surface too — Tiptap also saves without an anchorRect', async () => {
    await renderUntitled('pane-cmds-untitled-tiptap', '.md')
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    await pressCmdS('tiptap')

    expect(screen.getByDisplayValue('Untitled.md')).toBeInTheDocument()
  })

  it('leaves the toolbar button path unchanged (regression)', async () => {
    const backend = await renderUntitled('pane-cmds-untitled-toolbar')
    const saveButton = screen.getByTitle('Save (⌘S)')
    // T1.3: the button is enabled only because this untitled buffer has never
    // been saved. The fix must not have loosened that condition.
    expect(saveButton).not.toBeDisabled()

    fireEvent.click(saveButton)
    expect(screen.getByDisplayValue('Untitled.txt')).toBeInTheDocument()

    fireEvent.keyDown(screen.getByDisplayValue('Untitled.txt'), { key: 'Enter' })
    await waitFor(() => {
      expect(backend.write).toHaveBeenCalledWith('/buffer/Untitled.txt', new TextEncoder().encode(''))
    })
  })

  it('keeps Save disabled for a clean saved buffer (T1.3 semantics untouched)', async () => {
    const pane = createPane('/notes/clean-guard.txt', 'pane-cmds-clean')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 5, mtime: 1 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    renderEditorPane(pane)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/clean-guard.txt')]).toBeDefined()
    })

    expect(screen.getByTitle('Save (⌘S)')).toBeDisabled()
  })
})
