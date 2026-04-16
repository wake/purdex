import { FloppyDisk } from '@phosphor-icons/react'

interface Props {
  filePath: string
  isDirty: boolean
  isMarkdown: boolean
  editorMode: 'raw' | 'wysiwyg'
  onSave: () => void
  onToggleMode?: () => void
}

export function EditorToolbar({ filePath, isDirty, isMarkdown, editorMode, onSave, onToggleMode }: Props) {
  const fileName = filePath.split('/').pop() ?? filePath

  return (
    <div className="flex items-center justify-between px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="flex items-center gap-2 text-xs text-text-secondary truncate">
        <span className="truncate" title={filePath}>{fileName}</span>
        {isDirty && <span className="text-accent-base" title="Unsaved changes">●</span>}
      </div>
      <div className="flex items-center gap-1">
        {isMarkdown && onToggleMode && (
          <button
            onClick={onToggleMode}
            className="px-2 py-0.5 rounded text-[10px] border border-border-subtle hover:bg-surface-hover text-text-secondary transition-colors"
          >
            {editorMode === 'raw' ? 'WYSIWYG' : 'Raw'}
          </button>
        )}
        <button
          onClick={onSave}
          disabled={!isDirty}
          className="p-1 rounded hover:bg-surface-hover text-text-secondary disabled:opacity-30 transition-colors"
          title="Save (⌘S)"
        >
          <FloppyDisk size={14} />
        </button>
      </div>
    </div>
  )
}
