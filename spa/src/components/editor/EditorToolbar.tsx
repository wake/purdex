import { FloppyDisk } from '@phosphor-icons/react'

interface Props {
  filePath: string
  isDirty: boolean
  onSave: () => void
}

export function EditorToolbar({ filePath, isDirty, onSave }: Props) {
  const fileName = filePath.split('/').pop() ?? filePath

  return (
    <div className="flex items-center justify-between px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="flex items-center gap-2 text-xs text-text-secondary truncate">
        <span className="truncate" title={filePath}>{fileName}</span>
        {isDirty && <span className="text-accent-base" title="Unsaved changes">●</span>}
      </div>
      <button
        onClick={onSave}
        disabled={!isDirty}
        className="p-1 rounded hover:bg-surface-hover text-text-secondary disabled:opacity-30 transition-colors"
        title="Save (⌘S)"
      >
        <FloppyDisk size={14} />
      </button>
    </div>
  )
}
