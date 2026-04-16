import { DiffEditor } from '@monaco-editor/react'
import { useRef } from 'react'
import type { editor } from 'monaco-editor'

interface Props {
  original: string
  modified: string
  language: string
}

export function DiffView({ original, modified, language }: Props) {
  const editorRef = useRef<editor.IStandaloneDiffEditor | null>(null)

  return (
    <DiffEditor
      original={original}
      modified={modified}
      language={language}
      theme="vs-dark"
      onMount={(ed) => { editorRef.current = ed }}
      options={{
        readOnly: true,
        renderSideBySide: true,
        automaticLayout: true,
        fontSize: 13,
      }}
    />
  )
}
