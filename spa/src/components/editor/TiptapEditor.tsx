import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { useEffect, useRef } from 'react'

interface Props {
  content: string // raw markdown
  onChange: (markdown: string) => void
  onSave: () => void
}

export function TiptapEditor({ content, onChange, onSave }: Props) {
  const onSaveRef = useRef(onSave)
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
        class: 'prose prose-invert prose-sm max-w-none min-h-full px-4 py-4 focus:outline-none',
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

  if (!editor) return null

  return (
    <div data-testid="tiptap-scroll-root" className="h-full min-h-0 overflow-auto">
      <div className="min-h-full">
        <EditorContent editor={editor} />
      </div>
    </div>
  )
}
