import { FloppyDisk, GitDiff } from '@phosphor-icons/react'

interface Props {
  filePath: string
  isDirty: boolean
  isMarkdown: boolean
  editorMode: 'raw' | 'wysiwyg'
  showDiff?: boolean
  onSave: () => void
  onToggleMode?: () => void
  onDiff?: () => void
}

export function EditorToolbar({ filePath, isDirty, isMarkdown, editorMode, showDiff, onSave, onToggleMode, onDiff }: Props) {
  const segments = filePath.split('/').filter(Boolean)

  return (
    <div className="flex items-center justify-between px-3 py-1 border-b border-border-subtle bg-surface-secondary">
      <div className="flex items-center gap-1 min-w-0 text-xs text-text-secondary">
        <span className="shrink-0 text-text-muted">/</span>
        {segments.map((segment, index) => (
          <span key={`${segment}-${index}`} className="flex items-center min-w-0">
            {index > 0 && <span className="mx-1 shrink-0 text-text-muted">/</span>}
            <span className="truncate" title={filePath}>{segment}</span>
          </span>
        ))}
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
        {isDirty && onDiff && (
          <button
            onClick={onDiff}
            className={`p-1 rounded hover:bg-surface-hover transition-colors ${showDiff ? 'text-accent-base' : 'text-text-secondary'}`}
            title={showDiff ? 'Close diff' : 'Diff against saved'}
          >
            <GitDiff size={14} />
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
