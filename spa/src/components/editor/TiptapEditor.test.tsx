import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TiptapEditor } from './TiptapEditor'

const useEditorSpy = vi.hoisted(() => vi.fn())
const editorClassRef = vi.hoisted(() => ({ current: '' }))

vi.mock('@tiptap/react', () => ({
  useEditor: (config: { editorProps?: { attributes?: { class?: string } } }) => {
    editorClassRef.current = config.editorProps?.attributes?.class ?? ''
    return useEditorSpy(config)
  },
  EditorContent: () => <div data-testid="editor-content" data-editor-class={editorClassRef.current} />,
}))

vi.mock('@tiptap/starter-kit', () => ({
  default: {},
}))

vi.mock('@tiptap/markdown', () => ({
  Markdown: {},
}))

describe('TiptapEditor', () => {
  beforeEach(() => {
    useEditorSpy.mockReturnValue({
      getMarkdown: () => 'hello',
      commands: {
        setContent: vi.fn(),
      },
    })
  })

  it('uses a dedicated scroll container instead of putting prose on it', () => {
    render(<TiptapEditor content="# Hello" onChange={() => {}} onSave={() => {}} />)

    const scrollRoot = screen.getByTestId('tiptap-scroll-root')
    expect(scrollRoot.className).toContain('h-full')
    expect(scrollRoot.className).toContain('min-h-0')
    expect(scrollRoot.className).toContain('overflow-auto')
    expect(scrollRoot.className).not.toContain('prose')
  })

  it('applies typography classes to the editable root', () => {
    render(<TiptapEditor content="# Hello" onChange={() => {}} onSave={() => {}} />)

    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.stringContaining('prose prose-invert prose-sm max-w-none'),
    )
  })

  it('keeps a visible focus style on the editable root', () => {
    render(<TiptapEditor content="# Hello" onChange={() => {}} onSave={() => {}} />)

    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.not.stringContaining('focus:outline-none'),
    )
    expect(screen.getByTestId('editor-content')).toHaveAttribute(
      'data-editor-class',
      expect.stringContaining('focus-visible:outline'),
    )
  })
})
