import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { useEffect, useLayoutEffect, useRef } from 'react'
import { resolveRestoreSelection } from './tiptapSelection'
import type { TiptapViewState } from '../../stores/useEditorStore'

interface Props {
  content: string // raw markdown
  isActive: boolean
  initialViewState?: TiptapViewState | null
  onChange: (markdown: string) => void
  onViewStateChange?: (viewState: TiptapViewState) => void
  onSave: () => void
}

export function TiptapEditor({ content, isActive, initialViewState, onChange, onViewStateChange, onSave }: Props) {
  const onSaveRef = useRef(onSave)
  const containerRef = useRef<HTMLDivElement>(null)
  const isActiveRef = useRef(isActive)
  const didRestoreRef = useRef(false)
  const editorRef = useRef<Editor | null>(null)
  const onViewStateChangeRef = useRef(onViewStateChange)
  const hasInitializedRef = useRef(false)

  const focusEditable = () => {
    containerRef.current?.querySelector<HTMLElement>('[contenteditable="true"]')?.focus()
  }

  useEffect(() => {
    onSaveRef.current = onSave
  }, [onSave])

  useEffect(() => {
    onViewStateChangeRef.current = onViewStateChange
  }, [onViewStateChange])

  useEffect(() => {
    isActiveRef.current = isActive
  }, [isActive])

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

  // Keep editorRef in sync for unmount cleanup (reads from live ref, not stale closure)
  useEffect(() => {
    editorRef.current = editor ?? null
  }, [editor])

  // One-shot ready handler: restore selection + scroll BEFORE focus (AC8, I3)
  useEffect(() => {
    if (!editor) return
    if (didRestoreRef.current) return
    didRestoreRef.current = true
    const vs = initialViewState
    if (vs?.selection) {
      const sel = resolveRestoreSelection(editor.state.doc, vs.selection)
      editor.view.dispatch(editor.state.tr.setSelection(sel))
    }
    if (vs && containerRef.current) {
      containerRef.current.scrollTop = vs.scrollTop
    }
    if (isActiveRef.current) focusEditable()
  }, [editor]) // eslint-disable-line react-hooks/exhaustive-deps

  // Sync external content changes (e.g., reload from disk).
  // Skip the very first render — useEditor already initialized with the content prop.
  useEffect(() => {
    if (!editor) return
    if (!hasInitializedRef.current) {
      hasInitializedRef.current = true
      return
    }
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

  // Save viewState on unmount — useLayoutEffect cleanup runs before safelyDetachRef,
  // so containerRef.current is still valid when we read scrollTop (AC5, M2)
  useLayoutEffect(() => {
    return () => {
      onViewStateChangeRef.current?.({
        scrollTop: containerRef.current?.scrollTop ?? 0,
        selection: editorRef.current
          ? { from: editorRef.current.state.selection.from, to: editorRef.current.state.selection.to }
          : null,
      })
    }
  }, [])

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
