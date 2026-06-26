import Editor, { type BeforeMount, type OnMount } from '@monaco-editor/react'
import { useCallback, useEffect, useRef } from 'react'
import type { editor } from 'monaco-editor'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'

// Custom dark theme: match VSCode's current-line highlight (subtle
// background tint) instead of Monaco's default thin border. Everything
// else inherits from `vs-dark`.
const PURDEX_THEME_ID = 'purdex-dark'
const PURDEX_THEME_DATA: editor.IStandaloneThemeData = {
  base: 'vs-dark',
  inherit: true,
  rules: [],
  colors: {
    'editor.lineHighlightBackground': '#FFFFFF0D',
    'editor.lineHighlightBorder': '#00000000',
  },
}

interface Props {
  content: string
  language: string
  modelId: string
  isActive: boolean
  initialViewState: editor.ICodeEditorViewState | null
  onChange: (value: string) => void
  onCursorChange: (line: number, column: number) => void
  onViewStateChange: (viewState: editor.ICodeEditorViewState | null) => void
  onSave: () => void
}

export function MonacoWrapper({ content, language, modelId, isActive, initialViewState, onChange, onCursorChange, onViewStateChange, onSave }: Props) {
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)
  const onSaveRef = useRef(onSave)
  const onViewStateChangeRef = useRef(onViewStateChange)
  const isActiveRef = useRef(isActive)
  const tabSize = useEditorSettingsStore((s) => s.tabSize)
  const insertSpaces = useEditorSettingsStore((s) => s.insertSpaces)
  const wordWrap = useEditorSettingsStore((s) => s.wordWrap)
  const lineNumbers = useEditorSettingsStore((s) => s.lineNumbers)
  const minimap = useEditorSettingsStore((s) => s.minimap)
  const fontSize = useEditorSettingsStore((s) => s.fontSize)

  useEffect(() => {
    onSaveRef.current = onSave
  }, [onSave])

  useEffect(() => {
    onViewStateChangeRef.current = onViewStateChange
  }, [onViewStateChange])

  useEffect(() => {
    isActiveRef.current = isActive
  }, [isActive])

  const handleBeforeMount: BeforeMount = useCallback((monaco) => {
    monaco.editor.defineTheme(PURDEX_THEME_ID, PURDEX_THEME_DATA)
  }, [])

  const handleMount: OnMount = useCallback((ed, monaco) => {
    editorRef.current = ed
    if (initialViewState) {
      ed.restoreViewState(initialViewState)
    }
    if (isActiveRef.current) {
      ed.focus()
    }
    ed.addAction({
      id: 'purdex-save',
      label: 'Save',
      keybindings: [monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS],
      run: () => onSaveRef.current(),
    })
    ed.onDidChangeCursorPosition((e) => {
      onCursorChange(e.position.lineNumber, e.position.column)
    })
  }, [initialViewState, onCursorChange])

  useEffect(() => {
    return () => {
      onViewStateChangeRef.current(editorRef.current?.saveViewState() ?? null)
      editorRef.current = null
    }
  }, [])

  useEffect(() => {
    if (!isActive) return
    editorRef.current?.focus()
  }, [isActive])

  return (
    <Editor
      path={modelId}
      value={content}
      language={language}
      theme={PURDEX_THEME_ID}
      beforeMount={handleBeforeMount}
      onChange={(value) => onChange(value ?? '')}
      onMount={handleMount}
      keepCurrentModel={true}
      options={{
        minimap: { enabled: minimap },
        fontSize,
        lineNumbers,
        wordWrap,
        tabSize,
        insertSpaces,
        scrollBeyondLastLine: true,
        automaticLayout: true,
      }}
    />
  )
}
