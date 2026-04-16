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

  const editor = useEditor({
    extensions: [StarterKit, Markdown],
    content,
    contentType: 'markdown',
    onUpdate: ({ editor: ed }) => {
      const md = ed.getMarkdown()
      onChange(md)
    },
    editorProps: {
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
    const currentMd = editor.getMarkdown()
    if (currentMd !== content) {
      editor.commands.setContent(content, false, { contentType: 'markdown' })
    }
  }, [content, editor])

  if (!editor) return null

  return (
    <div className="flex-1 overflow-auto p-4 prose prose-invert prose-sm max-w-none">
      <EditorContent editor={editor} />
    </div>
  )
}
