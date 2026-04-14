import Editor, { type OnMount } from '@monaco-editor/react'
import { useCallback, useRef } from 'react'
import type { editor } from 'monaco-editor'

interface Props {
  content: string
  language: string
  onChange: (value: string) => void
  onCursorChange: (line: number, column: number) => void
  onSave: () => void
}

export function MonacoWrapper({ content, language, onChange, onCursorChange, onSave }: Props) {
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)

  const handleMount: OnMount = useCallback((ed, monaco) => {
    editorRef.current = ed
    ed.addAction({
      id: 'purdex-save',
      label: 'Save',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS],
      run: () => onSave(),
    })
    ed.onDidChangeCursorPosition((e) => {
      onCursorChange(e.position.lineNumber, e.position.column)
    })
  }, [onSave, onCursorChange])

  return (
    <Editor
      value={content}
      language={language}
      theme="vs-dark"
      onChange={(value) => onChange(value ?? '')}
      onMount={handleMount}
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
