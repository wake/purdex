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
})
