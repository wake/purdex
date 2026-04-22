import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { useEffect, useRef } from 'react'

interface Props {
  content: string // raw markdown
  isActive: boolean
  onChange: (markdown: string) => void
  onSave: () => void
}

export function TiptapEditor({ content, isActive, onChange, onSave }: Props) {
  const onSaveRef = useRef(onSave)
  const containerRef = useRef<HTMLDivElement>(null)

  const focusEditable = () => {
    containerRef.current?.querySelector<HTMLElement>('[contenteditable="true"]')?.focus()
  }

  useEffect(() => {
    onSaveRef.current = onSave
  }, [onSave])

  // Track whether the latest content change came from user typing (onUpdate)
  // to prevent the sync useEffect from re-setting content that just came from the editor
  const internalUpdateRef = useRef(false)

  const editor = useEditor({
    extensions: [StarterKit, Markdown],
    content,
    contentType: 'markdown',
    onUpdate: ({ editor: ed }) => {
      internalUpdateRef.current = true
      const md = ed.getMarkdown()
      onChange(md)
    },
    editorProps: {
      attributes: {
        class: 'tiptap-editor prose prose-invert prose-sm max-w-none min-h-full px-4 py-4 focus:outline-none',
      },
      handleKeyDown: (_view, event) => {
        if ((event.metaKey || event.ctrlKey) && event.key === 's') {
          event.preventDefault()
          onSaveRef.current()
          return true
        }
        return false
      },
    },
  })

  // Sync external content changes (e.g., reload from disk)
  useEffect(() => {
    if (!editor) return
    if (internalUpdateRef.current) {
      internalUpdateRef.current = false
      return
    }
    editor.commands.setContent(content, { emitUpdate: false, contentType: 'markdown' })
  }, [content, editor])

  useEffect(() => {
    if (!isActive) return
    focusEditable()
  }, [isActive])

  if (!editor) return null

  return (
    <div
      ref={containerRef}
      data-testid="tiptap-scroll-root"
      className="h-full min-h-0 overflow-auto"
      onMouseDown={(event) => {
        if ((event.target as HTMLElement).closest('[contenteditable="true"]')) return
        focusEditable()
      }}
    >
      <div className="min-h-full [&_.tiptap-editor]:min-h-full [&_.tiptap-editor]:cursor-text">
        <EditorContent editor={editor} />
      </div>
    </div>
  )
}
