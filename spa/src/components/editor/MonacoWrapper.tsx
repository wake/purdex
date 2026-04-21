import Editor, { type OnMount } from '@monaco-editor/react'
import { useCallback, useEffect, useRef } from 'react'
import type { editor } from 'monaco-editor'

interface Props {
  content: string
  language: string
  modelId: string
  initialViewState: editor.ICodeEditorViewState | null
  onChange: (value: string) => void
  onCursorChange: (line: number, column: number) => void
  onViewStateChange: (viewState: editor.ICodeEditorViewState | null) => void
  onSave: () => void
}

export function MonacoWrapper({ content, language, modelId, initialViewState, onChange, onCursorChange, onViewStateChange, onSave }: Props) {
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)

  const handleMount: OnMount = useCallback((ed, monaco) => {
    editorRef.current = ed
    if (initialViewState) {
      ed.restoreViewState(initialViewState)
    }
    ed.addAction({
      id: 'purdex-save',
      label: 'Save',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS],
      run: () => onSave(),
    })
    ed.onDidChangeCursorPosition((e) => {
      onCursorChange(e.position.lineNumber, e.position.column)
    })
  }, [initialViewState, onSave, onCursorChange])

  useEffect(() => {
    return () => {
      onViewStateChange(editorRef.current?.saveViewState() ?? null)
      editorRef.current = null
    }
  }, [onViewStateChange])

  return (
    <Editor
      path={modelId}
      value={content}
      language={language}
      theme="vs-dark"
      onChange={(value) => onChange(value ?? '')}
      onMount={handleMount}
      keepCurrentModel={true}
      options={{
        minimap: { enabled: true },
        fontSize: 13,
        lineNumbers: 'on',
        wordWrap: 'on',
        scrollBeyondLastLine: false,
        automaticLayout: true,
      }}
    />
  )
}
