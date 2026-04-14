interface Props {
  language: string
  line: number
  column: number
}

export function EditorStatusBar({ language, line, column }: Props) {
  return (
    <div className="flex items-center justify-between px-3 py-0.5 border-t border-border-subtle bg-surface-secondary text-[10px] text-text-muted">
      <span>{language}</span>
      <span>Ln {line}, Col {column}</span>
    </div>
  )
}
