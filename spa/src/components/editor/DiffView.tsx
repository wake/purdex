import { DiffEditor } from '@monaco-editor/react'

interface Props {
  original: string
  modified: string
  language: string
}

export function DiffView({ original, modified, language }: Props) {
  return (
    <DiffEditor
      original={original}
      modified={modified}
      language={language}
      theme="vs-dark"
      options={{
        readOnly: true,
        renderSideBySide: true,
        automaticLayout: true,
        fontSize: 13,
      }}
    />
  )
}
