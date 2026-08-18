// EditorPane — the round-trip safety assessment itself (T2.3): how often it runs
// and what happens when it blows up.
//
// This lives in its own file because it needs `round-trip-safety` replaced by a
// spy: the sibling EditorPane suites import the component at the top with a
// fixed mock set, so a call-count assertion cannot be retrofitted into them.
import { screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createBackend,
  createPane,
  getBufferKey,
  renderEditorPane,
  resetEditorPaneStores,
} from './editor-pane-harness'
import { getFsBackendMock } from './editor-pane-mocks'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'

const assessSpy = vi.hoisted(() => vi.fn(() => ({ safe: true, blockers: [] as string[] })))

vi.mock('../../../lib/markdown/round-trip-safety', () => ({
  assessMarkdownRoundTrip: assessSpy,
}))

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

function openMarkdownPane(paneId: string, filePath: string, content = '# hi') {
  const pane = createPane(filePath, paneId)
  getFsBackendMock.mockReturnValue(createBackend())
  useEditorStore.getState().openBuffer(getBufferKey(filePath), content, {
    language: 'markdown', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
  })
  useEditorStore.getState().attachPane(paneId, getBufferKey(filePath))
  return pane
}

describe('EditorPane round-trip gate', () => {
  beforeEach(() => {
    resetEditorPaneStores()
    assessSpy.mockReset()
    assessSpy.mockReturnValue({ safe: true, blockers: [] })
  })

  it('assesses a buffer once, not on every render', async () => {
    const pane = openMarkdownPane('pane-memo', '/notes/memo.md')
    const { rerender } = renderEditorPane(pane, true)
    await waitFor(() => screen.getByTestId('tiptap-editor'))

    const afterMount = assessSpy.mock.calls.length
    expect(afterMount).toBeGreaterThan(0)

    // An unrelated prop change re-renders the pane; lexing the whole document
    // again on every render would make typing quadratic.
    rerender(<EditorPane pane={pane} isActive={false} />)
    rerender(<EditorPane pane={pane} isActive={true} />)

    expect(assessSpy.mock.calls.length).toBe(afterMount)
  })

  it('does not re-assess while the user types (T2.3b)', async () => {
    const pane = openMarkdownPane('pane-memo-typing', '/notes/memo-typing.md')
    renderEditorPane(pane, true)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    const afterMount = assessSpy.mock.calls.length

    // A draft is not a file. Re-assessing it would both cost a full lex per
    // keystroke and let the verdict flip the editor out from under the cursor.
    useEditorStore.getState().updateContent(getBufferKey('/notes/memo-typing.md'), '# hi\n\n<div>now unsafe</div>\n')

    expect(assessSpy.mock.calls.length).toBe(afterMount)
  })

  it('re-assesses when the saved content actually changes', async () => {
    const pane = openMarkdownPane('pane-memo-content', '/notes/memo2.md')
    renderEditorPane(pane, true)
    await waitFor(() => screen.getByTestId('tiptap-editor'))
    const afterMount = assessSpy.mock.calls.length

    useEditorStore.getState().reloadBuffer(getBufferKey('/notes/memo2.md'), '# hi\n\n<div>now unsafe</div>\n')

    await waitFor(() => {
      expect(assessSpy.mock.calls.length).toBeGreaterThan(afterMount)
    })
    expect(assessSpy).toHaveBeenLastCalledWith('# hi\n\n<div>now unsafe</div>\n')
  })

  it('fails closed to raw when the assessment throws', async () => {
    assessSpy.mockImplementation(() => {
      throw new Error('lexer exploded')
    })
    const pane = openMarkdownPane('pane-memo-throw', '/notes/memo3.md')

    renderEditorPane(pane, true)

    // The pane still renders (a throwing lexer must not take down the editor),
    // and it renders the safe surface: raw, with a stated reason.
    await waitFor(() => screen.getByTestId('monaco-wrapper'))
    expect(screen.queryByTestId('tiptap-editor')).toBeNull()
    expect(screen.getByTestId('editor-raw-reason')).toBeInTheDocument()
  })
})
