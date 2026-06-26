import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TiptapEditor } from './TiptapEditor'

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

vi.mock('./tiptapSelection', () => ({
  resolveRestoreSelection: vi.fn(() => ({ __fake: 'selection' })),
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

    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.stringContaining('prose prose-invert prose-sm max-w-none'),
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
