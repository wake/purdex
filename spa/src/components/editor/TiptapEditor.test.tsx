import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TiptapEditor } from './TiptapEditor'
import { resolveRestoreSelection } from './tiptapSelection'
import { tiptapExtensions } from './tiptapExtensions'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { TableKit } from '@tiptap/extension-table'
import { TaskList } from '@tiptap/extension-task-list'
import { TaskItem } from '@tiptap/extension-task-item'

const useEditorSpy = vi.hoisted(() => vi.fn())
const editorClassRef = vi.hoisted(() => ({ current: '' }))
const focusSpy = vi.hoisted(() => vi.fn())

vi.mock('@tiptap/react', () => ({
  useEditor: (config: { editorProps?: { attributes?: { class?: string } } }) => {
    editorClassRef.current = config.editorProps?.attributes?.class ?? ''
    return useEditorSpy(config)
  },
  EditorContent: () => <div data-testid="editor-content" data-editor-class={editorClassRef.current}><div data-testid="editor-editable" contentEditable={true} tabIndex={0} ref={(node) => { if (node) node.focus = focusSpy }} /></div>,
}))

vi.mock('@tiptap/starter-kit', () => ({
  default: {},
}))

vi.mock('@tiptap/markdown', () => ({
  Markdown: {},
}))

vi.mock('@tiptap/extension-table', () => ({
  TableKit: { __ext: 'table-kit' },
}))

vi.mock('@tiptap/extension-task-list', () => ({
  TaskList: { __ext: 'task-list' },
}))

vi.mock('@tiptap/extension-task-item', () => ({
  TaskItem: { __ext: 'task-item' },
}))

vi.mock('./tiptapSelection', () => ({
  resolveRestoreSelection: vi.fn(() => ({ __fake: 'selection' })),
}))

// The table menu owns a real ProseMirror plugin; this suite drives a mock editor,
// so it is stubbed here and covered on its own in TableBubbleMenu.test.tsx.
vi.mock('./TableBubbleMenu', () => ({
  TableBubbleMenu: (props: { editor: unknown }) => (
    <div data-testid="table-bubble-menu" data-has-editor={props.editor ? 'true' : 'false'} />
  ),
}))

function makeMockEditor(overrides: Record<string, unknown> = {}) {
  return {
    getMarkdown: () => 'hello',
    commands: { setContent: vi.fn() },
    state: {
      selection: { from: 1, to: 1 },
      tr: { setSelection: vi.fn().mockReturnThis() },
    },
    view: { dispatch: vi.fn() },
    ...overrides,
  }
}

describe('TiptapEditor', () => {
  beforeEach(() => {
    focusSpy.mockReset()
    useEditorSpy.mockReturnValue(makeMockEditor())
  })

  it('uses a dedicated scroll container instead of putting prose on it', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    const scrollRoot = screen.getByTestId('tiptap-scroll-root')
    expect(scrollRoot.className).toContain('h-full')
    expect(scrollRoot.className).toContain('min-h-0')
    expect(scrollRoot.className).toContain('overflow-auto')
    expect(scrollRoot.className).not.toContain('prose')
  })

  it('applies typography classes to the editable root', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    // prose-sm dropped: explicit .tiptap-editor CSS now owns the type scale.
    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.stringContaining('prose prose-invert max-w-none'),
    )
    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.not.stringContaining('prose-sm'),
    )
  })

  it('keeps the tiptap-editor class the CSS scope depends on', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.stringContaining('tiptap-editor'),
    )
  })

  it('suppresses the editable focus outline to avoid a bottom accent line', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.stringContaining('focus:outline-none'),
    )
    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.not.stringContaining('focus-visible:outline'),
    )
  })

  it('focuses the editable content when the pane becomes active', () => {
    const { rerender } = render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    focusSpy.mockClear()

    rerender(<TiptapEditor content="# Hello" isActive={true} onChange={() => {}} onSave={() => {}} />)

    expect(focusSpy).toHaveBeenCalledTimes(1)
    // Re-activation focus must not scroll the caret into view — otherwise a
    // scrolled document jumps to the top on every tab switch back.
    expect(focusSpy).toHaveBeenCalledWith({ preventScroll: true })
  })

  it('focuses on the null→editor ready transition when active (AC2, M1)', () => {
    useEditorSpy.mockReturnValue(undefined) // editor 尚未 ready
    const { rerender } = render(<TiptapEditor content="# Hi" isActive={true} onChange={() => {}} onSave={() => {}} />)
    focusSpy.mockClear()
    useEditorSpy.mockReturnValue(makeMockEditor()) // editor ready
    rerender(<TiptapEditor content="# Hi" isActive={true} onChange={() => {}} onSave={() => {}} />)
    expect(focusSpy).toHaveBeenCalled()
  })

  it('does NOT focus on ready transition when inactive (AC3, I1)', () => {
    useEditorSpy.mockReturnValue(undefined)
    const { rerender } = render(<TiptapEditor content="# Hi" isActive={false} onChange={() => {}} onSave={() => {}} />)
    focusSpy.mockClear()
    useEditorSpy.mockReturnValue(makeMockEditor())
    rerender(<TiptapEditor content="# Hi" isActive={false} onChange={() => {}} onSave={() => {}} />)
    expect(focusSpy).not.toHaveBeenCalled()
  })

  it('focuses the editable content when clicking empty editor space', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    focusSpy.mockClear()

    fireEvent.mouseDown(screen.getByTestId('tiptap-scroll-root'))

    expect(focusSpy).toHaveBeenCalledTimes(1)
  })

  it('saves scrollTop + live selection on unmount via null→editor transition (AC5, M1/M2)', () => {
    const ed = makeMockEditor({ state: { selection: { from: 4, to: 9 }, tr: { setSelection: vi.fn().mockReturnThis() } } })
    const onViewStateChange = vi.fn()
    // M1: start with editor NOT ready, then become ready. This is the only shape
    // that catches a regression where cleanup reads a stale render-time `editor`
    // closure (null on first render) instead of editorRef.current (R2 health H1).
    useEditorSpy.mockReturnValue(undefined)
    const { rerender, unmount, container } = render(
      <TiptapEditor content="hi" isActive={false} initialViewState={null}
        onChange={() => {}} onViewStateChange={onViewStateChange} onSave={() => {}} />,
    )
    useEditorSpy.mockReturnValue(ed)
    rerender(
      <TiptapEditor content="hi" isActive={false} initialViewState={null}
        onChange={() => {}} onViewStateChange={onViewStateChange} onSave={() => {}} />,
    )
    const scrollRoot = container.querySelector('[data-testid="tiptap-scroll-root"]') as HTMLElement
    Object.defineProperty(scrollRoot, 'scrollTop', { value: 88, writable: true, configurable: true })
    unmount()
    // selection now carries its kind ('text' for a plain object selection, D2)
    expect(onViewStateChange).toHaveBeenCalledWith({ scrollTop: 88, selection: { type: 'text', from: 4, to: 9 } })
  })

  it('restores selection AND scroll BEFORE focus on ready (AC8, I3)', () => {
    const dispatch = vi.fn()
    const setSelection = vi.fn().mockReturnValue('TR')
    const ed = makeMockEditor({
      state: { selection: { from: 1, to: 1 }, doc: {}, tr: { setSelection } },
      view: { dispatch },
    })
    const initial = { scrollTop: 50, selection: { type: 'text' as const, from: 2, to: 5 } }
    let scrollAtFocus = -1
    focusSpy.mockImplementation(() => {
      const root = document.querySelector('[data-testid="tiptap-scroll-root"]') as HTMLElement | null
      if (scrollAtFocus === -1 && root) scrollAtFocus = root.scrollTop
    })
    useEditorSpy.mockReturnValue(ed)
    render(
      <TiptapEditor content="hi" isActive={true} initialViewState={initial}
        onChange={() => {}} onViewStateChange={() => {}} onSave={() => {}} />,
    )
    // selection restore goes through resolveRestoreSelection (mocked → {__fake})
    expect(setSelection).toHaveBeenCalledWith({ __fake: 'selection' })
    // selection dispatch before first focus
    expect(dispatch).toHaveBeenCalled()
    expect(dispatch.mock.invocationCallOrder[0]).toBeLessThan(focusSpy.mock.invocationCallOrder[0])
    // scroll restored before focus (scrollTop already 50 at focus time)
    expect(scrollAtFocus).toBe(50)
  })

  it('centers the content wrapper when contentWidth is narrow', () => {
    render(<TiptapEditor content="# Hello" isActive={false} contentWidth="narrow" onChange={() => {}} onSave={() => {}} />)

    const wrapper = screen.getByTestId('editor-content').parentElement as HTMLElement
    expect(wrapper.className).toContain('max-w-[52em]')
    expect(wrapper.className).toContain('mx-auto')
    expect(wrapper.className).toContain('box-border')
  })

  it('lets the content wrapper span full width when contentWidth is full', () => {
    render(<TiptapEditor content="# Hello" isActive={false} contentWidth="full" onChange={() => {}} onSave={() => {}} />)

    const wrapper = screen.getByTestId('editor-content').parentElement as HTMLElement
    expect(wrapper.className).toContain('max-w-none')
    expect(wrapper.className).not.toContain('max-w-[52em]')
  })

  it('defaults to narrow when contentWidth is not provided', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    const wrapper = screen.getByTestId('editor-content').parentElement as HTMLElement
    expect(wrapper.className).toContain('max-w-[52em]')
  })

  it('moves horizontal padding off the editable root onto the wrapper (keeps max-w-none / py-4)', () => {
    render(<TiptapEditor content="# Hello" isActive={false} contentWidth="narrow" onChange={() => {}} onSave={() => {}} />)

    // Horizontal padding now lives on the width wrapper; the editable root keeps
    // max-w-none (so prose does not self-limit to 65ch) and its vertical padding.
    const editorClass = screen.getByTestId('editor-content').getAttribute('data-editor-class') ?? ''
    expect(editorClass).not.toContain('px-4')
    expect(editorClass).toContain('max-w-none')
    expect(editorClass).toContain('py-4')
  })

  it('does not rerun restore/focus/viewState or reset scroll when contentWidth toggles', () => {
    const dispatch = vi.fn()
    const ed = makeMockEditor({
      state: { selection: { from: 1, to: 1 }, doc: {}, tr: { setSelection: vi.fn().mockReturnThis() } },
      view: { dispatch },
    })
    useEditorSpy.mockReturnValue(ed)
    const onViewStateChange = vi.fn()
    const initial = { scrollTop: 30, selection: { type: 'text' as const, from: 2, to: 5 } }

    const { rerender } = render(
      <TiptapEditor content="hi" isActive={true} initialViewState={initial} contentWidth="narrow"
        onChange={() => {}} onViewStateChange={onViewStateChange} onSave={() => {}} />,
    )

    // Snapshot the one-shot restore/focus footprint after the initial (narrow) render.
    const restoreCalls = vi.mocked(resolveRestoreSelection).mock.calls.length
    const dispatchCalls = dispatch.mock.calls.length
    const focusCalls = focusSpy.mock.calls.length
    const scrollRoot = screen.getByTestId('tiptap-scroll-root')
    Object.defineProperty(scrollRoot, 'scrollTop', { value: 77, writable: true, configurable: true })
    onViewStateChange.mockClear()

    rerender(
      <TiptapEditor content="hi" isActive={true} initialViewState={initial} contentWidth="full"
        onChange={() => {}} onViewStateChange={onViewStateChange} onSave={() => {}} />,
    )

    expect(vi.mocked(resolveRestoreSelection).mock.calls.length).toBe(restoreCalls) // restore not re-run
    expect(dispatch.mock.calls.length).toBe(dispatchCalls) // selection not re-dispatched
    expect(focusSpy.mock.calls.length).toBe(focusCalls) // focus not re-fired
    expect(onViewStateChange).not.toHaveBeenCalled() // no phantom unmount write-back
    expect(scrollRoot.scrollTop).toBe(77) // scroll position preserved
  })

  it('registers table + task list extensions alongside StarterKit and Markdown (T2.2a)', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    const config = useEditorSpy.mock.calls[0][0] as { extensions: unknown[] }
    expect(config.extensions).toEqual(tiptapExtensions)
    expect(config.extensions).toContain(StarterKit)
    expect(config.extensions).toContain(Markdown)
    expect(config.extensions).toContain(TableKit)
    expect(config.extensions).toContain(TaskList)
    expect(config.extensions).toContain(TaskItem)
  })

  it('mounts the table editing menu against the live editor (T2.2b)', () => {
    render(<TiptapEditor content="# Hello" isActive={false} onChange={() => {}} onSave={() => {}} />)

    expect(screen.getByTestId('table-bubble-menu')).toHaveAttribute('data-has-editor', 'true')
  })

  it('does NOT overwrite existing viewState when unmounted before editor is ready (R1 P2)', () => {
    // editor never becomes ready (stuck at null → restore never ran)
    useEditorSpy.mockReturnValue(undefined)
    const onViewStateChange = vi.fn()
    const { unmount } = render(
      <TiptapEditor content="hi" isActive={false} initialViewState={{ scrollTop: 100, selection: { type: 'text', from: 5, to: 5 } }}
        onChange={() => {}} onViewStateChange={onViewStateChange} onSave={() => {}} />,
    )
    unmount()
    // must not clobber the stored viewState with scrollTop:0/selection:null
    expect(onViewStateChange).not.toHaveBeenCalled()
  })
})
