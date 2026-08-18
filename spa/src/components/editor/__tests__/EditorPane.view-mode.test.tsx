// EditorPane — what the pane renders: raw vs Live Mode resolution, view-state
// hand-off, content width and the stale-paneState guards (#863).
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createBackend,
  createPane,
  getBufferKey,
  renderEditorPane,
  resetEditorPaneStores,
} from './editor-pane-harness'
import { editorStatusBarMock, getFsBackendMock, monacoPropsSpy, tiptapPropsSpy } from './editor-pane-mocks'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useEditorSettingsStore } from '../../../stores/useEditorSettingsStore'

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

  it('passes active state through to the editor content for refocus', async () => {
    const pane = createPane('/notes/focus.txt')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })

    const { rerender } = renderEditorPane(pane, false)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/focus.txt')]).toBeDefined()
    })

    rerender(<EditorPane pane={pane} isActive />)

    expect(screen.getByTestId('monaco-wrapper')).toHaveAttribute('data-active', 'true')
  })

  it('keeps non-markdown files in source mode even if the pane state was previously live mode', async () => {
    const pane = createPane('/notes/plain.txt', 'pane-txt')
    const backend = createBackend()
    backend.read.mockResolvedValue(new TextEncoder().encode('hello world'))
    backend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 11,
      mtime: 123,
    })
    getFsBackendMock.mockReturnValue(backend)

    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/plain.txt'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    renderEditorPane(pane)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers[getBufferKey('/notes/plain.txt')]).toBeDefined()
    })

    expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
    expect(screen.queryByTestId('tiptap-editor')).not.toBeInTheDocument()
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-is-markdown', 'false')
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'raw')
  })

  it('uses buffer metadata instead of the file extension to allow markdown live mode', async () => {
    const pane = createPane('/notes/plain.txt', 'pane-manual-markdown')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)

    useEditorStore.getState().openBuffer(getBufferKey('/notes/plain.txt'), '# hello', {
      language: 'markdown',
      languageSource: 'manual',
      eol: 'lf',
      encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/plain.txt'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    renderEditorPane(pane)

    await waitFor(() => {
      expect(screen.getByTestId('tiptap-editor')).toBeInTheDocument()
    })

    expect(screen.queryByTestId('monaco-wrapper')).not.toBeInTheDocument()
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-language', 'markdown')
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-is-markdown', 'true')
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'wysiwyg')
  })

  it('passes tiptapViewState into TiptapEditor and saves it back on change (AC9)', async () => {
    const pane = createPane('/notes/vs.md', 'pane-vs')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/vs.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/vs.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')
    useEditorStore.getState().saveTiptapViewState(pane.id, { scrollTop: 7, selection: { type: 'text', from: 1, to: 1 } })

    renderEditorPane(pane)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    // initialViewState 確實傳入
    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(
      expect.objectContaining({ initialViewState: { scrollTop: 7, selection: { type: 'text', from: 1, to: 1 } } }),
    )
    // onViewStateChange 回呼確實寫回 store
    fireEvent.click(screen.getByTestId('tiptap-editor'))
    expect(useEditorStore.getState().paneStates[pane.id].tiptapViewState).toEqual({ scrollTop: 42, selection: { type: 'text', from: 2, to: 3 } })
  })

  it('passes the store contentWidth into TiptapEditor (wysiwyg path)', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'full' })
    const pane = createPane('/notes/cw.md', 'pane-cw')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    renderEditorPane(pane)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(expect.objectContaining({ contentWidth: 'full' }))
  })

  it('passes contentWidth + onContentWidthChange into EditorStatusBar and the callback updates the store', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    const pane = createPane('/notes/cw2.md', 'pane-cw2')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw2.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw2.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    renderEditorPane(pane)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    expect(editorStatusBarMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ contentWidth: 'narrow', onContentWidthChange: expect.any(Function) }),
    )

    const onContentWidthChange = editorStatusBarMock.mock.calls.at(-1)?.[0].onContentWidthChange as (v: 'narrow' | 'full') => void
    act(() => onContentWidthChange('full'))
    expect(useEditorSettingsStore.getState().contentWidth).toBe('full')
  })

  it('withholds the width-toggle handler while DiffView is active, even entering diff from Live Mode (AC4)', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'narrow' })
    const pane = createPane('/notes/cw-diff.md', 'pane-cw-diff')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw-diff.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw-diff.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    renderEditorPane(pane)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    // Live Mode exposes the handler → toggle visible.
    expect(editorStatusBarMock.mock.calls.at(-1)?.[0].onContentWidthChange).toEqual(expect.any(Function))

    // Enter diff: DiffView mounts and Tiptap unmounts, but editorMode stays
    // 'wysiwyg'. The handler must be withheld so EditorStatusBar hides the toggle.
    act(() => useEditorStore.getState().setShowDiff(pane.id, true))
    await waitFor(() => {
      expect(editorStatusBarMock.mock.calls.at(-1)?.[0].onContentWidthChange).toBeUndefined()
    })
  })

  it('keeps the store contentWidth after a raw ↔ live round trip (AC8)', async () => {
    useEditorSettingsStore.setState({ contentWidth: 'full' })
    const pane = createPane('/notes/cw3.md', 'pane-cw3')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/cw3.md'), '# hello', {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/cw3.md'))
    useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg')

    renderEditorPane(pane)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(expect.objectContaining({ contentWidth: 'full' }))

    // Switch to raw and back to wysiwyg.
    act(() => useEditorStore.getState().setEditorMode(pane.id, 'raw'))
    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    tiptapPropsSpy.mockClear()
    act(() => useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg'))
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    expect(tiptapPropsSpy).toHaveBeenLastCalledWith(expect.objectContaining({ contentWidth: 'full' }))
  })

  // --- T2.3: the round-trip safety gate --------------------------------------
  //
  // Live Mode loses whatever Tiptap cannot represent, and it loses it at PARSE
  // time: a document carrying raw HTML / front matter / footnotes is already
  // degraded before the first keystroke, and the next save writes that back over
  // the file. Such a document therefore must not open in Live Mode by default.

  const UNSAFE_MD = '# Title\n\n<div class="callout">keep me</div>\n'

  async function openMarkdown(paneId: string, filePath: string, content: string, mode?: 'raw' | 'wysiwyg') {
    const pane = createPane(filePath, paneId)
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey(filePath), content, {
      language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(paneId, getBufferKey(filePath))
    if (mode) useEditorStore.getState().setEditorMode(paneId, mode)
    renderEditorPane(pane)
    return pane
  }

  it('opens markdown that cannot round-trip in raw mode when the user has not chosen (T2.3)', async () => {
    await openMarkdown('pane-gate-unsafe', '/notes/unsafe.md', UNSAFE_MD)

    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    expect(screen.queryByTestId('tiptap-editor')).toBeNull()
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'raw')
    // …and says why, so the user is not left wondering where Live Mode went.
    expect(screen.getByTestId('editor-raw-reason')).toHaveTextContent(/raw/i)
    expect(screen.getByTestId('editor-raw-reason')).toHaveTextContent(/HTML/i)
  })

  it('lets an explicit wysiwyg choice override the gate (T2.3)', async () => {
    await openMarkdown('pane-gate-override', '/notes/unsafe-override.md', UNSAFE_MD, 'wysiwyg')

    await waitFor(() => screen.getByTestId('tiptap-editor'))
    expect(screen.queryByTestId('monaco-wrapper')).toBeNull()
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'wysiwyg')
    expect(screen.queryByTestId('editor-raw-reason')).toBeNull()
  })

  it('keeps the Live Mode default for markdown that round-trips safely (T2.3)', async () => {
    await openMarkdown(
      'pane-gate-safe',
      '/notes/safe.md',
      '# Title\n\n- [ ] todo\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n',
    )

    await waitFor(() => screen.getByTestId('tiptap-editor'))
    expect(screen.queryByTestId('monaco-wrapper')).toBeNull()
    expect(screen.queryByTestId('editor-raw-reason')).toBeNull()
  })

  it("says nothing about the gate when raw was the user's own choice (T2.3)", async () => {
    await openMarkdown('pane-gate-user-raw', '/notes/unsafe-user-raw.md', UNSAFE_MD, 'raw')

    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    expect(screen.queryByTestId('editor-raw-reason')).toBeNull()
  })

  it('never applies the gate to non-markdown buffers (T2.3)', async () => {
    const pane = createPane('/notes/page.html', 'pane-gate-html')
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/page.html'), UNSAFE_MD, {
      language: 'html', languageSource: 'extension', eol: 'lf', encoding: 'utf8',
    })
    useEditorStore.getState().attachPane(pane.id, getBufferKey('/notes/page.html'))

    renderEditorPane(pane)

    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    expect(screen.getByTestId('editor-status-bar')).toHaveAttribute('data-editor-mode', 'raw')
    // Raw is simply what a non-markdown file gets; there is no gate to explain.
    expect(screen.queryByTestId('editor-raw-reason')).toBeNull()
  })

  it('renders prose for every blocker the assessment can report, never a bare key (T2.3)', async () => {
    await openMarkdown('pane-gate-footnote', '/notes/footnote.md', 'text[^a]\n\n[^a]: note\n')

    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    const reason = screen.getByTestId('editor-raw-reason')
    expect(reason.textContent).not.toMatch(/editor\.live_mode/)
    expect(reason.textContent?.trim().length).toBeGreaterThan(0)
  })

  it('does not mount TiptapEditor against stale paneState even when lazy is cached (stale→raw derivation, supersedes R3 gating)', async () => {
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/g-a.md'), '# A', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })
    useEditorStore.getState().openBuffer(getBufferKey('/notes/g-b.md'), '# B', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })

    // 1) Warm the React.lazy cache so TiptapEditor mounts SYNCHRONOUSLY afterwards
    //    (this is exactly the condition R3 flagged: lazy cached → no Suspense gap).
    useEditorStore.getState().attachPane('pane-warm', getBufferKey('/notes/g-a.md'))
    useEditorStore.getState().setEditorMode('pane-warm', 'wysiwyg')
    const warm = renderEditorPane(createPane('/notes/g-a.md', 'pane-warm'))
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    warm.unmount()

    // 2) Pane aligned to buffer A in wysiwyg, then FREEZE attachPane so paneState
    //    stays on A while we render the pane pointing at buffer B — deterministically
    //    reproducing the transient window (paneState.bufferKey=A, key=B) without
    //    racing the post-commit effect.
    useEditorStore.getState().attachPane('pane-gate', getBufferKey('/notes/g-a.md'))
    useEditorStore.getState().setEditorMode('pane-gate', 'wysiwyg')
    const spy = vi.spyOn(useEditorStore.getState(), 'attachPane').mockImplementation(() => {})
    try {
      tiptapPropsSpy.mockClear()
      renderEditorPane(createPane('/notes/g-b.md', 'pane-gate'))
      // lazy is cached now; WITHOUT the stale→raw derivation TiptapEditor would mount
      // synchronously against the stale paneState and lock didRestoreRef. Because the
      // stale paneState (bufferKey=A ≠ key=B) is treated as unaligned, editorMode
      // falls back to raw and the wysiwyg branch is never reached — raw Monaco mounts
      // instead, so Tiptap is never instantiated against a stale state.
      expect(tiptapPropsSpy).not.toHaveBeenCalled()
      expect(screen.queryByTestId('tiptap-editor')).toBeNull()
      expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
    } finally {
      spy.mockRestore()
    }
  })

  it('switching to a new markdown buffer while previous mode was wysiwyg renders raw Monaco, not a Loading editor flicker (#863)', async () => {
    const backend = createBackend()
    getFsBackendMock.mockReturnValue(backend)
    useEditorStore.getState().openBuffer(getBufferKey('/notes/f-a.md'), '# A', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })
    useEditorStore.getState().openBuffer(getBufferKey('/notes/f-b.md'), '# B', { language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8' })

    // Pane aligned to buffer A in wysiwyg, carrying stale A-only state across every
    // paneState-derived field — proves NONE of it leaks onto buffer B's render:
    // monacoViewState (must not seed B's Monaco), showDiff (must not pre-mount
    // DiffView), cursorPosition (must not leak to the status bar).
    useEditorStore.getState().attachPane('pane-flicker', getBufferKey('/notes/f-a.md'))
    useEditorStore.getState().setEditorMode('pane-flicker', 'wysiwyg')
    useEditorStore.getState().saveMonacoViewState('pane-flicker', { stale: 'A' } as unknown as import('monaco-editor').editor.ICodeEditorViewState)
    useEditorStore.getState().setShowDiff('pane-flicker', true)
    useEditorStore.getState().updateCursor('pane-flicker', 5, 9)

    // Freeze attachPane so paneState stays stale on A (bufferKey=A) while we render
    // the pane now pointing at buffer B — deterministically reproducing the transient
    // window that #863 paints as a `Loading editor…` flicker.
    const spy = vi.spyOn(useEditorStore.getState(), 'attachPane').mockImplementation(() => {})
    try {
      tiptapPropsSpy.mockClear()
      monacoPropsSpy.mockClear()
      editorStatusBarMock.mockClear()
      renderEditorPane(createPane('/notes/f-b.md', 'pane-flicker'))

      // AC1: raw Monaco shown; no Tiptap; no `Loading editor…` fallback.
      expect(screen.getByTestId('monaco-wrapper')).toBeInTheDocument()
      expect(screen.queryByTestId('tiptap-editor')).toBeNull()
      expect(tiptapPropsSpy).not.toHaveBeenCalled()
      expect(screen.queryByText(/Loading editor/)).toBeNull()

      // AC2: Monaco receives null initialViewState — stale A viewState must not leak.
      expect(monacoPropsSpy).toHaveBeenCalled()
      expect(monacoPropsSpy.mock.calls.every(([p]) => p.initialViewState === null)).toBe(true)

      // AC5: stale showDiff must not pre-mount DiffView; stale cursor must not leak.
      expect(screen.queryByTestId('diff-view')).toBeNull()
      expect(editorStatusBarMock).toHaveBeenCalled()
      expect(editorStatusBarMock.mock.calls.at(-1)?.[0]).toMatchObject({ line: 1, column: 1 })
    } finally {
      spy.mockRestore()
    }
  })

})
