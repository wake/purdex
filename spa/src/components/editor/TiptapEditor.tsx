import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { NodeSelection } from '@tiptap/pm/state'
import { useEffect, useLayoutEffect, useRef } from 'react'
import { resolveRestoreSelection } from './tiptapSelection'
import type { TiptapViewState } from '../../stores/useEditorStore'
import type { ContentWidthOption } from '../../stores/useEditorSettingsStore'

interface Props {
  content: string // raw markdown
  isActive: boolean
  initialViewState?: TiptapViewState | null
  contentWidth?: ContentWidthOption
  onChange: (markdown: string) => void
  onViewStateChange?: (viewState: TiptapViewState) => void
  onSave: () => void
}

export function TiptapEditor({ content, isActive, initialViewState, contentWidth = 'narrow', onChange, onViewStateChange, onSave }: Props) {
  const onSaveRef = useRef(onSave)
  const containerRef = useRef<HTMLDivElement>(null)
  const isActiveRef = useRef(isActive)
  const didRestoreRef = useRef(false)
  const editorRef = useRef<Editor | null>(null)
  const onViewStateChangeRef = useRef(onViewStateChange)
  const hasInitializedRef = useRef(false)

  const focusEditable = () => {
    // preventScroll: refocusing on tab re-activation (the isActive false→true
    // effect) must NOT scroll the caret into view — that jumps a scrolled
    // document back to the top on every tab switch. Scroll position is owned by
    // the container / restored viewState, never by focus.
    containerRef.current?.querySelector<HTMLElement>('[contenteditable="true"]')?.focus({ preventScroll: true })
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
        class: 'tiptap-editor prose prose-invert max-w-none min-h-full py-4 focus:outline-none',
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

  // Keep editorRef in sync for unmount cleanup. useLayoutEffect (not useEffect):
  // the unmount cleanup below is also a layout effect and reads editorRef +
  // didRestoreRef. If these were passive effects, an unmount right after the
  // editor-ready commit would run the layout cleanup BEFORE these passive effects
  // set the refs (editorRef still null, didRestoreRef still false) — silently
  // dropping the viewState (R2 defense D1).
  useLayoutEffect(() => {
    editorRef.current = editor ?? null
  }, [editor])

  // One-shot ready handler: restore selection + scroll BEFORE focus (AC8, I3).
  // useLayoutEffect so didRestoreRef ownership is set in the same commit phase the
  // unmount cleanup reads from (R2 defense D1).
  useLayoutEffect(() => {
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
  // so containerRef.current is still valid when we read scrollTop (AC5, M2).
  // We intentionally read containerRef.current AT cleanup time (the unmount
  // scrollTop), not a mount-time snapshot — so the exhaustive-deps "copy to a
  // variable" suggestion does not apply here.
  useLayoutEffect(() => {
    return () => {
      // If the editor never became ready (restore never ran), this component
      // never took ownership of the viewState — writing back here would clobber
      // the stored tiptapViewState with scrollTop:0/selection:null (R1 P2).
      if (!didRestoreRef.current) return
      onViewStateChangeRef.current?.({
        // eslint-disable-next-line react-hooks/exhaustive-deps -- read live scrollTop at unmount
        scrollTop: containerRef.current?.scrollTop ?? 0,
        selection: editorRef.current
          ? {
              type: editorRef.current.state.selection instanceof NodeSelection ? 'node' : 'text',
              from: editorRef.current.state.selection.from,
              to: editorRef.current.state.selection.to,
            }
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
      <div
        className={`min-h-full ${
          contentWidth === 'full' ? 'max-w-none px-4' : 'max-w-[52em] mx-auto box-border px-4'
        } [&_.tiptap-editor]:min-h-full [&_.tiptap-editor]:cursor-text`}
      >
        <EditorContent editor={editor} />
      </div>
    </div>
  )
}
