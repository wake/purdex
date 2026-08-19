// T5.1 — the placeholder registry seen from the editor: the two events that end
// a file's placeholder life while it is OPEN — a save and an in-editor rename.
//
// Deregistration is permanent and fires on the FIRST such event. A save of EMPTY
// content counts: the user deliberately saved that emptiness, so the file is
// theirs even though it is still 0 B. This is exactly the case the rejected
// `savedContent === '' && !isDirty && size === 0` predicate could not tell apart
// from an untouched reservation.
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import {
  createBackend,
  createPane,
  getBufferKey,
  registerTabPane,
  renderEditorPane,
  renderLoaded,
  resetEditorPaneStores,
} from './editor-pane-harness'
import { getFsBackendMock } from './editor-pane-mocks'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { usePlaceholderFilesStore } from '../../../stores/usePlaceholderFilesStore'

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

function isRegistered(path: string): boolean {
  return usePlaceholderFilesStore.getState().isPlaceholder(INAPP, path)
}

/**
 * Drain the placeholder sweep's detached `stat().then(...).then(delete)` chain.
 * It is fired with `void` from inside an unmount cleanup, so a bare assertion
 * right after `unmount()` would pass no matter what the sweep decided.
 */
async function flushSweep(): Promise<void> {
  await act(async () => {
    for (let i = 0; i < 5; i += 1) await Promise.resolve()
  })
}

beforeEach(() => {
  resetEditorPaneStores()
  usePlaceholderFilesStore.setState({ paths: [PLACEHOLDER] })
})

describe('T5.1 — a save deregisters the placeholder', () => {
  it('a successful save makes the file the user\'s, permanently', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-save')
    const backend = await renderLoaded(pane, '')

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey(PLACEHOLDER), 'typed something')
    })
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => expect(backend.write).toHaveBeenCalledTimes(1))
    expect(isRegistered(PLACEHOLDER)).toBe(false)
  })

  it('saving EMPTY content also deregisters (the user saved that emptiness)', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-save-empty')
    const backend = await renderLoaded(pane, 'draft')

    act(() => {
      // Wipe the buffer and save it: 0 B on disk, and unmistakably the user's.
      useEditorStore.getState().updateContent(getBufferKey(PLACEHOLDER), '')
    })
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => expect(backend.write).toHaveBeenCalledTimes(1))
    expect(backend.write.mock.calls[0][1]).toEqual(new TextEncoder().encode(''))
    expect(isRegistered(PLACEHOLDER)).toBe(false)
  })

  it('a FAILED save keeps the entry — nothing was written, so nothing changed hands', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-save-fail')
    const backend = await renderLoaded(pane, '')
    backend.write.mockRejectedValue(new Error('disk on fire'))

    act(() => {
      useEditorStore.getState().updateContent(getBufferKey(PLACEHOLDER), 'typed something')
    })
    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => expect(backend.write).toHaveBeenCalledTimes(1))
    expect(isRegistered(PLACEHOLDER)).toBe(true)
  })
})

describe('T5.1 — an in-editor rename deregisters the placeholder', () => {
  it('leaves neither the old nor the new path registered', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-rename')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode(''))
    backend.rename.mockResolvedValue(undefined)
    backend.stat.mockImplementation(async (path: string) => {
      // The source exists; the rename target does not (no collision).
      if (path === PLACEHOLDER) return { isFile: true, isDirectory: false, size: 0, mtime: 1 }
      throw new Error('ENOENT')
    })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)]).toBeDefined()
    })

    fireEvent.doubleClick(screen.getByText('Untitled.md'))
    fireEvent.change(screen.getByDisplayValue('Untitled.md'), { target: { value: 'notes.md' } })
    fireEvent.keyDown(screen.getByDisplayValue('notes.md'), { key: 'Enter' })

    await waitFor(() => expect(backend.rename).toHaveBeenCalledWith(PLACEHOLDER, '/buffer/notes.md'))
    expect(isRegistered(PLACEHOLDER)).toBe(false)
    expect(isRegistered('/buffer/notes.md')).toBe(false)
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })
})

describe('T5.1 — an EXTERNAL write ends the placeholder too', () => {
  // The reservation is a 0 B file on a real filesystem, and we are not the only
  // writer to it: another program, another tab's save, a sync client. Once the
  // external content is reloaded into the editor the file is no longer an empty
  // shell we may silently remove — but nothing on that path used to say so, and
  // the registry entry survived as a standing delete authorization.
  it('an external-change reload deregisters, so closing the last pane keeps the file', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-external')
    const backend = createBackend() as ReturnType<typeof createBackend> & { delete: ReturnType<typeof vi.fn> }
    backend.read.mockResolvedValue(new TextEncoder().encode(''))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 0, mtime: 1 })
    backend.delete.mockResolvedValue(undefined)
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    // Inactive first: the external-change probe runs on tab ACTIVATION, so this
    // is the state the reservation sits in while another writer gets to it.
    const view = renderEditorPane(pane, false)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)]).toBeDefined()
    })
    expect(isRegistered(PLACEHOLDER)).toBe(true)

    // Someone else fills the reserved file in.
    backend.read.mockResolvedValue(new TextEncoder().encode('written by someone else'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 23, mtime: 2 })

    view.rerender(<EditorPane pane={pane} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)].savedContent)
        .toBe('written by someone else')
    })
    expect(isRegistered(PLACEHOLDER)).toBe(false)
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])

    // …and the sweep on the last close now has no authorization to act on.
    act(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })
    act(() => {
      view.unmount()
    })
    await flushSweep()

    expect(backend.delete).not.toHaveBeenCalled()
  })

  // The nastiest shape of the same event: an external writer rewrote the file
  // with content that happens to MATCH what we last saved — for a freshly minted
  // 0 B reservation, "the same" is trivially "still empty". The bytes on disk are
  // someone else's now, but a content comparison cannot see that, and the
  // sweep's own 0 B re-check waves it straight through. What proves the file
  // changed hands is the mtime/size moving, so that is what has to end the
  // placeholder — not the text differing.
  it('an external rewrite with IDENTICAL content deregisters too (mtime moved)', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-external-same')
    const backend = createBackend() as ReturnType<typeof createBackend> & { delete: ReturnType<typeof vi.fn> }
    backend.read.mockResolvedValue(new TextEncoder().encode(''))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 0, mtime: 1 })
    backend.delete.mockResolvedValue(undefined)
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    const view = renderEditorPane(pane, false)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)]).toBeDefined()
    })
    expect(isRegistered(PLACEHOLDER)).toBe(true)

    // Someone else wrote the file — same (empty) bytes, new mtime.
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 0, mtime: 2 })

    view.rerender(<EditorPane pane={pane} isActive />)
    // The probe has run its course once it reaches the re-read (deregistration
    // happens before that, on the stat).
    await waitFor(() => expect(backend.read.mock.calls.length).toBeGreaterThan(1))

    // Closing the last pane must have no authorization to delete it, even though
    // the file is still 0 B and would sail through the sweep's stat re-check.
    // Asserted BEFORE the registry, because this is the loss that matters.
    act(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })
    act(() => {
      view.unmount()
    })
    // The sweep's own stat→delete chain is two microtask hops deep and runs
    // detached from the unmount, so assert only after draining them — otherwise
    // "delete was not called" would be true of any teardown at all.
    await flushSweep()

    expect(backend.delete).not.toHaveBeenCalled()
    expect(isRegistered(PLACEHOLDER)).toBe(false)
    expect(usePlaceholderFilesStore.getState().paths).toEqual([])
  })

  it('an UNCHANGED file on activation still leaves the placeholder registered', async () => {
    // The control: the probe fires on every activation, so deregistering there
    // unconditionally would end every placeholder's life on the first tab
    // switch and quietly disable the whole sweep.
    const pane = createPane(PLACEHOLDER, 'pane-ph-external-nochange')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode(''))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 0, mtime: 1 })
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    const view = renderEditorPane(pane, false)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)]).toBeDefined()
    })

    view.rerender(<EditorPane pane={pane} isActive />)
    await waitFor(() => expect(backend.stat).toHaveBeenCalledTimes(2))

    expect(isRegistered(PLACEHOLDER)).toBe(true)
  })

  // The sibling of the reload case above, and the more dangerous one: when the
  // buffer is DIRTY we deliberately do not clobber the user's edits with the
  // external content, so the path keeps a reservation-shaped buffer in front of
  // it. But the fact that ends the placeholder's life happened on DISK — someone
  // else wrote real bytes into it — and that is true regardless of what our
  // buffer holds. Leaving the entry standing means closing without saving hands
  // the sweep a licence to delete a file that now carries someone else's
  // content.
  it('an external change seen with UNSAVED edits deregisters too, so the close keeps the file', async () => {
    const pane = createPane(PLACEHOLDER, 'pane-ph-external-dirty')
    const backend = createBackend() as ReturnType<typeof createBackend> & { delete: ReturnType<typeof vi.fn> }
    backend.read.mockResolvedValue(new TextEncoder().encode(''))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 0, mtime: 1 })
    backend.delete.mockResolvedValue(undefined)
    getFsBackendMock.mockReturnValue(backend)
    registerTabPane(pane)
    const view = renderEditorPane(pane, false)
    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)]).toBeDefined()
    })

    // The user typed into the reservation without saving…
    act(() => {
      useEditorStore.getState().updateContent(getBufferKey(PLACEHOLDER), 'my unsaved draft')
    })
    expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)].isDirty).toBe(true)
    // …and meanwhile someone else filled the reserved file in.
    backend.read.mockResolvedValue(new TextEncoder().encode('written by someone else'))
    backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: 23, mtime: 2 })

    view.rerender(<EditorPane pane={pane} isActive />)

    await waitFor(() => expect(isRegistered(PLACEHOLDER)).toBe(false))
    // The dirty buffer is NOT clobbered — deregistering is the only effect.
    expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)].content).toBe('my unsaved draft')
    expect(useEditorStore.getState().buffers[getBufferKey(PLACEHOLDER)].savedContent).toBe('')

    // Closing without saving must now leave the external content alone.
    act(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })
    act(() => {
      view.unmount()
    })
    await flushSweep()

    expect(backend.delete).not.toHaveBeenCalled()
  })
})
